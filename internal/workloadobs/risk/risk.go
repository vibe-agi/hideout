// Package risk evaluates redacted activity with a small, versioned catalog of
// deterministic rules. Findings describe observed evidence; policy status is
// carried separately and never changes the rule's severity.
package risk

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	workloadobs "github.com/vibe-agi/hideout/internal/workloadobs"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	FindingSchema = "hideout.risk-finding.v1"

	PolicyAllowed      = "allowed"
	PolicyDenied       = "denied"
	PolicyNotEvaluated = "not-evaluated"

	PolicyDispositionAllowedObserved = "allowed-observed"
	PolicyDispositionPrevented       = "denied-prevented"
	PolicyDispositionViolation       = "policy-violation"
	PolicyDispositionNotEvaluated    = "not-evaluated"

	ConfidenceExact    = "exact"
	ConfidenceInferred = "inferred"
	ConfidenceLimited  = "limited"

	SeverityInfo     = "info"
	SeverityLow      = "low"
	SeverityMedium   = "medium"
	SeverityHigh     = "high"
	SeverityCritical = "critical"

	maxRules    = 128
	maxEvidence = 65536
)

var (
	ErrInvalidRuleSet  = errors.New("risk rule set is invalid")
	ErrInvalidEvidence = errors.New("risk evidence is invalid")
	ErrEvidenceRebound = errors.New("risk evidence id is bound to different evidence")
	ErrInvalidFinding  = errors.New("risk finding is invalid")

	ruleIDPattern     = regexp.MustCompile(`^[a-z][a-z0-9.-]{2,127}$`)
	versionPattern    = regexp.MustCompile(`^v[1-9][0-9]{0,15}$`)
	actionPattern     = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
	sessionIDPattern  = regexp.MustCompile(`^ses_[A-Za-z0-9_-]{1,124}$`)
	activityIDPattern = regexp.MustCompile(`^act_[A-Za-z0-9_-]{8,124}$`)
	coverageIDPattern = regexp.MustCompile(`^cov_[A-Za-z0-9_-]{8,124}$`)
	findingIDPattern  = regexp.MustCompile(`^risk_[A-Za-z0-9_-]{16,124}$`)
)

type Evidence struct {
	Activity     workloadtypes.ActivityRecord
	PolicyStatus string
}

type MatchFunc func(workloadtypes.ActivityRecord) (groupKey string, matched bool)

type Rule struct {
	ID           string
	Version      string
	Severity     string
	Title        string
	Explanation  string
	NextAction   string
	PreserveEach bool
	Match        MatchFunc
}

type RuleSet struct {
	Version string
	Rules   []Rule
}

type Engine struct {
	version string
	rules   []Rule
}

type Finding struct {
	Schema            string                      `json:"schema"`
	ID                string                      `json:"id"`
	Owner             workloadtypes.ActivityOwner `json:"owner"`
	SessionID         string                      `json:"sessionId"`
	CoverageID        string                      `json:"coverageId"`
	RuleSetVersion    string                      `json:"ruleSetVersion"`
	RuleID            string                      `json:"ruleId"`
	RuleVersion       string                      `json:"ruleVersion"`
	Severity          string                      `json:"severity"`
	Confidence        string                      `json:"confidence"`
	PolicyStatus      string                      `json:"policyStatus"`
	PolicyDisposition string                      `json:"policyDisposition"`
	Title             string                      `json:"title"`
	Explanation       string                      `json:"explanation"`
	NextAction        string                      `json:"nextAction"`
	Count             uint64                      `json:"count"`
	CountTruncated    bool                        `json:"countTruncated,omitempty"`
	EvidenceRefs      []string                    `json:"evidenceRefs"`
	FirstAt           time.Time                   `json:"firstAt"`
	LastAt            time.Time                   `json:"lastAt"`
}

