package rawvpn

import (
	"context"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// Delay the cancellation setter and the goodbye setter independently. This
// forces the late cancellation deadline to land after the goodbye reset when
// stop() does not join an already-started context.AfterFunc callback.
type gatedCancellationRelay struct {
	net.PacketConn
	cancellationStarted chan struct{}
	releaseCancellation chan struct{}
	goodbyeReset        chan struct{}
	releaseGoodbye      chan struct{}
	mu                  sync.Mutex
	writeDeadline       time.Time
	writes              atomic.Int32
}

func newGatedCancellationRelay() *gatedCancellationRelay {
	return &gatedCancellationRelay{
		cancellationStarted: make(chan struct{}),
		releaseCancellation: make(chan struct{}),
		goodbyeReset:        make(chan struct{}),
		releaseGoodbye:      make(chan struct{}),
	}
}

func (c *gatedCancellationRelay) SetDeadline(deadline time.Time) error {
	close(c.cancellationStarted)
	<-c.releaseCancellation
	c.mu.Lock()
	c.writeDeadline = deadline
	c.mu.Unlock()
	return nil
}

func (c *gatedCancellationRelay) SetWriteDeadline(deadline time.Time) error {
	c.mu.Lock()
	c.writeDeadline = deadline
	c.mu.Unlock()
	close(c.goodbyeReset)
	<-c.releaseGoodbye
	return nil
}

func (c *gatedCancellationRelay) WriteTo(packet []byte, _ net.Addr) (int, error) {
	c.mu.Lock()
	deadline := c.writeDeadline
	c.mu.Unlock()
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		return 0, os.ErrDeadlineExceeded
	}
	c.writes.Add(1)
	return len(packet), nil
}

func TestCancellationDeadlineIsJoinedBeforeGoodbye(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		relay := newGatedCancellationRelay()
		stopAndJoin := stopRelayOnCancel(ctx, relay)
		cancel()
		<-relay.cancellationStarted

		finished := make(chan error, 1)
		go func() {
			stopAndJoin()
			if err := relay.SetWriteDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
				finished <- err
				return
			}
			_, err := relay.WriteTo([]byte{frameClose}, nil)
			finished <- err
		}()

		// Old stop-only code has now reset the goodbye deadline and is blocked
		// before WriteTo. Fixed code is blocked joining the cancellation setter.
		synctest.Wait()
		close(relay.releaseCancellation)
		synctest.Wait()
		<-relay.goodbyeReset
		close(relay.releaseGoodbye)
		synctest.Wait()

		if err := <-finished; err != nil {
			t.Fatalf("late cancellation deadline broke goodbye: %v", err)
		}
		if relay.writes.Load() != 1 {
			t.Fatal("goodbye was not delivered exactly once")
		}
		stopAndJoin() // The deferred early-return guard is a second caller.
	})
}

func TestStoppingCancellationDeadlineTwicePreventsCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		relay := newGatedCancellationRelay()
		stopAndJoin := stopRelayOnCancel(ctx, relay)
		stopAndJoin()
		stopAndJoin() // Must not wait for a callback that was prevented.
		cancel()
		synctest.Wait()
		select {
		case <-relay.cancellationStarted:
			close(relay.releaseCancellation)
			t.Fatal("stopped cancellation callback ran")
		default:
		}
	})
}

type interruptCancellationRelay struct {
	net.PacketConn
	writeStarted chan struct{}
	interrupted  chan struct{}
	once         sync.Once
}

func (c *interruptCancellationRelay) WriteTo([]byte, net.Addr) (int, error) {
	close(c.writeStarted)
	<-c.interrupted
	return 0, os.ErrDeadlineExceeded
}

func (c *interruptCancellationRelay) SetDeadline(time.Time) error {
	c.once.Do(func() { close(c.interrupted) })
	return nil
}

func TestCancellationDeadlineStillInterruptsActiveWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		relay := &interruptCancellationRelay{
			writeStarted: make(chan struct{}),
			interrupted:  make(chan struct{}),
		}
		stopAndJoin := stopRelayOnCancel(ctx, relay)
		result := make(chan error, 1)
		go func() {
			_, err := relay.WriteTo([]byte("blocked data"), nil)
			result <- err
		}()
		<-relay.writeStarted
		cancel()
		synctest.Wait()
		if err := <-result; err != os.ErrDeadlineExceeded {
			t.Fatalf("active write not interrupted by cancellation: %v", err)
		}
		stopAndJoin()
		stopAndJoin()
	})
}

type cancellationInReadDeadlineRelay struct {
	net.PacketConn
	cancel                context.CancelFunc
	joinCancellation      func()
	sets                  int
	reads                 int
	cancellationDeadlines int
	deadline              time.Time
}

func (c *cancellationInReadDeadlineRelay) SetDeadline(deadline time.Time) error {
	c.cancellationDeadlines++
	c.deadline = deadline
	return nil
}
func (c *cancellationInReadDeadlineRelay) SetReadDeadline(deadline time.Time) error {
	c.sets++
	if c.cancel != nil {
		c.cancel()
		// Wait for the real AfterFunc callback to finish setting the expired
		// cancellation deadline, then overwrite it with the future read deadline.
		c.joinCancellation()
	}
	c.deadline = deadline
	return nil
}
func (c *cancellationInReadDeadlineRelay) ReadFrom(buffer []byte) (int, net.Addr, error) {
	c.reads++
	buffer[0] = 1
	return 1, &net.UDPAddr{}, nil
}
func TestReadClientPacketChecksCancellationAfterDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	relay := &cancellationInReadDeadlineRelay{cancel: cancel}
	relay.joinCancellation = stopRelayOnCancel(ctx, relay)
	defer relay.joinCancellation()
	deadline := time.Now().Add(35 * time.Second)
	n, addr, err := readClientPacket(ctx, relay, make([]byte, 1), deadline)
	if err != context.Canceled || n != 0 || addr != nil || relay.reads != 0 {
		t.Fatalf("ReadFrom entered after setter cancelled context: reads=%d n=%d addr=%v err=%v", relay.reads, n, addr, err)
	}
	if relay.sets != 1 || relay.cancellationDeadlines != 1 || !relay.deadline.Equal(deadline) {
		t.Fatal("read deadline was not installed")
	}
}
func TestReadClientPacketStillReadsLiveContext(t *testing.T) {
	relay := &cancellationInReadDeadlineRelay{}
	buffer := make([]byte, 1)
	n, addr, err := readClientPacket(context.Background(), relay, buffer, time.Now().Add(35*time.Second))
	if err != nil || n != 1 || addr == nil || relay.reads != 1 || buffer[0] != 1 {
		t.Fatalf("live reader was suppressed: reads=%d n=%d addr=%v err=%v", relay.reads, n, addr, err)
	}
}
