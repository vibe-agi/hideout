package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/migration"
)

const (
	maximumAdoptionDocumentBytes = 64 << 10
	maximumMachineIDBytes        = 128
	maximumSSHHostPublicKeyBytes = 16 << 10
)

var sshHostKeyFilePattern = regexp.MustCompile(
	`^ssh_host_[a-z0-9_-]+_key(?:\.pub)?$`,
)

type adoptionFailure struct {
	Code  string
	Cause error
}

func (failure *adoptionFailure) Error() string {
	if failure == nil || failure.Code == "" {
		return "migration.adoption.failed"
	}
	return failure.Code
}

func (failure *adoptionFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Cause
}

type adoptionRunner struct {
	rootPath            string
	requestPath         string
	receiptPath         string
	selfPath            string
	networkClassPath    string
	random              io.Reader
	generateSSHHostKeys func(string) error
	shutdown            func() error
}

func (runner adoptionRunner) run() error {
	request, err := readAdoptionRequest(runner.requestPath)
	if err != nil {
		return &adoptionFailure{Code: "migration.adoption.request_invalid", Cause: err}
	}
	if err := runner.validate(); err != nil {
		return runner.fail(
			request, nil, "migration.adoption.runtime_invalid", err,
		)
	}
	if err := verifyAdoptionNetworkIsolation(runner.networkClassPath); err != nil {
		return runner.fail(
			request, nil, "migration.adoption.network_present", err,
		)
	}
	selfDigest, err := adoptionFileDigest(runner.selfPath)
	if err != nil || selfDigest != request.Helper.SHA256 {
		return runner.fail(
			request, nil, "migration.adoption.helper_mismatch", err,
		)
	}
	before, err := observeGuestIdentity(runner.rootPath)
	if err != nil || !before.Equal(request.SourceIdentity) {
		return runner.fail(
			request, nil, "migration.adoption.source_identity_mismatch", err,
		)
	}

	results := make([]migration.AdoptionActionResult, 0, len(request.PermittedActions))
	switch request.Policy {
	case migration.GuestIdentitySafeClone:
		if err := resetMachineIdentity(
			runner.rootPath, runner.random, request.SourceIdentity.MachineIDDigest,
		); err != nil {
			return runner.fail(
				request, results, "migration.adoption.machine_id_reset_failed", err,
			)
		}
		results = append(results, migration.AdoptionActionResult{
			Action: migration.AdoptionActionResetMachineID,
			Status: migration.AdoptionActionStatusCompleted,
		})
		if err := resetSSHHostKeys(
			runner.rootPath, runner.generateSSHHostKeys,
		); err != nil {
			return runner.fail(
				request, results, "migration.adoption.ssh_host_key_reset_failed", err,
			)
		}
		results = append(results, migration.AdoptionActionResult{
			Action: migration.AdoptionActionResetSSHHostKeys,
			Status: migration.AdoptionActionStatusCompleted,
		})
	case migration.GuestIdentityExactRestore:
		results = append(results, migration.AdoptionActionResult{
			Action: migration.AdoptionActionPreserveIdentity,
			Status: migration.AdoptionActionStatusCompleted,
		})
	default:
		return runner.fail(
			request, results, "migration.adoption.policy_invalid", nil,
		)
	}

	after, err := observeGuestIdentity(runner.rootPath)
	if err != nil {
		return runner.fail(
			request, results, "migration.adoption.identity_proof_failed", err,
		)
	}
	receipt := completedAdoptionReceipt(request, results, after)
	if err := receipt.MatchesRequest(request); err != nil {
		return runner.fail(
			request, results, "migration.adoption.identity_proof_failed", err,
		)
	}
	if err := writeAdoptionReceipt(runner.receiptPath, receipt); err != nil {
		return &adoptionFailure{
			Code: "migration.adoption.receipt_write_failed", Cause: err,
		}
	}
	if err := runner.shutdown(); err != nil {
		return &adoptionFailure{
			Code: "migration.adoption.shutdown_failed", Cause: err,
		}
	}
	return nil
}

