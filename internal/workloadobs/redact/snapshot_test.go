package redact

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/secrets"
)

func TestSnapshotBuilderFreezesManagedAndControlGenerations(t *testing.T) {
	now := time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)
	managed := newSnapshotSecretSource(now)
	managed.set("local-proxy", 3, "managed-value-alpha-045")
	managed.set("unused-missing", 2, "")
	controls := &snapshotControlSource{tokens: []ControlToken{{
		ID: "daemon-operator", Generation: 7,
		Value: []byte("token_snapshot_control_alpha_045"),
	}}}
	builder := Builder{
		Secrets: managed, ControlTokens: controls,
		Now: func() time.Time { return now },
	}

	first, err := builder.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Clear)
	metadata := first.Metadata()
	if metadata.Schema != SnapshotSchema || metadata.ID == "" ||
		!metadata.CreatedAt.Equal(now) ||
		!slices.Equal(metadata.SecretGenerations, []SecretGeneration{{
			Ref: "local-proxy", Generation: 3,
		}}) ||
		!slices.Equal(metadata.ControlGenerations, []ControlGeneration{{
			ID: "daemon-operator", Generation: 7,
		}}) {
		t.Fatalf("snapshot metadata=%+v", metadata)
	}

	arguments, _, err := first.Argv([]string{
		"agent",
		"--label", url.QueryEscape("managed-value-alpha-045"),
		"--header", "token_snapshot_control_alpha_045",
		"--api-key", "split-field-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(arguments, "\x00")
	for _, forbidden := range []string{
		"managed-value-alpha-045",
		url.QueryEscape("managed-value-alpha-045"),
		"token_snapshot_control_alpha_045",
		"split-field-value",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("snapshot retained %q in argv=%q", forbidden, arguments)
		}
	}

	now = now.Add(time.Millisecond)
	sameGenerations, err := builder.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer sameGenerations.Clear()
	if sameGenerations.Metadata().ID != first.Metadata().ID {
		t.Fatalf("wall clock changed redaction generation: first=%s same=%s",
			first.Metadata().ID, sameGenerations.Metadata().ID)
	}

	managed.set("local-proxy", 4, "managed-value-beta-045")
	controls.set([]ControlToken{{
		ID: "daemon-operator", Generation: 8,
		Value: []byte("token_snapshot_control_beta_045"),
	}})
	now = now.Add(time.Second)
	second, err := builder.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Clear)
	if second.Metadata().ID == metadata.ID {
		t.Fatal("generation change did not produce a new snapshot identity")
	}

	oldSafe, _, err := first.Text(
		"managed-value-alpha-045 managed-value-beta-045",
	)
	if err != nil {
		t.Fatal(err)
	}
	if oldSafe != Replacement+" managed-value-beta-045" {
		t.Fatalf("old immutable snapshot changed: %q", oldSafe)
	}
	newSafe, _, err := second.Text(
		"managed-value-alpha-045 managed-value-beta-045",
	)
	if err != nil {
		t.Fatal(err)
	}
	if newSafe != "managed-value-alpha-045 "+Replacement {
		t.Fatalf("new snapshot generations are wrong: %q", newSafe)
	}

	metadata.SecretGenerations[0].Generation = 999
	if first.Metadata().SecretGenerations[0].Generation != 3 {
		t.Fatal("metadata caller mutated the immutable snapshot")
	}
}

