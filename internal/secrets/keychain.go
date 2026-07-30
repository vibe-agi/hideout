package secrets

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"math"
	"sort"
	"sync"
	"time"
)

const (
	KeychainProviderName = "macos-keychain"
	KeychainServiceName  = "com.vibe-agi.hideout.secret"

	keychainEnvelopeHeaderBytes = 52
	maxKeychainEnvelopeBytes    = keychainEnvelopeHeaderBytes +
		maxRefLength + 127 + maxSecretBytes
	maxKeychainReferences = 4096

	keychainEnvelopeAvailable byte = 1
	keychainEnvelopeDeleted   byte = 2

	keychainEnvelopeMagic = "hideout.secret1\x00"
)

var (
	errKeychainItemMissing = errors.New("keychain item is missing")

	keychainEnvelopeMutationMu sync.Mutex
)

type keychainItemBackend interface {
	Accounts(context.Context) ([]string, error)
	Get(context.Context, string) ([]byte, error)
	Put(context.Context, string, []byte) error
}

type keychainEnvelope struct {
	State          byte
	Ref            string
	OperationID    string
	BaseGeneration uint64
	Generation     uint64
	UpdatedAt      time.Time
	Value          []byte
}

type keychainEnvelopeStore struct {
	backend keychainItemBackend
	now     func() time.Time
}

func newKeychainEnvelopeStore(
	backend keychainItemBackend,
	now func() time.Time,
) *keychainEnvelopeStore {
	return &keychainEnvelopeStore{backend: backend, now: now}
}

func (store *keychainEnvelopeStore) Provider() string {
	return KeychainProviderName
}

