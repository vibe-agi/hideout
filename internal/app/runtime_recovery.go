package app

import (
	"errors"
	"fmt"

	"github.com/vibe-agi/hideout/internal/recovery"
)

type runtimeInstallFailureStage string

const (
	runtimeInstallNetwork  runtimeInstallFailureStage = "network"
	runtimeInstallDNS      runtimeInstallFailureStage = "dns"
	runtimeInstallRegistry runtimeInstallFailureStage = "registry"
	runtimeInstallPrefix   runtimeInstallFailureStage = "prefix"
)

// classifyRuntimeInstallFailure maps an observed boundary stage to shared
// recovery. It does not parse provider prose or guess a package for a command.
func classifyRuntimeInstallFailure(stage runtimeInstallFailureStage, observed error) (recovery.Code, error) {
	if observed == nil {
		return recovery.Code{}, errors.New("runtime install failure requires an observed error")
	}
	code := ""
	switch stage {
	case runtimeInstallNetwork:
		code = recovery.CodeRuntimeNetworkDenied
	case runtimeInstallDNS:
		code = recovery.CodeRuntimeDNSFailed
	case runtimeInstallRegistry:
		code = recovery.CodeRuntimeRegistryFailed
	case runtimeInstallPrefix:
		code = recovery.CodeRuntimePrefixUnwritable
	default:
		return recovery.Code{}, fmt.Errorf("unsupported runtime install failure stage %q", stage)
	}
	entry, ok := recovery.Lookup(code)
	if !ok {
		return recovery.Code{}, fmt.Errorf("runtime install recovery %q is not registered", code)
	}
	return entry, nil
}
