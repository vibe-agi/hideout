package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWaitForProcessReturnsReadyWithoutConsumingExit(t *testing.T) {
	done := make(chan error, 1)
	sentinel := errors.New("late exit")
	done <- sentinel
	if err := waitForProcess(time.Second, func() bool {
		return true
	}, done); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, sentinel) {
		t.Fatalf("exit result=%v want=%v", err, sentinel)
	}
}

func TestWaitForProcessReportsAndRetainsEarlyExit(t *testing.T) {
	done := make(chan error, 1)
	sentinel := errors.New("startup failed")
	done <- sentinel
	started := time.Now()
	err := waitForProcess(time.Second, func() bool {
		return false
	}, done)
	if !errors.Is(err, sentinel) ||
		!strings.Contains(err.Error(), "process exited before readiness") {
		t.Fatalf("readiness error=%v", err)
	}
	if elapsed := time.Since(started); elapsed >= 100*time.Millisecond {
		t.Fatalf("early exit detection took %s", elapsed)
	}
	if retained := <-done; !errors.Is(retained, sentinel) {
		t.Fatalf("retained exit result=%v want=%v", retained, sentinel)
	}
}

func TestWaitForProcessRejectsUnsafeCompletionChannel(t *testing.T) {
	if err := waitForProcess(
		time.Second,
		func() bool { return false },
		make(chan error),
	); err == nil ||
		!strings.Contains(err.Error(), "configuration is invalid") {
		t.Fatalf("configuration error=%v", err)
	}
}
