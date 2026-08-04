package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/vibe-agi/hideout/internal/migration"
)

func TestMigrationImportSecretPreparerWritesSelectedPlaintextDirectlyToProvider(t *testing.T) {
	plaintext := []byte("socks5://user:password@127.0.0.1:7890")
	action, manifest := migrationSelectedSecretPreparerFixture(plaintext)
	store := newManagerSecretStoreFixture()
	preparer, err := newMigrationImportSecretPreparer(
		context.Background(), "op_migration_secrettransfer1",
		[]migration.SecretAction{action}, manifest, store,
	)
	if err != nil {
		t.Fatal(err)
	}
	payload := migration.Record{
		Sequence: 1,
		Header: migration.PrivateRecordHeader{
			Type: migration.RecordSecretValue, ComponentID: action.ValueComponentID,
			Ordinal: 0, LogicalOffset: 0, PlaintextLength: uint64(len(plaintext)),
		},
		Plaintext: append([]byte(nil), plaintext...),
	}
	consumed, err := preparer.Consume(payload)
	if err != nil || !consumed {
		t.Fatalf("consume selected secret consumed=%t err=%v", consumed, err)
	}
	if store.writeCount() != 1 || !bytes.Equal(store.value, plaintext) {
		t.Fatalf("destination provider writes=%d value=%q", store.writeCount(), store.value)
	}
	if _, err := preparer.Prepared(); !errors.Is(err, ErrMigrationOperationInvalid) {
		t.Fatalf("secret became durable before authenticated checkpoint: %v", err)
	}
	if _, err := preparer.Consume(payload); err == nil {
		t.Fatal("duplicate secret payload was accepted")
	}
	checkpoint := migration.Record{
		Sequence: 2,
		Header: migration.PrivateRecordHeader{
			Type: migration.RecordCheckpoint, ComponentID: action.ValueComponentID,
		},
	}
	if consumed, err := preparer.Consume(checkpoint); err != nil || !consumed {
		t.Fatalf("consume checkpoint consumed=%t err=%v", consumed, err)
	}
	prepared, err := preparer.Prepared()
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 1 || prepared[0].SourceRef != action.SourceRef ||
		prepared[0].DestinationRef != action.DestinationRef ||
		prepared[0].Generation != 1 || prepared[0].OperationID == "" {
		t.Fatalf("prepared secret evidence=%+v", prepared)
	}
}

func TestMigrationImportSecretPreparerRejectsDigestChangeBeforeProviderWrite(t *testing.T) {
	action, manifest := migrationSelectedSecretPreparerFixture([]byte("expected"))
	store := newManagerSecretStoreFixture()
	preparer, err := newMigrationImportSecretPreparer(
		context.Background(), "op_migration_secrettransfer2",
		[]migration.SecretAction{action}, manifest, store,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = preparer.Consume(migration.Record{
		Sequence: 1,
		Header: migration.PrivateRecordHeader{
			Type: migration.RecordSecretValue, ComponentID: action.ValueComponentID,
			PlaintextLength: uint64(len("changed!")),
		},
		Plaintext: []byte("changed!"),
	})
	if err == nil || store.writeCount() != 0 {
		t.Fatalf("changed secret error=%v writes=%d", err, store.writeCount())
	}
}

func TestMigrationImportSecretPreparerIgnoresUnselectedSecretRecords(t *testing.T) {
	_, manifest := migrationSelectedSecretPreparerFixture([]byte("not-selected"))
	preparer, err := newMigrationImportSecretPreparer(
		context.Background(), "", nil, manifest, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	consumed, err := preparer.Consume(migration.Record{
		Sequence: 1,
		Header: migration.PrivateRecordHeader{
			Type: migration.RecordSecretValue, ComponentID: "component_secret01",
		},
		Plaintext: []byte("not-selected"),
	})
	if err != nil || consumed {
		t.Fatalf("unselected record consumed=%t err=%v", consumed, err)
	}
	prepared, err := preparer.Prepared()
	if err != nil || len(prepared) != 0 {
		t.Fatalf("unselected prepared=%+v err=%v", prepared, err)
	}

	t.Run("provider-specific opaque secret is rebind-only", func(t *testing.T) {
		action := migration.SecretAction{
			SourceRef:           "secret_opaque001",
			Transfer:            migration.SecretNonExportable,
			Decision:            migrationSecretDecisionExistingRef,
			SourceProvider:      "source-opaque-provider",
			DestinationProvider: "destination-keychain",
			DestinationRef:      "local-proxy",
			BaseGeneration:      1,
			EnvironmentRefs:     []migration.OpaqueID{"environment_source1"},
		}
		if err := validateMigrationSecretActions([]migration.SecretAction{action}); err != nil {
			t.Fatalf("opaque secret destination rebind was rejected: %v", err)
		}
		action.Decision = migrationSecretDecisionImportValue
		action.BaseGeneration = 0
		action.ValueComponentID = "component_secret01"
		if err := validateMigrationSecretActions([]migration.SecretAction{action}); err == nil {
			t.Fatal("opaque provider secret was accepted as a portable value")
		}
	})
}

func migrationSelectedSecretPreparerFixture(
	plaintext []byte,
) (migration.SecretAction, migration.Manifest) {
	digest := sha256.Sum256(plaintext)
	componentID := migration.OpaqueID("component_secret01")
	action := migration.SecretAction{
		SourceRef: "secret_source01", Transfer: migration.SecretSelectedValue,
		Decision:       migrationSecretDecisionImportValue,
		SourceProvider: "source-keychain", DestinationProvider: "memory-keychain",
		DestinationRef: "local-proxy", BaseGeneration: 0,
		ValueComponentID: componentID,
		EnvironmentRefs:  []migration.OpaqueID{"environment_source1"},
	}
	manifest := migration.Manifest{
		SecretEntries: []migration.SecretEntry{{
			SecretRef: action.SourceRef, Provider: action.SourceProvider,
			Transfer: migration.SecretSelectedValue, ValueComponentID: componentID,
			EnvironmentRefs: append([]migration.OpaqueID(nil), action.EnvironmentRefs...),
		}},
		ComponentIndex: []migration.ComponentIndexEntry{{
			ComponentID: componentID, Kind: "secret-value",
			LogicalBytes: uint64(len(plaintext)), FirstRecord: 1, LastRecord: 2,
			RecordCount:   2,
			ContentDigest: migration.Digest("sha256:" + hex.EncodeToString(digest[:])),
		}},
	}
	return action, manifest
}
