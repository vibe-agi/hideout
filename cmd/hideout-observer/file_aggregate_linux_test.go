//go:build linux

package main

import (
	"math"
	"testing"
	"time"

	filecollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/file"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestFileObservationAggregatorPreservesCountsBytesAndBounds(t *testing.T) {
	t.Parallel()

	config := observerTestConfig(t)
	first := observerFileEvent(
		config,
		"/workspace/input.json",
		filecollector.EventRead,
		11,
		100,
	)
	second := observerFileEvent(
		config,
		"/workspace/input.json",
		filecollector.EventRead,
		29,
		140,
	)
	second.CPU = 3
	second.MonotonicNS = 20_000
	aggregator := newFileObservationAggregator()
	for _, event := range []filecollector.Event{first, second} {
		added, err := aggregator.Add(event)
		if err != nil || !added {
			t.Fatalf("add=%t error=%v", added, err)
		}
	}
	var emitted []observerRecord
	normalizer, err := filecollector.NewNormalizer(filecollector.Boundary{
		Owner: config.Binding.Owner, SessionID: config.Binding.SessionID,
		CgroupID:           config.Binding.CgroupID,
		ObserverGeneration: config.Binding.ObserverGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := aggregator.Flush(
		normalizer,
		func(record observerRecord) error {
			emitted = append(emitted, record)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	if len(emitted) != 1 || aggregator.Len() != 0 {
		t.Fatalf("emitted=%d pending=%d", len(emitted), aggregator.Len())
	}
	got := emitted[0]
	if got.Record.Count != 2 ||
		got.Record.Bytes != 40 ||
		got.Record.FirstSequence != 100 ||
		got.Record.LastSequence != 140 ||
		!got.Record.FirstAt.Equal(first.At) ||
		!got.Record.LastAt.Equal(second.At) ||
		got.CPU != 3 ||
		got.MonotonicNS != 20_000 {
		t.Fatalf("aggregated observation=%+v", got)
	}
	if err := got.Record.Validate(); err != nil {
		t.Fatalf("aggregated record is invalid: %v", err)
	}
}

func TestFileObservationAggregatorKeepsSemanticBoundaries(t *testing.T) {
	t.Parallel()

	config := observerTestConfig(t)
	events := []filecollector.Event{
		observerFileEvent(
			config,
			"/workspace/a",
			filecollector.EventRead,
			1,
			1,
		),
		observerFileEvent(
			config,
			"/workspace/b",
			filecollector.EventRead,
			1,
			2,
		),
	}
	events[1].Actor = workloadtypes.Actor{
		ExecutionID: "exec_fileboundary0002",
		PID:         5252,
		UID:         1000,
		GID:         1000,
	}
	aggregator := newFileObservationAggregator()
	for index, event := range events {
		added, err := aggregator.Add(event)
		if err != nil || !added {
			t.Fatalf("add[%d]=%t error=%v", index, added, err)
		}
	}
	if aggregator.Len() != 2 {
		t.Fatalf("semantic boundaries collapsed: pending=%d", aggregator.Len())
	}

	destructive := observerFileEvent(
		config,
		"/workspace/a",
		filecollector.EventUnlink,
		0,
		3,
	)
	if added, err := aggregator.Add(destructive); err != nil || added {
		t.Fatalf("destructive add=%t error=%v", added, err)
	}
}

func TestFileObservationAggregatorFailsClosedOnCounterOverflow(t *testing.T) {
	t.Parallel()

	config := observerTestConfig(t)
	first := observerFileEvent(
		config,
		"/workspace/input",
		filecollector.EventRead,
		math.MaxUint64,
		1,
	)
	second := observerFileEvent(
		config,
		"/workspace/input",
		filecollector.EventRead,
		1,
		2,
	)
	aggregator := newFileObservationAggregator()
	if added, err := aggregator.Add(first); err != nil || !added {
		t.Fatalf("first add=%t error=%v", added, err)
	}
	if added, err := aggregator.Add(second); err !=
		errObserverFileAggregationOverflow || added {
		t.Fatalf("overflow add=%t error=%v", added, err)
	}
}

func observerFileEvent(
	config observerConfig,
	path string,
	kind filecollector.EventKind,
	bytes uint64,
	sequence uint64,
) filecollector.Event {
	at := time.Unix(10, int64(sequence)).UTC()
	return filecollector.Event{
		Kind:  kind,
		Owner: config.Binding.Owner, SessionID: config.Binding.SessionID,
		CgroupID:           config.Binding.CgroupID,
		ObserverGeneration: config.Binding.ObserverGeneration,
		Sequence:           sequence,
		CPU:                2,
		MonotonicNS:        10_000 + sequence,
		At:                 at,
		Actor: workloadtypes.Actor{
			ExecutionID: "exec_fileaggregate001",
			PID:         4242,
			UID:         1000,
			GID:         1000,
		},
		Attribution: workloadtypes.AttributionExact,
		Path:        path,
		PathState:   "resolved",
		PathClass:   "workspace",
		FileType:    "regular",
		Device:      8,
		Inode:       9001,
		Bytes:       bytes,
		Outcome: workloadtypes.Outcome{
			Status: workloadtypes.OutcomeSucceeded,
		},
		CoverageID: observerFileCoverageID,
	}
}
