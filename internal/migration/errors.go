package migration

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidBundle        = errors.New("migration bundle is invalid")
	ErrLimitExceeded        = errors.New("migration bundle limit exceeded")
	ErrAuthenticationFailed = errors.New("migration bundle authentication failed")
	ErrUnsupportedVersion   = errors.New("migration bundle version is unsupported")
	ErrUnsupportedRecord    = errors.New("migration bundle record is unsupported")
	ErrIncompleteBundle     = errors.New("migration bundle is incomplete")
	ErrCorruptBundle        = errors.New("migration bundle is corrupt")
	ErrTrailingData         = errors.New("migration bundle contains trailing data")
	ErrBundleChanged        = errors.New("migration bundle changed during import")
	ErrOutputExists         = errors.New("migration bundle output already exists")
)

const (
	CodeInvalidBundle        = "migration.bundle.invalid"
	CodeLimitExceeded        = "migration.bundle.limit_exceeded"
	CodeAuthenticationFailed = "migration.bundle.authentication_failed"
	CodeUnsupportedVersion   = "migration.bundle.unsupported_version"
	CodeUnsupportedRecord    = "migration.bundle.unsupported_record"
	CodeIncompleteBundle     = "migration.bundle.incomplete"
	CodeCorruptBundle        = "migration.bundle.corrupt"
	CodeTrailingData         = "migration.bundle.trailing_data"
	CodeBundleChanged        = "migration.bundle.changed_during_import"
	CodeOutputExists         = "migration.bundle.output_exists"
)

// Error carries stable, redacted bundle context. Cause is retained for
// privileged diagnostics through errors.Unwrap but is never rendered by Error.
type Error struct {
	Code             string
	Sequence         uint64
	SequenceKnown    bool
	ComponentID      string
	Retryable        bool
	RecoveryRequired bool
	Cause            error
}

func (err *Error) Error() string {
	if err == nil {
		return CodeInvalidBundle
	}
	code := err.Code
	if !validErrorCode(code) {
		code = CodeInvalidBundle
	}
	message := code
	if err.SequenceKnown || err.Sequence != 0 {
		message += fmt.Sprintf(" sequence=%d", err.Sequence)
	}
	if _, parseErr := ParseOpaqueID(err.ComponentID); parseErr == nil {
		message += " component=" + err.ComponentID
	}
	return message
}

func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

// CodeOf extracts a stable migration code through wrapping.
func CodeOf(err error) string {
	var migrationError *Error
	if errors.As(err, &migrationError) {
		if migrationError.Code == "" {
			return CodeInvalidBundle
		}
		return migrationError.Code
	}
	switch {
	case errors.Is(err, ErrLimitExceeded):
		return CodeLimitExceeded
	case errors.Is(err, ErrAuthenticationFailed):
		return CodeAuthenticationFailed
	case errors.Is(err, ErrUnsupportedVersion):
		return CodeUnsupportedVersion
	case errors.Is(err, ErrUnsupportedRecord):
		return CodeUnsupportedRecord
	case errors.Is(err, ErrIncompleteBundle):
		return CodeIncompleteBundle
	case errors.Is(err, ErrCorruptBundle):
		return CodeCorruptBundle
	case errors.Is(err, ErrTrailingData):
		return CodeTrailingData
	case errors.Is(err, ErrBundleChanged):
		return CodeBundleChanged
	case errors.Is(err, ErrOutputExists):
		return CodeOutputExists
	case errors.Is(err, ErrInvalidBundle):
		return CodeInvalidBundle
	default:
		return ""
	}
}

func validErrorCode(code string) bool {
	switch code {
	case CodeInvalidBundle,
		CodeLimitExceeded,
		CodeAuthenticationFailed,
		CodeUnsupportedVersion,
		CodeUnsupportedRecord,
		CodeIncompleteBundle,
		CodeCorruptBundle,
		CodeTrailingData,
		CodeBundleChanged,
		CodeOutputExists:
		return true
	default:
		return false
	}
}
