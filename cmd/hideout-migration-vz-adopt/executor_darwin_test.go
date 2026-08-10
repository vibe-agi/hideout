//go:build darwin && arm64

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Code-Hex/vz/v3"
	"github.com/vibe-agi/hideout/internal/migration/vzexecutor"
)

func TestVirtualMachineConfigurationHasNoNetworkAndBindsImportedDisks(t *testing.T) {
	if !adoptionAttachedDiskReadOnly {
		t.Fatal("temporary adoption guest has write authority over an imported attached disk")
	}
	stage := filepath.Join(t.TempDir(), "stage")
	request := vzexecutor.ExecutionRequest{
		Schema: vzexecutor.ExecutionRequestSchema, StageDirectory: stage,
		RootDiskRelativePath: filepath.Join("instances", "backend_dev1234", "disk"),
		ControlRelativePath:  filepath.Join("adoption", "envref_dev1234", "control"),
		AttachedDisks: []vzexecutor.AttachedDisk{{
			DiskID:         "disk_data1234",
			RelativePath:   filepath.Join("disks", "disk_handle1234", "datadisk"),
			GuestMountPath: "/mnt/lima-disk_handle1234", FSType: "ext4",
		}},
		ExecutionNonce: "nonce_exec1234", CPUCount: 2, MemoryBytes: 1 << 30,
	}
	paths, err := request.Paths()
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{
		filepath.Dir(paths.RootDisk), paths.RequestDirectory, paths.HelperDirectory,
		paths.ReceiptDirectory, paths.RuntimeDirectory,
		filepath.Dir(paths.AttachedDisks[0].HostPath),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string][]byte{
		paths.RootDisk:                  make([]byte, 1<<20),
		paths.CIDataISO:                 make([]byte, 1<<20),
		paths.GuestRequest:              []byte("{}"),
		paths.GuestHelper:               []byte("helper"),
		paths.AttachedDisks[0].HostPath: make([]byte, 1<<20),
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
	storage := configuration.StorageDevices()
	if got := len(storage); got != 3 {
		t.Fatalf("storage device count=%d want root+CIDATA+attached", got)
	}
	attached, ok := storage[2].(*vz.VirtioBlockDeviceConfiguration)
	if !ok {
		t.Fatalf("attached storage type=%T", storage[2])
	}
	identifier, err := attached.BlockDeviceIdentifier()
	if err != nil {
		t.Fatal(err)
	}
	if identifier != paths.AttachedDisks[0].BlockDeviceIdentifier {
		t.Fatalf("attached identifier=%q want=%q", identifier, paths.AttachedDisks[0].BlockDeviceIdentifier)
	}
}
