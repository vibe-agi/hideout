//go:build linux

package file

import (
	"errors"
	"testing"
	"time"

	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestKernelEventSourceRejectsInvalidConfigurationAndClosedReads(t *testing.T) {
	t.Parallel()

	var nilSource *KernelEventSource
	if _, err := nilSource.Read(); !errors.Is(err, ErrKernelEventSourceClosed) {
		t.Fatalf("nil source Read() error=%v want=%v", err, ErrKernelEventSourceClosed)
	}
	closedSource := &KernelEventSource{}
	if _, err := closedSource.Read(); !errors.Is(err, ErrKernelEventSourceClosed) {
		t.Fatalf("closed source Read() error=%v want=%v", err, ErrKernelEventSourceClosed)
	}
	if _, err := closedSource.Counters(); !errors.Is(err, ErrKernelEventSourceClosed) {
		t.Fatalf("closed source Counters() error=%v want=%v", err, ErrKernelEventSourceClosed)
	}

	owner, err := workloadtypes.NewReusableOwner(
		"env_fixture",
		"lima",
		"incarnation-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	boundary := Boundary{
		Owner: owner, SessionID: "ses_file_fixture",
		CgroupID: 3141, ObserverGeneration: 1,
	}
	anchor := ClockAnchor{
		WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		MonotonicNS: 1,
	}
	lookup := func(uint32, uint64) (string, bool) { return "", false }
	if _, err := OpenKernelEventSource(
		boundary, nil, ClockAnchor{}, "cov_file_fixture", lookup, nil,
	); !errors.Is(err, ErrKernelEventSourceConfig) {
		t.Fatalf("invalid anchor error=%v want=%v", err, ErrKernelEventSourceConfig)
	}
	if _, err := OpenKernelEventSource(
		boundary, nil, anchor, "cov_short", lookup, nil,
	); !errors.Is(err, ErrKernelEventSourceConfig) {
		t.Fatalf("invalid coverage error=%v want=%v", err, ErrKernelEventSourceConfig)
	}
	if _, err := OpenKernelEventSource(
		boundary, nil, anchor, "cov_file_fixture", nil, nil,
	); !errors.Is(err, ErrKernelEventSourceConfig) {
		t.Fatalf("missing lookup error=%v want=%v", err, ErrKernelEventSourceConfig)
	}
	if _, err := OpenKernelEventSource(
		boundary, nil, anchor, "cov_file_fixture", lookup, nil,
	); !errors.Is(err, observerbpf.ErrFileCollectorProcessState) {
		t.Fatalf("missing process state error=%v want=%v", err, observerbpf.ErrFileCollectorProcessState)
	}
}
