package export

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

var ErrReviewedPlanMismatch = errors.New(
	"reviewed export plan no longer matches the export request",
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
	return applyBuiltPlan(req, plan)
}

// ApplyPlan writes only an artifact that still matches the reviewed plan.
// CreatedAt is presentation metadata and is excluded from the comparison; the
// source, body, count, redaction stages, selectors, and decision remain bound.
func ApplyPlan(req Request, reviewed Plan) (Result, error) {
	if err := validateRequest(req, true); err != nil {
		_ = emitFailureMetaAudit(req, err.Error())
		return Result{}, err
	}
	current, err := BuildPlan(req)
	if err != nil {
		_ = emitFailureMetaAudit(req, err.Error())
		return Result{}, err
	}
	if current.Review.DecisionRequired {
		err := errors.New(
			"user data is present; choose --redact or --acknowledge-full-fidelity",
		)
		_ = emitFailureMetaAudit(req, err.Error())
		return Result{}, err
	}
	matches, err := reviewedPlansMatch(reviewed, current)
	if err != nil {
		_ = emitFailureMetaAudit(req, err.Error())
		return Result{}, err
	}
	if !matches {
		_ = emitFailureMetaAudit(req, ErrReviewedPlanMismatch.Error())
		return Result{}, ErrReviewedPlanMismatch
	}
	return applyBuiltPlan(req, current)
}

func applyBuiltPlan(req Request, plan Plan) (Result, error) {
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

func reviewedPlansMatch(left, right Plan) (bool, error) {
	type authority struct {
		Artifact Artifact `json:"artifact"`
		Review   Review   `json:"review"`
	}
	normalize := func(plan Plan) ([]byte, error) {
		artifact := plan.Artifact
		artifact.Provenance.CreatedAt = time.Time{}
		return json.Marshal(authority{Artifact: artifact, Review: plan.Review})
	}
	leftData, err := normalize(left)
	if err != nil {
		return false, err
	}
	rightData, err := normalize(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftData, rightData), nil
}

func validateRequest(req Request, requireOut bool) error {
	switch req.Source {
	case SourceAudit, SourceActivity, SourceBundle, SourceBoundarySummary,
		SourceDoctorReport:
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
	if req.Source == SourceDoctorReport && req.DoctorReportPath == "" {
		return errors.New("--doctor-report is required for doctor-report export")
	}
	if req.Source == SourceActivity {
		if req.Activity == nil {
			return errors.New("activity export source is required")
		}
		switch req.PathPolicy {
		case PathPolicyRedactHost:
		case PathPolicyPreserve:
			if !req.AcknowledgeFullFidelity &&
				!req.InteractiveConfirmed {
				return errors.New(
					"preserving host paths requires explicit full-fidelity acknowledgement",
				)
			}
		default:
			return errors.New("activity export path policy is required")
		}
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
