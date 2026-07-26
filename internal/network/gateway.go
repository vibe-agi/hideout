package network

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	gatewayHandshakeTimeout = 10 * time.Second
	gatewayConnectTimeout   = 15 * time.Second
	gatewayTransitionWait   = 10 * time.Second
	gatewayCredentialBytes  = 24
)

// GatewayBinding is the private guest-facing endpoint for one environment.
// Its credential is control-plane material and must only be written to a
// root-owned guest configuration file; it is never a target environment value.
type GatewayBinding struct {
	ID       string `json:"-"`
	Address  string `json:"-"`
	Username string `json:"-"`
	Password string `json:"-"`
}

func (binding GatewayBinding) ProxyURL(guestHost string) (string, error) {
	host, port, err := net.SplitHostPort(binding.Address)
	if err != nil || strings.TrimSpace(binding.ID) == "" || binding.Username == "" || binding.Password == "" {
		return "", errors.New("environment network gateway binding is incomplete")
	}
	if strings.TrimSpace(guestHost) != "" {
		host = strings.TrimSpace(guestHost)
	}
	if host == "" || port == "" {
		return "", errors.New("environment network gateway address is incomplete")
	}
	u := &url.URL{Scheme: "socks5", Host: net.JoinHostPort(host, port), User: url.UserPassword(binding.Username, binding.Password)}
	return u.String(), nil
}

type gatewayRoute struct {
	fingerprint string
	dial        func(context.Context, string, string) (net.Conn, error)
}

// GatewayObservation is a redacted, monotonic view of one environment
// gateway's traffic. It deliberately contains only protocol-stage counters:
// destinations, proxy addresses, credentials, URLs, and raw errors never enter
// the observation.
type GatewayObservation struct {
	Accepted             uint64
	Authenticated        uint64
	AuthenticationFailed uint64
	RequestParsed        uint64
	RequestRejected      uint64
	RouteMissing         uint64
	UpstreamDialStarted  uint64
	UpstreamDialFailed   uint64
	UpstreamConnected    uint64
}

// Since returns the non-negative counter delta from an earlier observation.
// A registry replacement or counter reset therefore cannot underflow into a
// misleadingly large diagnostic value.
func (current GatewayObservation) Since(previous GatewayObservation) GatewayObservation {
	return GatewayObservation{
		Accepted:             counterDelta(current.Accepted, previous.Accepted),
		Authenticated:        counterDelta(current.Authenticated, previous.Authenticated),
		AuthenticationFailed: counterDelta(current.AuthenticationFailed, previous.AuthenticationFailed),
		RequestParsed:        counterDelta(current.RequestParsed, previous.RequestParsed),
		RequestRejected:      counterDelta(current.RequestRejected, previous.RequestRejected),
		RouteMissing:         counterDelta(current.RouteMissing, previous.RouteMissing),
		UpstreamDialStarted:  counterDelta(current.UpstreamDialStarted, previous.UpstreamDialStarted),
		UpstreamDialFailed:   counterDelta(current.UpstreamDialFailed, previous.UpstreamDialFailed),
		UpstreamConnected:    counterDelta(current.UpstreamConnected, previous.UpstreamConnected),
	}
}

func counterDelta(current, previous uint64) uint64 {
	if current < previous {
		return current
	}
	return current - previous
}

type gatewayEntry struct {
	id       string
	listener net.Listener
	username string
	password string
	route    atomic.Pointer[gatewayRoute]

	accepted             atomic.Uint64
	authenticated        atomic.Uint64
	authenticationFailed atomic.Uint64
	requestParsed        atomic.Uint64
	requestRejected      atomic.Uint64
	routeMissing         atomic.Uint64
	upstreamDialStarted  atomic.Uint64
	upstreamDialFailed   atomic.Uint64
	upstreamConnected    atomic.Uint64

	switchMu sync.Mutex
	connMu   sync.Mutex
	conns    map[net.Conn]struct{}
	closed   chan struct{}
	close    sync.Once
}

// GatewayRegistry owns one authenticated host-loopback SOCKS gateway per
// environment. Route changes replace one immutable dialer pointer: accepted
// connections keep their old route, while connections accepted after the
// replacement use the new route.
type GatewayRegistry struct {
	mu      sync.Mutex
	entries map[string]*gatewayEntry
	listen  func() (net.Listener, error)
}

func NewGatewayRegistry() *GatewayRegistry {
	return &GatewayRegistry{
		entries: make(map[string]*gatewayEntry),
		listen:  func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") },
	}
}

