package releasechannel

import (
	"testing"
	"time"
)

func TestSigningAndNotarizationObservations(t *testing.T) {
	signing := SigningObservation{
		Schema: SigningObservationSchema, Status: "developer-id-verified", TeamID: "TEAM",
		CommonName: "Developer ID Application: Test (TEAM)", ObservedAt: time.Now(), HostOS: "darwin",
		PackageManifestSHA256: testDigest,
		Binaries:              []BinarySignature{{Path: "bin/hideout", Identifier: "hideout", CDHash: "ABC", SecureTimestamp: true, HardenedRuntime: true, StrictVerified: true, OnlineNotarizationValid: true}},
	}
	if err := signing.Validate(true); err != nil {
		t.Fatal(err)
	}
	notary := NotarizationObservation{Schema: NotarizationObservationSchema, Status: "accepted", SubmissionID: "uuid", SubmissionSHA256: testDigest, PackageManifestSHA256: testDigest, ObservedAt: time.Now(), TicketMode: "online", StapleStatus: "not-applicable-tar-gz"}
	if err := notary.Validate(true); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*SigningObservation){
		func(o *SigningObservation) { o.Status = "developer-preview-unsigned" },
		func(o *SigningObservation) { o.Binaries[0].HardenedRuntime = false },
		func(o *SigningObservation) { o.Binaries[0].OnlineNotarizationValid = false },
		func(o *SigningObservation) { o.CommonName = "Apple Development: Test" },
	} {
		copy := signing
		copy.Binaries = append([]BinarySignature(nil), signing.Binaries...)
		mutate(&copy)
		if err := copy.Validate(true); err == nil {
			t.Fatal("invalid public signing observation passed")
		}
	}
}

func TestDeveloperPreviewCannotSatisfyPublicSigning(t *testing.T) {
	preview := SigningObservation{Schema: SigningObservationSchema, Status: "developer-preview-unsigned", ObservedAt: time.Now(), HostOS: "darwin"}
	if err := preview.Validate(false); err != nil {
		t.Fatal(err)
	}
	if err := preview.Validate(true); err == nil {
		t.Fatal("unsigned preview passed public validation")
	}
}

func TestCodeDirectoryHardenedRuntimeParsing(t *testing.T) {
	valid := "CodeDirectory v=20500 size=241 flags=0x10000(runtime) hashes=2+2 location=embedded\n"
	if !codeDirectoryHasHardenedRuntime(valid) {
		t.Fatal("runtime flag on CodeDirectory line was not detected")
	}
	for _, invalid := range []string{
		"flags=0x10000(runtime)\n",
		"CodeDirectory v=20500 size=241 flags=0x0(none) hashes=2+2 location=embedded\n",
		"CodeDirectory v=20500 size=241 hashes=2+2 location=embedded\n",
	} {
		if codeDirectoryHasHardenedRuntime(invalid) {
			t.Fatalf("invalid CodeDirectory output passed: %q", invalid)
		}
	}
}
