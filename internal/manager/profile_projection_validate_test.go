package manager

import (
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/profile"
)

func TestProfileProjectionValidateRejectsUnprovedEffectiveClaims(t *testing.T) {
	now := time.Date(2026, 7, 30, 2, 0, 0, 0, time.UTC)
	valid := func() ProfileProjection {
		return ProfileProjection{
			Schema:        ProfileProjectionSchema,
			Profile:       "default",
			Revision:      3,
			ContentDigest: "sha256:" + strings.Repeat("a", 64),
			Desired:       profile.Default("default"),
			Effective: ProfileEffective{
				Status: EffectiveCurrent,
				Network: &EffectiveNetwork{
					Mode:       "direct",
					DNS:        "system",
					ObservedAt: now,
				},
				Sessions: []EffectiveSessionSnapshot{{
					SessionID:       "ses_projectionvalidate",
					SnapshotID:      "sha256:" + strings.Repeat("b", 64),
					ProfileRevision: 3,
					Current:         true,
				}},
			},
			UpdatedAt: now,
		}
	}
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid projection: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ProfileProjection)
	}{
		{
			name: "unknown effective status",
			mutate: func(value *ProfileProjection) {
				value.Effective.Status = "observed"
			},
		},
		{
			name: "effective without network evidence",
			mutate: func(value *ProfileProjection) {
				value.Effective.Network = nil
			},
		},
		{
			name: "not observed with network evidence",
			mutate: func(value *ProfileProjection) {
				value.Effective.Status = EffectiveNotObserved
			},
		},
		{
			name: "proxy without exact generation",
			mutate: func(value *ProfileProjection) {
				value.Effective.Network.Mode = "proxy"
				value.Effective.Network.ProxySecretRef = "local-proxy"
			},
		},
		{
			name: "noncanonical session snapshot",
			mutate: func(value *ProfileProjection) {
				value.Effective.Sessions[0].SnapshotID = "snapshot-current"
			},
		},
		{
			name: "current session bound to another revision",
			mutate: func(value *ProfileProjection) {
				value.Effective.Sessions[0].ProfileRevision = 2
			},
		},
		{
			name: "operation phase outside transition schema",
			mutate: func(value *ProfileProjection) {
				value.Transition = &ProfileTransition{
					OperationID: "op_projectionvalidate",
					Kind:        "profile.transaction",
					Phase:       OperationFailed,
					StartedAt:   now,
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid()
			test.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("invalid projection was accepted")
			}
		})
	}
}

func TestProfileTransitionPhaseMapsDurableOperationStates(t *testing.T) {
	tests := map[string]string{
		OperationPlanned:          OperationStaging,
		OperationClaimed:          OperationStaging,
		OperationStaging:          OperationStaging,
		OperationActivating:       OperationActivating,
		OperationProving:          OperationProving,
		OperationRollingBack:      OperationRollingBack,
		OperationRecoveryRequired: OperationRecoveryRequired,
		OperationFailed:           OperationRecoveryRequired,
		OperationRollbackUnproved: OperationRecoveryRequired,
	}
	for input, want := range tests {
		got, ok := profileTransitionPhase(input)
		if !ok || got != want {
			t.Fatalf("phase %q mapped to %q, %t; want %q", input, got, ok, want)
		}
	}
	for _, input := range []string{
		OperationSucceeded,
		OperationCancelled,
		OperationRolledBack,
	} {
		if got, ok := profileTransitionPhase(input); ok || got != "" {
			t.Fatalf("terminal phase %q unexpectedly mapped to %q", input, got)
		}
	}
}
