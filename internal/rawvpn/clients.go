package rawvpn

import (
	"errors"
	"fmt"
	"net/netip"
	"time"
)

const DefaultMaxDevices = 3

func (c ServerConfig) deviceLimit() int {
	if c.MaxDevices == 0 {
		return DefaultMaxDevices
	}
	return c.MaxDevices
}

func clientPool(first netip.Addr, limit int) ([]netip.Addr, error) {
	if limit == 0 {
		limit = DefaultMaxDevices
	}
	if limit < 1 || limit > 64 {
		return nil, errors.New("raw: max devices must be between 1 and 64")
	}
	addresses := make([]netip.Addr, 0, limit)
	for ip := first; len(addresses) < limit; ip = ip.Next() {
		if !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() {
			return nil, errors.New("raw: client address pool must contain unicast IPv4 addresses")
		}
		addresses = append(addresses, ip)
	}
	return addresses, nil
}

// ValidateClientPool checks all server-side client addresses before opening TUN.
// The pool starts at clientAddress; clients still all use clientAddress locally.
func ValidateClientPool(serverAddress netip.Prefix, clientAddress netip.Addr, maxDevices int) error {
	pool, err := clientPool(clientAddress, maxDevices)
	if err != nil {
		return err
	}
	if !serverAddress.IsValid() || !serverAddress.Addr().Is4() || serverAddress.Bits() < 1 || serverAddress.Bits() > 30 {
		return errors.New("raw: server address must be an IPv4 CIDR with prefix 1..30")
	}
	network := serverAddress.Masked().Addr()
	hostMask := uint32(1)<<(32-serverAddress.Bits()) - 1
	first := network.As4()
	broadcast := netip.AddrFrom4([4]byte{first[0] | byte(hostMask>>24), first[1] | byte(hostMask>>16), first[2] | byte(hostMask>>8), first[3] | byte(hostMask)})
	for _, ip := range pool {
		if !serverAddress.Contains(ip) || ip == serverAddress.Addr() || ip == network || ip == broadcast {
			return fmt.Errorf("raw: client pool address %s must be a usable host inside %s and differ from the server", ip, serverAddress)
		}
	}
	return nil
}

// assignAddress runs with mu held, after the client proves its session key.
// Pending handshakes cannot reserve slots. All workers of one process share a slot.
func (s *rawServer) assignAddress(worker *serverSession) (netip.Addr, bool) {
	s.expireLeases(time.Now())
	if lease, ok := s.leases[worker.group]; ok {
		return lease.address, true
	}
	for _, old := range s.sessions {
		if old.group == worker.group && old.address.IsValid() {
			return old.address, true
		}
	}
	pool, err := clientPool(s.cfg.ClientAddress, s.cfg.MaxDevices)
	if err != nil {
		return netip.Addr{}, false
	}
	for _, ip := range pool {
		used := false
		for _, lease := range s.leases {
			if lease.address == ip {
				used = true
				break
			}
		}
		for _, old := range s.sessions {
			if old.address == ip {
				used = true
				break
			}
		}
		if !used {
			return ip, true
		}
	}
	return netip.Addr{}, false
}

// The lease survives loss of all workers, but not a confirmed orderly close.
const deviceLeaseTTL = 90 * time.Second

type deviceLease struct {
	address netip.Addr
	expires time.Time
}

func (s *rawServer) rememberLease(w *serverSession) {
	if !w.address.IsValid() {
		return
	}
	if s.leases == nil {
		s.leases = make(map[[16]byte]deviceLease)
	}
	seen := w.seen
	if seen.IsZero() {
		seen = time.Now()
	}
	lease := s.leases[w.group]
	expires := seen.Add(deviceLeaseTTL)
	if lease.expires.After(expires) {
		expires = lease.expires
	}
	s.leases[w.group] = deviceLease{w.address, expires}
}
func (s *rawServer) expireLeases(now time.Time) {
	for group, lease := range s.leases {
		if !now.Before(lease.expires) {
			delete(s.leases, group)
		}
	}
}
func (s *rawServer) releaseClosedLease(group [16]byte) {
	for _, w := range s.sessions {
		if w.group == group && w.active {
			return
		}
	}
	delete(s.leases, group)
}
func (s *rawServer) removeCurrent(w *serverSession) {
	if s.sessions[w.addr.String()] == w {
		s.remove(w.addr.String())
	}
}
