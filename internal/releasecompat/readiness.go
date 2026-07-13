package releasecompat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/productevidence"
	"github.com/vibe-agi/hideout/internal/recovery"
)

const (
	ReadinessSchema = "hideout.release-readiness/v1"
)

const maxGateEvidenceBytes int64 = 8 << 20

type ReadinessOptions struct {
	Mode                          string
	Commit                        string
	Gate2Evidence                 string
	Gate3Evidence                 string
	ProductEvidence               []string
	LocalPassed                   bool
	Now                           time.Time
	Runtime                       *productevidence.RuntimeExpectation
	Package                       *productevidence.PackageIdentity
	SigningObservationSHA256      string
	NotarizationObservationSHA256 string
}

type Readiness struct {
	Schema                        string                              `json:"schema"`
	GeneratedAt                   time.Time                           `json:"generatedAt"`
	Mode                          string                              `json:"mode"`
	EvidenceClass                 string                              `json:"evidenceClass"`
	ReleaseReady                  bool                                `json:"releaseReady"`
	Status                        string                              `json:"status"`
	SourceCommit                  string                              `json:"sourceCommit"`
	Package                       *productevidence.PackageIdentity    `json:"package,omitempty"`
	RuntimeIdentity               *productevidence.RuntimeExpectation `json:"runtime,omitempty"`
	CandidateStatus               string                              `json:"candidateStatus,omitempty"`
	SigningObservationSHA256      string                              `json:"signingObservationSHA256,omitempty"`
	NotarizationObservationSHA256 string                              `json:"notarizationObservationSHA256,omitempty"`
	Platform                      Platform                            `json:"platform"`
	Matrix                        MatrixRef                           `json:"matrix"`
	Commands                      []CommandResult                     `json:"commands"`
	Gates                         []GateResult                        `json:"gates"`
	NonClaims                     []string                            `json:"nonClaims"`
	Redaction                     Redaction                           `json:"redaction"`
}

type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type MatrixRef struct {
	Schema  string `json:"schema"`
	Version string `json:"version"`
}

type CommandResult struct {
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	Code         string   `json:"code,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	Hint         string   `json:"hint,omitempty"`
	NextActions  []string `json:"nextActions,omitempty"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
	Summary      string   `json:"summary"`
}

type GateResult struct {
	ID           string                          `json:"id"`
	Required     bool                            `json:"required"`
	Status       string                          `json:"status"`
	Code         string                          `json:"code,omitempty"`
	Reason       string                          `json:"reason,omitempty"`
	Hint         string                          `json:"hint,omitempty"`
	NextActions  []string                        `json:"nextActions,omitempty"`
	EvidenceRefs []string                        `json:"evidenceRefs,omitempty"`
	EvidencePath string                          `json:"evidencePath,omitempty"`
	Summary      string                          `json:"summary"`
	Runtime      *productevidence.RuntimeBinding `json:"runtime,omitempty"`
}

type Redaction struct {
	Mode string `json:"mode"`
}

