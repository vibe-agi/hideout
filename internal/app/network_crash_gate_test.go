package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
)

const realLimaNetworkCrashExitCode = 86

var realLimaNetworkCrashEffects = []string{
	"network-stage",
	"network-probe",
	"network-activate",
	"network-prove",
	"network-drain",
}

// TestRealLimaNetworkCrashGateDaemon is compiled into a private gate-only test
// binary. Normal `go test` runs skip it, and the installed hideout binary has no
// environment-triggered crash path. The gate process uses the production app
// wiring and exits only after Manager has durably completed the selected live
// network effect.
func TestRealLimaNetworkCrashGateDaemon(t *testing.T) {
	if os.Getenv("HIDEOUT_REAL_LIMA_NETWORK_CRASH_GATE") != "1" {
		t.Skip("real Lima network crash gate is not enabled")
	}
	target := os.Getenv("HIDEOUT_NETWORK_CRASH_EFFECT")
	if !slices.Contains(realLimaNetworkCrashEffects, target) {
		t.Fatalf("invalid network crash effect %q", target)
	}
	readyPath := cleanAbsoluteGatePath(
		t,
		os.Getenv("HIDEOUT_NETWORK_CRASH_READY"),
	)
	markerPath := cleanAbsoluteGatePath(
		t,
		os.Getenv("HIDEOUT_NETWORK_CRASH_MARKER"),
	)
	if readyPath == markerPath {
		t.Fatal("network crash ready and marker paths must differ")
	}
	store, err := profile.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	gateApp := app{
		stdout: os.Stdout,
		stderr: os.Stderr,
		stdin:  os.Stdin,
	}
	gateSecrets := loadRealLimaGateSecretStore(t)
	defer gateSecrets.clear()
	opts := gateApp.daemonOptions(store, 15*time.Minute)
	opts.SecretStore = gateSecrets
	opts.NetworkTransitionCheckpoints = &realLimaNetworkCrashCheckpoint{
		target: target,
		marker: markerPath,
	}
	running, err := daemon.Start(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivateGateJSON(readyPath, map[string]any{
		"schema": "hideout.network-crash-daemon-ready/v1",
		"pid":    os.Getpid(),
		"socket": running.Socket(),
		"effect": target,
	}); err != nil {
		_ = running.Stop(context.Background())
		t.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			daemonShutdownTimeout,
		)
		defer cancel()
		if err := running.Stop(shutdownCtx); err != nil {
			t.Fatal(err)
		}
	case <-running.Done():
	}
}

type realLimaGateSecretStore struct {
	ref        string
	value      []byte
	generation uint64
	updatedAt  time.Time
}

