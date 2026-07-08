package cmdadapter

import (
	"context"

	"github.com/vibe-agi/hideout/internal/policy"
)

type EvalContext struct {
	Version   string            `json:"version"`
	Profile   map[string]string `json:"profile"`
	Session   map[string]any    `json:"session"`
	Adapter   map[string]any    `json:"adapter"`
	Command   map[string]any    `json:"command"`
	Env       map[string]any    `json:"env"`
	Workspace map[string]any    `json:"workspace"`
	Network   map[string]any    `json:"network"`
}

func Evaluate(ctx context.Context, evaluator policy.Evaluator, adapter RuntimeAdapter, evalCtx EvalContext) (Outcome, error) {
	_ = ctx
	var out Outcome
	if err := evaluator.RunCommandAdapterScript(adapter.Source, adapter.Entrypoint, evalCtx, &out); err != nil {
		return Outcome{}, err
	}
	if err := ValidateOutcome(out, adapter); err != nil {
		return Outcome{}, err
	}
	return out, nil
}
