package socks5

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const (
	version5               = 0x05
	methodNoAuth           = 0x00
	methodUsernamePassword = 0x02
	methodNone             = 0xff
	authVersion            = 0x01
	authSuccess            = 0x00
	authFailure            = 0x01
	cmdConnect             = 0x01
	cmdUDPAssociate        = 0x03
	atypIPv4               = 0x01
	atypDomain             = 0x03
	atypIPv6               = 0x04

	replySuccess     = 0x00
	replyGeneral     = 0x01
	replyCommand     = 0x07
	replyAddress     = 0x08
	handshakeTimeout = 10 * time.Second
	connectTimeout   = 20 * time.Second
	udpReplyTimeout  = 10 * time.Second
)

type Server struct {
	Listener net.Listener
	// Username and Password enable RFC 1929 username/password authentication
	// when both are non-empty. They are intentionally held only in memory by
	// gate fixtures and never included in trace output.
	Username string
	Password string
	// DialContext optionally supplies the fixture's host-side egress. Production
	// gates use it to chain through an operator's HTTP CONNECT proxy when the
	// host cannot reach public resolver IPs directly.
	DialContext func(context.Context, string, string) (net.Conn, error)
	// Trace, when set, receives one line per connection event. Every failure
	// path in handleConn returns silently, so without it a gate cannot tell a
	// guest that never reached the proxy from one whose CONNECT was refused.
	Trace func(string)

	mu      sync.Mutex
	targets []string
}

func (s *Server) trace(format string, args ...any) {
	if s.Trace == nil {
		return
	}
	s.Trace(fmt.Sprintf(format, args...))
}

func Listen(address string) (*Server, error) {
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	return &Server{Listener: ln}, nil
}

func (s *Server) Addr() net.Addr {
	if s == nil || s.Listener == nil {
		return nil
	}
	return s.Listener.Addr()
}

func (s *Server) URL(host string) (string, error) {
	addr, ok := s.Addr().(*net.TCPAddr)
	if !ok || addr == nil {
		return "", errors.New("socks5 listener is not TCP")
	}
	authenticated, err := s.authenticated()
	if err != nil {
		return "", err
	}
	if host == "" {
		host = addr.IP.String()
		if host == "" || addr.IP.IsUnspecified() {
			host = "127.0.0.1"
		}
	}
	proxyURL := &url.URL{
		Scheme: "socks5",
		Host:   net.JoinHostPort(host, strconv.Itoa(addr.Port)),
	}
	if authenticated {
		proxyURL.User = url.UserPassword(s.Username, s.Password)
	}
	return proxyURL.String(), nil
}

func (s *Server) Serve(ctx context.Context) error {
	if s == nil || s.Listener == nil {
		return errors.New("socks5 listener is required")
	}
	if _, err := s.authenticated(); err != nil {
		return err
	}
	var once sync.Once
	closeListener := func() {
		once.Do(func() {
			_ = s.Listener.Close()
		})
	}
	go func() {
		<-ctx.Done()
		closeListener()
	}()
	for {
		conn, err := s.Listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handleConn(ctx, conn)
	}
}

// Targets returns a copy of the CONNECT target host:ports the proxy has been
// asked to reach. It is the privacy-path observation point for the DNS
// mediation forward proof: a DNS-over-TCP query to the mediated resolver
// appears here as a CONNECT to that resolver's address.
func (s *Server) Targets() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.targets))
	copy(out, s.targets)
	return out
}

func (s *Server) recordTarget(addr string) {
	s.mu.Lock()
	s.targets = append(s.targets, addr)
	s.mu.Unlock()
}

func (s *Server) handleConn(ctx context.Context, client net.Conn) {
	defer client.Close()
	s.trace("accepted")
	_ = client.SetDeadline(time.Now().Add(handshakeTimeout))
	reader := bufio.NewReader(client)
	if err := s.negotiateMethod(reader, client); err != nil {
		s.trace("method_negotiation_failed")
		return
	}
	cmd, targetAddr, err := readRequest(reader, client)
	if err != nil {
		s.trace("request_read_failed")
		return
	}
	if cmd == cmdUDPAssociate {
		s.handleUDPAssociate(ctx, client)
		return
	}
	if cmd != cmdConnect {
		_ = writeReply(client, replyCommand)
		return
	}
	s.recordTarget(targetAddr)
	dialContext := s.DialContext
	if dialContext == nil {
		dialer := net.Dialer{Timeout: connectTimeout}
		dialContext = dialer.DialContext
	}
	s.trace("connect_started")
	target, err := dialContext(ctx, "tcp", targetAddr)
	if err != nil {
		s.trace("connect_failed")
		_ = writeReply(client, replyGeneral)
		return
	}
	s.trace("connect_established")
	defer target.Close()
	if err := writeReply(client, replySuccess); err != nil {
		return
	}
	_ = client.SetDeadline(time.Time{})
	copyBoth(client, reader, target)
}

