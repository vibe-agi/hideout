package manager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/recovery"
	"github.com/vibe-agi/hideout/internal/session"
)

func TestEnvironmentStopAndCleanRefuseLiveOwners(t *testing.T) {
	for _, action := range []string{EnvironmentActionStop, EnvironmentActionClean} {
		t.Run(action, func(t *testing.T) {
			core, store, record := concurrentLifecycleFixture(t)
			owner := acquireLifecycleOwner(t, store, record)
			defer owner.Close()

			var plan EnvironmentActionPlan
			var err error
			if action == EnvironmentActionStop {
				plan, err = core.PlanEnvironmentStop(EnvironmentActionOptions{IDs: []string{record.ID}})
			} else {
				plan, err = core.PlanEnvironmentClean(EnvironmentActionOptions{IDs: []string{record.ID}})
			}
			if err != nil {
				t.Fatal(err)
			}
			operator := &fakeEnvironmentOperator{}
			if action == EnvironmentActionStop {
				_, err = core.ApplyEnvironmentStop(t.Context(), plan, EnvironmentApplyOptions{Operator: operator})
			} else {
				_, err = core.ApplyEnvironmentClean(t.Context(), plan, EnvironmentApplyOptions{Operator: operator})
			}
			if err == nil || EnvironmentRecoveryCode(err) != recovery.CodeEnvironmentActiveSessions {
				t.Fatalf("live-owner %s error=%v recovery=%q", action, err, EnvironmentRecoveryCode(err))
			}
			if strings.Contains(err.Error(), store.OwnerRoot(record.ID)) || strings.Contains(err.Error(), "owner.lock") {
				t.Fatalf("owner refusal leaked implementation paths: %v", err)
			}
			if len(operator.stopped) != 0 || len(operator.cleaned) != 0 {
				t.Fatalf("operator ran despite live owner: %+v", operator)
			}
		})
	}
}

