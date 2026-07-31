// Command hideout-dns-stub is the guest-local DNS stub resolver. It listens for
// ordinary DNS on UDP+TCP and forwards each query as DoH (DNS-over-HTTPS) to a
// DoH server reached by IP literal, so DNS is mediated over the privacy path
// (the DoH HTTPS request traverses the TUN, tun2socks, and the SOCKS CONNECT
// proxy) without requiring SOCKS UDP ASSOCIATE.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
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
	readyFile := flag.String("ready-file", "", "optional absolute marker written after UDP+TCP listeners bind")
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
	if err := publishReadyMarker(*readyFile, os.Getpid()); err != nil {
		fmt.Fprintln(os.Stderr, "hideout-dns-stub:", err)
		return 1
	}
	if *readyFile != "" {
		defer func() {
			if err := os.Remove(*readyFile); err != nil && !errors.Is(err, os.ErrNotExist) {
				fmt.Fprintln(os.Stderr, "hideout-dns-stub: remove ready marker:", err)
			}
		}()
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

func publishReadyMarker(path string, pid int) (err error) {
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || pid <= 0 {
		return errors.New("ready marker requires an absolute clean path and positive process ID")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create ready marker: %w", err)
	}
	complete := false
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
			complete = false
		}
		if !complete {
			if removeErr := os.Remove(path); removeErr != nil &&
				!errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, removeErr)
			}
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("set ready marker permissions: %w", err)
	}
	if _, err := fmt.Fprintf(file, "%d\n", pid); err != nil {
		return fmt.Errorf("write ready marker: %w", err)
	}
	complete = true
	return nil
}
