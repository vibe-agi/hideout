//go:build darwin && arm64

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vibe-agi/hideout/internal/migration/vzexecutor"
)

func TestVirtualMachineConfigurationHasNoNetworkOrImportedDisks(t *testing.T) {
	stage := filepath.Join(t.TempDir(), "stage")
	request := vzexecutor.ExecutionRequest{
		Schema: vzexecutor.ExecutionRequestSchema, StageDirectory: stage,
		RootDiskRelativePath: filepath.Join("instances", "backend_dev1234", "disk"),
		ControlRelativePath:  filepath.Join("adoption", "envref_dev1234", "control"),
		ExecutionNonce:       "nonce_exec1234", CPUCount: 2, MemoryBytes: 1 << 30,
	}
	paths, err := request.Paths()
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{
		filepath.Dir(paths.RootDisk), paths.RequestDirectory, paths.HelperDirectory,
		paths.ReceiptDirectory, paths.RuntimeDirectory,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string][]byte{
		paths.RootDisk:     make([]byte, 1<<20),
		paths.CIDataISO:    make([]byte, 1<<20),
		paths.GuestRequest: []byte("{}"),
		paths.GuestHelper:  []byte("helper"),
	} {
		mode := os.FileMode(0o600)
		if path == paths.GuestHelper {
			mode = 0o500
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	configuration, err := buildVirtualMachineConfiguration(request, paths)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(configuration.NetworkDevices()); got != 0 {
		t.Fatalf("network device count=%d want 0", got)
	}
	if got := len(configuration.StorageDevices()); got != 2 {
		t.Fatalf("storage device count=%d want root+CIDATA only", got)
	}
}
