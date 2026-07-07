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

	// The CONNECT target is recorded as the privacy-path observation point for
	// the DNS mediation forward proof.
	targets := proxy.Targets()
	if len(targets) != 1 {
		t.Fatalf("Targets()=%v, want one recorded target", targets)
	}
	wantTarget := net.JoinHostPort(host, portText)
	if targets[0] != wantTarget {
		t.Fatalf("Targets()[0]=%q, want %q", targets[0], wantTarget)
	}
}

func TestFreshProxyRecordsNoTargets(t *testing.T) {
	proxy, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if got := proxy.Targets(); len(got) != 0 {
		t.Fatalf("fresh proxy Targets()=%v, want none", got)
	}
}

func TestUDPAssociateRelaysDatagram(t *testing.T) {
	// A local UDP echo server stands in for a mediated DNS resolver.
	echo, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := echo.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = echo.WriteToUDP(append([]byte("ans:"), buf[:n]...), addr)
		}
	}()
	echoAddr := echo.LocalAddr().(*net.UDPAddr)

	proxy, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = proxy.Serve(ctx) }()

	control, err := net.DialTimeout("tcp", proxy.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if _, err := control.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	methodResp := make([]byte, 2)
	if _, err := io.ReadFull(control, methodResp); err != nil {
		t.Fatal(err)
	}
	// UDP ASSOCIATE request (client address 0.0.0.0:0).
	if _, err := control.Write([]byte{0x05, cmdUDPAssociate, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(control, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != replySuccess {
		t.Fatalf("udp associate reply=%v", reply)
	}
	relayPort := int(binary.BigEndian.Uint16(reply[8:10]))
	relay, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: relayPort})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	// Encapsulate "dns-query" addressed to the echo server and send via the relay.
	req := []byte{0x00, 0x00, 0x00, atypIPv4}
	req = append(req, net.IPv4(127, 0, 0, 1).To4()...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(echoAddr.Port))
	req = append(req, portBytes...)
	req = append(req, []byte("dns-query")...)
	if _, err := relay.Write(req); err != nil {
		t.Fatal(err)
	}
	_ = relay.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp := make([]byte, 512)
	n, err := relay.Read(resp)
	if err != nil {
		t.Fatalf("reading relayed UDP response: %v", err)
	}
	// Response is SOCKS-encapsulated: skip the header, check the echoed payload.
	_, _, data, ok := parseUDPRequest(resp[:n])
	if !ok {
		t.Fatalf("malformed relayed response: %v", resp[:n])
	}
	if string(data) != "ans:dns-query" {
		t.Fatalf("relayed UDP payload=%q, want ans:dns-query", data)
	}
	wantTarget := net.JoinHostPort("127.0.0.1", strconv.Itoa(echoAddr.Port))
	found := false
	for _, tgt := range proxy.Targets() {
		if tgt == wantTarget {
			found = true
		}
	}
	if !found {
		t.Fatalf("UDP destination not recorded as target: %v", proxy.Targets())
	}
}
