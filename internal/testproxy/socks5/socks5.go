package socks5

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

const (
	version5     = 0x05
	methodNoAuth = 0x00
	methodNone   = 0xff
	cmdConnect   = 0x01
	atypIPv4     = 0x01
	atypDomain   = 0x03
	atypIPv6     = 0x04

	replySuccess     = 0x00
	replyGeneral     = 0x01
	replyCommand     = 0x07
	replyAddress     = 0x08
	handshakeTimeout = 10 * time.Second
	connectTimeout   = 20 * time.Second
)

type Server struct {
	Listener net.Listener
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
	if host == "" {
		host = addr.IP.String()
		if host == "" || addr.IP.IsUnspecified() {
			host = "127.0.0.1"
		}
	}
	return "socks5://" + net.JoinHostPort(host, strconv.Itoa(addr.Port)), nil
}

func (s *Server) Serve(ctx context.Context) error {
	if s == nil || s.Listener == nil {
		return errors.New("socks5 listener is required")
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
		go handleConn(ctx, conn)
	}
}

func handleConn(ctx context.Context, client net.Conn) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(handshakeTimeout))
	reader := bufio.NewReader(client)
	if err := negotiateMethod(reader, client); err != nil {
		return
	}
	targetAddr, err := readConnectRequest(reader, client)
	if err != nil {
		return
	}
	dialer := net.Dialer{Timeout: connectTimeout}
	target, err := dialer.DialContext(ctx, "tcp", targetAddr)
	if err != nil {
		_ = writeReply(client, replyGeneral)
		return
	}
	defer target.Close()
	if err := writeReply(client, replySuccess); err != nil {
		return
	}
	_ = client.SetDeadline(time.Time{})
	copyBoth(client, reader, target)
}

func negotiateMethod(reader *bufio.Reader, client net.Conn) error {
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
	for _, method := range methods {
		if method == methodNoAuth {
			_, err := client.Write([]byte{version5, methodNoAuth})
			return err
		}
	}
	_, _ = client.Write([]byte{version5, methodNone})
	return errors.New("no supported auth method")
}

func readConnectRequest(reader *bufio.Reader, client net.Conn) (string, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return "", err
	}
	if header[0] != version5 {
		_ = writeReply(client, replyGeneral)
		return "", errors.New("unsupported socks version")
	}
	if header[1] != cmdConnect {
		_ = writeReply(client, replyCommand)
		return "", errors.New("unsupported socks command")
	}
	host, err := readAddress(reader, header[3])
	if err != nil {
		_ = writeReply(client, replyAddress)
		return "", err
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		_ = writeReply(client, replyAddress)
		return "", err
	}
	port := binary.BigEndian.Uint16(portBytes)
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
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
