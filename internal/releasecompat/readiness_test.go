package releasecompat

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/packagekit"
	"github.com/vibe-agi/hideout/internal/productevidence"
	"github.com/vibe-agi/hideout/internal/recovery"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
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
	commit := strings.Repeat("a", 40)
	writeCompleteProductEvidence(t, evidence, commit)
	ready, err := BuildReadiness(ReadinessOptions{
		Mode:            "local-fast",
		Commit:          commit,
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
	commit := strings.Repeat("a", 40)
	var targeted []productevidence.ProofRequirement
	for _, req := range productevidence.ProductHardeningRequirements() {
		if req.RequiredFor == productevidence.RequiredForTargetedCompletion {
			targeted = append(targeted, req)
		}
	}
	writeProductEvidence(t, evidence, commit, targeted)
	ready, err := BuildReadiness(ReadinessOptions{
		Mode:            "local-fast",
		Commit:          commit,
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
	writeCompleteProductEvidence(t, evidence, strings.Repeat("a", 40))
	ready, err := BuildReadiness(ReadinessOptions{
		Mode:            "local-fast",
		Commit:          strings.Repeat("b", 40),
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

func TestReleaseCandidateRequiresProjectionReadinessProofSet(t *testing.T) {
	cases := []struct {
		name            string
		mutate          func(*productevidence.Manifest)
		wantReady       bool
		wantSummaryPart string
	}{
		{name: "complete exact proof set", wantReady: true},
		{
			name: "missing 043 readiness",
			mutate: func(manifest *productevidence.Manifest) {
				removeProductProof(manifest, productevidence.Proof043RealReadiness)
			},
			wantSummaryPart: productevidence.Proof043RealReadiness,
		},
		{
			name: "missing 039 persistent grant",
			mutate: func(manifest *productevidence.Manifest) {
				removeProductProof(manifest, productevidence.Proof039RealPersistentGrant)
			},
			wantSummaryPart: productevidence.Proof039RealPersistentGrant,
		},
		{
			name: "043 readiness not run",
			mutate: func(manifest *productevidence.Manifest) {
				proof := findProductProof(t, manifest, productevidence.Proof043RealReadiness)
				proof.Status = productevidence.StatusNotRun
				proof.RedactionStatus = productevidence.RedactionNotRun
				proof.NotRunReason = "real projection gate unavailable"
			},
		},
		{
			name: "dirty product evidence",
			mutate: func(manifest *productevidence.Manifest) {
				manifest.Dirty = true
			},
		},
		{
			name: "043 runtime mismatch",
			mutate: func(manifest *productevidence.Manifest) {
				proof := findProductProof(t, manifest, productevidence.Proof043RealReadiness)
				proof.Runtime.Revision = "other"
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			if tc.mutate != nil {
				manifest, err := productevidence.ReadFile(product)
				if err != nil {
					t.Fatal(err)
				}
				tc.mutate(&manifest)
				if err := productevidence.WriteFile(product, manifest); err != nil {
					t.Fatal(err)
				}
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
			if ready.ReleaseReady != tc.wantReady {
				t.Fatalf("releaseReady=%v want %v readiness=%+v", ready.ReleaseReady, tc.wantReady, ready)
			}
			if tc.wantReady {
				return
			}
			for _, command := range ready.Commands {
				if command.Name != "product-hardening-evidence" {
					continue
				}
				if command.Status != "failed" {
					t.Fatalf("projection evidence failure was not enforced: %+v", command)
				}
				if tc.wantSummaryPart != "" && !strings.Contains(command.Summary, tc.wantSummaryPart) {
					t.Fatalf("failure summary %q does not mention %q", command.Summary, tc.wantSummaryPart)
				}
				return
			}
			t.Fatal("readiness omitted product-hardening-evidence result")
		})
	}
}

func findProductProof(t *testing.T, manifest *productevidence.Manifest, proofID string) *productevidence.ProofEntry {
	t.Helper()
	for i := range manifest.Proofs {
		if manifest.Proofs[i].ProofID == proofID {
			return &manifest.Proofs[i]
		}
	}
	t.Fatalf("fixture missing proof %s", proofID)
	return nil
}

func removeProductProof(manifest *productevidence.Manifest, proofID string) {
	for i := range manifest.Proofs {
		if manifest.Proofs[i].ProofID == proofID {
			manifest.Proofs = append(manifest.Proofs[:i], manifest.Proofs[i+1:]...)
			return
		}
	}
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
		packageIdentity := packageIdentityFixture()
		packageIdentity.SourceCommit = commit
		proof.Artifacts = writeRequirementArtifacts(t, filepath.Dir(path), req, commit, runtimeBindingFixture(), packageIdentity)
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
			Artifacts:       writeRequirementArtifacts(t, filepath.Dir(path), req, commit, binding, packageIdentity),
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

func writeRequirementArtifacts(t *testing.T, root string, req productevidence.ProofRequirement, commit string, binding productevidence.RuntimeBinding, packageIdentity productevidence.PackageIdentity) []productevidence.ArtifactRef {
	t.Helper()
	if req.ArtifactPolicy == productevidence.ArtifactPolicyNone {
		return nil
	}
	artifacts := map[string][]byte{}
	switch req.ArtifactValidator {
	case productevidence.ArtifactValidatorSharedWorkspaceBehaviorV1,
		productevidence.ArtifactValidatorSharedWorkspacePerformanceV1:
		artifacts = sharedWorkspaceSemanticArtifacts(t, req.ArtifactValidator, commit, binding, packageIdentity)
	case productevidence.ArtifactValidatorProjectionReadinessV1,
		productevidence.ArtifactValidatorProjectionPrivacyV1:
		artifacts = projectionReadinessSemanticArtifacts(t, commit, binding, packageIdentity)
	default:
		rel := filepath.Join("artifacts", req.ProofID+".log")
		data := []byte("runtime proof\n")
		if req.ArtifactValidator != productevidence.ArtifactValidatorNone {
			data = semanticProductArtifact(t, req.ArtifactValidator, commit, binding)
		}
		artifacts[rel] = data
	}
	paths := make([]string, 0, len(artifacts))
	for path := range artifacts {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	refs := make([]productevidence.ArtifactRef, 0, len(paths))
	for _, rel := range paths {
		data := artifacts[rel]
		artifactPath := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(artifactPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		kind := "log"
		if (req.ArtifactValidator == productevidence.ArtifactValidatorSharedWorkspaceBehaviorV1 ||
			req.ArtifactValidator == productevidence.ArtifactValidatorSharedWorkspacePerformanceV1 ||
			req.ArtifactValidator == productevidence.ArtifactValidatorProjectionReadinessV1 ||
			req.ArtifactValidator == productevidence.ArtifactValidatorProjectionPrivacyV1) &&
			!strings.HasSuffix(rel, ".values") && !strings.HasSuffix(rel, ".tsv") {
			kind = "manifest"
		}
		refs = append(refs, productevidence.ArtifactRef{
			Kind: kind, Path: rel, SHA256: hex.EncodeToString(sum[:]),
			RedactionStatus: productevidence.RedactionPassed,
			Description:     "release-readiness semantic fixture artifact",
		})
	}
	return refs
}

func projectionReadinessSemanticArtifacts(
	t *testing.T,
	commit string,
	binding productevidence.RuntimeBinding,
	packageIdentity productevidence.PackageIdentity,
) map[string][]byte {
	t.Helper()
	readinessChecks := trueMap([]string{
		"readiness.catalog", "readiness.manifest", "readiness.dispatcher",
		"readiness.entryProperties", "readiness.exactSessionView", "readiness.readyCommitProof",
		"refusal.staleCatalog", "refusal.identityDrift", "refusal.bootDrift",
		"refusal.timeout", "refusal.cancellation", "refusal.symlink", "refusal.type",
		"refusal.digest", "refusal.zeroTarget", "refusal.zeroEffect", "refusal.zeroFallback",
		"concurrency.disjointCatalogs", "concurrency.ordinaryCommandCompatibility",
		"redaction.applicationIdentityClass", "redaction.publicArtifactScan",
	})
	flowChecks := trueMap([]string{
		"projection030.safeHostEffect", "projection030.taskSuppression",
		"projection030.aliasChannels", "projection030.preservePositiveControl",
		"projection030.runBoundGrant", "projection030.runBoundRevoke",
		"external032.oldSessionImmutable", "external032.workspaceResource",
		"external032.authorizedHostFS", "external032.unsafeIdentityDenied",
		"external032.disableNoFallback", "external032.revokeNoFallback",
		"persistent039.initialRefusal", "persistent039.hostGrant",
		"persistent039.separateRunReuse", "persistent039.revoke",
		"persistent039.laterRefusal",
	})
	privacyChecks := trueMap([]string{
		"guestWorkspaceAlias", "proxyEnvAbsent", "dnsMediated",
		"connectedSubnetBlocked", "httpsRequest", "privilegeSeparation",
		"publicEvidenceRedacted",
	})
	var samples strings.Builder
	samples.WriteString("lane\tindex\tduration_ms\tfirst_target\toperator_retries\ttarget_retries\tfallbacks\ttimeouts\tunauthorized_host_effects\tcross_session_access\n")
	for index := 1; index <= 10; index++ {
		fmt.Fprintf(&samples, "fresh\t%d\t%d\tprojected\t0\t0\t0\t0\t0\t0\n", index, 100+index-1)
	}
	for index := 1; index <= 30; index++ {
		fmt.Fprintf(&samples, "warm\t%d\t%d\tprojected\t0\t0\t0\t0\t0\t0\n", index, 50+index-1)
	}
	samples.WriteString("cancellation\t1\t100\tnone\t0\t0\t0\t0\t0\t0\n")
	artifacts := map[string][]byte{
		"artifacts/readiness-samples.tsv": []byte(samples.String()),
		"artifacts/projection-flows.json": fixtureJSON(t, map[string]any{
			"schema": "hideout.projection-flows-real-gate2/v1",
			"status": "passed", "checks": flowChecks,
		}),
		"artifacts/package-manifest.json": fixtureJSON(t, map[string]any{
			"schema":          "hideout.projection-package-manifest/v1",
			"packageIdentity": packageIdentity,
		}),
		"artifacts/runtime-manifest.json": fixtureJSON(t, map[string]any{
			"schema":  "hideout.projection-runtime-manifest/v1",
			"runtime": binding,
		}),
		"artifacts/projection-privacy-gate3.json": fixtureJSON(t, map[string]any{
			"schema": "hideout.projection-privacy-real-gate3/v1",
			"status": "passed", "generatedAt": "2026-07-23T12:00:00Z",
			"commit": commit, "dirty": false, "packageIdentity": packageIdentity,
			"runtime": binding, "checks": privacyChecks,
		}),
	}
	artifacts["artifacts/projection-readiness.json"] = fixtureJSON(t, map[string]any{
		"schema": "hideout.projection-readiness-real-gate2/v1",
		"status": "passed", "generatedAt": "2026-07-23T12:00:00Z",
		"commit": commit, "dirty": false, "packageIdentity": packageIdentity,
		"runtime": binding,
		"platform": map[string]any{
			"hostOS": "darwin", "hostArch": "arm64", "guestArch": "aarch64",
			"backend": "lima", "applicationIdentityClass": "bundle-id+designated-requirement",
		},
		"methodology": map[string]any{
			"minimumFreshSamples": 10, "minimumWarmSamples": 30,
			"minimumConcurrentPairs": 1, "p95Method": "nearest-rank",
			"readinessThresholdMs": 2000, "cancellationThresholdMs": 2000,
		},
		"readiness": map[string]any{
			"freshSamples": 10, "warmSamples": 30, "concurrentPairs": 1,
			"freshP95Ms": 109, "warmP95Ms": 78, "cancellationMaxMs": 100,
			"operatorRetries": 0, "targetRetries": 0, "fallbacks": 0, "timeouts": 0,
			"unauthorizedHostEffects": 0, "crossSessionAccess": 0,
		},
		"checks": readinessChecks,
		"artifacts": map[string]any{
			"samples":         fixtureDigest("artifacts/readiness-samples.tsv", artifacts["artifacts/readiness-samples.tsv"]),
			"flows":           fixtureDigest("artifacts/projection-flows.json", artifacts["artifacts/projection-flows.json"]),
			"packageManifest": fixtureDigest("artifacts/package-manifest.json", artifacts["artifacts/package-manifest.json"]),
			"runtimeManifest": fixtureDigest("artifacts/runtime-manifest.json", artifacts["artifacts/runtime-manifest.json"]),
		},
		"privacy": map[string]any{
			"status": "promoted",
			"artifact": fixtureDigest(
				"artifacts/projection-privacy-gate3.json",
				artifacts["artifacts/projection-privacy-gate3.json"],
			),
		},
		"nonClaims": []string{
			"guest-root-out-of-scope",
			"native-is-not-real-evidence",
			"readiness-is-not-authority",
		},
	})
	return artifacts
}

func sharedWorkspaceSemanticArtifacts(t *testing.T, validator, commit string, binding productevidence.RuntimeBinding, packageIdentity productevidence.PackageIdentity) map[string][]byte {
	t.Helper()
	decisionData := readSharedWorkspaceDecisionFixture(t)
	var decision workspaceattach.ResearchDecision
	if err := json.Unmarshal(decisionData, &decision); err != nil {
		t.Fatal(err)
	}
	if validator == productevidence.ArtifactValidatorSharedWorkspaceBehaviorV1 {
		return sharedWorkspaceBehaviorArtifacts(t, commit, binding, packageIdentity, decisionData)
	}
	if validator == productevidence.ArtifactValidatorSharedWorkspacePerformanceV1 {
		return sharedWorkspacePerformanceArtifacts(t, commit, binding, packageIdentity, decision, decisionData)
	}
	t.Fatalf("unsupported shared-workspace semantic validator %q", validator)
	return nil
}

func sharedWorkspaceBehaviorArtifacts(t *testing.T, commit string, binding productevidence.RuntimeBinding, packageIdentity productevidence.PackageIdentity, decisionData []byte) map[string][]byte {
	t.Helper()
	helperDigest := strings.Repeat("d", 64)
	checks := trueMap([]string{
		"oneMachineTwoProjects", "disjointIsolation", "sameRootLocks", "nestedAuthority",
		"siblingDetach", "lifecycleIntegration", "restartNoReadoption", "packagedHelperVerified", "hostPathRedacted",
	})
	relationChecks := trueMap([]string{
		"oneEnvironment", "oneInstance", "sameBootAcrossDisjointRoots", "sameBootAcrossNestedRoots",
		"disjointBidirectionalUnavailable", "siblingDetachPreservedExecution", "sameRootLockOwnersIndependent", "nestedAuthorityEnforced",
	})
	lifecycleChecks := trueMap([]string{
		"siblingDetachPreservedExecution", "bridgePinnedMachine", "graceCancelledByCrossWorkspaceAttach",
		"graceCancelReusedBoot", "exactFinalStopObserved", "restartDidNotReadoptWorkspaceAuthority",
		"postReconciliationAttachUsedFreshAuthority",
	})
	manifest := packagekit.Manifest{
		Schema:  packagekit.ArtifactSchema,
		Release: packagekit.ReleaseInfo{ProductVersion: packageIdentity.ProductVersion},
		Source:  packagekit.SourceInfo{Commit: packageIdentity.SourceCommit},
		Target:  packagekit.Target{HostOS: packageIdentity.HostOS, HostArch: packageIdentity.HostArch, LinuxGuestArch: "arm64"},
		Files: []packagekit.File{{
			Path: "bin/" + helperbin.LinuxWorkspacePortalCommand + "-linux-arm64",
			Kind: "linux-helper", SHA256: helperDigest, Executable: true,
		}},
	}
	helper := helperbin.Manifest{
		Version: helperbin.ManifestVersion, Command: helperbin.LinuxWorkspacePortalCommand,
		TargetOS: "linux", TargetArch: "arm64", Artifact: helperbin.LinuxWorkspacePortalCommand + "-linux-arm64", SHA256: helperDigest,
	}
	artifacts := map[string][]byte{
		"artifacts/behavior/relations.json": fixtureJSON(t, map[string]any{
			"schema": "hideout.shared-workspace-relations/v1", "status": "passed",
			"environmentCount": 1, "instanceCount": 1, "checks": relationChecks,
		}),
		"artifacts/behavior/lifecycle.json": fixtureJSON(t, map[string]any{
			"schema": "hideout.shared-workspace-lifecycle/v1", "status": "passed",
			"firstGeneration": 1, "restartGeneration": 2, "elapsedSeconds": 1, "checks": lifecycleChecks,
		}),
		"artifacts/behavior/correctness.json":                      fixtureJSON(t, validSharedWorkspaceCorrectnessFixture(30)),
		"artifacts/behavior/research-decision.json":                decisionData,
		"artifacts/behavior/package-manifest.json":                 fixtureJSON(t, manifest),
		"artifacts/behavior/workspace-portal-helper.manifest.json": fixtureJSON(t, helper),
	}
	descriptors := map[string]any{}
	for key, path := range map[string]string{
		"relations": "artifacts/behavior/relations.json", "lifecycle": "artifacts/behavior/lifecycle.json",
		"correctness": "artifacts/behavior/correctness.json", "researchDecision": "artifacts/behavior/research-decision.json",
		"packageManifest":               "artifacts/behavior/package-manifest.json",
		"workspacePortalHelperManifest": "artifacts/behavior/workspace-portal-helper.manifest.json",
	} {
		descriptors[key] = fixtureDigest(path, artifacts[path])
	}
	artifacts["behavior.json"] = fixtureJSON(t, map[string]any{
		"schema": "hideout.shared-workspace-real-gate2/v1", "status": "passed",
		"commit": commit, "dirty": false, "backend": "lima", "hostOS": "darwin", "hostArch": "arm64",
		"transport": "workspace-portal", "packageIdentity": packageIdentity, "runtime": binding,
		"artifacts": descriptors, "checks": checks,
	})
	return artifacts
}

func sharedWorkspacePerformanceArtifacts(t *testing.T, commit string, binding productevidence.RuntimeBinding, packageIdentity productevidence.PackageIdentity, decision workspaceattach.ResearchDecision, decisionData []byte) map[string][]byte {
	t.Helper()
	candidatePaths := map[string]string{
		"git-status":           "artifacts/performance/candidate/git-status.values",
		"package-scan":         "artifacts/performance/candidate/package-scan.values",
		"atomic-host-to-guest": "artifacts/performance/candidate/atomic-host-to-guest.values",
		"atomic-guest-to-host": "artifacts/performance/candidate/atomic-guest-to-host.values",
		"mount-ready":          "artifacts/performance/candidate/mount-ready.values",
		"first-byte":           "artifacts/performance/candidate/first-byte.values",
		"saturation-metadata":  "artifacts/performance/candidate/saturation-metadata.values",
	}
	controlPaths := map[string]string{
		"git-status":   "artifacts/performance/filesystem-control/git-status.values",
		"package-scan": "artifacts/performance/filesystem-control/package-scan.values",
	}
	researchPaths := map[string]string{
		"first-byte": "artifacts/performance/research-baseline/first-byte.values",
	}
	correctness := validSharedWorkspaceCorrectnessFixture(30)
	artifacts := map[string][]byte{
		"artifacts/performance/research-baseline/fixture.sha256":      []byte(decision.Provenance.FixtureDigest + "\n"),
		"artifacts/performance/filesystem-control/paired-samples.tsv": sharedWorkspacePairedSamplesFixture(100, 100, 100, 100, 30),
		"artifacts/performance/correctness.json":                      fixtureJSON(t, correctness),
		"artifacts/performance/saturation.json":                       fixtureJSON(t, map[string]any{"teardownMs": 100}),
		"artifacts/performance/research-decision.json":                decisionData,
	}
	for id, path := range candidatePaths {
		count := 30
		if id == "saturation-metadata" {
			count = 100
		}
		artifacts[path] = fixtureSamples(100, count)
	}
	for _, path := range controlPaths {
		artifacts[path] = fixtureSamples(100, 30)
	}
	for _, path := range researchPaths {
		artifacts[path] = fixtureSamples(100, 30)
	}
	candidateDescriptors := map[string]any{}
	for id, path := range candidatePaths {
		candidateDescriptors[id] = fixtureDigest(path, artifacts[path])
	}
	controlDescriptors := map[string]any{}
	for id, path := range controlPaths {
		controlDescriptors[id] = fixtureDigest(path, artifacts[path])
	}
	researchDescriptors := map[string]any{}
	for id, path := range researchPaths {
		researchDescriptors[id] = fixtureDigest(path, artifacts[path])
	}
	metrics := make([]map[string]any, 0, 6)
	for _, id := range []string{"git-status", "package-scan", "atomic-host-to-guest", "atomic-guest-to-host", "mount-ready", "first-byte"} {
		metric := map[string]any{
			"id": id, "candidate": map[string]any{"samples": 30, "medianMs": 100, "p95Ms": 100},
			"referenceKind": "absolute-threshold", "passed": true,
		}
		if _, ok := controlPaths[id]; ok {
			metric["reference"] = map[string]any{"samples": 30, "medianMs": 100, "p95Ms": 100}
			metric["referenceKind"] = "paired-static-virtiofs"
		} else if _, ok := researchPaths[id]; ok {
			metric["reference"] = map[string]any{"samples": 30, "medianMs": 100, "p95Ms": 100}
			metric["referenceKind"] = "retained-research-baseline"
		}
		metrics = append(metrics, metric)
	}
	pairedPath := "artifacts/performance/filesystem-control/paired-samples.tsv"
	controlManifestPath := "artifacts/performance/filesystem-control/manifest.json"
	artifacts[controlManifestPath] = fixtureJSON(t, map[string]any{
		"schema": "hideout.shared-workspace-paired-control/v1", "commit": commit, "dirty": false,
		"mechanism":     "profile-cache-static-virtiofs",
		"guestRoot":     "/hideout/profile/cache/035-static-virtiofs-control",
		"fixtureSHA256": decision.Provenance.FixtureDigest,
		"samples":       30, "warmups": 1, "sampleOrder": "alternating-pairs",
		"artifacts": controlDescriptors, "pairedSamples": fixtureDigest(pairedPath, artifacts[pairedPath]),
	})
	artifacts["performance.json"] = fixtureJSON(t, map[string]any{
		"schema": "hideout.shared-workspace-gate2-evaluation/v1", "result": "passed", "thresholdsPassed": true,
		"fixtureSHA256":    decision.Provenance.FixtureDigest,
		"candidate":        map[string]any{"commit": commit, "dirty": false},
		"researchBaseline": map[string]any{"commit": decision.Provenance.Commit, "dirty": decision.Provenance.Dirty},
		"filesystemControl": map[string]any{
			"commit": commit, "dirty": false, "mechanism": "profile-cache-static-virtiofs",
			"guestRoot": "/hideout/profile/cache/035-static-virtiofs-control", "sampleOrder": "alternating-pairs",
		},
		"packageIdentity": packageIdentity, "runtime": binding,
		"methodology": map[string]any{
			"samples": 30, "warmups": 1, "filesystemSampleOrder": "alternating-pairs",
			"firstByteSampleOrder": "one-warmup-then-measured",
			"gitMedianAbsoluteMs":  2000, "gitMedianBaselineRatio": 2, "packageMedianBaselineRatio": 3,
			"atomicVisibilityP95Ms": 250, "mountReadyP95Ms": 1000,
			"firstByteAbsoluteAllowanceMs": 500, "firstByteBaselineAllowance": 0.15, "saturationTeardownMs": 5000,
		},
		"metrics":     metrics,
		"correctness": map[string]any{"passed": true, "observation": correctness},
		"saturation": map[string]any{
			"passed": true, "observation": map[string]any{"teardownMs": 100},
			"metadata": map[string]any{"samples": 100, "medianMs": 100, "p95Ms": 100},
		},
		"artifacts": map[string]any{
			"candidate": candidateDescriptors, "filesystemControl": controlDescriptors,
			"researchBaseline":          researchDescriptors,
			"fixture":                   fixtureDigest("artifacts/performance/research-baseline/fixture.sha256", artifacts["artifacts/performance/research-baseline/fixture.sha256"]),
			"filesystemControlManifest": fixtureDigest(controlManifestPath, artifacts[controlManifestPath]),
			"pairedSamples":             fixtureDigest(pairedPath, artifacts[pairedPath]),
			"correctness":               fixtureDigest("artifacts/performance/correctness.json", artifacts["artifacts/performance/correctness.json"]),
			"saturation":                fixtureDigest("artifacts/performance/saturation.json", artifacts["artifacts/performance/saturation.json"]),
			"researchDecision":          fixtureDigest("artifacts/performance/research-decision.json", artifacts["artifacts/performance/research-decision.json"]),
		},
	})
	return artifacts
}

func validSharedWorkspaceCorrectnessFixture(samples int) map[string]any {
	return map[string]any{
		"schema":            "hideout.shared-workspace-correctness/v1",
		"hostCreateVisible": true, "targetCreateVisible": true,
		"hostAtomicReplaceVisible": true, "targetAtomicReplaceVisible": true,
		"renameVisible": true, "deleteVisible": true, "modeVisible": true, "flushDurable": true,
		"sameRootLocksConflict": true, "rootEscapeRejected": true, "symlinkEscapeRejected": true,
		"watcherStreamHealthy": true, "silentShortWrites": 0, "falseSuccesses": 0,
		"hostWatcherSamples": samples, "targetWatcherSamples": samples,
	}
}

func trueMap(names []string) map[string]bool {
	values := make(map[string]bool, len(names))
	for _, name := range names {
		values[name] = true
	}
	return values
}

func fixtureSamples(value float64, count int) []byte {
	return []byte(strings.Repeat(fmt.Sprintf("%.6f\n", value), count))
}

func sharedWorkspacePairedSamplesFixture(gitCandidate, gitControl, packageCandidate, packageControl float64, count int) []byte {
	values := map[string]map[string]float64{
		"git-status":   {"candidate": gitCandidate, "control": gitControl},
		"package-scan": {"candidate": packageCandidate, "control": packageControl},
	}
	var out strings.Builder
	for _, metric := range []string{"git-status", "package-scan"} {
		for index := 1; index <= count; index++ {
			sides := []string{"candidate", "control"}
			if index%2 == 0 {
				sides[0], sides[1] = sides[1], sides[0]
			}
			for _, side := range sides {
				fmt.Fprintf(&out, "%s\t%d\t%s\t%.6f\n", metric, index, side, values[metric][side])
			}
		}
	}
	return []byte(out.String())
}

func fixtureDigest(path string, data []byte) map[string]any {
	sum := sha256.Sum256(data)
	return map[string]any{"path": path, "sha256": hex.EncodeToString(sum[:])}
}

func fixtureJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func readSharedWorkspaceDecisionFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "dist", "workspace-research", "035", "decision.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func semanticProductArtifact(t *testing.T, validator, commit string, binding productevidence.RuntimeBinding) []byte {
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
			"lastSessionPreservedVm", "explicitStopStoppedVm", "realPTYInitialSize", "realPTYResize",
			"fullscreenFixture", "interruptExitExact", "daemonCrashClientsUnblocked",
			"daemonCrashTerminalRestored", "daemonCrashTargetsReaped", "restartStaleOwnerFailedClosed",
			"explicitRecovery", "postRecoveryRun",
		} {
			checks[name] = true
		}
		value = map[string]any{
			"schema": "hideout.concurrent-sessions-gate2/v1", "status": "passed",
			"generatedAt": "2026-07-16T12:00:00Z", "commit": commit, "dirty": false,
			"backend": "lima", "host": "macos-arm64", "metrics": map[string]any{"ownerReconcileMs": 125}, "checks": checks,
			"artifacts": map[string]any{"sessionPTYEvidenceSHA256": strings.Repeat("d", 64)},
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
	case productevidence.ArtifactValidatorLifecycleLocalV1:
		checks := map[string]bool{}
		for _, name := range []string{
			"catalogValidation", "cleanupBeforeRelease", "daemonSingleWriter", "evidenceRedaction",
			"generationFencing", "providerRegistration", "reconciliationReadiness", "reconciliationRetry",
			"schemaDriftGuard", "shutdownBounded", "statusSurfaceParity", "stopObservationAuthority",
		} {
			checks[name] = true
		}
		value = map[string]any{
			"schema": "hideout.lifecycle-local-evidence/v1", "status": "passed",
			"generatedAt": "2026-07-17T00:00:00Z", "commit": commit, "dirty": false, "checks": checks,
		}
	case productevidence.ArtifactValidatorLifecycleModelV1:
		invariants := map[string]bool{}
		for _, name := range lifecycle.RequiredModelInvariants() {
			invariants[name] = true
		}
		value = map[string]any{
			"schema": "hideout.lifecycle-model-evidence/v1", "status": "passed",
			"generatedAt": "2026-07-17T00:00:00Z", "commit": commit, "dirty": false,
			"exhaustiveSequences": 180, "exploredTransitions": 1848, "scenarioCount": 249,
			"persistedReplaySeeds": 32, "concurrentReplaySeeds": 32,
			"stepsPerPersistedReplay": 24, "concurrentWaitBoundMs": 2000,
			"coveredEvents": lifecycle.RequiredModelEvents(), "invariants": invariants,
			"journalValidAfterEachStep": true, "corruptionRejected": true,
			"stopsWithLiveClient": 0, "duplicateGenerationStops": 0,
		}
	case productevidence.ArtifactValidatorLifecycleRealV1:
		checks := map[string]bool{}
		for _, name := range []string{
			"attachStopRaceSafe", "auditEvidenceRetained", "automaticStopNonDestructive", "bootIdentityObserved",
			"bridgePinsEnvironment", "exactObservedStop", "explicitStaleRecovery", "finalSessionStops",
			"guestDiskRetained", "hostHandoffIndependent", "newBootGenerationObserved", "profileCacheRetained",
			"reconciliationRetry", "restartFreshGraceAtMostOnce", "restartNoInheritedAuthority",
			"retainedOverlayPreserved", "runBridgeClosed", "siblingSessionPreserved",
			"slowProbeDoesNotBlockStatus", "stopUnknownBlocksAttach",
		} {
			checks[name] = true
		}
		value = map[string]any{
			"schema": "hideout.lifecycle-real-gate2/v1", "status": "passed",
			"generatedAt": "2026-07-17T00:00:00Z", "commit": commit, "dirty": false,
			"backend": "lima", "hostOS": "darwin", "hostArch": "arm64",
			"metrics": map[string]any{
				"attachStopRaces": 100, "finalStopMs": 16000, "statusReadyMs": 100,
				"reconciliationRetryMs": 100, "shutdownMs": 100,
			},
			"checks":    checks,
			"nonClaims": map[string]any{"guestRootContainment": "not-claimed", "hostAppTermination": "not-owned"},
		}
	case productevidence.ArtifactValidatorLifecyclePerformanceV1:
		candidate := make([]float64, 30)
		baseline := make([]float64, 30)
		for index := range candidate {
			candidate[index], baseline[index] = 100, 100
		}
		value = map[string]any{
			"schema": "hideout.lifecycle-performance/v1", "status": "passed", "generatedAt": "2026-07-17T00:00:00Z",
			"candidate": map[string]any{"commit": commit, "dirty": false},
			"baseline":  map[string]any{"commit": strings.Repeat("b", 40), "dirty": false},
			"host":      map[string]any{"os": "darwin", "arch": "arm64"},
			"runtime": map[string]any{
				"family": binding.Family, "revision": binding.Revision, "artifactSHA256": binding.ArtifactSHA256,
				"buildCommit": binding.BuildCommit, "buildDirty": false,
			},
			"methodology": map[string]any{
				"command": "hideout run -- git status --short", "samples": 30, "warmups": 3,
				"fixtureSHA256": strings.Repeat("c", 64), "sampleOrder": "paired-alternating-ab-ba",
			},
			"candidateSamplesMs": candidate, "baselineSamplesMs": baseline,
			"candidateMedianMs": 100, "baselineMedianMs": 100, "observedDeltaMs": 0, "allowedDeltaMs": 10,
		}
	case productevidence.ArtifactValidatorWorkspaceExecutableV1:
		checks := map[string]bool{}
		for _, name := range []string{
			"checkoutWriteVisible", "directBinary", "directScript", "disjointIsolation",
			"escapingSymlinkRejected", "incompatibleFormatFailurePreserved", "laterSessionVisible",
			"localLauncher", "missingInterpreterFailurePreserved", "noHostFallback",
			"noWorkspaceCopy", "permissionFailurePreserved", "sharedModeObserved",
		} {
			checks[name] = true
		}
		value = map[string]any{
			"schema": "hideout.workspace-executable-gate2/v1", "status": "passed",
			"commit": commit, "dirty": false, "backend": "lima",
			"hostOS": "darwin", "hostArch": "arm64", "guestArch": "aarch64",
			"workspaceMechanism": "workspace-portal", "checks": checks,
			"samples": 30, "warmFirstOutputP95Ms": 800, "medianRegressionRatio": 1.02,
			"nonClaims": map[string]any{"staticVirtiofs": "not-claimed"},
		}
	case productevidence.ArtifactValidatorDisposableRecoveryV1:
		checks := map[string]bool{}
		for _, name := range []string{
			"boundedWorkers", "crashAfterBackendCleanup", "crashAfterIntent",
			"crashAfterJournalRemoval", "crashAfterStableAbsence", "ephemeralIdentity",
			"exactInstance", "gatewayCleared", "historicalJournalRefused",
			"identityMismatchRefused", "journalCleared", "liveOwnerRefused",
			"nameOnlyRefused", "nonDisposableRefused", "ordinaryFinalizer",
			"recordCleared", "runtimeCleared", "shutdownInterrupted",
			"stableAbsenceTwice", "startupStatusAvailable", "statusOnlyRefused",
			"targetFailure", "unprovableOwnerRefused", "zeroUnauthorizedCleanupCalls",
		} {
			checks[name] = true
		}
		value = map[string]any{
			"schema": "hideout.disposable-recovery-gate2/v1", "status": "passed",
			"commit": commit, "dirty": false, "backend": "lima",
			"hostOS": "darwin", "hostArch": "arm64", "guestArch": "aarch64",
			"checks": checks,
			"samples": map[string]any{
				"ordinaryRuns": 30, "crashSchedules": 100, "restartCheckpoints": 4,
			},
			"timing": map[string]any{
				"startupStatusP95Ms": 100, "recoveryP95Ms": 500, "recoveryTimeouts": 0,
			},
			"destructiveCalls": map[string]any{"authorized": 34, "unauthorized": 0},
			"residue": map[string]any{
				"environmentRecords": 0, "lifecycleJournals": 0, "backendInstances": 0,
				"gateways": 0, "runtimeReceipts": 0, "ownerRecords": 0,
			},
			"nonClaims": map[string]any{
				"historicalJournalOnly": "not-auto-recovered", "ordinaryOrphans": "report-only",
			},
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
