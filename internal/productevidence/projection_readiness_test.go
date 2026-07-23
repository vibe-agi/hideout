package productevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestProjectionReadinessValidatorAcceptsDerivedExactCandidate(t *testing.T) {
	fixture := newProjectionReadinessFixture(t, false)
	if err := fixture.validate(false); err != nil {
		t.Fatal(err)
	}
}

func TestProjectionPrivacyValidatorRequiresMatchingPassedGate3(t *testing.T) {
	fixture := newProjectionReadinessFixture(t, true)
	if err := fixture.validate(true); err != nil {
		t.Fatal(err)
	}
	var privacy projectionPrivacyEvidence
	mustDecodeProjectionFixture(t, fixture.artifacts["artifacts/projection-privacy-gate3.json"], &privacy)
	privacy.Checks["dnsMediated"] = false
	fixture.artifacts["artifacts/projection-privacy-gate3.json"] = mustProjectionJSON(t, privacy)
	fixture.refresh(t)
	if err := fixture.validate(true); err == nil || !strings.Contains(err.Error(), "dnsMediated") {
		t.Fatalf("false privacy check error=%v", err)
	}

	unpromoted := newProjectionReadinessFixture(t, false)
	if err := unpromoted.validate(true); err == nil || !strings.Contains(err.Error(), "not promoted") {
		t.Fatalf("unpromoted privacy error=%v", err)
	}
}

