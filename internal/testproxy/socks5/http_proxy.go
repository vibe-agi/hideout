package socks5

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPConnectDialer returns a TCP dial function that reaches targets through a
// fixed HTTP CONNECT proxy. It exists for real-gate fixtures; target processes
// never receive the host proxy URL or credentials.
func HTTPConnectDialer(rawURL string) (func(context.Context, string, string) (net.Conn, error), error) {
	proxyURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || proxyURL.Scheme != "http" || proxyURL.Hostname() == "" {
		return nil, errors.New("SOCKS fixture upstream proxy must be an http URL with a host")
	}
	proxyAddress := proxyURL.Host
	if proxyURL.Port() == "" {
		proxyAddress = net.JoinHostPort(proxyURL.Hostname(), "80")
	}
	authorization := ""
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		credentials := proxyURL.User.Username() + ":" + password
		authorization = "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(credentials)) + "\r\n"
	}

	return func(ctx context.Context, network, targetAddress string) (net.Conn, error) {
		if network != "tcp" {
			return nil, fmt.Errorf("HTTP CONNECT upstream does not support network %q", network)
		}
		dialer := net.Dialer{Timeout: connectTimeout}
		conn, err := dialer.DialContext(ctx, "tcp", proxyAddress)
		if err != nil {
			return nil, err
		}
		deadline := time.Now().Add(connectTimeout)
		_ = conn.SetDeadline(deadline)
		if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Connection: Keep-Alive\r\n%s\r\n", targetAddress, targetAddress, authorization); err != nil {
			_ = conn.Close()
			return nil, err
		}
		reader := bufio.NewReader(conn)
		resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			_ = conn.Close()
			return nil, fmt.Errorf("HTTP CONNECT upstream returned %s", resp.Status)
		}
		_ = conn.SetDeadline(time.Time{})
		return &bufferedConn{Conn: conn, reader: reader}, nil
	}, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}
