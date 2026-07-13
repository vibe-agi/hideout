package export

import (
	"bytes"
	"errors"
	"regexp"

	"github.com/vibe-agi/hideout/internal/audit"
)

const PublicEvidenceLocalPathStage = "user-data.local-path"

var publicEvidenceLocalPathPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?m)(^|[[:space:]"'=:(])/(?:Users|home)/[^/[:space:]"']+/[^[:space:]"']*`),
	regexp.MustCompile(`(?m)(^|[[:space:]"'=:(])/(?:private/)?var/folders/[^[:space:]"']+`),
	regexp.MustCompile(`(?m)(^|[[:space:]"'=:(])/(?:private/)?(?:tmp|var/tmp)/[^[:space:]"']+`),
	regexp.MustCompile(`(?mi)(^|[[:space:]"'=:(])[a-z]:\\Users\\[^\\[:space:]"']+\\[^[:space:]"']*`),
}

// PublicEvidenceReview is recomputed from the exact public bytes. It makes the
// existing export decision and deterministic redaction stages independently
// verifiable instead of trusting a producer-owned "passed" flag.
type PublicEvidenceReview struct {
	Decision ExportDecision
	Stages   []RedactionStage
}

func ReviewPublicEvidence(data []byte) (PublicEvidenceReview, error) {
	text := string(data)
	if audit.RedactString(text) != text {
		return PublicEvidenceReview{}, errors.New("public evidence contains control-plane material")
	}
	if ContainsLocalAbsolutePath(data) {
		return PublicEvidenceReview{}, errors.New("public evidence contains a local absolute path")
	}
	return PublicEvidenceReview{
		Decision: ExportDecision{Mode: DecisionRedact, Channel: DecisionChannelFlag},
		Stages: []RedactionStage{
			{Name: "control-plane"},
			{Name: PublicEvidenceLocalPathStage},
		},
	}, nil
}

func ContainsLocalAbsolutePath(data []byte) bool {
	jsonUnescaped := bytes.ReplaceAll(data, []byte(`\\`), []byte(`\`))
	for _, pattern := range publicEvidenceLocalPathPatterns {
		if pattern.Match(data) || pattern.Match(jsonUnescaped) {
			return true
		}
	}
	return false
}
