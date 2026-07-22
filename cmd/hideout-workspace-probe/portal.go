package main

import (
	"context"
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

	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func runPortalServe(args []string) error {
	fs := flag.NewFlagSet("hideout-workspace-probe portal-serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", "", "absolute host workspace root")
	controlDir := fs.String("control-dir", "", "private control output directory")
	session := fs.String("session", "research-session", "research session id")
	environment := fs.String("environment", "research-environment", "research environment id")
	incarnation := fs.String("incarnation", "research-incarnation", "research boot identity")
	ttl := fs.Duration("ttl", 30*time.Minute, "credential lifetime")
	guestHost := fs.String("guest-host", "", "guest-reachable host name for the endpoint file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !filepath.IsAbs(*root) || !filepath.IsAbs(*controlDir) {
		return errors.New("portal-serve requires absolute --root and --control-dir")
	}
	if err := os.MkdirAll(*controlDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(*controlDir, 0o700); err != nil {
		return err
	}
	authority := workspaceattach.NewPortalCredentialAuthority()
	credential, err := authority.Issue(*session, *environment, *incarnation, workspaceattach.PortalAudience, *ttl)
	if err != nil {
		return err
	}
	limits := workspaceattach.DefaultPortalLimits()
	admission, err := workspaceattach.NewAdmissionController(workspaceattach.SelectedLimits())
	if err != nil {
		return err
	}
	server, err := workspaceattach.NewPortalServer(workspaceattach.PortalServerOptions{
		Root: *root, Authority: authority, Limits: limits,
		EnvironmentID: *environment, ProviderID: "research-provider", Admission: admission,
	})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		server.Close()
		return err
	}
	if err := server.Start(listener); err != nil {
		listener.Close()
		server.Close()
		return err
	}
	defer server.Close()
	advertisedAddress, err := portalAdvertisedAddress(server.Addr(), *guestHost)
	if err != nil {
		return err
	}
	credentialPath := filepath.Join(*controlDir, "credential.bin")
	if err := workspaceattach.WritePortalCredential(credentialPath, credential); err != nil {
		return err
	}
	defer os.Remove(credentialPath)
	if err := os.WriteFile(filepath.Join(*controlDir, "endpoint.txt"), []byte(advertisedAddress+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*controlDir, "ready"), []byte("ready\n"), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "workspace-portal\tready\t%s\n", advertisedAddress)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	<-ctx.Done()
	return nil
}

func portalAdvertisedAddress(boundAddress, guestHost string) (string, error) {
	guestHost = strings.TrimSpace(guestHost)
	if guestHost == "" {
		return boundAddress, nil
	}
	_, port, err := net.SplitHostPort(boundAddress)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(guestHost, "/\x00\r\n\t ") {
		return "", errors.New("portal guest host is invalid")
	}
	return net.JoinHostPort(guestHost, port), nil
}
