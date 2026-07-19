// Package workspaceattach owns the transport-neutral workspace attachment
// model and the strict Phase R research gate.
package workspaceattach

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ResearchSchema               = "hideout.workspace-research-decision/v1"
	ResearchFeature              = "035"
	CandidateVZ                  = "vz-live-multiple-share"
	CandidatePortal              = "workspace-portal"
	ResearchAccepted             = "accepted"
	ResearchRejected             = "rejected"
	CandidatePassed              = "passed"
	CandidateFailed              = "failed"
	ResearchCheckPassed          = "passed"
	ResearchCheckFailed          = "failed"
	ResearchCheckNotUsed         = "not-applicable"
	PerformanceGitStatus         = "git-status"
	PerformancePackageScan       = "package-scan"
	PerformanceAtomicHostToGuest = "atomic-host-to-guest"
	PerformanceAtomicGuestToHost = "atomic-guest-to-host"
	PerformanceMountReady        = "mount-ready"
	PerformanceFirstByte         = "first-byte"
)

type ResearchDecision struct {
	Schema            string                    `json:"schema"`
	Feature           string                    `json:"feature"`
	Result            string                    `json:"result"`
	SelectedCandidate string                    `json:"selectedCandidate,omitempty"`
	CandidateResults  []ResearchCandidateResult `json:"candidateResults"`
	PathIdentity      ResearchPathIdentity      `json:"pathIdentity"`
	OperationMatrix   []ResearchOperation       `json:"operationMatrix"`
	Limits            *ResearchLimits           `json:"limits,omitempty"`
	Performance       ResearchPerformance       `json:"performance"`
	Topology          ResearchTopology          `json:"topology"`
	Provenance        ResearchProvenance        `json:"provenance"`
	Artifacts         []ResearchArtifact        `json:"artifacts"`
	DecisionAt        time.Time                 `json:"decisionAt"`
}

type ResearchCandidateResult struct {
	Candidate    string          `json:"candidate"`
	Status       string          `json:"status"`
	Checks       []ResearchCheck `json:"checks"`
	Summary      string          `json:"summary"`
	FailureCodes []string        `json:"failureCodes,omitempty"`
}

type ResearchCheck struct {
	ID           string   `json:"id"`
	Result       string   `json:"result"`
	EvidenceRefs []string `json:"evidenceRefs"`
	Notes        string   `json:"notes,omitempty"`
}

type ResearchPathIdentity struct {
	LogicalRoot         string                    `json:"logicalRoot"`
	PhysicalRootPattern string                    `json:"physicalRootPattern"`
	Mechanism           string                    `json:"mechanism"`
	Tools               []ResearchToolObservation `json:"tools"`
}

type ResearchToolObservation struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Result      string `json:"result"`
	EvidenceRef string `json:"evidenceRef"`
}

type ResearchOperation struct {
	Operation   string `json:"operation"`
	Support     string `json:"support"`
	Result      string `json:"result"`
	Errno       string `json:"errno,omitempty"`
	EvidenceRef string `json:"evidenceRef,omitempty"`
}

type ResearchLimits struct {
	ViewsPerEnvironment   int `json:"viewsPerEnvironment"`
	HandlesPerSession     int `json:"handlesPerSession"`
	InFlightPerSession    int `json:"inFlightPerSession"`
	QueuedBytesPerSession int `json:"queuedBytesPerSession"`
	FrameBytes            int `json:"frameBytes"`
	DirectoryEntries      int `json:"directoryEntries"`
}

type ResearchPerformance struct {
	Metrics          []ResearchPerformanceMetric `json:"metrics"`
	ThresholdsPassed bool                        `json:"thresholdsPassed"`
	RawSamplesRef    string                      `json:"rawSamplesRef"`
}

type ResearchPerformanceMetric struct {
	ID        string                     `json:"id"`
	Baseline  *ResearchPerformanceResult `json:"baseline,omitempty"`
	Candidate ResearchPerformanceResult  `json:"candidate"`
}

type ResearchPerformanceResult struct {
	Samples  int     `json:"samples"`
	MedianMS float64 `json:"medianMs"`
	P95MS    float64 `json:"p95Ms"`
}

type ResearchTopology struct {
	HostProcesses  []string `json:"hostProcesses"`
	GuestProcesses []string `json:"guestProcesses"`
	ControlPaths   []string `json:"controlPaths"`
	DataPaths      []string `json:"dataPaths"`
}