func TestRetainedProjectionReadinessEvidencePassesProductionEvaluator(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("HIDEOUT_043_EVIDENCE_DIR"))
	if root == "" {
		t.Skip("set HIDEOUT_043_EVIDENCE_DIR to validate retained real Gate 2 evidence")
	}
	manifest, err := ReadFile(filepath.Join(root, "product-hardening-evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.PackageIdentity == nil {
		t.Fatal("retained 043 evidence has no package identity")
	}
	required := map[string]bool{
		Proof030RealGate2CodeOpen:     true,
		Proof030RealGate2TrustedGrant: true,
		Proof032RealGate2External:     true,
		Proof039RealPersistentGrant:   true,
		Proof043RealReadiness:         true,
	}
	var runtime *RuntimeBinding
	seen := map[string]bool{}
	for _, proof := range manifest.Proofs {
		if !required[proof.ProofID] {
			continue
		}
		seen[proof.ProofID] = true
		if proof.Runtime == nil {
			t.Fatalf("real proof %s has no runtime binding", proof.ProofID)
		}
		if runtime == nil {
			copy := *proof.Runtime
			runtime = &copy
		} else if *runtime != *proof.Runtime {
			t.Fatalf("projection proofs carry different runtime bindings: %+v != %+v", *runtime, *proof.Runtime)
		}
	}
	if len(seen) != len(required) || runtime == nil {
		t.Fatalf("retained projection proof inventory=%v", seen)
	}
	requirements := make([]ProofRequirement, 0, len(required))
	for _, requirement := range ProductHardeningRequirements() {
		if required[requirement.ProofID] {
			requirements = append(requirements, requirement)
		}
	}
	expectedRuntime := RuntimeExpectation{
		Family: runtime.Family, Revision: runtime.Revision,
		ArtifactSHA256: runtime.ArtifactSHA256,
		HostOS:         runtime.HostOS, HostArch: runtime.HostArch, GuestArch: runtime.GuestArch,
		BuildCommit: runtime.BuildCommit, RequireClean: true,
	}
	report, err := EvaluateManifest(manifest, EvaluationOptions{
		Requirements: requirements, Target: RequiredForReleaseCandidate,
		ExpectedCommit: manifest.Commit, ExpectedPackage: manifest.PackageIdentity,
		ArtifactRoot: root, ExpectedRuntime: &expectedRuntime,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range report.Results {
		if result.Status != EvalSatisfied {
			t.Fatalf("retained projection proof failed production evaluation: %+v", result)
		}
	}
}

func TestProjectionReadinessValidatorRejectsFalseGreenArtifacts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *projectionReadinessFixture)
	}{
		{
			name: "dirty source",
			mutate: func(t *testing.T, f *projectionReadinessFixture) {
				f.evidence.Dirty = true
				f.refresh(t)
			},
		},
		{
			name: "wrong source package digest",
			mutate: func(_ *testing.T, f *projectionReadinessFixture) {
				f.expectedPackage.ArtifactSHA256 = strings.Repeat("e", 64)
			},
		},
		{
			name: "wrong runtime artifact build",
			mutate: func(_ *testing.T, f *projectionReadinessFixture) {
				f.expectedRuntime.ArtifactSHA256 = strings.Repeat("e", 64)
			},
		},
		{
			name: "missing check",
			mutate: func(t *testing.T, f *projectionReadinessFixture) {
				delete(f.evidence.Checks, "readiness.manifest")
				f.refresh(t)
			},
		},
		{
			name: "forged marker only",
			mutate: func(t *testing.T, f *projectionReadinessFixture) {
				f.evidence.Checks["forged.markerOnly"] = true
				f.refresh(t)
			},
		},
		{
			name: "false check",
			mutate: func(t *testing.T, f *projectionReadinessFixture) {
				f.evidence.Checks["refusal.zeroTarget"] = false
				f.refresh(t)
			},
		},
		{
			name: "unknown json field",
			mutate: func(t *testing.T, f *projectionReadinessFixture) {
				var raw map[string]any
				mustDecodeProjectionFixture(t, f.artifacts["artifacts/projection-readiness.json"], &raw)
				raw["forgedPassed"] = true
				f.artifacts["artifacts/projection-readiness.json"] = mustProjectionJSON(t, raw)
				f.rehashRefs()
			},
		},
		{
			name: "nine fresh samples",
			mutate: func(t *testing.T, f *projectionReadinessFixture) {
				f.artifacts["artifacts/readiness-samples.tsv"] = projectionSampleFixture(9, 30, 100, 50, 100)
				f.refresh(t)
			},
		},
		{
			name: "twenty nine warm samples",
			mutate: func(t *testing.T, f *projectionReadinessFixture) {
				f.artifacts["artifacts/readiness-samples.tsv"] = projectionSampleFixture(10, 29, 100, 50, 100)
				f.refresh(t)
			},
		},
		{
			name: "edited p95 summary",
			mutate: func(t *testing.T, f *projectionReadinessFixture) {
				f.evidence.Readiness.FreshP95MS++
				f.refresh(t)
			},
		},
		{
			name: "p95 above threshold",
			mutate: func(t *testing.T, f *projectionReadinessFixture) {
				f.artifacts["artifacts/readiness-samples.tsv"] = projectionSampleFixture(10, 30, 2500, 2500, 100)
				f.evidence.Readiness.FreshP95MS = 2509
				f.evidence.Readiness.WarmP95MS = 2528
				f.refresh(t)
			},
		},
		{
			name: "nonzero target retry",
			mutate: func(t *testing.T, f *projectionReadinessFixture) {
				data := string(f.artifacts["artifacts/readiness-samples.tsv"])
				data = strings.Replace(data, "\t0\t0\t0\t0\t0\t0\n", "\t0\t1\t0\t0\t0\t0\n", 1)
				f.artifacts["artifacts/readiness-samples.tsv"] = []byte(data)
				f.refresh(t)
			},
		},
		{
			name: "missing external flow",
			mutate: func(t *testing.T, f *projectionReadinessFixture) {
				var flows projectionFlowEvidence
				mustDecodeProjectionFixture(t, f.artifacts["artifacts/projection-flows.json"], &flows)
				delete(flows.Checks, "external032.oldSessionImmutable")
				f.artifacts["artifacts/projection-flows.json"] = mustProjectionJSON(t, flows)
				f.refresh(t)
			},
		},
		{
			name: "missing persistent grant flow",
			mutate: func(t *testing.T, f *projectionReadinessFixture) {
				var flows projectionFlowEvidence
				mustDecodeProjectionFixture(t, f.artifacts["artifacts/projection-flows.json"], &flows)
				delete(flows.Checks, "persistent039.separateRunReuse")
				f.artifacts["artifacts/projection-flows.json"] = mustProjectionJSON(t, flows)
				f.refresh(t)
			},
		},
		{
			name: "altered artifact bytes",
			mutate: func(_ *testing.T, f *projectionReadinessFixture) {
				f.artifacts["artifacts/projection-flows.json"] = append(
					f.artifacts["artifacts/projection-flows.json"], '\n',
				)
			},
		},
		{
			name: "missing artifact",
			mutate: func(_ *testing.T, f *projectionReadinessFixture) {
				delete(f.artifacts, "artifacts/runtime-manifest.json")
			},
		},
		{
			name: "not run marker",
			mutate: func(t *testing.T, f *projectionReadinessFixture) {
				f.evidence.Status = "not-run"
				f.refresh(t)
			},
		},
		{
			name: "unredacted control token",
			mutate: func(t *testing.T, f *projectionReadinessFixture) {
				f.evidence.Runtime.Revision = "cap_0123456789abcdef0123456789abcdef"
				f.expectedRuntime.Revision = f.evidence.Runtime.Revision
				var runtimeManifest projectionRuntimeManifest
				mustDecodeProjectionFixture(t, f.artifacts["artifacts/runtime-manifest.json"], &runtimeManifest)
				runtimeManifest.Runtime.Revision = f.evidence.Runtime.Revision
				f.artifacts["artifacts/runtime-manifest.json"] = mustProjectionJSON(t, runtimeManifest)
				f.refresh(t)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newProjectionReadinessFixture(t, false)
			test.mutate(t, fixture)
			if err := fixture.validate(false); err == nil {
				t.Fatal("false-green projection evidence passed")
			}
		})
	}
}