func (runner adoptionRunner) validate() error {
	if !filepath.IsAbs(runner.rootPath) || filepath.Clean(runner.rootPath) != runner.rootPath ||
		!filepath.IsAbs(runner.requestPath) || filepath.Clean(runner.requestPath) != runner.requestPath ||
		!filepath.IsAbs(runner.receiptPath) || filepath.Clean(runner.receiptPath) != runner.receiptPath ||
		!filepath.IsAbs(runner.selfPath) || filepath.Clean(runner.selfPath) != runner.selfPath ||
		!filepath.IsAbs(runner.networkClassPath) ||
		filepath.Clean(runner.networkClassPath) != runner.networkClassPath ||
		runner.random == nil || runner.generateSSHHostKeys == nil || runner.shutdown == nil {
		return errors.New("adoption runtime binding is incomplete")
	}
	if runner.requestPath == runner.receiptPath || runner.selfPath == runner.requestPath ||
		runner.selfPath == runner.receiptPath {
		return errors.New("adoption runtime paths overlap")
	}
	helperInfo, err := os.Lstat(runner.selfPath)
	if err != nil || helperInfo.Mode()&os.ModeSymlink != 0 ||
		!helperInfo.Mode().IsRegular() || helperInfo.Mode().Perm()&0o111 == 0 ||
		helperInfo.Mode().Perm()&0o022 != 0 {
		return errors.New("adoption helper is not a protected executable")
	}
	directory := filepath.Dir(runner.receiptPath)
	info, err := os.Lstat(directory)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() ||
		info.Mode().Perm()&0o077 != 0 {
		return errors.New("receipt directory is not a private non-symlink directory")
	}
	if _, err := os.Lstat(runner.receiptPath); err == nil {
		return errors.New("receipt path already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func verifyAdoptionNetworkIsolation(path string) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("network interface inventory is unavailable")
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 1 || entries[0].Name() != "lo" {
		return errors.New("a non-loopback network interface is present")
	}
	return nil
}

func (runner adoptionRunner) fail(
	request migration.AdoptionRequest,
	completed []migration.AdoptionActionResult,
	code string,
	cause error,
) error {
	results := append([]migration.AdoptionActionResult(nil), completed...)
	expected := request.PermittedActions
	failedIndex := len(results)
	if failedIndex >= len(expected) {
		failedIndex = len(expected) - 1
		results = results[:failedIndex]
	}
	if failedIndex >= 0 && failedIndex < len(expected) {
		results = append(results, migration.AdoptionActionResult{
			Action: expected[failedIndex], Status: migration.AdoptionActionStatusFailed,
			Code: code,
		})
	}
	receipt := baseAdoptionReceipt(request)
	receipt.ActionResults = results
	receipt.Status = migration.AdoptionReceiptStatusFailed
	receipt.FailureCode = code
	if err := receipt.Validate(); err != nil {
		return &adoptionFailure{
			Code: "migration.adoption.receipt_invalid", Cause: errors.Join(cause, err),
		}
	}
	if err := writeAdoptionReceipt(runner.receiptPath, receipt); err != nil {
		return &adoptionFailure{
			Code: "migration.adoption.receipt_write_failed", Cause: errors.Join(cause, err),
		}
	}
	return &adoptionFailure{Code: code, Cause: cause}
}

func baseAdoptionReceipt(request migration.AdoptionRequest) migration.AdoptionReceipt {
	return migration.AdoptionReceipt{
		Schema:      migration.AdoptionReceiptSchema,
		OperationID: request.OperationID, EnvironmentRef: request.EnvironmentRef,
		RequestNonce: request.RequestNonce, ReceiptNonce: request.ReceiptNonce,
		Policy: request.Policy, Helper: request.Helper,
	}
}

func completedAdoptionReceipt(
	request migration.AdoptionRequest,
	results []migration.AdoptionActionResult,
	identity migration.GuestIdentityEvidence,
) migration.AdoptionReceipt {
	receipt := baseAdoptionReceipt(request)
	receipt.ActionResults = append([]migration.AdoptionActionResult(nil), results...)
	receipt.PostIdentity = &identity
	receipt.Status = migration.AdoptionReceiptStatusCompleted
	receipt.CompletionMarker = true
	return receipt
}

func readAdoptionRequest(path string) (migration.AdoptionRequest, error) {
	data, err := readBoundedRegularFile(path, maximumAdoptionDocumentBytes, true)
	if err != nil {
		return migration.AdoptionRequest{}, err
	}
	var request migration.AdoptionRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return migration.AdoptionRequest{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return migration.AdoptionRequest{}, err
	}
	if err := request.Validate(); err != nil {
		return migration.AdoptionRequest{}, err
	}
	return request, nil
}

func readAdoptionReceipt(path string) (migration.AdoptionReceipt, error) {
	data, err := readBoundedRegularFile(path, maximumAdoptionDocumentBytes, false)
	if err != nil {
		return migration.AdoptionReceipt{}, err
	}
	var receipt migration.AdoptionReceipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return migration.AdoptionReceipt{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return migration.AdoptionReceipt{}, err
	}
	if err := receipt.Validate(); err != nil {
		return migration.AdoptionReceipt{}, err
	}
	return receipt, nil
}

func writeAdoptionReceipt(path string, receipt migration.AdoptionReceipt) (resultErr error) {
	if err := receipt.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("receipt path is not clean and absolute")
	}
	directory := filepath.Dir(path)
	info, err := os.Lstat(directory)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() ||
		info.Mode().Perm()&0o077 != 0 {
		return errors.New("receipt directory is not a private non-symlink directory")
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maximumAdoptionDocumentBytes {
		return errors.New("receipt exceeds the size limit")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	keep := false
	closed := false
	defer func() {
		if !closed {
			resultErr = errors.Join(resultErr, file.Close())
		}
		if !keep {
			if removeErr := os.Remove(path); removeErr != nil &&
				!errors.Is(removeErr, os.ErrNotExist) {
				resultErr = errors.Join(resultErr, removeErr)
			}
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
	closed = true
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

func resetMachineIdentity(
	root string,
	random io.Reader,
	sourceDigest migration.Digest,
) error {
	if random == nil {
		return errors.New("machine identity randomness is unavailable")
	}
	var value []byte
	for attempt := 0; attempt < 8; attempt++ {
		candidate := make([]byte, 16)
		if _, err := io.ReadFull(random, candidate); err != nil {
			clear(candidate)
			return err
		}
		value = []byte(fmt.Sprintf("%x\n", candidate))
		clear(candidate)
		digest, err := migration.MachineIdentityDigest(value)
		if err == nil && digest != sourceDigest {
			break
		}
		clear(value)
		value = nil
	}
	if len(value) == 0 {
		return errors.New("fresh machine identity could not be generated")
	}
	defer clear(value)
	if err := atomicWriteGuestFile(root, "etc/machine-id", value, 0o644); err != nil {
		return err
	}
	return synchronizeDBusMachineID(root, value)
}

func synchronizeDBusMachineID(root string, value []byte) error {
	relative := filepath.Join("var", "lib", "dbus", "machine-id")
	directory, err := guestPath(root, filepath.Dir(relative), true)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	path := filepath.Join(directory, filepath.Base(relative))
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return atomicWriteGuestFile(root, relative, value, 0o644)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		if filepath.IsAbs(target) {
			target = filepath.Join(root, strings.TrimPrefix(target, string(filepath.Separator)))
		} else {
			target = filepath.Join(filepath.Dir(path), target)
		}
		machinePath, machineErr := guestPath(root, "etc/machine-id", true)
		if machineErr != nil || filepath.Clean(target) != machinePath {
			return errors.New("dbus machine-id symlink does not target /etc/machine-id")
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return errors.New("dbus machine-id is not a regular file")
	}
	return atomicWriteGuestFile(root, relative, value, 0o644)
}

func resetSSHHostKeys(root string, generate func(string) error) error {
	if generate == nil {
		return errors.New("SSH host-key generator is unavailable")
	}
	directory, err := guestPath(root, filepath.Join("etc", "ssh"), true)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "ssh_host_") &&
			!sshHostKeyFilePattern.MatchString(entry.Name()) {
			return errors.New("unrecognized SSH host identity material is present")
		}
		if !sshHostKeyFilePattern.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("SSH host-key path is not a non-symlink regular file")
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return errors.New("no source SSH host keys were present")
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	if err := generate(root); err != nil {
		return err
	}
	_, err = observeSSHHostIdentity(root)
	return err
}

func observeGuestIdentity(root string) (migration.GuestIdentityEvidence, error) {
	machinePath, err := guestPath(root, "etc/machine-id", true)
	if err != nil {
		return migration.GuestIdentityEvidence{}, err
	}
	machineBytes, err := readBoundedRegularFile(machinePath, maximumMachineIDBytes, false)
	if err != nil {
		return migration.GuestIdentityEvidence{}, err
	}
	machineDigest, err := migration.MachineIdentityDigest(machineBytes)
	clear(machineBytes)
	if err != nil {
		return migration.GuestIdentityEvidence{}, err
	}
	sshDigests, err := observeSSHHostIdentity(root)
	if err != nil {
		return migration.GuestIdentityEvidence{}, err
	}
	identity := migration.GuestIdentityEvidence{
		MachineIDDigest: machineDigest, SSHHostKeyDigests: sshDigests,
	}
	return identity, identity.Validate()
}

func observeSSHHostIdentity(root string) ([]migration.Digest, error) {
	directory, err := guestPath(root, filepath.Join("etc", "ssh"), true)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	digests := make([]migration.Digest, 0, 4)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".pub") ||
			!sshHostKeyFilePattern.MatchString(entry.Name()) {
			continue
		}
		data, err := readBoundedRegularFile(
			filepath.Join(directory, entry.Name()), maximumSSHHostPublicKeyBytes, false,
		)
		if err != nil {
			return nil, err
		}
		digest, err := migration.SSHHostPublicKeyDigest(data)
		clear(data)
		if err != nil {
			return nil, err
		}
		digests = append(digests, digest)
	}
	sort.Slice(digests, func(left, right int) bool { return digests[left] < digests[right] })
	if len(digests) == 0 {
		return nil, errors.New("no SSH host public keys are present")
	}
	return digests, nil
}

func guestPath(root, relative string, requireLeaf bool) (string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root ||
		filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == "." {
		return "", errors.New("guest path binding is invalid")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return "", errors.New("guest root is not a non-symlink directory")
	}
	components := strings.Split(relative, string(filepath.Separator))
	current := root
	for index, component := range components {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		leaf := index == len(components)-1
		if errors.Is(statErr, os.ErrNotExist) && leaf && !requireLeaf {
			return current, os.ErrNotExist
		}
		if statErr != nil {
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("guest path contains a symlink")
		}
		if !leaf && !info.IsDir() {
			return "", errors.New("guest path parent is not a directory")
		}
	}
	return current, nil
}

func atomicWriteGuestFile(
	root, relative string,
	data []byte,
	mode os.FileMode,
) (resultErr error) {
	path, err := guestPath(root, relative, false)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("guest identity target is not a non-symlink regular file")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".hideout-adopt-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keep := false
	closed := false
	defer func() {
		if !closed {
			resultErr = errors.Join(resultErr, temporary.Close())
		}
		if !keep {
			if removeErr := os.Remove(temporaryPath); removeErr != nil &&
				!errors.Is(removeErr, os.ErrNotExist) {
				resultErr = errors.Join(resultErr, removeErr)
			}
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if err := writeAll(temporary, data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Rename(temporaryPath, path); err != nil {
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

func readBoundedRegularFile(path string, maximum int64, requireReadOnly bool) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || maximum <= 0 {
		return nil, errors.New("bounded file path is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		info.Size() < 0 || info.Size() > maximum ||
		(requireReadOnly && info.Mode().Perm()&0o222 != 0) {
		return nil, errors.New("bounded file is unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("bounded file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		clear(data)
		return nil, errors.New("bounded file exceeds the size limit")
	}
	return data, nil
}

func adoptionFileDigest(path string) (migration.Digest, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("adoption helper path is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		info.Size() < 0 || info.Size() > 128<<20 {
		return "", errors.New("adoption helper file is unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", errors.New("adoption helper changed while opening")
	}
	digest := sha256.New()
	written, err := io.Copy(digest, io.LimitReader(file, (128<<20)+1))
	if err != nil {
		return "", err
	}
	if written > 128<<20 {
		return "", errors.New("adoption helper exceeds the size limit")
	}
	return migration.Digest(fmt.Sprintf("sha256:%x", digest.Sum(nil))), nil
}

func generateSSHHostKeys(root string) error {
	if root != string(filepath.Separator) {
		return errors.New("production SSH key generation requires the guest root")
	}
	command := exec.Command("/usr/bin/ssh-keygen", "-A")
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	output, err := command.CombinedOutput()
	defer clear(output)
	if err != nil {
		return fmt.Errorf("ssh-keygen failed: %w (%d bytes output)", err, len(output))
	}
	return nil
}

func shutdownGuest() error {
	command := exec.Command("/sbin/poweroff")
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	output, err := command.CombinedOutput()
	defer clear(output)
	if err != nil {
		return fmt.Errorf("poweroff failed: %w (%d bytes output)", err, len(output))
	}
	return nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}
