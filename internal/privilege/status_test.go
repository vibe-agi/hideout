package privilege

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyGuestPrivilegeStatus(t *testing.T) {
	now := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	baseTarget := TargetIdentity{
		User:                  "hideout",
		UID:                   Int(1000),
		SudoN:                 CheckFailed(CheckTargetSudoN, "exit 1"),
		AbsoluteSudoN:         CheckFailed(CheckTargetAbsoluteSudo, "exit 1"),
		PasswordlessSudoKnown: true,
	}
	separateSetup := SetupIdentity{
		Kind:               SetupRootControlSSH,
		Available:          true,
		SeparateFromTarget: true,
		CredentialLocation: CredentialLocationClass("/secret/root/key"),
		Proof:              "system-provisioned-root-key",
	}
	cases := []struct {
		name          string
		input         ClassificationInput
		want          StatusValue
		wantReason    string
		enforcedError bool
	}{
		{
			name: "enforced with separate setup",
			input: ClassificationInput{
				Target:                  baseTarget,
				Setup:                   separateSetup,
				PrivilegedSetupRequired: true,
				Now:                     now,
			},
			want: StatusEnforced,
		},
		{
			name: "enforced with no setup required",
			input: ClassificationInput{
				Target: baseTarget,
				Setup:  SetupIdentity{Kind: SetupNoneRequired},
				Now:    now,
			},
			want: StatusEnforced,
		},
		{
			name: "passwordless sudo is degraded",
			input: ClassificationInput{
				Target: TargetIdentity{
					UID:                   Int(1000),
					SudoN:                 CheckPassed(CheckTargetSudoN, "root"),
					AbsoluteSudoN:         CheckFailed(CheckTargetAbsoluteSudo, "exit 1"),
					CanPasswordlessSudo:   true,
					PasswordlessSudoKnown: true,
				},
				Setup: separateSetup,
				Now:   now,
			},
			want:       StatusDegraded,
			wantReason: "passwordless sudo",
		},
		{
			name: "shared setup sudo is degraded",
			input: ClassificationInput{
				Target:                  baseTarget,
				Setup:                   SetupIdentity{Kind: SetupSharedSudo, Available: true, SeparateFromTarget: false},
				PrivilegedSetupRequired: true,
				Now:                     now,
			},
			want:       StatusDegraded,
			wantReason: "target-reachable",
		},
		{
			name: "missing uid is unknown",
			input: ClassificationInput{
				Target: TargetIdentity{PasswordlessSudoKnown: true},
				Setup:  separateSetup,
				Now:    now,
			},
			want:       StatusUnknown,
			wantReason: "id is unknown",
		},
		{
			name: "unsupported check is unknown",
			input: ClassificationInput{
				Target: baseTarget,
				Setup:  separateSetup,
				Checks: []CheckResult{{Name: CheckTargetSudoN, Status: CheckUnsupported}},
				Now:    now,
			},
			want:       StatusUnknown,
			wantReason: "incomplete",
		},
		{
			name: "enforced only fails closed",
			input: ClassificationInput{
				Target: TargetIdentity{
					UID:                   Int(1000),
					SudoN:                 CheckPassed(CheckTargetSudoN, "root"),
					CanPasswordlessSudo:   true,
					PasswordlessSudoKnown: true,
				},
				Setup:        separateSetup,
				EnforcedOnly: true,
				Now:          now,
			},
			want:          StatusDegraded,
			enforcedError: true,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Classify(tt.input)
			if tt.enforcedError {
				if err == nil || !strings.Contains(err.Error(), "enforced-only") {
					t.Fatalf("expected enforced-only error, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("Classify: %v", err)
			}
			if got.Status != tt.want {
				t.Fatalf("status=%q want %q (%+v)", got.Status, tt.want, got)
			}
			if tt.wantReason != "" && !strings.Contains(got.Reason, tt.wantReason) {
				t.Fatalf("reason=%q want containing %q", got.Reason, tt.wantReason)
			}
			if got.Version != "hideout.guest-privilege-status/v1" {
				t.Fatalf("version=%q", got.Version)
			}
		})
	}
}

func TestTargetFromChecks(t *testing.T) {
	checks := []CheckResult{
		CheckPassed(CheckTargetUID, "1000\n"),
		CheckFailed(CheckTargetSudoN, "sudo: a password is required"),
		CheckFailed(CheckTargetAbsoluteSudo, "sudo: a password is required"),
	}
	target := TargetFromChecks("hideout", "/home/hideout", checks)
	if target.UID == nil || *target.UID != 1000 {
		t.Fatalf("uid mismatch: %+v", target)
	}
	if target.CanPasswordlessSudo {
		t.Fatalf("target should not be passwordless sudo: %+v", target)
	}
	if !target.PasswordlessSudoKnown {
		t.Fatalf("sudo status should be known: %+v", target)
	}
}