type projectionReadinessFixture struct {
	evidence        projectionReadinessEvidence
	artifacts       map[string][]byte
	refs            []ArtifactRef
	expectedPackage PackageIdentity
	expectedRuntime RuntimeExpectation
}

func newProjectionReadinessFixture(t *testing.T, privacy bool) *projectionReadinessFixture {
	t.Helper()
	commit := strings.Repeat("a", 40)
	pkg := PackageIdentity{
		Name: "hideout", ProductVersion: "0.1.0-alpha.1", SourceCommit: commit,
		ArtifactSHA256: strings.Repeat("b", 64), HostOS: "darwin", HostArch: "arm64",
	}
	runtime := RuntimeBinding{
		Schema: RuntimeBindingSchema, Family: "ubuntu", Revision: "24.04",
		ArtifactSHA256: strings.Repeat("c", 64), EnvironmentID: "env_projection",
		HostOS: "darwin", HostArch: "arm64", GuestArch: "aarch64",
		BuildCommit: strings.Repeat("d", 40), BuildDirty: false,
	}
	var evidence projectionReadinessEvidence
	evidence.Schema = "hideout.projection-readiness-real-gate2/v1"
	evidence.Status = "passed"
	evidence.GeneratedAt = "2026-07-23T12:00:00Z"
	evidence.Commit = commit
	evidence.Package = pkg
	evidence.Runtime = runtime
	evidence.Platform.HostOS = "darwin"
	evidence.Platform.HostArch = "arm64"
	evidence.Platform.GuestArch = "aarch64"
	evidence.Platform.Backend = "lima"
	evidence.Platform.ApplicationIdentityClass = "bundle-id+designated-requirement"
	evidence.Methodology.MinimumFreshSamples = 10
	evidence.Methodology.MinimumWarmSamples = 30
	evidence.Methodology.MinimumConcurrentPairs = 1
	evidence.Methodology.P95Method = "nearest-rank"
	evidence.Methodology.ReadinessThresholdMS = 2000
	evidence.Methodology.CancellationThresholdMS = 2000
	evidence.Readiness.FreshSamples = 10
	evidence.Readiness.WarmSamples = 30
	evidence.Readiness.ConcurrentPairs = 1
	evidence.Readiness.FreshP95MS = 109
	evidence.Readiness.WarmP95MS = 78
	evidence.Readiness.CancellationMaxMS = 100
	evidence.Checks = trueProjectionChecks(requiredProjectionReadinessChecks)
	evidence.Privacy.Status = "not-promoted"
	evidence.NonClaims = append([]string(nil), requiredProjectionNonClaims...)
	flows := projectionFlowEvidence{
		Schema: "hideout.projection-flows-real-gate2/v1", Status: "passed",
		Checks: trueProjectionChecks(requiredProjectionFlowChecks),
	}
	packageManifest := projectionPackageManifest{
		Schema: "hideout.projection-package-manifest/v1", Package: pkg,
	}
	runtimeManifest := projectionRuntimeManifest{
		Schema: "hideout.projection-runtime-manifest/v1", Runtime: runtime,
	}
	fixture := &projectionReadinessFixture{
		evidence: evidence,
		artifacts: map[string][]byte{
			"artifacts/readiness-samples.tsv": projectionSampleFixture(10, 30, 100, 50, 100),
			"artifacts/projection-flows.json": mustProjectionJSON(t, flows),
			"artifacts/package-manifest.json": mustProjectionJSON(t, packageManifest),
			"artifacts/runtime-manifest.json": mustProjectionJSON(t, runtimeManifest),
		},
		expectedPackage: pkg,
		expectedRuntime: RuntimeExpectation{
			Family: runtime.Family, Revision: runtime.Revision,
			ArtifactSHA256: runtime.ArtifactSHA256, HostOS: runtime.HostOS,
			HostArch: runtime.HostArch, GuestArch: runtime.GuestArch,
			BuildCommit: runtime.BuildCommit, RequireClean: true,
		},
	}
	if privacy {
		privacyEvidence := projectionPrivacyEvidence{
			Schema: "hideout.projection-privacy-real-gate3/v1", Status: "passed",
			GeneratedAt: evidence.GeneratedAt, Commit: commit, Package: pkg, Runtime: runtime,
			Checks: trueProjectionChecks(requiredProjectionPrivacyChecks),
		}
		fixture.evidence.Privacy.Status = "promoted"
		fixture.artifacts["artifacts/projection-privacy-gate3.json"] = mustProjectionJSON(t, privacyEvidence)
	}
	fixture.refresh(t)
	return fixture
}

