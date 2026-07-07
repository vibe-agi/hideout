package dns

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// buildQuery builds a minimal A query for name.
func buildQuery(name string) []byte {
	msg := []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	for label := range strings.SplitSeq(name, ".") {
		msg = append(msg, byte(len(label)))
		msg = append(msg, []byte(label)...)
	}
	msg = append(msg, 0x00)       // end of QNAME
	msg = append(msg, 0x00, 0x01) // QTYPE A
	msg = append(msg, 0x00, 0x01) // QCLASS IN
	return msg
}

func startServer(t *testing.T, answer net.IP) *Server {
	t.Helper()
	srv, err := Listen("127.0.0.1:0", answer)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx) }()
	return srv
}

func TestListenerStartsWithZeroQueries(t *testing.T) {
	srv := startServer(t, nil)
	if got := srv.Count(); got != 0 {
		t.Fatalf("fresh listener Count()=%d, want 0", got)
	}
}

func TestListenerRecordsUDPQuery(t *testing.T) {
	srv := startServer(t, nil)
	hp, err := srv.HostPort()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("udp", hp)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write(buildQuery("known-bad.example")); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, srv, 1)
	if names := srv.Names(); len(names) != 1 || names[0] != "known-bad.example" {
		t.Fatalf("Names()=%v, want [known-bad.example]", names)
	}
}

func TestListenerRecordsTCPQueryDistinctFromUDP(t *testing.T) {
	srv := startServer(t, nil)
	hp, err := srv.HostPort()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", hp)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	query := buildQuery("tcp.example")
	framed := make([]byte, 2+len(query))
	binary.BigEndian.PutUint16(framed[:2], uint16(len(query)))
	copy(framed[2:], query)
	if _, err := conn.Write(framed); err != nil {
		t.Fatal(err)
	}
	// A response is read back over TCP: length prefix + message.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	lenBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		t.Fatalf("reading TCP response length: %v", err)
	}
	waitForCount(t, srv, 1)
	if names := srv.Names(); len(names) != 1 || names[0] != "tcp.example" {
		t.Fatalf("Names()=%v, want [tcp.example]", names)
	}
}

func TestMediatedResolverAnswersConfiguredIP(t *testing.T) {
	want := net.IPv4(203, 0, 113, 7)
	srv := startServer(t, want)
	hp, err := srv.HostPort()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("udp", hp)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write(buildQuery("mediated.example")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp := make([]byte, maxMessage)
	n, err := conn.Read(resp)
	if err != nil {
		t.Fatal(err)
	}
	resp = resp[:n]
	if n < 12 || resp[2]&0x80 == 0 {
		t.Fatalf("response is not a DNS answer: %v", resp)
	}
	if anCount := binary.BigEndian.Uint16(resp[6:8]); anCount != 1 {
		t.Fatalf("ANCOUNT=%d, want 1", anCount)
	}
	got := resp[len(resp)-4:]
	if !net.IP(got).Equal(want.To4()) {
		t.Fatalf("answer IP=%v, want %v", net.IP(got), want)
	}
}

func waitForCount(t *testing.T, srv *Server, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Count() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Count()=%d after wait, want %d", srv.Count(), want)
}
