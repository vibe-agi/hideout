package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/migration"
	"golang.org/x/crypto/ssh"
)

func TestSafeCloneAdoptionProducesIndependentGuestIdentities(t *testing.T) {
	first := newAdoptionFixture(t, migration.GuestIdentitySafeClone, 0x11)
	second := newAdoptionFixture(t, migration.GuestIdentitySafeClone, 0x11)
	second.request.OperationID = "op_import5678"
	writeAdoptionRequestFixture(t, second.requestPath, second.request)

	firstRequestDigest := fileDigestFixture(t, first.requestPath)
	firstReceipt := runSafeCloneFixture(t, first, 0x21, 0x31)
	secondReceipt := runSafeCloneFixture(t, second, 0x22, 0x32)

	for _, pair := range []struct {
		fixture *adoptionFixture
		receipt migration.AdoptionReceipt
	}{
		{fixture: first, receipt: firstReceipt},
		{fixture: second, receipt: secondReceipt},
	} {
		if err := pair.receipt.MatchesRequest(pair.fixture.request); err != nil {
			t.Fatal(err)
		}
		if pair.fixture.shutdowns != 1 || !pair.receipt.CompletionMarker ||
			pair.receipt.Status != migration.AdoptionReceiptStatusCompleted {
			t.Fatalf("receipt=%+v shutdowns=%d", pair.receipt, pair.fixture.shutdowns)
		}
		if pair.receipt.PostIdentity.Equal(pair.fixture.sourceIdentity) {
			t.Fatalf("Safe Clone preserved source identity: %+v", pair.receipt.PostIdentity)
		}
		if len(pair.receipt.ActionResults) != 3 ||
			pair.receipt.ActionResults[0].Action != migration.AdoptionActionResetMachineID ||
			pair.receipt.ActionResults[1].Action != migration.AdoptionActionResetSSHHostKeys ||
			pair.receipt.ActionResults[2].Action != migration.AdoptionActionInstallSSHKeys {
			t.Fatalf("Safe Clone action results=%+v", pair.receipt.ActionResults)
		}
		machineID, err := os.ReadFile(filepath.Join(pair.fixture.rootPath, "etc", "machine-id"))
		if err != nil {
			t.Fatal(err)
		}
		dbusID, err := os.ReadFile(
			filepath.Join(pair.fixture.rootPath, "var", "lib", "dbus", "machine-id"),
		)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(machineID, dbusID) {
			t.Fatalf("machine-id=%q dbus=%q", machineID, dbusID)
		}
		assertDestinationSSHKeyInstalled(t, pair.fixture)
	}
	if firstReceipt.PostIdentity.Equal(*secondReceipt.PostIdentity) ||
		firstReceipt.PostIdentity.MachineIDDigest == secondReceipt.PostIdentity.MachineIDDigest {
		t.Fatalf(
			"independent Safe Clone imports shared identity: first=%+v second=%+v",
			firstReceipt.PostIdentity, secondReceipt.PostIdentity,
		)
	}
	if got := fileDigestFixture(t, first.requestPath); got != firstRequestDigest {
		t.Fatalf("read-only adoption request changed: got=%s want=%s", got, firstRequestDigest)
	}
}

