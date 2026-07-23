package productevidence

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	Feature039 = "039-trusted-host-app-grant"
	Feature043 = "043-projection-readiness-proof"

	Proof039RealPersistentGrant = "039.trusted-host-app-grant.real-gate2.persistent"
	Proof039RealGate2NotRun     = "039.trusted-host-app-grant.real-gate2.not-run"

	Proof043Gate0Mechanics    = "043.projection-readiness.gate0.mechanics"
	Proof043RealReadiness     = "043.projection-readiness.real-gate2.readiness"
	Proof043RealPrivacy       = "043.projection-readiness.real-gate3.privacy"
	Proof043RealGate2NotRun   = "043.projection-readiness.real-gate2.not-run"
	Proof043DocsClaimBoundary = "043.projection-readiness.docs.claim-boundary"
)

var requiredProjectionReadinessChecks = []string{
	"readiness.catalog",
	"readiness.manifest",
	"readiness.dispatcher",
	"readiness.entryProperties",
	"readiness.exactSessionView",
	"readiness.readyCommitProof",
	"refusal.staleCatalog",
	"refusal.identityDrift",
	"refusal.bootDrift",
	"refusal.timeout",
	"refusal.cancellation",
	"refusal.symlink",
	"refusal.type",
	"refusal.digest",
	"refusal.zeroTarget",
	"refusal.zeroEffect",
	"refusal.zeroFallback",
	"concurrency.disjointCatalogs",
	"concurrency.ordinaryCommandCompatibility",
	"redaction.applicationIdentityClass",
	"redaction.publicArtifactScan",
}

var requiredProjectionFlowChecks = []string{
	"projection030.safeHostEffect",
	"projection030.taskSuppression",
	"projection030.aliasChannels",
	"projection030.preservePositiveControl",
	"projection030.runBoundGrant",
	"projection030.runBoundRevoke",
	"external032.oldSessionImmutable",
	"external032.workspaceResource",
	"external032.authorizedHostFS",
	"external032.unsafeIdentityDenied",
	"external032.disableNoFallback",
	"external032.revokeNoFallback",
	"persistent039.initialRefusal",
	"persistent039.hostGrant",
	"persistent039.separateRunReuse",
	"persistent039.revoke",
	"persistent039.laterRefusal",
}

var requiredProjectionPrivacyChecks = []string{
	"guestWorkspaceAlias",
	"proxyEnvAbsent",
	"dnsMediated",
	"connectedSubnetBlocked",
	"httpsRequest",
	"privilegeSeparation",
	"publicEvidenceRedacted",
}

var requiredProjectionNonClaims = []string{
	"guest-root-out-of-scope",
	"native-is-not-real-evidence",
	"readiness-is-not-authority",
}

type projectionArtifactDigest struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type projectionReadinessEvidence struct {
	Schema      string          `json:"schema"`
	Status      string          `json:"status"`
	GeneratedAt string          `json:"generatedAt"`
	Commit      string          `json:"commit"`
	Dirty       bool            `json:"dirty"`
	Package     PackageIdentity `json:"packageIdentity"`
	Runtime     RuntimeBinding  `json:"runtime"`
	Platform    struct {
		HostOS                   string `json:"hostOS"`
		HostArch                 string `json:"hostArch"`
		GuestArch                string `json:"guestArch"`
		Backend                  string `json:"backend"`
		ApplicationIdentityClass string `json:"applicationIdentityClass"`
	} `json:"platform"`
	Methodology struct {
		MinimumFreshSamples     int    `json:"minimumFreshSamples"`
		MinimumWarmSamples      int    `json:"minimumWarmSamples"`
		MinimumConcurrentPairs  int    `json:"minimumConcurrentPairs"`
		P95Method               string `json:"p95Method"`
		ReadinessThresholdMS    int64  `json:"readinessThresholdMs"`
		CancellationThresholdMS int64  `json:"cancellationThresholdMs"`
	} `json:"methodology"`
	Readiness struct {
		FreshSamples            int   `json:"freshSamples"`
		WarmSamples             int   `json:"warmSamples"`
		ConcurrentPairs         int   `json:"concurrentPairs"`
		FreshP95MS              int64 `json:"freshP95Ms"`
		WarmP95MS               int64 `json:"warmP95Ms"`
		CancellationMaxMS       int64 `json:"cancellationMaxMs"`
		OperatorRetries         int   `json:"operatorRetries"`
		TargetRetries           int   `json:"targetRetries"`
		Fallbacks               int   `json:"fallbacks"`
		Timeouts                int   `json:"timeouts"`
		UnauthorizedHostEffects int   `json:"unauthorizedHostEffects"`
		CrossSessionAccess      int   `json:"crossSessionAccess"`
	} `json:"readiness"`
	Checks    map[string]bool `json:"checks"`
	Artifacts struct {
		Samples         projectionArtifactDigest `json:"samples"`
		Flows           projectionArtifactDigest `json:"flows"`
		PackageManifest projectionArtifactDigest `json:"packageManifest"`
		RuntimeManifest projectionArtifactDigest `json:"runtimeManifest"`
	} `json:"artifacts"`
	Privacy struct {
		Status   string                    `json:"status"`
		Artifact *projectionArtifactDigest `json:"artifact,omitempty"`
	} `json:"privacy"`
	NonClaims []string `json:"nonClaims"`
}

