package render

import (
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
)

func TestOperationsRendersDurableIdentityPhaseAndProgressiveEvidence(
	t *testing.T,
) {
	state := operationsRenderState()
	output := Operations(OperationsInput{
		State: state, Selected: 1, DetailOpen: true,
	}, Options{
		Width: 110, Height: 30, NoColor: true,
	})
	for _, expected := range []string{
		"Operations · 2 retained",
		"op_recoveryfixture01",
		"profile.transaction",
		"profile/default",
		"RECOVERY-REQUIRED",
		"op_successfixture001",
		"SUCCEEDED",
		"Effect persist-profile · persist · manager · SUCCEEDED",
		"Evidence profile.revision=10",
		"Result configuration-applied",
		"configuration committed",
		"Recovery none",
		"no recovery is required",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("operations output missing %q:\n%s", expected, output)
		}
	}
}

func TestOperationsResponseLossResumesExactExistingID(t *testing.T) {
	state := operationsRenderState()
	output := Operations(OperationsInput{
		State:      state,
		Selected:   0,
		LookupID:   "op_successfixture001",
		DetailOpen: true,
	}, Options{
		Width: 100, Height: 28, NoColor: true,
	})
	if !strings.Contains(output, "Resumed exact operation op_successfixture001") ||
		!strings.Contains(output, "Result configuration-applied") {
		t.Fatalf("exact operation was not resumed:\n%s", output)
	}

	output = Operations(OperationsInput{
		State:    state,
		LookupID: "op_missingfixture001",
	}, Options{
		Width: 100, Height: 28, NoColor: true,
	})
	for _, expected := range []string{
		"op_missingfixture001",
		"not present in this snapshot",
		"refresh",
		"never create a replacement operation",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("missing-ID recovery omitted %q:\n%s", expected, output)
		}
	}
}

func TestOperationsSanitizesEvidenceAndRecoveryText(t *testing.T) {
	state := operationsRenderState()
	state.Operations[0].Recovery.Summary =
		"\x1b]8;;https://evil.invalid\aoperator\u202e recovery"
	state.Operations[0].Effects[0].Evidence[0].Ref =
		"\x1b[31mrevision-unsafe"
	output := Operations(OperationsInput{
		State: state, DetailOpen: true,
	}, Options{
		Width: 100, Height: 28, NoColor: true,
	})
	if strings.Contains(output, "\x1b") ||
		strings.Contains(output, "\u202e") {
		t.Fatalf("operations rendered terminal controls: %q", output)
	}
}

func TestOperationDetailExplainsRecoveryRollbackAndStoppingStates(t *testing.T) {
	now := time.Date(2026, 7, 30, 2, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		operation manager.Operation
		want      []string
	}{
		{
			name:      "recovery required",
			operation: operationsRenderState().Operations[0],
			want: []string{
				"State ACTION REQUIRED",
				"exact operation ID",
			},
		},
		{
			name: "rolling back",
			operation: operationStateFixture(
				now,
				"op_rollingfixture01",
				"profile.transaction",
				manager.OperationRollingBack,
			),
			want: []string{
				"State ROLLING BACK",
				"neither success nor restoration",
			},
		},
		{
			name: "rollback unproved",
			operation: operationStateFixture(
				now,
				"op_unprovedfixture1",
				"profile.transaction",
				manager.OperationRollbackUnproved,
			),
			want: []string{
				"State UNPROVED",
				"Do not repeat",
			},
		},
		{
			name: "stopping",
			operation: operationStateFixture(
				now,
				"op_stoppingfixture1",
				"environment.stop",
				manager.OperationActivating,
			),
			want: []string{
				"State STOPPING",
				"evidence are still pending",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := strings.Join(
				operationDetail(test.operation),
				"\n",
			)
			for _, want := range test.want {
				if !strings.Contains(output, want) {
					t.Fatalf(
						"operation detail missing %q:\n%s",
						want,
						output,
					)
				}
			}
		})
	}
}

func operationStateFixture(
	now time.Time,
	id, kind, phase string,
) manager.Operation {
	operation := manager.Operation{
		Schema: manager.OperationSchema, ID: id, Kind: kind,
		Owner: manager.OperationOwner{
			Kind: "environment", ID: "env_fixture",
		},
		PlanDigest: "sha256:" + strings.Repeat("c", 64),
		Phase:      phase,
		Effects: []manager.EffectResult{{
			ID: "effect", Kind: "cleanup", Provider: "provider",
			Status: manager.EffectRunning,
		}},
		Recovery: manager.Recovery{
			Code:       "inspect-operation",
			Summary:    "inspect durable evidence",
			NextAction: "retry the same operation ID",
		},
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}
	if phase == manager.OperationRollingBack {
		operation.Effects[0].Status = manager.EffectSucceeded
		operation.Effects[0].Evidence = []manager.EvidenceRef{{
			Code: "effect-completed",
		}}
	}
	if phase == manager.OperationRollbackUnproved {
		operation.Effects[0].Status = manager.EffectUnproved
		operation.Effects[0].Evidence = []manager.EvidenceRef{{
			Code: "rollback-unproved",
		}}
		operation.Result = &manager.OperationResult{
			Status:  "unproved",
			Code:    "rollback-unproved",
			Summary: "restoration could not be proved",
		}
	}
	return operation
}

func operationsRenderState() liveconsole.State {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	return liveconsole.State{
		StreamHealth: liveconsole.StreamHealth{
			State: liveconsole.HealthLive,
		},
		Operations: []manager.Operation{
			{
				Schema: manager.OperationSchema,
				ID:     "op_recoveryfixture01",
				Kind:   "profile.transaction",
				Owner: manager.OperationOwner{
					Kind: "profile", ID: "default",
				},
				PlanDigest:   "sha256:" + strings.Repeat("a", 64),
				BaseRevision: 10,
				Phase:        manager.OperationRecoveryRequired,
				Effects: []manager.EffectResult{{
					ID:       "activate-network",
					Kind:     "activate",
					Provider: "network",
					Status:   manager.EffectUnproved,
					Evidence: []manager.EvidenceRef{{
						Code:       "route.proof",
						Ref:        "pending",
						ObservedAt: now,
					}},
				}},
				Recovery: manager.Recovery{
					Code:       "inspect-route",
					Summary:    "inspect route proof before retry",
					NextAction: "hideout status",
				},
				CreatedAt: now.Add(-time.Minute),
				UpdatedAt: now,
			},
			{
				Schema: manager.OperationSchema,
				ID:     "op_successfixture001",
				Kind:   "profile.transaction",
				Owner: manager.OperationOwner{
					Kind: "profile", ID: "default",
				},
				PlanDigest:   "sha256:" + strings.Repeat("b", 64),
				BaseRevision: 9,
				Phase:        manager.OperationSucceeded,
				Effects: []manager.EffectResult{{
					ID:       "persist-profile",
					Kind:     "persist",
					Provider: "manager",
					Status:   manager.EffectSucceeded,
					Evidence: []manager.EvidenceRef{{
						Code:       "profile.revision",
						Ref:        "10",
						ObservedAt: now,
					}},
				}},
				Result: &manager.OperationResult{
					Status:  manager.OperationSucceeded,
					Code:    "configuration-applied",
					Summary: "configuration committed",
				},
				Recovery: manager.Recovery{
					Code:    "none",
					Summary: "no recovery is required",
				},
				CreatedAt: now.Add(-2 * time.Minute),
				UpdatedAt: now.Add(-time.Minute),
			},
		},
	}
}
