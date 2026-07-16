package doctor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/recovery"
)

const (
	Schema = "hideout.doctor-report/v1"

	StatusPass        = "pass"
	StatusWarn        = "warn"
	StatusError       = "error"
	StatusSkipped     = "skipped"
	StatusUnsupported = "unsupported"

	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"

	LevelLight = "light"
	LevelDeep  = "deep"
)

var SupportedFeatures = []string{
	"adapters",
	"cleanup",
	"daemon",
	"decisions",
	"dns",
	"export",
	"hostfs",
	"lima",
	"packaging",
	"privilege",
	"projection",
	"runtime",
	"sessions",
}

type Request struct {
	Profile      string   `json:"profile"`
	Backend      string   `json:"backend"`
	Workspace    string   `json:"workspace,omitempty"`
	Level        string   `json:"level"`
	Features     []string `json:"features,omitempty"`
	EvidencePath string   `json:"evidencePath,omitempty"`
}

type Check struct {
	ID       string
	Category string
	Title    string
	Level    string
	Features []string
	Required bool
}

type Finding struct {
	CheckID      string         `json:"checkId"`
	Category     string         `json:"category"`
	Status       string         `json:"status"`
	Severity     string         `json:"severity"`
	Required     bool           `json:"required"`
	Code         string         `json:"code,omitempty"`
	Reason       string         `json:"reason,omitempty"`
	Hint         string         `json:"hint,omitempty"`
	Summary      string         `json:"summary"`
	Details      map[string]any `json:"details,omitempty"`
	NextActions  []string       `json:"nextActions,omitempty"`
	EvidenceRefs []string       `json:"evidenceRefs,omitempty"`
}

type Summary struct {
	Pass        int  `json:"pass"`
	Warn        int  `json:"warn"`
	Error       int  `json:"error"`
	Skipped     int  `json:"skipped"`
	Unsupported int  `json:"unsupported"`
	Failed      bool `json:"failed"`
	ExitCode    int  `json:"exitCode"`
}

type Redaction struct {
	Mode string `json:"mode"`
}

type Report struct {
	Schema      string    `json:"schema"`
	GeneratedAt time.Time `json:"generatedAt"`
	Profile     string    `json:"profile"`
	Backend     string    `json:"backend"`
	Level       string    `json:"level"`
	Features    []string  `json:"features,omitempty"`
	Summary     Summary   `json:"summary"`
	Findings    []Finding `json:"findings"`
	Redaction   Redaction `json:"redaction"`
	Evidence    string    `json:"evidence,omitempty"`
}

type Builder struct {
	report Report
}

func NewBuilder(req Request) *Builder {
	level := strings.TrimSpace(req.Level)
	if level == "" {
		level = LevelLight
	}
	features := NormalizeFeatures(req.Features)
	return &Builder{report: Report{
		Schema:      Schema,
		GeneratedAt: time.Now().UTC(),
		Profile:     audit.RedactString(strings.TrimSpace(req.Profile)),
		Backend:     audit.RedactString(strings.TrimSpace(req.Backend)),
		Level:       level,
		Features:    features,
		Redaction:   Redaction{Mode: "control-plane"},
	}}
}

func (b *Builder) Add(checkID, category, status, summary string, opts ...FindingOption) {
	if b == nil {
		return
	}
	f := Finding{
		CheckID:  stableID(checkID),
		Category: stableID(category),
		Status:   normalizeStatus(status),
		Severity: severityForStatus(status),
		Required: normalizeStatus(status) == StatusError,
		Summary:  audit.RedactString(summary),
	}
	for _, opt := range opts {
		opt(&f)
	}
	f.Summary = audit.RedactString(f.Summary)
	f.Reason = audit.RedactString(f.Reason)
	f.Hint = audit.RedactString(f.Hint)
	f.Details = RedactDetails(f.Details)
	f.NextActions = redactStrings(f.NextActions)
	f.EvidenceRefs = redactStrings(f.EvidenceRefs)
	b.report.Findings = append(b.report.Findings, f)
}

