package releasecompat

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/productevidence"
	"github.com/vibe-agi/hideout/internal/recovery"
)

func TestLocalFastReadinessIsNotReleaseEvidence(t *testing.T) {
	ready, err := BuildReadiness(ReadinessOptions{
		Mode:        "local-fast",
		Commit:      "abc123",
		LocalPassed: true,
		Now:         time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	if err := ValidateReadiness(ready); err != nil {
		t.Fatalf("validate readiness: %v", err)
	}
	if ready.ReleaseReady {
		t.Fatal("local-fast claimed releaseReady")
	}
	if ready.Status != "not-release" || ready.EvidenceClass != "local-fast" {
		t.Fatalf("unexpected local-fast status: %+v", ready)
	}
	for _, gate := range ready.Gates {
		if gate.Status != "not-run" {
			t.Fatalf("local-fast gate should be not-run: %+v", gate)
		}
	}
}

func TestReleaseCandidateMissingEvidenceFailsClosed(t *testing.T) {
	ready, err := BuildReadiness(ReadinessOptions{Mode: "release-candidate", LocalPassed: true})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	if ready.ReleaseReady {
		t.Fatal("candidate without evidence claimed releaseReady")
	}
	if ready.Status != "failed" {
		t.Fatalf("status=%s", ready.Status)
	}
	for _, gate := range ready.Gates {
		if gate.Status != "missing" {
			t.Fatalf("gate should be missing: %+v", gate)
		}
		if gate.Code != recovery.CodeReleaseGateEvidenceMissing {
			t.Fatalf("missing gate should include recovery code: %+v", gate)
		}
		if gate.Reason == "" || gate.Hint == "" || len(gate.NextActions) == 0 || len(gate.EvidenceRefs) == 0 {
			t.Fatalf("missing gate should include full recovery fields: %+v", gate)
		}
	}
}

func TestReleaseCandidateRejectsMinimalFabricatedGateJSON(t *testing.T) {
	dir := t.TempDir()
	gate2 := filepath.Join(dir, "gate2.json")
	gate3 := filepath.Join(dir, "gate3.json")
	if err := os.WriteFile(gate2, []byte(`{"id":"gate2-lima","backend":"lima","result":"passed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate3, []byte(`{"id":"gate3-hidden-proxy","backend":"lima","result":"passed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ready, err := BuildReadiness(ReadinessOptions{Mode: "release-candidate", LocalPassed: true, Gate2Evidence: gate2, Gate3Evidence: gate3})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	if err := ValidateReadiness(ready); err != nil {
		t.Fatalf("validate readiness: %v", err)
	}
	if ready.ReleaseReady || ready.Status != "failed" {
		t.Fatalf("minimal self-authored gate JSON claimed readiness: %+v", ready)
	}
	for _, gate := range ready.Gates {
		if gate.Status != "failed" || !strings.Contains(gate.Summary, "trusted runtime expectation") {
			t.Fatalf("minimal gate should fail the runtime trust anchor: %+v", gate)
		}
	}
}

func TestReadinessReportsSupportingProductHardeningEvidence(t *testing.T) {
	dir := t.TempDir()
	evidence := filepath.Join(dir, "product-hardening.json")
	writeCompleteProductEvidence(t, evidence, "abc123")
	ready, err := BuildReadiness(ReadinessOptions{
		Mode:            "local-fast",
		Commit:          "abc123",
		LocalPassed:     true,
		ProductEvidence: []string{evidence},
		Now:             time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	var found bool
	for _, command := range ready.Commands {
		if command.Name == "product-hardening-evidence" {
			found = true
			if command.Status != "passed" {
				t.Fatalf("product hardening command=%+v", command)
			}
		}
	}
	if !found {
		t.Fatalf("missing product-hardening command: %+v", ready.Commands)
	}
	if ready.ReleaseReady {
		t.Fatal("supporting product-hardening evidence must not make local-fast release-ready")
	}
}

func TestReadinessTargetedProductHardeningDoesNotRequireLocalDogfoodProofs(t *testing.T) {
	dir := t.TempDir()
	evidence := filepath.Join(dir, "targeted-product-hardening.json")
	var targeted []productevidence.ProofRequirement
	for _, req := range productevidence.ProductHardeningRequirements() {
		if req.RequiredFor == productevidence.RequiredForTargetedCompletion {
			targeted = append(targeted, req)
		}
	}
	writeProductEvidence(t, evidence, "abc123", targeted)
	ready, err := BuildReadiness(ReadinessOptions{
		Mode:            "local-fast",
		Commit:          "abc123",
		LocalPassed:     true,
		ProductEvidence: []string{evidence},
		Now:             time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	for _, command := range ready.Commands {
		if command.Name == "product-hardening-evidence" {
			if command.Status != "passed" {
				t.Fatalf("targeted product hardening should not require local-dogfood proofs: %+v", command)
			}
			return
		}
	}
	t.Fatalf("missing product-hardening command: %+v", ready.Commands)
}

func TestReadinessReportsStaleProductHardeningEvidence(t *testing.T) {
	dir := t.TempDir()
	evidence := filepath.Join(dir, "product-hardening.json")
	writeCompleteProductEvidence(t, evidence, "old")
	ready, err := BuildReadiness(ReadinessOptions{
		Mode:            "local-fast",
		Commit:          "new",
		LocalPassed:     true,
		ProductEvidence: []string{evidence},
		Now:             time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	for _, command := range ready.Commands {
		if command.Name == "product-hardening-evidence" {
			if command.Status != "failed" || !strings.Contains(command.Summary, "stale") {
				t.Fatalf("stale product hardening command=%+v", command)
			}
			if command.Code != recovery.CodeReleaseEvidenceStale {
				t.Fatalf("stale product hardening command should include recovery code: %+v", command)
			}
			if command.Reason == "" || command.Hint == "" || len(command.NextActions) == 0 || len(command.EvidenceRefs) == 0 {
				t.Fatalf("stale product hardening command missing recovery fields: %+v", command)
			}
			return
		}
	}
	t.Fatalf("missing product-hardening command: %+v", ready.Commands)
}

func TestReleaseCandidateRejectsEmptyOrWrongGateEvidence(t *testing.T) {
	dir := t.TempDir()
	gate2 := filepath.Join(dir, "gate2.json")
	gate3 := filepath.Join(dir, "gate3.json")
	if err := os.WriteFile(gate2, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate3, []byte(`{"id":"gate2-lima","backend":"lima","result":"passed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ready, err := BuildReadiness(ReadinessOptions{Mode: "release-candidate", LocalPassed: true, Gate2Evidence: gate2, Gate3Evidence: gate3})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	if ready.ReleaseReady {
		t.Fatal("candidate with empty/wrong evidence claimed releaseReady")
	}
	if ready.Gates[0].Status != "failed" || ready.Gates[1].Status != "failed" {
		t.Fatalf("expected failed gates, got %+v", ready.Gates)
	}
}

func TestReleaseCandidateRejectsNativeGateEvidence(t *testing.T) {
	dir := t.TempDir()
	gate2 := filepath.Join(dir, "gate2.json")
	gate3 := filepath.Join(dir, "gate3.json")
	if err := os.WriteFile(gate2, []byte(`{"id":"gate2-lima","backend":"native","result":"passed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate3, []byte(`{"id":"gate3-hidden-proxy","backend":"lima","result":"passed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ready, err := BuildReadiness(ReadinessOptions{Mode: "release-candidate", LocalPassed: true, Gate2Evidence: gate2, Gate3Evidence: gate3})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	if ready.ReleaseReady || ready.Gates[0].Status != "failed" {
		t.Fatalf("native evidence accepted: %+v", ready)
	}
}

func TestReleaseCandidateRejectsUnboundReleaseDogfoodManifest(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "manifest.json")
	data := []byte(`{
  "schema": "hideout.release-dogfood.v1",
  "status": "passed",
  "isolationGates": [
    {"id":"gate2-lima","backend":"lima","result":"passed"},
    {"id":"gate3-hidden-proxy","backend":"lima","result":"passed"}
  ]
}`)
	if err := os.WriteFile(manifest, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ready, err := BuildReadiness(ReadinessOptions{Mode: "release-candidate", LocalPassed: true, Gate2Evidence: manifest, Gate3Evidence: manifest})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	if ready.ReleaseReady {
		t.Fatalf("unbound dogfood manifest claimed readiness: %+v", ready)
	}
}

func TestReleaseCandidateRuntimeEvidenceRequiresExactCleanCrossGateBinding(t *testing.T) {
	expected := runtimeExpectationFixture()
	packageIdentity := packageIdentityFixture()
	if packageIdentity.SourceCommit == expected.BuildCommit {
		t.Fatal("test fixture must keep package candidate and runtime image build commits distinct")
	}
	baseBinding := runtimeBindingFixture()
	cases := []struct {
		name        string
		mutateGate2 func(*productevidence.RuntimeBinding)
		mutateGate3 func(*productevidence.RuntimeBinding)
		mutateProof func(*productevidence.RuntimeBinding)
		product     bool
		removePrior bool
		wantReady   bool
	}{
		{name: "exact independent environments", product: true, wantReady: true},
		{name: "missing product evidence"},
		{name: "dirty gate", product: true, mutateGate2: func(b *productevidence.RuntimeBinding) { b.BuildDirty = true }},
		{name: "wrong digest", product: true, mutateGate3: func(b *productevidence.RuntimeBinding) { b.ArtifactSHA256 = strings.Repeat("b", 64) }},
		{name: "reused gate environment", product: true, mutateGate3: func(b *productevidence.RuntimeBinding) { b.EnvironmentID = baseBinding.EnvironmentID }},
		{name: "empty gate environment", product: true, mutateGate2: func(b *productevidence.RuntimeBinding) { b.EnvironmentID = "" }},
		{name: "invalid gate environment", product: true, mutateGate3: func(b *productevidence.RuntimeBinding) { b.EnvironmentID = "env_INVALID" }},
		{name: "product binding mismatch", product: true, mutateProof: func(b *productevidence.RuntimeBinding) { b.Revision = "other" }},
		{name: "missing prior release proof", product: true, removePrior: true},
		{name: "missing trusted package", product: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			gate2Binding, gate3Binding, proofBinding := baseBinding, baseBinding, baseBinding
			gate3Binding.EnvironmentID = "env_20260711t000000z1111111111111111111"
			if tc.mutateGate2 != nil {
				tc.mutateGate2(&gate2Binding)
			}
			if tc.mutateGate3 != nil {
				tc.mutateGate3(&gate3Binding)
			}
			if tc.mutateProof != nil {
				tc.mutateProof(&proofBinding)
			}
			gate2 := filepath.Join(dir, "gate2.json")
			gate3 := filepath.Join(dir, "gate3.json")
			writeRuntimeGateEvidence(t, gate2, "gate2-lima", "lima", gate2Binding)
			writeRuntimeGateEvidence(t, gate3, "gate3-hidden-proxy", "lima", gate3Binding)
			var productPaths []string
			if tc.product {
				product := filepath.Join(dir, "runtime-product.json")
				writeRuntimeProductEvidence(t, product, packageIdentityFixture().SourceCommit, proofBinding)
				if tc.removePrior {
					manifest, err := productevidence.ReadFile(product)
					if err != nil {
						t.Fatal(err)
					}
					for i, proof := range manifest.Proofs {
						if proof.FeatureID != productevidence.Feature031 {
							manifest.Proofs = append(manifest.Proofs[:i], manifest.Proofs[i+1:]...)
							break
						}
					}
					if err := productevidence.WriteFile(product, manifest); err != nil {
						t.Fatal(err)
					}
				}
				productPaths = []string{product}
			}
			var trustedPackage *productevidence.PackageIdentity
			if tc.name != "missing trusted package" {
				copy := packageIdentity
				trustedPackage = &copy
			}
			ready, err := BuildReadiness(ReadinessOptions{
				Mode: "release-candidate", Commit: "caller-controlled", LocalPassed: true,
				Gate2Evidence: gate2, Gate3Evidence: gate3, ProductEvidence: productPaths, Runtime: &expected, Package: trustedPackage,
				SigningObservationSHA256: strings.Repeat("d", 64), NotarizationObservationSHA256: strings.Repeat("e", 64),
			})
			if err != nil {
				t.Fatal(err)
			}
			if ready.ReleaseReady != tc.wantReady {
				t.Fatalf("releaseReady=%v want %v readiness=%+v", ready.ReleaseReady, tc.wantReady, ready)
			}
			if tc.wantReady && ready.SourceCommit != packageIdentity.SourceCommit {
				t.Fatalf("readiness sourceCommit=%q want verified package identity %q", ready.SourceCommit, packageIdentity.SourceCommit)
			}
		})
	}
}

func TestReleaseCandidateRejectsMissing034PerformanceProof(t *testing.T) {
	dir := t.TempDir()
	binding := runtimeBindingFixture()
	gate2Binding := binding
	gate3Binding := binding
	gate3Binding.EnvironmentID = "env_20260711t000000z1111111111111111111"
	gate2 := filepath.Join(dir, "gate2.json")
	gate3 := filepath.Join(dir, "gate3.json")
	product := filepath.Join(dir, "runtime-product.json")
	writeRuntimeGateEvidence(t, gate2, "gate2-lima", "lima", gate2Binding)
	writeRuntimeGateEvidence(t, gate3, "gate3-hidden-proxy", "lima", gate3Binding)
	writeRuntimeProductEvidence(t, product, packageIdentityFixture().SourceCommit, binding)

	manifest, err := productevidence.ReadFile(product)
	if err != nil {
		t.Fatal(err)
	}
	for i, proof := range manifest.Proofs {
		if proof.ProofID == productevidence.Proof034RealPerformance {
			manifest.Proofs = append(manifest.Proofs[:i], manifest.Proofs[i+1:]...)
			break
		}
	}
	if err := productevidence.WriteFile(product, manifest); err != nil {
		t.Fatal(err)
	}
	expected := runtimeExpectationFixture()
	trustedPackage := packageIdentityFixture()
	ready, err := BuildReadiness(ReadinessOptions{
		Mode: "release-candidate", Commit: "caller-controlled", LocalPassed: true,
		Gate2Evidence: gate2, Gate3Evidence: gate3, ProductEvidence: []string{product},
		Runtime: &expected, Package: &trustedPackage,
		SigningObservationSHA256:      strings.Repeat("d", 64),
		NotarizationObservationSHA256: strings.Repeat("e", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ready.ReleaseReady {
		t.Fatal("release candidate passed without the 034 performance proof")
	}
	for _, command := range ready.Commands {
		if command.Name == "product-hardening-evidence" {
			if command.Status != "failed" || !strings.Contains(command.Summary, productevidence.Proof034RealPerformance) {
				t.Fatalf("missing 034 proof was not surfaced: %+v", command)
			}
			return
		}
	}
	t.Fatal("readiness omitted product-hardening-evidence result")
}

func TestRuntimeReadinessRejectsAbsentStaleNativeAndFailedEvidence(t *testing.T) {
	expected := runtimeExpectationFixture()
	packageIdentity := packageIdentityFixture()
	binding := runtimeBindingFixture()
	cases := []struct {
		name    string
		absent  bool
		native  bool
		stale   bool
		failed  bool
		backend string
	}{
		{name: "absent gate", absent: true},
		{name: "native gate", native: true},
		{name: "stale product", stale: true},
		{name: "failed product", failed: true},
		{name: "unsupported backend", backend: "qemu"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			gate2 := filepath.Join(dir, "gate2.json")
			gate3 := filepath.Join(dir, "gate3.json")
			backend := tc.backend
			if backend == "" {
				backend = "lima"
			}
			if tc.native {
				backend = "native"
			}
			writeRuntimeGateEvidence(t, gate2, "gate2-lima", backend, binding)
			writeRuntimeGateEvidence(t, gate3, "gate3-hidden-proxy", "lima", binding)
			if tc.absent {
				gate2 = ""
			}
			product := filepath.Join(dir, "runtime-product.json")
			writeRuntimeProductEvidence(t, product, packageIdentityFixture().SourceCommit, binding)
			if tc.stale || tc.failed {
				manifest, err := productevidence.ReadFile(product)
				if err != nil {
					t.Fatal(err)
				}
				if tc.stale {
					manifest.Commit = "stale"
				}
				if tc.failed {
					manifest.Proofs[0].Status = productevidence.StatusFailed
				}
				if err := productevidence.WriteFile(product, manifest); err != nil {
					t.Fatal(err)
				}
			}
			ready, err := BuildReadiness(ReadinessOptions{
				Mode: "release-candidate", Commit: "caller-controlled", LocalPassed: true,
				Gate2Evidence: gate2, Gate3Evidence: gate3, ProductEvidence: []string{product}, Runtime: &expected, Package: &packageIdentity,
			})
			if err != nil {
				t.Fatal(err)
			}
			if ready.ReleaseReady {
				t.Fatalf("%s evidence produced runtime/release readiness: %+v", tc.name, ready)
			}
		})
	}
}

func TestReleaseCandidateRequiresEveryReleaseProofPackageAndArtifactDigest(t *testing.T) {
	expected := runtimeExpectationFixture()
	binding := runtimeBindingFixture()
	packageIdentity := packageIdentityFixture()
	cases := []struct {
		name   string
		mutate func(string, *productevidence.Manifest)
	}{
		{name: "missing registered proof", mutate: func(_ string, m *productevidence.Manifest) { m.Proofs = m.Proofs[:len(m.Proofs)-1] }},
		{name: "missing artifact digest", mutate: func(_ string, m *productevidence.Manifest) {
			m.Proofs[firstFeatureProof(m, productevidence.Feature031)].Artifacts[0].SHA256 = ""
		}},
		{name: "artifact digest mismatch", mutate: func(root string, m *productevidence.Manifest) {
			proof := m.Proofs[firstFeatureProof(m, productevidence.Feature031)]
			if err := os.WriteFile(filepath.Join(root, proof.Artifacts[0].Path), []byte("tampered\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unrelated package identity", mutate: func(_ string, m *productevidence.Manifest) { m.PackageIdentity.SourceCommit = strings.Repeat("f", 40) }},
		{name: "dirty product manifest", mutate: func(_ string, m *productevidence.Manifest) { m.Dirty = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			gate2 := filepath.Join(dir, "gate2.json")
			gate3 := filepath.Join(dir, "gate3.json")
			writeRuntimeGateEvidence(t, gate2, "gate2-lima", "lima", binding)
			writeRuntimeGateEvidence(t, gate3, "gate3-hidden-proxy", "lima", binding)
			product := filepath.Join(dir, "runtime-product.json")
			writeRuntimeProductEvidence(t, product, packageIdentityFixture().SourceCommit, binding)
			manifest, err := productevidence.ReadFile(product)
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(dir, &manifest)
			if err := productevidence.WriteFile(product, manifest); err != nil {
				t.Fatal(err)
			}
			ready, err := BuildReadiness(ReadinessOptions{
				Mode: "release-candidate", LocalPassed: true, Gate2Evidence: gate2, Gate3Evidence: gate3,
				ProductEvidence: []string{product}, Runtime: &expected, Package: &packageIdentity,
			})
			if err != nil {
				t.Fatal(err)
			}
			if ready.ReleaseReady {
				t.Fatalf("%s produced release readiness: %+v", tc.name, ready)
			}
		})
	}
}

func firstFeatureProof(manifest *productevidence.Manifest, featureID string) int {
	for i, proof := range manifest.Proofs {
		if proof.FeatureID == featureID {
			return i
		}
	}
	panic("test fixture has no proof for feature " + featureID)
}

func TestReleaseCandidateKeepsPackageCommitIndependentFromRuntimeBuildCommit(t *testing.T) {
	expected := runtimeExpectationFixture()
	binding := runtimeBindingFixture()
	for _, tc := range []struct {
		name          string
		packageCommit string
		wantCommit    string
	}{
		{name: "invalid package commit", packageCommit: "pkg-0123456789ab", wantCommit: "unknown"},
		{name: "canonical package mismatches proof", packageCommit: "fedcba987654fedcba987654fedcba987654fedc", wantCommit: "fedcba987654fedcba987654fedcba987654fedc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			gate2 := filepath.Join(dir, "gate2.json")
			gate3 := filepath.Join(dir, "gate3.json")
			product := filepath.Join(dir, "runtime-product.json")
			writeRuntimeGateEvidence(t, gate2, "gate2-lima", "lima", binding)
			writeRuntimeGateEvidence(t, gate3, "gate3-hidden-proxy", "lima", binding)
			writeRuntimeProductEvidence(t, product, packageIdentityFixture().SourceCommit, binding)
			packageIdentity := packageIdentityFixture()
			packageIdentity.SourceCommit = tc.packageCommit
			ready, err := BuildReadiness(ReadinessOptions{
				Mode: "release-candidate", LocalPassed: true, Gate2Evidence: gate2, Gate3Evidence: gate3,
				ProductEvidence: []string{product}, Runtime: &expected, Package: &packageIdentity,
			})
			if err != nil {
				t.Fatal(err)
			}
			if ready.ReleaseReady {
				t.Fatalf("package identity mismatch produced release readiness: %+v", ready)
			}
			if ready.SourceCommit != tc.wantCommit {
				t.Fatalf("readiness sourceCommit=%q want package candidate %q", ready.SourceCommit, tc.wantCommit)
			}
		})
	}
}

func TestReleaseCandidateRequiresRegistryFeatureAndFullClaimIdentity(t *testing.T) {
	expected := runtimeExpectationFixture()
	binding := runtimeBindingFixture()
	packageIdentity := packageIdentityFixture()
	cases := []struct {
		name   string
		mutate func(*productevidence.ProofEntry)
	}{
		{name: "feature spoof", mutate: func(proof *productevidence.ProofEntry) { proof.FeatureID = "031-spoofed-feature" }},
		{name: "partial claim set", mutate: func(proof *productevidence.ProofEntry) {
			proof.CoveredClaims = proof.CoveredClaims[:len(proof.CoveredClaims)-1]
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			gate2 := filepath.Join(dir, "gate2.json")
			gate3 := filepath.Join(dir, "gate3.json")
			product := filepath.Join(dir, "runtime-product.json")
			writeRuntimeGateEvidence(t, gate2, "gate2-lima", "lima", binding)
			writeRuntimeGateEvidence(t, gate3, "gate3-hidden-proxy", "lima", binding)
			writeRuntimeProductEvidence(t, product, packageIdentityFixture().SourceCommit, binding)
			manifest, err := productevidence.ReadFile(product)
			if err != nil {
				t.Fatal(err)
			}
			index := firstFeatureProof(&manifest, productevidence.Feature031)
			tc.mutate(&manifest.Proofs[index])
			if err := productevidence.WriteFile(product, manifest); err != nil {
				t.Fatal(err)
			}
			ready, err := BuildReadiness(ReadinessOptions{
				Mode: "release-candidate", LocalPassed: true, Gate2Evidence: gate2, Gate3Evidence: gate3,
				ProductEvidence: []string{product}, Runtime: &expected, Package: &packageIdentity,
			})
			if err != nil {
				t.Fatal(err)
			}
			if ready.ReleaseReady || ready.Commands[1].Status == "passed" {
				t.Fatalf("invalid registry identity produced release readiness: %+v", ready)
			}
		})
	}
}

func TestReleaseCandidateRejectsUnknownAndSecretBearingGateFields(t *testing.T) {
	expected := runtimeExpectationFixture()
	binding := runtimeBindingFixture()
	packageIdentity := packageIdentityFixture()
	cases := []struct {
		name        string
		mutate      func(map[string]any)
		wantSummary string
	}{
		{name: "unknown field", mutate: func(gate map[string]any) { gate["unexpectedAuthority"] = true }, wantSummary: "unknown field"},
		{name: "unknown runtime field", mutate: func(gate map[string]any) {
			gate["runtime"].(map[string]any)["unexpectedAuthority"] = true
		}, wantSummary: "unknown field"},
		{name: "real capability token", mutate: func(gate map[string]any) {
			gate["reason"] = "cap_0123456789abcdef0123456789abcdef"
		}, wantSummary: "control-plane"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			gate2 := filepath.Join(dir, "gate2.json")
			gate3 := filepath.Join(dir, "gate3.json")
			product := filepath.Join(dir, "runtime-product.json")
			writeRuntimeGateEvidence(t, gate2, "gate2-lima", "lima", binding)
			writeRuntimeGateEvidence(t, gate3, "gate3-hidden-proxy", "lima", binding)
			writeRuntimeProductEvidence(t, product, packageIdentityFixture().SourceCommit, binding)
			data, err := os.ReadFile(gate2)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			tc.mutate(document)
			data, err = json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(gate2, data, 0o600); err != nil {
				t.Fatal(err)
			}
			ready, err := BuildReadiness(ReadinessOptions{
				Mode: "release-candidate", LocalPassed: true, Gate2Evidence: gate2, Gate3Evidence: gate3,
				ProductEvidence: []string{product}, Runtime: &expected, Package: &packageIdentity,
			})
			if err != nil {
				t.Fatal(err)
			}
			if ready.ReleaseReady || ready.Gates[0].Status != "failed" || !strings.Contains(ready.Gates[0].Summary, tc.wantSummary) {
				t.Fatalf("adversarial gate evidence was accepted: %+v", ready.Gates[0])
			}
		})
	}
}

func TestReleaseCandidateRejectsUnknownNestedReleaseDogfoodField(t *testing.T) {
	expected := runtimeExpectationFixture()
	binding := runtimeBindingFixture()
	document := map[string]any{
		"schema": "hideout.release-dogfood.v1",
		"status": "passed",
		"git": map[string]any{
			"commit":              expected.BuildCommit,
			"dirty":               false,
			"unexpectedAuthority": true,
		},
		"isolationGates": []any{map[string]any{
			"id": "gate2-lima", "backend": "lima", "result": "passed", "runtime": binding,
		}},
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "release-dogfood.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateGateEvidence("gate2-lima", path, &expected); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown nested release-dogfood field err=%v", err)
	}
}

func TestReleaseCandidateScansArtifactBytesDespitePassedRedactionStatus(t *testing.T) {
	expected := runtimeExpectationFixture()
	binding := runtimeBindingFixture()
	packageIdentity := packageIdentityFixture()
	dir := t.TempDir()
	gate2 := filepath.Join(dir, "gate2.json")
	gate3 := filepath.Join(dir, "gate3.json")
	product := filepath.Join(dir, "runtime-product.json")
	writeRuntimeGateEvidence(t, gate2, "gate2-lima", "lima", binding)
	writeRuntimeGateEvidence(t, gate3, "gate3-hidden-proxy", "lima", binding)
	writeRuntimeProductEvidence(t, product, packageIdentityFixture().SourceCommit, binding)
	manifest, err := productevidence.ReadFile(product)
	if err != nil {
		t.Fatal(err)
	}
	proof := &manifest.Proofs[firstFeatureProof(&manifest, productevidence.Feature031)]
	secretData := []byte("HIDEOUT_SECRET_DEFAULT_PROXY=socks5://user:pass@127.0.0.1:1080\n")
	if err := os.WriteFile(filepath.Join(dir, proof.Artifacts[0].Path), secretData, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(secretData)
	proof.Artifacts[0].SHA256 = hex.EncodeToString(sum[:])
	proof.Artifacts[0].RedactionStatus = productevidence.RedactionPassed
	if err := productevidence.WriteFile(product, manifest); err != nil {
		t.Fatal(err)
	}
	ready, err := BuildReadiness(ReadinessOptions{
		Mode: "release-candidate", LocalPassed: true, Gate2Evidence: gate2, Gate3Evidence: gate3,
		ProductEvidence: []string{product}, Runtime: &expected, Package: &packageIdentity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ready.ReleaseReady || ready.Commands[1].Status != "failed" || !strings.Contains(ready.Commands[1].Summary, "control-plane") {
		t.Fatalf("secret-bearing artifact bytes were accepted: %+v", ready.Commands[1])
	}
}

func TestValidateReadinessRequiresExactReleaseRows(t *testing.T) {
	if err := ValidateReadiness(validReleaseReadinessFixture()); err != nil {
		t.Fatalf("valid release readiness rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Readiness)
	}{
		{name: "empty commands", mutate: func(r *Readiness) { r.Commands = nil }},
		{name: "missing product command", mutate: func(r *Readiness) { r.Commands = r.Commands[:1] }},
		{name: "blank command identity", mutate: func(r *Readiness) { r.Commands[1].Name = "" }},
		{name: "failed product command", mutate: func(r *Readiness) { r.Commands[1].Status = "failed" }},
		{name: "duplicate local command", mutate: func(r *Readiness) { r.Commands[1] = r.Commands[0] }},
		{name: "empty gates", mutate: func(r *Readiness) { r.Gates = nil }},
		{name: "missing gate3", mutate: func(r *Readiness) { r.Gates = r.Gates[:1] }},
		{name: "duplicate gate2", mutate: func(r *Readiness) { r.Gates[1].ID = r.Gates[0].ID }},
		{name: "unexpected gate", mutate: func(r *Readiness) { r.Gates[1].ID = "gate4-host-escape" }},
		{name: "extra gate", mutate: func(r *Readiness) { r.Gates = append(r.Gates, r.Gates[0]) }},
		{name: "optional gate", mutate: func(r *Readiness) { r.Gates[0].Required = false }},
		{name: "failed gate", mutate: func(r *Readiness) { r.Gates[0].Status = "failed" }},
		{name: "missing gate runtime", mutate: func(r *Readiness) { r.Gates[0].Runtime = nil }},
		{name: "invalid gate environment", mutate: func(r *Readiness) { r.Gates[0].Runtime.EnvironmentID = "env_INVALID" }},
		{name: "reused gate environment", mutate: func(r *Readiness) { r.Gates[1].Runtime.EnvironmentID = r.Gates[0].Runtime.EnvironmentID }},
		{name: "artifact mismatch", mutate: func(r *Readiness) { r.Gates[1].Runtime.ArtifactSHA256 = strings.Repeat("b", 64) }},
		{name: "image build mismatch", mutate: func(r *Readiness) { r.Gates[1].Runtime.BuildCommit = "abcdef012345" }},
		{name: "noncanonical readiness commit", mutate: func(r *Readiness) { r.SourceCommit = "pkg-0123456789ab" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			readiness := validReleaseReadinessFixture()
			tc.mutate(&readiness)
			if err := ValidateReadiness(readiness); err == nil {
				t.Fatalf("fabricated release readiness was accepted: %+v", readiness)
			}
		})
	}
}

func validReleaseReadinessFixture() Readiness {
	gate2Runtime := runtimeBindingFixture()
	gate3Runtime := runtimeBindingFixture()
	gate3Runtime.EnvironmentID = "env_20260711t000000z1111111111111111111"
	packageIdentity := packageIdentityFixture()
	runtimeIdentity := runtimeExpectationFixture()
	return Readiness{
		Schema: ReadinessSchema, GeneratedAt: time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC),
		Mode: "release-candidate", EvidenceClass: "real-gate", ReleaseReady: true, Status: "passed",
		SourceCommit: packageIdentity.SourceCommit, Package: &packageIdentity, RuntimeIdentity: &runtimeIdentity,
		CandidateStatus: "passed", SigningObservationSHA256: strings.Repeat("d", 64), NotarizationObservationSHA256: strings.Repeat("e", 64),
		Platform: Platform{OS: "darwin", Arch: "arm64"}, Matrix: MatrixRef{Schema: MatrixSchema, Version: MatrixVersion},
		Commands: []CommandResult{
			{Name: "local-checks", Status: "passed", Summary: "local checks passed"},
			{Name: "product-hardening-evidence", Status: "passed", Summary: "global release registry passed"},
		},
		Gates: []GateResult{
			{ID: "gate2-lima", Required: true, Status: "passed", EvidencePath: "gate2.json", Summary: "Gate 2 passed", Runtime: &gate2Runtime},
			{ID: "gate3-hidden-proxy", Required: true, Status: "passed", EvidencePath: "gate3.json", Summary: "Gate 3 passed", Runtime: &gate3Runtime},
		},
		NonClaims: RequiredNonClaimIDs(), Redaction: Redaction{Mode: "control-plane"},
	}
}

func TestReadinessRedactsControlPlaneMaterial(t *testing.T) {
	ready, err := BuildReadiness(ReadinessOptions{
		Mode:          "release-candidate",
		Commit:        `HIDEOUT_SECRET_DEFAULT_PROXY=socks5://user:pass@127.0.0.1:7890 cap_0123456789abcdef0123456789abcdef`,
		Gate2Evidence: "/tmp/HIDEOUT_SECRET_DEFAULT_PROXY=secret/gate2.json",
		Gate3Evidence: "/tmp/gate3.json",
		LocalPassed:   false,
	})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteReadinessJSON(&buf, ready); err != nil {
		t.Fatalf("write readiness: %v", err)
	}
	text := buf.String()
	for _, forbidden := range []string{
		"HIDEOUT_SECRET_DEFAULT_PROXY=socks5://user:pass@127.0.0.1:7890",
		"cap_0123456789abcdef0123456789abcdef",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("readiness leaked %q:\n%s", forbidden, text)
		}
	}
	var decoded Readiness
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if decoded.Redaction.Mode != "control-plane" {
		t.Fatalf("redaction=%+v", decoded.Redaction)
	}
}

func writeCompleteProductEvidence(t *testing.T, path, commit string) {
	t.Helper()
	writeProductEvidence(t, path, commit, productevidence.ProductHardeningRequirements())
}

func writeFeatureProductEvidence(t *testing.T, path, commit string, featureID string) {
	t.Helper()
	writeProductEvidence(t, path, commit, productevidence.RequirementsForFeature(featureID))
}

func writeProductEvidence(t *testing.T, path, commit string, reqs []productevidence.ProofRequirement) {
	t.Helper()
	manifest := productevidence.NewManifest(commit, false)
	for _, req := range reqs {
		proof := productevidence.ProofEntry{
			ProofID:         req.ProofID,
			FeatureID:       req.FeatureID,
			Mode:            "unit",
			EvidenceClass:   "unit-fixture",
			Status:          productevidence.StatusPassed,
			CommandSummary:  "unit fixture",
			CoveredClaims:   coveredClaimsForRequirement(req),
			Prerequisites:   []productevidence.PrerequisiteStatus{{Name: "unit", Status: "available"}},
			Artifacts:       []productevidence.ArtifactRef{},
			RedactionStatus: productevidence.RedactionPassed,
		}
		if req.ArtifactPolicy != productevidence.ArtifactPolicyNone {
			rel := filepath.Join("artifacts", req.ProofID+".txt")
			artifactPath := filepath.Join(filepath.Dir(path), rel)
			if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(artifactPath, []byte("unit fixture\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			proof.Artifacts = append(proof.Artifacts, productevidence.ArtifactRef{
				Kind:            "log",
				Path:            rel,
				RedactionStatus: productevidence.RedactionPassed,
				Description:     "unit fixture artifact",
			})
		}
		manifest.Proofs = append(manifest.Proofs, proof)
	}
	if err := productevidence.WriteFile(path, manifest); err != nil {
		t.Fatal(err)
	}
}

func runtimeBindingFixture() productevidence.RuntimeBinding {
	return productevidence.RuntimeBinding{
		Schema: productevidence.RuntimeBindingSchema, Family: "developer-standard", Revision: "2026.07.0",
		ArtifactSHA256: strings.Repeat("a", 64), EnvironmentID: "env_20260711t000000z0123456789abcdef0123",
		HostOS: "darwin", HostArch: "arm64", GuestArch: "aarch64",
		BuildCommit: "0123456789ab", BuildDirty: false,
	}
}

func runtimeExpectationFixture() productevidence.RuntimeExpectation {
	b := runtimeBindingFixture()
	return productevidence.RuntimeExpectation{
		Family: b.Family, Revision: b.Revision, ArtifactSHA256: b.ArtifactSHA256,
		HostOS: b.HostOS, HostArch: b.HostArch, GuestArch: b.GuestArch,
		BuildCommit: b.BuildCommit, RequireClean: true,
	}
}

func packageIdentityFixture() productevidence.PackageIdentity {
	return productevidence.PackageIdentity{
		Name: "hideout", ProductVersion: "0.1.0-alpha.1",
		SourceCommit:   "abcdef012345abcdef012345abcdef012345abcd",
		ArtifactSHA256: strings.Repeat("c", 64), HostOS: "darwin", HostArch: "arm64",
	}
}

func writeRuntimeGateEvidence(t *testing.T, path, id, backend string, binding productevidence.RuntimeBinding) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"id": id, "backend": backend, "result": "passed", "runtime": binding})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeRuntimeProductEvidence(t *testing.T, path, commit string, binding productevidence.RuntimeBinding) {
	t.Helper()
	manifest := productevidence.NewManifest(commit, false)
	packageIdentity := packageIdentityFixture()
	manifest.PackageIdentity = &packageIdentity
	for _, req := range productevidence.ProductHardeningRequirements() {
		if req.RequiredFor != productevidence.RequiredForReleaseCandidate {
			continue
		}
		rel := filepath.Join("artifacts", req.ProofID+".log")
		artifact := filepath.Join(filepath.Dir(path), rel)
		if err := os.MkdirAll(filepath.Dir(artifact), 0o700); err != nil {
			t.Fatal(err)
		}
		artifactData := []byte("runtime proof\n")
		if req.ArtifactValidator != productevidence.ArtifactValidatorNone {
			artifactData = semantic034Artifact(t, req.ArtifactValidator, commit, binding)
		}
		if err := os.WriteFile(artifact, artifactData, 0o600); err != nil {
			t.Fatal(err)
		}
		artifactSum := sha256.Sum256(artifactData)
		mode := req.RequiredMode
		if mode == "" {
			mode = "real-gate"
		}
		evidenceClass := req.RequiredEvidenceClass
		if evidenceClass == "" {
			evidenceClass = "runtime-real-gate"
		}
		proof := productevidence.ProofEntry{
			ProofID: req.ProofID, FeatureID: req.FeatureID, Mode: mode, EvidenceClass: evidenceClass,
			Status: productevidence.StatusPassed, CommandSummary: "runtime fixture",
			CoveredClaims:   coveredClaimsForRequirement(req),
			Prerequisites:   []productevidence.PrerequisiteStatus{{Name: "real-runtime", Status: "available"}},
			Artifacts:       []productevidence.ArtifactRef{{Kind: "log", Path: rel, SHA256: hex.EncodeToString(artifactSum[:]), RedactionStatus: productevidence.RedactionPassed}},
			RedactionStatus: productevidence.RedactionPassed,
		}
		if req.RuntimePolicy == productevidence.RuntimePolicyExactReal {
			copy := binding
			proof.Runtime = &copy
		}
		manifest.Proofs = append(manifest.Proofs, proof)
	}
	if err := productevidence.WriteFile(path, manifest); err != nil {
		t.Fatal(err)
	}
}

func semantic034Artifact(t *testing.T, validator, commit string, binding productevidence.RuntimeBinding) []byte {
	t.Helper()
	var value any
	switch validator {
	case productevidence.ArtifactValidatorConcurrentIsolationV1:
		checks := map[string]bool{}
		for _, name := range []string{
			"threeSameWorkspaceOwners", "distinctSessionIds", "distinctPidNamespaces", "distinctMountNamespaces",
			"nonRootTargets", "privateProc", "siblingPidHidden", "siblingRuntimeHidden",
			"guestRootPositiveControl", "hostfsOverlaySessionLocal", "forcedInterruptionTargetGone",
			"siblingSurvivedInterruption", "ownerReconciled", "stopRefusedWithLiveOwners",
			"lastSessionPreservedVm", "explicitStopStoppedVm",
		} {
			checks[name] = true
		}
		value = map[string]any{
			"schema": "hideout.concurrent-sessions-gate2/v1", "status": "passed",
			"generatedAt": "2026-07-16T12:00:00Z", "commit": commit, "dirty": false,
			"backend": "lima", "host": "macos-arm64", "metrics": map[string]any{"ownerReconcileMs": 125}, "checks": checks,
			"nonClaims": map[string]any{"guestRootContainment": "not-claimed"},
		}
	case productevidence.ArtifactValidatorConcurrentPerformanceV1:
		warm := make([]float64, 30)
		candidate := make([]float64, 30)
		baseline := make([]float64, 30)
		for i := range warm {
			warm[i], candidate[i], baseline[i] = 100, 10, 8
		}
		value = map[string]any{
			"schema": "hideout.concurrent-sessions-performance/v1", "status": "passed", "generatedAt": "2026-07-16T12:00:00Z",
			"candidate":        map[string]any{"commit": commit, "dirty": false, "environmentId": binding.EnvironmentID, "instance": "candidate"},
			"baseline":         map[string]any{"commit": strings.Repeat("b", 40), "dirty": false, "environmentId": "env_baseline", "instance": "baseline"},
			"host":             map[string]any{"os": "darwin", "arch": "arm64"},
			"runtime":          map[string]any{"family": binding.Family, "revision": binding.Revision, "artifactSHA256": binding.ArtifactSHA256, "buildCommit": binding.BuildCommit, "buildDirty": false},
			"methodology":      map[string]any{"samples": 30, "warmups": 3, "readyThresholdMs": 2000, "fixtureRatioThreshold": 1.25, "fixtureSHA256": strings.Repeat("c", 64)},
			"warmAttach":       map[string]any{"samplesMs": warm, "medianMs": 100, "p95Ms": 100},
			"workspaceFixture": map[string]any{"candidateSamplesMs": candidate, "baselineSamplesMs": baseline, "candidateMedianMs": 10, "candidateP95Ms": 10, "baselineMedianMs": 8, "baselineP95Ms": 8, "p95Ratio": 1.25},
		}
	default:
		t.Fatalf("unknown semantic validator %q", validator)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func coveredClaimsForRequirement(req productevidence.ProofRequirement) []productevidence.CoveredClaim {
	claims := make([]productevidence.CoveredClaim, 0, len(req.ClaimIDs))
	for _, claimID := range req.ClaimIDs {
		claims = append(claims, productevidence.CoveredClaim{ClaimID: claimID, Source: "spec", Description: "unit fixture"})
	}
	return claims
}
