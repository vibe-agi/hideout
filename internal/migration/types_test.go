package migration

import (
	"errors"
	"strings"
	"testing"
)

func TestDefaultLimitsMatchPublishedV1Envelope(t *testing.T) {
	limits := DefaultLimits()
	if limits.MaxEnvironments != 32 ||
		limits.MaxLogicalBytes != 4<<40 ||
		limits.MaxPayloadRecords != 1<<20 ||
		limits.MaxChunkBytes != 4<<20 ||
		limits.MaxManifestBytes != 16<<20 ||
		limits.MaxMetadataBytes != 1<<20 ||
		limits.MaxWorkingBytes != 256<<20 {
		t.Fatalf("v1 limits drifted: %+v", limits)
	}
	if err := limits.Validate(); err != nil {
		t.Fatalf("default limits: %v", err)
	}

	oversized := limits
	oversized.MaxChunkBytes++
	if !errors.Is(oversized.Validate(), ErrLimitExceeded) {
		t.Fatalf("oversized chunk limit error=%v", oversized.Validate())
	}
	zero := limits
	zero.MaxEnvironments = 0
	if !errors.Is(zero.Validate(), ErrInvalidBundle) {
		t.Fatalf("zero environment limit error=%v", zero.Validate())
	}
}

func TestTypedIdentifiersAndRecordHeadersFailClosed(t *testing.T) {
	if _, err := ParseBundleID("migb_fixture1234"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "source host", "migb_short", "../../bundle"} {
		if _, err := ParseBundleID(value); err == nil {
			t.Fatalf("accepted bundle ID %q", value)
		}
	}
	if _, err := ParseOpaqueID("component_disk1234"); err != nil {
		t.Fatal(err)
	}

	header := PrivateRecordHeader{
		Version:         RecordPrivateVersion,
		Type:            RecordDataChunk,
		ComponentID:     "component_disk1234",
		Ordinal:         2,
		LogicalOffset:   8 << 20,
		PlaintextLength: 4 << 20,
		EncodedLength:   1 << 20,
		PlaintextDigest: digestForTest("a"),
	}
	if err := header.Validate(DefaultLimits()); err != nil {
		t.Fatalf("valid data record: %v", err)
	}
	header.EncodedLength = 5 << 20
	if !errors.Is(header.Validate(DefaultLimits()), ErrLimitExceeded) {
		t.Fatalf("oversized encoded record error=%v", header.Validate(DefaultLimits()))
	}
	sparse := PrivateRecordHeader{
		Version: RecordPrivateVersion, Type: RecordHoleExtent,
		ComponentID: "component_disk1234", PlaintextLength: 64 << 20,
		PlaintextDigest: digestForTest("b"),
	}
	if err := sparse.Validate(DefaultLimits()); err != nil {
		t.Fatalf("coalesced sparse extent inherited the data chunk bound: %v", err)
	}
}

func TestMigrationErrorProjectsOnlyStableRedactedFacts(t *testing.T) {
	err := &Error{
		Code:             CodeAuthenticationFailed,
		Sequence:         7,
		ComponentID:      "component_disk1234",
		Retryable:        true,
		RecoveryRequired: true,
		Cause:            errors.New("socks5://user:password@127.0.0.1:7890"),
	}
	message := err.Error()
	if CodeOf(err) != CodeAuthenticationFailed ||
		!strings.Contains(message, CodeAuthenticationFailed) ||
		!strings.Contains(message, "sequence=7") ||
		strings.Contains(message, "password") ||
		strings.Contains(message, "127.0.0.1") {
		t.Fatalf("migration error projection leaked or drifted: %q", message)
	}
	if !errors.Is(err, err.Cause) {
		t.Fatal("migration error did not retain its privileged internal cause")
	}

	hostile := (&Error{
		Code:        "socks5://user:password@127.0.0.1:7890",
		ComponentID: "socks5://user:password@127.0.0.1:7890",
	}).Error()
	if hostile != CodeInvalidBundle {
		t.Fatalf("untrusted error projection leaked: %q", hostile)
	}

	unsupported := PrivateRecordHeader{
		Version: RecordPrivateVersion, Type: RecordType(255),
	}
	if err := unsupported.Validate(DefaultLimits()); !errors.Is(err, ErrUnsupportedRecord) || CodeOf(err) != CodeUnsupportedRecord {
		t.Fatalf("unsupported record error=%v code=%q", err, CodeOf(err))
	}
}

func TestIdentityObservationRequestReceiptBindingFailsClosed(t *testing.T) {
	request := IdentityObservationRequest{
		Schema:      IdentityObservationRequestSchema,
		OperationID: "operation_export1234", EnvironmentRef: "environment_alpha1",
		RequestNonce: "nonce_request1234", ReceiptNonce: "nonce_receipt1234",
		Helper: HelperBinding{
			PackageID: AdoptionHelperPackage, Version: "1.0.0",
			SHA256: digestForTest("a"),
		},
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	identity := GuestIdentityEvidence{
		MachineIDDigest:   digestForTest("b"),
		SSHHostKeyDigests: []Digest{digestForTest("c")},
	}
	receipt := IdentityObservationReceipt{
		Schema:      IdentityObservationReceiptSchema,
		OperationID: request.OperationID, EnvironmentRef: request.EnvironmentRef,
		RequestNonce: request.RequestNonce, ReceiptNonce: request.ReceiptNonce,
		Helper: request.Helper, Identity: &identity,
		Status: AdoptionReceiptStatusCompleted, CompletionMarker: true,
	}
	if err := receipt.MatchesRequest(request); err != nil {
		t.Fatal(err)
	}

	substituted := receipt
	substituted.ReceiptNonce = "nonce_attacker1234"
	if err := substituted.MatchesRequest(request); err == nil {
		t.Fatal("accepted an identity observation receipt nonce substitution")
	}
	failed := receipt
	failed.Identity = nil
	failed.Status = AdoptionReceiptStatusFailed
	failed.CompletionMarker = false
	failed.FailureCode = "migration.identity_observation.network_present"
	if err := failed.MatchesRequest(request); err != nil {
		t.Fatal(err)
	}
	failed.FailureCode = "socks5://user:password@127.0.0.1:7890"
	if err := failed.Validate(); err == nil {
		t.Fatal("accepted a hostile identity observation failure code")
	}
}

func digestForTest(character string) Digest {
	value := ""
	for len(value) < 64 {
		value += character
	}
	return Digest("sha256:" + value[:64])
}
