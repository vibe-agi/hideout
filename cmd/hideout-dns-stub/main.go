// Command hideout-dns-stub is the guest-local DNS stub resolver. It listens for
// ordinary DNS on UDP+TCP and forwards each query as DoH (DNS-over-HTTPS) to a
// DoH server reached by IP literal, so DNS is mediated over the privacy path
// (the DoH HTTPS request traverses the TUN, tun2socks, and the SOCKS CONNECT
// proxy) without requiring SOCKS UDP ASSOCIATE.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/vibe-agi/hideout/internal/dnsstub"
)

func main() {
	os.Exit(run())
}

func run() int {
	listen := flag.String("listen", "127.0.0.1:53", "UDP+TCP listen address")
	dohServer := flag.String("doh-server", "", "DoH server IP literal (queries are sent to https://<ip>/dns-query)")
	tlsServerName := flag.String("tls-server-name", "", "TLS SNI / cert name for the DoH server (optional)")
	flag.Parse()

	if *dohServer == "" {
		fmt.Fprintln(os.Stderr, "hideout-dns-stub: --doh-server is required")
		return 2
	}

	server, err := dnsstub.Listen(*listen, *dohServer, *tlsServerName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hideout-dns-stub:", err)
		return 1
	}
	hostPort, err := server.HostPort()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hideout-dns-stub:", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, hostPort)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Serve(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "hideout-dns-stub:", err)
		return 1
	}
	return 0
}