func BuildReadiness(opts ReadinessOptions) (Readiness, error) {
	mode := strings.TrimSpace(opts.Mode)
	if mode == "" {
		mode = "local-fast"
	}
	if mode != "local-fast" && mode != "release-candidate" {
		return Readiness{}, fmt.Errorf("unsupported readiness mode %q", mode)
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	commit := strings.TrimSpace(opts.Commit)
	if mode == "release-candidate" {
		commit = ""
		if opts.Package != nil && opts.Package.Validate() == nil {
			commit = opts.Package.Commit()
		}
	}
	if commit == "" {
		commit = "unknown"
	}
	ready := Readiness{
		Schema:        ReadinessSchema,
		GeneratedAt:   now.UTC(),
		Mode:          mode,
		EvidenceClass: "local-fast",
		ReleaseReady:  false,
		Status:        "not-release",
		SourceCommit:  audit.RedactString(commit),
		Platform:      Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Matrix:        MatrixRef{Schema: MatrixSchema, Version: MatrixVersion},
		Commands: []CommandResult{
			{Name: "local-checks", Status: statusForBool(opts.LocalPassed), Summary: summaryForLocal(opts.LocalPassed)},
			productHardeningCommand(opts.ProductEvidence, commit, opts.Runtime, opts.Package, mode == "release-candidate"),
		},
		NonClaims: RequiredNonClaimIDs(),
		Redaction: Redaction{Mode: "control-plane"},
	}
	if mode == "release-candidate" {
		if opts.Package != nil {
			copy := *opts.Package
			ready.Package = &copy
		}
		if opts.Runtime != nil {
			runtimeCopy := *opts.Runtime
			ready.RuntimeIdentity = &runtimeCopy
		}
		ready.CandidateStatus = "failed"
		ready.SigningObservationSHA256 = strings.TrimSpace(opts.SigningObservationSHA256)
		ready.NotarizationObservationSHA256 = strings.TrimSpace(opts.NotarizationObservationSHA256)
	}
	if mode == "local-fast" {
		ready.Gates = []GateResult{
			{ID: "gate2-lima", Required: true, Status: "not-run", Summary: "real Gate 2 not run in local-fast mode"},
			{ID: "gate3-hidden-proxy", Required: true, Status: "not-run", Summary: "real Gate 3 not run in local-fast mode"},
		}
		return RedactReadiness(ready), nil
	}
	ready.EvidenceClass = "real-gate"
	ready.Status = "failed"
	ready.Gates = []GateResult{
		evidenceGate("gate2-lima", opts.Gate2Evidence, opts.Runtime),
		evidenceGate("gate3-hidden-proxy", opts.Gate3Evidence, opts.Runtime),
	}
	if opts.Runtime != nil && len(ready.Gates) == 2 && ready.Gates[0].Runtime != nil && ready.Gates[1].Runtime != nil {
		if !ready.Gates[0].Runtime.SameArtifactBuild(*ready.Gates[1].Runtime) {
			ready.Gates[1].Status = "failed"
			ready.Gates[1].Summary = "Gate 3 runtime artifact and image-build binding does not match Gate 2"
		} else if ready.Gates[0].Runtime.EnvironmentID == ready.Gates[1].Runtime.EnvironmentID {
			ready.Gates[1].Status = "failed"
			ready.Gates[1].Summary = "Gate 3 must run in a managed environment independent from Gate 2"
		}
	}
	commandsValid := validateReleaseCommandRows(ready.Commands) == nil
	gatesValid := validateReleaseGateRows(ready.Gates) == nil
	observationsValid := true
	if mode == "release-candidate" {
		observationsValid = isSHA256(ready.SigningObservationSHA256) && isSHA256(ready.NotarizationObservationSHA256)
	}
	ready.ReleaseReady = opts.LocalPassed && releaseTrustAnchorsValid(opts.Runtime, opts.Package, commit) && commandsValid && gatesValid && observationsValid
	if ready.ReleaseReady {
		ready.Status = "passed"
		ready.CandidateStatus = "passed"
	}
	return RedactReadiness(ready), nil
}

func productHardeningCommand(paths []string, commit string, runtimeExpectation *productevidence.RuntimeExpectation, expectedPackage *productevidence.PackageIdentity, releaseCandidate bool) CommandResult {
	if releaseCandidate {
		if !releaseTrustAnchorsValid(runtimeExpectation, expectedPackage, commit) {
			return CommandResult{Name: "product-hardening-evidence", Status: "failed", Summary: "trusted promoted runtime and verified package identity are required"}
		}
	}
	if len(paths) == 0 {
		if releaseCandidate {
			return CommandResult{Name: "product-hardening-evidence", Status: "failed", Summary: "runtime product evidence is required"}
		}
		return CommandResult{Name: "product-hardening-evidence", Status: "not-run", Summary: "supporting product-hardening evidence not supplied"}
	}
	manifests := make([]productevidence.Manifest, 0, len(paths))
	artifactRoots := map[string]string{}
	for _, path := range paths {
		manifest, err := productevidence.ReadFile(path)
		if err != nil {
			return CommandResult{Name: "product-hardening-evidence", Status: "failed", Summary: audit.RedactString(err.Error())}
		}
		manifests = append(manifests, manifest)
		for _, proof := range manifest.Proofs {
			artifactRoots[proof.ProofID] = filepath.Dir(path)
		}
	}
	requirements := productevidence.ProductHardeningRequirements()
	target := productevidence.RequiredForTargetedCompletion
	if runtimeExpectation != nil {
		target = productevidence.RequiredForReleaseCandidate
	}
	report, err := productevidence.EvaluateManifests(manifests, productevidence.EvaluationOptions{
		Requirements:    requirements,
		Target:          target,
		ExpectedCommit:  commit,
		ArtifactRoots:   artifactRoots,
		ExpectedRuntime: runtimeExpectation,
		ExpectedPackage: expectedPackage,
	})
	if err != nil {
		return commandWithRecovery("product-hardening-evidence", "failed", productEvidenceCode(err), audit.RedactString(err.Error()), paths...)
	}
	if err := report.RequireSatisfied(); err != nil {
		return commandWithRecovery("product-hardening-evidence", "failed", productEvidenceCode(err), audit.RedactString(err.Error()), paths...)
	}
	return CommandResult{Name: "product-hardening-evidence", Status: "passed", Summary: "supporting product-hardening evidence satisfied"}
}

func releaseTrustAnchorsValid(runtimeExpectation *productevidence.RuntimeExpectation, expectedPackage *productevidence.PackageIdentity, packageCommit string) bool {
	if runtimeExpectation == nil || runtimeExpectation.Validate() != nil || expectedPackage == nil {
		return false
	}
	return expectedPackage.ValidateCandidateCommit(packageCommit) == nil
}

func productEvidenceCode(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(err.Error(), "stale") {
		return recovery.CodeReleaseEvidenceStale
	}
	return ""
}

func statusForBool(ok bool) string {
	if ok {
		return "passed"
	}
	return "failed"
}

func summaryForLocal(ok bool) string {
	if ok {
		return "local build/test/schema checks passed"
	}
	return "local build/test/schema checks failed"
}

func evidenceGate(id, path string, expected *productevidence.RuntimeExpectation) GateResult {
	path = strings.TrimSpace(path)
	if path == "" {
		return gateWithRecovery(id, true, "missing", recovery.CodeReleaseGateEvidenceMissing, "", "required real gate evidence is missing", id)
	}
	summary, binding, err := validateGateEvidence(id, path, expected)
	if err != nil {
		return GateResult{ID: id, Required: true, Status: "failed", EvidencePath: audit.RedactString(path), Summary: audit.RedactString(err.Error())}
	}
	return GateResult{ID: id, Required: true, Status: "passed", EvidencePath: audit.RedactString(path), Summary: summary, Runtime: binding}
}

func commandWithRecovery(name, status, code, summary string, evidenceRefs ...string) CommandResult {
	result := CommandResult{Name: name, Status: status, Code: code, Summary: summary}
	result.Reason, result.Hint, result.NextActions, result.EvidenceRefs = recoveryFields(code, evidenceRefs...)
	return result
}

func gateWithRecovery(id string, required bool, status, code, evidencePath, summary string, evidenceRefs ...string) GateResult {
	result := GateResult{ID: id, Required: required, Status: status, Code: code, EvidencePath: audit.RedactString(evidencePath), Summary: summary}
	result.Reason, result.Hint, result.NextActions, result.EvidenceRefs = recoveryFields(code, evidenceRefs...)
	return result
}

func recoveryFields(code string, evidenceRefs ...string) (string, string, []string, []string) {
	if strings.TrimSpace(code) == "" {
		return "", "", nil, cleanEvidenceRefs(evidenceRefs)
	}
	entry, ok := recovery.Lookup(code)
	if !ok {
		return "", "", nil, cleanEvidenceRefs(evidenceRefs)
	}
	return entry.Reason, entry.Hint, append([]string(nil), entry.NextActions...), cleanEvidenceRefs(evidenceRefs)
}

func cleanEvidenceRefs(refs []string) []string {
	var out []string
	for _, ref := range refs {
		ref = strings.TrimSpace(audit.RedactString(ref))
		if ref != "" {
			out = append(out, ref)
		}
	}
	return out
}

type gateEvidence struct {
	ID              string                          `json:"id"`
	Backend         string                          `json:"backend"`
	EnvironmentName string                          `json:"environmentName,omitempty"`
	Result          string                          `json:"result"`
	Reason          string                          `json:"reason,omitempty"`
	AuditPath       string                          `json:"auditPath,omitempty"`
	BoundarySummary string                          `json:"boundarySummary,omitempty"`
	Runtime         *productevidence.RuntimeBinding `json:"runtime,omitempty"`
}

type releaseDogfoodEvidence struct {
	Schema              string                            `json:"schema"`
	Status              string                            `json:"status"`
	ExitCode            int                               `json:"exitCode"`
	StartedAt           string                            `json:"startedAt"`
	EndedAt             string                            `json:"endedAt"`
	Command             string                            `json:"command"`
	Evidence            releaseDogfoodEvidenceRef         `json:"evidence"`
	Git                 releaseDogfoodGit                 `json:"git"`
	Host                releaseDogfoodHost                `json:"host"`
	Tools               releaseDogfoodTools               `json:"tools"`
	OperatorProxy       releaseDogfoodOperatorProxy       `json:"operatorProxy"`
	Browser             releaseDogfoodBrowser             `json:"browser"`
	ReleaseArtifact     *releaseDogfoodArtifact           `json:"releaseArtifact,omitempty"`
	Gates               []string                          `json:"gates"`
	Cleanup             releaseDogfoodCleanup             `json:"cleanup"`
	IsolationGates      []gateEvidence                    `json:"isolationGates"`
	EnvironmentSnapshot releaseDogfoodEnvironmentSnapshot `json:"environmentSnapshot"`
}

type releaseDogfoodEvidenceRef struct {
	Directory string `json:"directory"`
	Log       string `json:"log"`
}

type releaseDogfoodGit struct {
	Commit string `json:"commit"`
	Dirty  *bool  `json:"dirty"`
}

type releaseDogfoodHost struct {
	Uname               string `json:"uname"`
	MacOSProductVersion string `json:"macOSProductVersion"`
}

type releaseDogfoodTools struct {
	Go      string `json:"go"`
	Limactl string `json:"limactl"`
	JQ      string `json:"jq"`
}

type releaseDogfoodOperatorProxy struct {
	Provided bool   `json:"provided"`
	Scheme   string `json:"scheme"`
	URL      string `json:"url"`
}

type releaseDogfoodBrowser struct {
	RealBrowserRequired bool   `json:"realBrowserRequired"`
	BrowserPathProvided bool   `json:"browserPathProvided"`
	BrowserApp          string `json:"browserApp"`
}

type releaseDogfoodArtifact struct {
	File           string                       `json:"file"`
	SHA256         string                       `json:"sha256"`
	Bytes          int64                        `json:"bytes"`
	HideoutVersion releaseDogfoodHideoutVersion `json:"hideoutVersion"`
}

type releaseDogfoodHideoutVersion struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	BuiltAt  string `json:"builtAt"`
	Go       string `json:"go"`
	Platform string `json:"platform"`
}

