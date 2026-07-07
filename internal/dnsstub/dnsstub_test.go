package dnsstub

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// startStub builds a stub whose DoH endpoint points at a mock server.
func startStub(t *testing.T, dohHandler http.HandlerFunc) *Server {
	t.Helper()
	doh := httptest.NewServer(dohHandler)
	t.Cleanup(doh.Close)

	srv, err := Listen("127.0.0.1:0", "1.1.1.1", "")
	if err != nil {
		t.Fatal(err)
	}
	// White-box override: point the stub at the mock DoH server.
	srv.dohURL = doh.URL + "/dns-query"
	srv.client = doh.Client()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx) }()
	return srv
}

func TestStubForwardsUDPQueryOverDoH(t *testing.T) {
	var gotQuery []byte
	var gotContentType string
	answer := []byte{0x12, 0x34, 0x81, 0x80, 0, 1, 0, 1, 0, 0, 0, 0, 'a', 'n', 's'}
	stub := startStub(t, func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotQuery, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(answer)
	})

	hp, err := stub.HostPort()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("udp", hp)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	query := []byte{0x12, 0x34, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0, 'q'}
	if _, err := conn.Write(query); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp := make([]byte, maxMessage)
	n, err := conn.Read(resp)
	if err != nil {
		t.Fatalf("reading DoH-mediated UDP response: %v", err)
	}
	if string(resp[:n]) != string(answer) {
		t.Fatalf("stub returned %v, want the DoH answer %v", resp[:n], answer)
	}
	if gotContentType != "application/dns-message" {
		t.Fatalf("DoH request Content-Type=%q, want application/dns-message", gotContentType)
	}
	if string(gotQuery) != string(query) {
		t.Fatalf("DoH server received query %v, want %v", gotQuery, query)
	}
}

func TestStubForwardsTCPQueryOverDoH(t *testing.T) {
	answer := []byte{0x56, 0x78, 0x81, 0x80, 0, 1, 0, 1, 0, 0, 0, 0, 'x'}
	stub := startStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(answer)
	})
	hp, err := stub.HostPort()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", hp)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	query := []byte{0x56, 0x78, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0, 'y'}
	framed := make([]byte, 2+len(query))
	binary.BigEndian.PutUint16(framed[:2], uint16(len(query)))
	copy(framed[2:], query)
	if _, err := conn.Write(framed); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	lenBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		t.Fatal(err)
	}
	respLen := binary.BigEndian.Uint16(lenBuf)
	respBody := make([]byte, respLen)
	if _, err := io.ReadFull(conn, respBody); err != nil {
		t.Fatal(err)
	}
	if string(respBody) != string(answer) {
		t.Fatalf("TCP stub returned %v, want %v", respBody, answer)
	}
}

func TestListenRejectsNonIPDoHServer(t *testing.T) {
	if _, err := Listen("127.0.0.1:0", "cloudflare-dns.com", ""); err == nil {
		t.Fatal("expected rejection of a non-IP DoH server (avoids DNS chicken-and-egg)")
	}
}
