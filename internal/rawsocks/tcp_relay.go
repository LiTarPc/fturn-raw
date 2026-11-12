package rawsocks

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"time"
)

func markActivity(last *atomic.Int64, started time.Time) {
	now := time.Since(started).Nanoseconds()
	for previous := last.Load(); now > previous; previous = last.Load() {
		if last.CompareAndSwap(previous, now) {
			return
		}
	}
}

// activityConn keeps idle deadlines effective even when either copy is blocked.
type activityConn struct {
	net.Conn
	started time.Time
	last    *atomic.Int64
}

func (c activityConn) Read(p []byte) (int, error) {
	n, e := c.Conn.Read(p)
	if n > 0 {
		markActivity(c.last, c.started)
	}
	return n, e
}
func (c activityConn) Write(p []byte) (int, error) {
	n, e := c.Conn.Write(p)
	if n > 0 {
		markActivity(c.last, c.started)
	}
	return n, e
}

// Preserve TCP half-close for requests whose response starts after upload EOF,
// while bounding both inactive full-duplex and abandoned half-closed sessions.
func relayTCP(ctx context.Context, client, upstream net.Conn, idle, halfCloseIdle time.Duration) {
	started := time.Now()
	var last atomic.Int64
	var halfClosed atomic.Bool
	stopped := make(chan struct{})
	defer close(stopped)
	closeBoth := func() { client.Close(); upstream.Close() }
	go func() {
		tick := time.NewTicker(min(time.Second, idle, halfCloseIdle))
		defer tick.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-ctx.Done():
				closeBoth()
				return
			case <-tick.C:
				limit := idle
				if halfClosed.Load() {
					limit = halfCloseIdle
				}
				if time.Since(started)-time.Duration(last.Load()) >= limit {
					closeBoth()
					return
				}
			}
		}
	}()
	peerEOF := func(c net.Conn) {
		markActivity(&last, started)
		halfClosed.Store(true)
		if writer, ok := c.(interface{ CloseWrite() error }); ok {
			writer.CloseWrite()
		}
	}
	fromClient := activityConn{client, started, &last}
	fromUpstream := activityConn{upstream, started, &last}
	uploaded := make(chan struct{})
	go func() {
		if _, err := io.Copy(fromUpstream, fromClient); err != nil {
			closeBoth()
		} else {
			peerEOF(upstream)
		}
		close(uploaded)
	}()
	if _, err := io.Copy(fromClient, fromUpstream); err != nil {
		closeBoth()
	} else {
		peerEOF(client)
	}
	<-uploaded
}
