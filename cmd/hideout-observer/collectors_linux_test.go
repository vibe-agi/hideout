//go:build linux

package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cilium/ebpf/ringbuf"
	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
	filecollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/file"
)

func TestFileCollectorDrainWaitsForFlushAfterAggregationDeadline(t *testing.T) {
	reader := &observerFileReaderFixture{
		readErrors: []error{
			fmt.Errorf("wrapped aggregation deadline: %w", os.ErrDeadlineExceeded),
			ringbuf.ErrFlushed,
		},
	}
	config := observerTestConfig(t)
	normalizer, err := filecollector.NewNormalizer(filecollector.Boundary{
		Owner: config.Binding.Owner, SessionID: config.Binding.SessionID,
		CgroupID:           config.Binding.CgroupID,
		ObserverGeneration: config.Binding.ObserverGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	collectors := &linuxObserverCollectors{
		fileReader:     reader,
		fileNormalizer: normalizer,
		errs:           make(chan error, 1),
	}
	collectors.draining.Store(true)
	collectors.readFile(context.Background(), func(observerRecord) error {
		t.Fatal("empty drain emitted an observation")
		return nil
	})
	if reader.reads != 2 {
		t.Fatalf("file drain reads=%d want=2", reader.reads)
	}
	if !reader.deadlineClearedBeforeFlush {
		t.Fatalf("file drain deadlines=%v; expired deadline was not cleared", reader.deadlines)
	}
}

type observerFileReaderFixture struct {
	readErrors                 []error
	reads                      int
	deadlines                  []time.Time
	deadlineClearedBeforeFlush bool
}

func (reader *observerFileReaderFixture) ReadFileEventInto(
	*observerbpf.RawFileEvent,
) error {
	if reader.reads == 1 && len(reader.deadlines) != 0 &&
		reader.deadlines[len(reader.deadlines)-1].IsZero() {
		reader.deadlineClearedBeforeFlush = true
	}
	if reader.reads >= len(reader.readErrors) {
		return ringbuf.ErrFlushed
	}
	err := reader.readErrors[reader.reads]
	reader.reads++
	return err
}

func (reader *observerFileReaderFixture) SetDeadline(deadline time.Time) {
	reader.deadlines = append(reader.deadlines, deadline)
}

func (*observerFileReaderFixture) Counters() (
	observerbpf.FileCollectorCounters,
	error,
) {
	return observerbpf.FileCollectorCounters{}, nil
}

func (*observerFileReaderFixture) FlushPending() error { return nil }
func (*observerFileReaderFixture) Stop() error         { return nil }
func (*observerFileReaderFixture) Close() error        { return nil }
