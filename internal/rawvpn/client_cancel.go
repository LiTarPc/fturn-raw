package rawvpn

import (
	"context"
	"net"
	"sync"
	"time"
)

// stopRelayOnCancel interrupts blocked relay I/O on cancellation. Its returned
// function stops an unstarted callback or joins a started one before deadlines
// may be reused for the best-effort goodbye. Repeated calls are safe.
func stopRelayOnCancel(ctx context.Context, relay net.PacketConn) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		_ = relay.SetDeadline(time.Now())
	})
	return sync.OnceFunc(func() {
		if !stop() {
			<-done
		}
	})
}

// readClientPacket checks cancellation after installing the read deadline. A
// reader that passed a pre-check could otherwise overwrite the cancellation
// deadline with a future one and postpone goodbye until TURN's forced abort.
func readClientPacket(ctx context.Context, relay net.PacketConn, buffer []byte, deadline time.Time) (int, net.Addr, error) {
	_ = relay.SetReadDeadline(deadline)
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	return relay.ReadFrom(buffer)
}
