package socks5

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestHTTPConnectDialerCarriesTCPThroughAuthenticatedProxy(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	go func() {
		conn, acceptErr := target.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()

	authCh := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		authCh <- r.Header.Get("Proxy-Authorization")
		upstream, dialErr := net.Dial("tcp", r.Host)
		if dialErr != nil {
			http.Error(w, dialErr.Error(), http.StatusBadGateway)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			upstream.Close()
			http.Error(w, "hijacking unavailable", http.StatusInternalServerError)
			return
		}
		client, rw, hijackErr := hijacker.Hijack()
		if hijackErr != nil {
			upstream.Close()
			return
		}
		_, _ = fmt.Fprint(rw, "HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = rw.Flush()
		go func() {
			defer client.Close()
			defer upstream.Close()
			go func() { _, _ = io.Copy(upstream, rw) }()
			_, _ = io.Copy(client, upstream)
		}()
	}))
	t.Cleanup(proxy.Close)

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxyURL.User = url.UserPassword("gate", "secret")
	dialContext, err := HTTPConnectDialer(proxyURL.String())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dialContext(ctx, "tcp", target.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q", buf)
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("gate:secret"))
	select {
	case got := <-authCh:
		if got != wantAuth {
			t.Fatalf("Proxy-Authorization = %q, want %q", got, wantAuth)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not observe CONNECT authorization")
	}
}
