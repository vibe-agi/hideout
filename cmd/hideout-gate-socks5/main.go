package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vibe-agi/hideout/internal/testproxy/socks5"
)

func main() {
	os.Exit(run())
}

func run() int {
	listen := flag.String("listen", "127.0.0.1:0", "TCP listen address")
	urlHost := flag.String("url-host", "", "host name to print in the socks5 URL")
	useEnvHTTPProxy := flag.Bool("use-env-http-proxy", false, "chain fixture egress through HTTPS_PROXY/HTTP_PROXY")
	mapConnect := flag.String("map-connect", "", "rewrite one exact CONNECT target as source=destination")
	flag.Parse()

	server, err := socks5.Listen(*listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hideout-gate-socks5:", err)
		return 1
	}
	// The gate captures this stream. Without it a failed privacy forward proof
	// cannot distinguish a guest that never reached the proxy from a CONNECT
	// the proxy refused.
	server.Trace = func(line string) {
		fmt.Fprintln(os.Stderr, "hideout-gate-socks5:", line)
	}
	var dialContext func(context.Context, string, string) (net.Conn, error)
	if *useEnvHTTPProxy {
		upstream := os.Getenv("HTTPS_PROXY")
		if upstream == "" {
			upstream = os.Getenv("HTTP_PROXY")
		}
		var dialErr error
		dialContext, dialErr = socks5.HTTPConnectDialer(upstream)
		if dialErr != nil {
			fmt.Fprintln(os.Stderr, "hideout-gate-socks5:", dialErr)
			return 1
		}
	}
	if *mapConnect != "" {
		source, destination, mapErr := parseConnectMap(*mapConnect)
		if mapErr != nil {
			fmt.Fprintln(os.Stderr, "hideout-gate-socks5:", mapErr)
			return 1
		}
		if dialContext == nil {
			dialer := &net.Dialer{Timeout: 20 * time.Second}
			dialContext = dialer.DialContext
		}
		upstreamDial := dialContext
		dialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
			if target == source {
				target = destination
			}
			return upstreamDial(ctx, network, target)
		}
	}
	server.DialContext = dialContext
	url, err := server.URL(*urlHost)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hideout-gate-socks5:", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, url)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Serve(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "hideout-gate-socks5:", err)
		return 1
	}
	return 0
}

func parseConnectMap(value string) (string, string, error) {
	source, destination, ok := strings.Cut(strings.TrimSpace(value), "=")
	if !ok || strings.Contains(destination, "=") {
		return "", "", fmt.Errorf("--map-connect must be source-host:port=destination-host:port")
	}
	for _, address := range []string{source, destination} {
		host, port, err := net.SplitHostPort(address)
		if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
			return "", "", fmt.Errorf("--map-connect must contain two complete host:port addresses")
		}
	}
	return source, destination, nil
}
