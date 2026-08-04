package vzexecutor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"github.com/vibe-agi/hideout/internal/migration"
)

const (
	ExecutionProtocol                 = "hideout.migration-vz-adopt/v1"
	ExecutionRequestSchema            = "hideout.migration-vz-adopt-request/v1"
	ExecutionResponseSchema           = "hideout.migration-vz-adopt-response/v1"
	ProbeSchema                       = "hideout.migration-vz-adopt-probe/v1"
	ExecutorVersion                   = "1.0.0"
	GuestHelperFilename               = "hideout-migration-adopt"
	StopReasonReceiptAndGuestShutdown = "receipt-and-guest-shutdown"

	minimumMemoryBytes = 512 << 20
	maximumMemoryBytes = 8 << 30
	maximumCPUCount    = 64
)

// ExecutionRequest contains paths and resource bounds only. It cannot carry a
// command, script, environment variable, network option, or imported runtime
// configuration.
type ExecutionRequest struct {
	Schema               string             `json:"schema"`
	StageDirectory       string             `json:"stageDirectory"`
	RootDiskRelativePath string             `json:"rootDiskRelativePath"`
	ControlRelativePath  string             `json:"controlRelativePath"`
	ExecutionNonce       migration.OpaqueID `json:"executionNonce"`
	CPUCount             uint               `json:"cpuCount"`
	MemoryBytes          uint64             `json:"memoryBytes"`
}

type ExecutionPaths struct {
	Stage             string
	RootDisk          string
	Control           string
	RequestDirectory  string
	GuestRequest      string
	HelperDirectory   string
	GuestHelper       string
	ReceiptDirectory  string
	GuestReceipt      string
	ExecutorResponse  string
	RuntimeDirectory  string
	CIDDataSource     string
	CIDataISO         string
	EFIVariableStore  string
	MachineIdentifier string
	SerialLog         string
}

func (request ExecutionRequest) Validate() error {
	if request.Schema != ExecutionRequestSchema ||
		!cleanAbsolutePath(request.StageDirectory) || request.StageDirectory == string(filepath.Separator) {
		return errors.New("VZ adoption request envelope is invalid")
	}
	rootParts, ok := exactRelativeParts(request.RootDiskRelativePath, 3)
	if !ok || rootParts[0] != "instances" || rootParts[2] != "disk" ||
		!validPathToken(rootParts[1]) {
		return errors.New("VZ adoption root disk path is invalid")
	}
	controlParts, ok := exactRelativeParts(request.ControlRelativePath, 3)
	if !ok || controlParts[0] != "adoption" || controlParts[2] != "control" ||
		!validPathToken(controlParts[1]) {
		return errors.New("VZ adoption control path is invalid")
	}
	if _, err := migration.ParseOpaqueID(string(request.ExecutionNonce)); err != nil {
		return errors.New("VZ adoption execution nonce is invalid")
	}
	if request.CPUCount == 0 || request.CPUCount > maximumCPUCount ||
		request.MemoryBytes < minimumMemoryBytes ||
		request.MemoryBytes > maximumMemoryBytes ||
		request.MemoryBytes%(1<<20) != 0 {
		return errors.New("VZ adoption resources are invalid")
	}
	return nil
}

func (request ExecutionRequest) Paths() (ExecutionPaths, error) {
	if err := request.Validate(); err != nil {
		return ExecutionPaths{}, err
	}
	control := filepath.Join(request.StageDirectory, request.ControlRelativePath)
	paths := ExecutionPaths{
		Stage:            request.StageDirectory,
		RootDisk:         filepath.Join(request.StageDirectory, request.RootDiskRelativePath),
		Control:          control,
		RequestDirectory: filepath.Join(control, "request"),
		HelperDirectory:  filepath.Join(control, "helper"),
		ReceiptDirectory: filepath.Join(control, "receipt"),
		RuntimeDirectory: filepath.Join(control, "runtime"),
	}
	paths.GuestRequest = filepath.Join(paths.RequestDirectory, "request.json")
	paths.GuestHelper = filepath.Join(paths.HelperDirectory, GuestHelperFilename)
	paths.GuestReceipt = filepath.Join(paths.ReceiptDirectory, "receipt.json")
	paths.ExecutorResponse = filepath.Join(paths.Control, "executor-response.json")
	paths.CIDDataSource = filepath.Join(paths.RuntimeDirectory, "cidata-source")
	paths.CIDataISO = filepath.Join(paths.RuntimeDirectory, "cidata.iso")
	paths.EFIVariableStore = filepath.Join(paths.RuntimeDirectory, "efi-variable-store")
	paths.MachineIdentifier = filepath.Join(paths.RuntimeDirectory, "machine-identifier")
	paths.SerialLog = filepath.Join(paths.RuntimeDirectory, "serial.log")
	for _, path := range []string{
		paths.RootDisk, paths.Control, paths.RequestDirectory, paths.GuestRequest,
		paths.HelperDirectory, paths.GuestHelper, paths.ReceiptDirectory,
		paths.GuestReceipt, paths.ExecutorResponse, paths.RuntimeDirectory, paths.CIDDataSource,
		paths.CIDataISO, paths.EFIVariableStore, paths.MachineIdentifier,
		paths.SerialLog,
	} {
		if !pathWithin(request.StageDirectory, path) {
			return ExecutionPaths{}, errors.New("VZ adoption path escaped its stage")
		}
	}
	return paths, nil
}

