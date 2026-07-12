package hostapppack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/cmdgrammar"
)

func RunQualityTests(manifest Manifest, revisionID string, recordedAt time.Time) (TestResult, error) {
	if err := ValidateManifest(manifest); err != nil {
		return TestResult{}, err
	}
	if strings.TrimSpace(revisionID) == "" || recordedAt.IsZero() {
		return TestResult{}, fmt.Errorf("host-app quality test revision and time are required")
	}
	result := TestResult{
		SchemaVersion: TestResultVersion,
		ID:            qualityTestResultID(manifest.ID, revisionID),
		PackID:        manifest.ID,
		RevisionID:    revisionID,
		Status:        TestNotRun,
		RecordedAt:    recordedAt.UTC(),
	}
	if len(manifest.Tests) == 0 {
		return result, nil
	}
	bindings := make(map[string]BindingSpec, len(manifest.Bindings))
	for _, binding := range manifest.Bindings {
		bindings[binding.ID] = binding
	}
	for _, vector := range manifest.Tests {
		binding := bindings[vector.BindingID]
		grammar := cmdgrammar.OpenResourceGrammar{
			Kind: binding.Grammar.Kind, ResourceCount: binding.Grammar.ResourceCount,
			GotoFlags:        append([]string(nil), binding.Grammar.GotoFlags...),
			NewWindowFlags:   append([]string(nil), binding.Grammar.NewWindowFlags...),
			ReuseWindowFlags: append([]string(nil), binding.Grammar.ReuseWindowFlags...),
			UnknownFlags:     binding.Grammar.UnknownFlags,
		}
		outcome := TestOutcome{ID: vector.ID, Status: TestPassed}
		intent, err := cmdgrammar.ParseOpenResource(grammar, vector.Argv, "/workspace")
		if err != nil {
			outcome.Status = TestFailed
			outcome.Reason = boundedQualityReason(err.Error())
		} else if mismatch := qualityMismatch(intent, vector.Expected); mismatch != "" {
			outcome.Status = TestFailed
			outcome.Reason = mismatch
		}
		result.Results = append(result.Results, outcome)
		if outcome.Status == TestPassed {
			result.Passed++
		} else {
			result.Failed++
			result.Failures = append(result.Failures, boundedQualityText(vector.ID+": "+outcome.Reason))
		}
	}
	if result.Failed == 0 {
		result.Status = TestPassed
	} else {
		result.Status = TestFailed
	}
	return result, nil
}

func qualityMismatch(intent cmdgrammar.UnboundOpenResourceIntent, expected TestExpectation) string {
	if len(intent.Resources) != 1 || intent.Resources[0].GuestPath != expected.Resource {
		return "resource mismatch"
	}
	line, column := 0, 0
	if intent.Location != nil {
		line, column = intent.Location.Line, intent.Location.Column
	}
	if line != expected.Line || column != expected.Column {
		return "location mismatch"
	}
	window := string(intent.WindowMode)
	if window == "" {
		window = "reuse"
	}
	if window != expected.WindowMode {
		return "window mode mismatch"
	}
	return ""
}

func qualityTestResultID(packID, revisionID string) string {
	sum := sha256.Sum256([]byte(packID + "\x00" + revisionID + "\x00quality-v1"))
	return "test_" + hex.EncodeToString(sum[:12])
}

func boundedQualityReason(reason string) string {
	reason = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, reason)
	reason = boundedQualityText(reason)
	if reason == "" {
		return "quality test failed"
	}
	return reason
}

func boundedQualityText(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > MaxDescriptionBytes {
		value = value[:MaxDescriptionBytes]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return strings.TrimSpace(value)
}