type releaseDogfoodCleanup struct {
	Gate4BrowserProcesses int `json:"gate4BrowserProcesses"`
	Gate4TempDirs         int `json:"gate4TempDirs"`
	HideoutLimaInstances  int `json:"hideoutLimaInstances"`
}

type releaseDogfoodEnvironmentSnapshot struct {
	ProxyMode         string                          `json:"proxyMode"`
	HostPrerequisites releaseDogfoodHostPrerequisites `json:"hostPrerequisites"`
	ExternalContext   string                          `json:"externalContext"`
}

type releaseDogfoodHostPrerequisites struct {
	Gate4Browser bool `json:"gate4Browser"`
	EnvImageURL  bool `json:"envImageURL"`
}

func validateGateEvidence(id, path string, expected *productevidence.RuntimeExpectation) (string, *productevidence.RuntimeBinding, error) {
	data, err := readBoundedGateEvidence(path)
	if err != nil {
		return "", nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return "", nil, fmt.Errorf("%s evidence is empty", id)
	}
	if productevidence.ContainsControlPlaneBytes(data) {
		return "", nil, fmt.Errorf("%s evidence contains unredacted control-plane material", id)
	}
	var envelope struct {
		ID     string `json:"id"`
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", nil, fmt.Errorf("%s evidence is not valid JSON gate evidence: %w", id, err)
	}
	if envelope.ID != "" && envelope.Schema != "" {
		return "", nil, fmt.Errorf("%s evidence has ambiguous gate and manifest identities", id)
	}
	if envelope.ID != "" {
		var gate gateEvidence
		if err := decodeStrictJSON(data, &gate); err != nil {
			return "", nil, fmt.Errorf("%s gate evidence: %w", id, err)
		}
		if err := validateSingleGate(id, gate, expected); err != nil {
			return "", nil, err
		}
		return "real gate result passed", gate.Runtime, nil
	}
	if envelope.Schema == "" {
		return "", nil, fmt.Errorf("%s evidence is missing a gate id or manifest schema", id)
	}
	var manifest releaseDogfoodEvidence
	if err := decodeStrictJSON(data, &manifest); err != nil {
		return "", nil, fmt.Errorf("%s evidence is not valid JSON gate evidence: %w", id, err)
	}
	if manifest.Schema != "hideout.release-dogfood.v1" {
		return "", nil, fmt.Errorf("%s evidence has unsupported schema %q", id, manifest.Schema)
	}
	if manifest.Status != "passed" {
		return "", nil, fmt.Errorf("%s release dogfood evidence status is %q", id, manifest.Status)
	}
	for _, candidate := range manifest.IsolationGates {
		if candidate.ID != id {
			continue
		}
		if err := validateSingleGate(id, candidate, expected); err != nil {
			return "", nil, err
		}
		return "release dogfood manifest includes passed gate", candidate.Runtime, nil
	}
	return "", nil, fmt.Errorf("%s release dogfood evidence does not include the required gate", id)
}

