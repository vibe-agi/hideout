package redact

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/secrets"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const SnapshotSchema = "hideout.activity-redaction-snapshot.v1"

var (
	ErrSnapshotUnavailable = errors.New(
		"activity redaction snapshot is unavailable",
	)
	snapshotControlIDPattern = regexp.MustCompile(
		`^[a-z][a-z0-9._-]{0,127}$`,
	)
)

// ManagedSecretSource is the daemon-only part of managed secret storage needed
// to freeze one redaction operation. Manager routes never receive this
// interface because it includes runtime value resolution.
type ManagedSecretSource interface {
	List(context.Context) ([]secrets.Reference, error)
	Reference(context.Context, string) (secrets.Reference, error)
	Resolve(context.Context, string) (*secrets.Buffer, error)
}

type ControlToken struct {
	ID         string
	Generation uint64
	Value      []byte
}

type ControlTokenSource interface {
	SnapshotControlTokens(context.Context) ([]ControlToken, error)
}

type SecretGeneration struct {
	Ref        string `json:"ref"`
	Generation uint64 `json:"generation"`
}

type ControlGeneration struct {
	ID         string `json:"id"`
	Generation uint64 `json:"generation"`
}

type SnapshotMetadata struct {
	Schema             string              `json:"schema"`
	ID                 string              `json:"id"`
	SecretGenerations  []SecretGeneration  `json:"secretGenerations"`
	ControlGenerations []ControlGeneration `json:"controlGenerations"`
	CreatedAt          time.Time           `json:"createdAt"`
}

// Snapshot is immutable while in use. Clear is an explicit terminal operation
// that destroys all protected variants and makes subsequent redaction fail
// closed.
type Snapshot struct {
	mu       sync.RWMutex
	metadata SnapshotMetadata
	redactor *Redactor
	cleared  bool
}

type Builder struct {
	Secrets       ManagedSecretSource
	ControlTokens ControlTokenSource
	Now           func() time.Time

	MaxValueBytes  int
	MaxOutputBytes int
	MaxArguments   int
}

func (builder Builder) Build(ctx context.Context) (*Snapshot, error) {
	if ctx == nil {
		return nil, errors.Join(ErrSnapshotUnavailable, context.Canceled)
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrSnapshotUnavailable, err)
	}
	now := time.Now().UTC()
	if builder.Now != nil {
		now = builder.Now().UTC()
	}
	if now.IsZero() {
		return nil, errors.Join(ErrSnapshotUnavailable, ErrInvalidConfig)
	}

	secretValues, secretGenerations, err := builder.snapshotSecrets(ctx)
	if err != nil {
		clearByteSlices(secretValues)
		return nil, errors.Join(ErrSnapshotUnavailable, err)
	}
	controlValues, controlGenerations, err := builder.snapshotControls(ctx)
	if err != nil {
		clearByteSlices(secretValues)
		clearStrings(controlValues)
		return nil, errors.Join(ErrSnapshotUnavailable, err)
	}
	defer clearByteSlices(secretValues)
	defer clearStrings(controlValues)

	redactor, err := New(Config{
		KnownSecrets:   secretValues,
		ControlTokens:  controlValues,
		MaxValueBytes:  builder.MaxValueBytes,
		MaxOutputBytes: builder.MaxOutputBytes,
		MaxArguments:   builder.MaxArguments,
	})
	if err != nil {
		return nil, errors.Join(ErrSnapshotUnavailable, err)
	}
	metadata := SnapshotMetadata{
		Schema: SnapshotSchema,
		SecretGenerations: append(
			[]SecretGeneration(nil),
			secretGenerations...,
		),
		ControlGenerations: append(
			[]ControlGeneration(nil),
			controlGenerations...,
		),
		CreatedAt: now,
	}
	metadata.ID, err = snapshotID(metadata)
	if err != nil {
		redactor.Clear()
		return nil, errors.Join(ErrSnapshotUnavailable, err)
	}
	return &Snapshot{metadata: metadata, redactor: redactor}, nil
}

func (builder Builder) snapshotSecrets(
	ctx context.Context,
) ([][]byte, []SecretGeneration, error) {
	if builder.Secrets == nil {
		return [][]byte{}, []SecretGeneration{}, nil
	}
	references, err := builder.Secrets.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	references = append([]secrets.Reference(nil), references...)
	secrets.SortReferences(references)
	values := make([][]byte, 0, len(references))
	generations := make([]SecretGeneration, 0, len(references))
	seen := make(map[string]struct{}, len(references))
	for _, before := range references {
		if err := ctx.Err(); err != nil {
			clearByteSlices(values)
			return nil, nil, err
		}
		if err := before.Validate(); err != nil {
			clearByteSlices(values)
			return nil, nil, err
		}
		if _, exists := seen[before.Ref]; exists {
			clearByteSlices(values)
			return nil, nil, secrets.ErrSecretEnvelopeCorrupt
		}
		seen[before.Ref] = struct{}{}
		if before.Availability != secrets.AvailabilityAvailable {
			continue
		}
		buffer, err := builder.Secrets.Resolve(ctx, before.Ref)
		if err != nil {
			clearByteSlices(values)
			return nil, nil, err
		}
		var value []byte
		err = buffer.Use(func(raw []byte) error {
			if len(raw) == 0 {
				return ErrInvalidConfig
			}
			value = append([]byte(nil), raw...)
			return nil
		})
		if err != nil {
			clear(value)
			clearByteSlices(values)
			return nil, nil, err
		}
		after, err := builder.Secrets.Reference(ctx, before.Ref)
		if err != nil {
			clear(value)
			clearByteSlices(values)
			return nil, nil, err
		}
		if err := after.Validate(); err != nil ||
			after.Ref != before.Ref ||
			after.Availability != secrets.AvailabilityAvailable ||
			after.Generation != before.Generation {
			clear(value)
			clearByteSlices(values)
			return nil, nil, secrets.ErrSecretGenerationMismatch
		}
		values = append(values, value)
		generations = append(generations, SecretGeneration{
			Ref: before.Ref, Generation: before.Generation,
		})
	}
	return values, generations, nil
}