// GatewayChange holds the environment's route-configuration lock until the
// caller commits or rolls back the service transition.
type GatewayChange struct {
	entry    *gatewayEntry
	previous *gatewayRoute
	next     *gatewayRoute
	active   atomic.Bool
	done     atomic.Bool
}

// Activate makes the prepared route visible to connections accepted after this
// call. Stage itself never changes traffic. The environment service controller
// therefore chooses the exact commit point after its old generation has been
// verified or its candidate generation is ready to consume the route.
func (change *GatewayChange) Activate() error {
	if change == nil || change.entry == nil {
		return errors.New("environment network gateway change is unavailable")
	}
	if change.done.Load() {
		return errors.New("environment network gateway change is already resolved")
	}
	if !change.active.CompareAndSwap(false, true) {
		return errors.New("environment network gateway change is already active")
	}
	if !change.entry.route.CompareAndSwap(change.previous, change.next) {
		change.active.Store(false)
		return errors.New("environment network gateway route changed before activation")
	}
	return nil
}

func (change *GatewayChange) Commit() error {
	if change == nil || change.entry == nil {
		return errors.New("environment network gateway change is unavailable")
	}
	if !change.done.CompareAndSwap(false, true) {
		return errors.New("environment network gateway change is already resolved")
	}
	change.entry.switchMu.Unlock()
	return nil
}

func (change *GatewayChange) Rollback() error {
	if change == nil || change.entry == nil {
		return nil
	}
	if !change.done.CompareAndSwap(false, true) {
		return nil
	}
	if change.active.Load() && !change.entry.route.CompareAndSwap(change.next, change.previous) {
		change.entry.switchMu.Unlock()
		return errors.New("environment network gateway route changed during rollback")
	}
	change.entry.switchMu.Unlock()
	return nil
}

// Stage validates and prepares a route while retaining the environment's
// transition lock. It does not change traffic until GatewayChange.Activate.
// upstream is empty for direct host egress.
func (registry *GatewayRegistry) Stage(environmentID, upstream string) (GatewayBinding, *GatewayChange, error) {
	if registry == nil || !environmentIDPattern.MatchString(environmentID) {
		return GatewayBinding{}, nil, errors.New("environment network gateway identity is invalid")
	}
	route, err := buildGatewayRoute(upstream)
	if err != nil {
		return GatewayBinding{}, nil, err
	}
	entry, err := registry.entry(environmentID)
	if err != nil {
		return GatewayBinding{}, nil, err
	}
	if err := entry.lockTransition(); err != nil {
		return GatewayBinding{}, nil, err
	}
	select {
	case <-entry.closed:
		entry.switchMu.Unlock()
		return GatewayBinding{}, nil, errors.New("environment network gateway is closed")
	default:
	}
	previous := entry.route.Load()
	return GatewayBinding{
		ID: entry.id, Address: entry.listener.Addr().String(), Username: entry.username, Password: entry.password,
	}, &GatewayChange{entry: entry, previous: previous, next: route}, nil
}

func (entry *gatewayEntry) lockTransition() error {
	deadline := time.NewTimer(gatewayTransitionWait)
	defer deadline.Stop()
	retry := time.NewTicker(10 * time.Millisecond)
	defer retry.Stop()
	for {
		if entry.switchMu.TryLock() {
			return nil
		}
		select {
		case <-entry.closed:
			return errors.New("environment network gateway is closed")
		case <-deadline.C:
			return errors.New("environment network service transition did not finish within the bounded wait")
		case <-retry.C:
		}
	}
}

func (registry *GatewayRegistry) CloseEnvironment(environmentID string) error {
	if registry == nil {
		return nil
	}
	registry.mu.Lock()
	entry := registry.entries[environmentID]
	delete(registry.entries, environmentID)
	registry.mu.Unlock()
	if entry == nil {
		return nil
	}
	entry.switchMu.Lock()
	defer entry.switchMu.Unlock()
	return entry.shutdown()
}

func (registry *GatewayRegistry) Close() error {
	if registry == nil {
		return nil
	}
	registry.mu.Lock()
	entries := make([]*gatewayEntry, 0, len(registry.entries))
	for id, entry := range registry.entries {
		entries = append(entries, entry)
		delete(registry.entries, id)
	}
	registry.mu.Unlock()
	var result error
	for _, entry := range entries {
		entry.switchMu.Lock()
		result = errors.Join(result, entry.shutdown())
		entry.switchMu.Unlock()
	}
	return result
}

