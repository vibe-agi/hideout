package export

import (
	"path/filepath"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/session"
)

type metaAuditResult struct {
	Path    string
	Session string
}

func emitMetaAudit(req Request, artifact Artifact, decision, reason string) (metaAuditResult, error) {
	if req.StoreRoot == "" {
		return metaAuditResult{}, nil
	}
	layout, err := session.New(req.StoreRoot)
	if err != nil {
		return metaAuditResult{}, err
	}
	aw, err := audit.NewFile(layout.AuditPath)
	if err != nil {
		return metaAuditResult{}, err
	}
	details := map[string]any{
		"source":          string(req.Source),
		"recordCount":     artifact.RecordCount,
		"redactionStages": artifact.Provenance.RedactionStages,
		"decision":        artifact.Provenance.Decision.Mode,
		"decisionChannel": artifact.Provenance.Decision.Channel,
		"out":             req.Out,
	}
	if reason != "" {
		details["reason"] = reason
	}
	profileName := req.ProfileName
	if profileName == "" {
		profileName = req.PolicyProfile
	}
	if profileName == "" {
		profileName = "default"
	}
	if err := aw.Emit(audit.Event{
		Session:  layout.ID,
		Profile:  profileName,
		Backend:  "native",
		Action:   Action,
		Decision: decision,
		Details:  details,
	}); err != nil {
		_ = aw.Close()
		_, _ = session.CleanupEphemeral(req.StoreRoot, layout.ID, false)
		return metaAuditResult{}, err
	}
	if err := aw.Close(); err != nil {
		_, _ = session.CleanupEphemeral(req.StoreRoot, layout.ID, false)
		return metaAuditResult{}, err
	}
	_, _ = session.CleanupEphemeral(req.StoreRoot, layout.ID, false)
	return metaAuditResult{Path: filepath.Clean(layout.AuditPath), Session: layout.ID}, nil
}

func emitFailureMetaAudit(req Request, reason string) error {
	artifact := Artifact{
		Version: ArtifactVersion,
		Provenance: Provenance{
			Source:          req.Source,
			RedactionStages: []RedactionStage{{Name: "control-plane"}},
			Decision:        ExportDecision{},
		},
	}
	_, err := emitMetaAudit(req, artifact, "deny", reason)
	return err
}