// AddHostAppInspection translates the shared Manager inspection into doctor
// findings. Package-provided installation hints are intentionally not copied
// into summary, details, recovery, or next actions; doctor never executes or
// promotes package prose.
func (b *Builder) AddHostAppInspection(inspection hostapppack.Inspection, recoveryByCommand map[string]string) {
	for _, entry := range inspection.Entries {
		status := StatusPass
		switch entry.Summary.Readiness {
		case "review-required", "new-run-required", "disabled":
			status = StatusWarn
		case "unavailable":
			status = StatusError
		}
		details := map[string]any{
			"command":           entry.Summary.Command,
			"packId":            entry.Package.ID,
			"revisionId":        entry.Package.RevisionID,
			"readiness":         entry.Summary.Readiness,
			"access":            entry.Summary.Access,
			"permissionStatus":  entry.Permissions.Status,
			"permissionDiff":    append([]string(nil), entry.Permissions.Diff...),
			"identity":          entry.AppIdentity.Verification,
			"safety":            entry.Safety.Posture,
			"shadowStatus":      entry.Binding.ShadowStatus,
			"grantState":        entry.Runtime.GrantState,
			"lastOutcome":       entry.Runtime.LastOutcome,
			"resourceKinds":     append([]string(nil), entry.Binding.ResourceKinds...),
			"qualityTestStatus": entry.Package.TestStatus,
			"capability":        entry.Binding.CapabilityID,
			"resultPolicy":      entry.Binding.ResultPolicy,
		}
		opts := []FindingOption{WithRequired(false), WithDetails(details)}
		if entry.Summary.NextAction != "" {
			opts = append(opts, WithNextActions(entry.Summary.NextAction))
		}
		if code := recoveryByCommand[entry.Summary.Command]; code != "" {
			opts = append(opts, WithRecovery(code))
		}
		b.Add("host-app-"+entry.Summary.Command, "host-app", status,
			"host-app binding "+entry.Summary.Command+" is "+entry.Summary.Readiness, opts...)
	}
}

func (b *Builder) Report() Report {
	if b == nil {
		return Report{}
	}
	out := b.report
	out.Summary = Summarize(out.Findings)
	return out
}

func (b *Builder) WriteEvidence(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("doctor evidence path is required")
	}
	clean := filepath.Clean(path)
	report := b.Report()
	report.Evidence = audit.RedactString(clean)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(clean), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(clean), "."+filepath.Base(clean)+".tmp-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, clean); err != nil {
		return "", err
	}
	keepTemp = false
	b.report.Evidence = audit.RedactString(clean)
	return clean, nil
}

type FindingOption func(*Finding)

func WithRequired(required bool) FindingOption {
	return func(f *Finding) { f.Required = required }
}

func WithDetails(details map[string]any) FindingOption {
	return func(f *Finding) { f.Details = details }
}

func WithNextActions(actions ...string) FindingOption {
	return func(f *Finding) { f.NextActions = append(f.NextActions, actions...) }
}

func WithEvidenceRefs(refs ...string) FindingOption {
	return func(f *Finding) { f.EvidenceRefs = append(f.EvidenceRefs, refs...) }
}

func WithRecovery(code string) FindingOption {
	return func(f *Finding) {
		entry, ok := recovery.Lookup(code)
		if !ok {
			return
		}
		f.Code = entry.Code
		f.Reason = entry.Reason
		f.Hint = entry.Hint
		f.NextActions = append(f.NextActions, entry.NextActions...)
	}
}

func Summarize(findings []Finding) Summary {
	var s Summary
	for _, f := range findings {
		switch normalizeStatus(f.Status) {
		case StatusPass:
			s.Pass++
		case StatusWarn:
			s.Warn++
		case StatusError:
			s.Error++
			if f.Required {
				s.Failed = true
			}
		case StatusSkipped:
			s.Skipped++
		case StatusUnsupported:
			s.Unsupported++
		}
	}
	if s.Failed {
		s.ExitCode = 1
	}
	return s
}

func RedactDetails(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[audit.RedactKey(key)] = audit.RedactValue(key, value)
	}
	return out
}

func redactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = audit.RedactString(value)
	}
	return out
}

func normalizeStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "ok", StatusPass:
		return StatusPass
	case StatusWarn:
		return StatusWarn
	case StatusError:
		return StatusError
	case StatusSkipped:
		return StatusSkipped
	case StatusUnsupported:
		return StatusUnsupported
	default:
		return StatusError
	}
}

func severityForStatus(status string) string {
	switch normalizeStatus(status) {
	case StatusError:
		return SeverityError
	case StatusWarn:
		return SeverityWarning
	default:
		return SeverityInfo
	}
}

func stableID(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, " ", "-")
	return value
}

func NormalizeFeatures(features []string) []string {
	if len(features) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, feature := range features {
		feature = stableID(feature)
		if feature == "" || seen[feature] {
			continue
		}
		seen[feature] = true
		out = append(out, feature)
	}
	sort.Strings(out)
	return out
}
