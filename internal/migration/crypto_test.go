package migration

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestArgon2idAndRecordKeyKnownAnswers(t *testing.T) {
	params := KDFParameters{MemoryKiB: 8 << 10, Passes: 1, Lanes: 1}
	wrappingKey, err := deriveWrappingKey(
		[]byte("correct horse battery staple"),
		bytes.Repeat([]byte{0x42}, KDFSaltBytes),
		params,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(wrappingKey)
	if got := hex.EncodeToString(wrappingKey); got !=
		"c8bbda82ddb108e361cc7736ffe1f620a2d0f605aa3f916e45c914e6d3a10c75" {
		t.Fatalf("Argon2id known answer=%s", got)
	}

	recordKey, err := DeriveRecordKey(
		bytes.Repeat([]byte{0x11}, MasterKeyBytes),
		"migb_fixture1234",
		7,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(recordKey)
	if got := hex.EncodeToString(recordKey); got !=
		"e61231b34d5d2645e4bac4c549f5f317a2a4fb369bbc9ac293a39b5897bbea5b" {
		t.Fatalf("HKDF known answer=%s", got)
	}
}

func TestMasterKeyWrapRoundTripWrongKeyAndTamper(t *testing.T) {
	params := KDFParameters{MemoryKiB: 8 << 10, Passes: 1, Lanes: 1}
	passphrase := []byte("migration passphrase")
	masterKey := bytes.Repeat([]byte{0x37}, MasterKeyBytes)
	salt := bytes.Repeat([]byte{0x51}, KDFSaltBytes)
	nonce := bytes.Repeat([]byte{0x62}, XNonceBytes)
	associatedData := []byte("authenticated public header")

	wrapped, err := WrapMasterKey(
		passphrase, masterKey, params, salt, nonce, associatedData,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(wrapped) != MasterKeyBytes+AEADTagBytes {
		t.Fatalf("wrapped master key bytes=%d", len(wrapped))
	}
	unwrapped, err := UnwrapMasterKey(
		passphrase, wrapped, params, salt, nonce, associatedData,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := unwrapped.Use(func(value []byte) error {
		if !bytes.Equal(value, masterKey) {
			t.Fatalf("unwrapped master key=%x", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !allZero(unwrapped.value) {
		t.Fatal("unwrapped master-key buffer was not cleared after use")
	}

	for name, attempt := range map[string]func() error{
		"wrong passphrase": func() error {
			_, openErr := UnwrapMasterKey(
				[]byte("different passphrase"), wrapped, params,
				salt, nonce, associatedData,
			)
			return openErr
		},
		"tampered ciphertext": func() error {
			tampered := append([]byte(nil), wrapped...)
			tampered[len(tampered)-1] ^= 0x80
			_, openErr := UnwrapMasterKey(
				passphrase, tampered, params, salt, nonce, associatedData,
			)
			return openErr
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := attempt()
			if !errors.Is(err, ErrAuthenticationFailed) ||
				CodeOf(err) != CodeAuthenticationFailed {
				t.Fatalf("authentication error=%v code=%q", err, CodeOf(err))
			}
			message := err.Error()
			if strings.Contains(message, "passphrase") ||
				strings.Contains(message, hex.EncodeToString(wrapped)) {
				t.Fatalf("authentication error leaked input: %q", message)
			}
		})
	}
}

func TestRecordSealBindsSequenceHeaderAndPayload(t *testing.T) {
	masterKey := bytes.Repeat([]byte{0x73}, MasterKeyBytes)
	recordKey, err := DeriveRecordKey(masterKey, "migb_fixture1234", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(recordKey)
	nonce := bytes.Repeat([]byte{0x29}, XNonceBytes)
	aad := []byte("frame-header-sequence-4")
	plaintext := []byte("migration record payload")
	ciphertext, err := SealRecord(recordKey, nonce, aad, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenRecord(recordKey, nonce, aad, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("record plaintext=%q", opened)
	}
	clear(opened)

	for name, altered := range map[string][]byte{
		"associated data": []byte("frame-header-sequence-5"),
		"nonce":           bytes.Repeat([]byte{0x30}, XNonceBytes),
	} {
		t.Run(name, func(t *testing.T) {
			candidateNonce := nonce
			candidateAAD := aad
			if name == "associated data" {
				candidateAAD = altered
			} else {
				candidateNonce = altered
			}
			if _, err := OpenRecord(
				recordKey, candidateNonce, candidateAAD, ciphertext,
			); !errors.Is(err, ErrAuthenticationFailed) {
				t.Fatalf("altered %s error=%v", name, err)
			}
		})
	}
}

func TestKDFBoundsNonceUniquenessAndSensitiveBufferCleanup(t *testing.T) {
	params := DefaultKDFParameters()
	params.MemoryKiB = HardMaxArgonMemoryKiB + 1
	if _, err := deriveWrappingKey(
		[]byte("passphrase"),
		bytes.Repeat([]byte{0x01}, KDFSaltBytes),
		params,
	); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("unbounded KDF error=%v", err)
	}

	tracker := NewNonceTracker()
	nonce := bytes.Repeat([]byte{0x44}, XNonceBytes)
	if err := tracker.Add(nonce); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Add(nonce); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("duplicate nonce error=%v", err)
	}

	buffer, err := NewSensitiveBuffer([]byte("replaceable secret bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if err := buffer.Use(func(value []byte) error {
		if string(value) != "replaceable secret bytes" {
			t.Fatalf("sensitive buffer=%q", value)
		}
		return errors.New("callback failure")
	}); err == nil {
		t.Fatal("sensitive callback failure was discarded")
	}
	if !allZero(buffer.value) {
		t.Fatal("sensitive buffer was not cleared on callback failure")
	}
	if err := buffer.Use(func([]byte) error { return nil }); !errors.Is(err, ErrSensitiveBufferUsed) {
		t.Fatalf("sensitive buffer reuse error=%v", err)
	}
}

func allZero(value []byte) bool {
	for _, current := range value {
		if current != 0 {
			return false
		}
	}
	return true
}