// Observation returns a redacted counter snapshot without blocking gateway
// traffic. The boolean is false when the environment has no live gateway.
func (registry *GatewayRegistry) Observation(environmentID string) (GatewayObservation, bool) {
	if registry == nil {
		return GatewayObservation{}, false
	}
	registry.mu.Lock()
	entry := registry.entries[environmentID]
	registry.mu.Unlock()
	if entry == nil {
		return GatewayObservation{}, false
	}
	return entry.observation(), true
}

func (entry *gatewayEntry) observation() GatewayObservation {
	return GatewayObservation{
		Accepted:             entry.accepted.Load(),
		Authenticated:        entry.authenticated.Load(),
		AuthenticationFailed: entry.authenticationFailed.Load(),
		RequestParsed:        entry.requestParsed.Load(),
		RequestRejected:      entry.requestRejected.Load(),
		RouteMissing:         entry.routeMissing.Load(),
		UpstreamDialStarted:  entry.upstreamDialStarted.Load(),
		UpstreamDialFailed:   entry.upstreamDialFailed.Load(),
		UpstreamConnected:    entry.upstreamConnected.Load(),
	}
}

func (registry *GatewayRegistry) entry(environmentID string) (*gatewayEntry, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if entry := registry.entries[environmentID]; entry != nil {
		return entry, nil
	}
	listener, err := registry.listen()
	if err != nil {
		return nil, fmt.Errorf("listen for environment network gateway: %w", err)
	}
	id, err := randomGatewayValue("gw_", 16)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	username, err := randomGatewayValue("u_", gatewayCredentialBytes)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	password, err := randomGatewayValue("p_", gatewayCredentialBytes)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	entry := &gatewayEntry{
		id: id, listener: listener, username: username, password: password,
		conns: make(map[net.Conn]struct{}), closed: make(chan struct{}),
	}
	registry.entries[environmentID] = entry
	go entry.serve()
	return entry, nil
}

func randomGatewayValue(prefix string, size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(value), nil
}

func buildGatewayRoute(upstream string) (*gatewayRoute, error) {
	upstream = strings.TrimSpace(upstream)
	if upstream == "" {
		dialer := &net.Dialer{Timeout: gatewayConnectTimeout}
		return &gatewayRoute{fingerprint: "direct", dial: dialer.DialContext}, nil
	}
	if err := validateProxyURL(upstream); err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(upstream)
	if strings.EqualFold(parsed.Hostname(), "host.lima.internal") {
		return nil, errors.New("proxy URL is consumed by the host gateway; use 127.0.0.1 for a proxy running on this host")
	}
	fingerprint := sha256.Sum256([]byte(upstream))
	route := &gatewayRoute{fingerprint: hex.EncodeToString(fingerprint[:])}
	switch parsed.Scheme {
	case "socks5", "socks5h":
		route.dial = socks5UpstreamDialer(parsed)
	case "http", "https":
		route.dial = httpConnectUpstreamDialer(parsed)
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", parsed.Scheme)
	}
	return route, nil
}

func (entry *gatewayEntry) serve() {
	for {
		connection, err := entry.listener.Accept()
		if err != nil {
			select {
			case <-entry.closed:
				return
			default:
				continue
			}
		}
		entry.accepted.Add(1)
		entry.track(connection, true)
		route := entry.route.Load()
		go func(route *gatewayRoute) {
			defer entry.track(connection, false)
			entry.handle(connection, route)
		}(route)
	}
}

func (entry *gatewayEntry) handle(client net.Conn, route *gatewayRoute) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(gatewayHandshakeTimeout))
	reader := bufio.NewReader(client)
	if err := authenticateGatewayClient(reader, client, entry.username, entry.password); err != nil {
		entry.authenticationFailed.Add(1)
		return
	}
	entry.authenticated.Add(1)
	command, target, err := readSOCKSRequest(reader)
	if err != nil {
		entry.requestRejected.Add(1)
		_ = writeSOCKSReply(client, 0x01)
		return
	}
	entry.requestParsed.Add(1)
	if command != 0x01 {
		entry.requestRejected.Add(1)
		_ = writeSOCKSReply(client, 0x07)
		return
	}
	if route == nil || route.dial == nil {
		entry.routeMissing.Add(1)
		_ = writeSOCKSReply(client, 0x01)
		return
	}
	entry.upstreamDialStarted.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), gatewayConnectTimeout)
	upstream, err := route.dial(ctx, "tcp", target)
	cancel()
	if err != nil {
		entry.upstreamDialFailed.Add(1)
		_ = writeSOCKSReply(client, 0x01)
		return
	}
	entry.upstreamConnected.Add(1)
	defer upstream.Close()
	if err := writeSOCKSReply(client, 0x00); err != nil {
		return
	}
	_ = client.SetDeadline(time.Time{})
	copyGatewayConnections(client, reader, upstream)
}

