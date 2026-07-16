package lima

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestRunCancelableSSHCommandStopsSessionOnContextCancellation(t *testing.T) {
	started := make(chan struct{})
	wait := make(chan error, 1)
	session := &fakeSSHCommandSession{started: started, wait: wait}
	connection := &fakeSSHConnection{close: func() { wait <- errors.New("connection closed") }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runCancelableSSHCommand(ctx, session, connection, "long-running-target") }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("ssh command did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ssh command ignored cancellation")
	}
	if got := session.signal(); got != ssh.SIGTERM {
		t.Fatalf("signal=%q want %q", got, ssh.SIGTERM)
	}
	if !connection.closed() {
		t.Fatal("ssh connection remained open after cancellation")
	}
}

func TestRunCancelableSSHCommandPreservesRemoteExit(t *testing.T) {
	want := errors.New("remote exit 37")
	wait := make(chan error, 1)
	wait <- want
	err := runCancelableSSHCommand(context.Background(), &fakeSSHCommandSession{wait: wait}, &fakeSSHConnection{}, "target")
	if !errors.Is(err, want) {
		t.Fatalf("error=%v want remote exit", err)
	}
}

func TestSetupSSHConnectRetriesOnlyHandshakeEOF(t *testing.T) {
	attempts := 0
	client, err := retrySetupSSHConnect(context.Background(), func() (*ssh.Client, error) {
		attempts++
		if attempts < setupSSHConnectAttempts {
			return nil, fmt.Errorf("ssh: handshake failed: %w", io.EOF)
		}
		return nil, nil
	})
	if err != nil || client != nil || attempts != setupSSHConnectAttempts {
		t.Fatalf("retry result client=%v attempts=%d err=%v", client, attempts, err)
	}

	attempts = 0
	want := errors.New("ssh authentication failed")
	_, err = retrySetupSSHConnect(context.Background(), func() (*ssh.Client, error) {
		attempts++
		return nil, want
	})
	if !errors.Is(err, want) || attempts != 1 {
		t.Fatalf("non-transient result attempts=%d err=%v", attempts, err)
	}
}

type fakeSSHCommandSession struct {
	mu      sync.Mutex
	started chan struct{}
	wait    chan error
	sig     ssh.Signal
}

func (s *fakeSSHCommandSession) Start(string) error {
	if s.started != nil {
		close(s.started)
	}
	return nil
}

func (s *fakeSSHCommandSession) Wait() error { return <-s.wait }

func (s *fakeSSHCommandSession) Signal(signal ssh.Signal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sig = signal
	return nil
}

func (s *fakeSSHCommandSession) signal() ssh.Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sig
}

type fakeSSHConnection struct {
	mu      sync.Mutex
	isClose bool
	close   func()
}

func (c *fakeSSHConnection) Close() error {
	c.mu.Lock()
	c.isClose = true
	closeFn := c.close
	c.mu.Unlock()
	if closeFn != nil {
		closeFn()
	}
	return nil
}

func (c *fakeSSHConnection) closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isClose
}
