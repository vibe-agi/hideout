package export

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

func BuildPlan(req Request) (Plan, error) {
	if err := validateRequest(req, false); err != nil {
		return Plan{}, err
	}
	source, err := readSource(req)
	if err != nil {
		return Plan{}, err
	}
	redaction, err := redactForExport(req, source.Body)
	if err != nil && !redaction.DecisionRequired {
		return Plan{}, err
	}
	artifact := Artifact{
		Version: ArtifactVersion,
		Provenance: Provenance{
			Source:          req.Source,
			Commit:          req.Commit,
			CreatedAt:       nowUTC(req),
			RedactionStages: redaction.Stages,
			Decision:        redaction.Decision,
		},
		RecordCount: source.RecordCount,
		Body:        redaction.Body,
		Notice:      source.Notice,
	}
	review := BuildReview(req.Source, source.RecordCount, redaction.Stages, req.RedactSelectors, redaction.Residual, redaction.Decision, redaction.DecisionRequired)
	return Plan{Artifact: artifact, Review: review}, nil
}

func Apply(req Request) (Result, error) {
	if err := validateRequest(req, true); err != nil {
		_ = emitFailureMetaAudit(req, err.Error())
		return Result{}, err
	}
	plan, err := BuildPlan(req)
	if err != nil {
		_ = emitFailureMetaAudit(req, err.Error())
		return Result{}, err
	}
	if plan.Review.DecisionRequired {
		err := errors.New("user data is present; choose --redact or --acknowledge-full-fidelity")
		_ = emitFailureMetaAudit(req, err.Error())
		return Result{}, err
	}
	if err := writeArtifact(req.Out, plan.Artifact); err != nil {
		_ = emitFailureMetaAudit(req, err.Error())
		return Result{}, err
	}
	meta, err := emitMetaAudit(req, plan.Artifact, "allow", "")
	if err != nil {
		return Result{}, err
	}
	return Result{
		ArtifactPath:     filepath.Clean(req.Out),
		MetaAuditPath:    meta.Path,
		MetaAuditSession: meta.Session,
		RecordCount:      plan.Artifact.RecordCount,
	}, nil
}

func validateRequest(req Request, requireOut bool) error {
	switch req.Source {
	case SourceAudit, SourceBundle, SourceBoundarySummary:
	default:
		return fmt.Errorf("unsupported export source %q", req.Source)
	}
	if requireOut {
		if req.Out == "" {
			return errors.New("--out is required")
		}
		if !isLocalPath(req.Out) {
			return errors.New("--out must be a local path, not a URL")
		}
	}
	if req.Source == SourceBundle && req.BundlePath == "" {
		return errors.New("--bundle is required for bundle export")
	}
	if req.Source == SourceBoundarySummary && req.BoundarySummary == nil {
		return errors.New("--from is required for boundary-summary export")
	}
	return nil
}

func RecordFailure(req Request, reason string) error {
	return emitFailureMetaAudit(req, reason)
}

func isLocalPath(path string) bool {
	if path == "" {
		return false
	}
	if u, err := url.Parse(path); err == nil && u.Scheme != "" {
		return false
	}
	return true
}

func writeArtifact(path string, artifact Artifact) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
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
	keepTemp = false
	return nil
}
