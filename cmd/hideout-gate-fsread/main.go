package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/broker"
)

func main() {
	readPath := flag.String("read", "", "path that must be readable")
	denyPath := flag.String("deny", "", "path that must not be readable")
	brokerReadPath := flag.String("broker-read", "", "test-only direct HostFS broker read path")
	flag.Parse()
	if *brokerReadPath != "" {
		if err := brokerRead(*brokerReadPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *readPath == "" || *denyPath == "" {
		fmt.Fprintln(os.Stderr, "usage: hideout-gate-fsread --read <path> --deny <path> | --broker-read <path>")
		os.Exit(2)
	}
	data, err := os.ReadFile(*readPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read granted path: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("hostfs_go=%s\n", strings.TrimSpace(string(data)))
	if _, err := os.ReadFile(*denyPath); err == nil {
		fmt.Fprintln(os.Stderr, "denied path unexpectedly readable")
		os.Exit(1)
	}
	fmt.Println("hostfs_go_denied=yes")
}

func brokerRead(path string) error {
	endpoint, err := broker.ParseEndpoint(os.Getenv(broker.EnvEndpoint))
	if err != nil {
		return fmt.Errorf("parse broker endpoint: %w", err)
	}
	id, err := broker.NewRequestID()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp := broker.ClientOpenEndpoint(ctx, endpoint, broker.Request{
		ID:              id,
		SessionID:       os.Getenv(broker.EnvSession),
		CapabilityToken: os.Getenv(broker.EnvToken),
		Subject:         "hostfs:daemon",
		Route:           "host-broker",
		Action:          "host.fs.read",
		Args: map[string]any{
			"path":   path,
			"offset": int64(0),
			"size":   1 << 20,
		},
	})
	if resp.Status != "ok" {
		code := "unknown"
		errno := "unknown"
		if resp.Error != nil {
			code = resp.Error.Code
			errno = resp.Error.Errno
		}
		return fmt.Errorf("broker read denied: status=%s code=%s errno=%s", resp.Status, code, errno)
	}
	raw, _ := resp.Data["dataBase64"].(string)
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return fmt.Errorf("decode broker read: %w", err)
	}
	fmt.Printf("hostfs_broker=%s\n", strings.TrimSpace(string(data)))
	return nil
}
