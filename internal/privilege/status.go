package privilege

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	StatusEnforced StatusValue = "enforced"
	StatusDegraded StatusValue = "degraded"
	StatusUnknown  StatusValue = "unknown"

	CheckPass        CheckStatus = "pass"
	CheckFail        CheckStatus = "fail"
	CheckUnsupported CheckStatus = "unsupported"
	CheckError       CheckStatus = "error"

	CheckTargetUID          CheckName = "target.uid"
	CheckTargetSudoN        CheckName = "target.sudo-n"
	CheckTargetAbsoluteSudo CheckName = "target.absolute-sudo-n"
	CheckSetupIdentity      CheckName = "setup.identity"
	CheckSetupCredential    CheckName = "setup.credential-location"

	SetupRootControlSSH SetupIdentityKind = "root-control-ssh"
	SetupRootHelper     SetupIdentityKind = "root-helper"
	SetupSharedSudo     SetupIdentityKind = "shared-sudo"
	SetupNoneRequired   SetupIdentityKind = "none-required"

	ActionGuestPrivilegeStatus = "guest.privilege.status"
	ActionPrivilegedSetup      = "hideout.privileged_setup"
	ActionPrivilegedCleanup    = "hideout.privileged_cleanup"
	ActionTargetRootAttempt    = "target.root_attempt"
)

type StatusValue string
type CheckName string
type CheckStatus string
type SetupIdentityKind string

type Status struct {
	Version       string         `json:"version"`
	Status        StatusValue    `json:"status"`
	Profile       string         `json:"profile,omitempty"`
	Backend       string         `json:"backend,omitempty"`
	EnvironmentID string         `json:"environmentId,omitempty"`
	Target        TargetIdentity `json:"targetIdentity"`
	Setup         SetupIdentity  `json:"setupIdentity"`
	Checks        []CheckResult  `json:"checks"`
	Reason        string         `json:"reason"`
	Guidance      string         `json:"guidance,omitempty"`
	CreatedAt     time.Time      `json:"createdAt,omitempty"`
}

type TargetIdentity struct {
	User                  string      `json:"user,omitempty"`
	UID                   *int        `json:"uid,omitempty"`
	Home                  string      `json:"home,omitempty"`
	SudoN                 CheckResult `json:"sudoN,omitempty"`
	AbsoluteSudoN         CheckResult `json:"absoluteSudoN,omitempty"`
	CanPasswordlessSudo   bool        `json:"canPasswordlessSudo"`
	PasswordlessSudoKnown bool        `json:"passwordlessSudoKnown"`
}

type SetupIdentity struct {
	Kind               SetupIdentityKind `json:"kind"`
	Available          bool              `json:"available"`
	SeparateFromTarget bool              `json:"separateFromTarget"`
	CredentialLocation string            `json:"credentialLocation,omitempty"`
	Proof              string            `json:"proof,omitempty"`
}

type CheckResult struct {
	Name      CheckName   `json:"name"`
	Status    CheckStatus `json:"status"`
	Observed  string      `json:"observed,omitempty"`
	Error     string      `json:"error,omitempty"`
	CheckedAt time.Time   `json:"checkedAt,omitempty"`
}

type ClassificationInput struct {
	Profile                 string
	Backend                 string
	EnvironmentID           string
	Target                  TargetIdentity
	Setup                   SetupIdentity
	Checks                  []CheckResult
	PrivilegedSetupRequired bool
	EnforcedOnly            bool
	Now                     time.Time
}

func Classify(in ClassificationInput) (Status, error) {
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := Status{
		Version:       "hideout.guest-privilege-status/v1",
		Profile:       in.Profile,
		Backend:       in.Backend,
		EnvironmentID: in.EnvironmentID,
		Target:        in.Target,
		Setup:         normalizeSetup(in.Setup, in.PrivilegedSetupRequired),
		Checks:        append([]CheckResult(nil), in.Checks...),
		CreatedAt:     now,
	}
	if out.Checks == nil {
		out.Checks = []CheckResult{}
	}
	status, reason, guidance := classifyStatus(out.Target, out.Setup, in.PrivilegedSetupRequired, out.Checks)
	out.Status = status
	out.Reason = reason
	out.Guidance = guidance
	if in.EnforcedOnly && out.Status != StatusEnforced {
		return out, fmt.Errorf("guest privilege separation is %s: enforced-only run denied: %s", out.Status, out.Reason)
	}
	return out, nil
}

func classifyStatus(target TargetIdentity, setup SetupIdentity, setupRequired bool, checks []CheckResult) (StatusValue, string, string) {
	if hasErrorOrUnsupported(checks) {
		return StatusUnknown, "guest privilege checks were incomplete or unsupported", "retry with a supported backend or recreate the environment"
	}
	if target.UID == nil {
		return StatusUnknown, "target user id is unknown", "recreate the environment or run privilege doctor"
	}
	if *target.UID == 0 {
		return StatusDegraded, "target command would run as guest root", "use a non-root target user or recreate the environment"
	}
	if !target.PasswordlessSudoKnown {
		return StatusUnknown, "target passwordless sudo status is unknown", "rerun privilege checks or recreate the environment"
	}
	if target.CanPasswordlessSudo {
		return StatusDegraded, "target user can run passwordless sudo", "use a base image without passwordless sudo or recreate the environment"
	}
	if setupRequired {
		if !setup.Available {
			return StatusUnknown, "privileged setup identity is unavailable", "recreate with a Hideout setup identity"
		}
		if !setup.SeparateFromTarget || setup.Kind == SetupSharedSudo {
			return StatusDegraded, "privileged setup still uses target-reachable authority", "recreate with separated setup identity"
		}
	}
	return StatusEnforced, "target is non-root, passwordless sudo is unavailable, and setup separation is proven", ""
}

func normalizeSetup(setup SetupIdentity, required bool) SetupIdentity {
	if setup.Kind == "" {
		if required {
			setup.Kind = SetupSharedSudo
		} else {
			setup.Kind = SetupNoneRequired
			setup.Available = true
			setup.SeparateFromTarget = true
		}
	}
	if setup.Kind == SetupNoneRequired {
		setup.Available = true
		setup.SeparateFromTarget = true
	}
	return setup
}

func hasErrorOrUnsupported(checks []CheckResult) bool {
	for _, check := range checks {
		if check.Status == CheckError || check.Status == CheckUnsupported {
			return true
		}
	}
	return false
}

func MustEnforced(status Status) error {
	if status.Status == StatusEnforced {
		return nil
	}
	if status.Reason == "" {
		return errors.New("guest privilege separation is not enforced")
	}
	return fmt.Errorf("guest privilege separation is %s: %s", status.Status, status.Reason)
}

func NonClaim(status StatusValue) string {
	switch status {
	case StatusEnforced:
		return "Hideout claims target non-root/no-sudo separation for command execution, but does not claim protection after guest root is obtained."
	case StatusDegraded:
		return "Hideout does not claim guest-root containment for this run because privilege separation is degraded."
	case StatusUnknown:
		return "Hideout does not claim guest-root containment for this run because privilege separation could not be proven."
	default:
		return "Hideout does not claim guest-root containment for this run because privilege separation status is invalid."
	}
}

func Int(v int) *int {
	return &v
}

func CheckPassed(name CheckName, observed string) CheckResult {
	return CheckResult{Name: name, Status: CheckPass, Observed: strings.TrimSpace(observed)}
}

func CheckFailed(name CheckName, observed string) CheckResult {
	return CheckResult{Name: name, Status: CheckFail, Observed: strings.TrimSpace(observed)}
}
