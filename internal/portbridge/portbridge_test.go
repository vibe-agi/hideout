package portbridge

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestBridgeForwardsTCPBetweenExplicitEndpoints(t *testing.T) {
	for _, direction := range []Direction{DirectionGuestToHost, DirectionHostToGuest} {
		t.Run(string(direction), func(t *testing.T) {
			targetAddr, closeTarget := startEchoServer(t)
			defer closeTarget()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bridge, err := Start(ctx, Spec{
				ID:            "br_test",
				Direction:     direction,
				ListenAddress: "127.0.0.1:0",
				TargetAddress: targetAddr,
			})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer bridge.Close()

			conn, err := net.DialTimeout("tcp", bridge.ListenAddress(), time.Second)
			if err != nil {
				t.Fatalf("dial bridge: %v", err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprintln(conn, "hello"); err != nil {
				t.Fatalf("write bridge: %v", err)
			}
			line, err := bufio.NewReader(conn).ReadString('\n')
			if err != nil {
				t.Fatalf("read bridge: %v", err)
			}
			if line != "echo:hello\n" {
				t.Fatalf("bridge response=%q", line)
			}
		})
	}
}

func TestValidateRejectsUnsafeOrUnimplementedPortBridgeShapes(t *testing.T) {
	tests := []struct {
		name string
		spec Spec
		want string
	}{
		{
			name: "unknown direction",
			spec: Spec{Direction: "browser", ListenAddress: "127.0.0.1:0", TargetAddress: "127.0.0.1:1"},
			want: "direction",
		},
		{
			name: "wildcard listen",
			spec: Spec{Direction: DirectionGuestToHost, ListenAddress: "0.0.0.0:0", TargetAddress: "127.0.0.1:1"},
			want: "loopback",
		},
		{
			name: "wildcard target",
			spec: Spec{Direction: DirectionHostToGuest, ListenAddress: "127.0.0.1:0", TargetAddress: "0.0.0.0:1"},
			want: "explicit host",
		},
		{
			name: "guest reachable not implemented",
			spec: Spec{Direction: DirectionGuestToHost, ListenScope: ListenScopeGuestReachable, ListenAddress: "127.0.0.1:0", TargetAddress: "127.0.0.1:1"},
			want: "backend-specific policy",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.spec)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func startEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
					return
				}
				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil {
					return
				}
				_, _ = fmt.Fprintf(conn, "echo:%s", line)
			}()
		}
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}