type ResearchProvenance struct {
	Commit        string            `json:"commit"`
	Dirty         bool              `json:"dirty"`
	HostArch      string            `json:"hostArch"`
	MacOSVersion  string            `json:"macosVersion"`
	LimaVersion   string            `json:"limaVersion"`
	RuntimeDigest string            `json:"runtimeDigest"`
	FixtureDigest string            `json:"fixtureDigest"`
	ToolVersions  map[string]string `json:"toolVersions"`
}

type ResearchArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type ResearchEvaluationOptions struct {
	ArtifactRoot   string
	ExpectedCommit string
	AllowDirty     bool
}

func ValidateResearchDecision(decision ResearchDecision, options ResearchEvaluationOptions) error {
	if decision.Schema != ResearchSchema || decision.Feature != ResearchFeature {
		return errors.New("workspace research decision identity is invalid")
	}
	if decision.Result != ResearchAccepted && decision.Result != ResearchRejected {
		return fmt.Errorf("unsupported workspace research result %q", decision.Result)
	}
	if decision.DecisionAt.IsZero() {
		return errors.New("workspace research decision timestamp is required")
	}
	if options.ExpectedCommit != "" && decision.Provenance.Commit != options.ExpectedCommit {
		return errors.New("workspace research decision commit is stale")
	}
	if !isResearchCommit(decision.Provenance.Commit) || decision.Provenance.HostArch != "arm64" ||
		strings.TrimSpace(decision.Provenance.MacOSVersion) == "" || strings.TrimSpace(decision.Provenance.LimaVersion) == "" ||
		!isResearchSHA256(decision.Provenance.RuntimeDigest) || !isResearchSHA256(decision.Provenance.FixtureDigest) ||
		len(decision.Provenance.ToolVersions) == 0 {
		return errors.New("workspace research provenance is incomplete")
	}
	if decision.Provenance.Dirty && !options.AllowDirty {
		return errors.New("dirty workspace research evidence cannot authorize promotion")
	}

	artifactPaths, err := validateResearchArtifacts(decision.Artifacts, options.ArtifactRoot)
	if err != nil {
		return err
	}
	if err := validateResearchCandidates(decision, artifactPaths); err != nil {
		return err
	}
	accepted := decision.Result == ResearchAccepted
	if err := validateResearchPathIdentity(decision.PathIdentity, artifactPaths, accepted); err != nil {
		return err
	}
	if err := validateResearchOperations(decision.OperationMatrix, artifactPaths, accepted); err != nil {
		return err
	}
	if err := validateResearchPerformance(decision.Performance, artifactPaths, accepted); err != nil {
		return err
	}
	if len(decision.Topology.HostProcesses) == 0 || len(decision.Topology.GuestProcesses) == 0 ||
		len(decision.Topology.ControlPaths) == 0 || len(decision.Topology.DataPaths) == 0 {
		return errors.New("workspace research topology is incomplete")
	}
	return nil
}

func LoadResearchDecision(path string, options ResearchEvaluationOptions) (ResearchDecision, error) {
	data, err := readResearchRootedFile(options.ArtifactRoot, path, 4<<20)
	if err != nil {
		return ResearchDecision{}, err
	}
	var decision ResearchDecision
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decision); err != nil {
		return ResearchDecision{}, fmt.Errorf("decode workspace research decision: %w", err)
	}
	if err := ensureResearchJSONEOF(decoder); err != nil {
		return ResearchDecision{}, err
	}
	if err := ValidateResearchDecision(decision, options); err != nil {
		return ResearchDecision{}, err
	}
	return decision, nil
}

