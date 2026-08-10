package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/migration/vzexecutor"
)

func TestProbeReportsPinnedZeroNetworkExecutorContract(t *testing.T) {
	var output bytes.Buffer
	if err := runCLIWithCapabilityProbe(
		[]string{"--probe"}, strings.NewReader(""), &output, func() error { return nil },
	); err != nil {
		t.Fatal(err)
	}
	var probe vzexecutor.Probe
	decoder := json.NewDecoder(&output)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&probe); err != nil {
		t.Fatal(err)
	}
	if err := probe.Validate(); err != nil {
		t.Fatal(err)
	}
	if probe.NetworkDeviceCount != 0 || probe.ControlChannel != "virtiofs-private" {
		t.Fatalf("probe=%+v", probe)
	}
}

func TestProbeFailsClosedWhenVirtualizationCapabilityIsUnavailable(t *testing.T) {
	var output bytes.Buffer
	err := runCLIWithCapabilityProbe(
		[]string{"--probe"}, strings.NewReader(""), &output,
		func() error { return errors.New("private detail") },
	)
	if err == nil || strings.Contains(err.Error(), "private detail") || output.Len() != 0 {
		t.Fatalf("probe error=%v output=%q", err, output.String())
	}
}

func TestCLIRejectsArgumentsAndUnknownRequestFields(t *testing.T) {
	if err := runCLI([]string{"--root", "/tmp/disk"}, strings.NewReader(""), &bytes.Buffer{}); err == nil {
		t.Fatal("untyped execution arguments were accepted")
	}
	input := fmt.Sprintf(`{"schema":%q,"script":"curl example.com"}`,
		vzexecutor.ExecutionRequestSchema)
	if err := runCLI(nil, strings.NewReader(input), &bytes.Buffer{}); err == nil ||
		strings.Contains(err.Error(), "curl") {
		t.Fatalf("unknown-field error=%v", err)
	}
}

func TestCloudBoothookContainsOnlyFixedOfflineMountsAndHelperEntrypoint(t *testing.T) {
	request := vzexecutor.ExecutionRequest{
		Schema: vzexecutor.ExecutionRequestSchema, StageDirectory: filepath.Join(t.TempDir(), "stage"),
		RootDiskRelativePath: filepath.Join("instances", "backend_dev1234", "disk"),
		ControlRelativePath:  filepath.Join("adoption", "envref_dev1234", "control"),
		AttachedDisks: []vzexecutor.AttachedDisk{{
			DiskID:         "disk_data1234",
			RelativePath:   filepath.Join("disks", "disk_handle1234", "datadisk"),
			GuestMountPath: "/mnt/lima-disk_handle1234", FSType: "ext4",
		}},
		ExecutionNonce: "nonce_exec1234", CPUCount: 2, MemoryBytes: 1 << 30,
	}
	script, err := adoptionCloudBoothook(request)
	if err != nil {
		t.Fatal(err)
	}
	identifier, err := request.AttachedDisks[0].BlockDeviceIdentifier()
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"hideout-migration-request",
		"hideout-migration-helper",
		"hideout-migration-receipt",
		"/run/hideout-migration-helper/hideout-migration-adopt",
		"-t virtiofs",
		"/dev/disk/by-id/virtio-" + identifier + "-part1",
		"/mnt/lima-disk_handle1234",
		"-t 'ext4' -o ro,nodev,nosuid,noexec",
		"poweroff -f",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("cloud boothook lacks %q: %s", required, script)
		}
	}
	for _, forbidden := range []string{
		"curl", "wget", "ssh", "http://", "https://", "$1", "eval",
		"mkfs", "sfdisk", "fdisk", "parted", "wipefs",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("cloud boothook contains forbidden %q: %s", forbidden, script)
		}
	}
}

func TestCloudBoothookRejectsUntypedAttachedDiskAuthority(t *testing.T) {
	request := vzexecutor.ExecutionRequest{
		Schema: vzexecutor.ExecutionRequestSchema, StageDirectory: filepath.Join(t.TempDir(), "stage"),
		RootDiskRelativePath: filepath.Join("instances", "backend_dev1234", "disk"),
		ControlRelativePath:  filepath.Join("adoption", "envref_dev1234", "control"),
		AttachedDisks: []vzexecutor.AttachedDisk{{
			DiskID:         "disk_data1234",
			RelativePath:   filepath.Join("disks", "disk_handle1234", "datadisk"),
			GuestMountPath: "/mnt/lima-disk_handle1234;curl", FSType: "ext4",
		}},
		ExecutionNonce: "nonce_exec1234", CPUCount: 2, MemoryBytes: 1 << 30,
	}
	if script, err := adoptionCloudBoothook(request); err == nil || script != "" {
		t.Fatalf("unsafe boothook script=%q error=%v", script, err)
	}
}

func TestMaximumAttachedDiskRequestAndBoothookRemainBounded(t *testing.T) {
	request := vzexecutor.ExecutionRequest{
		Schema: vzexecutor.ExecutionRequestSchema, StageDirectory: filepath.Join(t.TempDir(), "stage"),
		RootDiskRelativePath: filepath.Join("instances", "backend_dev1234", "disk"),
		ControlRelativePath:  filepath.Join("adoption", "envref_dev1234", "control"),
		ExecutionNonce:       "nonce_exec1234", CPUCount: 2, MemoryBytes: 1 << 30,
	}
	for index := 0; index < 256; index++ {
		handle := fixedWidthExecutorToken("disk_handle", index, 128)
		request.AttachedDisks = append(request.AttachedDisks, vzexecutor.AttachedDisk{
			DiskID: migration.OpaqueID(
				fixedWidthExecutorToken("disk_data", index, 128),
			),
			RelativePath:   filepath.Join("disks", handle, "datadisk"),
			GuestMountPath: "/mnt/lima-" + handle,
			FSType:         "ext4",
		})
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > maximumExecutorRequestBytes {
		t.Fatalf("maximum request bytes=%d limit=%d", len(data), maximumExecutorRequestBytes)
	}
	script, err := adoptionCloudBoothook(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(script) > maximumExecutorRequestBytes {
		t.Fatalf("maximum boothook bytes=%d limit=%d", len(script), maximumExecutorRequestBytes)
	}
}

func fixedWidthExecutorToken(prefix string, index, width int) string {
	value := fmt.Sprintf("%s%03d_", prefix, index)
	return value + strings.Repeat("a", width-len(value))
}
