package migration

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	MasterKeyBytes   = 32
	WrappingKeyBytes = 32
	KDFSaltBytes     = 16
	XNonceBytes      = chacha20poly1305.NonceSizeX
	AEADTagBytes     = 16

	MinArgonMemoryKiB       uint32 = 8 << 10
	MaxPassphraseBytes             = 4096
	MaxSensitiveBufferBytes        = 16 << 20
)

var ErrSensitiveBufferUsed = errors.New("migration sensitive buffer was already used")

// SensitiveBuffer owns a replaceable byte slice, grants one synchronous use,
// and clears its storage on success, error, or panic.
type SensitiveBuffer struct {
	mu    sync.Mutex
	value []byte
	used  bool
}

func NewSensitiveBuffer(value []byte) (*SensitiveBuffer, error) {
	if len(value) == 0 || len(value) > MaxSensitiveBufferBytes {
		return nil, fmt.Errorf("%w: sensitive value is empty or unbounded", ErrInvalidBundle)
	}
	buffer := &SensitiveBuffer{value: make([]byte, len(value))}
	copy(buffer.value, value)
	return buffer, nil
}

func (buffer *SensitiveBuffer) Use(callback func([]byte) error) (err error) {
	if buffer == nil || callback == nil {
		return fmt.Errorf("%w: sensitive-buffer callback is required", ErrInvalidBundle)
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if buffer.used {
		return ErrSensitiveBufferUsed
	}
	buffer.used = true
	defer clear(buffer.value)
	return callback(buffer.value)
}

func (buffer *SensitiveBuffer) Clear() {
	if buffer == nil {
		return
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	clear(buffer.value)
	buffer.used = true
}

// DefaultKDFParameters is the v1 writer profile. Tests may use a lower but
// still accepted profile to keep local feedback bounded.
func DefaultKDFParameters() KDFParameters {
	return KDFParameters{MemoryKiB: 64 << 10, Passes: 3, Lanes: 4}
}

func (parameters KDFParameters) Validate() error {
	if parameters.MemoryKiB < MinArgonMemoryKiB ||
		parameters.Passes == 0 || parameters.Lanes == 0 {
		return fmt.Errorf("%w: Argon2id parameters are below the v1 floor", ErrInvalidBundle)
	}
	if parameters.MemoryKiB > HardMaxArgonMemoryKiB ||
		parameters.Passes > HardMaxArgonPasses ||
		parameters.Lanes > HardMaxArgonLanes {
		return fmt.Errorf("%w: Argon2id parameters exceed the v1 ceiling", ErrLimitExceeded)
	}
	if uint64(parameters.MemoryKiB)<<10 > HardMaxWorkingBytes {
		return fmt.Errorf("%w: Argon2id memory exceeds the working-set ceiling", ErrLimitExceeded)
	}
	return nil
}

func deriveWrappingKey(
	passphrase []byte,
	salt []byte,
	parameters KDFParameters,
) ([]byte, error) {
	if err := parameters.Validate(); err != nil {
		return nil, err
	}
	if len(passphrase) == 0 || len(passphrase) > MaxPassphraseBytes ||
		len(salt) != KDFSaltBytes {
		return nil, fmt.Errorf("%w: passphrase or KDF salt is invalid", ErrInvalidBundle)
	}
	return argon2.IDKey(
		passphrase,
		salt,
		parameters.Passes,
		parameters.MemoryKiB,
		parameters.Lanes,
		WrappingKeyBytes,
	), nil
}

// WrapMasterKey encrypts the random bundle master key. The caller retains
// ownership of passphrase and masterKey and must clear them at its outer scope.
func WrapMasterKey(
	passphrase []byte,
	masterKey []byte,
	parameters KDFParameters,
	salt []byte,
	nonce []byte,
	associatedData []byte,
) ([]byte, error) {
	if len(masterKey) != MasterKeyBytes || len(nonce) != XNonceBytes ||
		len(associatedData) == 0 || len(associatedData) > int(HardMaxHeaderBytes) {
		return nil, fmt.Errorf("%w: master-key wrap inputs are invalid", ErrInvalidBundle)
	}
	wrappingKey, err := deriveWrappingKey(passphrase, salt, parameters)
	if err != nil {
		return nil, err
	}
	defer clear(wrappingKey)
	aead, err := chacha20poly1305.NewX(wrappingKey)
	if err != nil {
		return nil, fmt.Errorf("%w: initialize master-key wrapper", ErrInvalidBundle)
	}
	return aead.Seal(nil, nonce, masterKey, associatedData), nil
}

// UnwrapMasterKey deliberately projects wrong passwords and authenticated
// header/ciphertext corruption to one public error class.
func UnwrapMasterKey(
	passphrase []byte,
	wrapped []byte,
	parameters KDFParameters,
	salt []byte,
	nonce []byte,
	associatedData []byte,
) (*SensitiveBuffer, error) {
	if len(wrapped) != MasterKeyBytes+AEADTagBytes ||
		len(nonce) != XNonceBytes || len(associatedData) == 0 ||
		len(associatedData) > int(HardMaxHeaderBytes) {
		return nil, authenticationError(errors.New("wrapped master-key envelope is invalid"))
	}
	wrappingKey, err := deriveWrappingKey(passphrase, salt, parameters)
	if err != nil {
		return nil, err
	}
	defer clear(wrappingKey)
	aead, err := chacha20poly1305.NewX(wrappingKey)
	if err != nil {
		return nil, authenticationError(err)
	}
	masterKey, err := aead.Open(nil, nonce, wrapped, associatedData)
	if err != nil {
		return nil, authenticationError(err)
	}
	buffer, err := NewSensitiveBuffer(masterKey)
	clear(masterKey)
	if err != nil {
		return nil, authenticationError(err)
	}
	return buffer, nil
}

// DeriveRecordKey creates a sequence-specific, purpose-separated key.
func DeriveRecordKey(
	masterKey []byte,
	bundleID BundleID,
	sequence uint64,
) ([]byte, error) {
	if len(masterKey) != MasterKeyBytes {
		return nil, fmt.Errorf("%w: bundle master key length is invalid", ErrInvalidBundle)
	}
	if _, err := ParseBundleID(string(bundleID)); err != nil {
		return nil, err
	}
	salt := sha256.Sum256(append(
		[]byte("hideout-migration/v1/bundle/"),
		[]byte(bundleID)...,
	))
	var sequenceBytes [8]byte
	binary.BigEndian.PutUint64(sequenceBytes[:], sequence)
	info := append([]byte("hideout-migration/v1/record-key/"), sequenceBytes[:]...)
	reader := hkdf.New(sha256.New, masterKey, salt[:], info)
	key := make([]byte, MasterKeyBytes)
	if _, err := io.ReadFull(reader, key); err != nil {
		clear(key)
		return nil, fmt.Errorf("%w: derive record key", ErrInvalidBundle)
	}
	return key, nil
}

func SealRecord(
	key []byte,
	nonce []byte,
	associatedData []byte,
	plaintext []byte,
) ([]byte, error) {
	if err := validateRecordCryptoInputs(key, nonce, associatedData, len(plaintext)); err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("%w: initialize record cipher", ErrInvalidBundle)
	}
	return aead.Seal(nil, nonce, plaintext, associatedData), nil
}

func OpenRecord(
	key []byte,
	nonce []byte,
	associatedData []byte,
	ciphertext []byte,
) ([]byte, error) {
	if len(ciphertext) < AEADTagBytes ||
		len(ciphertext) > int(HardMaxManifestBytes+HardMaxMetadataBytes)+AEADTagBytes {
		return nil, authenticationError(errors.New("record ciphertext length is invalid"))
	}
	if err := validateRecordCryptoInputs(
		key, nonce, associatedData, len(ciphertext)-AEADTagBytes,
	); err != nil {
		return nil, authenticationError(err)
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, authenticationError(err)
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, associatedData)
	if err != nil {
		return nil, authenticationError(err)
	}
	return plaintext, nil
}

func validateRecordCryptoInputs(
	key []byte,
	nonce []byte,
	associatedData []byte,
	payloadBytes int,
) error {
	if len(key) != MasterKeyBytes || len(nonce) != XNonceBytes ||
		len(associatedData) == 0 || len(associatedData) > int(HardMaxHeaderBytes) ||
		payloadBytes <= 0 ||
		payloadBytes > int(HardMaxManifestBytes+HardMaxMetadataBytes) {
		return fmt.Errorf("%w: record cryptographic inputs are invalid", ErrInvalidBundle)
	}
	return nil
}

func authenticationError(cause error) error {
	return &Error{
		Code:  CodeAuthenticationFailed,
		Cause: errors.Join(ErrAuthenticationFailed, cause),
	}
}

// NonceTracker rejects accidental nonce reuse within one bundle writer.
type NonceTracker struct {
	mu   sync.Mutex
	seen map[[XNonceBytes]byte]struct{}
}

func NewNonceTracker() *NonceTracker {
	return &NonceTracker{seen: make(map[[XNonceBytes]byte]struct{})}
}

func (tracker *NonceTracker) Add(nonce []byte) error {
	if tracker == nil || len(nonce) != XNonceBytes {
		return fmt.Errorf("%w: record nonce is invalid", ErrInvalidBundle)
	}
	var value [XNonceBytes]byte
	copy(value[:], nonce)
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if _, exists := tracker.seen[value]; exists {
		return fmt.Errorf("%w: record nonce was reused", ErrInvalidBundle)
	}
	tracker.seen[value] = struct{}{}
	return nil
}

func GenerateMasterKey(source io.Reader) (*SensitiveBuffer, error) {
	value, err := randomBytes(source, MasterKeyBytes)
	if err != nil {
		return nil, err
	}
	buffer, err := NewSensitiveBuffer(value)
	clear(value)
	return buffer, err
}

func GenerateSalt(source io.Reader) ([]byte, error) {
	return randomBytes(source, KDFSaltBytes)
}

func GenerateNonce(source io.Reader) ([]byte, error) {
	return randomBytes(source, XNonceBytes)
}

func randomBytes(source io.Reader, size int) ([]byte, error) {
	if source == nil {
		source = rand.Reader
	}
	value := make([]byte, size)
	if _, err := io.ReadFull(source, value); err != nil {
		clear(value)
		return nil, fmt.Errorf("%w: cryptographic randomness unavailable", ErrInvalidBundle)
	}
	return value, nil
}
