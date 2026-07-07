// Package dns provides a minimal controlled DNS listener for gate tests. It
// records every query it receives and can answer A queries with a fixed
// address. It is a retained fixture: the Gate 3 DNS mediation proof now uses the
// guest-local DoH stub for the forward direction and, for the reverse, queries
// each captured connected-subnet resolver directly and confirms it is
// unreachable after the block — so this listener is no longer the proof
// observation point, though it can still model a controlled resolver on either
// side.
//
// It is a test/lab fixture only and is never part of the product runtime path.
package dns

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	readTimeout = 30 * time.Second
	maxMessage  = 4096
)

// Server listens for DNS queries on both UDP and TCP at one address.
type Server struct {
	udp    *net.UDPConn
	tcp    net.Listener
	answer net.IP

	mu      sync.Mutex
	queries []string
}

// Listen binds UDP and TCP on the same host:port. When answer is non-nil, A
// queries are answered with it so a mediated resolver actually resolves; when
// nil, queries are still recorded but answered with no records.
func Listen(address string, answer net.IP) (*Server, error) {
	tcpLn, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	tcpAddr, ok := tcpLn.Addr().(*net.TCPAddr)
	if !ok {
		_ = tcpLn.Close()
		return nil, errors.New("dns listener is not TCP")
	}
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: tcpAddr.IP, Port: tcpAddr.Port})
	if err != nil {
		_ = tcpLn.Close()
		return nil, err
	}
	return &Server{udp: udpConn, tcp: tcpLn, answer: answer}, nil
}

// Addr returns the TCP listen address (host:port shared with UDP).
func (s *Server) Addr() net.Addr {
	if s == nil || s.tcp == nil {
		return nil
	}
	return s.tcp.Addr()
}

// HostPort returns the host:port the resolver listens on.
func (s *Server) HostPort() (string, error) {
	addr, ok := s.Addr().(*net.TCPAddr)
	if !ok || addr == nil {
		return "", errors.New("dns listener is not TCP")
	}
	host := addr.IP.String()
	if host == "" || addr.IP.IsUnspecified() {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(addr.Port)), nil
}

// Serve handles UDP and TCP queries until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	if s == nil || s.udp == nil || s.tcp == nil {
		return errors.New("dns listener is required")
	}
	var once sync.Once
	closeAll := func() {
		once.Do(func() {
			_ = s.udp.Close()
			_ = s.tcp.Close()
		})
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
		req := make([]byte, n)
		copy(req, buf[:n])
		name := s.record(req)
		if resp := s.buildResponse(req, name); resp != nil {
			_, _ = s.udp.WriteToUDP(resp, addr)
		}
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
		go s.handleTCP(conn)
	}
}

func (s *Server) handleTCP(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(readTimeout))
	lenBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return
	}
	msgLen := binary.BigEndian.Uint16(lenBuf)
	if msgLen == 0 || int(msgLen) > maxMessage {
		return
	}
	req := make([]byte, msgLen)
	if _, err := io.ReadFull(conn, req); err != nil {
		return
	}
	name := s.record(req)
	resp := s.buildResponse(req, name)
	if resp == nil {
		return
	}
	out := make([]byte, 2+len(resp))
	binary.BigEndian.PutUint16(out[:2], uint16(len(resp)))
	copy(out[2:], resp)
	_, _ = conn.Write(out)
}

// record decodes the query name and records it. Returns the recorded name.
func (s *Server) record(msg []byte) string {
	name := parseQName(msg)
	s.mu.Lock()
	s.queries = append(s.queries, name)
	s.mu.Unlock()
	return name
}

// Count returns how many queries the resolver has received.
func (s *Server) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queries)
}

// Names returns a copy of the query names received, for diagnostics.
func (s *Server) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.queries))
	copy(out, s.queries)
	return out
}

// parseQName decodes the first question's QNAME. Returns "" on malformed input.
func parseQName(msg []byte) string {
	if len(msg) < 12 {
		return ""
	}
	pos := 12
	var labels []string
	for pos < len(msg) {
		l := int(msg[pos])
		pos++
		if l == 0 {
			break
		}
		if l&0xc0 != 0 || pos+l > len(msg) {
			return ""
		}
		labels = append(labels, string(msg[pos:pos+l]))
		pos += l
	}
	return strings.Join(labels, ".")
}

// buildResponse echoes the question and, when an answer is configured and the
// query is type A, appends one A record.
func (s *Server) buildResponse(req []byte, _ string) []byte {
	if len(req) < 12 {
		return nil
	}
	pos := 12
	for pos < len(req) {
		l := int(req[pos])
		pos++
		if l == 0 {
			break
		}
		if l&0xc0 != 0 || pos+l > len(req) {
			return nil
		}
		pos += l
	}
	if pos+4 > len(req) {
		return nil
	}
	qtype := binary.BigEndian.Uint16(req[pos : pos+2])
	questionEnd := pos + 4

	resp := make([]byte, questionEnd)
	copy(resp, req[:questionEnd])
	resp[2] = 0x81                             // QR=1, opcode=0, AA=0, TC=0, RD=1
	resp[3] = 0x80                             // RA=1, RCODE=0
	binary.BigEndian.PutUint16(resp[6:8], 0)   // ANCOUNT
	binary.BigEndian.PutUint16(resp[8:10], 0)  // NSCOUNT
	binary.BigEndian.PutUint16(resp[10:12], 0) // ARCOUNT

	if s.answer != nil && qtype == 1 {
		if a := s.answer.To4(); a != nil {
			answer := []byte{0xc0, 0x0c, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x3c, 0x00, 0x04}
			answer = append(answer, a...)
			resp = append(resp, answer...)
			binary.BigEndian.PutUint16(resp[6:8], 1)
		}
	}
	return resp
}