func TestEnvironmentStopRefusesUnprovableOwner(t *testing.T) {
	core, store, record := concurrentLifecycleFixture(t)
	id := "ses_20260716T120000Z_abcdefabcdefabcd"
	dir := filepath.Join(store.OwnerRoot(record.ID), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "owner.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := core.PlanEnvironmentStop(EnvironmentActionOptions{IDs: []string{record.ID}})
	if err != nil {
		t.Fatal(err)
	}
	operator := &fakeEnvironmentOperator{}
	_, err = core.ApplyEnvironmentStop(t.Context(), plan, EnvironmentApplyOptions{Operator: operator})
	if err == nil || EnvironmentRecoveryCode(err) != recovery.CodeSessionOwnerUnprovable {
		t.Fatalf("unprovable error=%v recovery=%q", err, EnvironmentRecoveryCode(err))
	}
	if len(operator.stopped) != 0 {
		t.Fatalf("operator ran despite unprovable owner: %+v", operator.stopped)
	}
}

func TestEnvironmentStopReconcilesStaleOwnerAndExactRuntime(t *testing.T) {
	core, store, record := concurrentLifecycleFixture(t)
	id := "ses_20260716T120000Z_0123456789abcdef"
	if _, err := store.PrepareSessionRuntime(record.ID, id); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store.OwnerRoot(record.ID), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	data, err := json.Marshal(session.OwnerRecord{
		Schema: session.ActiveSessionSchema, SessionID: id, EnvironmentID: record.ID,
		Profile: record.Profile, Backend: record.Backend, WorkspaceID: strings.Repeat("a", 64),
		State: session.OwnerStateRunning, TerminalMode: session.TerminalNone,
		StartedAt: now, UpdatedAt: now, CommandClass: "sleep",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "owner.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	plan, err := core.PlanEnvironmentStop(EnvironmentActionOptions{IDs: []string{record.ID}})
	if err != nil {
		t.Fatal(err)
	}
	operator := &fakeEnvironmentOperator{}
	if _, err := core.ApplyEnvironmentStop(t.Context(), plan, EnvironmentApplyOptions{Operator: operator}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.RuntimeSessionDir(record.ID, id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale session runtime remains: %v", err)
	}
	if len(operator.stopped) != 1 || operator.stopped[0] != record.InstanceName {
		t.Fatalf("stop calls=%+v", operator.stopped)
	}
}

func TestEnvironmentStopRecoversFailedOwnerOnlyAfterInstanceStops(t *testing.T) {
	core, store, record := concurrentLifecycleFixture(t)
	id := "ses_20260716T120000Z_abcdef0123456789"
	if _, err := store.PrepareSessionRuntime(record.ID, id); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store.OwnerRoot(record.ID), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	data, err := json.Marshal(session.OwnerRecord{
		Schema: session.ActiveSessionSchema, SessionID: id, EnvironmentID: record.ID,
		Profile: record.Profile, Backend: record.Backend, WorkspaceID: strings.Repeat("b", 64),
		State: session.OwnerStateFailed, TerminalMode: session.TerminalNone,
		StartedAt: now, UpdatedAt: now, CommandClass: "bash", CleanupError: "cleanup proof failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "owner.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	plan, err := core.PlanEnvironmentStop(EnvironmentActionOptions{IDs: []string{record.ID}})
	if err != nil {
		t.Fatal(err)
	}
	operator := &fakeEnvironmentOperator{}
	if _, err := core.ApplyEnvironmentStop(t.Context(), plan, EnvironmentApplyOptions{Operator: operator}); err != nil {
		t.Fatal(err)
	}
	if len(operator.stopped) != 1 || operator.stopped[0] != record.InstanceName {
		t.Fatalf("stop calls=%+v", operator.stopped)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed owner remains after proved stop: %v", err)
	}
	if _, err := os.Stat(store.RuntimeSessionDir(record.ID, id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed owner runtime remains after proved stop: %v", err)
	}
}

func TestEnvironmentStopRetainsFailedOwnerWhenInstanceStopFails(t *testing.T) {
	core, store, record := concurrentLifecycleFixture(t)
	id := "ses_20260716T120000Z_fedcba9876543210"
	dir := filepath.Join(store.OwnerRoot(record.ID), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	data, err := json.Marshal(session.OwnerRecord{
		Schema: session.ActiveSessionSchema, SessionID: id, EnvironmentID: record.ID,
		Profile: record.Profile, Backend: record.Backend, WorkspaceID: strings.Repeat("c", 64),
		State: session.OwnerStateFailed, TerminalMode: session.TerminalNone,
		StartedAt: now, UpdatedAt: now, CommandClass: "bash", CleanupError: "cleanup proof failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "owner.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	plan, err := core.PlanEnvironmentStop(EnvironmentActionOptions{IDs: []string{record.ID}})
	if err != nil {
		t.Fatal(err)
	}
	operator := &fakeEnvironmentOperator{stopErr: errors.New("stop failed")}
	if _, err := core.ApplyEnvironmentStop(t.Context(), plan, EnvironmentApplyOptions{Operator: operator}); err == nil {
		t.Fatal("stop failure should be returned")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("failed owner was removed without stop proof: %v", err)
	}
}

func TestEnvironmentStopLinearizesAfterAttachOwner(t *testing.T) {
	core, store, record := concurrentLifecycleFixture(t)
	plan, err := core.PlanEnvironmentStop(EnvironmentActionOptions{IDs: []string{record.ID}})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := store.Lock(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	operator := &fakeEnvironmentOperator{}
	done := make(chan error, 1)
	go func() {
		_, err := core.ApplyEnvironmentStop(context.Background(), plan, EnvironmentApplyOptions{Operator: operator})
		done <- err
	}()
	owner := acquireLifecycleOwner(t, store, record)
	defer owner.Close()
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || EnvironmentRecoveryCode(err) != recovery.CodeEnvironmentActiveSessions {
			t.Fatalf("stop won after attach owner: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not observe released transition lock")
	}
	if len(operator.stopped) != 0 {
		t.Fatalf("stop operator ran after attach linearized: %+v", operator.stopped)
	}
}

func TestEnvironmentStopInvalidatesPriorBootRuntimeState(t *testing.T) {
	core, store, record := concurrentLifecycleFixture(t)
	serviceState := filepath.Join(store.RuntimeNetworkServiceDir(record.ID), "state.json")
	activation := filepath.Join(store.RuntimeDir(record.ID), "activation.json")
	if err := os.WriteFile(serviceState, []byte("stale service\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activation, []byte("stale activation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := core.PlanEnvironmentStop(EnvironmentActionOptions{IDs: []string{record.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyEnvironmentStop(t.Context(), plan, EnvironmentApplyOptions{Operator: &fakeEnvironmentOperator{}}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{serviceState, activation} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("prior-boot state remains at %s: %v", path, err)
		}
	}
}

func TestForceRemoveAndRecreateLinearizeAgainstNewOwner(t *testing.T) {
	for _, operation := range []string{"remove", "recreate"} {
		t.Run(operation, func(t *testing.T) {
			core, store, record := concurrentLifecycleFixture(t)
			lock, err := store.Lock(record.ID)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				var opErr error
				if operation == "remove" {
					_, opErr = core.RemoveEnvironment(context.Background(), record.Name, true, EnvironmentApplyOptions{Operator: &fakeEnvironmentOperator{}})
				} else {
					_, opErr = core.RecreateEnvironment(context.Background(), record.Name, true, EnvironmentApplyOptions{Operator: &fakeEnvironmentOperator{}})
				}
				done <- opErr
			}()
			owner := acquireLifecycleOwner(t, store, record)
			defer owner.Close()
			if err := lock.Unlock(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil || EnvironmentRecoveryCode(err) != recovery.CodeEnvironmentActiveSessions {
					t.Fatalf("%s crossed owner transition: %v", operation, err)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("%s did not observe released transition lock", operation)
			}
		})
	}
}

func concurrentLifecycleFixture(t *testing.T) (Core, environment.Store, environment.Record) {
	t.Helper()
	profileStore := profile.Store{Root: t.TempDir()}
	store := environment.Store{Root: profileStore.Root}
	record, err := store.Create(environment.Spec{
		Name: "concurrent-lifecycle", ImageRef: environment.BuiltinBaseImage,
		Profile: "default", Backend: "lima", Workspace: t.TempDir(), GuestWorkspace: "/workspace",
		InstanceName: "hideout-concurrent-lifecycle",
	})
	if err != nil {
		t.Fatal(err)
	}
	record.Status = "running"
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareRuntimeRoot(record.ID); err != nil {
		t.Fatal(err)
	}
	return New(profileStore), store, record
}

func acquireLifecycleOwner(t *testing.T, store environment.Store, record environment.Record) *session.Owner {
	t.Helper()
	now := time.Now().UTC()
	owner, err := session.AcquireOwner(store.OwnerRoot(record.ID), session.OwnerRecord{
		Schema: session.ActiveSessionSchema, SessionID: "ses_20260716T120000Z_0123456789abcdef",
		EnvironmentID: record.ID, Profile: record.Profile, Backend: record.Backend,
		WorkspaceID: strings.Repeat("a", 64), State: session.OwnerStateRunning,
		TerminalMode: session.TerminalPTY, StartedAt: now, UpdatedAt: now, CommandClass: "bash",
	})
	if err != nil {
		t.Fatal(err)
	}
	return owner
}