func readBoundedGateEvidence(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxGateEvidenceBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxGateEvidenceBytes {
		return nil, fmt.Errorf("gate evidence exceeds %d-byte validation limit", maxGateEvidenceBytes)
	}
	return data, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func validateSingleGate(id string, gate gateEvidence, expected *productevidence.RuntimeExpectation) error {
	if gate.ID != id {
		return fmt.Errorf("gate evidence id %q does not match required %q", gate.ID, id)
	}
	if gate.Result != "passed" {
		return fmt.Errorf("%s evidence result is %q", id, gate.Result)
	}
	if expected == nil || expected.Validate() != nil {
		return fmt.Errorf("%s trusted runtime expectation is required", id)
	}
	if gate.Backend != "lima" {
		return fmt.Errorf("%s evidence backend %q does not match supported backend %q", id, gate.Backend, "lima")
	}
	if gate.Runtime != nil {
		if err := gate.Runtime.Validate(); err != nil {
			return fmt.Errorf("%s runtime binding: %w", id, err)
		}
	}
	if gate.Runtime == nil {
		return fmt.Errorf("%s evidence is missing exact runtime binding", id)
	}
	if err := gate.Runtime.Matches(*expected); err != nil {
		return fmt.Errorf("%s runtime binding: %w", id, err)
	}
	return nil
}

func RedactReadiness(in Readiness) Readiness {
	in.SourceCommit = audit.RedactString(in.SourceCommit)
	if in.Package != nil {
		copy := *in.Package
		copy.Name = audit.RedactString(copy.Name)
		copy.ProductVersion = audit.RedactString(copy.ProductVersion)
		copy.SourceCommit = audit.RedactString(copy.SourceCommit)
		copy.ArtifactSHA256 = audit.RedactString(copy.ArtifactSHA256)
		copy.HostOS = audit.RedactString(copy.HostOS)
		copy.HostArch = audit.RedactString(copy.HostArch)
		in.Package = &copy
	}
	in.SigningObservationSHA256 = audit.RedactString(in.SigningObservationSHA256)
	in.NotarizationObservationSHA256 = audit.RedactString(in.NotarizationObservationSHA256)
	for i := range in.Commands {
		in.Commands[i].Name = audit.RedactString(in.Commands[i].Name)
		in.Commands[i].Code = audit.RedactString(in.Commands[i].Code)
		in.Commands[i].Reason = audit.RedactString(in.Commands[i].Reason)
		in.Commands[i].Hint = audit.RedactString(in.Commands[i].Hint)
		for j := range in.Commands[i].NextActions {
			in.Commands[i].NextActions[j] = audit.RedactString(in.Commands[i].NextActions[j])
		}
		for j := range in.Commands[i].EvidenceRefs {
			in.Commands[i].EvidenceRefs[j] = audit.RedactString(in.Commands[i].EvidenceRefs[j])
		}
		in.Commands[i].Summary = audit.RedactString(in.Commands[i].Summary)
	}
	for i := range in.Gates {
		in.Gates[i].Code = audit.RedactString(in.Gates[i].Code)
		in.Gates[i].Reason = audit.RedactString(in.Gates[i].Reason)
		in.Gates[i].Hint = audit.RedactString(in.Gates[i].Hint)
		for j := range in.Gates[i].NextActions {
			in.Gates[i].NextActions[j] = audit.RedactString(in.Gates[i].NextActions[j])
		}
		for j := range in.Gates[i].EvidenceRefs {
			in.Gates[i].EvidenceRefs[j] = audit.RedactString(in.Gates[i].EvidenceRefs[j])
		}
		in.Gates[i].EvidencePath = audit.RedactString(in.Gates[i].EvidencePath)
		in.Gates[i].Summary = audit.RedactString(in.Gates[i].Summary)
	}
	return in
}

func ValidateReadiness(readiness Readiness) error {
	if readiness.Schema != ReadinessSchema {
		return fmt.Errorf("unsupported readiness schema %q", readiness.Schema)
	}
	if readiness.Mode != "local-fast" && readiness.Mode != "release-candidate" {
		return fmt.Errorf("unsupported readiness mode %q", readiness.Mode)
	}
	if readiness.Mode == "release-candidate" {
		if readiness.CandidateStatus != "passed" && readiness.CandidateStatus != "failed" {
			return fmt.Errorf("release-candidate readiness candidateStatus is invalid")
		}
		if readiness.Package != nil {
			if err := readiness.Package.ValidateCandidateCommit(readiness.SourceCommit); err != nil {
				return fmt.Errorf("release-candidate package identity: %w", err)
			}
		}
		if readiness.RuntimeIdentity != nil && readiness.RuntimeIdentity.Validate() != nil {
			return fmt.Errorf("release-candidate runtime identity is invalid")
		}
		if readiness.ReleaseReady && readiness.CandidateStatus != "passed" {
			return fmt.Errorf("release-ready readiness requires candidateStatus=passed")
		}
		if readiness.ReleaseReady {
			if readiness.Package == nil || readiness.RuntimeIdentity == nil {
				return fmt.Errorf("release-ready readiness requires package and runtime identity")
			}
			if !isSHA256(readiness.SigningObservationSHA256) || !isSHA256(readiness.NotarizationObservationSHA256) {
				return fmt.Errorf("release-ready readiness requires signing and notarization observation digests")
			}
		}
	}
	if readiness.Mode == "local-fast" {
		if readiness.ReleaseReady {
			return fmt.Errorf("local-fast readiness must not claim releaseReady")
		}
		if readiness.Status != "not-release" {
			return fmt.Errorf("local-fast readiness must use not-release status")
		}
	}
	if !readiness.ReleaseReady {
		return nil
	}
	if readiness.Mode != "release-candidate" {
		return fmt.Errorf("releaseReady requires release-candidate mode")
	}
	if readiness.EvidenceClass != "real-gate" || readiness.Status != "passed" {
		return fmt.Errorf("releaseReady requires real-gate evidenceClass and passed status")
	}
	if !productevidence.IsCanonicalCommit(readiness.SourceCommit) {
		return fmt.Errorf("releaseReady sourceCommit must be a canonical candidate commit")
	}
	if err := validateReleaseCommandRows(readiness.Commands); err != nil {
		return fmt.Errorf("releaseReady commands: %w", err)
	}
	if err := validateReleaseGateRows(readiness.Gates); err != nil {
		return fmt.Errorf("releaseReady gates: %w", err)
	}
	if productevidence.ContainsControlPlaneMaterial(readiness) {
		return fmt.Errorf("releaseReady artifact contains unredacted control-plane material")
	}
	return nil
}

func isSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func validateReleaseCommandRows(commands []CommandResult) error {
	expected := map[string]struct{}{
		"local-checks":               {},
		"product-hardening-evidence": {},
	}
	if len(commands) != len(expected) {
		return fmt.Errorf("exact local-checks and product-hardening-evidence rows are required")
	}
	seen := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		if _, ok := expected[command.Name]; !ok {
			return fmt.Errorf("unexpected command row %q", command.Name)
		}
		if _, duplicate := seen[command.Name]; duplicate {
			return fmt.Errorf("duplicate command row %q", command.Name)
		}
		seen[command.Name] = struct{}{}
		if command.Status != "passed" {
			return fmt.Errorf("command %q status is %q", command.Name, command.Status)
		}
		if strings.TrimSpace(command.Summary) == "" {
			return fmt.Errorf("command %q summary is required", command.Name)
		}
	}
	return nil
}