func (entry *gatewayEntry) track(connection net.Conn, add bool) {
	entry.connMu.Lock()
	defer entry.connMu.Unlock()
	if add {
		entry.conns[connection] = struct{}{}
	} else {
		delete(entry.conns, connection)
	}
}

func (entry *gatewayEntry) shutdown() error {
	var result error
	entry.close.Do(func() {
		close(entry.closed)
		result = entry.listener.Close()
		entry.connMu.Lock()
		for connection := range entry.conns {
			result = errors.Join(result, connection.Close())
		}
		entry.connMu.Unlock()
	})
	return result
}

func authenticateGatewayClient(reader *bufio.Reader, client net.Conn, username, password string) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil || header[0] != 0x05 || header[1] == 0 {
		return errors.New("invalid SOCKS greeting")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return err
	}
	supported := false
	for _, method := range methods {
		if method == 0x02 {
			supported = true
		}
	}
	if !supported {
		_, _ = client.Write([]byte{0x05, 0xff})
		return errors.New("SOCKS authentication required")
	}
	if _, err := client.Write([]byte{0x05, 0x02}); err != nil {
		return err
	}
	if version, err := reader.ReadByte(); err != nil || version != 0x01 {
		return errors.New("invalid SOCKS authentication request")
	}
	userLength, err := reader.ReadByte()
	if err != nil || userLength == 0 {
		return errors.New("invalid SOCKS username")
	}
	user := make([]byte, int(userLength))
	if _, err := io.ReadFull(reader, user); err != nil {
		return err
	}
	passwordLength, err := reader.ReadByte()
	if err != nil || passwordLength == 0 {
		return errors.New("invalid SOCKS password")
	}
	secret := make([]byte, int(passwordLength))
	if _, err := io.ReadFull(reader, secret); err != nil {
		return err
	}
	if string(user) != username || string(secret) != password {
		_, _ = client.Write([]byte{0x01, 0x01})
		return errors.New("SOCKS authentication failed")
	}
	_, err = client.Write([]byte{0x01, 0x00})
	return err
}

func readSOCKSRequest(reader *bufio.Reader) (byte, string, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil || header[0] != 0x05 || header[2] != 0x00 {
		return 0, "", errors.New("invalid SOCKS request")
	}
	host, err := readSOCKSHost(reader, header[3])
	if err != nil {
		return 0, "", err
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return 0, "", err
	}
	port := int(portBytes[0])<<8 | int(portBytes[1])
	if port == 0 {
		return 0, "", errors.New("invalid SOCKS target port")
	}
	return header[1], net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func readSOCKSHost(reader *bufio.Reader, kind byte) (string, error) {
	switch kind {
	case 0x01:
		value := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", err
		}
		return net.IP(value).String(), nil
	case 0x04:
		value := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", err
		}
		return net.IP(value).String(), nil
	case 0x03:
		length, err := reader.ReadByte()
		if err != nil || length == 0 {
			return "", errors.New("invalid SOCKS target name")
		}
		value := make([]byte, int(length))
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", err
		}
		return string(value), nil
	default:
		return "", errors.New("unsupported SOCKS address type")
	}
}