func (f *projectionReadinessFixture) refresh(t *testing.T) {
	t.Helper()
	f.evidence.Artifacts.Samples = projectionFixtureDigest(
		"artifacts/readiness-samples.tsv", f.artifacts["artifacts/readiness-samples.tsv"],
	)
	f.evidence.Artifacts.Flows = projectionFixtureDigest(
		"artifacts/projection-flows.json", f.artifacts["artifacts/projection-flows.json"],
	)
	f.evidence.Artifacts.PackageManifest = projectionFixtureDigest(
		"artifacts/package-manifest.json", f.artifacts["artifacts/package-manifest.json"],
	)
	f.evidence.Artifacts.RuntimeManifest = projectionFixtureDigest(
		"artifacts/runtime-manifest.json", f.artifacts["artifacts/runtime-manifest.json"],
	)
	if f.evidence.Privacy.Status == "promoted" {
		descriptor := projectionFixtureDigest(
			"artifacts/projection-privacy-gate3.json",
			f.artifacts["artifacts/projection-privacy-gate3.json"],
		)
		f.evidence.Privacy.Artifact = &descriptor
	} else {
		f.evidence.Privacy.Artifact = nil
	}
	f.artifacts["artifacts/projection-readiness.json"] = mustProjectionJSON(t, f.evidence)
	f.refs = f.refs[:0]
	paths := make([]string, 0, len(f.artifacts))
	for path := range f.artifacts {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		kind := "manifest"
		if strings.HasSuffix(path, ".tsv") {
			kind = "log"
		}
		f.refs = append(f.refs, ArtifactRef{
			Kind: kind, Path: path, SHA256: projectionFixtureSHA256(f.artifacts[path]),
			RedactionStatus: RedactionPassed,
		})
	}
}

func (f *projectionReadinessFixture) rehashRefs() {
	for index := range f.refs {
		if data, ok := f.artifacts[f.refs[index].Path]; ok {
			f.refs[index].SHA256 = projectionFixtureSHA256(data)
		}
	}
}

func (f *projectionReadinessFixture) validate(requirePrivacy bool) error {
	return validateProjectionReadinessArtifact(
		f.refs, f.artifacts, f.evidence.Commit,
		&f.expectedPackage, &f.expectedRuntime, requirePrivacy,
	)
}

func projectionSampleFixture(fresh, warm int, freshBase, warmBase, cancellation int64) []byte {
	var builder strings.Builder
	builder.WriteString("lane\tindex\tduration_ms\tfirst_target\toperator_retries\ttarget_retries\tfallbacks\ttimeouts\tunauthorized_host_effects\tcross_session_access\n")
	for index := 1; index <= fresh; index++ {
		fmt.Fprintf(&builder, "fresh\t%d\t%d\tprojected\t0\t0\t0\t0\t0\t0\n", index, freshBase+int64(index-1))
	}
	for index := 1; index <= warm; index++ {
		fmt.Fprintf(&builder, "warm\t%d\t%d\tprojected\t0\t0\t0\t0\t0\t0\n", index, warmBase+int64(index-1))
	}
	fmt.Fprintf(&builder, "cancellation\t1\t%d\tnone\t0\t0\t0\t0\t0\t0\n", cancellation)
	return []byte(builder.String())
}

func trueProjectionChecks(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

func projectionFixtureDigest(path string, data []byte) projectionArtifactDigest {
	return projectionArtifactDigest{Path: path, SHA256: projectionFixtureSHA256(data)}
}

func projectionFixtureSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func mustProjectionJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustDecodeProjectionFixture(t *testing.T, data []byte, value any) {
	t.Helper()
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatal(err)
	}
}
