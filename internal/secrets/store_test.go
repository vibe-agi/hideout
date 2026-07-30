package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestManagerSecretStoreHasNoReadValueMethod(t *testing.T) {
	storeType := reflect.TypeOf((*Store)(nil)).Elem()
	for _, forbidden := range []string{"Get", "Read", "Resolve", "Value"} {
		if _, exists := storeType.MethodByName(forbidden); exists {
			t.Fatalf("Manager-facing Store exposes %s", forbidden)
		}
	}
}

func TestSecretBufferIsOneUseAndClearedOnFailure(t *testing.T) {
	buffer, err := NewBuffer([]byte("fixture-secret"))
	if err != nil {
		t.Fatal(err)
	}
	callbackErr := errors.New("fixture callback failed")
	if err := buffer.Use(func(value []byte) error {
		if string(value) != "fixture-secret" {
			t.Fatalf("buffer value=%q", value)
		}
		return callbackErr
	}); !errors.Is(err, callbackErr) {
		t.Fatalf("Use error=%v want %v", err, callbackErr)
	}
	if err := buffer.Use(func([]byte) error { return nil }); !errors.Is(err, ErrSecretBufferUsed) {
		t.Fatalf("second Use error=%v want %v", err, ErrSecretBufferUsed)
	}
	for _, value := range buffer.value {
		if value != 0 {
			t.Fatal("secret buffer was not cleared")
		}
	}
}

func TestUnsupportedStoreReturnsMetadataWithoutSecretDerivedFields(t *testing.T) {
	store := UnsupportedStore{ProviderName: "macos-keychain", Reason: "platform-unsupported"}
	reference, err := store.Reference(context.Background(), "local-proxy")
	if err != nil {
		t.Fatal(err)
	}
	if reference.Availability != AvailabilityUnavailable || reference.Generation != 0 {
		t.Fatalf("unexpected unsupported reference: %+v", reference)
	}
	data, err := json.Marshal(reference)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"value", "digest", "hash"} {
		if strings.Contains(strings.ToLower(string(data)), forbidden) {
			t.Fatalf("reference contains secret-derived field %q: %s", forbidden, data)
		}
	}
	buffer, err := NewBuffer([]byte("must-be-cleared"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Set(context.Background(), WriteRequest{
		Ref: "local-proxy", OperationID: "op_fixture0001", Value: buffer,
	}); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("unsupported Set error=%v want %v", err, ErrProviderUnavailable)
	}
	for _, value := range buffer.value {
		if value != 0 {
			t.Fatal("unsupported provider retained write bytes")
		}
	}
}
