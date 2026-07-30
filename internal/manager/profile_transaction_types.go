package manager

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	profilechanges "github.com/vibe-agi/hideout/internal/manager/profile_changes"
)

const (
	ConfigurationDraftSchema = "hideout.configuration-draft.v1"
	ConfigurationPlanSchema  = "hideout.configuration-plan.v1"

	ChangeNetworkPosture        = "network.posture"
	ChangeNetworkProxyRef       = "network.proxyRef"
	ChangeNetworkDNS            = "network.dns"
	ChangeProfileEnvironment    = "profile.environment"
	ChangeProfileHostFS         = "profile.hostfs"
	ChangeProfileCommandProxy   = "profile.commandProxy"
	ChangeProfileCommandAdapter = "profile.commandAdapter"
	ChangeActivityRetention     = "activity.retention"

	maxDraftChanges     = 64
	maxChangeValueBytes = 64 << 10
	maxPlanReviewItems  = 256
	maxPlanTextBytes    = 2048
	maxClientNonceBytes = 128
)

var (
	ErrInvalidConfigurationDraft = errors.New("configuration draft is invalid")
	ErrUnknownTypedChange        = errors.New("typed change kind is not registered")
	ErrInvalidConfigurationPlan  = errors.New("configuration plan is invalid")
	changeKindPattern            = regexp.MustCompile(`^[a-z][A-Za-z0-9.-]{0,127}$`)
)

type ConfigurationDraft struct {
	Schema       string        `json:"schema"`
	Profile      string        `json:"profile"`
	BaseRevision uint64        `json:"baseRevision"`
	ClientNonce  string        `json:"clientNonce,omitempty"`
	Changes      []TypedChange `json:"changes"`
}

type TypedChange struct {
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value"`
}

type ChangeDefinition struct {
	Kind        string
	MutationKey string
	Normalize   func(json.RawMessage) (json.RawMessage, error)
	Review      func(json.RawMessage) (json.RawMessage, error)
}

type TypedChangeRegistry struct {
	definitions map[string]ChangeDefinition
}

type ConfigurationPlan struct {
	Schema           string          `json:"schema"`
	OperationID      string          `json:"operationId"`
	Profile          string          `json:"profile"`
	BaseRevision     uint64          `json:"baseRevision"`
	BaseDigest       string          `json:"baseDigest"`
	CanonicalChanges []TypedChange   `json:"canonicalChanges"`
	Diff             []ReviewDiff    `json:"diff"`
	Effects          []PlannedEffect `json:"effects"`
	Blockers         []Blocker       `json:"blockers"`
	Warnings         []Warning       `json:"warnings"`
	Rollback         RollbackPlan    `json:"rollback"`
	PlanDigest       string          `json:"planDigest"`
	ExpiresAt        time.Time       `json:"expiresAt"`
}

type ReviewDiff struct {
	Kind   string `json:"kind"`
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
	Scope  string `json:"scope"`
}

type PlannedEffect struct {
	ID            string   `json:"effectId"`
	Kind          string   `json:"kind"`
	Scope         string   `json:"scope"`
	Provider      string   `json:"provider"`
	Live          bool     `json:"live"`
	Summary       string   `json:"summary"`
	ProofRequired []string `json:"proofRequired"`
}

type Blocker struct {
	Code     string `json:"code"`
	Resource string `json:"resource,omitempty"`
	Owner    string `json:"owner,omitempty"`
	Phase    string `json:"phase,omitempty"`
	Summary  string `json:"summary"`
	Recovery string `json:"recovery"`
}

type Warning struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
}

type RollbackPlan struct {
	Mode    string   `json:"mode"`
	Summary string   `json:"summary"`
	Effects []string `json:"effects"`
}

func DefaultTypedChangeRegistry() TypedChangeRegistry {
	registry, err := NewTypedChangeRegistry([]ChangeDefinition{
		profileChangeDefinition(ChangeNetworkPosture, "profile.network"),
		profileChangeDefinition(ChangeNetworkProxyRef, "profile.network"),
		profileChangeDefinition(ChangeNetworkDNS, "profile.network"),
		profileChangeDefinition(ChangeProfileEnvironment, "profile.environment"),
		profileChangeDefinition(ChangeProfileHostFS, "profile.hostfs"),
		profileChangeDefinition(ChangeProfileCommandProxy, "profile.command-proxy"),
		profileChangeDefinition(ChangeProfileCommandAdapter, "profile.command-adapter"),
		profileChangeDefinition(ChangeActivityRetention, "activity.retention"),
	})
	if err != nil {
		panic(err)
	}
	return registry
}

