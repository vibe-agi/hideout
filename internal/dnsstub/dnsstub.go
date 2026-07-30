// Package dnsstub is a guest-local DNS stub resolver that mediates DNS over the
// privacy path. It receives ordinary UDP/TCP :53 DNS queries and forwards each
// one as DoH (RFC 8484, application/dns-message) to a DoH server reached by IP
// literal. The DoH request is HTTPS (TCP), so it traverses the TUN to tun2socks
// and the SOCKS CONNECT proxy exactly like any other HTTPS connection — no SOCKS
// UDP ASSOCIATE is required, and the connected-subnet resolver stays blocked.
//
// Reaching the DoH server by IP (for example https://1.1.1.1/dns-query) avoids a
// chicken-and-egg dependency on DNS to resolve the resolver's own name.
package dnsstub

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

const (
	maxMessage         = 65535
	dohTimeout         = 15 * time.Second
	readDeadline       = 30 * time.Second
	maxDoHReplyKB      = 64 << 10
	listenPairAttempts = 16
)

// Server forwards DNS queries to a DoH endpoint over HTTPS.
type Server struct {
	udp    *net.UDPConn
	tcp    net.Listener
	dohURL string
	tlsSNI string
	client *http.Client
}

// Listen binds UDP and TCP on address and forwards queries to
// https://<dohServerIP>/dns-query. tlsServerName, when set, is used for TLS SNI
// and certificate verification (a DoH server reached by IP may present a cert
// for its hostname); when empty the IP is used and the server's cert must cover
// the IP.
func Listen(address, dohServerIP, tlsServerName string) (*Server, error) {
	if net.ParseIP(dohServerIP) == nil {
		return nil, fmt.Errorf("doh server %q is not an IP literal", dohServerIP)
	}
	udpConn, tcpLn, err := listenPair(address)
	if err != nil {
		return nil, err
	}
	tlsConf := &tls.Config{MinVersion: tls.VersionTLS12}
	if tlsServerName != "" {
		tlsConf.ServerName = tlsServerName
	}
	return &Server{
		udp:    udpConn,
		tcp:    tcpLn,
		dohURL: "https://" + net.JoinHostPort(dohServerIP, "443") + "/dns-query",
		tlsSNI: tlsServerName,
		client: &http.Client{
			Timeout:   dohTimeout,
			Transport: &http.Transport{TLSClientConfig: tlsConf, ForceAttemptHTTP2: true},
		},
	}, nil
}

func listenPair(address string) (*net.UDPConn, net.Listener, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "0" {
		return listenPairExact(address)
	}
	udpAddress, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, nil, err
	}
	var lastErr error
	for range listenPairAttempts {
		udpConn, err := net.ListenUDP("udp", udpAddress)
		if err != nil {
			return nil, nil, err
		}
		selected, ok := udpConn.LocalAddr().(*net.UDPAddr)
		if !ok {
			_ = udpConn.Close()
			return nil, nil, errors.New("dns stub listener is not UDP")
		}
		tcpLn, err := net.Listen("tcp", selected.String())
		if err == nil {
			return udpConn, tcpLn, nil
		}
		lastErr = err
		_ = udpConn.Close()
	}
	return nil, nil, fmt.Errorf("bind DNS TCP/UDP listener pair: %w", lastErr)
}

func listenPairExact(address string) (*net.UDPConn, net.Listener, error) {
	tcpLn, err := net.Listen("tcp", address)
	if err != nil {
		return nil, nil, err
	}
	tcpAddr, ok := tcpLn.Addr().(*net.TCPAddr)
	if !ok {
		_ = tcpLn.Close()
		return nil, nil, errors.New("dns stub listener is not TCP")
	}
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: tcpAddr.IP, Port: tcpAddr.Port})
	if err != nil {
		_ = tcpLn.Close()
		return nil, nil, err
	}
	return udpConn, tcpLn, nil
}

// Addr returns the TCP listen address (shared with UDP).
func (s *Server) Addr() net.Addr {
	if s == nil || s.tcp == nil {
		return nil
	}
	return s.tcp.Addr()
}

// HostPort returns the host:port the stub listens on.
func (s *Server) HostPort() (string, error) {
	addr, ok := s.Addr().(*net.TCPAddr)
	if !ok || addr == nil {
		return "", errors.New("dns stub listener is not TCP")
	}
	host := addr.IP.String()
	if host == "" || addr.IP.IsUnspecified() {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(addr.Port)), nil
}

// Serve handles UDP and TCP DNS queries until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	if s == nil || s.udp == nil || s.tcp == nil {
		return errors.New("dns stub listener is required")
	}
	closeAll := func() {
		_ = s.udp.Close()
		_ = s.tcp.Close()
	}
	go func() {
		<-ctx.Done()
		closeAll()
	}()
	errCh := make(chan error, 2)
	go func() { errCh <- s.serveUDP(ctx) }()
	go func() { errCh <- s.serveTCP(ctx) }()
	first := <-errCh
	closeAll()
	<-errCh
	if ctx.Err() != nil {
		return nil
	}
	return first
}

func (s *Server) serveUDP(ctx context.Context) error {
	buf := make([]byte, maxMessage)
	for {
		n, addr, err := s.udp.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		query := make([]byte, n)
		copy(query, buf[:n])
		go func() {
			resp, err := s.resolve(ctx, query)
			if err != nil {
				return
			}
			_, _ = s.udp.WriteToUDP(resp, addr)
		}()
	}
}

func (s *Server) serveTCP(ctx context.Context) error {
	for {
		conn, err := s.tcp.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handleTCP(ctx, conn)
	}
}

func (s *Server) handleTCP(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(readDeadline))
	lenBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return
	}
	msgLen := binary.BigEndian.Uint16(lenBuf)
	if msgLen == 0 || int(msgLen) > maxMessage {
		return
	}
	query := make([]byte, msgLen)
	if _, err := io.ReadFull(conn, query); err != nil {
		return
	}
	resp, err := s.resolve(ctx, query)
	if err != nil {
		return
	}
	out := make([]byte, 2+len(resp))
	binary.BigEndian.PutUint16(out[:2], uint16(len(resp)))
	copy(out[2:], resp)
	_, _ = conn.Write(out)
}

// resolve forwards a raw DNS query to the DoH endpoint and returns the raw DNS
// response.
func (s *Server) resolve(ctx context.Context, query []byte) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, dohTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, s.dohURL, bytes.NewReader(query))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("doh status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDoHReplyKB))
}
