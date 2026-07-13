package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	dnsserver "github.com/vibe-agi/hideout/internal/testproxy/dns"
)

func main() {
	os.Exit(run())
}

func run() int {
	listen := flag.String("listen", "127.0.0.1:0", "UDP+TCP listen address")
	answer := flag.String("answer", "", "A record to return for queries (mediated resolver role); empty records queries only")
	countFile := flag.String("count-file", "", "write JSON query count on start and exit")
	query := flag.String("query", "", "send one direct UDP DNS query to an IP or IP:port and exit")
	timeout := flag.Duration("timeout", 3*time.Second, "direct query timeout")
	flag.Parse()
	if *query != "" {
		if err := directDNSQuery(*query, *timeout); err != nil {
			fmt.Fprintln(os.Stderr, "hideout-gate-dns:", err)
			if errors.Is(err, errInvalidQueryTarget) {
				return 2
			}
			return 1
		}
		return 0
	}

	var answerIP net.IP
	if *answer != "" {
		answerIP = net.ParseIP(*answer)
		if answerIP == nil {
			fmt.Fprintln(os.Stderr, "hideout-gate-dns: invalid --answer:", *answer)
			return 1
		}
	}

	server, err := dnsserver.Listen(*listen, answerIP)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hideout-gate-dns:", err)
		return 1
	}
	hostPort, err := server.HostPort()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hideout-gate-dns:", err)
		return 1
	}
	if *countFile != "" {
		if err := writeCountFile(*countFile, server); err != nil {
			fmt.Fprintln(os.Stderr, "hideout-gate-dns:", err)
			return 1
		}
	}
	fmt.Fprintln(os.Stdout, hostPort)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := server.Serve(ctx)
	if *countFile != "" {
		if err := writeCountFile(*countFile, server); err != nil {
			fmt.Fprintln(os.Stderr, "hideout-gate-dns:", err)
			return 1
		}
	}
	if serveErr != nil {
		fmt.Fprintln(os.Stderr, "hideout-gate-dns:", serveErr)
		return 1
	}
	return 0
}

var errInvalidQueryTarget = errors.New("invalid direct DNS query target")

func directDNSQuery(target string, timeout time.Duration) error {
	if timeout <= 0 || timeout > 30*time.Second {
		return fmt.Errorf("%w: timeout must be within 30 seconds", errInvalidQueryTarget)
	}
	address, err := directDNSAddress(target)
	if err != nil {
		return err
	}
	query, id, err := directDNSQueryPacket("example.com")
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("udp", address, timeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	if _, err := conn.Write(query); err != nil {
		return err
	}
	response := make([]byte, 4096)
	n, err := conn.Read(response)
	if err != nil {
		return err
	}
	if n < 12 || binary.BigEndian.Uint16(response[:2]) != id || response[2]&0x80 == 0 {
		return errors.New("resolver returned an invalid DNS response")
	}
	return nil
}

func directDNSAddress(target string) (string, error) {
	target = strings.TrimSpace(target)
	if ip := net.ParseIP(target); ip != nil {
		return net.JoinHostPort(ip.String(), "53"), nil
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil || net.ParseIP(host) == nil || port == "" {
		return "", fmt.Errorf("%w: target must be an IP or IP:port", errInvalidQueryTarget)
	}
	return net.JoinHostPort(host, port), nil
}

func directDNSQueryPacket(name string) ([]byte, uint16, error) {
	var idBytes [2]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return nil, 0, err
	}
	id := binary.BigEndian.Uint16(idBytes[:])
	message := []byte{idBytes[0], idBytes[1], 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return nil, 0, errors.New("invalid DNS query name")
		}
		message = append(message, byte(len(label)))
		message = append(message, label...)
	}
	message = append(message, 0x00, 0x00, 0x01, 0x00, 0x01)
	return message, id, nil
}

type countSnapshot struct {
	Count int      `json:"count"`
	Names []string `json:"names"`
}

func writeCountFile(path string, server *dnsserver.Server) error {
	names := server.Names()
	data, err := json.MarshalIndent(countSnapshot{Count: len(names), Names: names}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return nil
}