func WriteResearchDecision(path string, decision ResearchDecision, options ResearchEvaluationOptions) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("workspace research decision path is required")
	}
	if err := ValidateResearchDecision(decision, options); err != nil {
		return err
	}
	relative, err := researchRelativePath(options.ArtifactRoot, path)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(decision, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	rooted, err := os.OpenRoot(options.ArtifactRoot)
	if err != nil {
		return err
	}
	defer rooted.Close()
	parent := filepath.Dir(relative)
	if parent != "." {
		if err := rooted.MkdirAll(parent, 0o700); err != nil {
			return err
		}
	}
	temporaryPath, err := researchTemporaryPath(parent)
	if err != nil {
		return err
	}
	temporary, err := rooted.OpenFile(temporaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer rooted.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := rooted.Rename(temporaryPath, relative); err != nil {
		return err
	}
	directory, err := rooted.Open(parent)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func researchTemporaryPath(parent string) (string, error) {
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return filepath.Join(parent, ".workspace-research-"+hex.EncodeToString(suffix[:])+".tmp"), nil
}

func validateResearchCandidates(decision ResearchDecision, artifacts map[string]struct{}) error {
	if len(decision.CandidateResults) != 2 {
		return errors.New("workspace research decision requires exactly two candidates")
	}
	seen := map[string]bool{}
	passed := ""
	for _, candidate := range decision.CandidateResults {
		if candidate.Candidate != CandidateVZ && candidate.Candidate != CandidatePortal {
			return fmt.Errorf("unknown workspace research candidate %q", candidate.Candidate)
		}
		if seen[candidate.Candidate] {
			return fmt.Errorf("duplicate workspace research candidate %q", candidate.Candidate)
		}
		seen[candidate.Candidate] = true
		if candidate.Status != CandidatePassed && candidate.Status != CandidateFailed {
			return fmt.Errorf("candidate %s has invalid status %q", candidate.Candidate, candidate.Status)
		}
		if strings.TrimSpace(candidate.Summary) == "" || len(candidate.Checks) == 0 {
			return fmt.Errorf("candidate %s has incomplete result", candidate.Candidate)
		}
		checkIDs := map[string]bool{}
		allPassed := true
		hasFailedCheck := false
		for _, check := range candidate.Checks {
			if strings.TrimSpace(check.ID) == "" || checkIDs[check.ID] {
				return fmt.Errorf("candidate %s has invalid or duplicate check id %q", candidate.Candidate, check.ID)
			}
			checkIDs[check.ID] = true
			if check.Result != ResearchCheckPassed && check.Result != ResearchCheckFailed && check.Result != ResearchCheckNotUsed {
				return fmt.Errorf("candidate %s check %s has invalid result", candidate.Candidate, check.ID)
			}
			if check.Result != ResearchCheckPassed {
				allPassed = false
			}
			if check.Result == ResearchCheckFailed {
				hasFailedCheck = true
			}
			if err := validateResearchRefs(check.EvidenceRefs, artifacts); err != nil {
				return fmt.Errorf("candidate %s check %s: %w", candidate.Candidate, check.ID, err)
			}
		}
		if candidate.Status == CandidatePassed {
			if !allPassed || passed != "" || len(candidate.FailureCodes) != 0 {
				return fmt.Errorf("candidate %s has a false-green passed status", candidate.Candidate)
			}
			passed = candidate.Candidate
		} else if len(candidate.FailureCodes) == 0 || !hasFailedCheck {
			return fmt.Errorf("failed candidate %s requires a failure code and failed check", candidate.Candidate)
		}
	}
	if !seen[CandidateVZ] || !seen[CandidatePortal] {
		return errors.New("workspace research candidate inventory is incomplete")
	}
	if decision.Result == ResearchAccepted {
		if decision.SelectedCandidate == "" || decision.SelectedCandidate != passed || decision.Limits == nil {
			return errors.New("accepted workspace research decision has no single complete selected candidate")
		}
		if err := validateResearchLimits(*decision.Limits); err != nil {
			return err
		}
	} else {
		if decision.SelectedCandidate != "" || passed != "" || decision.Limits != nil {
			return errors.New("rejected workspace research decision cannot select or pass a candidate")
		}
	}
	return nil
}

func validateResearchPathIdentity(identity ResearchPathIdentity, artifacts map[string]struct{}, accepted bool) error {
	if identity.LogicalRoot != "/workspace" || identity.PhysicalRootPattern != "/hideout/workspaces/<workspaceId>" || strings.TrimSpace(identity.Mechanism) == "" {
		return errors.New("workspace research path identity is incomplete")
	}
	required := []string{"bash", "git", "node", "python", "go", "claude", "codex"}
	seen := map[string]bool{}
	for _, tool := range identity.Tools {
		if strings.TrimSpace(tool.Name) == "" || seen[tool.Name] || strings.TrimSpace(tool.Version) == "" {
			return fmt.Errorf("invalid or duplicate workspace path tool %q", tool.Name)
		}
		seen[tool.Name] = true
		if tool.Result != CandidatePassed && tool.Result != CandidateFailed {
			return fmt.Errorf("workspace path tool %s has invalid result", tool.Name)
		}
		if accepted && tool.Result != CandidatePassed {
			return fmt.Errorf("workspace path tool %s did not pass", tool.Name)
		}
		if _, ok := artifacts[tool.EvidenceRef]; !ok {
			return fmt.Errorf("workspace path tool %s references unknown evidence %q", tool.Name, tool.EvidenceRef)
		}
	}
	for _, name := range required {
		if !seen[name] {
			return fmt.Errorf("workspace path identity is missing tool %s", name)
		}
	}
	return nil
}

func validateResearchOperations(operations []ResearchOperation, artifacts map[string]struct{}, accepted bool) error {
	if len(operations) == 0 {
		return errors.New("workspace research operation matrix is empty")
	}
	seen := map[string]bool{}
	for _, operation := range operations {
		if strings.TrimSpace(operation.Operation) == "" || seen[operation.Operation] {
			return fmt.Errorf("invalid or duplicate workspace operation %q", operation.Operation)
		}
		seen[operation.Operation] = true
		if operation.Support != "required" && operation.Support != "supported" && operation.Support != "unsupported" {
			return fmt.Errorf("operation %s has invalid support", operation.Operation)
		}
		if operation.Result != ResearchCheckPassed && operation.Result != ResearchCheckFailed && operation.Result != ResearchCheckNotUsed {
			return fmt.Errorf("operation %s has invalid result", operation.Operation)
		}
		if accepted && operation.Support == "required" && operation.Result != ResearchCheckPassed {
			return fmt.Errorf("required operation %s did not pass", operation.Operation)
		}
		if operation.EvidenceRef != "" {
			if _, ok := artifacts[operation.EvidenceRef]; !ok {
				return fmt.Errorf("operation %s references unknown evidence %q", operation.Operation, operation.EvidenceRef)
			}
		} else if operation.Result != ResearchCheckNotUsed {
			return fmt.Errorf("operation %s has no evidence", operation.Operation)
		}
	}
	return nil
}

func validateResearchPerformance(performance ResearchPerformance, artifacts map[string]struct{}, accepted bool) error {
	if _, ok := artifacts[performance.RawSamplesRef]; !ok {
		return errors.New("workspace research raw samples are not bound to an artifact")
	}
	required := []string{
		PerformanceGitStatus, PerformancePackageScan, PerformanceAtomicHostToGuest,
		PerformanceAtomicGuestToHost, PerformanceMountReady, PerformanceFirstByte,
	}
	seen := map[string]bool{}
	computedPassed := true
	for _, metric := range performance.Metrics {
		if seen[metric.ID] {
			return fmt.Errorf("duplicate workspace research performance metric %q", metric.ID)
		}
		seen[metric.ID] = true
		if err := validateResearchPerformanceResult(metric.Candidate); err != nil {
			return fmt.Errorf("performance metric %s candidate: %w", metric.ID, err)
		}
		baselineRequired := metric.ID == PerformanceGitStatus || metric.ID == PerformancePackageScan || metric.ID == PerformanceFirstByte
		if baselineRequired {
			if metric.Baseline == nil {
				return fmt.Errorf("performance metric %s requires a baseline", metric.ID)
			}
			if err := validateResearchPerformanceResult(*metric.Baseline); err != nil {
				return fmt.Errorf("performance metric %s baseline: %w", metric.ID, err)
			}
		} else if metric.Baseline != nil {
			return fmt.Errorf("performance metric %s has an inapplicable baseline", metric.ID)
		}
		passed, err := researchPerformanceMetricPassed(metric)
		if err != nil {
			return err
		}
		computedPassed = computedPassed && passed
	}
	for _, id := range required {
		if !seen[id] {
			return fmt.Errorf("workspace research performance is missing metric %s", id)
		}
	}
	if len(performance.Metrics) != len(required) {
		return errors.New("workspace research performance contains an unknown metric")
	}
	if performance.ThresholdsPassed != computedPassed {
		return errors.New("workspace research performance threshold summary does not match measured metrics")
	}
	if accepted && !computedPassed {
		return errors.New("workspace research performance thresholds did not pass")
	}
	return nil
}

func validateResearchPerformanceResult(result ResearchPerformanceResult) error {
	if result.Samples < 30 || !validResearchDuration(result.MedianMS) || !validResearchDuration(result.P95MS) || result.P95MS < result.MedianMS {
		return errors.New("samples are incomplete or percentiles are inconsistent")
	}
	return nil
}

func researchPerformanceMetricPassed(metric ResearchPerformanceMetric) (bool, error) {
	switch metric.ID {
	case PerformanceGitStatus:
		return metric.Candidate.MedianMS <= 2000 && metric.Candidate.MedianMS <= 2*metric.Baseline.MedianMS, nil
	case PerformancePackageScan:
		return metric.Candidate.MedianMS <= 3*metric.Baseline.MedianMS, nil
	case PerformanceAtomicHostToGuest, PerformanceAtomicGuestToHost:
		return metric.Candidate.P95MS <= 250, nil
	case PerformanceMountReady:
		return metric.Candidate.P95MS <= 1000, nil
	case PerformanceFirstByte:
		allowance := math.Max(500, metric.Baseline.P95MS*0.15)
		return metric.Candidate.P95MS <= metric.Baseline.P95MS+allowance, nil
	default:
		return false, fmt.Errorf("unknown workspace research performance metric %q", metric.ID)
	}
}

func validateResearchLimits(limits ResearchLimits) error {
	if limits.ViewsPerEnvironment <= 0 || limits.HandlesPerSession <= 0 || limits.InFlightPerSession <= 0 ||
		limits.QueuedBytesPerSession < 4096 || limits.FrameBytes < 1024 || limits.DirectoryEntries <= 0 {
		return errors.New("workspace research limits are incomplete")
	}
	return nil
}

func validateResearchArtifacts(artifacts []ResearchArtifact, root string) (map[string]struct{}, error) {
	if len(artifacts) == 0 || strings.TrimSpace(root) == "" {
		return nil, errors.New("workspace research artifacts and root are required")
	}
	rooted, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer rooted.Close()
	seen := map[string]struct{}{}
	for _, artifact := range artifacts {
		clean, err := cleanResearchRelativePath(artifact.Path)
		if err != nil || clean != artifact.Path || !isResearchSHA256(artifact.SHA256) {
			return nil, fmt.Errorf("invalid workspace research artifact %q", artifact.Path)
		}
		if _, exists := seen[artifact.Path]; exists {
			return nil, fmt.Errorf("duplicate workspace research artifact %q", artifact.Path)
		}
		file, err := rooted.Open(artifact.Path)
		if err != nil {
			return nil, fmt.Errorf("open workspace research artifact %q: %w", artifact.Path, err)
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			file.Close()
			return nil, fmt.Errorf("workspace research artifact %q is not a regular file", artifact.Path)
		}
		if info.Size() > 256<<20 {
			file.Close()
			return nil, fmt.Errorf("workspace research artifact %q exceeds the size limit", artifact.Path)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return nil, fmt.Errorf("hash workspace research artifact %q: %v %v", artifact.Path, copyErr, closeErr)
		}
		if hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
			return nil, fmt.Errorf("workspace research artifact %q digest mismatch", artifact.Path)
		}
		seen[artifact.Path] = struct{}{}
	}
	return seen, nil
}

func validateResearchRefs(refs []string, artifacts map[string]struct{}) error {
	if len(refs) == 0 {
		return errors.New("evidence reference is required")
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref] {
			return fmt.Errorf("duplicate evidence reference %q", ref)
		}
		seen[ref] = true
		if _, ok := artifacts[ref]; !ok {
			return fmt.Errorf("unknown evidence reference %q", ref)
		}
	}
	return nil
}

func readResearchRootedFile(root, path string, limit int64) ([]byte, error) {
	relative, err := researchRelativePath(root, path)
	if err != nil {
		return nil, err
	}
	rooted, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer rooted.Close()
	file, err := rooted.Open(relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("workspace research decision exceeds size limit")
	}
	return data, nil
}

func researchRelativePath(root, path string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("workspace research artifact root is required")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return "", err
	}
	return cleanResearchRelativePath(relative)
}

func cleanResearchRelativePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" || filepath.IsAbs(path) {
		return "", errors.New("workspace research path must be relative")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("workspace research path escapes its root")
	}
	return clean, nil
}

func ensureResearchJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("workspace research decision contains multiple JSON values")
		}
		return err
	}
	return nil
}

func isResearchCommit(value string) bool {
	return len(value) == 40 && strings.Trim(value, "0123456789abcdef") == ""
}

func isResearchSHA256(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func validResearchDuration(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
