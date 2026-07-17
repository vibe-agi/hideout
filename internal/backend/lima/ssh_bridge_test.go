package lima

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestConnectLimaSSHRetriesOneTransientPreAuthorityFailure(t *testing.T) {
	attempts := 0
	want := &ssh.Client{}
	client, err := connectLimaSSHWithRetry(context.Background(), func() (*ssh.Client, error) {
		attempts++
		if attempts == 1 {
			return nil, fmt.Errorf("handshake: %w", &net.DNSError{IsTimeout: true})
		}
		return want, nil
	})
	if err != nil || client != want || attempts != 2 {
		t.Fatalf("client=%p err=%v attempts=%d", client, err, attempts)
	}
}

func TestConnectLimaSSHDoesNotRetryNonTransientFailure(t *testing.T) {
	attempts := 0
	want := errors.New("host key rejected")
	_, err := connectLimaSSHWithRetry(context.Background(), func() (*ssh.Client, error) {
		attempts++
		return nil, want
	})
	if !errors.Is(err, want) || attempts != 1 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}

func TestConnectLimaSSHRetriesHandshakeEOF(t *testing.T) {
	attempts := 0
	want := &ssh.Client{}
	client, err := connectLimaSSHWithRetry(context.Background(), func() (*ssh.Client, error) {
		attempts++
		if attempts == 1 {
			return nil, fmt.Errorf("ssh handshake: %w", io.EOF)
		}
		return want, nil
	})
	if err != nil || client != want || attempts != 2 {
		t.Fatalf("client=%p err=%v attempts=%d", client, err, attempts)
	}
}

func TestConnectLimaSSHDoesNotRetryAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts := 0
	_, err := connectLimaSSHWithRetry(ctx, func() (*ssh.Client, error) {
		attempts++
		return nil, &net.DNSError{IsTimeout: true}
	})
	if err == nil || attempts != 1 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}
