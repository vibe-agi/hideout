package main

import (
	"testing"

	filecollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/file"
)

func TestRetainFileObservationKeepsRelevantReadsAndEveryMutation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		event filecollector.Event
	}{
		{
			name: "workspace read",
			event: filecollector.Event{
				Kind: filecollector.EventRead,
				Path: "/workspace/project/go.mod",
			},
		},
		{
			name: "home open",
			event: filecollector.Event{
				Kind: filecollector.EventOpen,
				Path: "/home/lima/.ssh/config",
			},
		},
		{
			name: "configuration mmap",
			event: filecollector.Event{
				Kind: filecollector.EventMmap,
				Path: "/etc/passwd",
			},
		},
		{
			name: "similar prefix is not a system root",
			event: filecollector.Event{
				Kind: filecollector.EventRead,
				Path: "/usr-local/project/input",
			},
		},
		{
			name: "unresolved path is retained conservatively",
			event: filecollector.Event{
				Kind: filecollector.EventRead,
			},
		},
		{
			name: "system write",
			event: filecollector.Event{
				Kind: filecollector.EventWrite,
				Path: "/usr/lib/example",
			},
		},
		{
			name: "device create",
			event: filecollector.Event{
				Kind: filecollector.EventCreate,
				Path: "/dev/example",
			},
		},
		{
			name: "future kind fails visible",
			event: filecollector.Event{
				Kind: filecollector.EventKind("file.future"),
				Path: "/usr/lib/example",
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if !retainFileObservation(test.event) {
				t.Fatalf("event unexpectedly filtered: %+v", test.event)
			}
		})
	}
}

func TestRetainFileObservationFiltersOnlySystemRuntimeReads(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		event filecollector.Event
	}{
		{
			name: "binary open",
			event: filecollector.Event{
				Kind: filecollector.EventOpen,
				Path: "/usr/bin/true",
			},
		},
		{
			name: "library read",
			event: filecollector.Event{
				Kind: filecollector.EventRead,
				Path: "/lib/aarch64-linux-gnu/libc.so.6",
			},
		},
		{
			name: "library mmap",
			event: filecollector.Event{
				Kind: filecollector.EventMmap,
				Path: "/lib64/ld-linux-aarch64.so.1",
			},
		},
		{
			name: "kernel metadata",
			event: filecollector.Event{
				Kind: filecollector.EventRead,
				Path: "/proc/self/maps",
			},
		},
		{
			name: "device open",
			event: filecollector.Event{
				Kind: filecollector.EventOpen,
				Path: "/dev/null",
			},
		},
		{
			name: "loader cache",
			event: filecollector.Event{
				Kind: filecollector.EventMmap,
				Path: "/etc/ld.so.cache",
			},
		},
		{
			name: "exact root",
			event: filecollector.Event{
				Kind: filecollector.EventOpen,
				Path: "/usr",
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if retainFileObservation(test.event) {
				t.Fatalf("runtime event unexpectedly retained: %+v", test.event)
			}
		})
	}
}
