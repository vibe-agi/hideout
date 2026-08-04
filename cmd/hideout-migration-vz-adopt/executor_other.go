//go:build !darwin || !arm64

package main

import (
	"errors"

	"github.com/vibe-agi/hideout/internal/migration/vzexecutor"
)

func runAdoptionExecutor(
	vzexecutor.ExecutionRequest,
) (vzexecutor.ExecutionResponse, error) {
	return vzexecutor.ExecutionResponse{}, errors.New(
		"VZ adoption executor requires macOS arm64",
	)
}