func (s *Server) authenticated() (bool, error) {
	usernameSet := s.Username != ""
	passwordSet := s.Password != ""
	if usernameSet != passwordSet {
		return false, errors.New("socks5 username and password must both be set")
	}
	if len(s.Username) > 255 || len(s.Password) > 255 {
		return false, errors.New("socks5 username and password must not exceed 255 bytes")
	}
	return usernameSet, nil
}

func (s *Server) negotiateMethod(reader *bufio.Reader, client net.Conn) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	if header[0] != version5 {
		return errors.New("unsupported socks version")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return err
	}
	authenticated, err := s.authenticated()
	if err != nil {
		return err
	}
	for _, method := range methods {
		if authenticated && method == methodUsernamePassword {
			if _, err := client.Write([]byte{version5, methodUsernamePassword}); err != nil {
				return err
			}
			return authenticateUsernamePassword(
				reader,
				client,
				s.Username,
				s.Password,
			)
		}
		if !authenticated && method == methodNoAuth {
			_, err := client.Write([]byte{version5, methodNoAuth})
			return err
		}
	}
	_, _ = client.Write([]byte{version5, methodNone})
	return errors.New("no supported auth method")
}

func authenticateUsernamePassword(
	reader *bufio.Reader,
	client net.Conn,
	username string,
	password string,
) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	if header[0] != authVersion || header[1] == 0 {
		_, _ = client.Write([]byte{authVersion, authFailure})
		return errors.New("invalid socks5 username/password request")
	}
	providedUsername := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, providedUsername); err != nil {
		return err
	}
	passwordLength, err := reader.ReadByte()
	if err != nil || passwordLength == 0 {
		_, _ = client.Write([]byte{authVersion, authFailure})
		return errors.New("invalid socks5 username/password request")
	}
	providedPassword := make([]byte, int(passwordLength))
	if _, err := io.ReadFull(reader, providedPassword); err != nil {
		return err
	}
	usernameMatches := subtle.ConstantTimeCompare(
		providedUsername,
		[]byte(username),
	)
	passwordMatches := subtle.ConstantTimeCompare(
		providedPassword,
		[]byte(password),
	)
	if usernameMatches != 1 || passwordMatches != 1 {
		_, _ = client.Write([]byte{authVersion, authFailure})
		return errors.New("socks5 username/password authentication failed")
	}
	if _, err := client.Write([]byte{authVersion, authSuccess}); err != nil {
		return err
	}
	return nil
}

func readRequest(reader *bufio.Reader, client net.Conn) (byte, string, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, "", err
	}
	if header[0] != version5 {
		_ = writeReply(client, replyGeneral)
		return 0, "", errors.New("unsupported socks version")
	}
	host, err := readAddress(reader, header[3])
	if err != nil {
		_ = writeReply(client, replyAddress)
		return 0, "", err
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		_ = writeReply(client, replyAddress)
		return 0, "", err
	}
	port := binary.BigEndian.Uint16(portBytes)
	return header[1], net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func readAddress(reader *bufio.Reader, atyp byte) (string, error) {
	switch atyp {
	case atypIPv4:
		buf := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", err
		}
		return net.IP(buf).String(), nil
	case atypIPv6:
		buf := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", err
		}
		return net.IP(buf).String(), nil
	case atypDomain:
		length, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		if length == 0 {
			return "", errors.New("empty domain")
		}
		buf := make([]byte, int(length))
		if _, err := io.ReadFull(reader, buf); err != nil {
			return "", err
		}
		return string(buf), nil
	default:
		return "", fmt.Errorf("unsupported address type %d", atyp)
	}
}

func writeReply(w io.Writer, code byte) error {
	_, err := w.Write([]byte{version5, code, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0})
	return err
}

func writeReplyAddr(w io.Writer, code byte, ip net.IP, port int) error {
	reply := []byte{version5, code, 0x00}
	if v4 := ip.To4(); v4 != nil {
		reply = append(reply, atypIPv4)
		reply = append(reply, v4...)
	} else {
		reply = append(reply, atypIPv6)
		reply = append(reply, ip.To16()...)
	}
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	reply = append(reply, portBytes...)
	_, err := w.Write(reply)
	return err
}