func writeSOCKSReply(writer io.Writer, status byte) error {
	_, err := writer.Write([]byte{0x05, status, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	return err
}

func socks5UpstreamDialer(proxyURL *url.URL) func(context.Context, string, string) (net.Conn, error) {
	address := proxyURL.Host
	if _, _, err := net.SplitHostPort(address); err != nil {
		address = net.JoinHostPort(address, "1080")
	}
	return func(ctx context.Context, networkName, target string) (net.Conn, error) {
		if networkName != "tcp" {
			return nil, errors.New("environment network gateway supports TCP upstreams only")
		}
		dialer := &net.Dialer{Timeout: gatewayConnectTimeout}
		connection, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, err
		}
		if deadline, ok := ctx.Deadline(); ok {
			_ = connection.SetDeadline(deadline)
		}
		if err := negotiateUpstreamSOCKS(connection, proxyURL.User, target); err != nil {
			_ = connection.Close()
			return nil, err
		}
		_ = connection.SetDeadline(time.Time{})
		return connection, nil
	}
}

func negotiateUpstreamSOCKS(connection net.Conn, credentials *url.Userinfo, target string) error {
	reader := bufio.NewReader(connection)
	methods := []byte{0x00}
	if credentials != nil {
		methods = append(methods, 0x02)
	}
	if _, err := connection.Write(append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		return err
	}
	choice := make([]byte, 2)
	if _, err := io.ReadFull(reader, choice); err != nil || choice[0] != 0x05 || choice[1] == 0xff {
		return errors.New("upstream SOCKS authentication negotiation failed")
	}
	if choice[1] == 0x02 {
		if credentials == nil {
			return errors.New("upstream SOCKS requested unavailable credentials")
		}
		username := credentials.Username()
		password, _ := credentials.Password()
		if len(username) == 0 || len(username) > 255 || len(password) == 0 || len(password) > 255 {
			return errors.New("upstream SOCKS credentials are invalid")
		}
		request := []byte{0x01, byte(len(username))}
		request = append(request, username...)
		request = append(request, byte(len(password)))
		request = append(request, password...)
		if _, err := connection.Write(request); err != nil {
			return err
		}
		response := make([]byte, 2)
		if _, err := io.ReadFull(reader, response); err != nil || response[1] != 0x00 {
			return errors.New("upstream SOCKS credentials were rejected")
		}
	} else if choice[1] != 0x00 {
		return errors.New("upstream SOCKS selected an unsupported authentication method")
	}
	request, err := encodeSOCKSConnect(target)
	if err != nil {
		return err
	}
	if _, err := connection.Write(request); err != nil {
		return err
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(reader, response); err != nil || response[0] != 0x05 || response[1] != 0x00 {
		return errors.New("upstream SOCKS CONNECT failed")
	}
	if _, err := readSOCKSHost(reader, response[3]); err != nil {
		return err
	}
	port := make([]byte, 2)
	_, err = io.ReadFull(reader, port)
	return err
}

func encodeSOCKSConnect(target string) ([]byte, error) {
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("invalid SOCKS CONNECT target")
	}
	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ipv4 := ip.To4(); ipv4 != nil {
			request = append(request, 0x01)
			request = append(request, ipv4...)
		} else {
			request = append(request, 0x04)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, errors.New("invalid SOCKS CONNECT host")
		}
		request = append(request, 0x03, byte(len(host)))
		request = append(request, host...)
	}
	return append(request, byte(port>>8), byte(port)), nil
}

func httpConnectUpstreamDialer(proxyURL *url.URL) func(context.Context, string, string) (net.Conn, error) {
	address := proxyURL.Host
	if _, _, err := net.SplitHostPort(address); err != nil {
		port := "80"
		if proxyURL.Scheme == "https" {
			port = "443"
		}
		address = net.JoinHostPort(address, port)
	}
	return func(ctx context.Context, networkName, target string) (net.Conn, error) {
		if networkName != "tcp" {
			return nil, errors.New("environment network gateway supports TCP upstreams only")
		}
		dialer := &net.Dialer{Timeout: gatewayConnectTimeout}
		connection, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, err
		}
		if proxyURL.Scheme == "https" {
			host, _, _ := net.SplitHostPort(address)
			tlsConnection := tls.Client(connection, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
			if err := tlsConnection.HandshakeContext(ctx); err != nil {
				_ = connection.Close()
				return nil, err
			}
			connection = tlsConnection
		}
		request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
		if proxyURL.User != nil {
			username := proxyURL.User.Username()
			password, _ := proxyURL.User.Password()
			request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+password)))
		}
		if err := request.Write(connection); err != nil {
			_ = connection.Close()
			return nil, err
		}
		reader := bufio.NewReader(connection)
		response, err := http.ReadResponse(reader, request)
		if err != nil {
			_ = connection.Close()
			return nil, err
		}
		if response.StatusCode != http.StatusOK {
			_ = response.Body.Close()
			_ = connection.Close()
			return nil, fmt.Errorf("upstream HTTP CONNECT returned %s", response.Status)
		}
		return &bufferedGatewayConnection{Conn: connection, reader: reader}, nil
	}
}

type bufferedGatewayConnection struct {
	net.Conn
	reader *bufio.Reader
}

func (connection *bufferedGatewayConnection) Read(value []byte) (int, error) {
	return connection.reader.Read(value)
}

func copyGatewayConnections(client net.Conn, clientReader *bufio.Reader, target net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(target, clientReader)
		if tcp, ok := target.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, target)
		if tcp, ok := client.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
}
