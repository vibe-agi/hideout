//go:build darwin && arm64

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Code-Hex/vz/v3"
	"github.com/vibe-agi/hideout/internal/migration/vzexecutor"
)

const adoptionBootTimeout = 5 * time.Minute

const (
	requestShareTag = "hideout-migration-request"
	helperShareTag  = "hideout-migration-helper"
	receiptShareTag = "hideout-migration-receipt"
)

func validateAdoptionExecutorCapability() error {
	executable, err := os.Executable()
	if err != nil {
		return errors.New("VZ executor identity is unavailable")
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	info, err := os.Lstat(executable)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("VZ executor identity is unsafe")
	}

	var verifyOutput boundedCommandOutput
	verify := exec.Command("/usr/bin/codesign", "--verify", "--strict", executable)
	verify.Stdout = &verifyOutput
	verify.Stderr = &verifyOutput
	if err := verify.Run(); err != nil || verifyOutput.truncated {
		return errors.New("VZ executor signature is invalid")
	}

	var entitlementXML, entitlementErrors boundedCommandOutput
	display := exec.Command(
		"/usr/bin/codesign", "--display", "--xml", "--entitlements", "-", executable,
	)
	display.Stdout = &entitlementXML
	display.Stderr = &entitlementErrors
	if err := display.Run(); err != nil || entitlementXML.truncated || entitlementErrors.truncated {
		return errors.New("VZ executor entitlements are unavailable")
	}
	var entitlementJSON boundedCommandOutput
	convert := exec.Command("/usr/bin/plutil", "-convert", "json", "-o", "-", "--", "-")
	convert.Stdin = strings.NewReader(entitlementXML.String())
	convert.Stdout = &entitlementJSON
	convert.Stderr = &entitlementErrors
	if err := convert.Run(); err != nil || entitlementJSON.truncated || entitlementErrors.truncated {
		return errors.New("VZ executor entitlements are invalid")
	}
	entitlements := make(map[string]bool)
	if err := json.Unmarshal([]byte(entitlementJSON.String()), &entitlements); err != nil ||
		len(entitlements) != 1 || !entitlements["com.apple.security.virtualization"] {
		return errors.New("VZ executor virtualization entitlement is absent or non-minimal")
	}
	return nil
}

func runAdoptionExecutor(
	request vzexecutor.ExecutionRequest,
) (vzexecutor.ExecutionResponse, error) {
	paths, err := request.Paths()
	if err != nil {
		return vzexecutor.ExecutionResponse{}, err
	}
	if err := validatePreparedExecutionPaths(paths); err != nil {
		return vzexecutor.ExecutionResponse{}, err
	}
	if err := prepareAdoptionRuntime(request, paths); err != nil {
		return vzexecutor.ExecutionResponse{}, err
	}
	configuration, err := buildVirtualMachineConfiguration(request, paths)
	if err != nil {
		return vzexecutor.ExecutionResponse{}, err
	}
	valid, err := configuration.Validate()
	if err != nil || !valid {
		return vzexecutor.ExecutionResponse{}, fmt.Errorf(
			"validate zero-network VZ configuration: %w", err,
		)
	}
	if len(configuration.NetworkDevices()) != 0 {
		return vzexecutor.ExecutionResponse{}, errors.New(
			"zero-network VZ configuration acquired a network device",
		)
	}
	machine, err := vz.NewVirtualMachine(configuration)
	if err != nil {
		return vzexecutor.ExecutionResponse{}, err
	}
	if err := machine.Start(); err != nil {
		return vzexecutor.ExecutionResponse{}, err
	}

	started := false
	timer := time.NewTimer(adoptionBootTimeout)
	defer timer.Stop()
	for {
		select {
		case state := <-machine.StateChangedNotify():
			switch state {
			case vz.VirtualMachineStateRunning:
				started = true
			case vz.VirtualMachineStateError:
				return vzexecutor.ExecutionResponse{}, errors.New(
					"zero-network VZ machine entered an error state",
				)
			case vz.VirtualMachineStateStopped:
				if !started {
					return vzexecutor.ExecutionResponse{}, errors.New(
						"zero-network VZ machine stopped before running",
					)
				}
				if err := waitForReceipt(paths.GuestReceipt, 2*time.Second); err != nil {
					return vzexecutor.ExecutionResponse{}, err
				}
				response := vzexecutor.ExecutionResponse{
					Schema:         vzexecutor.ExecutionResponseSchema,
					ExecutionNonce: request.ExecutionNonce,
					Started:        true, Stopped: true, NetworkDeviceCount: 0,
					ReceiptObserved: true,
					StopReason:      vzexecutor.StopReasonReceiptAndGuestShutdown,
				}
				proof, err := response.ExpectedShutdownProof()
				if err != nil {
					return vzexecutor.ExecutionResponse{}, err
				}
				response.ShutdownProof = proof
				responseData, err := json.Marshal(response)
				if err != nil {
					return vzexecutor.ExecutionResponse{}, err
				}
				responseData = append(responseData, '\n')
				if err := writeExclusiveFile(
					paths.ExecutorResponse, responseData, 0o600,
				); err != nil {
					return vzexecutor.ExecutionResponse{}, err
				}
				if err := syncExecutorDirectory(paths.Control); err != nil {
					return vzexecutor.ExecutionResponse{}, err
				}
				return response, response.Validate()
			}
		case <-timer.C:
			if machine.CanStop() {
				_ = machine.Stop()
			}
			return vzexecutor.ExecutionResponse{}, errors.New(
				"zero-network VZ adoption timed out",
			)
		}
	}
}

