package productevidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
)

const Schema = "hideout.product-hardening-evidence/v1"

const (
	StatusPassed = "passed"
	StatusFailed = "failed"
	StatusNotRun = "not-run"

	RedactionPassed = "passed"
	RedactionFailed = "failed"
	RedactionNotRun = "not-run"
)

var validModes = []string{"browser-e2e", "pty-e2e", "local-fast", "real-gate", "schema", "docs", "unit"}

var validProofStatuses = []string{StatusPassed, StatusFailed, StatusNotRun}

var validRedactionStatuses = []string{RedactionPassed, RedactionFailed, RedactionNotRun}

var validPrerequisiteStatuses = []string{"available", "missing", "skipped"}

var validArtifactKinds = []string{
	"screenshot",
	"terminal-capture",
	"log",
	"event-summary",
	"request-summary",
	"manifest",
	"schema",
	"docs-report",
}

var validClaimSources = []string{"spec", "docs", "status", "test-plan", "quickstart"}

type Manifest struct {
	Version         string           `json:"version"`
	GeneratedAt     time.Time        `json:"generatedAt"`
	Commit          string           `json:"commit"`
	Dirty           bool             `json:"dirty"`
	PackageIdentity *PackageIdentity `json:"packageIdentity,omitempty"`
	Proofs          []ProofEntry     `json:"proofs"`
}

type PackageIdentity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ProofEntry struct {
	ProofID         string               `json:"proofId"`
	FeatureID       string               `json:"featureId"`
	Mode            string               `json:"mode"`
	EvidenceClass   string               `json:"evidenceClass"`
	Status          string               `json:"status"`
	CommandSummary  string               `json:"commandSummary"`
	CoveredClaims   []CoveredClaim       `json:"coveredClaims"`
	Prerequisites   []PrerequisiteStatus `json:"prerequisites"`
	Artifacts       []ArtifactRef        `json:"artifacts"`
	RedactionStatus string               `json:"redactionStatus"`
	NotRunReason    string               `json:"notRunReason,omitempty"`
	StartedAt       *time.Time           `json:"startedAt,omitempty"`
	EndedAt         *time.Time           `json:"endedAt,omitempty"`
	Host            *HostSummary         `json:"host,omitempty"`
	Runtime         *RuntimeBinding      `json:"runtime,omitempty"`
	Notes           []string             `json:"notes,omitempty"`
}

type CoveredClaim struct {
	ClaimID     string `json:"claimId"`
	Source      string `json:"source"`
	Description string `json:"description"`
	Scope       string `json:"scope,omitempty"`
}

type PrerequisiteStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type ArtifactRef struct {
	Kind            string `json:"kind"`
	Path            string `json:"path"`
	SHA256          string `json:"sha256,omitempty"`
	RedactionStatus string `json:"redactionStatus"`
	Description     string `json:"description,omitempty"`
}

type HostSummary struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

func NewManifest(commit string, dirty bool) Manifest {
	commit = strings.TrimSpace(commit)
	if commit == "" {
		commit = "unknown"
	}
	return Manifest{
		Version:     Schema,
		GeneratedAt: time.Now().UTC(),
		Commit:      audit.RedactString(commit),
		Dirty:       dirty,
	}
}

