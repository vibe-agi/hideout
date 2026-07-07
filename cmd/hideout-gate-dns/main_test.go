package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dnsserver "github.com/vibe-agi/hideout/internal/testproxy/dns"
)

func TestWriteCountFileExposesQueryCount(t *testing.T) {
	server, err := dnsserver.Listen("127.0.0.1:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = server.Serve(ctx) }()

	hostPort, err := server.HostPort()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("udp", hostPort)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write(buildDNSQuery("known-bad.example")); err != nil {
		t.Fatal(err)
	}
	waitForDNSCount(t, server, 1)

	path := filepath.Join(t.TempDir(), "nested", "count.json")
	if err := writeCountFile(path, server); err != nil {
		t.Fatalf("writeCountFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot countSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("decode count file: %v\n%s", err, data)
	}
	if snapshot.Count != 1 || len(snapshot.Names) != 1 || snapshot.Names[0] != "known-bad.example" {
		t.Fatalf("snapshot=%+v, want one known-bad.example query", snapshot)
	}
}

func buildDNSQuery(name string) []byte {
	msg := []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	for label := range strings.SplitSeq(name, ".") {
		msg = append(msg, byte(len(label)))
		msg = append(msg, []byte(label)...)
	}
	msg = append(msg, 0x00)
	msg = append(msg, 0x00, 0x01)
	msg = append(msg, 0x00, 0x01)
	return msg
}

func waitForDNSCount(t *testing.T, server *dnsserver.Server, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if server.Count() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Count()=%d after wait, want %d", server.Count(), want)
}