func validatePreparedExecutionPaths(paths vzexecutor.ExecutionPaths) error {
	if err := requireProtectedDirectory(paths.Stage); err != nil {
		return err
	}
	for _, path := range []string{
		paths.Control, paths.RequestDirectory, paths.HelperDirectory,
		paths.ReceiptDirectory,
	} {
		if err := requireProtectedDirectory(path); err != nil {
			return err
		}
	}
	if err := requireRegularFile(paths.RootDisk, false, false); err != nil {
		return err
	}
	if err := requireRegularFile(paths.GuestRequest, true, false); err != nil {
		return err
	}
	if err := requireRegularFile(paths.GuestHelper, false, true); err != nil {
		return err
	}
	if _, err := os.Lstat(paths.GuestReceipt); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = errors.New("receipt already exists")
		}
		return err
	}
	if _, err := os.Lstat(paths.RuntimeDirectory); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = errors.New("adoption runtime already exists")
		}
		return err
	}
	if err := requireDirectoryEntries(
		paths.Control, []string{"helper", "receipt", "request"},
	); err != nil {
		return err
	}
	if err := requireDirectoryEntries(paths.RequestDirectory, []string{"request.json"}); err != nil {
		return err
	}
	if err := requireDirectoryEntries(
		paths.HelperDirectory, []string{vzexecutor.GuestHelperFilename},
	); err != nil {
		return err
	}
	return requireDirectoryEntries(paths.ReceiptDirectory, nil)
}

func prepareAdoptionRuntime(
	request vzexecutor.ExecutionRequest,
	paths vzexecutor.ExecutionPaths,
) error {
	if err := os.Mkdir(paths.RuntimeDirectory, 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(paths.CIDDataSource, 0o700); err != nil {
		return err
	}
	metadata := []byte(fmt.Sprintf(
		"instance-id: hideout-%s\nlocal-hostname: hideout-migration\n",
		request.ExecutionNonce,
	))
	if err := writeExclusiveFile(
		filepath.Join(paths.CIDDataSource, "meta-data"), metadata, 0o400,
	); err != nil {
		return err
	}
	if err := writeExclusiveFile(
		filepath.Join(paths.CIDDataSource, "user-data"), []byte(cloudBoothook), 0o400,
	); err != nil {
		return err
	}
	command := exec.Command(
		"/usr/bin/hdiutil", "makehybrid", "-iso", "-joliet",
		"-default-volume-name", "cidata", "-o", paths.CIDataISO,
		paths.CIDDataSource,
	)
	command.Env = []string{
		"LANG=C", "LC_ALL=C", "TMPDIR=" + paths.RuntimeDirectory,
	}
	var output boundedCommandOutput
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return fmt.Errorf("create fixed CIDATA image: %w: %s", err, output.String())
	}
	if err := os.Chmod(paths.CIDataISO, 0o600); err != nil {
		return err
	}
	return requireRegularFile(paths.CIDataISO, false, false)
}

