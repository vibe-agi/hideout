package lima

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestSSHClientPoolReusesOneTransportAcrossConcurrentLeases(t *testing.T) {
	var connects atomic.Int32
	var closes atomic.Int32
	client := &ssh.Client{}
	pool := newSSHClientPool(
		func(context.Context, *ssh.Client) error { return nil },
		func(*ssh.Client) error { closes.Add(1); return nil },
	)
	key := sshPoolKey{instance: "hideout-test", hostName: "127.0.0.1", port: "60022", user: "root"}
	connect := func() (*ssh.Client, error) {
		connects.Add(1)
		return client, nil
	}

	const count = 32
	leasing := make(chan struct{})
	leases := make(chan *sshClientLease, count)
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-leasing
			lease, err := pool.acquire(context.Background(), key, connect)
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			leases <- lease
		}()
	}
	close(leasing)
	wg.Wait()
	close(leases)
	for lease := range leases {
		if lease.Client() != client {
			t.Fatal("pool returned another transport")
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if got := connects.Load(); got != 1 {
		t.Fatalf("connections=%d want 1", got)
	}
	if got := closes.Load(); got != 0 {
		t.Fatalf("transport closed while pooled: %d", got)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if got := closes.Load(); got != 1 {
		t.Fatalf("shutdown closes=%d want 1", got)
	}
}

func TestSSHClientPoolReplacesIdleUnhealthyTransport(t *testing.T) {
	first := &ssh.Client{}
	second := &ssh.Client{}
	checks := 0
	closes := 0
	pool := newSSHClientPool(
		func(context.Context, *ssh.Client) error {
			checks++
			return errors.New("transport closed")
		},
		func(*ssh.Client) error { closes++; return nil },
	)
	key := sshPoolKey{instance: "hideout-test", port: "60022", user: "root"}
	connects := 0
	connect := func() (*ssh.Client, error) {
		connects++
		if connects == 1 {
			return first, nil
		}
		return second, nil
	}
	lease, err := pool.acquire(context.Background(), key, connect)
	if err != nil {
		t.Fatal(err)
	}
	_ = lease.Close()
	replacement, err := pool.acquire(context.Background(), key, connect)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if replacement.Client() != second || checks != 1 || connects != 2 || closes != 1 {
		t.Fatalf("replacement=%p checks=%d connects=%d closes=%d", replacement.Client(), checks, connects, closes)
	}
}

func TestSSHClientPoolInvalidateInstanceFencesEveryUserTransport(t *testing.T) {
	closes := 0
	pool := newSSHClientPool(nil, func(*ssh.Client) error { closes++; return nil })
	for _, user := range []string{"root", "developer"} {
		lease, err := pool.acquire(context.Background(), sshPoolKey{instance: "hideout-test", user: user}, func() (*ssh.Client, error) {
			return &ssh.Client{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Close()
	}
	other, err := pool.acquire(context.Background(), sshPoolKey{instance: "hideout-other", user: "root"}, func() (*ssh.Client, error) {
		return &ssh.Client{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := pool.InvalidateInstance("hideout-test"); err != nil {
		t.Fatal(err)
	}
	if closes != 2 || len(pool.entries) != 1 {
		t.Fatalf("closes=%d entries=%d", closes, len(pool.entries))
	}
}

func TestSSHClientPoolRejectsAcquireAfterShutdown(t *testing.T) {
	pool := newSSHClientPool(nil, func(*ssh.Client) error { return nil })
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.acquire(context.Background(), sshPoolKey{instance: "hideout-test"}, func() (*ssh.Client, error) {
		return &ssh.Client{}, nil
	}); err == nil {
		t.Fatal("closed pool accepted a new transport")
	}
}