func TestSnapshotBuilderFailsClosedAcrossGenerationDriftAndInvalidControlMaterial(t *testing.T) {
	now := time.Date(2026, 7, 29, 13, 30, 0, 0, time.UTC)
	managed := newSnapshotSecretSource(now)
	managed.set("rotating", 1, "managed-value-drift-045")
	managed.drift = true
	builder := Builder{
		Secrets: managed,
		ControlTokens: &snapshotControlSource{tokens: []ControlToken{{
			ID: "daemon-operator", Generation: 1,
			Value: []byte("token_snapshot_control_drift_045"),
		}}},
		Now: func() time.Time { return now },
	}
	if snapshot, err := builder.Build(context.Background()); snapshot != nil ||
		!errors.Is(err, ErrSnapshotUnavailable) {
		t.Fatalf("generation drift snapshot=%v error=%v", snapshot, err)
	}

	managed.drift = false
	builder.ControlTokens = &snapshotControlSource{tokens: []ControlToken{
		{
			ID: "daemon-operator", Generation: 2,
			Value: []byte("token_snapshot_control_duplicate_a"),
		},
		{
			ID: "daemon-operator", Generation: 2,
			Value: []byte("token_snapshot_control_duplicate_b"),
		},
	}}
	if snapshot, err := builder.Build(context.Background()); snapshot != nil ||
		!errors.Is(err, ErrSnapshotUnavailable) {
		t.Fatalf("duplicate control snapshot=%v error=%v", snapshot, err)
	}
}

type snapshotSecretValue struct {
	generation uint64
	value      []byte
	updatedAt  time.Time
}

type snapshotSecretSource struct {
	mu     sync.Mutex
	values map[string]snapshotSecretValue
	drift  bool
}

func newSnapshotSecretSource(_ time.Time) *snapshotSecretSource {
	return &snapshotSecretSource{values: make(map[string]snapshotSecretValue)}
}

func (source *snapshotSecretSource) set(
	ref string,
	generation uint64,
	value string,
) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.values[ref] = snapshotSecretValue{
		generation: generation,
		value:      []byte(value),
		updatedAt:  time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
	}
}

func (source *snapshotSecretSource) List(
	context.Context,
) ([]secrets.Reference, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	refs := make([]string, 0, len(source.values))
	for ref := range source.values {
		refs = append(refs, ref)
	}
	slices.Sort(refs)
	result := make([]secrets.Reference, 0, len(refs))
	for _, ref := range refs {
		result = append(result, source.referenceLocked(ref))
	}
	return result, nil
}

func (source *snapshotSecretSource) Reference(
	_ context.Context,
	ref string,
) (secrets.Reference, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	reference := source.referenceLocked(ref)
	if source.drift && reference.Availability == secrets.AvailabilityAvailable {
		reference.Generation++
	}
	return reference, nil
}

func (source *snapshotSecretSource) Resolve(
	_ context.Context,
	ref string,
) (*secrets.Buffer, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	value, ok := source.values[ref]
	if !ok || len(value.value) == 0 {
		return nil, secrets.ErrSecretMissing
	}
	return secrets.NewBuffer(value.value)
}

func (source *snapshotSecretSource) referenceLocked(
	ref string,
) secrets.Reference {
	value, ok := source.values[ref]
	if !ok || len(value.value) == 0 {
		return secrets.Reference{
			Schema: secrets.SecretReferenceSchema,
			Ref:    ref, Provider: "fixture",
			Availability: secrets.AvailabilityMissing,
			Generation:   value.generation,
			Reason:       "secret-missing",
		}
	}
	return secrets.Reference{
		Schema: secrets.SecretReferenceSchema,
		Ref:    ref, Provider: "fixture",
		Availability: secrets.AvailabilityAvailable,
		Generation:   value.generation, UpdatedAt: value.updatedAt,
	}
}

type snapshotControlSource struct {
	mu     sync.Mutex
	tokens []ControlToken
}

func (source *snapshotControlSource) SnapshotControlTokens(
	context.Context,
) ([]ControlToken, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	result := make([]ControlToken, len(source.tokens))
	for index, token := range source.tokens {
		result[index] = token
		result[index].Value = append([]byte(nil), token.Value...)
	}
	return result, nil
}

func (source *snapshotControlSource) set(tokens []ControlToken) {
	source.mu.Lock()
	defer source.mu.Unlock()
	for index := range source.tokens {
		clear(source.tokens[index].Value)
	}
	source.tokens = make([]ControlToken, len(tokens))
	for index, token := range tokens {
		source.tokens[index] = token
		source.tokens[index].Value = append([]byte(nil), token.Value...)
	}
}
