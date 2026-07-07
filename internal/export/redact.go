package export

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/profile"
)

type redactionOutcome struct {
	Body             any
	Stages           []RedactionStage
	Residual         []string
	Decision         ExportDecision
	DecisionRequired bool
}

func redactForExport(req Request, body any) (redactionOutcome, error) {
	selectors, err := parseSelectors(req.RedactSelectors)
	if err != nil {
		return redactionOutcome{}, err
	}
	out := redactionOutcome{
		Stages: []RedactionStage{{Name: "control-plane"}},
	}
	redactedBody, residual, stages, err := applyUserRedaction(req, body, selectors)
	if err != nil {
		return redactionOutcome{}, err
	}
	out.Body = redactAny(redactedBody)
	out.Stages = append(out.Stages, stages...)
	out.Residual = uniqueSorted(residual)
	switch {
	case len(selectors) > 0:
		out.Decision = ExportDecision{Mode: DecisionRedact, Channel: DecisionChannelFlag}
	case req.AcknowledgeFullFidelity:
		out.Decision = ExportDecision{Mode: DecisionAcknowledgeFullFidelity, Channel: DecisionChannelFlag}
	case req.InteractiveConfirmed:
		out.Decision = ExportDecision{Mode: DecisionAcknowledgeFullFidelity, Channel: DecisionChannelInteractive}
	case len(out.Residual) > 0:
		out.DecisionRequired = true
		return out, errors.New("user data is present; choose --redact or --acknowledge-full-fidelity")
	default:
		out.Decision = ExportDecision{Mode: DecisionRedact, Channel: DecisionChannelFlag}
	}
	return out, nil
}

func parseSelectors(raw []string) ([]string, error) {
	var out []string
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, errors.New("redaction selector must not be empty")
		}
		first := item
		if before, _, ok := strings.Cut(item, "."); ok {
			first = before
		}
		if audit.IsEvidentiaryKey(first) {
			return nil, fmt.Errorf("redaction selector %q targets non-redactable evidentiary field %q", item, first)
		}
		out = append(out, item)
	}
	return out, nil
}

func applyUserRedaction(req Request, body any, selectors []string) (any, []string, []RedactionStage, error) {
	switch typed := body.(type) {
	case []AuditEvent:
		events := make([]AuditEvent, len(typed))
		var residual []string
		var allStages []RedactionStage
		for i, event := range typed {
			redacted, stages, err := redactDetailsForProfile(req, event.Profile, event, selectors)
			if err != nil {
				return nil, nil, nil, err
			}
			event.Details = redacted
			events[i] = event
			allStages = appendPolicyStages(allStages, stages)
			residual = append(residual, residualKeys(redacted)...)
		}
		if len(typed) > 0 && len(selectors) > 0 && len(allStages) == 0 {
			return nil, nil, nil, errors.New("redaction selection requires a configured audit.redact policy")
		}
		return events, residual, allStages, nil
	case map[string]any:
		profileName := req.PolicyProfile
		if profileName == "" {
			if len(selectors) > 0 {
				return nil, nil, nil, errors.New("--policy-profile is required when redacting bundle or boundary-summary sources")
			}
			return typed, residualKeysFromAny(typed), nil, nil
		}
		redacted, stages, err := redactAnyWithPolicy(req, typed, profileName, selectors)
		if err != nil {
			return nil, nil, nil, err
		}
		if len(selectors) > 0 && len(stages) == 0 {
			return nil, nil, nil, fmt.Errorf("profile %q has no audit.redact policy", profileName)
		}
		return redacted, residualKeysFromAny(redacted), stages, nil
	default:
		return body, residualKeysFromAny(body), nil, nil
	}
}

func redactAnyWithPolicy(req Request, value any, profileName string, selectors []string) (any, []RedactionStage, error) {
	switch typed := value.(type) {
	case map[string]any:
		out := cloneDetails(typed)
		var stages []RedactionStage
		if details, ok := mapValue(out["details"]); ok {
			eventProfile := stringValue(out["profile"])
			if eventProfile == "" {
				eventProfile = profileName
			}
			redacted, nextStages, err := redactDetailsForProfile(req, eventProfile, AuditEvent{
				Session:  stringValue(out["session"]),
				Profile:  eventProfile,
				Action:   stringValue(out["action"]),
				Decision: stringValue(out["decision"]),
				Details:  details,
			}, selectors)
			if err != nil {
				return nil, stages, err
			}
			out["details"] = redacted
			stages = appendPolicyStages(stages, nextStages)
		} else {
			redacted, nextStages, err := redactDetailsForProfile(req, profileName, AuditEvent{Profile: profileName, Action: string(req.Source), Decision: "audit-only", Details: out}, selectors)
			if err != nil {
				return nil, stages, err
			}
			if len(nextStages) > 0 {
				out = redacted
			}
			stages = appendPolicyStages(stages, nextStages)
		}
		for key, item := range out {
			if key == "details" {
				continue
			}
			child, nextStages, err := redactAnyWithPolicy(req, item, profileName, selectors)
			if err != nil {
				return nil, stages, err
			}
			out[key] = child
			stages = appendPolicyStages(stages, nextStages)
		}
		return out, stages, nil
	case []any:
		out := make([]any, len(typed))
		var stages []RedactionStage
		for i, item := range typed {
			child, nextStages, err := redactAnyWithPolicy(req, item, profileName, selectors)
			if err != nil {
				return nil, stages, err
			}
			out[i] = child
			stages = appendPolicyStages(stages, nextStages)
		}
		return out, stages, nil
	default:
		return value, nil, nil
	}
}

