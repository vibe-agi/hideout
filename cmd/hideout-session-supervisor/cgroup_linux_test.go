//go:build linux

package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

func TestSessionCgroupCreatesFreshNonDelegatedLeafAndBindsAtomicExec(t *testing.T) {
	backend := newFakeSessionCgroupBackend()
	root := filepath.Join(t.TempDir(), "hideout")
	leaf, err := newSessionCgroup(sessionCgroupOptions{
		Root: root, SessionID: "ses_20260729T120000Z_cgroup", Backend: backend,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = leaf.Close() })

	wantPath := filepath.Join(root, "sessions", "ses_20260729T120000Z_cgroup")
	if leaf.Path() != wantPath || leaf.ID() != backend.created.ID {
		t.Fatalf("leaf path/id=%q/%d want %q/%d", leaf.Path(), leaf.ID(), wantPath, backend.created.ID)
	}
	if backend.created.Delegated || !backend.validated {
		t.Fatalf("leaf must be validated and non-delegated: %+v", backend.created)
	}

	attrs := &syscall.SysProcAttr{Setsid: true}
	if err := leaf.BindTarget(attrs); err != nil {
		t.Fatal(err)
	}
	if !attrs.UseCgroupFD || attrs.CgroupFD != backend.created.FD || !attrs.Setsid {
		t.Fatalf("atomic target attributes were not preserved and bound: %+v", attrs)
	}

	if _, err := newSessionCgroup(sessionCgroupOptions{
		Root: root, SessionID: "ses_20260729T120000Z_cgroup", Backend: backend,
	}); !errors.Is(err, errSessionCgroupExists) {
		t.Fatalf("reused leaf error=%v, want %v", err, errSessionCgroupExists)
	}
}

func TestSessionCgroupRejectsDelegationEscapeAndWrongFDIdentity(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		handle sessionCgroupHandle
		want   error
	}{
		{
			name: "target-writable delegation",
			handle: sessionCgroupHandle{
				FD: 10, ID: 111, Delegated: true,
			},
			want: errSessionCgroupDelegated,
		},
		{
			name: "fd path identity mismatch",
			handle: sessionCgroupHandle{
				FD: 10, ID: 111,
			},
			want: errSessionCgroupIdentity,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			backend := newFakeSessionCgroupBackend()
			backend.next = testCase.handle
			if errors.Is(testCase.want, errSessionCgroupIdentity) {
				backend.validateErr = errSessionCgroupIdentity
			}
			_, err := newSessionCgroup(sessionCgroupOptions{
				Root:      filepath.Join(t.TempDir(), "hideout"),
				SessionID: "ses_20260729T120001Z_escape",
				Backend:   backend,
			})
			if !errors.Is(err, testCase.want) {
				t.Fatalf("error=%v want %v", err, testCase.want)
			}
			if backend.closed != 1 {
				t.Fatalf("rejected handle close count=%d want 1", backend.closed)
			}
		})
	}
}

func TestSessionCgroupMembershipInheritsButPIDReuseCannotCrossIdentity(t *testing.T) {
	backend := newFakeSessionCgroupBackend()
	leaf, err := newSessionCgroup(sessionCgroupOptions{
		Root:      filepath.Join(t.TempDir(), "hideout"),
		SessionID: "ses_20260729T120002Z_membership",
		Backend:   backend,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = leaf.Close() })

	const reusedPID = uint32(4242)
	for _, pid := range []uint32{4000, 4001, 4002, reusedPID} {
		if !leaf.OwnsObservation(pid, leaf.ID()) {
			t.Fatalf("descendant pid %d in inherited leaf was rejected", pid)
		}
	}
	if leaf.OwnsObservation(reusedPID, leaf.ID()+1) {
		t.Fatal("same numeric PID from another cgroup was accepted after PID reuse")
	}
	if leaf.OwnsObservation(0, leaf.ID()) {
		t.Fatal("zero PID was accepted")
	}
}

func TestSessionCgroupCleanupRequiresExactEmptyProof(t *testing.T) {
	backend := newFakeSessionCgroupBackend()
	leaf, err := newSessionCgroup(sessionCgroupOptions{
		Root:      filepath.Join(t.TempDir(), "hideout"),
		SessionID: "ses_20260729T120003Z_cleanup",
		Backend:   backend,
	})
	if err != nil {
		t.Fatal(err)
	}
	backend.processes = []int{701, 702}
	if err := leaf.ProveEmptyAndRemove(); !errors.Is(err, errSessionCgroupNotEmpty) {
		t.Fatalf("non-empty cleanup error=%v want %v", err, errSessionCgroupNotEmpty)
	}
	if backend.removed {
		t.Fatal("non-empty leaf was removed")
	}

	backend.processes = nil
	if err := leaf.ProveEmptyAndRemove(); err != nil {
		t.Fatal(err)
	}
	if !backend.removed {
		t.Fatal("empty leaf was not removed")
	}
	if err := leaf.ProveEmptyAndRemove(); err != nil {
		t.Fatalf("exact cleanup retry must be idempotent: %v", err)
	}
}

type fakeSessionCgroupBackend struct {
	next        sessionCgroupHandle
	created     sessionCgroupHandle
	paths       map[string]bool
	processes   []int
	validated   bool
	validateErr error
	removed     bool
	closed      int
}

func newFakeSessionCgroupBackend() *fakeSessionCgroupBackend {
	return &fakeSessionCgroupBackend{
		next:  sessionCgroupHandle{FD: 91, ID: 314159},
		paths: map[string]bool{},
	}
}

func (backend *fakeSessionCgroupBackend) Create(path string) (sessionCgroupHandle, error) {
	if backend.paths[path] {
		return sessionCgroupHandle{}, errSessionCgroupExists
	}
	backend.paths[path] = true
	handle := backend.next
	handle.Close = func() error {
		backend.closed++
		return nil
	}
	backend.created = handle
	return handle, nil
}

func (backend *fakeSessionCgroupBackend) Validate(path string, handle sessionCgroupHandle) error {
	backend.validated = true
	if backend.validateErr != nil {
		return backend.validateErr
	}
	if path == "" || handle.FD < 0 || handle.ID == 0 {
		return errSessionCgroupIdentity
	}
	return nil
}

func (backend *fakeSessionCgroupBackend) Processes(path string, handle sessionCgroupHandle) ([]int, error) {
	if path == "" || handle.ID != backend.created.ID {
		return nil, errSessionCgroupIdentity
	}
	return append([]int(nil), backend.processes...), nil
}

func (backend *fakeSessionCgroupBackend) Remove(path string, handle sessionCgroupHandle) error {
	if backend.removed {
		return nil
	}
	if !reflect.DeepEqual(backend.processes, []int(nil)) && len(backend.processes) != 0 {
		return errSessionCgroupNotEmpty
	}
	delete(backend.paths, path)
	backend.removed = true
	return nil
}
