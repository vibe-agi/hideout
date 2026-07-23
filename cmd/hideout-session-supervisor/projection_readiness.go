package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

const maxProjectionManifestBytes = 256 << 10

type projectionReadinessSpec struct {
	EnvironmentID     string
	SessionSnapshotID string
	CatalogDigest     string
	ExpectedEntries   int
	TargetProjected   bool
}

func (spec projectionReadinessSpec) validate() error {
	if strings.TrimSpace(spec.EnvironmentID) == "" ||
		strings.TrimSpace(spec.EnvironmentID) != spec.EnvironmentID {
		return errors.New("projection readiness environment identity is invalid")
	}
	if !validPrefixedSHA256(spec.SessionSnapshotID) ||
		!validPrefixedSHA256(spec.CatalogDigest) {
		return errors.New("projection readiness digest identity is invalid")
	}
	if spec.ExpectedEntries <= 0 ||
		spec.ExpectedEntries > backend.MaxProjectionReadinessEntries {
		return errors.New("projection readiness entry count is invalid")
	}
	return nil
}

type projectionReadinessObservation struct {
	CatalogDigest   string
	ExpectedEntries int
	ObservedEntries int
	DurationMillis  int64
	TargetProjected bool
}

type projectionReadinessError struct {
	reason backend.ProjectionReadinessReason
	err    error
}

func (e *projectionReadinessError) Error() string {
	if e == nil || e.err == nil {
		return "projection readiness failed"
	}
	return e.err.Error()
}

func (e *projectionReadinessError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func readinessFailure(reason backend.ProjectionReadinessReason, format string, args ...any) error {
	return &projectionReadinessError{reason: reason, err: fmt.Errorf(format, args...)}
}

func readinessReason(err error) string {
	var typed *projectionReadinessError
	if errors.As(err, &typed) {
		return string(typed.reason)
	}
	return ""
}

func observeProjectionReadiness(
	sessionRoot, sessionID string,
	expectation projectionReadinessSpec,
) (projectionReadinessObservation, error) {
	started := time.Now()
	if err := expectation.validate(); err != nil {
		return projectionReadinessObservation{}, readinessFailure(
			backend.ProjectionReadinessCatalogDrift, "%v", err,
		)
	}
	manifestPath := filepath.Join(sessionRoot, backend.ProjectionReadinessManifestFile)
	info, err := os.Lstat(manifestPath)
	if err != nil {
		reason := backend.ProjectionReadinessManifestMissing
		if !errors.Is(err, os.ErrNotExist) {
			reason = backend.ProjectionReadinessEntryInvalid
		}
		return projectionReadinessObservation{}, readinessFailure(reason, "projection readiness manifest is unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		info.Mode().Perm()&0o077 != 0 || info.Mode().Perm()&0o400 == 0 ||
		info.Size() <= 0 || info.Size() > maxProjectionManifestBytes {
		return projectionReadinessObservation{}, readinessFailure(
			backend.ProjectionReadinessEntryInvalid,
			"projection readiness manifest is not a private bounded regular file",
		)
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return projectionReadinessObservation{}, readinessFailure(
			backend.ProjectionReadinessEntryInvalid, "projection readiness manifest cannot be opened",
		)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxProjectionManifestBytes+1))
	decoder.DisallowUnknownFields()
	var manifest backend.ProjectionReadinessManifest
	decodeErr := decoder.Decode(&manifest)
	if decodeErr == nil {
		var trailing any
		if trailingErr := decoder.Decode(&trailing); !errors.Is(trailingErr, io.EOF) {
			decodeErr = errors.New("projection readiness manifest contains trailing JSON")
		}
	}
	closeErr := file.Close()
	if decodeErr != nil || closeErr != nil {
		return projectionReadinessObservation{}, readinessFailure(
			backend.ProjectionReadinessEntryInvalid, "projection readiness manifest is malformed",
		)
	}
	if err := manifest.Validate(); err != nil {
		return projectionReadinessObservation{}, readinessFailure(
			backend.ProjectionReadinessEntryInvalid, "projection readiness manifest structure is invalid",
		)
	}
	if err := manifest.ValidateCatalogDigest(); err != nil ||
		manifest.CatalogDigest != expectation.CatalogDigest ||
		len(manifest.Entries) != expectation.ExpectedEntries {
		return projectionReadinessObservation{}, readinessFailure(
			backend.ProjectionReadinessCatalogDrift, "projection readiness catalog identity changed",
		)
	}
	if manifest.SessionID != sessionID ||
		manifest.EnvironmentID != expectation.EnvironmentID ||
		manifest.SessionSnapshotID != expectation.SessionSnapshotID {
		return projectionReadinessObservation{}, readinessFailure(
			backend.ProjectionReadinessIdentityDrift, "projection readiness session identity changed",
		)
	}
	observed := 0
	for _, entry := range manifest.Entries {
		entryPath := filepath.Join(sessionRoot, "shims", entry.RelativePath)
		entryInfo, err := os.Lstat(entryPath)
		if err != nil {
			reason := backend.ProjectionReadinessEntryMissing
			if !errors.Is(err, os.ErrNotExist) {
				reason = backend.ProjectionReadinessEntryInvalid
			}
			return projectionReadinessObservation{}, readinessFailure(
				reason, "projected command %q is unavailable", entry.Name,
			)
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 || !entryInfo.Mode().IsRegular() ||
			entryInfo.Mode().Perm()&0o111 == 0 ||
			entryInfo.Size() < 0 || entryInfo.Size() > backend.MaxProjectionReadinessEntryBytes {
			return projectionReadinessObservation{}, readinessFailure(
				backend.ProjectionReadinessEntryInvalid,
				"projected command %q is not a bounded regular executable", entry.Name,
			)
		}
		entryFile, err := os.Open(entryPath)
		if err != nil {
			return projectionReadinessObservation{}, readinessFailure(
				backend.ProjectionReadinessEntryInvalid,
				"projected command %q cannot be opened", entry.Name,
			)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, io.LimitReader(entryFile, backend.MaxProjectionReadinessEntryBytes+1))
		entryCloseErr := entryFile.Close()
		if copyErr != nil || entryCloseErr != nil {
			return projectionReadinessObservation{}, readinessFailure(
				backend.ProjectionReadinessEntryInvalid,
				"projected command %q cannot be read", entry.Name,
			)
		}
		if actual := "sha256:" + hex.EncodeToString(hash.Sum(nil)); actual != entry.SHA256 {
			return projectionReadinessObservation{}, readinessFailure(
				backend.ProjectionReadinessDigestMismatch,
				"projected command %q digest changed", entry.Name,
			)
		}
		observed++
	}
	return projectionReadinessObservation{
		CatalogDigest:   manifest.CatalogDigest,
		ExpectedEntries: len(manifest.Entries), ObservedEntries: observed,
		DurationMillis:  time.Since(started).Milliseconds(),
		TargetProjected: expectation.TargetProjected,
	}, nil
}

func validPrefixedSHA256(value string) bool {
	raw := strings.TrimPrefix(value, "sha256:")
	if len(value) != len("sha256:")+64 || len(raw) != 64 ||
		strings.ToLower(raw) != raw {
		return false
	}
	decoded, err := hex.DecodeString(raw)
	return err == nil && len(decoded) == 32
}