func redactDetailsForProfile(req Request, profileName string, event AuditEvent, selectors []string) (map[string]any, []RedactionStage, error) {
	details := cloneDetails(event.Details)
	if profileName == "" {
		if len(selectors) > 0 {
			return nil, nil, errors.New("audit event profile is required for redaction selection")
		}
		return details, nil, nil
	}
	store := profile.Store{Root: req.StoreRoot}
	p, err := store.Load(profileName)
	if err != nil {
		if len(selectors) > 0 {
			return nil, nil, fmt.Errorf("resolve audit.redact policy for profile %q: %w", profileName, err)
		}
		return details, nil, nil
	}
	evaluator := policy.NewEvaluator(p)
	profileDir := store.ProfileDir(profileName)
	current := cloneDetails(details)
	immutable := cloneDetails(details)
	var stages []RedactionStage
	for _, ref := range p.Policy.ScriptRefs {
		if !contains(ref.Entrypoints, "redactAudit") {
			continue
		}
		path := ref.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(profileDir, path)
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return nil, stages, fmt.Errorf("audit redaction script %s: %w", ref.ID, err)
		}
		sum := sha256.Sum256(source)
		stage := RedactionStage{
			Name:       "audit.redact",
			ID:         ref.ID,
			SHA256:     hex.EncodeToString(sum[:]),
			Entrypoint: "redactAudit",
		}
		ctx := policy.AuditContext{
			Version:  "policy-audit/v1",
			Profile:  map[string]string{"name": profileName},
			Session:  map[string]any{"id": event.Session},
			Subject:  stringValue(current["subject"]),
			Action:   event.Action,
			Decision: event.Decision,
			Details:  audit.RedactDetails(cloneDetails(current)),
			Extra: map[string]interface{}{
				"exportRedaction": selectors,
			},
		}
		redaction, err := evaluator.RunAuditRedactScript(string(source), "redactAudit", ctx)
		if err != nil {
			return nil, append(stages, stage), fmt.Errorf("audit redaction script %s: %w", ref.ID, err)
		}
		next := cloneDetails(redaction.Details)
		if err := verifyEvidentiaryUnchanged(immutable, next); err != nil {
			return nil, append(stages, stage), err
		}
		current = next
		stages = append(stages, stage)
	}
	if len(selectors) > 0 && len(stages) == 0 {
		return nil, nil, fmt.Errorf("profile %q has no audit.redact policy", profileName)
	}
	return current, stages, nil
}

func verifyEvidentiaryUnchanged(before, after map[string]any) error {
	for _, key := range audit.EvidentiaryKeys {
		beforeValue, beforeOK := before[key]
		afterValue, afterOK := after[key]
		if beforeOK != afterOK || !reflect.DeepEqual(beforeValue, afterValue) {
			return fmt.Errorf("audit.redact attempted to mutate non-redactable evidentiary field %q", key)
		}
	}
	return nil
}

func redactAny(value any) any {
	normalized, err := normalizeAny(value)
	if err != nil {
		return audit.RedactValue("", value)
	}
	return stripControlPlaneAny(normalized)
}

func normalizeAny(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func stripControlPlaneAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if isControlPlaneKeyName(key) {
				continue
			}
			out[audit.RedactKey(key)] = stripControlPlaneAny(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = stripControlPlaneAny(item)
		}
		return out
	case string:
		return audit.RedactString(typed)
	default:
		return typed
	}
}

func residualKeys(details map[string]any) []string {
	var keys []string
	for key, value := range details {
		if audit.IsEvidentiaryKey(key) || isControlPlaneDetail(key, value) {
			continue
		}
		if isZeroValue(value) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func residualKeysFromAny(value any) []string {
	switch typed := value.(type) {
	case map[string]any:
		return residualKeys(typed)
	case []any:
		var keys []string
		for _, item := range typed {
			keys = append(keys, residualKeysFromAny(item)...)
		}
		return uniqueSorted(keys)
	default:
		return nil
	}
}

func isControlPlaneDetail(key string, value any) bool {
	if audit.RedactKey(key) != key {
		return true
	}
	return !reflect.DeepEqual(audit.RedactValue(key, value), value)
}

func isControlPlaneKeyName(key string) bool {
	if audit.RedactKey(key) != key {
		return true
	}
	return !reflect.DeepEqual(audit.RedactValue(key, "value"), "value")
}

func isZeroValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	default:
		return false
	}
}

func cloneDetails(details map[string]any) map[string]any {
	if details == nil {
		return map[string]any{}
	}
	data, err := json.Marshal(details)
	if err != nil {
		out := make(map[string]any, len(details))
		for key, value := range details {
			out[key] = value
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil || out == nil {
		out = map[string]any{}
	}
	return out
}

func mapValue(value any) (map[string]any, bool) {
	m, ok := value.(map[string]any)
	return m, ok
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func appendPolicyStages(existing, next []RedactionStage) []RedactionStage {
	seen := map[string]bool{}
	out := append([]RedactionStage(nil), existing...)
	for _, stage := range out {
		seen[stage.Name+"|"+stage.ID+"|"+stage.SHA256] = true
	}
	for _, stage := range next {
		key := stage.Name + "|" + stage.ID + "|" + stage.SHA256
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, stage)
	}
	return out
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func stringValue(value any) string {
	s, _ := value.(string)
	return s
}

func nowUTC(req Request) time.Time {
	if req.Now != nil {
		return req.Now().UTC()
	}
	return time.Now().UTC()
}
