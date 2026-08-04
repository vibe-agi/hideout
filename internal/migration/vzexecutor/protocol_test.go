package vzexecutor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/migration"
)

func TestExecutionRequestDerivesOnlyOperationOwnedPaths(t *testing.T) {
	stage := filepath.Join(t.TempDir(), "stage")
	request := ExecutionRequest{
		Schema:               ExecutionRequestSchema,
		StageDirectory:       stage,
		RootDiskRelativePath: filepath.Join("instances", "backend_dev1234", "disk"),
		ControlRelativePath: filepath.Join(
			"adoption", "envref_dev1234", "control",
		),
		ExecutionNonce: "nonce_exec1234",
		CPUCount:       2,
		MemoryBytes:    1 << 30,
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	paths, err := request.Paths()
	if err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"root":    paths.RootDisk,
		"request": paths.GuestRequest,
		"helper":  paths.GuestHelper,
		"receipt": paths.GuestReceipt,
		"efi":     paths.EFIVariableStore,
		"cidata":  paths.CIDataISO,
	} {
		if !strings.HasPrefix(path, stage+string(filepath.Separator)) {
			t.Fatalf("%s path escaped stage: %q", name, path)
		}
	}
	if got := paths.GuestRequest; got != filepath.Join(
		stage, "adoption", "envref_dev1234", "control", "request", "request.json",
	) {
		t.Fatalf("guest request path=%q", got)
	}
}

func TestExecutionRequestRejectsTraversalAndResourceMutation(t *testing.T) {
	valid := ExecutionRequest{
		Schema: ExecutionRequestSchema, StageDirectory: filepath.Join(t.TempDir(), "stage"),
		RootDiskRelativePath: filepath.Join("instances", "backend_dev1234", "disk"),
		ControlRelativePath:  filepath.Join("adoption", "envref_dev1234", "control"),
		ExecutionNonce:       "nonce_exec1234", CPUCount: 2, MemoryBytes: 1 << 30,
	}
	mutations := map[string]func(*ExecutionRequest){
		"root traversal": func(request *ExecutionRequest) {
			request.RootDiskRelativePath = filepath.Join("..", "source", "disk")
		},
		"control traversal": func(request *ExecutionRequest) {
			request.ControlRelativePath = filepath.Join("adoption", "..", "control")
		},
		"relative stage": func(request *ExecutionRequest) { request.StageDirectory = "stage" },
		"oversized cpu":  func(request *ExecutionRequest) { request.CPUCount = 65 },
		"tiny memory":    func(request *ExecutionRequest) { request.MemoryBytes = 128 << 20 },
		"bad nonce":      func(request *ExecutionRequest) { request.ExecutionNonce = "exec" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if err := request.Validate(); err == nil {
				t.Fatalf("mutation accepted: %+v", request)
			}
		})
	}
}

func TestExecutionResponseBindsZeroNetworkShutdownProof(t *testing.T) {
	response := ExecutionResponse{
		Schema: ExecutionResponseSchema, ExecutionNonce: "nonce_exec1234",
		Started: true, Stopped: true, NetworkDeviceCount: 0,
		ReceiptObserved: true, StopReason: StopReasonReceiptAndGuestShutdown,
	}
	proof, err := response.ExpectedShutdownProof()
	if err != nil {
		t.Fatal(err)
	}
	response.ShutdownProof = proof
	if err := response.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ExecutionResponse){
		"network device": func(value *ExecutionResponse) { value.NetworkDeviceCount = 1 },
		"not stopped":    func(value *ExecutionResponse) { value.Stopped = false },
		"missing receipt": func(value *ExecutionResponse) {
			value.ReceiptObserved = false
		},
		"forged proof": func(value *ExecutionResponse) {
			value.ShutdownProof = migration.Digest("sha256:" + strings.Repeat("f", 64))
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := response
			mutate(&changed)
			if err := changed.Validate(); err == nil {
				t.Fatalf("mutation accepted: %+v", changed)
			}
		})
	}
}

func TestProbeIdentityBindsExecutorBytesAndNoNetworkContract(t *testing.T) {
	probe := CurrentProbe()
	if err := probe.Validate(); err != nil {
		t.Fatal(err)
	}
	first, err := probe.ProofIdentity(migration.Digest("sha256:" + strings.Repeat("a", 64)))
	if err != nil {
		t.Fatal(err)
	}
	second, err := probe.ProofIdentity(migration.Digest("sha256:" + strings.Repeat("b", 64)))
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasPrefix(first, "vz-offline-v1:") {
		t.Fatalf("proof identities first=%q second=%q", first, second)
	}
	probe.NetworkDeviceCount = 1
	if err := probe.Validate(); err == nil {
		t.Fatal("network-enabled probe was accepted")
	}
}