func NewTypedChangeRegistry(definitions []ChangeDefinition) (TypedChangeRegistry, error) {
	registry := TypedChangeRegistry{definitions: make(map[string]ChangeDefinition, len(definitions))}
	for _, definition := range definitions {
		if definition.Review == nil {
			definition.Review = definition.Normalize
		}
		if !changeKindPattern.MatchString(definition.Kind) ||
			!operationCodePattern.MatchString(definition.MutationKey) ||
			definition.Normalize == nil ||
			definition.Review == nil {
			return TypedChangeRegistry{}, errors.New("typed change definition is invalid")
		}
		if _, exists := registry.definitions[definition.Kind]; exists {
			return TypedChangeRegistry{}, fmt.Errorf("typed change %q is duplicated", definition.Kind)
		}
		registry.definitions[definition.Kind] = definition
	}
	if len(registry.definitions) == 0 {
		return TypedChangeRegistry{}, errors.New("typed change registry is empty")
	}
	return registry, nil
}

func profileChangeDefinition(
	kind, mutationKey string,
) ChangeDefinition {
	return ChangeDefinition{
		Kind: kind, MutationKey: mutationKey,
		Normalize: func(raw json.RawMessage) (json.RawMessage, error) {
			return profilechanges.Normalize(kind, raw)
		},
		Review: func(raw json.RawMessage) (json.RawMessage, error) {
			return profilechanges.Review(kind, raw)
		},
	}
}

func (registry TypedChangeRegistry) Definition(kind string) (ChangeDefinition, bool) {
	definition, ok := registry.definitions[kind]
	return definition, ok
}

