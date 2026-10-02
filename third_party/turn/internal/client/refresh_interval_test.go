package client

import (
	"github.com/pion/logging"
	"github.com/pion/stun/v3"
	"net"
	"strings"
	"testing"
	"time"
)

func TestEarlierAllocationRefresh(t *testing.T) {
	for _, v := range []struct{ life, want, expected time.Duration }{{600 * time.Second, 0, 300 * time.Second}, {600 * time.Second, 240 * time.Second, 240 * time.Second}, {60 * time.Second, 240 * time.Second, 30 * time.Second}} {
		if got := allocationRefreshInterval(v.life, v.want); got != v.expected {
			t.Fatalf("got %s want %s", got, v.expected)
		}
	}
}
func TestRefreshErrorResponseIsNotSuccess(t *testing.T) {
	for _, code := range []stun.ErrorCode{stun.CodeBadRequest, stun.CodeUnauthorized, stun.CodeAllocMismatch} {
		a := allocation{client: &mockClient{performTransaction: func(*stun.Message, net.Addr, bool) (TransactionResult, error) {
			return TransactionResult{Msg: stun.MustBuild(stun.TransactionID, stun.NewType(stun.MethodRefresh, stun.ClassErrorResponse), stun.ErrorCodeAttribute{Code: code, Reason: []byte("rejected")})}, nil
		}}, log: logging.NewDefaultLoggerFactory().NewLogger("test"), username: stun.Username("u"), realm: stun.Realm("r"), _nonce: stun.NewNonce("n"), integrity: stun.NewLongTermIntegrity("u", "r", "p")}
		if err := a.refreshAllocation(600*time.Second, false); err == nil || !strings.Contains(err.Error(), "code=") {
			t.Fatalf("rejection %d reported as success: %v", code, err)
		}
	}
}
