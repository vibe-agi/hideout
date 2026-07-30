package manager

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/decision"
	exportboundary "github.com/vibe-agi/hideout/internal/export"
)

type ExportOptions struct {
	Source                  exportboundary.SourceKind
	Session                 string
	Profile                 string
	Action                  string
	Decision                string
	Limit                   int
	BundlePath              string
	DoctorReportPath        string
	From                    string
	Activity                json.RawMessage
	PathPolicy              exportboundary.PathPolicy
	PreRedactionStages      []exportboundary.RedactionStage
	Out                     string
	Redact                  []string
	PolicyProfile           string
	AcknowledgeFullFidelity bool
	InteractiveConfirmed    bool
	Share                   bool
	ShareDecisionID         string
	ShareApproved           bool
	Commit                  string
	CreatedAt               time.Time
}

func (c Core) PlanExport(opts ExportOptions) (exportboundary.Plan, error) {
	req, err := c.exportRequest(opts)
	if err != nil {
		return exportboundary.Plan{}, err
	}
	plan, err := exportboundary.BuildPlan(req)
	if err != nil {
		return exportboundary.Plan{}, err
	}
	if opts.Share && !opts.ShareApproved {
		if plan.Review.DecisionRequired {
			return exportboundary.Plan{}, errors.New("share export still requires redaction or explicit full-fidelity acknowledgement")
		}
		d, err := c.createShareExportDecision(opts, plan)
		if err != nil {
			return exportboundary.Plan{}, err
		}
		plan.DecisionID = d.ID
	}
	return plan, nil
}

func (c Core) ApplyExport(plan exportboundary.Plan, opts ExportOptions) (result exportboundary.Result, err error) {
	if opts.Share && !opts.ShareApproved {
		return result, errors.New("share export requires approved evidence.share decision")
	}
	c.emitOperation("export", "start", exportOperationDetails(opts, plan, result, "running", ""))
	defer func() {
		status := "completed"
		phase := "complete"
		reason := ""
		if err != nil {
			status = "failed"
			phase = "failed"
			reason = err.Error()
		}
		c.emitOperation("export", phase, exportOperationDetails(opts, plan, result, status, reason))
	}()
	if plan.Artifact.Version == "" {
		err = errors.New("export plan is required")
		return result, err
	}
	req, err := c.exportRequest(opts)
	if err != nil {
		return result, err
	}
	result, err = exportboundary.ApplyPlan(req, plan)
	return result, err
}

type shareExportRequest struct {
	Version    string        `json:"version"`
	DecisionID string        `json:"decisionId"`
	Options    ExportOptions `json:"options"`
}

func (c Core) createShareExportDecision(opts ExportOptions, plan exportboundary.Plan) (decision.Decision, error) {
	if opts.Out == "" {
		return decision.Decision{}, errors.New("--out is required for share export")
	}
	id := opts.ShareDecisionID
	if id == "" {
		var err error
		id, err = newShareDecisionID()
		if err != nil {
			return decision.Decision{}, err
		}
	}
	opts.Share = false
	opts.ShareDecisionID = id
	opts.ShareApproved = true
	if err := c.saveShareExportRequest(id, opts); err != nil {
		return decision.Decision{}, err
	}
	now := time.Now().UTC()
	return c.CreateDecision(decision.Decision{
		ID:   id,
		Kind: decision.KindEvidenceShare,
		Source: decision.Source{
			Profile: opts.Profile,
			Surface: "export-share",
		},
		State: decision.StatePending,
		Risk: map[string]any{
			"leavesMachine": true,
			"recordCount":   plan.Artifact.RecordCount,
			"source":        string(plan.Artifact.Provenance.Source),
		},
		ProposedAction: map[string]any{
			"source": string(plan.Artifact.Provenance.Source),
			"out":    opts.Out,
		},
		Preview: decision.Preview{
			Summary:         fmt.Sprintf("share %d %s export records", plan.Artifact.RecordCount, plan.Artifact.Provenance.Source),
			UserDataPresent: len(plan.Review.ResidualKeys) > 0,
			Facts: map[string]any{
				"source":      string(plan.Artifact.Provenance.Source),
				"recordCount": plan.Artifact.RecordCount,
				"out":         opts.Out,
			},
		},
		AllowedActions: []string{decision.ActionApprove, decision.ActionDeny},
		DefaultOutcome: decision.DefaultOutcomeNoRelease,
		TimeoutAt:      now.Add(5 * time.Minute),
		ProviderRef: decision.ProviderRef{
			Provider:    decision.KindEvidenceShare,
			OperationID: id,
		},
		AuditRef: "audit:evidence-share:" + id,
	})
}

func newShareDecisionID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return "share_" + hex.EncodeToString(buf[:]), nil
}