func TestExactGuestRestorePreservesIdentityWithoutCallingResetters(t *testing.T) {
	fixture := newAdoptionFixture(t, migration.GuestIdentityExactRestore, 0x41)
	before := guestIdentityFilesDigest(t, fixture.rootPath)
	resetCalls := 0
	runner := fixture.runner(errorReader{errors.New("randomness must not be read")})
	runner.generateSSHHostKeys = func(string) error {
		resetCalls++
		return errors.New("SSH reset must not run")
	}
	if err := runner.run(); err != nil {
		t.Fatal(err)
	}
	receipt, err := readAdoptionReceipt(fixture.receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.MatchesRequest(fixture.request); err != nil {
		t.Fatal(err)
	}
	if !receipt.PostIdentity.Equal(fixture.sourceIdentity) || resetCalls != 0 ||
		fixture.shutdowns != 1 || len(receipt.ActionResults) != 2 ||
		receipt.ActionResults[0].Action != migration.AdoptionActionPreserveIdentity ||
		receipt.ActionResults[1].Action != migration.AdoptionActionInstallSSHKeys {
		t.Fatalf(
			"Exact Guest Restore receipt=%+v resetCalls=%d shutdowns=%d",
			receipt, resetCalls, fixture.shutdowns,
		)
	}
	if after := guestIdentityFilesDigest(t, fixture.rootPath); after != before {
		t.Fatalf("Exact Guest Restore changed guest identity files: before=%s after=%s", before, after)
	}
	assertDestinationSSHKeyInstalled(t, fixture)
}

func TestAdoptionRebindsAttachedDiskMountWithoutFormattingAuthority(t *testing.T) {
	fixture := newAdoptionFixture(t, migration.GuestIdentityExactRestore, 0x42)
	sourceMount := filepath.Join(fixture.rootPath, "mnt", "lima-source-data")
	if err := os.MkdirAll(sourceMount, 0o755); err != nil {
		t.Fatal(err)
	}
	binding := migration.DiskMountBinding{
		DiskID: "disk_attached1", SourceGuestPath: "/mnt/lima-source-data",
		DestinationGuestPath: "/mnt/lima-disk_destination1", FSType: "ext4",
	}
	destinationMount := writeAdoptionMountInventory(t, fixture.rootPath, binding, "ext4")
	if err := os.WriteFile(
		filepath.Join(destinationMount, "attached-proof"), []byte("preserved"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	fixture.request.MountBindings = []migration.DiskMountBinding{binding}
	fixture.request.PermittedActions = []string{
		migration.AdoptionActionPreserveIdentity,
		migration.AdoptionActionRebindDiskMounts,
		migration.AdoptionActionInstallSSHKeys,
	}
	writeAdoptionRequestFixture(t, fixture.requestPath, fixture.request)

	runner := fixture.runner(errorReader{errors.New("randomness must not be read")})
	if err := runner.run(); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(sourceMount)
	if err != nil || target != binding.DestinationGuestPath {
		t.Fatalf("attached mount alias target=%q err=%v", target, err)
	}
	// The production symlink is intentionally guest-absolute. A host-side temp
	// root cannot follow it without chroot, so assert the exact link above and
	// independently prove that adoption left the mounted target data intact.
	if proof, readErr := os.ReadFile(filepath.Join(destinationMount, "attached-proof")); readErr != nil || string(proof) != "preserved" {
		t.Fatalf("attached mount target data changed: data=%q err=%v", proof, readErr)
	}
	receipt, err := readAdoptionReceipt(fixture.receiptPath)
	if err != nil || receipt.MatchesRequest(fixture.request) != nil ||
		len(receipt.ActionResults) != 3 ||
		receipt.ActionResults[1].Action != migration.AdoptionActionRebindDiskMounts {
		t.Fatalf("attached mount receipt=%+v err=%v", receipt, err)
	}

	if err := os.Remove(fixture.receiptPath); err != nil {
		t.Fatal(err)
	}
	if err := runner.run(); err != nil {
		t.Fatalf("idempotent mount rebind failed: %v", err)
	}
	target, err = os.Readlink(sourceMount)
	if err != nil || target != binding.DestinationGuestPath {
		t.Fatalf("replayed attached mount alias target=%q err=%v", target, err)
	}
}

func TestAdoptionRefusesToHideFilesUnderAttachedDiskMountAlias(t *testing.T) {
	fixture := newAdoptionFixture(t, migration.GuestIdentityExactRestore, 0x46)
	sourceMount := filepath.Join(fixture.rootPath, "mnt", "lima-source-data")
	if err := os.MkdirAll(sourceMount, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceMount, "hidden.txt"), []byte("retain"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.request.MountBindings = []migration.DiskMountBinding{{
		DiskID: "disk_attached1", SourceGuestPath: "/mnt/lima-source-data",
		DestinationGuestPath: "/mnt/lima-disk_destination1", FSType: "ext4",
	}}
	writeAdoptionMountInventory(t, fixture.rootPath, fixture.request.MountBindings[0], "ext4")
	fixture.request.PermittedActions = []string{
		migration.AdoptionActionPreserveIdentity,
		migration.AdoptionActionRebindDiskMounts,
		migration.AdoptionActionInstallSSHKeys,
	}
	writeAdoptionRequestFixture(t, fixture.requestPath, fixture.request)
	err := fixture.runner(errorReader{errors.New("randomness must not be read")}).run()
	if err == nil || err.Error() != "migration.adoption.disk_mount_rebind_failed" {
		t.Fatalf("non-empty source mount error=%v", err)
	}
	if data, readErr := os.ReadFile(filepath.Join(sourceMount, "hidden.txt")); readErr != nil || string(data) != "retain" {
		t.Fatalf("non-empty source mount changed: data=%q err=%v", data, readErr)
	}
}

func TestAdoptionRefusesUnprovedAttachedDiskMount(t *testing.T) {
	fixture := newAdoptionFixture(t, migration.GuestIdentityExactRestore, 0x47)
	sourceMount := filepath.Join(fixture.rootPath, "mnt", "lima-source-data")
	if err := os.MkdirAll(sourceMount, 0o755); err != nil {
		t.Fatal(err)
	}
	binding := migration.DiskMountBinding{
		DiskID: "disk_attached1", SourceGuestPath: "/mnt/lima-source-data",
		DestinationGuestPath: "/mnt/lima-disk_destination1", FSType: "ext4",
	}
	writeAdoptionMountInventory(t, fixture.rootPath, binding, "xfs")
	fixture.request.MountBindings = []migration.DiskMountBinding{binding}
	fixture.request.PermittedActions = []string{
		migration.AdoptionActionPreserveIdentity,
		migration.AdoptionActionRebindDiskMounts,
		migration.AdoptionActionInstallSSHKeys,
	}
	writeAdoptionRequestFixture(t, fixture.requestPath, fixture.request)
	err := fixture.runner(errorReader{errors.New("randomness must not be read")}).run()
	if err == nil || err.Error() != "migration.adoption.disk_mount_rebind_failed" {
		t.Fatalf("unproved attached mount error=%v", err)
	}
	if info, statErr := os.Lstat(sourceMount); statErr != nil || !info.IsDir() {
		t.Fatalf("unproved attached mount changed source path: info=%v err=%v", info, statErr)
	}
}

func TestAdoptionRefusesWritableAttachedDiskMount(t *testing.T) {
	fixture := newAdoptionFixture(t, migration.GuestIdentityExactRestore, 0x48)
	sourceMount := filepath.Join(fixture.rootPath, "mnt", "lima-source-data")
	if err := os.MkdirAll(sourceMount, 0o755); err != nil {
		t.Fatal(err)
	}
	binding := migration.DiskMountBinding{
		DiskID: "disk_attached1", SourceGuestPath: "/mnt/lima-source-data",
		DestinationGuestPath: "/mnt/lima-disk_destination1", FSType: "ext4",
	}
	writeAdoptionMountInventoryWithMode(t, fixture.rootPath, binding, "ext4", "rw")
	fixture.request.MountBindings = []migration.DiskMountBinding{binding}
	fixture.request.PermittedActions = []string{
		migration.AdoptionActionPreserveIdentity,
		migration.AdoptionActionRebindDiskMounts,
		migration.AdoptionActionInstallSSHKeys,
	}
	writeAdoptionRequestFixture(t, fixture.requestPath, fixture.request)
	err := fixture.runner(errorReader{errors.New("randomness must not be read")}).run()
	if err == nil || err.Error() != "migration.adoption.disk_mount_rebind_failed" {
		t.Fatalf("writable attached mount error=%v", err)
	}
	if info, statErr := os.Lstat(sourceMount); statErr != nil || !info.IsDir() {
		t.Fatalf("writable attached mount changed source path: info=%v err=%v", info, statErr)
	}
}

func writeAdoptionMountInventory(
	t *testing.T,
	root string,
	binding migration.DiskMountBinding,
	observedFSType string,
) string {
	t.Helper()
	return writeAdoptionMountInventoryWithMode(
		t, root, binding, observedFSType, "ro",
	)
}

func writeAdoptionMountInventoryWithMode(
	t *testing.T,
	root string,
	binding migration.DiskMountBinding,
	observedFSType,
	mountMode string,
) string {
	t.Helper()
	destination := filepath.Join(root, strings.TrimPrefix(binding.DestinationGuestPath, "/"))
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	mountInfoDir := filepath.Join(root, "proc", fmt.Sprintf("%d", os.Getpid()))
	if err := os.MkdirAll(mountInfoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mountInfo := fmt.Sprintf(
		"42 1 8:1 / %s %s,relatime - %s /dev/vdb1 %s\n",
		binding.DestinationGuestPath,
		mountMode,
		observedFSType,
		mountMode,
	)
	if err := os.WriteFile(filepath.Join(mountInfoDir, "mountinfo"), []byte(mountInfo), 0o444); err != nil {
		t.Fatal(err)
	}
	return destination
}

func TestIdentityObservationReportsEvidenceWithoutMutatingGuest(t *testing.T) {
	fixture := newAdoptionFixture(t, migration.GuestIdentityExactRestore, 0x43)
	request := migration.IdentityObservationRequest{
		Schema:         migration.IdentityObservationRequestSchema,
		OperationID:    fixture.request.OperationID,
		EnvironmentRef: fixture.request.EnvironmentRef,
		RequestNonce:   fixture.request.RequestNonce,
		ReceiptNonce:   fixture.request.ReceiptNonce,
		Helper:         fixture.request.Helper,
	}
	writeIdentityObservationRequestFixture(t, fixture.requestPath, request)
	before := guestIdentityFilesDigest(t, fixture.rootPath)
	runner := identityObservationRunner{
		rootPath: fixture.rootPath, requestPath: fixture.requestPath,
		receiptPath: fixture.receiptPath, selfPath: fixture.selfPath,
		networkClassPath: filepath.Join(fixture.rootPath, "sys", "class", "net"),
		shutdown:         func() error { fixture.shutdowns++; return nil },
	}
	if err := runner.run(); err != nil {
		t.Fatal(err)
	}
	receipt, err := readIdentityObservationReceipt(fixture.receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.MatchesRequest(request); err != nil {
		t.Fatal(err)
	}
	if receipt.Identity == nil || !receipt.Identity.Equal(fixture.sourceIdentity) ||
		fixture.shutdowns != 1 {
		t.Fatalf("identity observation receipt=%+v shutdowns=%d", receipt, fixture.shutdowns)
	}
	if after := guestIdentityFilesDigest(t, fixture.rootPath); after != before {
		t.Fatalf("identity observation mutated guest: before=%s after=%s", before, after)
	}
}

func TestIdentityObservationRejectsNetworkAndUnknownFieldsBeforeMutation(t *testing.T) {
	fixture := newAdoptionFixture(t, migration.GuestIdentityExactRestore, 0x44)
	request := migration.IdentityObservationRequest{
		Schema:         migration.IdentityObservationRequestSchema,
		OperationID:    fixture.request.OperationID,
		EnvironmentRef: fixture.request.EnvironmentRef,
		RequestNonce:   fixture.request.RequestNonce,
		ReceiptNonce:   fixture.request.ReceiptNonce,
		Helper:         fixture.request.Helper,
	}
	writeIdentityObservationRequestFixture(t, fixture.requestPath, request)
	if err := os.Mkdir(filepath.Join(
		fixture.rootPath, "sys", "class", "net", "eth0",
	), 0o700); err != nil {
		t.Fatal(err)
	}
	before := guestIdentityFilesDigest(t, fixture.rootPath)
	runner := identityObservationRunner{
		rootPath: fixture.rootPath, requestPath: fixture.requestPath,
		receiptPath: fixture.receiptPath, selfPath: fixture.selfPath,
		networkClassPath: filepath.Join(fixture.rootPath, "sys", "class", "net"),
		shutdown:         func() error { fixture.shutdowns++; return nil },
	}
	if err := runner.run(); err == nil ||
		err.Error() != "migration.identity_observation.network_present" {
		t.Fatalf("network observation error=%v", err)
	}
	receipt, err := readIdentityObservationReceipt(fixture.receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != migration.AdoptionReceiptStatusFailed ||
		receipt.FailureCode != "migration.identity_observation.network_present" ||
		receipt.Identity != nil || receipt.CompletionMarker || fixture.shutdowns != 0 {
		t.Fatalf("network observation receipt=%+v shutdowns=%d", receipt, fixture.shutdowns)
	}
	if after := guestIdentityFilesDigest(t, fixture.rootPath); after != before {
		t.Fatal("rejected identity observation mutated guest")
	}

	other := newAdoptionFixture(t, migration.GuestIdentityExactRestore, 0x45)
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"schema":`), []byte(`"command":"id","schema":`), 1)
	writeReadOnlyFixture(t, other.requestPath, data)
	unknown := identityObservationRunner{
		rootPath: other.rootPath, requestPath: other.requestPath,
		receiptPath: other.receiptPath, selfPath: other.selfPath,
		networkClassPath: filepath.Join(other.rootPath, "sys", "class", "net"),
		shutdown:         func() error { return nil },
	}
	if err := unknown.run(); err == nil ||
		err.Error() != "migration.identity_observation.request_invalid" {
		t.Fatalf("unknown-field observation error=%v", err)
	}
}

func TestAdoptionRejectsPolicyEscalationUnknownJSONAndExistingReceiptBeforeMutation(t *testing.T) {
	t.Run("policy action escalation", func(t *testing.T) {
		fixture := newAdoptionFixture(t, migration.GuestIdentitySafeClone, 0x51)
		fixture.request.PermittedActions = []string{migration.AdoptionActionPreserveIdentity}
		writeAdoptionRequestFixture(t, fixture.requestPath, fixture.request)
		before := guestIdentityFilesDigest(t, fixture.rootPath)
		err := fixture.runner(bytes.NewReader(bytes.Repeat([]byte{0x61}, 32))).run()
		if err == nil || err.Error() != "migration.adoption.request_invalid" {
			t.Fatalf("policy escalation error=%v", err)
		}
		if _, statErr := os.Stat(fixture.receiptPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("invalid request created receipt: %v", statErr)
		}
		if after := guestIdentityFilesDigest(t, fixture.rootPath); after != before {
			t.Fatal("invalid request mutated guest identity")
		}
	})

	t.Run("unknown request field", func(t *testing.T) {
		fixture := newAdoptionFixture(t, migration.GuestIdentitySafeClone, 0x52)
		data, err := json.Marshal(fixture.request)
		if err != nil {
			t.Fatal(err)
		}
		data = bytes.Replace(data, []byte(`"schema":`), []byte(`"script":"rm -rf /","schema":`), 1)
		writeReadOnlyFixture(t, fixture.requestPath, data)
		err = fixture.runner(bytes.NewReader(bytes.Repeat([]byte{0x62}, 32))).run()
		if err == nil || err.Error() != "migration.adoption.request_invalid" ||
			strings.Contains(err.Error(), "rm -rf") {
			t.Fatalf("unknown-field error=%v", err)
		}
	})

	t.Run("existing receipt", func(t *testing.T) {
		fixture := newAdoptionFixture(t, migration.GuestIdentitySafeClone, 0x53)
		before := guestIdentityFilesDigest(t, fixture.rootPath)
		if err := os.WriteFile(fixture.receiptPath, []byte("owned"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := fixture.runner(bytes.NewReader(bytes.Repeat([]byte{0x63}, 32))).run()
		if err == nil || err.Error() != "migration.adoption.receipt_write_failed" {
			t.Fatalf("existing-receipt error=%v", err)
		}
		content, readErr := os.ReadFile(fixture.receiptPath)
		if readErr != nil || string(content) != "owned" {
			t.Fatalf("existing receipt changed: content=%q err=%v", content, readErr)
		}
		if after := guestIdentityFilesDigest(t, fixture.rootPath); after != before {
			t.Fatal("existing receipt mutated guest identity")
		}
	})
}

func TestAdoptionRejectsNonLoopbackInterfaceBeforeMutation(t *testing.T) {
	fixture := newAdoptionFixture(t, migration.GuestIdentitySafeClone, 0x54)
	before := guestIdentityFilesDigest(t, fixture.rootPath)
	if err := os.Mkdir(
		filepath.Join(fixture.rootPath, "sys", "class", "net", "eth0"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	runner := fixture.runner(bytes.NewReader(bytes.Repeat([]byte{0x64}, 32)))
	keygenCalls := 0
	runner.generateSSHHostKeys = func(string) error {
		keygenCalls++
		return nil
	}
	err := runner.run()
	if err == nil || err.Error() != "migration.adoption.network_present" {
		t.Fatalf("network isolation error=%v", err)
	}
	if keygenCalls != 0 || fixture.shutdowns != 0 {
		t.Fatalf("keygenCalls=%d shutdowns=%d", keygenCalls, fixture.shutdowns)
	}
	if after := guestIdentityFilesDigest(t, fixture.rootPath); after != before {
		t.Fatalf("network isolation failure mutated identity: before=%s after=%s", before, after)
	}
	receipt, readErr := readAdoptionReceipt(fixture.receiptPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if receipt.Status != migration.AdoptionReceiptStatusFailed ||
		receipt.CompletionMarker ||
		receipt.FailureCode != "migration.adoption.network_present" {
		t.Fatalf("network isolation receipt=%+v", receipt)
	}
}

func TestAdoptionHelperMismatchAndSSHFailureLeaveNonCompletionReceipt(t *testing.T) {
	t.Run("helper digest mismatch", func(t *testing.T) {
		fixture := newAdoptionFixture(t, migration.GuestIdentitySafeClone, 0x71)
		fixture.request.Helper.SHA256 = migration.Digest("sha256:" + strings.Repeat("f", 64))
		writeAdoptionRequestFixture(t, fixture.requestPath, fixture.request)
		before := guestIdentityFilesDigest(t, fixture.rootPath)
		err := fixture.runner(bytes.NewReader(bytes.Repeat([]byte{0x72}, 32))).run()
		if err == nil || err.Error() != "migration.adoption.helper_mismatch" {
			t.Fatalf("helper mismatch error=%v", err)
		}
		receipt, readErr := readAdoptionReceipt(fixture.receiptPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if receipt.Status != migration.AdoptionReceiptStatusFailed ||
			receipt.CompletionMarker || receipt.FailureCode != "migration.adoption.helper_mismatch" ||
			fixture.shutdowns != 0 {
			t.Fatalf("helper mismatch receipt=%+v shutdowns=%d", receipt, fixture.shutdowns)
		}
		if after := guestIdentityFilesDigest(t, fixture.rootPath); after != before {
			t.Fatal("helper mismatch mutated guest identity")
		}
	})

	t.Run("SSH generation failure", func(t *testing.T) {
		fixture := newAdoptionFixture(t, migration.GuestIdentitySafeClone, 0x73)
		runner := fixture.runner(bytes.NewReader(bytes.Repeat([]byte{0x74}, 32)))
		runner.generateSSHHostKeys = func(string) error { return errors.New("fixture failure") }
		err := runner.run()
		if err == nil || err.Error() != "migration.adoption.ssh_host_key_reset_failed" {
			t.Fatalf("SSH failure error=%v", err)
		}
		receipt, readErr := readAdoptionReceipt(fixture.receiptPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if receipt.Status != migration.AdoptionReceiptStatusFailed ||
			receipt.CompletionMarker || len(receipt.ActionResults) != 2 ||
			receipt.ActionResults[0].Status != migration.AdoptionActionStatusCompleted ||
			receipt.ActionResults[1].Status != migration.AdoptionActionStatusFailed ||
			fixture.shutdowns != 0 {
			t.Fatalf("SSH failure receipt=%+v shutdowns=%d", receipt, fixture.shutdowns)
		}
	})

	t.Run("destination authorized keys symlink", func(t *testing.T) {
		fixture := newAdoptionFixture(t, migration.GuestIdentityExactRestore, 0x74)
		path := filepath.Join(
			fixture.rootPath, "home", "developer", ".ssh", "authorized_keys",
		)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../../../etc/passwd", path); err != nil {
			t.Fatal(err)
		}
		err := fixture.runner(errorReader{errors.New("randomness must not be read")}).run()
		if err == nil || err.Error() != "migration.adoption.destination_ssh_install_failed" {
			t.Fatalf("destination SSH install error=%v", err)
		}
		receipt, readErr := readAdoptionReceipt(fixture.receiptPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if receipt.Status != migration.AdoptionReceiptStatusFailed ||
			receipt.CompletionMarker || len(receipt.ActionResults) != 2 ||
			receipt.ActionResults[0].Action != migration.AdoptionActionPreserveIdentity ||
			receipt.ActionResults[0].Status != migration.AdoptionActionStatusCompleted ||
			receipt.ActionResults[1].Action != migration.AdoptionActionInstallSSHKeys ||
			receipt.ActionResults[1].Status != migration.AdoptionActionStatusFailed ||
			fixture.shutdowns != 0 {
			t.Fatalf("destination SSH failure receipt=%+v shutdowns=%d", receipt, fixture.shutdowns)
		}
	})

	t.Run("destination authorized keys hard link", func(t *testing.T) {
		fixture := newAdoptionFixture(t, migration.GuestIdentityExactRestore, 0x75)
		path := filepath.Join(
			fixture.rootPath, "home", "developer", ".ssh", "authorized_keys",
		)
		existing, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		existing = append(existing, fixture.request.DestinationSSHKeys[0]...)
		existing = append(existing, '\n')
		if err := os.WriteFile(path, existing, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		alias := filepath.Join(fixture.rootPath, "etc", "authorized-keys-alias")
		if err := os.Link(path, alias); err != nil {
			t.Fatal(err)
		}
		err = fixture.runner(errorReader{errors.New("randomness must not be read")}).run()
		if err == nil || err.Error() != "migration.adoption.destination_ssh_install_failed" {
			t.Fatalf("destination SSH hard-link error=%v", err)
		}
		info, statErr := os.Stat(alias)
		if statErr != nil || info.Mode().Perm() != 0o644 {
			t.Fatalf("hard-linked alias protection changed: info=%v err=%v", info, statErr)
		}
		observed, readErr := os.ReadFile(alias)
		if readErr != nil || !bytes.Equal(observed, existing) {
			t.Fatalf("hard-linked alias content changed: err=%v", readErr)
		}
	})

	t.Run("changed cloud-init identity policy", func(t *testing.T) {
		fixture := newAdoptionFixture(t, migration.GuestIdentityExactRestore, 0x76)
		path := filepath.Join(
			fixture.rootPath, "etc", "cloud", "cloud.cfg.d",
			"99-hideout-migration-identity.cfg",
		)
		if err := os.WriteFile(path, []byte("ssh_deletekeys: true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := fixture.runner(errorReader{errors.New("randomness must not be read")}).run()
		if err == nil || err.Error() != "migration.adoption.destination_ssh_install_failed" {
			t.Fatalf("changed cloud-init policy error=%v", err)
		}
		receipt, readErr := readAdoptionReceipt(fixture.receiptPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if receipt.Status != migration.AdoptionReceiptStatusFailed ||
			receipt.CompletionMarker || len(receipt.ActionResults) != 2 ||
			receipt.ActionResults[1].Action != migration.AdoptionActionInstallSSHKeys ||
			receipt.ActionResults[1].Status != migration.AdoptionActionStatusFailed ||
			fixture.shutdowns != 0 {
			t.Fatalf("changed cloud-init policy receipt=%+v shutdowns=%d", receipt, fixture.shutdowns)
		}
	})
}

func TestSafeCloneRejectsUnrecognizedSSHHostIdentityMaterial(t *testing.T) {
	fixture := newAdoptionFixture(t, migration.GuestIdentitySafeClone, 0x75)
	unknown := filepath.Join(
		fixture.rootPath,
		"etc",
		"ssh",
		"ssh_host_ed25519_key-cert.pub",
	)
	if err := os.WriteFile(unknown, []byte("stale host certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := fixture.runner(bytes.NewReader(bytes.Repeat([]byte{0x76}, 32)))
	keygenCalls := 0
	runner.generateSSHHostKeys = func(string) error {
		keygenCalls++
		return nil
	}
	err := runner.run()
	if err == nil || err.Error() != "migration.adoption.ssh_host_key_reset_failed" {
		t.Fatalf("unknown SSH identity error=%v", err)
	}
	if keygenCalls != 0 || fixture.shutdowns != 0 {
		t.Fatalf("keygenCalls=%d shutdowns=%d", keygenCalls, fixture.shutdowns)
	}
	if _, statErr := os.Stat(filepath.Join(
		fixture.rootPath,
		"etc",
		"ssh",
		"ssh_host_ed25519_key",
	)); statErr != nil {
		t.Fatalf("known host key was removed before full preflight: %v", statErr)
	}
}

func TestSafeCloneRejectsMachineIdentityRandomnessCollision(t *testing.T) {
	fixture := newAdoptionFixture(t, migration.GuestIdentitySafeClone, 0x77)
	before := guestIdentityFilesDigest(t, fixture.rootPath)
	runner := fixture.runner(bytes.NewReader(bytes.Repeat([]byte{0x77}, 16*8)))
	keygenCalls := 0
	runner.generateSSHHostKeys = func(string) error {
		keygenCalls++
		return nil
	}
	err := runner.run()
	if err == nil || err.Error() != "migration.adoption.machine_id_reset_failed" {
		t.Fatalf("machine identity collision error=%v", err)
	}
	if keygenCalls != 0 || fixture.shutdowns != 0 {
		t.Fatalf("keygenCalls=%d shutdowns=%d", keygenCalls, fixture.shutdowns)
	}
	if after := guestIdentityFilesDigest(t, fixture.rootPath); after != before {
		t.Fatalf("identity collision mutated guest files: before=%s after=%s", before, after)
	}
	receipt, readErr := readAdoptionReceipt(fixture.receiptPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if receipt.Status != migration.AdoptionReceiptStatusFailed ||
		receipt.CompletionMarker || len(receipt.ActionResults) != 1 ||
		receipt.ActionResults[0].Action != migration.AdoptionActionResetMachineID ||
		receipt.ActionResults[0].Status != migration.AdoptionActionStatusFailed {
		t.Fatalf("collision receipt=%+v", receipt)
	}
}

type adoptionFixture struct {
	rootPath       string
	requestPath    string
	receiptPath    string
	selfPath       string
	request        migration.AdoptionRequest
	sourceIdentity migration.GuestIdentityEvidence
	newPublicKey   []byte
	shutdowns      int
}

func newAdoptionFixture(
	t *testing.T,
	policy migration.GuestIdentityPolicy,
	seed byte,
) *adoptionFixture {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "guest")
	for _, directory := range []string{
		filepath.Join(root, "etc", "cloud", "cloud.cfg.d"),
		filepath.Join(root, "etc", "ssh"),
		filepath.Join(root, "home", "developer", ".ssh"),
		filepath.Join(root, "root"),
		filepath.Join(root, "var", "lib", "dbus"),
		filepath.Join(root, "sys", "class", "net", "lo"),
		filepath.Join(base, "request"),
		filepath.Join(base, "receipt"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	machineID := []byte(strings.Repeat(fmt.Sprintf("%x", seed%16), 32) + "\n")
	if err := os.WriteFile(filepath.Join(root, "etc", "machine-id"), machineID, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "var", "lib", "dbus", "machine-id"), machineID, 0o644,
	); err != nil {
		t.Fatal(err)
	}
	oldPublicKey := publicKeyFixture(t, seed)
	if err := os.WriteFile(
		filepath.Join(root, "etc", "passwd"),
		[]byte("root:x:0:0:root:/root:/bin/bash\ndeveloper:x:1000:1000:Developer:/home/developer:/bin/bash\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "home", "developer", ".ssh", "authorized_keys"),
		append(append([]byte(nil), oldPublicKey...), []byte("existing-source-key\n")...),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "etc", "ssh", "ssh_host_ed25519_key.pub"),
		oldPublicKey, 0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "etc", "ssh", "ssh_host_ed25519_key"),
		[]byte("fixture-private-host-key"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	selfPath := filepath.Join(base, "hideout-migration-adopt")
	if err := os.WriteFile(selfPath, []byte("fixture helper bytes"), 0o500); err != nil {
		t.Fatal(err)
	}
	helperDigest, err := adoptionFileDigest(selfPath)
	if err != nil {
		t.Fatal(err)
	}
	sourceIdentity, err := observeGuestIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	actions := []string{
		migration.AdoptionActionPreserveIdentity,
		migration.AdoptionActionInstallSSHKeys,
	}
	if policy == migration.GuestIdentitySafeClone {
		actions = []string{
			migration.AdoptionActionResetMachineID,
			migration.AdoptionActionResetSSHHostKeys,
			migration.AdoptionActionInstallSSHKeys,
		}
	}
	request := migration.AdoptionRequest{
		Schema:      migration.AdoptionRequestSchema,
		OperationID: "op_import1234", EnvironmentRef: "envref_dev1234",
		RequestNonce: "nonce_request1234", ReceiptNonce: "nonce_receipt1234",
		Policy: policy, SourceIdentity: sourceIdentity,
		DestinationSSHUser: "developer",
		DestinationSSHKeys: []string{strings.TrimSpace(string(publicKeyFixture(t, seed+1)))},
		PermittedActions:   actions,
		Helper: migration.HelperBinding{
			PackageID: migration.AdoptionHelperPackage,
			Version:   "0.1.0-alpha.4", SHA256: helperDigest,
		},
	}
	requestPath := filepath.Join(base, "request", "request.json")
	writeAdoptionRequestFixture(t, requestPath, request)
	return &adoptionFixture{
		rootPath: root, requestPath: requestPath,
		receiptPath: filepath.Join(base, "receipt", "receipt.json"),
		selfPath:    selfPath, request: request, sourceIdentity: sourceIdentity,
	}
}

func (fixture *adoptionFixture) runner(random io.Reader) adoptionRunner {
	return adoptionRunner{
		rootPath: fixture.rootPath, requestPath: fixture.requestPath,
		receiptPath: fixture.receiptPath, selfPath: fixture.selfPath,
		networkClassPath: filepath.Join(fixture.rootPath, "sys", "class", "net"),
		random:           random,
		generateSSHHostKeys: func(root string) error {
			if root != fixture.rootPath {
				return errors.New("unexpected fixture root")
			}
			if len(fixture.newPublicKey) == 0 {
				return errors.New("new fixture public key is absent")
			}
			if err := os.WriteFile(
				filepath.Join(root, "etc", "ssh", "ssh_host_ed25519_key.pub"),
				fixture.newPublicKey, 0o644,
			); err != nil {
				return err
			}
			return os.WriteFile(
				filepath.Join(root, "etc", "ssh", "ssh_host_ed25519_key"),
				[]byte("fresh-fixture-private-host-key"), 0o600,
			)
		},
		fileOwnership: func(*os.File, int, int) error { return nil },
		shutdown: func() error {
			fixture.shutdowns++
			return nil
		},
	}
}

func runSafeCloneFixture(
	t *testing.T,
	fixture *adoptionFixture,
	machineSeed, keySeed byte,
) migration.AdoptionReceipt {
	t.Helper()
	fixture.newPublicKey = publicKeyFixture(t, keySeed)
	if err := fixture.runner(
		bytes.NewReader(bytes.Repeat([]byte{machineSeed}, 16)),
	).run(); err != nil {
		t.Fatal(err)
	}
	receipt, err := readAdoptionReceipt(fixture.receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func assertDestinationSSHKeyInstalled(t *testing.T, fixture *adoptionFixture) {
	t.Helper()
	want := fixture.request.DestinationSSHKeys[0]
	for _, path := range []string{
		filepath.Join(fixture.rootPath, "home", "developer", ".ssh", "authorized_keys"),
		filepath.Join(fixture.rootPath, "root", ".ssh", "authorized_keys"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want+"\n") {
			t.Fatalf("destination control key is absent from %s", path)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("destination authorized_keys protection changed: path=%s info=%v err=%v", path, info, err)
		}
	}
	target, err := os.ReadFile(filepath.Join(
		fixture.rootPath, "home", "developer", ".ssh", "authorized_keys",
	))
	if err != nil || !bytes.Contains(target, []byte("existing-source-key\n")) {
		t.Fatalf("existing guest authorized_keys content was not preserved: err=%v", err)
	}
	policyPath := filepath.Join(
		fixture.rootPath, "etc", "cloud", "cloud.cfg.d",
		"99-hideout-migration-identity.cfg",
	)
	policy, err := os.ReadFile(policyPath)
	if err != nil || string(policy) != migrationCloudInitSSHPolicy {
		t.Fatalf("migration cloud-init identity policy changed: data=%q err=%v", policy, err)
	}
	info, err := os.Stat(policyPath)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("migration cloud-init identity policy protection changed: info=%v err=%v", info, err)
	}
}

func writeAdoptionRequestFixture(
	t *testing.T,
	path string,
	request migration.AdoptionRequest,
) {
	t.Helper()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	writeReadOnlyFixture(t, path, data)
}

func writeIdentityObservationRequestFixture(
	t *testing.T,
	path string,
	request migration.IdentityObservationRequest,
) {
	t.Helper()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	writeReadOnlyFixture(t, path, data)
}

func writeReadOnlyFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
}

func publicKeyFixture(t *testing.T, seed byte) []byte {
	t.Helper()
	seedBytes := bytes.Repeat([]byte{seed}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seedBytes)
	publicKey, err := ssh.NewPublicKey(privateKey.Public())
	clear(seedBytes)
	clear(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return ssh.MarshalAuthorizedKey(publicKey)
}

func guestIdentityFilesDigest(t *testing.T, root string) string {
	t.Helper()
	hash := sha256.New()
	for _, relative := range []string{
		filepath.Join("etc", "machine-id"),
		filepath.Join("var", "lib", "dbus", "machine-id"),
		filepath.Join("etc", "ssh", "ssh_host_ed25519_key"),
		filepath.Join("etc", "ssh", "ssh_host_ed25519_key.pub"),
	} {
		data, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = hash.Write([]byte(relative))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		clear(data)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func fileDigestFixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	clear(data)
	return fmt.Sprintf("%x", digest[:])
}

type errorReader struct {
	err error
}

func (reader errorReader) Read([]byte) (int, error) {
	return 0, reader.err
}
