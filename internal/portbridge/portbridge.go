package portbridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
)

type Direction string

const (
	DirectionGuestToHost Direction = "guest-to-host"
	DirectionHostToGuest Direction = "host-to-guest"
)

type ListenScope string

const (
	ListenScopeLoopback       ListenScope = "loopback"
	ListenScopeGuestReachable ListenScope = "guest-reachable"
)

type Lifetime string

const (
	LifetimeRun Lifetime = "run"
)

type TargetScope string

const (
	TargetScopeGuest TargetScope = "guest"
)

type EndpointCategory string

const (
	EndpointCategoryHostLoopback EndpointCategory = "host-loopback"
)

type Spec struct {
	ID               string
	Owner            string
	Action           string
	Source           string
	ClosePolicy      string
	Lifetime         Lifetime
	Direction        Direction
	ListenScope      ListenScope
	ListenAddress    string
	TargetScope      TargetScope
	TargetAddress    string
	EndpointCategory EndpointCategory
}

type Bridge struct {
	spec     Spec
	listener net.Listener
	wg       sync.WaitGroup
	once     sync.Once
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
}

func Start(ctx context.Context, spec Spec) (*Bridge, error) {
	if err := Validate(spec); err != nil {
		return nil, err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", spec.ListenAddress)
	if err != nil {
		return nil, err
	}
	b := &Bridge{spec: spec, listener: ln, conns: map[net.Conn]struct{}{}}
	go func() {
		<-ctx.Done()
		_ = b.Close()
	}()
	b.wg.Add(1)
	go b.accept()
	return b, nil
}

func Validate(spec Spec) error {
	switch spec.Direction {
	case DirectionGuestToHost, DirectionHostToGuest:
	default:
		return fmt.Errorf("port bridge direction %q is unsupported", spec.Direction)
	}
	scope := spec.ListenScope
	if scope == "" {
		scope = ListenScopeLoopback
	}
	switch scope {
	case ListenScopeLoopback:
	case ListenScopeGuestReachable:
		return errors.New("guest-reachable port bridge requires backend-specific policy and is not implemented")
	default:
		return fmt.Errorf("port bridge listen scope %q is unsupported", scope)
	}
	listenHost, _, err := net.SplitHostPort(spec.ListenAddress)
	if err != nil {
		return fmt.Errorf("port bridge listen address is invalid: %w", err)
	}
	if !isLoopbackHost(listenHost) {
		return errors.New("loopback port bridge must listen on localhost or a loopback IP")
	}
	targetHost, _, err := net.SplitHostPort(spec.TargetAddress)
	if err != nil {
		return fmt.Errorf("port bridge target address is invalid: %w", err)
	}
	if isUnspecifiedHost(targetHost) {
		return errors.New("port bridge target must be an explicit host, not a wildcard address")
	}
	return nil
}

func ValidateHostToGuestProduct(spec Spec) error {
	if strings.TrimSpace(spec.ID) == "" {
		return errors.New("host-to-guest port bridge ID is required")
	}
	if !validProductLabel(spec.ID) {
		return errors.New("host-to-guest port bridge ID contains unsupported characters")
	}
	if strings.TrimSpace(spec.Owner) == "" {
		return errors.New("host-to-guest port bridge owner is required")
	}
	if !validProductLabel(spec.Owner) {
		return errors.New("host-to-guest port bridge owner contains unsupported characters")
	}
	if spec.Direction != DirectionHostToGuest {
		return fmt.Errorf("host-to-guest product port bridge requires direction %q", DirectionHostToGuest)
	}
	if spec.Lifetime != LifetimeRun {
		return fmt.Errorf("host-to-guest product port bridge requires lifetime %q", LifetimeRun)
	}
	if spec.ListenScope != ListenScopeLoopback {
		return fmt.Errorf("host-to-guest product port bridge requires listen scope %q", ListenScopeLoopback)
	}
	if spec.TargetScope != TargetScopeGuest {
		return fmt.Errorf("host-to-guest product port bridge requires target scope %q", TargetScopeGuest)
	}
	if spec.EndpointCategory != EndpointCategoryHostLoopback {
		return fmt.Errorf("host-to-guest product port bridge requires endpoint category %q", EndpointCategoryHostLoopback)
	}
	if err := Validate(spec); err != nil {
		return err
	}
	targetHost, _, err := net.SplitHostPort(spec.TargetAddress)
	if err != nil {
		return fmt.Errorf("port bridge target address is invalid: %w", err)
	}
	if !isLoopbackHost(targetHost) {
		return errors.New("host-to-guest product port bridge target must be guest loopback")
	}
	return nil
}

func (b *Bridge) ListenAddress() string {
	if b == nil || b.listener == nil {
		return ""
	}
	return b.listener.Addr().String()
}

func (b *Bridge) Close() error {
	if b == nil {
		return nil
	}
	var err error
	b.once.Do(func() {
		if b.listener != nil {
			err = b.listener.Close()
		}
		b.closeTracked()
		b.wg.Wait()
	})
	return err
}

func (b *Bridge) accept() {
	defer b.wg.Done()
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			return
		}
		b.wg.Add(1)
		go b.handle(conn)
	}
}

func (b *Bridge) handle(inbound net.Conn) {
	defer b.wg.Done()
	b.track(inbound)
	defer inbound.Close()
	defer b.untrack(inbound)
	outbound, err := net.Dial("tcp", b.spec.TargetAddress)
	if err != nil {
		return
	}
	b.track(outbound)
	defer outbound.Close()
	defer b.untrack(outbound)
	var copies sync.WaitGroup
	copies.Add(2)
	go proxyCopy(&copies, outbound, inbound)
	go proxyCopy(&copies, inbound, outbound)
	copies.Wait()
}

func (b *Bridge) track(conn net.Conn) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.conns[conn] = struct{}{}
}

func (b *Bridge) untrack(conn net.Conn) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.conns, conn)
}

func (b *Bridge) closeTracked() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for conn := range b.conns {
		_ = conn.Close()
	}
}

func proxyCopy(wg *sync.WaitGroup, dst, src net.Conn) {
	defer wg.Done()
	_, _ = io.Copy(dst, src)
	_ = dst.Close()
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isUnspecifiedHost(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

func validProductLabel(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == ':':
		default:
			return false
		}
	}
	return true
}