func loadRealLimaGateSecretStore(t *testing.T) *realLimaGateSecretStore {
	t.Helper()
	secretPath := cleanAbsoluteGatePath(
		t,
		os.Getenv("HIDEOUT_NETWORK_CRASH_SECRET_FILE"),
	)
	info, err := os.Lstat(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() ||
		info.Mode().Perm() != 0o600 ||
		info.Size() <= 0 ||
		info.Size() > 16<<10 {
		t.Fatal("network crash gate secret file is not a bounded private file")
	}
	ref := os.Getenv("HIDEOUT_NETWORK_CRASH_SECRET_REF")
	if err := secrets.ValidateRef(ref); err != nil {
		t.Fatal(err)
	}
	generation, err := strconv.ParseUint(
		os.Getenv("HIDEOUT_NETWORK_CRASH_SECRET_GENERATION"),
		10,
		64,
	)
	if err != nil || generation == 0 {
		t.Fatal("network crash gate secret generation is invalid")
	}
	value, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	rawValue := value
	value = append([]byte(nil), bytes.TrimSpace(rawValue)...)
	clear(rawValue)
	if len(value) == 0 {
		t.Fatal("network crash gate secret value is empty")
	}
	return &realLimaGateSecretStore{
		ref:        ref,
		value:      value,
		generation: generation,
		updatedAt:  time.Now().Round(0).UTC(),
	}
}

func (store *realLimaGateSecretStore) Provider() string {
	return "gate-private-file"
}

func (store *realLimaGateSecretStore) List(
	ctx context.Context,
) ([]secrets.Reference, error) {
	if err := gateSecretContext(ctx); err != nil {
		return nil, err
	}
	reference, err := store.reference(store.ref)
	if err != nil {
		return nil, err
	}
	return []secrets.Reference{reference}, nil
}

func (store *realLimaGateSecretStore) Reference(
	ctx context.Context,
	ref string,
) (secrets.Reference, error) {
	if err := gateSecretContext(ctx); err != nil {
		return secrets.Reference{}, err
	}
	return store.reference(ref)
}

func (store *realLimaGateSecretStore) Set(
	_ context.Context,
	request secrets.WriteRequest,
) (secrets.Reference, error) {
	if request.Value != nil {
		request.Value.Clear()
	}
	return secrets.Reference{}, secrets.ErrProviderUnavailable
}

func (store *realLimaGateSecretStore) Delete(
	context.Context,
	secrets.DeleteRequest,
) (secrets.Reference, error) {
	return secrets.Reference{}, secrets.ErrProviderUnavailable
}

func (store *realLimaGateSecretStore) Resolve(
	ctx context.Context,
	ref string,
) (*secrets.Buffer, error) {
	if err := gateSecretContext(ctx); err != nil {
		return nil, err
	}
	if store == nil || ref != store.ref || len(store.value) == 0 {
		return nil, secrets.ErrSecretMissing
	}
	return secrets.NewBuffer(store.value)
}

func (store *realLimaGateSecretStore) reference(
	ref string,
) (secrets.Reference, error) {
	if store == nil || ref != store.ref || len(store.value) == 0 {
		return secrets.Reference{}, secrets.ErrSecretMissing
	}
	reference := secrets.Reference{
		Schema:       secrets.SecretReferenceSchema,
		Ref:          store.ref,
		Provider:     store.Provider(),
		Availability: secrets.AvailabilityAvailable,
		Generation:   store.generation,
		UpdatedAt:    store.updatedAt,
	}
	return reference, reference.Validate()
}

func (store *realLimaGateSecretStore) clear() {
	if store == nil {
		return
	}
	clear(store.value)
	store.value = nil
}

func gateSecretContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

type realLimaNetworkCrashCheckpoint struct {
	target string
	marker string
}

func (checkpoint *realLimaNetworkCrashCheckpoint) BeforeNetworkTransitionEffect(
	context.Context,
	manager.NetworkTransitionPlan,
	manager.PlannedEffect,
) error {
	return nil
}

func (checkpoint *realLimaNetworkCrashCheckpoint) AfterNetworkTransitionEffect(
	_ context.Context,
	plan manager.NetworkTransitionPlan,
	effect manager.EffectResult,
) error {
	if checkpoint == nil ||
		effect.ID != checkpoint.target ||
		effect.Status != manager.EffectSucceeded {
		return nil
	}
	if len(effect.Evidence) != 1 {
		return errors.New(
			"network crash boundary lacks one exact durable evidence reference",
		)
	}
	if err := writePrivateGateJSON(checkpoint.marker, map[string]any{
		"schema":        "hideout.network-crash-boundary/v1",
		"pid":           os.Getpid(),
		"environmentId": plan.EnvironmentID,
		"planDigest":    plan.PlanDigest,
		"kind":          plan.Kind,
		"effect":        effect.ID,
		"status":        effect.Status,
		"evidence":      effect.Evidence,
	}); err != nil {
		return err
	}
	os.Exit(realLimaNetworkCrashExitCode)
	return nil
}

func cleanAbsoluteGatePath(t *testing.T, candidate string) string {
	t.Helper()
	if candidate == "" ||
		!filepath.IsAbs(candidate) ||
		filepath.Clean(candidate) != candidate {
		t.Fatalf("gate path is not clean and absolute: %q", candidate)
	}
	return candidate
}

func writePrivateGateJSON(path string, value any) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(parent, ".network-crash-*.json")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	keep = true
	directory, err := os.Open(parent)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil &&
		!errors.Is(err, syscall.EINVAL) {
		return err
	}
	return nil
}

var _ manager.NetworkTransitionEffectCheckpoint = (*realLimaNetworkCrashCheckpoint)(nil)
var _ secrets.RuntimeStore = (*realLimaGateSecretStore)(nil)