func validateReleaseGateRows(gates []GateResult) error {
	expected := map[string]struct{}{
		"gate2-lima":         {},
		"gate3-hidden-proxy": {},
	}
	if len(gates) != len(expected) {
		return fmt.Errorf("exact Gate 2 and Gate 3 rows are required")
	}
	seen := make(map[string]struct{}, len(gates))
	seenEnvironments := make(map[string]string, len(gates))
	var runtimeAnchor *productevidence.RuntimeBinding
	for _, gate := range gates {
		if _, ok := expected[gate.ID]; !ok {
			return fmt.Errorf("unexpected gate row %q", gate.ID)
		}
		if _, duplicate := seen[gate.ID]; duplicate {
			return fmt.Errorf("duplicate gate row %q", gate.ID)
		}
		seen[gate.ID] = struct{}{}
		if !gate.Required || gate.Status != "passed" {
			return fmt.Errorf("gate %q must be required and passed", gate.ID)
		}
		if strings.TrimSpace(gate.EvidencePath) == "" || strings.TrimSpace(gate.Summary) == "" {
			return fmt.Errorf("gate %q evidencePath and summary are required", gate.ID)
		}
		if gate.Runtime == nil {
			return fmt.Errorf("gate %q runtime binding is required", gate.ID)
		}
		if err := gate.Runtime.Validate(); err != nil {
			return fmt.Errorf("gate %q runtime binding: %w", gate.ID, err)
		}
		if otherGate, exists := seenEnvironments[gate.Runtime.EnvironmentID]; exists {
			return fmt.Errorf("gates %q and %q reused managed environment %q", otherGate, gate.ID, gate.Runtime.EnvironmentID)
		}
		seenEnvironments[gate.Runtime.EnvironmentID] = gate.ID
		if gate.Runtime.BuildDirty {
			return fmt.Errorf("gate %q does not bind a clean runtime image build", gate.ID)
		}
		if runtimeAnchor == nil {
			copy := *gate.Runtime
			runtimeAnchor = &copy
		} else if !runtimeAnchor.SameArtifactBuild(*gate.Runtime) {
			return fmt.Errorf("gate %q runtime artifact and image-build binding does not match the other required gate", gate.ID)
		}
	}
	return nil
}

func WriteReadinessJSON(w io.Writer, readiness Readiness) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(readiness)
}