func (builder Builder) snapshotControls(
	ctx context.Context,
) ([]string, []ControlGeneration, error) {
	if builder.ControlTokens == nil {
		return []string{}, []ControlGeneration{}, nil
	}
	tokens, err := builder.ControlTokens.SnapshotControlTokens(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		for index := range tokens {
			clear(tokens[index].Value)
		}
	}()
	slices.SortFunc(tokens, func(left, right ControlToken) int {
		if left.ID != right.ID {
			if left.ID < right.ID {
				return -1
			}
			return 1
		}
		if left.Generation < right.Generation {
			return -1
		}
		if left.Generation > right.Generation {
			return 1
		}
		return 0
	})
	values := make([]string, 0, len(tokens))
	generations := make([]ControlGeneration, 0, len(tokens))
	for index, token := range tokens {
		if err := ctx.Err(); err != nil {
			clearStrings(values)
			return nil, nil, err
		}
		if !snapshotControlIDPattern.MatchString(token.ID) ||
			token.Generation == 0 || len(token.Value) == 0 {
			clearStrings(values)
			return nil, nil, ErrInvalidConfig
		}
		if index > 0 && token.ID == tokens[index-1].ID &&
			token.Generation == tokens[index-1].Generation {
			clearStrings(values)
			return nil, nil, ErrInvalidConfig
		}
		values = append(values, string(token.Value))
		generations = append(generations, ControlGeneration{
			ID: token.ID, Generation: token.Generation,
		})
	}
	return values, generations, nil
}

func (snapshot *Snapshot) Metadata() SnapshotMetadata {
	if snapshot == nil {
		return SnapshotMetadata{}
	}
	snapshot.mu.RLock()
	defer snapshot.mu.RUnlock()
	metadata := snapshot.metadata
	metadata.SecretGenerations = append(
		[]SecretGeneration(nil),
		snapshot.metadata.SecretGenerations...,
	)
	metadata.ControlGenerations = append(
		[]ControlGeneration(nil),
		snapshot.metadata.ControlGenerations...,
	)
	return metadata
}

func (snapshot *Snapshot) Argv(
	arguments []string,
) ([]string, []string, error) {
	if snapshot == nil {
		return nil, nil, ErrRedactionFailed
	}
	snapshot.mu.RLock()
	defer snapshot.mu.RUnlock()
	if snapshot.cleared || snapshot.redactor == nil {
		return nil, nil, ErrRedactionFailed
	}
	return snapshot.redactor.Argv(arguments)
}

func (snapshot *Snapshot) Text(
	value string,
) (string, []string, error) {
	if snapshot == nil {
		return "", nil, ErrRedactionFailed
	}
	snapshot.mu.RLock()
	defer snapshot.mu.RUnlock()
	if snapshot.cleared || snapshot.redactor == nil {
		return "", nil, ErrRedactionFailed
	}
	return snapshot.redactor.Text(value)
}

func (snapshot *Snapshot) Activity(
	record workloadtypes.ActivityRecord,
) (workloadtypes.ActivityRecord, error) {
	if snapshot == nil {
		return workloadtypes.ActivityRecord{}, ErrRedactionFailed
	}
	snapshot.mu.RLock()
	defer snapshot.mu.RUnlock()
	if snapshot.cleared || snapshot.redactor == nil {
		return workloadtypes.ActivityRecord{}, ErrRedactionFailed
	}
	return snapshot.redactor.Activity(record)
}

func (snapshot *Snapshot) Execution(
	execution workloadtypes.Execution,
) (workloadtypes.Execution, error) {
	if snapshot == nil {
		return workloadtypes.Execution{}, ErrRedactionFailed
	}
	snapshot.mu.RLock()
	defer snapshot.mu.RUnlock()
	if snapshot.cleared || snapshot.redactor == nil {
		return workloadtypes.Execution{}, ErrRedactionFailed
	}
	return snapshot.redactor.Execution(execution)
}

func (snapshot *Snapshot) Clear() {
	if snapshot == nil {
		return
	}
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	if snapshot.cleared {
		return
	}
	snapshot.cleared = true
	if snapshot.redactor != nil {
		snapshot.redactor.Clear()
		snapshot.redactor = nil
	}
}

func snapshotID(metadata SnapshotMetadata) (string, error) {
	payload := struct {
		Schema             string              `json:"schema"`
		SecretGenerations  []SecretGeneration  `json:"secretGenerations"`
		ControlGenerations []ControlGeneration `json:"controlGenerations"`
	}{
		Schema:             metadata.Schema,
		SecretGenerations:  metadata.SecretGenerations,
		ControlGenerations: metadata.ControlGenerations,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode redaction snapshot identity: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "red_" + base64.RawURLEncoding.EncodeToString(sum[:18]), nil
}

func clearByteSlices(values [][]byte) {
	for index := range values {
		clear(values[index])
	}
	clear(values)
}

func clearStrings(values []string) {
	for index := range values {
		values[index] = ""
	}
	clear(values)
}