func DefaultRuleSet() RuleSet {
	return RuleSet{
		Version: workloadobs.DefaultRiskRuleSetVersion,
		Rules: []Rule{
			{
				ID:       "file.write-outside-workspace",
				Version:  workloadobs.DefaultRiskRuleVersion,
				Severity: workloadobs.DefaultRiskOutsideWorkspaceSeverity,
				Title:    "File changed outside the workspace",
				Explanation: "A workload execution changed a file in a resolved non-workspace path. " +
					"This is an observed action, not a claim that policy allowed or prevented it.",
				NextAction: "activity.files",
				Match:      matchWriteOutsideWorkspace,
			},
			{
				ID:       "file.destructive-change",
				Version:  workloadobs.DefaultRiskRuleVersion,
				Severity: workloadobs.DefaultRiskDestructiveFileSeverity,
				Title:    "Destructive file change observed",
				Explanation: "A destructive file operation was observed. Each operation remains " +
					"separate so its exact evidence can be reviewed.",
				NextAction:   "activity.files",
				PreserveEach: true,
				Match:        matchDestructiveFile,
			},
			{
				ID:       "process.root-execution",
				Version:  workloadobs.DefaultRiskRuleVersion,
				Severity: workloadobs.DefaultRiskRootExecutionSeverity,
				Title:    "Workload execution ran as root",
				Explanation: "A process attributed to the workload was observed with guest UID 0. " +
					"Supported Hideout targets are expected to remain non-root.",
				NextAction:   "activity.executions",
				PreserveEach: true,
				Match:        matchRootExecution,
			},
		},
	}
}

func NewEngine(ruleSet RuleSet) (*Engine, error) {
	if err := ruleSet.validate(); err != nil {
		return nil, err
	}
	return &Engine{
		version: ruleSet.Version,
		rules:   append([]Rule(nil), ruleSet.Rules...),
	}, nil
}

func (ruleSet RuleSet) validate() error {
	if !versionPattern.MatchString(ruleSet.Version) ||
		len(ruleSet.Rules) == 0 || len(ruleSet.Rules) > maxRules {
		return ErrInvalidRuleSet
	}
	seen := make(map[string]struct{}, len(ruleSet.Rules))
	for _, rule := range ruleSet.Rules {
		if !ruleIDPattern.MatchString(rule.ID) ||
			!versionPattern.MatchString(rule.Version) ||
			!validSeverity(rule.Severity) ||
			!boundedText(rule.Title, 1, 256) ||
			!boundedText(rule.Explanation, 1, 2048) ||
			!actionPattern.MatchString(rule.NextAction) ||
			rule.Match == nil {
			return ErrInvalidRuleSet
		}
		if _, exists := seen[rule.ID]; exists {
			return ErrInvalidRuleSet
		}
		seen[rule.ID] = struct{}{}
	}
	return nil
}

func (engine *Engine) Evaluate(values []Evidence) ([]Finding, error) {
	if engine == nil || !versionPattern.MatchString(engine.version) ||
		len(engine.rules) == 0 || len(values) > maxEvidence {
		return nil, ErrInvalidEvidence
	}
	evidence, err := normalizeEvidence(values)
	if err != nil {
		return nil, err
	}
	sort.Slice(evidence, func(left, right int) bool {
		return lessEvidence(evidence[left], evidence[right])
	})

	groups := make(map[string]*Finding)
	for _, item := range evidence {
		confidence := confidenceFor(item.Activity.Attribution)
		disposition := policyDisposition(item)
		for _, rule := range engine.rules {
			subjectKey, matched := rule.Match(cloneActivity(item.Activity))
			if !matched {
				continue
			}
			if subjectKey == "" || len(subjectKey) > 16384 || containsControl(subjectKey) {
				return nil, ErrInvalidRuleSet
			}
			groupKey, err := findingGroupKey(
				engine.version,
				rule,
				item,
				subjectKey,
				confidence,
				disposition,
			)
			if err != nil {
				return nil, errors.Join(ErrInvalidEvidence, err)
			}
			finding := groups[groupKey]
			if finding == nil {
				id := findingID(groupKey)
				finding = &Finding{
					Schema: FindingSchema, ID: id,
					Owner: item.Activity.Owner, SessionID: item.Activity.SessionID,
					CoverageID:     item.Activity.CoverageID,
					RuleSetVersion: engine.version,
					RuleID:         rule.ID, RuleVersion: rule.Version,
					Severity: rule.Severity, Confidence: confidence,
					PolicyStatus: item.PolicyStatus, PolicyDisposition: disposition,
					Title: rule.Title, Explanation: rule.Explanation,
					NextAction: rule.NextAction,
					FirstAt:    item.Activity.FirstAt, LastAt: item.Activity.LastAt,
				}
				groups[groupKey] = finding
			}
			if item.Activity.Count > math.MaxUint64-finding.Count {
				finding.Count = math.MaxUint64
				finding.CountTruncated = true
			} else {
				finding.Count += item.Activity.Count
			}
			if item.Activity.FirstAt.Before(finding.FirstAt) {
				finding.FirstAt = item.Activity.FirstAt
			}
			if item.Activity.LastAt.After(finding.LastAt) {
				finding.LastAt = item.Activity.LastAt
			}
			finding.EvidenceRefs = append(finding.EvidenceRefs, item.Activity.ID)
		}
	}

	findings := make([]Finding, 0, len(groups))
	for _, finding := range groups {
		sort.Strings(finding.EvidenceRefs)
		if err := finding.Validate(); err != nil {
			return nil, err
		}
		findings = append(findings, cloneFinding(*finding))
	}
	sort.Slice(findings, func(left, right int) bool {
		return lessFinding(findings[left], findings[right])
	})
	return findings, nil
}