type projectionFlowEvidence struct {
	Schema string          `json:"schema"`
	Status string          `json:"status"`
	Checks map[string]bool `json:"checks"`
}

type projectionPrivacyEvidence struct {
	Schema      string          `json:"schema"`
	Status      string          `json:"status"`
	GeneratedAt string          `json:"generatedAt"`
	Commit      string          `json:"commit"`
	Dirty       bool            `json:"dirty"`
	Package     PackageIdentity `json:"packageIdentity"`
	Runtime     RuntimeBinding  `json:"runtime"`
	Checks      map[string]bool `json:"checks"`
}

type projectionPackageManifest struct {
	Schema  string          `json:"schema"`
	Package PackageIdentity `json:"packageIdentity"`
}

type projectionRuntimeManifest struct {
	Schema  string         `json:"schema"`
	Runtime RuntimeBinding `json:"runtime"`
}

type projectionReadinessSample struct {
	Lane                    string
	Index                   int
	DurationMS              int64
	FirstTarget             string
	OperatorRetries         int
	TargetRetries           int
	Fallbacks               int
	Timeouts                int
	UnauthorizedHostEffects int
	CrossSessionAccess      int
}

func validateProjectionReadinessArtifact(
	refs []ArtifactRef,
	artifacts map[string][]byte,
	expectedCommit string,
	expectedPackage *PackageIdentity,
	expectedRuntime *RuntimeExpectation,
	requirePrivacy bool,
) error {
	data, ok := artifacts["artifacts/projection-readiness.json"]
	if !ok {
		return errors.New("projection readiness evidence is missing artifacts/projection-readiness.json")
	}
	var evidence projectionReadinessEvidence
	if err := decodeStrictEvidence(data, &evidence); err != nil {
		return fmt.Errorf("projection readiness evidence: %w", err)
	}
	if evidence.Schema != "hideout.projection-readiness-real-gate2/v1" ||
		evidence.Status != "passed" || evidence.Platform.HostOS != "darwin" ||
		evidence.Platform.HostArch != "arm64" || evidence.Platform.GuestArch != "aarch64" ||
		evidence.Platform.Backend != "lima" ||
		evidence.Platform.ApplicationIdentityClass != "bundle-id+designated-requirement" {
		return errors.New("projection readiness identity, platform, backend, or application identity class is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, evidence.GeneratedAt); err != nil {
		return errors.New("projection readiness generatedAt is invalid")
	}
	if !IsCanonicalCommit(evidence.Commit) || evidence.Dirty ||
		(expectedCommit != "" && evidence.Commit != expectedCommit) {
		return errors.New("projection readiness is not bound to the clean candidate commit")
	}
	if err := validateSharedWorkspacePackageAndRuntime(
		evidence.Package, evidence.Runtime, expectedCommit, expectedPackage, expectedRuntime,
	); err != nil {
		return fmt.Errorf("projection readiness candidate identity: %w", err)
	}
	if evidence.Methodology.MinimumFreshSamples != 10 ||
		evidence.Methodology.MinimumWarmSamples != 30 ||
		evidence.Methodology.MinimumConcurrentPairs != 1 ||
		evidence.Methodology.P95Method != "nearest-rank" ||
		evidence.Methodology.ReadinessThresholdMS != 2000 ||
		evidence.Methodology.CancellationThresholdMS != 2000 {
		return errors.New("projection readiness methodology drifted")
	}
	if err := validateExactBooleanChecks(
		"projection readiness", evidence.Checks, requiredProjectionReadinessChecks,
	); err != nil {
		return err
	}
	if err := validateProjectionNonClaims(evidence.NonClaims); err != nil {
		return err
	}
	wantPaths := map[string]string{
		"samples":         "artifacts/readiness-samples.tsv",
		"flows":           "artifacts/projection-flows.json",
		"packageManifest": "artifacts/package-manifest.json",
		"runtimeManifest": "artifacts/runtime-manifest.json",
	}
	descriptors := map[string]projectionArtifactDigest{
		"samples": evidence.Artifacts.Samples, "flows": evidence.Artifacts.Flows,
		"packageManifest": evidence.Artifacts.PackageManifest,
		"runtimeManifest": evidence.Artifacts.RuntimeManifest,
	}
	if err := validateProjectionDigestMap(descriptors, wantPaths); err != nil {
		return err
	}
	kinds := map[string]string{
		"artifacts/projection-readiness.json": "manifest",
		"artifacts/readiness-samples.tsv":     "log",
		"artifacts/projection-flows.json":     "manifest",
		"artifacts/package-manifest.json":     "manifest",
		"artifacts/runtime-manifest.json":     "manifest",
	}
	switch evidence.Privacy.Status {
	case "not-promoted":
		if evidence.Privacy.Artifact != nil {
			return errors.New("unpromoted projection privacy cannot declare an artifact")
		}
		if requirePrivacy {
			return errors.New("projection privacy is not promoted")
		}
	case "promoted":
		if evidence.Privacy.Artifact == nil ||
			evidence.Privacy.Artifact.Path != "artifacts/projection-privacy-gate3.json" ||
			!isLowerHexSHA256(evidence.Privacy.Artifact.SHA256) {
			return errors.New("promoted projection privacy artifact identity is invalid")
		}
		descriptors["privacy"] = *evidence.Privacy.Artifact
		kinds[evidence.Privacy.Artifact.Path] = "manifest"
	default:
		return errors.New("projection privacy status is invalid")
	}
	if err := validateProjectionArtifactInventory(refs, artifacts, kinds, descriptors); err != nil {
		return err
	}
	samples, err := parseProjectionReadinessSamples(artifacts[evidence.Artifacts.Samples.Path])
	if err != nil {
		return err
	}
	if err := validateProjectionReadinessSamples(evidence, samples); err != nil {
		return err
	}
	var flows projectionFlowEvidence
	if err := decodeStrictEvidence(artifacts[evidence.Artifacts.Flows.Path], &flows); err != nil {
		return fmt.Errorf("projection flow evidence: %w", err)
	}
	if flows.Schema != "hideout.projection-flows-real-gate2/v1" || flows.Status != "passed" {
		return errors.New("projection flow evidence identity or status is invalid")
	}
	if err := validateExactBooleanChecks("projection flows", flows.Checks, requiredProjectionFlowChecks); err != nil {
		return err
	}
	if err := validateProjectionPackageManifest(
		artifacts[evidence.Artifacts.PackageManifest.Path], evidence.Package,
	); err != nil {
		return err
	}
	if err := validateProjectionRuntimeManifest(
		artifacts[evidence.Artifacts.RuntimeManifest.Path], evidence.Runtime,
	); err != nil {
		return err
	}
	if evidence.Privacy.Status == "promoted" {
		if err := validateProjectionPrivacy(
			artifacts[evidence.Privacy.Artifact.Path], evidence, expectedCommit, expectedPackage, expectedRuntime,
		); err != nil {
			return err
		}
	}
	for path, artifact := range artifacts {
		if ContainsControlPlaneBytes(artifact) {
			return fmt.Errorf("projection readiness artifact %q contains unredacted control-plane material", path)
		}
	}
	return nil
}

func validateProjectionDigestMap(got map[string]projectionArtifactDigest, want map[string]string) error {
	if len(got) != len(want) {
		return errors.New("projection readiness artifact descriptor inventory drifted")
	}
	for key, path := range want {
		value, ok := got[key]
		if !ok || value.Path != path || !isLowerHexSHA256(value.SHA256) {
			return fmt.Errorf("projection readiness artifact descriptor %q is invalid", key)
		}
	}
	return nil
}

func validateProjectionArtifactInventory(
	refs []ArtifactRef,
	artifacts map[string][]byte,
	kinds map[string]string,
	descriptors map[string]projectionArtifactDigest,
) error {
	if len(refs) != len(kinds) || len(artifacts) != len(kinds) {
		return errors.New("projection readiness proof artifact inventory drifted")
	}
	declared := make(map[string]string, len(descriptors))
	for _, descriptor := range descriptors {
		if _, duplicate := declared[descriptor.Path]; duplicate {
			return errors.New("projection readiness repeats an artifact path")
		}
		declared[descriptor.Path] = descriptor.SHA256
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		kind, ok := kinds[ref.Path]
		if !ok || ref.Kind != kind || seen[ref.Path] ||
			ref.RedactionStatus != RedactionPassed || !isLowerHexSHA256(ref.SHA256) {
			return fmt.Errorf("projection readiness artifact ref %q is invalid", ref.Path)
		}
		seen[ref.Path] = true
		data, ok := artifacts[ref.Path]
		if !ok {
			return fmt.Errorf("projection readiness artifact %q bytes are missing", ref.Path)
		}
		sum := sha256.Sum256(data)
		actual := hex.EncodeToString(sum[:])
		if ref.SHA256 != actual {
			return fmt.Errorf("projection readiness artifact %q digest mismatch", ref.Path)
		}
		if want := declared[ref.Path]; want != "" && want != actual {
			return fmt.Errorf("projection readiness semantic artifact %q digest mismatch", ref.Path)
		}
	}
	return nil
}

func parseProjectionReadinessSamples(data []byte) ([]projectionReadinessSample, error) {
	const header = "lane\tindex\tduration_ms\tfirst_target\toperator_retries\ttarget_retries\tfallbacks\ttimeouts\tunauthorized_host_effects\tcross_session_access"
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	if !scanner.Scan() || scanner.Text() != header {
		return nil, errors.New("projection readiness samples have an invalid header")
	}
	var samples []projectionReadinessSample
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 10 {
			return nil, errors.New("projection readiness sample has an invalid record shape")
		}
		values := make([]int64, 8)
		for index, field := range []string{
			fields[1], fields[2], fields[4], fields[5], fields[6], fields[7], fields[8], fields[9],
		} {
			value, err := strconv.ParseInt(field, 10, 64)
			if err != nil || value < 0 {
				return nil, errors.New("projection readiness sample contains an invalid integer")
			}
			values[index] = value
		}
		if values[0] == 0 || values[0] > 1_000_000 {
			return nil, errors.New("projection readiness sample index is invalid")
		}
		samples = append(samples, projectionReadinessSample{
			Lane: fields[0], Index: int(values[0]), DurationMS: values[1], FirstTarget: fields[3],
			OperatorRetries: int(values[2]), TargetRetries: int(values[3]),
			Fallbacks: int(values[4]), Timeouts: int(values[5]),
			UnauthorizedHostEffects: int(values[6]), CrossSessionAccess: int(values[7]),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read projection readiness samples: %w", err)
	}
	if len(samples) == 0 {
		return nil, errors.New("projection readiness samples are empty")
	}
	return samples, nil
}

func validateProjectionReadinessSamples(evidence projectionReadinessEvidence, samples []projectionReadinessSample) error {
	byLane := map[string][]projectionReadinessSample{}
	for _, sample := range samples {
		switch sample.Lane {
		case "fresh", "warm":
			if sample.FirstTarget != "projected" {
				return errors.New("projection readiness sample did not use the projected command as its first target")
			}
		case "cancellation":
			if sample.FirstTarget != "none" {
				return errors.New("projection cancellation sample started a target")
			}
		default:
			return fmt.Errorf("projection readiness sample has unsupported lane %q", sample.Lane)
		}
		if sample.OperatorRetries != 0 || sample.TargetRetries != 0 || sample.Fallbacks != 0 ||
			sample.Timeouts != 0 || sample.UnauthorizedHostEffects != 0 || sample.CrossSessionAccess != 0 {
			return errors.New("projection readiness sample contains a retry, fallback, timeout, effect, or cross-session access")
		}
		byLane[sample.Lane] = append(byLane[sample.Lane], sample)
	}
	if len(byLane["fresh"]) < evidence.Methodology.MinimumFreshSamples ||
		len(byLane["warm"]) < evidence.Methodology.MinimumWarmSamples ||
		len(byLane["cancellation"]) != 1 {
		return errors.New("projection readiness raw sample inventory is incomplete")
	}
	for lane, values := range byLane {
		sort.Slice(values, func(i, j int) bool { return values[i].Index < values[j].Index })
		for index, value := range values {
			if value.Index != index+1 {
				return fmt.Errorf("projection readiness %s sample indices are not exact and contiguous", lane)
			}
		}
		byLane[lane] = values
	}
	freshP95 := projectionNearestRankP95(byLane["fresh"])
	warmP95 := projectionNearestRankP95(byLane["warm"])
	cancelMax := byLane["cancellation"][0].DurationMS
	if evidence.Readiness.FreshSamples != len(byLane["fresh"]) ||
		evidence.Readiness.WarmSamples != len(byLane["warm"]) ||
		evidence.Readiness.ConcurrentPairs < evidence.Methodology.MinimumConcurrentPairs ||
		evidence.Readiness.FreshP95MS != freshP95 || evidence.Readiness.WarmP95MS != warmP95 ||
		evidence.Readiness.CancellationMaxMS != cancelMax {
		return errors.New("projection readiness summary is not derived from raw samples")
	}
	if freshP95 > evidence.Methodology.ReadinessThresholdMS ||
		warmP95 > evidence.Methodology.ReadinessThresholdMS ||
		cancelMax > evidence.Methodology.CancellationThresholdMS {
		return errors.New("projection readiness threshold did not pass when recomputed")
	}
	if evidence.Readiness.OperatorRetries != 0 || evidence.Readiness.TargetRetries != 0 ||
		evidence.Readiness.Fallbacks != 0 || evidence.Readiness.Timeouts != 0 ||
		evidence.Readiness.UnauthorizedHostEffects != 0 || evidence.Readiness.CrossSessionAccess != 0 {
		return errors.New("projection readiness summary contains a retry, fallback, timeout, effect, or cross-session access")
	}
	return nil
}

func projectionNearestRankP95(samples []projectionReadinessSample) int64 {
	values := make([]int64, len(samples))
	for index, sample := range samples {
		values[index] = sample.DurationMS
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	rank := int(math.Ceil(0.95 * float64(len(values))))
	if rank < 1 {
		rank = 1
	}
	return values[rank-1]
}

func validateProjectionPackageManifest(data []byte, expected PackageIdentity) error {
	var manifest projectionPackageManifest
	if err := decodeStrictEvidence(data, &manifest); err != nil {
		return fmt.Errorf("projection package manifest: %w", err)
	}
	if manifest.Schema != "hideout.projection-package-manifest/v1" ||
		packageStale(&manifest.Package, &expected) {
		return errors.New("projection package manifest does not match the candidate package")
	}
	return nil
}

func validateProjectionRuntimeManifest(data []byte, expected RuntimeBinding) error {
	var manifest projectionRuntimeManifest
	if err := decodeStrictEvidence(data, &manifest); err != nil {
		return fmt.Errorf("projection runtime manifest: %w", err)
	}
	if manifest.Schema != "hideout.projection-runtime-manifest/v1" ||
		!manifest.Runtime.SameArtifactBuild(expected) ||
		manifest.Runtime.EnvironmentID != expected.EnvironmentID {
		return errors.New("projection runtime manifest does not match the observed runtime")
	}
	return nil
}

func validateProjectionPrivacy(
	data []byte,
	parent projectionReadinessEvidence,
	expectedCommit string,
	expectedPackage *PackageIdentity,
	expectedRuntime *RuntimeExpectation,
) error {
	var evidence projectionPrivacyEvidence
	if err := decodeStrictEvidence(data, &evidence); err != nil {
		return fmt.Errorf("projection privacy evidence: %w", err)
	}
	if evidence.Schema != "hideout.projection-privacy-real-gate3/v1" || evidence.Status != "passed" ||
		evidence.Commit != parent.Commit || evidence.Dirty || evidence.GeneratedAt == "" ||
		packageStale(&evidence.Package, &parent.Package) ||
		!evidence.Runtime.SameArtifactBuild(parent.Runtime) ||
		evidence.Runtime.EnvironmentID != parent.Runtime.EnvironmentID {
		return errors.New("projection privacy identity does not match the readiness candidate")
	}
	if err := validateSharedWorkspacePackageAndRuntime(
		evidence.Package, evidence.Runtime, expectedCommit, expectedPackage, expectedRuntime,
	); err != nil {
		return fmt.Errorf("projection privacy candidate identity: %w", err)
	}
	return validateExactBooleanChecks("projection privacy", evidence.Checks, requiredProjectionPrivacyChecks)
}

func validateProjectionNonClaims(values []string) error {
	if len(values) != len(requiredProjectionNonClaims) {
		return errors.New("projection readiness non-claim inventory drifted")
	}
	got := append([]string(nil), values...)
	want := append([]string(nil), requiredProjectionNonClaims...)
	sort.Strings(got)
	sort.Strings(want)
	for index := range want {
		if got[index] != want[index] {
			return errors.New("projection readiness non-claim inventory drifted")
		}
	}
	return nil
}