func (store *keychainEnvelopeStore) List(
	ctx context.Context,
) ([]Reference, error) {
	if err := checkSecretContext(ctx); err != nil {
		return nil, err
	}
	if store == nil || store.backend == nil {
		return nil, keychainProviderError(
			"provider-unavailable",
			ErrProviderUnavailable,
		)
	}
	accounts, err := store.backend.Accounts(ctx)
	if err != nil {
		return nil, normalizeKeychainBackendError(err)
	}
	if len(accounts) > maxKeychainReferences {
		return nil, keychainProviderError(
			"reference-limit-exceeded",
			ErrSecretEnvelopeCorrupt,
		)
	}
	sort.Strings(accounts)
	references := make([]Reference, 0, len(accounts))
	previous := ""
	for _, account := range accounts {
		if err := ValidateRef(account); err != nil {
			return nil, keychainProviderError(
				"envelope-corrupt",
				ErrSecretEnvelopeCorrupt,
			)
		}
		if account == previous {
			continue
		}
		previous = account
		reference, err := store.Reference(ctx, account)
		if err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	SortReferences(references)
	return references, nil
}

func (store *keychainEnvelopeStore) Reference(
	ctx context.Context,
	ref string,
) (Reference, error) {
	if err := ValidateRef(ref); err != nil {
		return Reference{}, err
	}
	if err := checkSecretContext(ctx); err != nil {
		return Reference{}, err
	}
	envelope, err := store.load(ctx, ref)
	if err != nil {
		switch {
		case errors.Is(err, errKeychainItemMissing):
			return store.missingReference(ref, 0, time.Time{}, "secret-missing")
		case errors.Is(err, ErrSecretLocked):
			return store.unavailableReference(
				ref,
				AvailabilityLocked,
				"keychain-locked",
			)
		case errors.Is(err, ErrProviderUnavailable):
			return store.unavailableReference(
				ref,
				AvailabilityUnavailable,
				"provider-unavailable",
			)
		default:
			return Reference{}, normalizeKeychainBackendError(err)
		}
	}
	defer envelope.clear()
	return store.referenceForEnvelope(envelope)
}

func (store *keychainEnvelopeStore) Set(
	ctx context.Context,
	request WriteRequest,
) (Reference, error) {
	if request.Value != nil {
		defer request.Value.Clear()
	}
	if err := request.Validate(); err != nil {
		return Reference{}, err
	}
	if err := checkSecretContext(ctx); err != nil {
		return Reference{}, err
	}
	if store == nil || store.backend == nil {
		return Reference{}, keychainProviderError(
			"provider-unavailable",
			ErrProviderUnavailable,
		)
	}

	keychainEnvelopeMutationMu.Lock()
	defer keychainEnvelopeMutationMu.Unlock()

	current, err := store.load(ctx, request.Ref)
	switch {
	case errors.Is(err, errKeychainItemMissing):
		current = keychainEnvelope{}
	case err != nil:
		return Reference{}, normalizeKeychainBackendError(err)
	default:
		defer current.clear()
	}
	if current.OperationID == request.OperationID {
		if current.State != keychainEnvelopeAvailable ||
			current.BaseGeneration != request.ExpectedGeneration {
			return Reference{}, ErrSecretOperationMismatch
		}
		matched := false
		err := request.Value.Use(func(value []byte) error {
			matched = len(value) == len(current.Value) &&
				subtle.ConstantTimeCompare(value, current.Value) == 1
			return nil
		})
		if err != nil {
			return Reference{}, err
		}
		if !matched {
			return Reference{}, ErrSecretOperationMismatch
		}
		return store.referenceForEnvelope(current)
	}
	if current.Generation != request.ExpectedGeneration {
		return Reference{}, &GenerationConflictError{
			Ref: request.Ref, Expected: request.ExpectedGeneration,
			Current: current.Generation,
		}
	}
	if current.Generation == math.MaxUint64 {
		return Reference{}, errors.New("secret generation is exhausted")
	}

	var written keychainEnvelope
	err = request.Value.Use(func(value []byte) error {
		written = keychainEnvelope{
			State:          keychainEnvelopeAvailable,
			Ref:            request.Ref,
			OperationID:    request.OperationID,
			BaseGeneration: request.ExpectedGeneration,
			Generation:     request.ExpectedGeneration + 1,
			UpdatedAt:      store.nowUTC(),
			Value:          value,
		}
		return store.put(ctx, written)
	})
	if err != nil {
		return Reference{}, normalizeKeychainBackendError(err)
	}
	return store.referenceForEnvelope(written)
}

func (store *keychainEnvelopeStore) Delete(
	ctx context.Context,
	request DeleteRequest,
) (Reference, error) {
	if err := request.Validate(); err != nil {
		return Reference{}, err
	}
	if err := checkSecretContext(ctx); err != nil {
		return Reference{}, err
	}
	if store == nil || store.backend == nil {
		return Reference{}, keychainProviderError(
			"provider-unavailable",
			ErrProviderUnavailable,
		)
	}

	keychainEnvelopeMutationMu.Lock()
	defer keychainEnvelopeMutationMu.Unlock()

	current, err := store.load(ctx, request.Ref)
	if err != nil {
		if errors.Is(err, errKeychainItemMissing) {
			return Reference{}, keychainProviderError(
				"secret-missing",
				ErrSecretMissing,
			)
		}
		return Reference{}, normalizeKeychainBackendError(err)
	}
	defer current.clear()
	if current.OperationID == request.OperationID {
		if current.State != keychainEnvelopeDeleted ||
			current.BaseGeneration != request.ExpectedGeneration {
			return Reference{}, ErrSecretOperationMismatch
		}
		return store.referenceForEnvelope(current)
	}
	if current.State == keychainEnvelopeDeleted {
		return Reference{}, keychainProviderError(
			"secret-missing",
			ErrSecretMissing,
		)
	}
	if current.Generation != request.ExpectedGeneration {
		return Reference{}, &GenerationConflictError{
			Ref: request.Ref, Expected: request.ExpectedGeneration,
			Current: current.Generation,
		}
	}
	if current.Generation == math.MaxUint64 {
		return Reference{}, errors.New("secret generation is exhausted")
	}
	deleted := keychainEnvelope{
		State:          keychainEnvelopeDeleted,
		Ref:            request.Ref,
		OperationID:    request.OperationID,
		BaseGeneration: request.ExpectedGeneration,
		Generation:     request.ExpectedGeneration + 1,
		UpdatedAt:      store.nowUTC(),
	}
	if err := store.put(ctx, deleted); err != nil {
		return Reference{}, normalizeKeychainBackendError(err)
	}
	return store.referenceForEnvelope(deleted)
}

func (store *keychainEnvelopeStore) Resolve(
	ctx context.Context,
	ref string,
) (*Buffer, error) {
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	if err := checkSecretContext(ctx); err != nil {
		return nil, err
	}
	envelope, err := store.load(ctx, ref)
	if err != nil {
		if errors.Is(err, errKeychainItemMissing) {
			return nil, keychainProviderError(
				"secret-missing",
				ErrSecretMissing,
			)
		}
		return nil, normalizeKeychainBackendError(err)
	}
	defer envelope.clear()
	if envelope.State != keychainEnvelopeAvailable {
		return nil, keychainProviderError(
			"secret-missing",
			ErrSecretMissing,
		)
	}
	buffer, err := NewBuffer(envelope.Value)
	if err != nil {
		return nil, keychainProviderError(
			"envelope-corrupt",
			ErrSecretEnvelopeCorrupt,
		)
	}
	return buffer, nil
}

func (store *keychainEnvelopeStore) Reconcile(
	ctx context.Context,
	request ReconcileRequest,
) (ReconcileResult, error) {
	if err := ValidateRef(request.Ref); err != nil {
		return ReconcileResult{}, err
	}
	if !validOperationID(request.OperationID) {
		return ReconcileResult{}, errors.New(
			"secret reconcile operation identity is invalid",
		)
	}
	switch request.Action {
	case ActionSet, ActionRotate, ActionDelete:
	default:
		return ReconcileResult{}, errors.New(
			"secret reconcile action is invalid",
		)
	}
	if err := checkSecretContext(ctx); err != nil {
		return ReconcileResult{}, err
	}
	envelope, err := store.load(ctx, request.Ref)
	if err != nil {
		if errors.Is(err, errKeychainItemMissing) {
			reference, referenceErr := store.missingReference(
				request.Ref,
				0,
				time.Time{},
				"secret-missing",
			)
			return ReconcileResult{
				Reference: reference,
				Uncommitted: request.Action == ActionSet &&
					request.ExpectedGeneration == 0,
			}, referenceErr
		}
		return ReconcileResult{}, normalizeKeychainBackendError(err)
	}
	defer envelope.clear()
	reference, err := store.referenceForEnvelope(envelope)
	if err != nil {
		return ReconcileResult{}, err
	}
	wantState := keychainEnvelopeAvailable
	if request.Action == ActionDelete {
		wantState = keychainEnvelopeDeleted
	}
	committed := envelope.OperationID == request.OperationID &&
		envelope.BaseGeneration == request.ExpectedGeneration &&
		envelope.State == wantState
	baseState := keychainEnvelopeAvailable
	if request.Action == ActionSet {
		baseState = keychainEnvelopeDeleted
	}
	return ReconcileResult{
		Reference: reference,
		Committed: committed,
		Uncommitted: !committed &&
			envelope.Generation == request.ExpectedGeneration &&
			envelope.State == baseState,
	}, nil
}

func (store *keychainEnvelopeStore) load(
	ctx context.Context,
	ref string,
) (keychainEnvelope, error) {
	if store == nil || store.backend == nil {
		return keychainEnvelope{}, ErrProviderUnavailable
	}
	data, err := store.backend.Get(ctx, ref)
	if err != nil {
		return keychainEnvelope{}, err
	}
	defer clear(data)
	envelope, err := decodeKeychainEnvelope(data)
	if err != nil {
		return keychainEnvelope{}, err
	}
	if envelope.Ref != ref {
		envelope.clear()
		return keychainEnvelope{}, ErrSecretEnvelopeCorrupt
	}
	return envelope, nil
}

func (store *keychainEnvelopeStore) put(
	ctx context.Context,
	envelope keychainEnvelope,
) error {
	if err := checkSecretContext(ctx); err != nil {
		return err
	}
	data, err := encodeKeychainEnvelope(envelope)
	if err != nil {
		return err
	}
	defer clear(data)
	if err := store.backend.Put(ctx, envelope.Ref, data); err != nil {
		return err
	}
	return checkSecretContext(ctx)
}

func (store *keychainEnvelopeStore) referenceForEnvelope(
	envelope keychainEnvelope,
) (Reference, error) {
	switch envelope.State {
	case keychainEnvelopeAvailable:
		reference := Reference{
			Schema:       SecretReferenceSchema,
			Ref:          envelope.Ref,
			Provider:     store.Provider(),
			Availability: AvailabilityAvailable,
			Generation:   envelope.Generation,
			UpdatedAt:    envelope.UpdatedAt,
		}
		return reference, reference.Validate()
	case keychainEnvelopeDeleted:
		return store.missingReference(
			envelope.Ref,
			envelope.Generation,
			envelope.UpdatedAt,
			"secret-deleted",
		)
	default:
		return Reference{}, ErrSecretEnvelopeCorrupt
	}
}

func (store *keychainEnvelopeStore) missingReference(
	ref string,
	generation uint64,
	updatedAt time.Time,
	reason string,
) (Reference, error) {
	reference := Reference{
		Schema:       SecretReferenceSchema,
		Ref:          ref,
		Provider:     store.Provider(),
		Availability: AvailabilityMissing,
		Generation:   generation,
		UpdatedAt:    updatedAt,
		Reason:       reason,
	}
	return reference, reference.Validate()
}

func (store *keychainEnvelopeStore) unavailableReference(
	ref, availability, reason string,
) (Reference, error) {
	reference := Reference{
		Schema:       SecretReferenceSchema,
		Ref:          ref,
		Provider:     store.Provider(),
		Availability: availability,
		Reason:       reason,
	}
	return reference, reference.Validate()
}

func (store *keychainEnvelopeStore) nowUTC() time.Time {
	if store.now != nil {
		return store.now().Round(0).UTC()
	}
	return time.Now().Round(0).UTC()
}

func (envelope *keychainEnvelope) clear() {
	if envelope == nil {
		return
	}
	clear(envelope.Value)
	envelope.Value = nil
}

func encodeKeychainEnvelope(envelope keychainEnvelope) ([]byte, error) {
	if err := envelope.validate(); err != nil {
		return nil, err
	}
	size := keychainEnvelopeHeaderBytes +
		len(envelope.Ref) +
		len(envelope.OperationID) +
		len(envelope.Value)
	if size > maxKeychainEnvelopeBytes {
		return nil, ErrSecretEnvelopeCorrupt
	}
	data := make([]byte, size)
	copy(data[:16], keychainEnvelopeMagic)
	data[16] = envelope.State
	binary.BigEndian.PutUint64(data[20:28], envelope.Generation)
	binary.BigEndian.PutUint64(data[28:36], envelope.BaseGeneration)
	binary.BigEndian.PutUint64(
		data[36:44],
		uint64(envelope.UpdatedAt.UnixNano()),
	)
	binary.BigEndian.PutUint16(data[44:46], uint16(len(envelope.Ref)))
	binary.BigEndian.PutUint16(
		data[46:48],
		uint16(len(envelope.OperationID)),
	)
	binary.BigEndian.PutUint32(data[48:52], uint32(len(envelope.Value)))
	offset := keychainEnvelopeHeaderBytes
	offset += copy(data[offset:], envelope.Ref)
	offset += copy(data[offset:], envelope.OperationID)
	copy(data[offset:], envelope.Value)
	return data, nil
}

func decodeKeychainEnvelope(data []byte) (keychainEnvelope, error) {
	if len(data) < keychainEnvelopeHeaderBytes ||
		len(data) > maxKeychainEnvelopeBytes ||
		!bytes.Equal(data[:16], []byte(keychainEnvelopeMagic)) ||
		data[17] != 0 ||
		data[18] != 0 ||
		data[19] != 0 {
		return keychainEnvelope{}, ErrSecretEnvelopeCorrupt
	}
	refLength := int(binary.BigEndian.Uint16(data[44:46]))
	operationLength := int(binary.BigEndian.Uint16(data[46:48]))
	valueLength := int(binary.BigEndian.Uint32(data[48:52]))
	expected := keychainEnvelopeHeaderBytes +
		refLength +
		operationLength +
		valueLength
	if expected != len(data) ||
		refLength == 0 ||
		operationLength == 0 ||
		valueLength > maxSecretBytes {
		return keychainEnvelope{}, ErrSecretEnvelopeCorrupt
	}
	updatedNanos := binary.BigEndian.Uint64(data[36:44])
	if updatedNanos == 0 || updatedNanos > math.MaxInt64 {
		return keychainEnvelope{}, ErrSecretEnvelopeCorrupt
	}
	offset := keychainEnvelopeHeaderBytes
	ref := string(data[offset : offset+refLength])
	offset += refLength
	operationID := string(data[offset : offset+operationLength])
	offset += operationLength
	envelope := keychainEnvelope{
		State:          data[16],
		Ref:            ref,
		OperationID:    operationID,
		Generation:     binary.BigEndian.Uint64(data[20:28]),
		BaseGeneration: binary.BigEndian.Uint64(data[28:36]),
		UpdatedAt:      time.Unix(0, int64(updatedNanos)).UTC(),
		Value:          append([]byte(nil), data[offset:]...),
	}
	if err := envelope.validate(); err != nil {
		envelope.clear()
		return keychainEnvelope{}, ErrSecretEnvelopeCorrupt
	}
	return envelope, nil
}

func (envelope keychainEnvelope) validate() error {
	if envelope.State != keychainEnvelopeAvailable &&
		envelope.State != keychainEnvelopeDeleted {
		return ErrSecretEnvelopeCorrupt
	}
	if err := ValidateRef(envelope.Ref); err != nil {
		return ErrSecretEnvelopeCorrupt
	}
	if !validOperationID(envelope.OperationID) ||
		envelope.Generation == 0 ||
		envelope.BaseGeneration == math.MaxUint64 ||
		envelope.Generation != envelope.BaseGeneration+1 ||
		envelope.UpdatedAt.IsZero() ||
		envelope.UpdatedAt.UnixNano() <= 0 {
		return ErrSecretEnvelopeCorrupt
	}
	switch envelope.State {
	case keychainEnvelopeAvailable:
		if len(envelope.Value) == 0 || len(envelope.Value) > maxSecretBytes {
			return ErrSecretEnvelopeCorrupt
		}
	case keychainEnvelopeDeleted:
		if len(envelope.Value) != 0 {
			return ErrSecretEnvelopeCorrupt
		}
	}
	return nil
}

func normalizeKeychainBackendError(err error) error {
	if err == nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrSecretGenerationMismatch) ||
		errors.Is(err, ErrSecretOperationMismatch) {
		return err
	}
	var providerError *ProviderError
	if errors.As(err, &providerError) {
		return err
	}
	switch {
	case errors.Is(err, ErrSecretLocked):
		return keychainProviderError("keychain-locked", ErrSecretLocked)
	case errors.Is(err, ErrProviderUnavailable):
		return keychainProviderError(
			"provider-unavailable",
			ErrProviderUnavailable,
		)
	case errors.Is(err, ErrSecretEnvelopeCorrupt):
		return keychainProviderError(
			"envelope-corrupt",
			ErrSecretEnvelopeCorrupt,
		)
	default:
		return keychainProviderError("keychain-operation-failed", err)
	}
}

func keychainProviderError(reason string, cause error) error {
	return &ProviderError{
		Provider: KeychainProviderName,
		Reason:   reason,
		Cause:    cause,
	}
}

func checkSecretContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

var (
	_ Store               = (*keychainEnvelopeStore)(nil)
	_ RuntimeResolver     = (*keychainEnvelopeStore)(nil)
	_ RuntimeStore        = (*keychainEnvelopeStore)(nil)
	_ OperationReconciler = (*keychainEnvelopeStore)(nil)
)
