//go:build darwin && cgo && keychainreal

package secrets

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestRealMacOSKeychainSetRotateDeleteAndRestartReconcile(
	t *testing.T,
) {
	if os.Getenv("HIDEOUT_KEYCHAIN_REAL") != "1" {
		t.Skip("set HIDEOUT_KEYCHAIN_REAL=1 to mutate an isolated Keychain item")
	}
	ctx := context.Background()
	suffix := randomKeychainTestSuffix(t)
	ref := "hideout-test-" + suffix
	service := KeychainServiceName + ".test." + suffix
	store := newDarwinKeychainStore(service)
	backend, ok := store.backend.(*darwinKeychainBackend)
	if !ok || backend.service != service {
		t.Fatalf("unexpected Keychain backend: %#v", store.backend)
	}
	t.Cleanup(func() {
		if err := backend.deleteItem(
			context.Background(),
			ref,
		); err != nil {
			t.Errorf("delete real Keychain fixture: %v", err)
		}
	})

	if err := backend.deleteItem(ctx, ref); err != nil {
		t.Fatal(err)
	}
	missing, err := store.Reference(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Availability != AvailabilityMissing ||
		missing.Generation != 0 {
		t.Fatalf("initial real Keychain reference=%+v", missing)
	}

	firstCanary := "socks5://real-canary-user:real-canary-password@127.0.0.1:7890"
	firstBuffer := mustSecretBuffer(t, firstCanary)
	first, err := store.Set(ctx, WriteRequest{
		Ref: ref, OperationID: "op_realkeychainset1",
		ExpectedGeneration: 0, Value: firstBuffer,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSecretBufferCleared(t, firstBuffer)
	if first.Generation != 1 ||
		resolveSecret(t, store, ref) != firstCanary {
		t.Fatalf("real Keychain set reference=%+v", first)
	}
	assertRealReferenceHasNoCanary(t, first, "real-canary")

	restarted := newDarwinKeychainStore(service)
	retryBuffer := mustSecretBuffer(t, firstCanary)
	replayed, err := restarted.Set(ctx, WriteRequest{
		Ref: ref, OperationID: "op_realkeychainset1",
		ExpectedGeneration: 0, Value: retryBuffer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Generation != 1 {
		t.Fatalf("real set reconcile=%+v", replayed)
	}
	assertSecretBufferCleared(t, retryBuffer)

	secondCanary := "socks5h://real-rotate-user:real-rotate-password@127.0.0.1:7891"
	secondBuffer := mustSecretBuffer(t, secondCanary)
	second, err := restarted.Set(ctx, WriteRequest{
		Ref: ref, OperationID: "op_realkeychainrotate",
		ExpectedGeneration: 1, Value: secondBuffer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != 2 ||
		resolveSecret(t, restarted, ref) != secondCanary {
		t.Fatalf("real rotate reference=%+v", second)
	}

	deleted, err := restarted.Delete(ctx, DeleteRequest{
		Ref: ref, OperationID: "op_realkeychaindelete",
		ExpectedGeneration: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Availability != AvailabilityMissing ||
		deleted.Generation != 3 {
		t.Fatalf("real delete reference=%+v", deleted)
	}
	if _, err := restarted.Resolve(ctx, ref); !errors.Is(err, ErrSecretMissing) {
		t.Fatalf("real deleted resolve error=%v", err)
	}

	restartedAgain := newDarwinKeychainStore(service)
	deleteReplay, err := restartedAgain.Delete(ctx, DeleteRequest{
		Ref: ref, OperationID: "op_realkeychaindelete",
		ExpectedGeneration: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if deleteReplay.Generation != 3 {
		t.Fatalf("real delete reconcile=%+v", deleteReplay)
	}
	references, err := restartedAgain.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, reference := range references {
		if reference.Ref == ref {
			found = reference.Availability == AvailabilityMissing &&
				reference.Generation == 3
		}
	}
	if !found {
		t.Fatalf("real tombstone missing from Keychain metadata: %+v", references)
	}
}

func randomKeychainTestSuffix(t *testing.T) string {
	t.Helper()
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw[:])
}

func assertRealReferenceHasNoCanary(
	t *testing.T,
	reference Reference,
	canary string,
) {
	t.Helper()
	data, err := json.Marshal(reference)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(data)), strings.ToLower(canary)) {
		t.Fatalf("real Keychain metadata contains secret canary: %s", data)
	}
}
