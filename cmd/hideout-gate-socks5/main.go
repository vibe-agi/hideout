package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/vibe-agi/hideout/internal/testproxy/socks5"
)

func main() {
	os.Exit(run())
}

func run() int {
	listen := flag.String("listen", "127.0.0.1:0", "TCP listen address")
	urlHost := flag.String("url-host", "", "host name to print in the socks5 URL")
	useEnvHTTPProxy := flag.Bool("use-env-http-proxy", false, "chain fixture egress through HTTPS_PROXY/HTTP_PROXY")
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
	if *useEnvHTTPProxy {
		upstream := os.Getenv("HTTPS_PROXY")
		if upstream == "" {
			upstream = os.Getenv("HTTP_PROXY")
		}
		dialContext, dialErr := socks5.HTTPConnectDialer(upstream)
		if dialErr != nil {
			fmt.Fprintln(os.Stderr, "hideout-gate-socks5:", dialErr)
			return 1
		}
		server.DialContext = dialContext
	}
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
