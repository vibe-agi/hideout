package environment

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDisposableIdentityDigestIsCanonicalAndIgnoresMutableRunState(t *testing.T) {
	record := disposableIdentityRecord(t)
	identity, err := NewDisposableIdentity(record)
	if err != nil {
		t.Fatalf("NewDisposableIdentity: %v", err)
	}
	if identity.EnvironmentID != record.ID || identity.RecordVersion != RecordVersion ||
		identity.Backend != "lima" || identity.InstanceName != record.InstanceName ||
		identity.Mode != ModeDedicated || !identity.Disposable {
		t.Fatalf("identity=%+v", identity)
	}
	if identity.Digest != "e8056df995ee19d14e25d8fcd5a39f23b160848ab5632a9bafb0403882438778" {
		t.Fatalf("digest=%q", identity.Digest)
	}

	mutable := record
	mutable.Status = StatusError
	mutable.LastSessionID = "session-retry"
	mutable.LastCommand = "false"
	mutable.LastStartedAt = record.CreatedAt.Add(time.Minute)
	mutable.LastEndedAt = record.CreatedAt.Add(2 * time.Minute)
	retry, err := NewDisposableIdentity(mutable)
	if err != nil {
		t.Fatalf("retry identity: %v", err)
	}
	if retry != identity {
		t.Fatalf("mutable run state changed identity:\n got %+v\nwant %+v", retry, identity)
	}
}

func TestDisposableIdentityDigestChangesForEveryBoundField(t *testing.T) {
	base := disposableIdentityRecord(t)
	identity, err := NewDisposableIdentity(base)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*Record){
		"environment-id": func(record *Record) {
			record.ID = "env_20260717t000000zfedcba9876543210"
		},
		"backend": func(record *Record) {
			record.Backend = "native"
		},
		"profile": func(record *Record) {
			record.Profile = "other"
		},
		"machine-identity": func(record *Record) {
			record.MachineIdentityID = "sha256:" + repeatHex("b")
		},
		"instance": func(record *Record) {
			record.InstanceName = "hideout-other-instance"
		},
		"created-at": func(record *Record) {
			record.CreatedAt = record.CreatedAt.Add(time.Nanosecond)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			got, err := NewDisposableIdentity(changed)
			if err != nil {
				t.Fatalf("NewDisposableIdentity: %v", err)
			}
			if got.Digest == identity.Digest {
				t.Fatalf("%s did not change digest %q", name, got.Digest)
			}
		})
	}
}

func TestDisposableIdentityRejectsAuthorizationShortcuts(t *testing.T) {
	base := disposableIdentityRecord(t)
	tests := map[string]func(*Record){
		"not-disposable": func(record *Record) {
			record.Disposable = false
		},
		"name-only": func(record *Record) {
			record.Disposable = false
			record.Name = "rm-name-is-not-authority"
		},
		"status-only": func(record *Record) {
			record.Disposable = false
			record.Status = StatusError
		},
		"non-dedicated": func(record *Record) {
			record.Mode = ModeWorkspaceBound
			record.DedicatedWorkspace = ""
			record.DedicatedGuestRoot = ""
			record.BoundWorkspace = filepath.Clean(t.TempDir())
			record.BoundGuestRoot = "/workspace"
		},
		"missing-instance": func(record *Record) {
			record.InstanceName = ""
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			record := base
			mutate(&record)
			if identity, err := NewDisposableIdentity(record); err == nil {
				t.Fatalf("shortcut accepted: %+v", identity)
			}
		})
	}
}

func disposableIdentityRecord(t *testing.T) Record {
	t.Helper()
	return Record{
		Version:             RecordVersion,
		ID:                  "env_20260717t000000z0123456789abcdef",
		Name:                "disposable-test",
		ImageRef:            BuiltinBaseImage,
		Profile:             "default",
		Backend:             "lima",
		Mode:                ModeDedicated,
		MachineIdentityID:   "sha256:" + repeatHex("a"),
		BootConfigurationID: "sha256:" + repeatHex("c"),
		DedicatedWorkspace:  filepath.Clean(t.TempDir()),
		DedicatedGuestRoot:  "/workspace",
		InstanceName:        "hideout-default-env-disposable",
		Disposable:          true,
		Status:              StatusCreated,
		CreatedAt:           time.Date(2026, 7, 23, 1, 2, 3, 4, time.UTC),
	}
}

func repeatHex(value string) string {
	out := ""
	for len(out) < 64 {
		out += value
	}
	return out
}
