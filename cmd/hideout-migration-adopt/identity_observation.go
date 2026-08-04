package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/vibe-agi/hideout/internal/migration"
)

type identityObservationRunner struct {
	rootPath         string
	requestPath      string
	receiptPath      string
	selfPath         string
	networkClassPath string
	shutdown         func() error
}

func (runner identityObservationRunner) run() error {
	request, err := readIdentityObservationRequest(runner.requestPath)
	if err != nil {
		return &adoptionFailure{Code: "migration.identity_observation.request_invalid", Cause: err}
	}
	fail := func(code string, cause error) error {
		receipt := baseIdentityObservationReceipt(request)
		receipt.Status = migration.AdoptionReceiptStatusFailed
		receipt.FailureCode = code
		if writeErr := writeIdentityObservationReceipt(runner.receiptPath, receipt); writeErr != nil {
			return &adoptionFailure{
				Code:  "migration.identity_observation.receipt_write_failed",
				Cause: errors.Join(cause, writeErr),
			}
		}
		return &adoptionFailure{Code: code, Cause: cause}
	}
	if err := runner.validate(); err != nil {
		return fail("migration.identity_observation.runtime_invalid", err)
	}
	if err := verifyAdoptionNetworkIsolation(runner.networkClassPath); err != nil {
		return fail("migration.identity_observation.network_present", err)
	}
	digest, err := adoptionFileDigest(runner.selfPath)
	if err != nil || digest != request.Helper.SHA256 {
		return fail("migration.identity_observation.helper_mismatch", err)
	}
	identity, err := observeGuestIdentity(runner.rootPath)
	if err != nil {
		return fail("migration.identity_observation.evidence_unavailable", err)
	}
	receipt := baseIdentityObservationReceipt(request)
	receipt.Identity = &identity
	receipt.Status = migration.AdoptionReceiptStatusCompleted
	receipt.CompletionMarker = true
	if err := writeIdentityObservationReceipt(runner.receiptPath, receipt); err != nil {
		return &adoptionFailure{
			Code: "migration.identity_observation.receipt_write_failed", Cause: err,
		}
	}
	if err := runner.shutdown(); err != nil {
		return &adoptionFailure{
			Code: "migration.identity_observation.shutdown_failed", Cause: err,
		}
	}
	return nil
}

func (runner identityObservationRunner) validate() error {
	for _, value := range []string{
		runner.rootPath, runner.requestPath, runner.receiptPath,
		runner.selfPath, runner.networkClassPath,
	} {
		if !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return errors.New("identity observation runtime path is invalid")
		}
	}
	if runner.shutdown == nil || runner.requestPath == runner.receiptPath {
		return errors.New("identity observation runtime binding is incomplete")
	}
	self, err := os.Lstat(runner.selfPath)
	if err != nil || self.Mode()&os.ModeSymlink != 0 || !self.Mode().IsRegular() ||
		self.Mode().Perm()&0o111 == 0 || self.Mode().Perm()&0o022 != 0 {
		return errors.New("identity observation helper is not protected")
	}
	receiptDirectory, err := os.Lstat(filepath.Dir(runner.receiptPath))
	if err != nil || receiptDirectory.Mode()&os.ModeSymlink != 0 ||
		!receiptDirectory.IsDir() || receiptDirectory.Mode().Perm()&0o077 != 0 {
		return errors.New("identity observation receipt directory is not private")
	}
	if _, err := os.Lstat(runner.receiptPath); err == nil {
		return errors.New("identity observation receipt already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func baseIdentityObservationReceipt(
	request migration.IdentityObservationRequest,
) migration.IdentityObservationReceipt {
	return migration.IdentityObservationReceipt{
		Schema:      migration.IdentityObservationReceiptSchema,
		OperationID: request.OperationID, EnvironmentRef: request.EnvironmentRef,
		RequestNonce: request.RequestNonce, ReceiptNonce: request.ReceiptNonce,
		Helper: request.Helper,
	}
}

func readGuestMigrationRequestSchema(path string) (string, error) {
	data, err := readBoundedRegularFile(path, maximumAdoptionDocumentBytes, true)
	if err != nil {
		return "", err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", err
	}
	if len(envelope) == 0 {
		return "", errors.New("guest migration request is empty")
	}
	var schema string
	if err := json.Unmarshal(envelope["schema"], &schema); err != nil || schema == "" {
		return "", errors.New("guest migration request schema is invalid")
	}
	return schema, nil
}

func readIdentityObservationRequest(
	path string,
) (migration.IdentityObservationRequest, error) {
	data, err := readBoundedRegularFile(path, maximumAdoptionDocumentBytes, true)
	if err != nil {
		return migration.IdentityObservationRequest{}, err
	}
	var request migration.IdentityObservationRequest
	if err := decodeIdentityObservationDocument(data, &request); err != nil {
		return migration.IdentityObservationRequest{}, err
	}
	return request, request.Validate()
}

func readIdentityObservationReceipt(
	path string,
) (migration.IdentityObservationReceipt, error) {
	data, err := readBoundedRegularFile(path, maximumAdoptionDocumentBytes, false)
	if err != nil {
		return migration.IdentityObservationReceipt{}, err
	}
	var receipt migration.IdentityObservationReceipt
	if err := decodeIdentityObservationDocument(data, &receipt); err != nil {
		return migration.IdentityObservationReceipt{}, err
	}
	return receipt, receipt.Validate()
}

func decodeIdentityObservationDocument(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeIdentityObservationReceipt(
	path string,
	receipt migration.IdentityObservationReceipt,
) error {
	if err := receipt.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeProtectedReceipt(path, data)
}

func writeProtectedReceipt(path string, data []byte) error {
	if len(data) == 0 || len(data) > maximumAdoptionDocumentBytes ||
		!filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("identity observation receipt path or size is invalid")
	}
	directory := filepath.Dir(path)
	info, err := os.Lstat(directory)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() ||
		info.Mode().Perm()&0o077 != 0 {
		return errors.New("identity observation receipt directory is not private")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if err := writeAll(file, data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	if err := directoryHandle.Sync(); err != nil {
		_ = directoryHandle.Close()
		return err
	}
	if err := directoryHandle.Close(); err != nil {
		return err
	}
	keep = true
	return nil
}
