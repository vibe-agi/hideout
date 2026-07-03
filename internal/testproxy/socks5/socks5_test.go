package socks5

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestProxyConnectsToTCPServer(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		conn, err := echo.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		if string(buf) == "ping" {
			_, _ = conn.Write([]byte("pong"))
		}
	}()

	proxy, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = proxy.Serve(ctx)
	}()

	conn, err := net.DialTimeout("tcp", proxy.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatal(err)
	}
	if resp[0] != 0x05 || resp[1] != 0x00 {
		t.Fatalf("method response=%v", resp)
	}

	host, portText, err := net.SplitHostPort(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	req := []byte{0x05, 0x01, 0x00, 0x01}
	req = append(req, net.ParseIP(host).To4()...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	req = append(req, portBytes...)
	if _, err := conn.Write(req); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != 0x00 {
		t.Fatalf("connect reply=%v", reply)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 4)
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatal(err)
	}
	if string(body) != "pong" {
		t.Fatalf("body=%q", body)
	}
}