func normalizeEvidence(values []Evidence) ([]Evidence, error) {
	seen := make(map[string]string, len(values))
	result := make([]Evidence, 0, len(values))
	for _, input := range values {
		item := Evidence{
			Activity:     cloneActivity(input.Activity),
			PolicyStatus: input.PolicyStatus,
		}
		if err := item.Activity.ValidatePersistable(); err != nil ||
			!validPolicyStatus(item.PolicyStatus) ||
			(item.Activity.Owner.Kind == workloadtypes.OwnerDisposableSession &&
				item.Activity.Owner.SessionID != item.Activity.SessionID) {
			return nil, ErrInvalidEvidence
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, errors.Join(ErrInvalidEvidence, err)
		}
		digest := string(encoded)
		if previous, exists := seen[item.Activity.ID]; exists {
			if previous != digest {
				return nil, ErrEvidenceRebound
			}
			continue
		}
		seen[item.Activity.ID] = digest
		result = append(result, item)
	}
	return result, nil
}

func findingGroupKey(
	ruleSetVersion string,
	rule Rule,
	evidence Evidence,
	subjectKey, confidence, disposition string,
) (string, error) {
	value := struct {
		RuleSetVersion    string                      `json:"ruleSetVersion"`
		RuleID            string                      `json:"ruleId"`
		RuleVersion       string                      `json:"ruleVersion"`
		Owner             workloadtypes.ActivityOwner `json:"owner"`
		SessionID         string                      `json:"sessionId"`
		CoverageID        string                      `json:"coverageId"`
		SubjectKey        string                      `json:"subjectKey"`
		Confidence        string                      `json:"confidence"`
		PolicyStatus      string                      `json:"policyStatus"`
		PolicyDisposition string                      `json:"policyDisposition"`
		EvidenceID        string                      `json:"evidenceId,omitempty"`
	}{
		RuleSetVersion: ruleSetVersion,
		RuleID:         rule.ID, RuleVersion: rule.Version,
		Owner: evidence.Activity.Owner, SessionID: evidence.Activity.SessionID,
		CoverageID: evidence.Activity.CoverageID, SubjectKey: subjectKey,
		Confidence: confidence, PolicyStatus: evidence.PolicyStatus,
		PolicyDisposition: disposition,
	}
	if rule.PreserveEach {
		value.EvidenceID = evidence.Activity.ID
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func matchWriteOutsideWorkspace(
	record workloadtypes.ActivityRecord,
) (string, bool) {
	if record.Kind != workloadtypes.ActivityFile ||
		record.Count < workloadobs.DefaultRiskMinimumEvidenceCount ||
		!fileMutation(record.Operation) {
		return "", false
	}
	subject, ok := record.Subject.(workloadtypes.FileSubject)
	if !ok || subject.PathClass == "workspace" ||
		subject.PathClass == "runtime" || subject.PathClass == "unknown" {
		return "", false
	}
	return semanticActivityKey(record), true
}

func matchDestructiveFile(
	record workloadtypes.ActivityRecord,
) (string, bool) {
	if record.Kind != workloadtypes.ActivityFile ||
		record.Count < workloadobs.DefaultRiskMinimumEvidenceCount {
		return "", false
	}
	subject, ok := record.Subject.(workloadtypes.FileSubject)
	if !ok || (!subject.Destructive && !destructiveFileOperation(record.Operation)) {
		return "", false
	}
	return semanticActivityKey(record), true
}

func matchRootExecution(
	record workloadtypes.ActivityRecord,
) (string, bool) {
	if record.Kind != workloadtypes.ActivityProcess {
		return "", false
	}
	subject, ok := record.Subject.(workloadtypes.ProcessSubject)
	if !ok || record.Count < workloadobs.DefaultRiskMinimumEvidenceCount ||
		((record.Actor == nil ||
			record.Actor.UID != workloadobs.DefaultRiskPrivilegedUID) &&
			subject.GuestIdentity.UID !=
				workloadobs.DefaultRiskPrivilegedUID) {
		return "", false
	}
	return semanticActivityKey(record), true
}

func semanticActivityKey(record workloadtypes.ActivityRecord) string {
	value := cloneActivity(record)
	value.ID = ""
	value.Count = 0
	value.Bytes = 0
	value.FirstAt = time.Time{}
	value.LastAt = time.Time{}
	value.FirstSequence = 0
	value.LastSequence = 0
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func fileMutation(operation string) bool {
	switch operation {
	case "write", "create", "truncate", "rename", "unlink", "metadata",
		"mkdir", "rmdir", "symlink", "hardlink", "mmap-write":
		return true
	default:
		return false
	}
}

func destructiveFileOperation(operation string) bool {
	switch operation {
	case "truncate", "rename", "unlink", "delete", "remove", "rmdir":
		return true
	default:
		return false
	}
}

func confidenceFor(attribution string) string {
	switch attribution {
	case workloadtypes.AttributionExact:
		return ConfidenceExact
	case workloadtypes.AttributionInferred:
		return ConfidenceInferred
	default:
		return ConfidenceLimited
	}
}

func policyDisposition(evidence Evidence) string {
	switch evidence.PolicyStatus {
	case PolicyAllowed:
		return PolicyDispositionAllowedObserved
	case PolicyDenied:
		if evidence.Activity.Outcome.Status == workloadtypes.OutcomeDenied {
			return PolicyDispositionPrevented
		}
		return PolicyDispositionViolation
	default:
		return PolicyDispositionNotEvaluated
	}
}

func findingID(groupKey string) string {
	digest := sha256.Sum256([]byte(groupKey))
	return "risk_" + base64.RawURLEncoding.EncodeToString(digest[:18])
}

func (finding Finding) Validate() error {
	if finding.Schema != FindingSchema ||
		!findingIDPattern.MatchString(finding.ID) ||
		finding.Owner.Validate() != nil ||
		!sessionIDPattern.MatchString(finding.SessionID) ||
		(finding.Owner.Kind == workloadtypes.OwnerDisposableSession &&
			finding.Owner.SessionID != finding.SessionID) ||
		!coverageIDPattern.MatchString(finding.CoverageID) ||
		!versionPattern.MatchString(finding.RuleSetVersion) ||
		!ruleIDPattern.MatchString(finding.RuleID) ||
		!versionPattern.MatchString(finding.RuleVersion) ||
		!validSeverity(finding.Severity) ||
		!validConfidence(finding.Confidence) ||
		!validPolicyStatus(finding.PolicyStatus) ||
		!validPolicyDisposition(finding.PolicyStatus, finding.PolicyDisposition) ||
		!boundedText(finding.Title, 1, 256) ||
		!boundedText(finding.Explanation, 1, 2048) ||
		!actionPattern.MatchString(finding.NextAction) ||
		finding.Count == 0 ||
		finding.FirstAt.IsZero() || finding.LastAt.IsZero() ||
		finding.LastAt.Before(finding.FirstAt) ||
		len(finding.EvidenceRefs) == 0 ||
		len(finding.EvidenceRefs) > maxEvidence {
		return ErrInvalidFinding
	}
	previous := ""
	for _, reference := range finding.EvidenceRefs {
		if !activityIDPattern.MatchString(reference) || reference <= previous {
			return ErrInvalidFinding
		}
		previous = reference
	}
	return nil
}

func validPolicyDisposition(status, disposition string) bool {
	switch status {
	case PolicyAllowed:
		return disposition == PolicyDispositionAllowedObserved
	case PolicyDenied:
		return disposition == PolicyDispositionPrevented ||
			disposition == PolicyDispositionViolation
	case PolicyNotEvaluated:
		return disposition == PolicyDispositionNotEvaluated
	default:
		return false
	}
}

func validPolicyStatus(value string) bool {
	switch value {
	case PolicyAllowed, PolicyDenied, PolicyNotEvaluated:
		return true
	default:
		return false
	}
}

func validConfidence(value string) bool {
	switch value {
	case ConfidenceExact, ConfidenceInferred, ConfidenceLimited:
		return true
	default:
		return false
	}
}

func validSeverity(value string) bool {
	switch value {
	case SeverityInfo, SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical:
		return true
	default:
		return false
	}
}

func lessEvidence(left, right Evidence) bool {
	switch {
	case left.Activity.FirstAt.Before(right.Activity.FirstAt):
		return true
	case right.Activity.FirstAt.Before(left.Activity.FirstAt):
		return false
	case left.Activity.FirstSequence != right.Activity.FirstSequence:
		return left.Activity.FirstSequence < right.Activity.FirstSequence
	case left.Activity.Owner.String() != right.Activity.Owner.String():
		return left.Activity.Owner.String() < right.Activity.Owner.String()
	case left.Activity.SessionID != right.Activity.SessionID:
		return left.Activity.SessionID < right.Activity.SessionID
	case left.PolicyStatus != right.PolicyStatus:
		return left.PolicyStatus < right.PolicyStatus
	default:
		return left.Activity.ID < right.Activity.ID
	}
}

func lessFinding(left, right Finding) bool {
	switch {
	case left.FirstAt.Before(right.FirstAt):
		return true
	case right.FirstAt.Before(left.FirstAt):
		return false
	case left.Owner.String() != right.Owner.String():
		return left.Owner.String() < right.Owner.String()
	case left.SessionID != right.SessionID:
		return left.SessionID < right.SessionID
	case left.RuleID != right.RuleID:
		return left.RuleID < right.RuleID
	case left.CoverageID != right.CoverageID:
		return left.CoverageID < right.CoverageID
	default:
		return left.ID < right.ID
	}
}

func cloneFinding(finding Finding) Finding {
	cloned := finding
	cloned.EvidenceRefs = append([]string(nil), finding.EvidenceRefs...)
	return cloned
}

func cloneActivity(record workloadtypes.ActivityRecord) workloadtypes.ActivityRecord {
	cloned := record
	cloned.FirstAt = stripMonotonic(record.FirstAt)
	cloned.LastAt = stripMonotonic(record.LastAt)
	if record.Actor != nil {
		value := *record.Actor
		cloned.Actor = &value
	}
	if record.Mediator != nil {
		value := *record.Mediator
		cloned.Mediator = &value
	}
	if record.Outcome.Code != nil {
		value := *record.Outcome.Code
		cloned.Outcome.Code = &value
	}
	cloned.Truncation = append([]string(nil), record.Truncation...)
	sort.Strings(cloned.Truncation)
	switch subject := record.Subject.(type) {
	case workloadtypes.ProcessSubject:
		subject.Argv = append([]string(nil), subject.Argv...)
		cloned.Subject = subject
	case *workloadtypes.ProcessSubject:
		if subject != nil {
			value := *subject
			value.Argv = append([]string(nil), subject.Argv...)
			cloned.Subject = value
		}
	case workloadtypes.FileSubject:
		cloned.Subject = subject
	case *workloadtypes.FileSubject:
		if subject != nil {
			cloned.Subject = *subject
		}
	case workloadtypes.NetworkSubject:
		cloned.Subject = subject
	case *workloadtypes.NetworkSubject:
		if subject != nil {
			cloned.Subject = *subject
		}
	case workloadtypes.DNSSubject:
		subject.Answers = append([]string(nil), subject.Answers...)
		cloned.Subject = subject
	case *workloadtypes.DNSSubject:
		if subject != nil {
			value := *subject
			value.Answers = append([]string(nil), subject.Answers...)
			cloned.Subject = value
		}
	case workloadtypes.GenericSubject:
		cloned.Subject = subject
	case *workloadtypes.GenericSubject:
		if subject != nil {
			cloned.Subject = *subject
		}
	}
	return cloned
}

func stripMonotonic(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.Round(0).UTC()
}

func boundedText(value string, minimum, maximum int) bool {
	return len(value) >= minimum && len(value) <= maximum &&
		strings.TrimSpace(value) == value && !containsControl(value)
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}