func CurrentHost() HostSummary {
	return HostSummary{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

func NotRunProof(proofID, featureID, mode, evidenceClass, commandSummary, reason string, claims []CoveredClaim, prereqs []PrerequisiteStatus) ProofEntry {
	return ProofEntry{
		ProofID:         proofID,
		FeatureID:       featureID,
		Mode:            mode,
		EvidenceClass:   evidenceClass,
		Status:          StatusNotRun,
		CommandSummary:  commandSummary,
		CoveredClaims:   claims,
		Prerequisites:   prereqs,
		Artifacts:       []ArtifactRef{},
		RedactionStatus: RedactionNotRun,
		NotRunReason:    reason,
	}
}

func (m Manifest) Sanitized() Manifest {
	out := m
	out.Version = audit.RedactString(out.Version)
	out.Commit = audit.RedactString(out.Commit)
	if out.PackageIdentity != nil {
		pkg := *out.PackageIdentity
		pkg.Name = audit.RedactString(pkg.Name)
		pkg.Version = audit.RedactString(pkg.Version)
		out.PackageIdentity = &pkg
	}
	out.Proofs = make([]ProofEntry, len(m.Proofs))
	for i, proof := range m.Proofs {
		out.Proofs[i] = proof.Sanitized()
	}
	return out
}

func (p ProofEntry) Sanitized() ProofEntry {
	out := p
	out.ProofID = audit.RedactString(out.ProofID)
	out.FeatureID = audit.RedactString(out.FeatureID)
	out.Mode = audit.RedactString(out.Mode)
	out.EvidenceClass = audit.RedactString(out.EvidenceClass)
	out.Status = audit.RedactString(out.Status)
	out.CommandSummary = audit.RedactString(out.CommandSummary)
	out.RedactionStatus = audit.RedactString(out.RedactionStatus)
	out.NotRunReason = audit.RedactString(out.NotRunReason)
	out.CoveredClaims = make([]CoveredClaim, len(p.CoveredClaims))
	for i, claim := range p.CoveredClaims {
		out.CoveredClaims[i] = claim.Sanitized()
	}
	out.Prerequisites = make([]PrerequisiteStatus, len(p.Prerequisites))
	for i, prereq := range p.Prerequisites {
		out.Prerequisites[i] = prereq.Sanitized()
	}
	out.Artifacts = make([]ArtifactRef, len(p.Artifacts))
	for i, artifact := range p.Artifacts {
		out.Artifacts[i] = artifact.Sanitized()
	}
	if out.Host != nil {
		host := *out.Host
		host.OS = audit.RedactString(host.OS)
		host.Arch = audit.RedactString(host.Arch)
		out.Host = &host
	}
	if out.Runtime != nil {
		runtime := out.Runtime.Sanitized()
		out.Runtime = &runtime
	}
	for i, note := range out.Notes {
		out.Notes[i] = audit.RedactString(note)
	}
	return out
}

func (c CoveredClaim) Sanitized() CoveredClaim {
	c.ClaimID = audit.RedactString(c.ClaimID)
	c.Source = audit.RedactString(c.Source)
	c.Description = audit.RedactString(c.Description)
	c.Scope = audit.RedactString(c.Scope)
	return c
}

func (p PrerequisiteStatus) Sanitized() PrerequisiteStatus {
	p.Name = audit.RedactString(p.Name)
	p.Status = audit.RedactString(p.Status)
	p.Reason = audit.RedactString(p.Reason)
	return p
}

func (a ArtifactRef) Sanitized() ArtifactRef {
	a.Kind = audit.RedactString(a.Kind)
	a.Path = audit.RedactString(a.Path)
	a.SHA256 = audit.RedactString(a.SHA256)
	a.RedactionStatus = audit.RedactString(a.RedactionStatus)
	a.Description = audit.RedactString(a.Description)
	return a
}

func (m Manifest) Validate() error {
	if m.Version != Schema {
		return fmt.Errorf("product evidence version must be %q", Schema)
	}
	if m.GeneratedAt.IsZero() {
		return errors.New("generatedAt is required")
	}
	if strings.TrimSpace(m.Commit) == "" {
		return errors.New("commit is required")
	}
	if m.PackageIdentity != nil {
		if strings.TrimSpace(m.PackageIdentity.Name) == "" || strings.TrimSpace(m.PackageIdentity.Version) == "" {
			return errors.New("packageIdentity requires name and version")
		}
	}
	if len(m.Proofs) == 0 {
		return errors.New("at least one proof is required")
	}
	for i, proof := range m.Proofs {
		if err := proof.Validate(); err != nil {
			return fmt.Errorf("proofs[%d]: %w", i, err)
		}
	}
	if ContainsControlPlaneMaterial(m) {
		return errors.New("manifest contains unredacted control-plane material")
	}
	return nil
}

func (p ProofEntry) Validate() error {
	if strings.TrimSpace(p.ProofID) == "" {
		return errors.New("proofId is required")
	}
	if strings.TrimSpace(p.FeatureID) == "" {
		return errors.New("featureId is required")
	}
	if !slices.Contains(validModes, p.Mode) {
		return fmt.Errorf("unsupported mode %q", p.Mode)
	}
	if strings.TrimSpace(p.EvidenceClass) == "" {
		return errors.New("evidenceClass is required")
	}
	if !slices.Contains(validProofStatuses, p.Status) {
		return fmt.Errorf("unsupported status %q", p.Status)
	}
	if strings.TrimSpace(p.CommandSummary) == "" {
		return errors.New("commandSummary is required")
	}
	if !slices.Contains(validRedactionStatuses, p.RedactionStatus) {
		return fmt.Errorf("unsupported redactionStatus %q", p.RedactionStatus)
	}
	if p.Status == StatusPassed && p.RedactionStatus != RedactionPassed {
		return errors.New("passed proofs require redactionStatus=passed")
	}
	if p.Status == StatusNotRun && strings.TrimSpace(p.NotRunReason) == "" && !hasMissingPrerequisiteReason(p.Prerequisites) {
		return errors.New("not-run proofs require notRunReason or a missing/skipped prerequisite reason")
	}
	if len(p.CoveredClaims) == 0 {
		return errors.New("coveredClaims is required")
	}
	for i, claim := range p.CoveredClaims {
		if err := claim.Validate(); err != nil {
			return fmt.Errorf("coveredClaims[%d]: %w", i, err)
		}
	}
	for i, prereq := range p.Prerequisites {
		if err := prereq.Validate(); err != nil {
			return fmt.Errorf("prerequisites[%d]: %w", i, err)
		}
	}
	for i, artifact := range p.Artifacts {
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("artifacts[%d]: %w", i, err)
		}
	}
	if p.Runtime != nil {
		if err := p.Runtime.Validate(); err != nil {
			return fmt.Errorf("runtime: %w", err)
		}
	}
	return nil
}

func (c CoveredClaim) Validate() error {
	if strings.TrimSpace(c.ClaimID) == "" {
		return errors.New("claimId is required")
	}
	if !slices.Contains(validClaimSources, c.Source) {
		return fmt.Errorf("unsupported source %q", c.Source)
	}
	if strings.TrimSpace(c.Description) == "" {
		return errors.New("description is required")
	}
	return nil
}

func (p PrerequisiteStatus) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("name is required")
	}
	if !slices.Contains(validPrerequisiteStatuses, p.Status) {
		return fmt.Errorf("unsupported status %q", p.Status)
	}
	if (p.Status == "missing" || p.Status == "skipped") && strings.TrimSpace(p.Reason) == "" {
		return errors.New("missing or skipped prerequisites require reason")
	}
	return nil
}