func (registry TypedChangeRegistry) MutationKeys() []string {
	set := make(map[string]struct{}, len(registry.definitions))
	for _, definition := range registry.definitions {
		set[definition.MutationKey] = struct{}{}
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (registry TypedChangeRegistry) Kinds() []string {
	kinds := make([]string, 0, len(registry.definitions))
	for kind := range registry.definitions {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func (registry TypedChangeRegistry) NormalizeDraft(draft ConfigurationDraft) (ConfigurationDraft, error) {
	if draft.Schema != ConfigurationDraftSchema ||
		draft.BaseRevision == 0 ||
		len(draft.Changes) == 0 ||
		len(draft.Changes) > maxDraftChanges ||
		len(draft.ClientNonce) > maxClientNonceBytes ||
		containsControlText(draft.ClientNonce) {
		return ConfigurationDraft{}, ErrInvalidConfigurationDraft
	}
	if err := profileNameForTransaction(draft.Profile); err != nil {
		return ConfigurationDraft{}, err
	}
	normalized := draft
	normalized.Changes = make([]TypedChange, 0, len(draft.Changes))
	seen := make(map[string]struct{}, len(draft.Changes))
	for _, change := range draft.Changes {
		definition, ok := registry.definitions[change.Kind]
		if !ok {
			return ConfigurationDraft{}, fmt.Errorf("%w: %s", ErrUnknownTypedChange, change.Kind)
		}
		if _, duplicate := seen[change.Kind]; duplicate {
			return ConfigurationDraft{}, fmt.Errorf("%w: duplicate change %s", ErrInvalidConfigurationDraft, change.Kind)
		}
		seen[change.Kind] = struct{}{}
		value, err := definition.Normalize(change.Value)
		if err != nil {
			return ConfigurationDraft{}, fmt.Errorf("%w: %s: %v", ErrInvalidConfigurationDraft, change.Kind, err)
		}
		normalized.Changes = append(normalized.Changes, TypedChange{Kind: change.Kind, Value: value})
	}
	sort.Slice(normalized.Changes, func(left, right int) bool {
		return normalized.Changes[left].Kind < normalized.Changes[right].Kind
	})
	return normalized, nil
}

func (registry TypedChangeRegistry) ReviewChanges(
	changes []TypedChange,
) ([]TypedChange, error) {
	reviewed := make([]TypedChange, len(changes))
	for index, change := range changes {
		definition, ok := registry.definitions[change.Kind]
		if !ok {
			return nil, fmt.Errorf(
				"%w: %s",
				ErrUnknownTypedChange,
				change.Kind,
			)
		}
		value, err := definition.Review(change.Value)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: %s: %v",
				ErrInvalidConfigurationDraft,
				change.Kind,
				err,
			)
		}
		reviewed[index] = TypedChange{
			Kind:  change.Kind,
			Value: value,
		}
	}
	return reviewed, nil
}

func (plan *ConfigurationPlan) Seal() error {
	if plan == nil {
		return ErrInvalidConfigurationPlan
	}
	plan.PlanDigest = ""
	if err := plan.validate(false); err != nil {
		return err
	}
	digest, err := CanonicalDigest(CanonicalDomainConfigurationPlan, *plan)
	if err != nil {
		return err
	}
	plan.PlanDigest = digest
	return plan.Validate()
}

func (plan ConfigurationPlan) Validate() error {
	return plan.validate(true)
}

func (plan ConfigurationPlan) VerifyDigest() error {
	if err := plan.Validate(); err != nil {
		return err
	}
	provided := plan.PlanDigest
	plan.PlanDigest = ""
	expected, err := CanonicalDigest(CanonicalDomainConfigurationPlan, plan)
	if err != nil {
		return err
	}
	if provided != expected {
		return fmt.Errorf("%w: plan digest mismatch", ErrInvalidConfigurationPlan)
	}
	return nil
}

func (plan ConfigurationPlan) validate(requireDigest bool) error {
	if plan.Schema != ConfigurationPlanSchema ||
		!operationIDPattern.MatchString(plan.OperationID) ||
		plan.BaseRevision == 0 ||
		!profileDigestPattern.MatchString(plan.BaseDigest) ||
		plan.ExpiresAt.IsZero() ||
		len(plan.CanonicalChanges) == 0 ||
		len(plan.CanonicalChanges) > maxDraftChanges ||
		len(plan.Diff) > maxPlanReviewItems ||
		len(plan.Effects) > maxPlanReviewItems ||
		len(plan.Blockers) > maxPlanReviewItems ||
		len(plan.Warnings) > maxPlanReviewItems {
		return ErrInvalidConfigurationPlan
	}
	if requireDigest && !profileDigestPattern.MatchString(plan.PlanDigest) {
		return ErrInvalidConfigurationPlan
	}
	if !requireDigest && plan.PlanDigest != "" {
		return ErrInvalidConfigurationPlan
	}
	if err := profileNameForTransaction(plan.Profile); err != nil {
		return err
	}
	registry := DefaultTypedChangeRegistry()
	draft := ConfigurationDraft{
		Schema:       ConfigurationDraftSchema,
		Profile:      plan.Profile,
		BaseRevision: plan.BaseRevision,
		Changes:      plan.CanonicalChanges,
	}
	normalized, err := registry.NormalizeDraft(draft)
	if err != nil {
		return ErrInvalidConfigurationPlan
	}
	reviewed, err := registry.ReviewChanges(normalized.Changes)
	if err != nil ||
		!rawChangesEqual(reviewed, plan.CanonicalChanges) {
		return ErrInvalidConfigurationPlan
	}
	for _, diff := range plan.Diff {
		if err := diff.Validate(); err != nil {
			return err
		}
	}
	seenEffects := make(map[string]struct{}, len(plan.Effects))
	for _, effect := range plan.Effects {
		if err := effect.Validate(); err != nil {
			return err
		}
		if _, exists := seenEffects[effect.ID]; exists {
			return ErrInvalidConfigurationPlan
		}
		seenEffects[effect.ID] = struct{}{}
	}
	for _, blocker := range plan.Blockers {
		if err := blocker.Validate(); err != nil {
			return err
		}
	}
	for _, warning := range plan.Warnings {
		if err := warning.Validate(); err != nil {
			return err
		}
	}
	return plan.Rollback.Validate()
}

func (diff ReviewDiff) Validate() error {
	if !changeKindPattern.MatchString(diff.Kind) ||
		len(diff.Field) == 0 || len(diff.Field) > 256 ||
		len(diff.Before) > maxPlanTextBytes || len(diff.After) > maxPlanTextBytes ||
		len(diff.Scope) == 0 || len(diff.Scope) > 128 ||
		containsControlText(diff.Field) || containsControlText(diff.Before) ||
		containsControlText(diff.After) || containsControlText(diff.Scope) {
		return ErrInvalidConfigurationPlan
	}
	return nil
}

func (effect PlannedEffect) Validate() error {
	if len(effect.ID) == 0 || len(effect.ID) > 128 ||
		len(effect.Provider) == 0 || len(effect.Provider) > 128 ||
		len(effect.Summary) == 0 || len(effect.Summary) > maxPlanTextBytes ||
		containsControlText(effect.ID) || containsControlText(effect.Provider) ||
		containsControlText(effect.Summary) {
		return ErrInvalidConfigurationPlan
	}
	switch effect.Kind {
	case "persist", "stage", "activate", "drain", "restart", "cleanup", "prove":
	default:
		return ErrInvalidConfigurationPlan
	}
	switch effect.Scope {
	case "profile", "environment", "new-sessions", "active-connections", "activity-owner":
	default:
		return ErrInvalidConfigurationPlan
	}
	if len(effect.ProofRequired) > 64 {
		return ErrInvalidConfigurationPlan
	}
	for _, proof := range effect.ProofRequired {
		if !evidenceCodePattern.MatchString(proof) {
			return ErrInvalidConfigurationPlan
		}
	}
	return nil
}

func (blocker Blocker) Validate() error {
	if !evidenceCodePattern.MatchString(blocker.Code) ||
		len(blocker.Resource) > 256 || len(blocker.Owner) > 128 ||
		len(blocker.Phase) > 128 ||
		len(blocker.Summary) == 0 || len(blocker.Summary) > maxPlanTextBytes ||
		len(blocker.Recovery) == 0 || len(blocker.Recovery) > maxPlanTextBytes ||
		containsControlText(blocker.Resource) || containsControlText(blocker.Owner) ||
		containsControlText(blocker.Phase) || containsControlText(blocker.Summary) ||
		containsControlText(blocker.Recovery) {
		return ErrInvalidConfigurationPlan
	}
	return nil
}

func (warning Warning) Validate() error {
	if !evidenceCodePattern.MatchString(warning.Code) ||
		len(warning.Summary) == 0 || len(warning.Summary) > maxPlanTextBytes ||
		containsControlText(warning.Summary) {
		return ErrInvalidConfigurationPlan
	}
	return nil
}

func (rollback RollbackPlan) Validate() error {
	switch rollback.Mode {
	case "not-required", "restore-previous", "provider-reconcile", "manual-recovery":
	default:
		return ErrInvalidConfigurationPlan
	}
	if len(rollback.Summary) == 0 || len(rollback.Summary) > maxPlanTextBytes ||
		containsControlText(rollback.Summary) || len(rollback.Effects) > 64 {
		return ErrInvalidConfigurationPlan
	}
	for _, effect := range rollback.Effects {
		if len(effect) == 0 || len(effect) > 128 || containsControlText(effect) {
			return ErrInvalidConfigurationPlan
		}
	}
	return nil
}

func NewOperationID() (string, error) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "op_" + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func NewTypedChange(kind string, value any) (TypedChange, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return TypedChange{}, err
	}
	registry := DefaultTypedChangeRegistry()
	draft, err := registry.NormalizeDraft(ConfigurationDraft{
		Schema:       ConfigurationDraftSchema,
		Profile:      "default",
		BaseRevision: 1,
		Changes:      []TypedChange{{Kind: kind, Value: raw}},
	})
	if err != nil {
		return TypedChange{}, err
	}
	return draft.Changes[0], nil
}

func profileNameForTransaction(name string) error {
	if strings.TrimSpace(name) == "" || len(name) > 128 {
		return ErrInvalidConfigurationDraft
	}
	return normalizeProfileNameOnly(name)
}

func normalizeProfileNameOnly(name string) error {
	if _, err := normalizeManagerProfileName(name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfigurationDraft, err)
	}
	return nil
}

func rawChangesEqual(left, right []TypedChange) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Kind != right[index].Kind || !bytes.Equal(left[index].Value, right[index].Value) {
			return false
		}
	}
	return true
}

func containsControlText(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) && character != '\t' {
			return true
		}
	}
	return false
}
