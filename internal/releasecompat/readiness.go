package releasecompat

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
)

const ReadinessSchema = "hideout.release-readiness/v1"

type ReadinessOptions struct {
	Mode          string
	Commit        string
	Gate2Evidence string
	Gate3Evidence string
	LocalPassed   bool
	Now           time.Time
}

type Readiness struct {
	Schema        string          `json:"schema"`
	GeneratedAt   time.Time       `json:"generatedAt"`
	Mode          string          `json:"mode"`
	EvidenceClass string          `json:"evidenceClass"`
	ReleaseReady  bool            `json:"releaseReady"`
	Status        string          `json:"status"`
	Commit        string          `json:"commit"`
	Platform      Platform        `json:"platform"`
	Matrix        MatrixRef       `json:"matrix"`
	Commands      []CommandResult `json:"commands"`
	Gates         []GateResult    `json:"gates"`
	NonClaims     []string        `json:"nonClaims"`
	Redaction     Redaction       `json:"redaction"`
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
	Name    string `json:"name"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

type GateResult struct {
	ID           string `json:"id"`
	Required     bool   `json:"required"`
	Status       string `json:"status"`
	EvidencePath string `json:"evidencePath,omitempty"`
	Summary      string `json:"summary"`
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
		Commit:        audit.RedactString(commit),
		Platform:      Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Matrix:        MatrixRef{Schema: MatrixSchema, Version: MatrixVersion},
		Commands: []CommandResult{
			{Name: "local-checks", Status: statusForBool(opts.LocalPassed), Summary: summaryForLocal(opts.LocalPassed)},
		},
		NonClaims: RequiredNonClaimIDs(),
		Redaction: Redaction{Mode: "control-plane"},
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
		evidenceGate("gate2-lima", opts.Gate2Evidence),
		evidenceGate("gate3-hidden-proxy", opts.Gate3Evidence),
	}
	ready.ReleaseReady = opts.LocalPassed && gatesPassed(ready.Gates)
	if ready.ReleaseReady {
		ready.Status = "passed"
	}
	return RedactReadiness(ready), nil
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

func evidenceGate(id, path string) GateResult {
	path = strings.TrimSpace(path)
	if path == "" {
		return GateResult{ID: id, Required: true, Status: "missing", Summary: "required real gate evidence is missing"}
	}
	summary, err := validateGateEvidence(id, path)
	if err != nil {
		return GateResult{ID: id, Required: true, Status: "failed", EvidencePath: audit.RedactString(path), Summary: audit.RedactString(err.Error())}
	}
	return GateResult{ID: id, Required: true, Status: "passed", EvidencePath: audit.RedactString(path), Summary: summary}
}

type gateEvidence struct {
	ID      string `json:"id"`
	Backend string `json:"backend"`
	Result  string `json:"result"`
}

type releaseDogfoodEvidence struct {
	Schema         string         `json:"schema"`
	Status         string         `json:"status"`
	IsolationGates []gateEvidence `json:"isolationGates"`
}

func validateGateEvidence(id, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return "", fmt.Errorf("%s evidence is empty", id)
	}
	var gate gateEvidence
	if err := json.Unmarshal(data, &gate); err == nil && gate.ID != "" {
		if err := validateSingleGate(id, gate); err != nil {
			return "", err
		}
		return "real gate result passed", nil
	}
	var manifest releaseDogfoodEvidence
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("%s evidence is not valid JSON gate evidence: %w", id, err)
	}
	if manifest.Schema != "hideout.release-dogfood.v1" {
		return "", fmt.Errorf("%s evidence has unsupported schema %q", id, manifest.Schema)
	}
	if manifest.Status != "passed" {
		return "", fmt.Errorf("%s release dogfood evidence status is %q", id, manifest.Status)
	}
	for _, candidate := range manifest.IsolationGates {
		if candidate.ID != id {
			continue
		}
		if err := validateSingleGate(id, candidate); err != nil {
			return "", err
		}
		return "release dogfood manifest includes passed gate", nil
	}
	return "", fmt.Errorf("%s release dogfood evidence does not include the required gate", id)
}

func validateSingleGate(id string, gate gateEvidence) error {
	if gate.ID != id {
		return fmt.Errorf("gate evidence id %q does not match required %q", gate.ID, id)
	}
	if gate.Result != "passed" {
		return fmt.Errorf("%s evidence result is %q", id, gate.Result)
	}
	if strings.TrimSpace(gate.Backend) == "" {
		return fmt.Errorf("%s evidence backend is missing", id)
	}
	if gate.Backend == "native" {
		return fmt.Errorf("%s evidence cannot be satisfied by native backend", id)
	}
	return nil
}

func gatesPassed(gates []GateResult) bool {
	for _, gate := range gates {
		if gate.Required && gate.Status != "passed" {
			return false
		}
	}
	return true
}

func RedactReadiness(in Readiness) Readiness {
	in.Commit = audit.RedactString(in.Commit)
	for i := range in.Commands {
		in.Commands[i].Name = audit.RedactString(in.Commands[i].Name)
		in.Commands[i].Summary = audit.RedactString(in.Commands[i].Summary)
	}
	for i := range in.Gates {
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
	if readiness.Mode == "local-fast" {
		if readiness.ReleaseReady {
			return fmt.Errorf("local-fast readiness must not claim releaseReady")
		}
		if readiness.Status != "not-release" {
			return fmt.Errorf("local-fast readiness must use not-release status")
		}
	}
	if readiness.ReleaseReady && !gatesPassed(readiness.Gates) {
		return fmt.Errorf("releaseReady cannot be true with missing required gates")
	}
	return nil
}

func WriteReadinessJSON(w io.Writer, readiness Readiness) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(readiness)
}