func (a ArtifactRef) Validate() error {
	if !slices.Contains(validArtifactKinds, a.Kind) {
		return fmt.Errorf("unsupported kind %q", a.Kind)
	}
	if strings.TrimSpace(a.Path) == "" {
		return errors.New("path is required")
	}
	clean := filepath.Clean(a.Path)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("artifact path %q must be relative to the evidence directory", a.Path)
	}
	if a.SHA256 != "" && !isLowerHexSHA256(a.SHA256) {
		return errors.New("sha256 must be 64 lowercase hex characters")
	}
	if !slices.Contains(validRedactionStatuses, a.RedactionStatus) {
		return fmt.Errorf("unsupported redactionStatus %q", a.RedactionStatus)
	}
	return nil
}

func ContainsControlPlaneMaterial(v any) bool {
	data, err := json.Marshal(v)
	if err != nil {
		return true
	}
	return ContainsControlPlaneBytes(data)
}

func ContainsControlPlaneBytes(data []byte) bool {
	text := string(data)
	return audit.RedactString(text) != text
}

func hasMissingPrerequisiteReason(values []PrerequisiteStatus) bool {
	for _, value := range values {
		if (value.Status == "missing" || value.Status == "skipped") && strings.TrimSpace(value.Reason) != "" {
			return true
		}
	}
	return false
}

func isLowerHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