// handleUDPAssociate implements SOCKS5 UDP ASSOCIATE: it binds a UDP relay
// socket, returns its address to the client, and relays each datagram to its
// destination and one response back. This lets tun2socks forward UDP DNS
// through the proxy so a mediated resolver is reachable over the privacy path.
// It records each datagram's destination as an observed target, so the forward
// DNS proof can assert the mediated resolver was reached.
func (s *Server) handleUDPAssociate(ctx context.Context, client net.Conn) {
	host := "127.0.0.1"
	if tcp, ok := client.LocalAddr().(*net.TCPAddr); ok && tcp.IP != nil {
		host = tcp.IP.String()
	}
	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(host), Port: 0})
	if err != nil {
		_ = writeReply(client, replyGeneral)
		return
	}
	defer relay.Close()
	bnd := relay.LocalAddr().(*net.UDPAddr)
	if err := writeReplyAddr(client, replySuccess, bnd.IP, bnd.Port); err != nil {
		return
	}
	_ = client.SetDeadline(time.Time{})
	go func() {
		<-ctx.Done()
		_ = relay.Close()
	}()
	// The UDP association lives as long as the TCP control connection; when the
	// client closes it, tear down the relay.
	go func() {
		_, _ = io.Copy(io.Discard, client)
		_ = relay.Close()
	}()
	s.relayUDP(relay)
}

func (s *Server) relayUDP(relay *net.UDPConn) {
	buf := make([]byte, 64*1024)
	for {
		n, clientAddr, err := relay.ReadFromUDP(buf)
		if err != nil {
			return
		}
		host, port, data, ok := parseUDPRequest(buf[:n])
		if !ok {
			continue
		}
		s.recordTarget(net.JoinHostPort(host, strconv.Itoa(port)))
		resp, err := forwardUDP(host, port, data)
		if err != nil {
			continue
		}
		_, _ = relay.WriteToUDP(encapUDP(host, port, resp), clientAddr)
	}
}

// parseUDPRequest decodes a SOCKS5 UDP request header (RSV, FRAG, ATYP, DST) and
// returns the destination and payload. Fragmented datagrams (FRAG != 0) are
// rejected.
func parseUDPRequest(msg []byte) (string, int, []byte, bool) {
	if len(msg) < 4 || msg[2] != 0 {
		return "", 0, nil, false
	}
	pos := 4
	var host string
	switch msg[3] {
	case atypIPv4:
		if len(msg) < pos+net.IPv4len+2 {
			return "", 0, nil, false
		}
		host = net.IP(msg[pos : pos+net.IPv4len]).String()
		pos += net.IPv4len
	case atypIPv6:
		if len(msg) < pos+net.IPv6len+2 {
			return "", 0, nil, false
		}
		host = net.IP(msg[pos : pos+net.IPv6len]).String()
		pos += net.IPv6len
	case atypDomain:
		if len(msg) < pos+1 {
			return "", 0, nil, false
		}
		l := int(msg[pos])
		pos++
		if len(msg) < pos+l+2 {
			return "", 0, nil, false
		}
		host = string(msg[pos : pos+l])
		pos += l
	default:
		return "", 0, nil, false
	}
	port := int(binary.BigEndian.Uint16(msg[pos : pos+2]))
	pos += 2
	return host, port, msg[pos:], true
}

// encapUDP wraps payload in a SOCKS5 UDP response header addressed from host:port.
func encapUDP(host string, port int, payload []byte) []byte {
	out := []byte{0x00, 0x00, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			out = append(out, atypIPv4)
			out = append(out, v4...)
		} else {
			out = append(out, atypIPv6)
			out = append(out, ip.To16()...)
		}
	} else {
		out = append(out, atypDomain, byte(len(host)))
		out = append(out, []byte(host)...)
	}
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	out = append(out, portBytes...)
	return append(out, payload...)
}

func forwardUDP(host string, port int, data []byte) ([]byte, error) {
	conn, err := net.DialTimeout("udp", net.JoinHostPort(host, strconv.Itoa(port)), connectTimeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.Write(data); err != nil {
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(udpReplyTimeout))
	resp := make([]byte, 64*1024)
	n, err := conn.Read(resp)
	if err != nil {
		return nil, err
	}
	return resp[:n], nil
}

func copyBoth(client net.Conn, clientReader *bufio.Reader, target net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(target, clientReader)
		closeWrite(target)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, target)
		closeWrite(client)
		done <- struct{}{}
	}()
	<-done
	_ = client.Close()
	_ = target.Close()
	<-done
}

func closeWrite(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
}