func (c Core) saveShareExportRequest(id string, opts ExportOptions) error {
	if c.Store.Root == "" {
		return errors.New("manager store root is required")
	}
	dir := filepath.Join(c.Store.Root, "operator-center", "share")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(shareExportRequest{Version: "hideout.share-export-request/v1", DecisionID: id, Options: opts}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(dir, id+".json")
	tmp, err := os.CreateTemp(dir, "."+id+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keep := true
	defer func() {
		if keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	keep = false
	return nil
}

func (c Core) loadShareExportRequest(id string) (ExportOptions, error) {
	if c.Store.Root == "" {
		return ExportOptions{}, errors.New("manager store root is required")
	}
	path := filepath.Join(c.Store.Root, "operator-center", "share", id+".json")
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return ExportOptions{}, err
	}
	var req shareExportRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return ExportOptions{}, err
	}
	if req.DecisionID != id {
		return ExportOptions{}, errors.New("share export decision id mismatch")
	}
	req.Options.Share = true
	req.Options.ShareDecisionID = id
	req.Options.ShareApproved = true
	return req.Options, nil
}

func (c Core) RecordExportFailure(opts ExportOptions, reason string) error {
	req, err := c.exportRequest(opts)
	if err != nil {
		source := opts.Source
		if source == "" {
			source = exportboundary.SourceAudit
		}
		req = exportboundary.Request{
			Source:        source,
			Out:           opts.Out,
			StoreRoot:     c.Store.Root,
			ProfileName:   opts.Profile,
			PolicyProfile: opts.PolicyProfile,
			Commit:        opts.Commit,
		}
	}
	return exportboundary.RecordFailure(req, reason)
}

func (c Core) exportRequest(opts ExportOptions) (exportboundary.Request, error) {
	if c.Store.Root == "" {
		return exportboundary.Request{}, errors.New("manager store root is required")
	}
	source := opts.Source
	if source == "" {
		source = exportboundary.SourceAudit
	}
	req := exportboundary.Request{
		Source:            source,
		BundlePath:        opts.BundlePath,
		BoundaryAuditPath: opts.From,
		DoctorReportPath:  opts.DoctorReportPath,
		Activity:          opts.Activity,
		PathPolicy:        opts.PathPolicy,
		PreRedactionStages: append(
			[]exportboundary.RedactionStage(nil),
			opts.PreRedactionStages...,
		),
		Out:                     opts.Out,
		StoreRoot:               c.Store.Root,
		ProfileName:             opts.Profile,
		PolicyProfile:           opts.PolicyProfile,
		RedactSelectors:         append([]string(nil), opts.Redact...),
		AcknowledgeFullFidelity: opts.AcknowledgeFullFidelity,
		InteractiveConfirmed:    opts.InteractiveConfirmed,
		Commit:                  opts.Commit,
	}
	if !opts.CreatedAt.IsZero() {
		createdAt := opts.CreatedAt.Round(0).UTC()
		req.Now = func() time.Time { return createdAt }
	}
	switch source {
	case exportboundary.SourceAudit:
		events, err := c.AuditEvents(AuditEventFilter{
			Session:  opts.Session,
			Profile:  opts.Profile,
			Action:   opts.Action,
			Decision: opts.Decision,
			Limit:    opts.Limit,
		})
		if err != nil {
			return exportboundary.Request{}, err
		}
		req.AuditEvents = exportAuditEvents(events)
	case exportboundary.SourceActivity:
		if len(opts.Activity) == 0 {
			return exportboundary.Request{}, errors.New(
				"activity export source is required",
			)
		}
	case exportboundary.SourceBoundarySummary:
		if opts.From == "" {
			return exportboundary.Request{}, errors.New("--from is required for boundary-summary export")
		}
		req.BoundarySummary = SummarizeRunBoundary(opts.From)
	case exportboundary.SourceBundle:
		if opts.BundlePath == "" {
			return exportboundary.Request{}, errors.New("--bundle is required for bundle export")
		}
	case exportboundary.SourceDoctorReport:
		if opts.DoctorReportPath == "" {
			return exportboundary.Request{}, errors.New("--doctor-report is required for doctor-report export")
		}
	default:
		return exportboundary.Request{}, errors.New("unsupported export source")
	}
	return req, nil
}

func exportAuditEvents(events []audit.Event) []exportboundary.AuditEvent {
	out := make([]exportboundary.AuditEvent, len(events))
	for i, event := range events {
		out[i] = exportboundary.AuditEvent{
			Time:     event.Time,
			Session:  event.Session,
			Profile:  event.Profile,
			Backend:  event.Backend,
			Action:   event.Action,
			Decision: event.Decision,
			Details:  event.Details,
		}
	}
	return out
}

func exportOperationDetails(opts ExportOptions, plan exportboundary.Plan, result exportboundary.Result, status, reason string) map[string]any {
	source := opts.Source
	if source == "" {
		source = exportboundary.SourceAudit
	}
	artifactPath := result.ArtifactPath
	if artifactPath == "" {
		artifactPath = opts.Out
	}
	id := artifactPath
	if id == "" {
		id = string(source)
	}
	details := map[string]any{
		"id":           id,
		"source":       string(source),
		"out":          opts.Out,
		"artifactPath": artifactPath,
		"profile":      opts.Profile,
		"status":       status,
	}
	if decision := plan.Artifact.Provenance.Decision.Mode; decision != "" {
		details["decision"] = string(decision)
	}
	if reason != "" {
		details["reason"] = reason
	}
	return details
}
