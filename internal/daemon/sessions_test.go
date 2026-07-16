package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSessionRegistryCapacityIdentityAndIndependentCancellation(t *testing.T) {
	r := newSessionRegistry(2, func() time.Time { return time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC) })
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	a, err := r.register("conn-a", cancelA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.register("conn-b", cancelB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.register("conn-c", func() {}); !errors.Is(err, errSessionCapacity) {
		t.Fatalf("capacity error=%v", err)
	}
	if err := a.markStarted(sessionStart{SessionID: "ses_a", EnvironmentID: "env_a", Profile: "default", Backend: "lima", TerminalMode: "pty"}); err != nil {
		t.Fatal(err)
	}
	if err := b.markStarted(sessionStart{SessionID: "ses_b", EnvironmentID: "env_a", Profile: "default", Backend: "lima", TerminalMode: "none"}); err != nil {
		t.Fatal(err)
	}
	r.cancelConnection("conn-a")
	select {
	case <-ctxA.Done():
	case <-time.After(time.Second):
		t.Fatal("connection A was not cancelled")
	}
	select {
	case <-ctxB.Done():
		t.Fatal("connection B was cancelled with sibling")
	default:
	}
	r.finish("conn-a", "")
	if got := r.snapshots(); len(got) != 1 || got[0].SessionID != "ses_b" {
		t.Fatalf("snapshots=%+v", got)
	}
	r.finish("conn-b", "")
}

func TestSessionRegistryStopSerializesAgainstRegister(t *testing.T) {
	r := newSessionRegistry(32, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, _ = r.register(fmt.Sprintf("conn-%d", index), func() {})
		}(i)
	}
	r.beginStop()
	wg.Wait()
	if _, err := r.register("late", func() {}); !errors.Is(err, errSessionRegistryStopping) {
		t.Fatalf("late registration error=%v", err)
	}
	for _, snapshot := range r.snapshots() {
		r.finish(snapshot.ConnectionID, "")
	}
}

func TestSessionRegistryDrainIsBounded(t *testing.T) {
	r := newSessionRegistry(1, nil)
	_, err := r.register("stuck", func() {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := r.drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain error=%v", err)
	}
	r.finish("stuck", "cleanup failed")
}
