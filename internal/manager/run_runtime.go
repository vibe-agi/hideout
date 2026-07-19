package manager

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/privilege"
	"github.com/vibe-agi/hideout/internal/recovery"
	"github.com/vibe-agi/hideout/internal/runtimeverify"
)

type runtimeVerificationAuthority string

const (
	runtimeVerificationSessionAuthority runtimeVerificationAuthority = "session-workspace"
	runtimeVerificationMachineAuthority runtimeVerificationAuthority = "machine"
)

func (c Core) attachRuntimeVerification(
	spec *backend.RunSpec,
	runSession RunSession,
	runEnv RunEnvironment,
	backendName string,
	authority runtimeVerificationAuthority,
) error {
	if spec == nil || runEnv.Record.Runtime == nil {
		return nil
	}
	receiptStore := runtimeverify.Store{Root: c.Store.Root}
	if err := receiptStore.Remove(runEnv.Record.ID); err != nil {
		return fmt.Errorf("invalidate prior runtime verification: %w", err)
	}
	receiptAuthoritySafe, err := c.runtimeReceiptAuthorityForRun(runSession, runEnv, authority)
	if err != nil {
		return err
	}
	provenance := *runEnv.Record.Runtime
	resolution, err := c.currentRuntimeResolution(runEnv.Record)
	if err != nil {
		return fmt.Errorf("resolve pinned runtime contract: %w", err)
	}
	contract := backend.RuntimeContract{
		ID:     resolution.Contract.ID,
		Digest: provenance.ContractDigest,
	}
	for _, observation := range resolution.Contract.Observations {
		contract.Observations = append(contract.Observations, backend.RuntimeObservation{
			ID: observation.ID, Class: observation.Class, Command: observation.Command,
			VersionArgs: append([]string(nil), observation.VersionArgs...), OutputPattern: observation.OutputPattern,
		})
	}
	if err := contract.Validate(); err != nil {
		return fmt.Errorf("pinned runtime contract: %w", err)
	}
	authoritativePrivilege := "unknown"
	previousPrivilegeSink := spec.PrivilegeStatusSink
	spec.PrivilegeStatusSink = func(status privilege.Status) error {
		authoritativePrivilege = string(status.Status)
		if previousPrivilegeSink != nil {
			return previousPrivilegeSink(status)
		}
		return nil
	}
	spec.RuntimeContract = &contract
	expected := runtimeInstanceExpectation(provenance)
	spec.RuntimeInstanceExpected = &expected
	previousCompletionSink := spec.RuntimeCompletionSink
	spec.RuntimeCompletionSink = func(runErr error) error {
		var errs []error
		if runErr != nil {
			errs = append(errs, receiptStore.Remove(runEnv.Record.ID))
		}
		if previousCompletionSink != nil {
			errs = append(errs, previousCompletionSink(runErr))
		}
		return errors.Join(errs...)
	}
	spec.RuntimeResultSink = func(report backend.RuntimeObservationReport) error {
		if report.PrivilegeStatus != authoritativePrivilege {
			return errors.New("runtime observation privilege status is not bound to the current run")
		}
		if report.Instance.SessionID != runSession.Layout.ID || report.Instance.EnvironmentID != runEnv.Record.ID ||
			report.Instance.InstanceName != runEnv.InstanceName {
			return errors.New("runtime instance observation is not bound to the current session and environment")
		}
		receipt, failedClasses, err := runtimeReceipt(runEnv, contract, report, backendName)
		if err != nil {
			return err
		}
		if receiptAuthoritySafe {
			if err := receiptStore.Write(receipt); err != nil {
				return fmt.Errorf("write runtime verification receipt: %w", err)
			}
		}
		details := map[string]any{
			"family": provenance.Family, "revision": provenance.Revision,
			"contractDigest": provenance.ContractDigest,
			"artifactDigest": "sha256:" + provenance.ArtifactSHA256,
			"backend":        backendName, "backendReal": receipt.BackendReal,
			"status": receipt.Status, "resultCount": len(receipt.Results),
			"failedCount": len(receipt.FailedIDs), "failedIds": append([]string(nil), receipt.FailedIDs...),
			"failedClasses": failedClasses, "privilegeStatus": receipt.PrivilegeStatus,
			"recoveryCode":     receipt.RecoveryCode,
			"receiptPersisted": receiptAuthoritySafe,
		}
		if receiptAuthoritySafe {
			details["receiptRef"] = "environment:" + runEnv.Record.ID + "/runtime-verification"
		}
		decisionValue := "allow"
		if receipt.Status != runtimeverify.StatusPreviewReady {
			decisionValue = "degrade"
		}
		if err := runSession.Audit.Emit(audit.Event{
			Session: runSession.Layout.ID, Profile: runSession.Plan.ProfileName,
			Backend: runSession.Plan.Backend, Action: "runtime.verify",
			Decision: decisionValue, Details: details,
		}); err != nil {
			return err
		}
		if receipt.Status != runtimeverify.StatusPreviewReady {
			severity := decision.NoticeSeverityWarning
			if slices.Contains(failedClasses, backend.RuntimeObservationBoundary) {
				severity = decision.NoticeSeverityError
			}
			_, err := c.CreateNotice(decision.Notice{
				ID: "runtime-" + runEnv.Record.ID, Kind: decision.KindRuntimeStatus,
				Severity: severity, Status: receipt.Status,
				Source:   decision.Source{Profile: runSession.Plan.ProfileName, Session: runSession.Layout.ID, Backend: runSession.Plan.Backend},
				Payload:  map[string]any{"environmentId": runEnv.Record.ID, "failedIds": append([]string(nil), receipt.FailedIDs...), "recoveryCode": receipt.RecoveryCode},
				Preview:  decision.Preview{Summary: "runtime verification is " + receipt.Status, Facts: map[string]any{"environmentId": runEnv.Record.ID, "failedCount": len(receipt.FailedIDs)}},
				AuditRef: "audit:runtime:" + runEnv.Record.ID,
			})
			if err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

func (c Core) runtimeReceiptAuthorityForRun(
	runSession RunSession,
	runEnv RunEnvironment,
	authority runtimeVerificationAuthority,
) (bool, error) {
	switch authority {
	case runtimeVerificationSessionAuthority:
		workspaceAuthority, err := workspaceAuthorityForRunSession(runSession)
		if err != nil {
			return false, fmt.Errorf("resolve runtime receipt session authority: %w", err)
		}
		return runtimeReceiptAuthoritySafe(workspaceAuthority.HostRoot, c.Store.Root), nil
	case runtimeVerificationMachineAuthority:
		if !runEnv.Active || runSession.Environment.Record.ID != runEnv.Record.ID {
			return false, errors.New("runtime receipt machine authority does not match the selected environment")
		}
		return runtimeReceiptAuthoritySafeForEnvironment(runEnv.Record, c.Store.Root), nil
	default:
		return false, errors.New("runtime verification authority is required")
	}
}

func runtimeReceipt(runEnv RunEnvironment, contract backend.RuntimeContract, report backend.RuntimeObservationReport, backendName string) (runtimeverify.Receipt, []string, error) {
	if runEnv.Record.Runtime == nil {
		return runtimeverify.Receipt{}, nil, errors.New("runtime provenance is required")
	}
	if report.ContractID != contract.ID || report.ContractDigest != contract.Digest {
		return runtimeverify.Receipt{}, nil, errors.New("runtime observation report does not match the selected contract")
	}
	if len(report.Results) != len(contract.Observations) {
		return runtimeverify.Receipt{}, nil, errors.New("runtime observation report is incomplete")
	}
	receipt := runtimeverify.Receipt{
		Schema: runtimeverify.Schema, EnvironmentID: runEnv.Record.ID,
		ImageRef: runEnv.Record.ImageRef, Provenance: *runEnv.Record.Runtime,
		ContractDigest: contract.Digest, ObservedAt: time.Now().UTC(), SessionID: report.Instance.SessionID,
		Backend: backendName, BackendReal: backendName == "lima" && runEnv.Record.Backend == "lima" && report.Instance.VMType == "vz",
		Running: true, PrivilegeStatus: report.PrivilegeStatus,
		Instance: runtimeverify.Instance{
			Name: report.Instance.InstanceName, Status: report.Instance.Status, VMType: report.Instance.VMType,
			HostOS: report.Instance.HostOS, HostArch: report.Instance.HostArch, GuestArch: report.Instance.GuestArch,
			ImageLocation: report.Instance.ImageLocation, ImageSHA256: report.Instance.ImageSHA256,
			ActiveBuildIdentity: "sha256:" + report.Instance.PackageInventorySHA256, BootID: report.Instance.BootID,
		},
	}
	failedClassSet := map[string]struct{}{}
	for i, result := range report.Results {
		expected := contract.Observations[i]
		if result.ID != expected.ID || result.Class != expected.Class || result.Command != expected.Command {
			return runtimeverify.Receipt{}, nil, fmt.Errorf("runtime observation result %d does not match the selected contract", i)
		}
		receipt.Results = append(receipt.Results, runtimeverify.Result{
			ID: result.ID, Class: result.Class, Command: result.Command,
			Present: result.Present, VersionOutput: result.Output, Matched: result.Matched, Reason: result.Reason,
		})
		if !result.Present || !result.Matched {
			receipt.FailedIDs = append(receipt.FailedIDs, result.ID)
			failedClassSet[result.Class] = struct{}{}
		}
	}
	slices.Sort(receipt.FailedIDs)
	failedClasses := make([]string, 0, len(failedClassSet))
	for class := range failedClassSet {
		failedClasses = append(failedClasses, class)
	}
	slices.Sort(failedClasses)
	ready := receipt.BackendReal && receipt.PrivilegeStatus == "enforced" && len(receipt.FailedIDs) == 0
	if ready {
		receipt.Status = runtimeverify.StatusPreviewReady
	} else {
		receipt.Status = runtimeverify.StatusPreviewFailed
		switch {
		case slices.Contains(failedClasses, backend.RuntimeObservationBoundary):
			receipt.RecoveryCode = recovery.CodeRuntimeBoundaryMissing
		case slices.Contains(failedClasses, backend.RuntimeObservationBaseline):
			receipt.RecoveryCode = recovery.CodeRuntimeBaselineMissing
		case receipt.PrivilegeStatus != "enforced":
			receipt.RecoveryCode = recovery.CodePrivilegeStatusDegraded
		default:
			receipt.RecoveryCode = recovery.CodeRuntimeSelectionUnsupported
		}
	}
	receipt.Normalize()
	if err := receipt.Validate(); err != nil {
		return runtimeverify.Receipt{}, nil, fmt.Errorf("runtime observation receipt: %w", err)
	}
	return receipt, failedClasses, nil
}

type RuntimeRecoveryError struct {
	Code   string
	Reason string
	Hint   string
	Err    error
}

func (e RuntimeRecoveryError) Error() string {
	return fmt.Sprintf("code=%s reason=%s hint=%s: %v", e.Code, e.Reason, e.Hint, e.Err)
}

func (e RuntimeRecoveryError) Unwrap() error { return e.Err }

func runtimeRunError(runEnv RunEnvironment, err error) error {
	if err == nil || runEnv.Record.Runtime == nil {
		return err
	}
	var missing backend.CommandNotFoundError
	if !errors.As(err, &missing) {
		return err
	}
	entry, ok := recovery.Lookup(recovery.CodeRuntimeCommandMissing)
	if !ok {
		return err
	}
	return RuntimeRecoveryError{Code: entry.Code, Reason: entry.Reason, Hint: entry.Hint, Err: err}
}
