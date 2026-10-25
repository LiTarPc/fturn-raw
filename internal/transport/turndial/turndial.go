// Package turndial инкапсулирует подключение, аутентификацию и аллокацию сессий TURN.
package turndial

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/pion/turn/v5"
	"github.com/samosvalishe/free-turn-proxy/internal/logx"
	"github.com/samosvalishe/free-turn-proxy/internal/netconn"
	"github.com/samosvalishe/free-turn-proxy/internal/netctl"
	"github.com/samosvalishe/free-turn-proxy/internal/randx"
)

// Config задаёт параметры подключения к TURN-серверу.
type Config struct {
	HostOverride   string
	PortOverride   string
	TransportUDP   bool
	DialTimeout    time.Duration
	Log            logx.Logger
	StreamID       int
	RawMaintenance bool
}

// Stream представляет активную TURN-аллокацию.
type Stream struct {
	Relay         net.PacketConn
	ServerUDPAddr *net.UDPAddr
	// PermDead закрывается при стойком провале ChannelBind refresh (relay блэкхолит трафик).
	PermDead <-chan struct{}
	close    func() error
}

// Close освобождает аллокацию, TURN-клиент и транспортное соединение.
func (s *Stream) Close() error {
	if s == nil || s.close == nil {
		return nil
	}
	return s.close()
}