func buildVirtualMachineConfiguration(
	request vzexecutor.ExecutionRequest,
	paths vzexecutor.ExecutionPaths,
) (*vz.VirtualMachineConfiguration, error) {
	efi, err := vz.NewEFIVariableStore(
		paths.EFIVariableStore, vz.WithCreatingEFIVariableStore(),
	)
	if err != nil {
		return nil, err
	}
	bootLoader, err := vz.NewEFIBootLoader(vz.WithEFIVariableStore(efi))
	if err != nil {
		return nil, err
	}
	configuration, err := vz.NewVirtualMachineConfiguration(
		bootLoader, request.CPUCount, request.MemoryBytes,
	)
	if err != nil {
		return nil, err
	}

	machineIdentifier, err := vz.NewGenericMachineIdentifier()
	if err != nil {
		return nil, err
	}
	if err := writeExclusiveFile(
		paths.MachineIdentifier, machineIdentifier.DataRepresentation(), 0o600,
	); err != nil {
		return nil, err
	}
	platform, err := vz.NewGenericPlatformConfiguration(
		vz.WithGenericMachineIdentifier(machineIdentifier),
	)
	if err != nil {
		return nil, err
	}
	configuration.SetPlatformVirtualMachineConfiguration(platform)

	rootAttachment, err := vz.NewDiskImageStorageDeviceAttachmentWithCacheAndSync(
		paths.RootDisk, false, vz.DiskImageCachingModeCached,
		vz.DiskImageSynchronizationModeFsync,
	)
	if err != nil {
		return nil, err
	}
	rootDevice, err := vz.NewVirtioBlockDeviceConfiguration(rootAttachment)
	if err != nil {
		return nil, err
	}
	cidataAttachment, err := vz.NewDiskImageStorageDeviceAttachment(paths.CIDataISO, true)
	if err != nil {
		return nil, err
	}
	cidataDevice, err := vz.NewVirtioBlockDeviceConfiguration(cidataAttachment)
	if err != nil {
		return nil, err
	}
	configuration.SetStorageDevicesVirtualMachineConfiguration(
		[]vz.StorageDeviceConfiguration{rootDevice, cidataDevice},
	)

	shares := make([]vz.DirectorySharingDeviceConfiguration, 0, 3)
	for _, share := range []struct {
		tag      string
		path     string
		readOnly bool
	}{
		{tag: requestShareTag, path: paths.RequestDirectory, readOnly: true},
		{tag: helperShareTag, path: paths.HelperDirectory, readOnly: true},
		{tag: receiptShareTag, path: paths.ReceiptDirectory, readOnly: false},
	} {
		directory, err := vz.NewSharedDirectory(share.path, share.readOnly)
		if err != nil {
			return nil, err
		}
		single, err := vz.NewSingleDirectoryShare(directory)
		if err != nil {
			return nil, err
		}
		device, err := vz.NewVirtioFileSystemDeviceConfiguration(share.tag)
		if err != nil {
			return nil, err
		}
		device.SetDirectoryShare(single)
		shares = append(shares, device)
	}
	configuration.SetDirectorySharingDevicesVirtualMachineConfiguration(shares)

	serialAttachment, err := vz.NewFileSerialPortAttachment(paths.SerialLog, false)
	if err != nil {
		return nil, err
	}
	serial, err := vz.NewVirtioConsoleDeviceSerialPortConfiguration(serialAttachment)
	if err != nil {
		return nil, err
	}
	configuration.SetSerialPortsVirtualMachineConfiguration(
		[]*vz.VirtioConsoleDeviceSerialPortConfiguration{serial},
	)
	entropy, err := vz.NewVirtioEntropyDeviceConfiguration()
	if err != nil {
		return nil, err
	}
	configuration.SetEntropyDevicesVirtualMachineConfiguration(
		[]*vz.VirtioEntropyDeviceConfiguration{entropy},
	)
	balloon, err := vz.NewVirtioTraditionalMemoryBalloonDeviceConfiguration()
	if err != nil {
		return nil, err
	}
	configuration.SetMemoryBalloonDevicesVirtualMachineConfiguration(
		[]vz.MemoryBalloonDeviceConfiguration{balloon},
	)

	// This explicit empty assignment is the security boundary that stock Lima's
	// ordinary VZ startup path cannot express: this VM has no network adapter.
	configuration.SetNetworkDevicesVirtualMachineConfiguration(
		[]*vz.VirtioNetworkDeviceConfiguration{},
	)
	if len(configuration.NetworkDevices()) != 0 {
		return nil, errors.New("VZ configuration contains a network device")
	}
	for _, path := range []string{
		paths.EFIVariableStore, paths.MachineIdentifier, paths.SerialLog,
	} {
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, err
		}
	}
	return configuration, nil
}

func requireProtectedDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() ||
		info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("unprotected adoption directory %q", filepath.Base(path))
	}
	return nil
}

func requireRegularFile(path string, readOnly, executable bool) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
		info.Size() <= 0 || info.Mode().Perm()&0o022 != 0 ||
		(readOnly && info.Mode().Perm()&0o222 != 0) ||
		(executable && info.Mode().Perm()&0o111 == 0) {
		return fmt.Errorf("invalid adoption file %q", filepath.Base(path))
	}
	return nil
}

func requireDirectoryEntries(path string, expected []string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		actual = append(actual, entry.Name())
	}
	slices.Sort(actual)
	expected = append([]string(nil), expected...)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		return fmt.Errorf("adoption directory %q has unexpected entries", filepath.Base(path))
	}
	return nil
}

func writeExclusiveFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	writeErr := error(nil)
	if _, err := file.Write(data); err != nil {
		writeErr = err
	} else if err := file.Sync(); err != nil {
		writeErr = err
	}
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func syncExecutorDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

func waitForReceipt(path string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		if err := requireRegularFile(path, false, false); err == nil {
			info, statErr := os.Lstat(path)
			if statErr == nil && info.Size() <= maximumExecutorDocumentBytes {
				return nil
			}
		}
		if !time.Now().Before(deadline) {
			return errors.New("guest stopped without a bounded adoption receipt")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type boundedCommandOutput struct {
	buffer    bytes.Buffer
	truncated bool
}

func (output *boundedCommandOutput) Write(data []byte) (int, error) {
	original := len(data)
	remaining := 4096 - output.buffer.Len()
	if remaining <= 0 {
		output.truncated = true
		return original, nil
	}
	if len(data) > remaining {
		data = data[:remaining]
		output.truncated = true
	}
	_, _ = output.buffer.Write(data)
	return original, nil
}

func (output *boundedCommandOutput) String() string {
	value := strings.TrimSpace(output.buffer.String())
	if output.truncated {
		value += " [truncated]"
	}
	return value
}
