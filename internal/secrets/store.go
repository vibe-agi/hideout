package secrets

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	SecretReferenceSchema = "hideout.secret-reference.v1"

	ActionSet    = "set"
	ActionRotate = "rotate"
	ActionDelete = "delete"

	AvailabilityAvailable   = "available"
	AvailabilityMissing     = "missing"
	AvailabilityLocked      = "locked"
	AvailabilityUnavailable = "unavailable"

	maxSecretBytes = 16 << 10
)

var (
	ErrProviderUnavailable      = errors.New("secret provider is unavailable")
	ErrSecretMissing            = errors.New("secret reference is missing")
	ErrSecretLocked             = errors.New("secret provider is locked")
	ErrSecretBufferUsed         = errors.New("secret buffer has already been consumed")
	ErrSecretGenerationMismatch = errors.New(
		"secret generation does not match",
	)
	ErrSecretOperationMismatch = errors.New(
		"secret operation identity is already bound to a different request",
	)
	ErrSecretEnvelopeCorrupt = errors.New(
		"secret keychain envelope is corrupt",
	)

	reasonPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
)

// Store is the Manager-facing secret metadata and mutation boundary. It has no
// read-value method. Runtime resolution is deliberately a separate interface
// held only by daemon-owned provider code.
type Store interface {
	Provider() string
	List(context.Context) ([]Reference, error)
	Reference(context.Context, string) (Reference, error)
	Set(context.Context, WriteRequest) (Reference, error)
	Delete(context.Context, DeleteRequest) (Reference, error)
}

// RuntimeResolver is not exposed by Manager routes. Resolve returns a one-use
// buffer whose Use method clears its bytes on every exit path.
type RuntimeResolver interface {
	Resolve(context.Context, string) (*Buffer, error)
}

type RuntimeStore interface {
	Store
	RuntimeResolver
}

type OperationReconciler interface {
	Reconcile(context.Context, ReconcileRequest) (ReconcileResult, error)
}

type ReconcileRequest struct {
	Ref                string
	Action             string
	OperationID        string
	ExpectedGeneration uint64
}

type ReconcileResult struct {
	Reference   Reference
	Committed   bool
	Uncommitted bool
}

type Reference struct {
	Schema       string    `json:"schema"`
	Ref          string    `json:"ref"`
	Provider     string    `json:"provider"`
	Availability string    `json:"availability"`
	Generation   uint64    `json:"generation"`
	UpdatedAt    time.Time `json:"updatedAt,omitempty"`
	Reason       string    `json:"reason,omitempty"`
}

type WriteRequest struct {
	Ref                string
	OperationID        string
	ExpectedGeneration uint64
	Value              *Buffer
}

type DeleteRequest struct {
	Ref                string
	OperationID        string
	ExpectedGeneration uint64
}

type ProviderError struct {
	Provider string
	Reason   string
	Cause    error
}

type GenerationConflictError struct {
	Ref      string
	Expected uint64
	Current  uint64
}

func (err *GenerationConflictError) Error() string {
	if err == nil {
		return ErrSecretGenerationMismatch.Error()
	}
	return fmt.Sprintf(
		"%s: ref=%s expected=%d current=%d",
		ErrSecretGenerationMismatch,
		err.Ref,
		err.Expected,
		err.Current,
	)
}

func (err *GenerationConflictError) Unwrap() error {
	return ErrSecretGenerationMismatch
}

func (err *ProviderError) Error() string {
	if err == nil {
		return ""
	}
	message := "secret provider"
	if err.Provider != "" {
		message += " " + err.Provider
	}
	if err.Reason != "" {
		message += ": " + err.Reason
	}
	if err.Cause != nil {
		message += ": " + err.Cause.Error()
	}
	return message
}

func (err *ProviderError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

type Buffer struct {
	mu    sync.Mutex
	value []byte
	used  bool
}

func NewBuffer(value []byte) (*Buffer, error) {
	if len(value) == 0 || len(value) > maxSecretBytes {
		return nil, errors.New("secret value must be non-empty and bounded")
	}
	buffer := &Buffer{value: make([]byte, len(value))}
	copy(buffer.value, value)
	return buffer, nil
}

// Use grants synchronous access exactly once and clears the backing bytes even
// when the callback fails or panics. Callers must not retain the provided
// slice.
func (buffer *Buffer) Use(callback func([]byte) error) (err error) {
	if buffer == nil || callback == nil {
		return errors.New("secret buffer callback is required")
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if buffer.used {
		return ErrSecretBufferUsed
	}
	buffer.used = true
	defer clear(buffer.value)
	return callback(buffer.value)
}

func (buffer *Buffer) Clear() {
	if buffer == nil {
		return
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	clear(buffer.value)
	buffer.used = true
}

func (reference Reference) Validate() error {
	if reference.Schema != SecretReferenceSchema {
		return fmt.Errorf("unsupported secret reference schema %q", reference.Schema)
	}
	if err := ValidateRef(reference.Ref); err != nil {
		return err
	}
	if !validProviderName(reference.Provider) {
		return errors.New("secret provider name is invalid")
	}
	switch reference.Availability {
	case AvailabilityAvailable:
		if reference.Generation == 0 || reference.UpdatedAt.IsZero() || reference.Reason != "" {
			return errors.New("available secret reference metadata is incomplete")
		}
	case AvailabilityMissing, AvailabilityLocked, AvailabilityUnavailable:
		if reference.Reason == "" || !reasonPattern.MatchString(reference.Reason) {
			return errors.New("unavailable secret reference requires a stable reason")
		}
	default:
		return fmt.Errorf("unsupported secret availability %q", reference.Availability)
	}
	return nil
}

func (request WriteRequest) Validate() error {
	if err := ValidateRef(request.Ref); err != nil {
		return err
	}
	if !validOperationID(request.OperationID) {
		return errors.New("secret write operation identity is invalid")
	}
	if request.Value == nil {
		return errors.New("secret write value is required")
	}
	return nil
}

func (request DeleteRequest) Validate() error {
	if err := ValidateRef(request.Ref); err != nil {
		return err
	}
	if !validOperationID(request.OperationID) {
		return errors.New("secret delete operation identity is invalid")
	}
	return nil
}

func SortReferences(references []Reference) {
	sort.Slice(references, func(left, right int) bool {
		if references[left].Ref == references[right].Ref {
			return references[left].Generation < references[right].Generation
		}
		return references[left].Ref < references[right].Ref
	})
}

func validProviderName(value string) bool {
	return value != "" && len(value) <= 128 &&
		strings.TrimSpace(value) == value &&
		operationNameCharacters(value)
}

func operationNameCharacters(value string) bool {
	for index, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '-' {
			continue
		}
		if index == 0 {
			return false
		}
		return false
	}
	return value[0] >= 'a' && value[0] <= 'z'
}

func validOperationID(value string) bool {
	if !strings.HasPrefix(value, "op_") || len(value) < len("op_")+8 || len(value) > 127 {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "op_") {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}
