package daemon

import (
	"context"
	"errors"

	workloadredact "github.com/vibe-agi/hideout/internal/workloadobs/redact"
)

type daemonControlTokenSource struct {
	credentials *credentialManager
}

func (source daemonControlTokenSource) SnapshotControlTokens(
	ctx context.Context,
) ([]workloadredact.ControlToken, error) {
	if ctx == nil {
		return nil, errors.New("control-token snapshot context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source.credentials == nil {
		return nil, errors.New("daemon credential manager is unavailable")
	}
	value, generation := source.credentials.redactionToken()
	if value == "" || generation == 0 {
		return nil, errors.New("daemon control token is unavailable")
	}
	return []workloadredact.ControlToken{{
		ID: "daemon-operator", Generation: generation,
		Value: []byte(value),
	}}, nil
}
