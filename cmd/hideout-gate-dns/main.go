package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	dnsserver "github.com/vibe-agi/hideout/internal/testproxy/dns"
)

func main() {
	os.Exit(run())
}

func run() int {
	listen := flag.String("listen", "127.0.0.1:0", "UDP+TCP listen address")
	answer := flag.String("answer", "", "A record to return for queries (mediated resolver role); empty records queries only")
	countFile := flag.String("count-file", "", "write JSON query count on start and exit")
	flag.Parse()

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
