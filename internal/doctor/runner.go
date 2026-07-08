package doctor

import (
	"fmt"
	"slices"
	"strings"
)

func ValidateRequest(req Request) error {
	level := strings.TrimSpace(req.Level)
	if level == "" {
		level = LevelLight
	}
	switch level {
	case LevelLight, LevelDeep:
	default:
		return fmt.Errorf("unsupported doctor level %q", req.Level)
	}
	for _, feature := range NormalizeFeatures(req.Features) {
		if !slices.Contains(SupportedFeatures, feature) {
			return fmt.Errorf("unsupported doctor feature %q", feature)
		}
	}
	return nil
}

func FeatureSelected(req Request, feature string) bool {
	feature = stableID(feature)
	for _, item := range NormalizeFeatures(req.Features) {
		if item == feature {
			return true
		}
	}
	return false
}

func DeepSelected(req Request) bool {
	return strings.TrimSpace(req.Level) == LevelDeep
}