type ExecutionResponse struct {
	Schema             string             `json:"schema"`
	ExecutionNonce     migration.OpaqueID `json:"executionNonce"`
	Started            bool               `json:"started"`
	Stopped            bool               `json:"stopped"`
	NetworkDeviceCount uint8              `json:"networkDeviceCount"`
	ReceiptObserved    bool               `json:"receiptObserved"`
	StopReason         string             `json:"stopReason"`
	ShutdownProof      migration.Digest   `json:"shutdownProof"`
}

func (response ExecutionResponse) Validate() error {
	if response.Schema != ExecutionResponseSchema ||
		migrationOpaqueIDInvalid(response.ExecutionNonce) ||
		!response.Started || !response.Stopped || response.NetworkDeviceCount != 0 ||
		!response.ReceiptObserved || response.StopReason != StopReasonReceiptAndGuestShutdown ||
		response.ShutdownProof.Validate() != nil {
		return errors.New("VZ adoption response is invalid")
	}
	expected, err := response.ExpectedShutdownProof()
	if err != nil || response.ShutdownProof != expected {
		return errors.New("VZ adoption shutdown proof is invalid")
	}
	return nil
}

func (response ExecutionResponse) ExpectedShutdownProof() (migration.Digest, error) {
	unsigned := response
	unsigned.ShutdownProof = ""
	data, err := json.Marshal(unsigned)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return migration.Digest("sha256:" + hex.EncodeToString(digest[:])), nil
}

type Probe struct {
	Schema             string `json:"schema"`
	Protocol           string `json:"protocol"`
	Version            string `json:"version"`
	HostOS             string `json:"hostOS"`
	HostArch           string `json:"hostArch"`
	Hypervisor         string `json:"hypervisor"`
	NetworkDeviceCount uint8  `json:"networkDeviceCount"`
	ControlChannel     string `json:"controlChannel"`
	BootTrigger        string `json:"bootTrigger"`
}

func CurrentProbe() Probe {
	return Probe{
		Schema: ProbeSchema, Protocol: ExecutionProtocol, Version: ExecutorVersion,
		HostOS: runtime.GOOS, HostArch: runtime.GOARCH,
		Hypervisor:         "apple-virtualization-framework",
		NetworkDeviceCount: 0, ControlChannel: "virtiofs-private",
		BootTrigger: "nocloud-fixed-cloud-boothook",
	}
}

func (probe Probe) Validate() error {
	expected := CurrentProbe()
	if !reflect.DeepEqual(probe, expected) || probe.HostOS != "darwin" ||
		probe.HostArch != "arm64" || probe.NetworkDeviceCount != 0 {
		return errors.New("VZ adoption probe is invalid or unsupported")
	}
	return nil
}

func (probe Probe) ProofIdentity(executorDigest migration.Digest) (string, error) {
	if err := probe.Validate(); err != nil || executorDigest.Validate() != nil {
		return "", errors.New("VZ adoption proof inputs are invalid")
	}
	data, err := json.Marshal(struct {
		Probe          Probe            `json:"probe"`
		ExecutorDigest migration.Digest `json:"executorDigest"`
	}{Probe: probe, ExecutorDigest: executorDigest})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return fmt.Sprintf("vz-offline-v1:%x", digest[:]), nil
}

func cleanAbsolutePath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && strings.TrimSpace(path) == path
}

func exactRelativeParts(path string, count int) ([]string, bool) {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path ||
		strings.Contains(path, "\\") {
		return nil, false
	}
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) != count {
		return nil, false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, false
		}
	}
	return parts, true
}

func validPathToken(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		if index == 0 {
			return false
		}
		return false
	}
	return value[0] >= 'a' && value[0] <= 'z'
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative)
}

func migrationOpaqueIDInvalid(value migration.OpaqueID) bool {
	_, err := migration.ParseOpaqueID(string(value))
	return err != nil
}