// Open выполняет подключение к TURN-серверу и создаёт релей-соединение.
func Open(ctx context.Context, cfg Config, peer *net.UDPAddr, user, pass, rawAddr string) (*Stream, error) {
	dialTimeout := cfg.DialTimeout
	if dialTimeout == 0 {
		dialTimeout = 5 * time.Second
	}
	openCtx, cancelOpen := context.WithTimeout(ctx, dialTimeout)
	defer cancelOpen()
	urlhost, urlport, err := net.SplitHostPort(rawAddr)
	if err != nil {
		return nil, fmt.Errorf("parse TURN addr: %w", err)
	}
	if cfg.HostOverride != "" {
		urlhost = cfg.HostOverride
	}
	if cfg.PortOverride != "" {
		urlport = cfg.PortOverride
	}
	turnServerAddr := net.JoinHostPort(urlhost, urlport)
	port, err := net.DefaultResolver.LookupPort(openCtx, "udp", urlport)
	if err != nil {
		return nil, fmt.Errorf("resolve TURN port: %w", err)
	}
	ips, err := net.DefaultResolver.LookupIPAddr(openCtx, urlhost)
	if err != nil {
		return nil, fmt.Errorf("resolve TURN addr: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve TURN addr: no addresses")
	}
	turnServerUDPAddr := &net.UDPAddr{IP: ips[0].IP, Zone: ips[0].Zone, Port: port}
	if err != nil {
		return nil, fmt.Errorf("resolve TURN addr: %w", err)
	}
	turnServerAddr = turnServerUDPAddr.String()

	var (
		turnConn  net.PacketConn
		closeConn func() error
	)
	if cfg.TransportUDP {
		raw, derr := (&net.Dialer{Control: netctl.Apply}).DialContext(openCtx, "udp", turnServerUDPAddr.String())
		if derr != nil {
			return nil, fmt.Errorf("dial TURN (udp): %w", derr)
		}
		c, ok := raw.(*net.UDPConn)
		if !ok {
			_ = raw.Close()
			return nil, fmt.Errorf("turndial: expected *net.UDPConn, got %T", raw)
		}
		turnConn = &netconn.ConnectedUDPConn{UDPConn: c}
		closeConn = c.Close
	} else {
		d := net.Dialer{Control: netctl.Apply}
		c, derr := d.DialContext(openCtx, "tcp", turnServerAddr)
		if derr != nil {
			return nil, fmt.Errorf("dial TURN (tcp): %w", derr)
		}
		// Разрезание внутри STUN magic cookie (байты 4-7) ломает DPI сигнатуры без TCP-реассемблинга.
		wrapped := &netconn.SplitFirstWriteConn{Conn: c, SplitAt: 5 + randx.Intn(3), Delay: 20 * time.Millisecond}
		turnConn = turn.NewSTUNConn(wrapped)
		closeConn = c.Close
	}

	turnConn = &boundedTransport{PacketConn: turnConn}

	var addrFamily turn.RequestedAddressFamily
	if peer.IP.To4() != nil {
		addrFamily = turn.RequestedAddressFamilyIPv4
	} else {
		addrFamily = turn.RequestedAddressFamilyIPv6
	}

	// VK отбрасывает CreatePermission refresh с кодом 400; канал поддерживается через ChannelBind.
	permDead := make(chan struct{})
	var permOnce sync.Once
	loggerFactory := &permWatchFactory{
		inner:             &logxFactory{log: cfg.Log, stream: cfg.StreamID, maintenance: cfg.RawMaintenance},
		threshold:         permFailThreshold,
		allocationFailure: cfg.RawMaintenance,
		onDead:            func() { permOnce.Do(func() { close(permDead) }) },
	}
	clientConfig := &turn.ClientConfig{
		STUNServerAddr:            turnServerAddr,
		TURNServerAddr:            turnServerAddr,
		Conn:                      turnConn,
		Net:                       netconn.New(),
		Username:                  user,
		Password:                  pass,
		RequestedAddressFamily:    addrFamily,
		PermissionRefreshInterval: 24 * time.Hour,
		LoggerFactory:             loggerFactory,
	}
	if cfg.RawMaintenance {
		// Stagger workers; refresh permission well before its 300 second expiry.
		clientConfig.BindingRefreshInterval = time.Duration(110+randx.Intn(21)) * time.Second
		clientConfig.BindingCheckInterval = 5 * time.Second
		clientConfig.AllocationRefreshInterval = time.Duration(225+randx.Intn(31)) * time.Second
	}
	client, err := turn.NewClient(clientConfig)
	if err != nil {
		if cerr := closeConn(); cerr != nil {
			err = fmt.Errorf("%w (close: %v)", err, cerr)
		}
		return nil, fmt.Errorf("create TURN client: %w", err)
	}

	// Close the socket before transactions: retransmission may hold the client lock
	// while writing to the transport. This ordering also bounds cancellation.
	var abortOnce sync.Once
	var relayMu sync.Mutex
	var allocated net.PacketConn
	abort := func() {
		abortOnce.Do(func() {
			_ = closeConn()
			client.Close()
			relayMu.Lock()
			r := allocated
			relayMu.Unlock()
			if r != nil {
				_ = r.Close()
			}
		})
	}
	stopParent := context.AfterFunc(ctx, abort)
	stopOpen := context.AfterFunc(openCtx, abort)
	failed := true
	defer func() {
		stopOpen()
		if failed {
			stopParent()
			abort()
		}
	}()
	if err = client.Listen(); err != nil {
		return nil, fmt.Errorf("TURN listen: %w", err)
	}
	relay, err := client.Allocate()
	if err != nil {
		if openCtx.Err() != nil {
			err = openCtx.Err()
		}
		return nil, fmt.Errorf("TURN allocate: %w", err)
	}
	relayMu.Lock()
	allocated = relay
	relayMu.Unlock()
	if !stopOpen() || openCtx.Err() != nil || ctx.Err() != nil {
		abort()
		_ = relay.Close()
		err = openCtx.Err()
		if err == nil {
			err = ctx.Err()
		}
		return nil, err
	}
	// After allocation allow a short goodbye window on normal session shutdown.
	// Allocate cancellation above remains immediate; a blocked write still aborts
	// immediately through deadlineRelay. The fallback also wakes idle readers.
	stopParent()
	stopParent = context.AfterFunc(ctx, func() { time.AfterFunc(250*time.Millisecond, abort) })
	var closeOnce sync.Once
	var closeErr error
	closeStream := func() error {
		closeOnce.Do(func() {
			stopParent()
			// Best-effort deallocation gets a short budget; forced close always follows.
			timer := time.AfterFunc(250*time.Millisecond, abort)
			closeErr = relay.Close()
			abort()
			timer.Stop()
		})
		return closeErr
	}
	guarded := &deadlineRelay{PacketConn: relay, abort: abort}
	failed = false
	return &Stream{Relay: guarded, ServerUDPAddr: turnServerUDPAddr, PermDead: permDead, close: closeStream}, nil
}
