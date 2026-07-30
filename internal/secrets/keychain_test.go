package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestKeychainStoreSetRotateDeleteAndMissingLifecycle(t *testing.T) {
	backend := newMemoryKeychainBackend()
	store := newKeychainTestStore(backend)
	ctx := context.Background()

	missing, err := store.Reference(ctx, "local-proxy")
	if err != nil {
		t.Fatal(err)
	}
	if missing.Availability != AvailabilityMissing ||
		missing.Generation != 0 ||
		missing.Reason != "secret-missing" {
		t.Fatalf("missing reference=%+v", missing)
	}

	firstCanary := "socks5://canary-user:canary-password@127.0.0.1:7890"
	firstBuffer := mustSecretBuffer(t, firstCanary)
	first, err := store.Set(ctx, WriteRequest{
		Ref: "local-proxy", OperationID: "op_keychainset01",
		ExpectedGeneration: 0, Value: firstBuffer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Availability != AvailabilityAvailable || first.Generation != 1 {
		t.Fatalf("first reference=%+v", first)
	}
	assertSecretBufferCleared(t, firstBuffer)
	assertBytesCleared(t, backend.lastPutArgument(), "encoded write argument")
	assertPublicReferenceHasNoCanary(t, first, firstCanary)
	if got := resolveSecret(t, store, "local-proxy"); got != firstCanary {
		t.Fatalf("resolved first secret=%q", got)
	}
	assertBytesCleared(t, backend.lastGetResult(), "encoded read result")

	staleBuffer := mustSecretBuffer(t, "socks5://stale.invalid:9999")
	_, err = store.Set(ctx, WriteRequest{
		Ref: "local-proxy", OperationID: "op_keychainstale1",
		ExpectedGeneration: 0, Value: staleBuffer,
	})
	var conflict *GenerationConflictError
	if !errors.As(err, &conflict) ||
		conflict.Ref != "local-proxy" ||
		conflict.Expected != 0 ||
		conflict.Current != 1 {
		t.Fatalf("stale rotate error=%v conflict=%+v", err, conflict)
	}
	assertSecretBufferCleared(t, staleBuffer)

	secondCanary := "socks5h://rotated-user:rotated-password@127.0.0.1:7891"
	secondBuffer := mustSecretBuffer(t, secondCanary)
	second, err := store.Set(ctx, WriteRequest{
		Ref: "local-proxy", OperationID: "op_keychainrotate1",
		ExpectedGeneration: 1, Value: secondBuffer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != 2 ||
		resolveSecret(t, store, "local-proxy") != secondCanary {
		t.Fatalf("rotated reference=%+v", second)
	}
	assertSecretBufferCleared(t, secondBuffer)

	references, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 1 ||
		references[0].Ref != "local-proxy" ||
		references[0].Generation != 2 {
		t.Fatalf("listed references=%+v", references)
	}

	deleted, err := store.Delete(ctx, DeleteRequest{
		Ref: "local-proxy", OperationID: "op_keychaindelete1",
		ExpectedGeneration: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Availability != AvailabilityMissing ||
		deleted.Generation != 3 ||
		deleted.Reason != "secret-deleted" {
		t.Fatalf("deleted reference=%+v", deleted)
	}
	raw := backend.item("local-proxy")
	for _, canary := range []string{firstCanary, secondCanary} {
		if bytes.Contains(raw, []byte(canary)) {
			t.Fatalf("delete tombstone retained secret canary %q", canary)
		}
	}
	if _, err := store.Resolve(ctx, "local-proxy"); !errors.Is(err, ErrSecretMissing) {
		t.Fatalf("resolve deleted error=%v", err)
	}

	putsBeforeReplay := backend.putCount()
	replayed, err := store.Delete(ctx, DeleteRequest{
		Ref: "local-proxy", OperationID: "op_keychaindelete1",
		ExpectedGeneration: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Generation != 3 || backend.putCount() != putsBeforeReplay {
		t.Fatalf(
			"delete replay=%+v puts=%d want=%d",
			replayed,
			backend.putCount(),
			putsBeforeReplay,
		)
	}
	if _, err := store.Delete(ctx, DeleteRequest{
		Ref: "local-proxy", OperationID: "op_keychaindelete2",
		ExpectedGeneration: 3,
	}); !errors.Is(err, ErrSecretMissing) {
		t.Fatalf("new delete of tombstone error=%v", err)
	}

	recreatedBuffer := mustSecretBuffer(t, "socks5://recreated@127.0.0.1:7892")
	recreated, err := store.Set(ctx, WriteRequest{
		Ref: "local-proxy", OperationID: "op_keychainreset01",
		ExpectedGeneration: 3, Value: recreatedBuffer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if recreated.Generation != 4 ||
		recreated.Availability != AvailabilityAvailable {
		t.Fatalf("recreated reference=%+v", recreated)
	}
}

func TestKeychainStoreRecoversCommittedSetAndDeleteAfterResponseLoss(t *testing.T) {
	backend := newMemoryKeychainBackend()
	store := newKeychainTestStore(backend)
	responseLost := errors.New("simulated keychain response loss")
	canary := "socks5://response-user:response-password@127.0.0.1:7890"
	request := WriteRequest{
		Ref: "local-proxy", OperationID: "op_keychaincrash01",
		ExpectedGeneration: 0, Value: mustSecretBuffer(t, canary),
	}
	backend.failNextPutAfterCommit(responseLost)

	if _, err := store.Set(context.Background(), request); !errors.Is(err, responseLost) {
		t.Fatalf("first set error=%v", err)
	}
	assertSecretBufferCleared(t, request.Value)
	if backend.putCount() != 1 {
		t.Fatalf("first set put count=%d", backend.putCount())
	}

	restarted := newKeychainTestStore(backend)
	reconciled, err := restarted.Reconcile(context.Background(), ReconcileRequest{
		Ref: "local-proxy", Action: ActionSet,
		OperationID: "op_keychaincrash01", ExpectedGeneration: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reconciled.Committed || reconciled.Reference.Generation != 1 {
		t.Fatalf("set reconcile without secret=%+v", reconciled)
	}
	retryBuffer := mustSecretBuffer(t, canary)
	replayed, err := restarted.Set(context.Background(), WriteRequest{
		Ref: "local-proxy", OperationID: "op_keychaincrash01",
		ExpectedGeneration: 0, Value: retryBuffer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Generation != 1 || backend.putCount() != 1 {
		t.Fatalf("set replay=%+v puts=%d", replayed, backend.putCount())
	}
	assertSecretBufferCleared(t, retryBuffer)

	mismatch := mustSecretBuffer(t, "socks5://different.invalid:1")
	if _, err := restarted.Set(context.Background(), WriteRequest{
		Ref: "local-proxy", OperationID: "op_keychaincrash01",
		ExpectedGeneration: 0, Value: mismatch,
	}); !errors.Is(err, ErrSecretOperationMismatch) {
		t.Fatalf("mismatched set replay error=%v", err)
	}
	assertSecretBufferCleared(t, mismatch)
	if strings.Contains(
		normalizeKeychainBackendError(responseLost).Error(),
		canary,
	) {
		t.Fatal("provider error exposed the secret canary")
	}

	backend.failNextPutAfterCommit(responseLost)
	deleteRequest := DeleteRequest{
		Ref: "local-proxy", OperationID: "op_keychaindelcrash",
		ExpectedGeneration: 1,
	}
	if _, err := restarted.Delete(
		context.Background(),
		deleteRequest,
	); !errors.Is(err, responseLost) {
		t.Fatalf("first delete error=%v", err)
	}
	if backend.putCount() != 2 {
		t.Fatalf("delete put count=%d", backend.putCount())
	}
	restartedAgain := newKeychainTestStore(backend)
	deleteReconcile, err := restartedAgain.Reconcile(
		context.Background(),
		ReconcileRequest{
			Ref: "local-proxy", Action: ActionDelete,
			OperationID: "op_keychaindelcrash", ExpectedGeneration: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !deleteReconcile.Committed ||
		deleteReconcile.Reference.Generation != 2 {
		t.Fatalf("delete reconcile without secret=%+v", deleteReconcile)
	}
	deleted, err := restartedAgain.Delete(context.Background(), deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Availability != AvailabilityMissing ||
		deleted.Generation != 2 ||
		backend.putCount() != 2 {
		t.Fatalf("delete replay=%+v puts=%d", deleted, backend.putCount())
	}
	if bytes.Contains(backend.item("local-proxy"), []byte(canary)) {
		t.Fatal("recovered delete retained the secret canary")
	}
}

func TestKeychainReconcileDistinguishesCommitNegativeProofAndUnknown(
	t *testing.T,
) {
	backend := newMemoryKeychainBackend()
	store := newKeychainTestStore(backend)
	ctx := context.Background()
	missing, err := store.Reconcile(ctx, ReconcileRequest{
		Ref: "local-proxy", Action: ActionSet,
		OperationID: "op_keychainnegative1", ExpectedGeneration: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if missing.Committed || !missing.Uncommitted {
		t.Fatalf("missing base was not an exact negative proof: %+v", missing)
	}
	value := mustSecretBuffer(
		t,
		"socks5://negative-proof.invalid:7890",
	)
	if _, err := store.Set(ctx, WriteRequest{
		Ref: "local-proxy", OperationID: "op_keychainbase0001",
		ExpectedGeneration: 0, Value: value,
	}); err != nil {
		t.Fatal(err)
	}
	negative, err := store.Reconcile(ctx, ReconcileRequest{
		Ref: "local-proxy", Action: ActionDelete,
		OperationID: "op_keychainnegative2", ExpectedGeneration: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if negative.Committed || !negative.Uncommitted {
		t.Fatalf("exact current generation was not a negative proof: %+v", negative)
	}
	unknown, err := store.Reconcile(ctx, ReconcileRequest{
		Ref: "local-proxy", Action: ActionDelete,
		OperationID: "op_keychainunknown01", ExpectedGeneration: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Committed || unknown.Uncommitted {
		t.Fatalf("generation mismatch was falsely proved: %+v", unknown)
	}
}

func TestKeychainStoreReportsLockedMissingAndCorruptWithoutLeaking(t *testing.T) {
	backend := newMemoryKeychainBackend()
	store := newKeychainTestStore(backend)
	backend.setGetError(ErrSecretLocked)

	locked, err := store.Reference(context.Background(), "local-proxy")
	if err != nil {
		t.Fatal(err)
	}
	if locked.Availability != AvailabilityLocked ||
		locked.Reason != "keychain-locked" {
		t.Fatalf("locked reference=%+v", locked)
	}
	buffer := mustSecretBuffer(t, "socks5://locked-user:locked-password@localhost:1")
	if _, err := store.Set(context.Background(), WriteRequest{
		Ref: "local-proxy", OperationID: "op_keychainlocked1",
		ExpectedGeneration: 0, Value: buffer,
	}); !errors.Is(err, ErrSecretLocked) {
		t.Fatalf("locked set error=%v", err)
	}
	assertSecretBufferCleared(t, buffer)
	if _, err := store.Resolve(
		context.Background(),
		"local-proxy",
	); !errors.Is(err, ErrSecretLocked) {
		t.Fatalf("locked resolve error=%v", err)
	}

	backend.setGetError(nil)
	if _, err := store.Delete(context.Background(), DeleteRequest{
		Ref: "missing-proxy", OperationID: "op_keychainmissing1",
		ExpectedGeneration: 0,
	}); !errors.Is(err, ErrSecretMissing) {
		t.Fatalf("delete missing error=%v", err)
	}

	corruptCanary := "corrupt-user:corrupt-password"
	backend.setItem("corrupt-proxy", []byte(corruptCanary))
	if _, err := store.Reference(
		context.Background(),
		"corrupt-proxy",
	); !errors.Is(err, ErrSecretEnvelopeCorrupt) {
		t.Fatalf("corrupt reference error=%v", err)
	} else if strings.Contains(err.Error(), corruptCanary) {
		t.Fatal("corrupt-envelope error exposed stored bytes")
	}
	assertBytesCleared(t, backend.lastGetResult(), "corrupt read result")
}

func TestKeychainEnvelopeRejectsSecretBearingTombstoneAndTrailingData(t *testing.T) {
	envelope := keychainEnvelope{
		State: keychainEnvelopeAvailable, Ref: "local-proxy",
		OperationID: "op_keychaincodec01", BaseGeneration: 0,
		Generation: 1,
		UpdatedAt: time.Date(
			2026, 7, 29, 18, 0, 0, 0, time.UTC,
		),
		Value: []byte("codec-canary"),
	}
	data, err := encodeKeychainEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeKeychainEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded.Value) != "codec-canary" ||
		decoded.Ref != envelope.Ref ||
		decoded.Generation != envelope.Generation {
		t.Fatalf("decoded envelope=%+v", decoded)
	}
	decoded.clear()
	clear(data)

	tombstone := envelope
	tombstone.State = keychainEnvelopeDeleted
	if _, err := encodeKeychainEnvelope(tombstone); !errors.Is(err, ErrSecretEnvelopeCorrupt) {
		t.Fatalf("secret-bearing tombstone error=%v", err)
	}
	valid, err := encodeKeychainEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	valid = append(valid, 0)
	if _, err := decodeKeychainEnvelope(valid); !errors.Is(err, ErrSecretEnvelopeCorrupt) {
		t.Fatalf("trailing envelope error=%v", err)
	}
	clear(valid)
}

func newKeychainTestStore(
	backend *memoryKeychainBackend,
) *keychainEnvelopeStore {
	return newKeychainEnvelopeStore(backend, func() time.Time {
		return time.Date(2026, 7, 29, 17, 0, 0, 0, time.UTC)
	})
}

func mustSecretBuffer(t *testing.T, value string) *Buffer {
	t.Helper()
	buffer, err := NewBuffer([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return buffer
}

func resolveSecret(
	t *testing.T,
	store RuntimeResolver,
	ref string,
) string {
	t.Helper()
	buffer, err := store.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	var result string
	if err := buffer.Use(func(value []byte) error {
		result = string(value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertSecretBufferCleared(t, buffer)
	return result
}

func assertSecretBufferCleared(t *testing.T, buffer *Buffer) {
	t.Helper()
	if buffer == nil || !buffer.used {
		t.Fatal("secret buffer was not consumed")
	}
	assertBytesCleared(t, buffer.value, "secret buffer")
}

func assertBytesCleared(t *testing.T, data []byte, label string) {
	t.Helper()
	for _, value := range data {
		if value != 0 {
			t.Fatalf("%s was not cleared", label)
		}
	}
}

func assertPublicReferenceHasNoCanary(
	t *testing.T,
	reference Reference,
	canary string,
) {
	t.Helper()
	data, err := json.Marshal(reference)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		canary,
		"canary-user",
		"canary-password",
		"value",
		"digest",
		"hash",
	} {
		if strings.Contains(strings.ToLower(string(data)), strings.ToLower(forbidden)) {
			t.Fatalf("public reference contains %q: %s", forbidden, data)
		}
	}
}

type memoryKeychainBackend struct {
	mu sync.Mutex

	items              map[string][]byte
	getErr             error
	putErrBeforeCommit error
	putErrAfterCommit  error
	puts               int
	lastPut            []byte
	lastGet            []byte
}

func newMemoryKeychainBackend() *memoryKeychainBackend {
	return &memoryKeychainBackend{items: map[string][]byte{}}
}

func (backend *memoryKeychainBackend) Accounts(
	ctx context.Context,
) ([]string, error) {
	if err := checkSecretContext(ctx); err != nil {
		return nil, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	accounts := make([]string, 0, len(backend.items))
	for account := range backend.items {
		accounts = append(accounts, account)
	}
	sort.Strings(accounts)
	return accounts, nil
}

func (backend *memoryKeychainBackend) Get(
	ctx context.Context,
	ref string,
) ([]byte, error) {
	if err := checkSecretContext(ctx); err != nil {
		return nil, err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.getErr != nil {
		return nil, backend.getErr
	}
	value, exists := backend.items[ref]
	if !exists {
		return nil, errKeychainItemMissing
	}
	result := append([]byte(nil), value...)
	backend.lastGet = result
	return result, nil
}

func (backend *memoryKeychainBackend) Put(
	ctx context.Context,
	ref string,
	data []byte,
) error {
	if err := checkSecretContext(ctx); err != nil {
		return err
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.lastPut = data
	if backend.putErrBeforeCommit != nil {
		err := backend.putErrBeforeCommit
		backend.putErrBeforeCommit = nil
		return err
	}
	backend.items[ref] = append([]byte(nil), data...)
	backend.puts++
	if backend.putErrAfterCommit != nil {
		err := backend.putErrAfterCommit
		backend.putErrAfterCommit = nil
		return err
	}
	return nil
}

func (backend *memoryKeychainBackend) failNextPutAfterCommit(err error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.putErrAfterCommit = err
}

func (backend *memoryKeychainBackend) setGetError(err error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.getErr = err
}

func (backend *memoryKeychainBackend) setItem(ref string, value []byte) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.items[ref] = append([]byte(nil), value...)
}

func (backend *memoryKeychainBackend) item(ref string) []byte {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return append([]byte(nil), backend.items[ref]...)
}

func (backend *memoryKeychainBackend) putCount() int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.puts
}

func (backend *memoryKeychainBackend) lastPutArgument() []byte {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.lastPut
}

func (backend *memoryKeychainBackend) lastGetResult() []byte {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.lastGet
}
