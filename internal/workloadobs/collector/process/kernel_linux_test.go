//go:build linux

package process

import (
	"errors"
	"testing"
)

func TestKernelEventSourceReadClosed(t *testing.T) {
	t.Parallel()

	var nilSource *KernelEventSource
	if _, err := nilSource.Read(); !errors.Is(err, ErrKernelEventSourceClosed) {
		t.Fatalf("nil source Read() error = %v, want %v", err, ErrKernelEventSourceClosed)
	}

	closedSource := &KernelEventSource{}
	if _, err := closedSource.Read(); !errors.Is(err, ErrKernelEventSourceClosed) {
		t.Fatalf("closed source Read() error = %v, want %v", err, ErrKernelEventSourceClosed)
	}
	if _, err := closedSource.Counters(); !errors.Is(err, ErrKernelEventSourceClosed) {
		t.Fatalf("closed source Counters() error = %v, want %v", err, ErrKernelEventSourceClosed)
	}
}
