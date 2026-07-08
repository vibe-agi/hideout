package manager

import (
	"errors"

	"github.com/vibe-agi/hideout/internal/audit"
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
	From                    string
	Out                     string
	Redact                  []string
	PolicyProfile           string
	AcknowledgeFullFidelity bool
	InteractiveConfirmed    bool
	Commit                  string
}

func (c Core) PlanExport(opts ExportOptions) (exportboundary.Plan, error) {
	req, err := c.exportRequest(opts)
	if err != nil {
		return exportboundary.Plan{}, err
	}
	return exportboundary.BuildPlan(req)
}

func (c Core) ApplyExport(plan exportboundary.Plan, opts ExportOptions) (result exportboundary.Result, err error) {
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
	result, err = exportboundary.Apply(req)
	return result, err
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
		Source:                  source,
		BundlePath:              opts.BundlePath,
		BoundaryAuditPath:       opts.From,
		Out:                     opts.Out,
		StoreRoot:               c.Store.Root,
		ProfileName:             opts.Profile,
		PolicyProfile:           opts.PolicyProfile,
		RedactSelectors:         append([]string(nil), opts.Redact...),
		AcknowledgeFullFidelity: opts.AcknowledgeFullFidelity,
		InteractiveConfirmed:    opts.InteractiveConfirmed,
		Commit:                  opts.Commit,
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
	case exportboundary.SourceBoundarySummary:
		if opts.From == "" {
			return exportboundary.Request{}, errors.New("--from is required for boundary-summary export")
		}
		req.BoundarySummary = SummarizeRunBoundary(opts.From)
	case exportboundary.SourceBundle:
		if opts.BundlePath == "" {
			return exportboundary.Request{}, errors.New("--bundle is required for bundle export")
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
