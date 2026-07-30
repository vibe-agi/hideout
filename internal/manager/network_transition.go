package manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/secrets"
)

const (
	NetworkTransitionPlanSchema   = "hideout.network-transition-plan.v1"
	NetworkTransitionResultSchema = "hideout.network-transition-result.v1"

	NetworkTransitionPosture = "posture"
	NetworkTransitionRoute   = "route"
	NetworkTransitionDNS     = "dns"

	NetworkTransitionBlocked          = "blocked"
	NetworkTransitionSucceeded        = "succeeded"
	NetworkTransitionRolledBack       = "rolled-back"
	NetworkTransitionRollbackUnproved = "rollback-unproved"

	NetworkSessionLive       = "live"
	NetworkSessionStale      = "stale"
	NetworkSessionUnprovable = "unprovable"

	networkTransitionProviderName = "manager.network"
	networkTransitionDigestDomain = "network-transition-plan"
	maxNetworkCandidateBytes      = 16 << 10
)

var (
	ErrInvalidNetworkTransition = errors.New(
		"network transition is invalid",
	)
	ErrNetworkTransitionNoChange = errors.New(
		"network transition has no effective change",
	)
	ErrNetworkTransitionBlocked = errors.New(
		"network transition is blocked by active or unprovable sessions",
	)
	ErrNetworkTransitionStale = errors.New(
		"network transition effective state changed after review",
	)
	ErrNetworkTransitionProviderUnavailable = errors.New(
		"network transition provider is unavailable",
	)
	ErrNetworkTransitionRolledBack = errors.New(
		"network transition failed and the previous route was restored",
	)
	ErrNetworkTransitionRollbackUnproved = errors.New(
		"network transition rollback could not be proved",
	)

	networkTransitionEnvironmentPattern = regexp.MustCompile(
		`^env_[a-z0-9]+$`,
	)
	networkTransitionSessionPattern = regexp.MustCompile(
		`^ses_[A-Za-z0-9_-]{1,124}$`,
	)
)

// NetworkRouteConfiguration is the complete non-secret route identity used by
// planning, review, evidence, and stale checks. It contains a Keychain
// generation, never a secret-derived digest or secret value.
type NetworkRouteConfiguration struct {
	Mode                  string `json:"mode"`
	ProxySecretRef        string `json:"proxySecretRef,omitempty"`
	ProxySecretGeneration uint64 `json:"proxySecretGeneration,omitempty"`
	MediatedResolver      string `json:"mediatedResolver,omitempty"`
}

type NetworkTransitionDraft struct {
	EnvironmentID string                    `json:"environmentId"`
	Desired       NetworkRouteConfiguration `json:"desired"`
}

type NetworkTransitionPlan struct {
	Schema        string                    `json:"schema"`
	PlanDigest    string                    `json:"planDigest"`
	EnvironmentID string                    `json:"environmentId"`
	Kind          string                    `json:"kind"`
	From          NetworkRouteConfiguration `json:"from"`
	Desired       NetworkRouteConfiguration `json:"desired"`
	Effects       []PlannedEffect           `json:"effects"`
	Blockers      []Blocker                 `json:"blockers"`
	Rollback      RollbackPlan              `json:"rollback"`
}

// NetworkCandidateMaterial crosses only the internal provider boundary.
// Serialization is explicitly forbidden because UpstreamProxyURL may contain
// URI userinfo.
type NetworkCandidateMaterial struct {
	UpstreamProxyURL string `json:"-"`
}

type NetworkTransitionSession struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Phase  string `json:"phase"`
}

type NetworkTransitionSessionInventory interface {
	NetworkTransitionSessions(
		context.Context,
		string,
	) ([]NetworkTransitionSession, error)
}

type NetworkTransitionSessionInventoryFunc func(
	context.Context,
	string,
) ([]NetworkTransitionSession, error)

func (inventory NetworkTransitionSessionInventoryFunc) NetworkTransitionSessions(
	ctx context.Context,
	environmentID string,
) ([]NetworkTransitionSession, error) {
	if inventory == nil {
		return nil, ErrNetworkTransitionProviderUnavailable
	}
	return inventory(ctx, environmentID)
}

// NetworkTransitionProvider owns effective route observation and candidate
// staging. Stage must leave the effective route unchanged. If it returns a
// non-nil handle with an error, the caller rolls that partial stage back.
type NetworkTransitionProvider interface {
	ObserveNetworkRoute(
		context.Context,
		string,
	) (NetworkRouteConfiguration, error)
	StageNetworkCandidate(
		context.Context,
		NetworkTransitionPlan,
		NetworkCandidateMaterial,
	) (NetworkStagedCandidate, NetworkTransitionProof, error)
}

// NetworkTransitionCapabilityProvider lets a composed provider distinguish
// route-only support from guest-service transitions. Callers must still treat
// StageNetworkCandidate as authoritative.
type NetworkTransitionCapabilityProvider interface {
	SupportsNetworkTransitionKind(string) bool
}

// NetworkTransitionAvailabilityProvider supplies a review-time eligibility
// check for capabilities such as a daemon-owned live guest runtime. It is
// advisory: apply must acquire and revalidate the capability again.
type NetworkTransitionAvailabilityProvider interface {
	NetworkTransitionAvailable(
		context.Context,
		NetworkTransitionPlan,
	) error
}

// NetworkTransitionBatchProvider is the optional provider capability required
// when one reviewed profile change affects more than one live environment.
// Every candidate is staged, probed, and activated before CommitCandidates is
// called. CommitCandidates must first validate the complete set and then
// finalize it without a fallible partial commit. RollbackCandidates has the
// same all-or-unproved contract and returns one restoration proof per input
// candidate, in input order.
type NetworkTransitionBatchProvider interface {
	NetworkTransitionProvider
	CommitNetworkCandidates(
		context.Context,
		[]NetworkStagedCandidate,
	) error
	RollbackNetworkCandidates(
		context.Context,
		[]NetworkStagedCandidate,
	) ([]NetworkTransitionProof, error)
}

// NetworkStagedCandidate is a single-use provider transaction. Activate
// changes only the route used by subsequently accepted connections. Drain
// observes prior bindings; it must not forcibly close them.
type NetworkStagedCandidate interface {
	ProbeNetworkCandidate(context.Context) (NetworkTransitionProof, error)
	ActivateNetworkCandidate(context.Context) (NetworkTransitionProof, error)
	ProveNetworkCandidate(context.Context) (NetworkTransitionProof, error)
	DrainPreviousConnections(context.Context) (NetworkTransitionProof, error)
	CommitNetworkCandidate(context.Context) error
	RollbackNetworkCandidate(context.Context) (NetworkTransitionProof, error)
}

// NetworkTransitionProof is deliberately small and non-secret. Route
// generation is provider-owned; secret generation must match the reviewed
// Keychain generation. ConnectionsRetained proves old bindings were observed
// rather than terminated during a pointer change.
type NetworkTransitionProof struct {
	RouteGeneration     uint64    `json:"routeGeneration"`
	SecretGeneration    uint64    `json:"secretGeneration,omitempty"`
	MediatedResolver    string    `json:"mediatedResolver,omitempty"`
	RuntimeBootID       string    `json:"runtimeBootId,omitempty"`
	ConnectionsRetained uint64    `json:"connectionsRetained"`
	ObservedAt          time.Time `json:"observedAt"`
}

type NetworkTransitionResult struct {
	Schema              string                    `json:"schema"`
	PlanDigest          string                    `json:"planDigest"`
	EnvironmentID       string                    `json:"environmentId"`
	Kind                string                    `json:"kind"`
	Phase               string                    `json:"phase"`
	From                NetworkRouteConfiguration `json:"from"`
	Desired             NetworkRouteConfiguration `json:"desired"`
	Effective           NetworkRouteConfiguration `json:"effective,omitempty"`
	EffectiveProved     bool                      `json:"effectiveProved"`
	ConnectionsRetained uint64                    `json:"connectionsRetained"`
	Effects             []EffectResult            `json:"effects"`
	Blockers            []Blocker                 `json:"blockers"`
	Recovery            Recovery                  `json:"recovery"`
}

// NetworkTransitionEffectCheckpoint durably brackets each provider effect.
// BeforeNetworkTransitionEffect must complete before the provider is invoked;
// AfterNetworkTransitionEffect records the exact resulting evidence before the
// next provider effect begins. A checkpoint error aborts the transition and
// triggers rollback for any already staged candidate.
type NetworkTransitionEffectCheckpoint interface {
	BeforeNetworkTransitionEffect(
		context.Context,
		NetworkTransitionPlan,
		PlannedEffect,
	) error
	AfterNetworkTransitionEffect(
		context.Context,
		NetworkTransitionPlan,
		EffectResult,
	) error
}

type NetworkTransitionService struct {
	Provider    NetworkTransitionProvider
	Sessions    NetworkTransitionSessionInventory
	Checkpoints NetworkTransitionEffectCheckpoint
}

func (service NetworkTransitionService) Plan(
	ctx context.Context,
	draft NetworkTransitionDraft,
) (NetworkTransitionPlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return NetworkTransitionPlan{}, err
	}
	if !networkTransitionEnvironmentPattern.MatchString(
		draft.EnvironmentID,
	) {
		return NetworkTransitionPlan{}, ErrInvalidNetworkTransition
	}
	if err := draft.Desired.Validate(); err != nil {
		return NetworkTransitionPlan{}, err
	}
	if service.Provider == nil {
		return NetworkTransitionPlan{},
			ErrNetworkTransitionProviderUnavailable
	}
	current, err := service.Provider.ObserveNetworkRoute(
		ctx,
		draft.EnvironmentID,
	)
	if err != nil {
		return NetworkTransitionPlan{}, newNetworkTransitionProviderError(
			"observe",
			err,
		)
	}
	if err := current.Validate(); err != nil {
		return NetworkTransitionPlan{}, ErrInvalidNetworkTransition
	}
	kind, err := classifyNetworkTransition(current, draft.Desired)
	if err != nil {
		return NetworkTransitionPlan{}, err
	}
	blockers := service.postureBlockers(
		ctx,
		draft.EnvironmentID,
		kind,
	)
	plan := NetworkTransitionPlan{
		Schema:        NetworkTransitionPlanSchema,
		EnvironmentID: draft.EnvironmentID,
		Kind:          kind,
		From:          current,
		Desired:       draft.Desired,
		Effects:       plannedNetworkTransitionEffects(kind),
		Blockers:      blockers,
		Rollback:      plannedNetworkTransitionRollback(),
	}
	if err := plan.Seal(); err != nil {
		return NetworkTransitionPlan{}, err
	}
	return plan, nil
}

func (service NetworkTransitionService) Apply(
	ctx context.Context,
	plan NetworkTransitionPlan,
	material NetworkCandidateMaterial,
) (NetworkTransitionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result := newNetworkTransitionResult(plan)
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := plan.VerifyDigest(); err != nil {
		return result, err
	}
	if service.Provider == nil {
		return result, ErrNetworkTransitionProviderUnavailable
	}
	if len(plan.Blockers) != 0 {
		result.Phase = NetworkTransitionBlocked
		result.Recovery = networkTransitionBlockedRecovery()
		return result, ErrNetworkTransitionBlocked
	}

	current, err := service.Provider.ObserveNetworkRoute(
		ctx,
		plan.EnvironmentID,
	)
	if err != nil {
		return result, newNetworkTransitionProviderError("observe", err)
	}
	if current != plan.From {
		result.Recovery = Recovery{
			Code:       "network-transition-stale",
			Summary:    "The effective route changed after this transition was reviewed.",
			NextAction: "Review a fresh configuration plan before retrying.",
		}
		return result, ErrNetworkTransitionStale
	}
	if blockers := service.postureBlockers(
		ctx,
		plan.EnvironmentID,
		plan.Kind,
	); len(blockers) != 0 {
		result.Phase = NetworkTransitionBlocked
		result.Blockers = blockers
		result.Recovery = networkTransitionBlockedRecovery()
		return result, ErrNetworkTransitionBlocked
	}
	if err := validateNetworkCandidateMaterial(
		plan.Desired,
		material,
	); err != nil {
		return result, err
	}
	if err := service.beforeNetworkTransitionEffect(
		ctx,
		plan,
		"network-stage",
	); err != nil {
		return result, err
	}

	handle, proof, stageErr := service.Provider.StageNetworkCandidate(
		ctx,
		plan,
		material,
	)
	if stageErr != nil {
		setNetworkTransitionEffect(
			&result,
			"network-stage",
			EffectFailed,
			nil,
		)
		stageErr = errors.Join(
			stageErr,
			service.afterNetworkTransitionEffect(
				ctx,
				plan,
				networkTransitionResultEffect(
					result,
					"network-stage",
				),
			),
		)
		if handle != nil {
			return rollbackNetworkTransition(
				ctx,
				result,
				plan,
				handle,
				stageErr,
				proof.RouteGeneration,
				false,
			)
		}
		return proveNetworkTransitionUnchangedAfterStageFailure(
			ctx,
			service.Provider,
			result,
			plan,
			stageErr,
		)
	}
	if handle == nil {
		setNetworkTransitionEffect(
			&result,
			"network-stage",
			EffectUnproved,
			nil,
		)
		result.Phase = NetworkTransitionRollbackUnproved
		result.Effective = NetworkRouteConfiguration{}
		result.EffectiveProved = false
		result.Recovery = networkTransitionRollbackUnprovedRecovery()
		return result, newNetworkTransitionApplyError(
			ErrNetworkTransitionRollbackUnproved,
			newNetworkTransitionProviderError(
				"stage",
				errors.New("provider returned no staged candidate"),
			),
		)
	}
	candidateGeneration := proof.RouteGeneration
	candidateActivated := false
	rollback := func(
		operationErr error,
	) (NetworkTransitionResult, error) {
		return rollbackNetworkTransition(
			ctx,
			result,
			plan,
			handle,
			operationErr,
			candidateGeneration,
			candidateActivated,
		)
	}
	if err := validateNetworkTransitionPhaseProof(
		plan,
		"network-stage",
		proof,
		proof.RouteGeneration,
	); err != nil {
		return rollback(err)
	}
	setNetworkTransitionEffect(
		&result,
		"network-stage",
		EffectSucceeded,
		networkTransitionEvidence(
			"network-candidate-staged",
			plan.EnvironmentID,
			proof,
		),
	)
	if err := service.afterNetworkTransitionEffect(
		ctx,
		plan,
		networkTransitionResultEffect(result, "network-stage"),
	); err != nil {
		return rollback(err)
	}

	if err := service.beforeNetworkTransitionEffect(
		ctx,
		plan,
		"network-probe",
	); err != nil {
		return rollback(err)
	}
	proof, err = handle.ProbeNetworkCandidate(ctx)
	if err != nil {
		setNetworkTransitionEffect(
			&result,
			"network-probe",
			EffectFailed,
			nil,
		)
		err = errors.Join(
			err,
			service.afterNetworkTransitionEffect(
				ctx,
				plan,
				networkTransitionResultEffect(
					result,
					"network-probe",
				),
			),
		)
		return rollback(err)
	}
	if err := validateNetworkTransitionPhaseProof(
		plan,
		"network-probe",
		proof,
		candidateGeneration,
	); err != nil {
		return rollback(err)
	}
	setNetworkTransitionEffect(
		&result,
		"network-probe",
		EffectSucceeded,
		networkTransitionEvidence(
			"network-candidate-probed",
			plan.EnvironmentID,
			proof,
		),
	)
	if err := service.afterNetworkTransitionEffect(
		ctx,
		plan,
		networkTransitionResultEffect(result, "network-probe"),
	); err != nil {
		return rollback(err)
	}

	if err := service.beforeNetworkTransitionEffect(
		ctx,
		plan,
		"network-activate",
	); err != nil {
		return rollback(err)
	}
	proof, err = handle.ActivateNetworkCandidate(ctx)
	if err != nil {
		setNetworkTransitionEffect(
			&result,
			"network-activate",
			EffectFailed,
			nil,
		)
		err = errors.Join(
			err,
			service.afterNetworkTransitionEffect(
				ctx,
				plan,
				networkTransitionResultEffect(
					result,
					"network-activate",
				),
			),
		)
		return rollback(err)
	}
	candidateActivated = true
	if err := validateNetworkTransitionPhaseProof(
		plan,
		"network-activate",
		proof,
		candidateGeneration,
	); err != nil {
		return rollback(err)
	}
	setNetworkTransitionEffect(
		&result,
		"network-activate",
		EffectSucceeded,
		networkTransitionEvidence(
			"network-route-activated",
			plan.EnvironmentID,
			proof,
		),
	)
	if err := service.afterNetworkTransitionEffect(
		ctx,
		plan,
		networkTransitionResultEffect(result, "network-activate"),
	); err != nil {
		return rollback(err)
	}

	if err := service.beforeNetworkTransitionEffect(
		ctx,
		plan,
		"network-prove",
	); err != nil {
		return rollback(err)
	}
	proof, err = handle.ProveNetworkCandidate(ctx)
	if err != nil {
		setNetworkTransitionEffect(
			&result,
			"network-prove",
			EffectFailed,
			nil,
		)
		err = errors.Join(
			err,
			service.afterNetworkTransitionEffect(
				ctx,
				plan,
				networkTransitionResultEffect(
					result,
					"network-prove",
				),
			),
		)
		return rollback(err)
	}
	if err := validateNetworkTransitionPhaseProof(
		plan,
		"network-prove",
		proof,
		candidateGeneration,
	); err != nil {
		return rollback(err)
	}
	setNetworkTransitionEffect(
		&result,
		"network-prove",
		EffectSucceeded,
		networkTransitionEvidence(
			"network-route-proved",
			plan.EnvironmentID,
			proof,
		),
	)
	if err := service.afterNetworkTransitionEffect(
		ctx,
		plan,
		networkTransitionResultEffect(result, "network-prove"),
	); err != nil {
		return rollback(err)
	}

	if err := service.beforeNetworkTransitionEffect(
		ctx,
		plan,
		"network-drain",
	); err != nil {
		return rollback(err)
	}
	proof, err = handle.DrainPreviousConnections(ctx)
	if err != nil {
		setNetworkTransitionEffect(
			&result,
			"network-drain",
			EffectFailed,
			nil,
		)
		err = errors.Join(
			err,
			service.afterNetworkTransitionEffect(
				ctx,
				plan,
				networkTransitionResultEffect(
					result,
					"network-drain",
				),
			),
		)
		return rollback(err)
	}
	if err := validateNetworkTransitionPhaseProof(
		plan,
		"network-drain",
		proof,
		candidateGeneration,
	); err != nil {
		return rollback(err)
	}
	result.ConnectionsRetained = proof.ConnectionsRetained
	setNetworkTransitionEffect(
		&result,
		"network-drain",
		EffectSucceeded,
		networkTransitionEvidence(
			"network-existing-connections-draining",
			plan.EnvironmentID,
			proof,
		),
	)
	if err := service.afterNetworkTransitionEffect(
		ctx,
		plan,
		networkTransitionResultEffect(result, "network-drain"),
	); err != nil {
		return rollback(err)
	}

	if err := handle.CommitNetworkCandidate(ctx); err != nil {
		return rollback(err)
	}
	result.Phase = NetworkTransitionSucceeded
	result.Effective = plan.Desired
	result.EffectiveProved = true
	result.Recovery = Recovery{
		Code:    "network-transition-complete",
		Summary: "The candidate route is proved and committed for new connections.",
	}
	return result, nil
}

func (service NetworkTransitionService) beforeNetworkTransitionEffect(
	ctx context.Context,
	plan NetworkTransitionPlan,
	id string,
) error {
	if service.Checkpoints == nil {
		return nil
	}
	for _, effect := range plan.Effects {
		if effect.ID == id {
			return service.Checkpoints.BeforeNetworkTransitionEffect(
				ctx,
				plan,
				effect,
			)
		}
	}
	return ErrInvalidNetworkTransition
}

func (service NetworkTransitionService) afterNetworkTransitionEffect(
	ctx context.Context,
	plan NetworkTransitionPlan,
	effect EffectResult,
) error {
	if service.Checkpoints == nil {
		return nil
	}
	if effect.ID == "" {
		return ErrInvalidNetworkTransition
	}
	return service.Checkpoints.AfterNetworkTransitionEffect(
		ctx,
		plan,
		effect,
	)
}

func networkTransitionResultEffect(
	result NetworkTransitionResult,
	id string,
) EffectResult {
	for _, effect := range result.Effects {
		if effect.ID == id {
			return effect
		}
	}
	return EffectResult{}
}

func (configuration NetworkRouteConfiguration) Validate() error {
	if strings.TrimSpace(configuration.Mode) != configuration.Mode {
		return ErrInvalidNetworkTransition
	}
	switch configuration.Mode {
	case netpolicy.ModeDirect:
		if configuration.ProxySecretRef != "" ||
			configuration.ProxySecretGeneration != 0 ||
			configuration.MediatedResolver != "" {
			return ErrInvalidNetworkTransition
		}
	case netpolicy.ModeTun2Socks:
		if secrets.ValidateRef(configuration.ProxySecretRef) != nil ||
			configuration.ProxySecretGeneration == 0 ||
			net.ParseIP(configuration.MediatedResolver) == nil ||
			strings.TrimSpace(configuration.MediatedResolver) !=
				configuration.MediatedResolver {
			return ErrInvalidNetworkTransition
		}
	default:
		return ErrInvalidNetworkTransition
	}
	return nil
}

func (plan *NetworkTransitionPlan) Seal() error {
	if plan == nil {
		return ErrInvalidNetworkTransition
	}
	plan.PlanDigest = ""
	if err := plan.validate(false); err != nil {
		return err
	}
	digest, err := CanonicalDigest(networkTransitionDigestDomain, *plan)
	if err != nil {
		return err
	}
	plan.PlanDigest = digest
	return plan.Validate()
}

func (plan NetworkTransitionPlan) Validate() error {
	return plan.validate(true)
}

func (plan NetworkTransitionPlan) VerifyDigest() error {
	if err := plan.Validate(); err != nil {
		return err
	}
	provided := plan.PlanDigest
	plan.PlanDigest = ""
	expected, err := CanonicalDigest(networkTransitionDigestDomain, plan)
	if err != nil {
		return err
	}
	if provided != expected {
		return ErrInvalidNetworkTransition
	}
	return nil
}

func (plan NetworkTransitionPlan) validate(requireDigest bool) error {
	if plan.Schema != NetworkTransitionPlanSchema ||
		!networkTransitionEnvironmentPattern.MatchString(
			plan.EnvironmentID,
		) ||
		plan.From.Validate() != nil ||
		plan.Desired.Validate() != nil ||
		len(plan.Effects) != 5 ||
		len(plan.Blockers) > maxPlanReviewItems {
		return ErrInvalidNetworkTransition
	}
	if requireDigest &&
		!profileDigestPattern.MatchString(plan.PlanDigest) {
		return ErrInvalidNetworkTransition
	}
	if !requireDigest && plan.PlanDigest != "" {
		return ErrInvalidNetworkTransition
	}
	kind, err := classifyNetworkTransition(plan.From, plan.Desired)
	if err != nil || kind != plan.Kind {
		return ErrInvalidNetworkTransition
	}
	expectedEffects := plannedNetworkTransitionEffects(plan.Kind)
	for index, effect := range plan.Effects {
		if effect.Validate() != nil ||
			!sameNetworkTransitionEffect(
				effect,
				expectedEffects[index],
			) {
			return ErrInvalidNetworkTransition
		}
	}
	previous := ""
	for _, blocker := range plan.Blockers {
		if blocker.Validate() != nil {
			return ErrInvalidNetworkTransition
		}
		switch blocker.Code {
		case "network-posture-session-active",
			"network-posture-session-unprovable":
			if !networkTransitionSessionPattern.MatchString(blocker.Owner) ||
				(previous != "" && previous >= blocker.Owner) {
				return ErrInvalidNetworkTransition
			}
			previous = blocker.Owner
		case "network-posture-session-inventory-unavailable":
			if blocker.Owner != "" || len(plan.Blockers) != 1 {
				return ErrInvalidNetworkTransition
			}
		default:
			return ErrInvalidNetworkTransition
		}
	}
	if plan.Kind != NetworkTransitionPosture &&
		len(plan.Blockers) != 0 {
		return ErrInvalidNetworkTransition
	}
	expectedRollback := plannedNetworkTransitionRollback()
	if plan.Rollback.Validate() != nil ||
		plan.Rollback.Mode != expectedRollback.Mode ||
		plan.Rollback.Summary != expectedRollback.Summary ||
		!slices.Equal(
			plan.Rollback.Effects,
			expectedRollback.Effects,
		) {
		return ErrInvalidNetworkTransition
	}
	return nil
}

func (proof NetworkTransitionProof) Validate(
	expected NetworkRouteConfiguration,
) error {
	if expected.Validate() != nil ||
		proof.RouteGeneration == 0 ||
		proof.ObservedAt.IsZero() ||
		proof.MediatedResolver != "" &&
			net.ParseIP(proof.MediatedResolver) == nil ||
		proof.RuntimeBootID != "" &&
			!environmentNetworkBootPattern.MatchString(
				proof.RuntimeBootID,
			) {
		return ErrInvalidNetworkTransition
	}
	if expected.Mode == netpolicy.ModeDirect {
		if proof.SecretGeneration != 0 {
			return ErrInvalidNetworkTransition
		}
		return nil
	}
	if proof.SecretGeneration != expected.ProxySecretGeneration {
		return ErrInvalidNetworkTransition
	}
	return nil
}

func validateNetworkTransitionPhaseProof(
	plan NetworkTransitionPlan,
	effectID string,
	proof NetworkTransitionProof,
	candidateGeneration uint64,
) error {
	if err := proof.ValidateCandidate(
		plan.Desired,
		candidateGeneration,
	); err != nil {
		return err
	}
	if plan.Kind != NetworkTransitionDNS {
		return nil
	}
	resolver := plan.Desired.MediatedResolver
	switch effectID {
	case "network-stage", "network-probe":
		resolver = plan.From.MediatedResolver
	case "network-activate", "network-prove", "network-drain":
	default:
		return ErrInvalidNetworkTransition
	}
	if proof.MediatedResolver != resolver ||
		!environmentNetworkBootPattern.MatchString(
			proof.RuntimeBootID,
		) {
		return ErrInvalidNetworkTransition
	}
	return nil
}

func validateNetworkTransitionRollbackProof(
	plan NetworkTransitionPlan,
	proof NetworkTransitionProof,
) error {
	if err := proof.Validate(plan.From); err != nil {
		return err
	}
	if plan.Kind == NetworkTransitionDNS &&
		(proof.MediatedResolver !=
			plan.From.MediatedResolver ||
			!environmentNetworkBootPattern.MatchString(
				proof.RuntimeBootID,
			)) {
		return ErrInvalidNetworkTransition
	}
	return nil
}

// ValidateCandidate binds every stage/probe/activation/proof observation to
// the one immutable route generation allocated during staging.
func (proof NetworkTransitionProof) ValidateCandidate(
	expected NetworkRouteConfiguration,
	candidateGeneration uint64,
) error {
	if proof.Validate(expected) != nil ||
		candidateGeneration == 0 ||
		proof.RouteGeneration != candidateGeneration {
		return ErrInvalidNetworkTransition
	}
	return nil
}

func classifyNetworkTransition(
	current NetworkRouteConfiguration,
	desired NetworkRouteConfiguration,
) (string, error) {
	if current == desired {
		return "", ErrNetworkTransitionNoChange
	}
	if current.Mode != desired.Mode {
		return NetworkTransitionPosture, nil
	}
	if desired.Mode != netpolicy.ModeTun2Socks {
		return "", ErrInvalidNetworkTransition
	}
	if current.ProxySecretRef != desired.ProxySecretRef ||
		current.ProxySecretGeneration != desired.ProxySecretGeneration {
		return NetworkTransitionRoute, nil
	}
	if current.MediatedResolver != desired.MediatedResolver {
		return NetworkTransitionDNS, nil
	}
	return "", ErrNetworkTransitionNoChange
}

func plannedNetworkTransitionEffects(kind string) []PlannedEffect {
	return []PlannedEffect{
		{
			ID: "network-stage", Kind: "stage", Scope: "environment",
			Provider: networkTransitionProviderName, Live: true,
			Summary: "Stage the candidate " + kind +
				" route without changing traffic.",
			ProofRequired: []string{"network-candidate-staged"},
		},
		{
			ID: "network-probe", Kind: "prove", Scope: "environment",
			Provider: networkTransitionProviderName, Live: true,
			Summary: "Validate candidate reachability and proxy protocol " +
				"without exposing secret material.",
			ProofRequired: []string{"network-candidate-probed"},
		},
		{
			ID: "network-activate", Kind: "activate",
			Scope:    "active-connections",
			Provider: networkTransitionProviderName, Live: true,
			Summary: "Atomically select the candidate route for newly " +
				"accepted connections.",
			ProofRequired: []string{"network-route-activated"},
		},
		{
			ID: "network-prove", Kind: "prove", Scope: "environment",
			Provider: networkTransitionProviderName, Live: true,
			Summary:       "Prove the selected route and DNS posture before commit.",
			ProofRequired: []string{"network-route-proved"},
		},
		{
			ID: "network-drain", Kind: "drain",
			Scope:    "active-connections",
			Provider: networkTransitionProviderName, Live: true,
			Summary: "Retain existing connection bindings while their prior " +
				"route generation drains naturally.",
			ProofRequired: []string{
				"network-existing-connections-draining",
			},
		},
	}
}

func sameNetworkTransitionEffect(
	left PlannedEffect,
	right PlannedEffect,
) bool {
	return left.ID == right.ID &&
		left.Kind == right.Kind &&
		left.Scope == right.Scope &&
		left.Provider == right.Provider &&
		left.Live == right.Live &&
		left.Summary == right.Summary &&
		slices.Equal(left.ProofRequired, right.ProofRequired)
}

func plannedNetworkTransitionRollback() RollbackPlan {
	return RollbackPlan{
		Mode: "restore-previous",
		Summary: "Restore the prior effective route for new connections " +
			"and retain already accepted connection bindings.",
		Effects: []string{
			"network-stage",
			"network-probe",
			"network-activate",
			"network-prove",
			"network-drain",
		},
	}
}

func (service NetworkTransitionService) postureBlockers(
	ctx context.Context,
	environmentID string,
	kind string,
) []Blocker {
	if kind != NetworkTransitionPosture {
		return []Blocker{}
	}
	if service.Sessions == nil {
		return []Blocker{networkInventoryUnavailableBlocker(environmentID)}
	}
	sessions, err := service.Sessions.NetworkTransitionSessions(
		ctx,
		environmentID,
	)
	if err != nil {
		return []Blocker{networkInventoryUnavailableBlocker(environmentID)}
	}
	blockers := make([]Blocker, 0, len(sessions))
	seen := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		if !networkTransitionSessionPattern.MatchString(session.ID) ||
			len(session.Phase) == 0 ||
			len(session.Phase) > 128 ||
			containsControlText(session.Phase) {
			return []Blocker{
				networkInventoryUnavailableBlocker(environmentID),
			}
		}
		if _, duplicate := seen[session.ID]; duplicate {
			return []Blocker{
				networkInventoryUnavailableBlocker(environmentID),
			}
		}
		seen[session.ID] = struct{}{}
		var blocker Blocker
		switch session.Status {
		case NetworkSessionStale:
			continue
		case NetworkSessionLive:
			blocker = Blocker{
				Code:     "network-posture-session-active",
				Resource: "session:" + session.ID,
				Owner:    session.ID, Phase: session.Phase,
				Summary: "The active session retains the current network " +
					"posture and will not be terminated automatically.",
				Recovery: "Wait for " + session.ID +
					" to finish, then review and apply again.",
			}
		case NetworkSessionUnprovable:
			blocker = Blocker{
				Code:     "network-posture-session-unprovable",
				Resource: "session:" + session.ID,
				Owner:    session.ID, Phase: session.Phase,
				Summary: "The session owner cannot be proved safe for a " +
					"network posture change.",
				Recovery: "Inspect " + session.ID +
					" and complete explicit session recovery before retrying.",
			}
		default:
			return []Blocker{
				networkInventoryUnavailableBlocker(environmentID),
			}
		}
		blockers = append(blockers, blocker)
	}
	sort.Slice(blockers, func(left, right int) bool {
		return blockers[left].Owner < blockers[right].Owner
	})
	if blockers == nil {
		return []Blocker{}
	}
	return blockers
}

func networkInventoryUnavailableBlocker(
	environmentID string,
) Blocker {
	return Blocker{
		Code:     "network-posture-session-inventory-unavailable",
		Resource: "environment:" + environmentID,
		Phase:    "unknown",
		Summary: "The complete active-session inventory is unavailable, so " +
			"safe posture eligibility cannot be proved.",
		Recovery: "Run hideout doctor, recover session ownership, and review " +
			"the transition again.",
	}
}

func validateNetworkCandidateMaterial(
	desired NetworkRouteConfiguration,
	material NetworkCandidateMaterial,
) error {
	if desired.Mode == netpolicy.ModeDirect {
		if material.UpstreamProxyURL != "" {
			return errors.New(
				"direct network candidate must not include proxy material",
			)
		}
		return nil
	}
	raw := material.UpstreamProxyURL
	if len(raw) == 0 ||
		len(raw) > maxNetworkCandidateBytes ||
		!utf8.ValidString(raw) ||
		strings.TrimSpace(raw) != raw ||
		strings.IndexByte(raw, 0) >= 0 {
		return errors.New("proxy candidate material is invalid")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return errors.New("proxy candidate material is invalid")
	}
	switch parsed.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return errors.New("proxy candidate scheme is unsupported")
	}
	if strings.EqualFold(parsed.Hostname(), "host.lima.internal") {
		return errors.New(
			"proxy candidate must use its host address, not the guest alias",
		)
	}
	return nil
}

func newNetworkTransitionResult(
	plan NetworkTransitionPlan,
) NetworkTransitionResult {
	effects := make([]EffectResult, len(plan.Effects))
	for index, effect := range plan.Effects {
		effects[index] = EffectResult{
			ID: effect.ID, Kind: effect.Kind,
			Provider: effect.Provider, Status: EffectPending,
			Evidence: []EvidenceRef{},
		}
	}
	return NetworkTransitionResult{
		Schema:     NetworkTransitionResultSchema,
		PlanDigest: plan.PlanDigest, EnvironmentID: plan.EnvironmentID,
		Kind: plan.Kind, From: plan.From, Desired: plan.Desired,
		Effects:  effects,
		Blockers: append([]Blocker(nil), plan.Blockers...),
		Recovery: Recovery{
			Code:    "network-transition-pending",
			Summary: "The reviewed network transition has not completed.",
		},
	}
}

func setNetworkTransitionEffect(
	result *NetworkTransitionResult,
	id string,
	status string,
	evidence []EvidenceRef,
) {
	if result == nil {
		return
	}
	for index := range result.Effects {
		if result.Effects[index].ID != id {
			continue
		}
		result.Effects[index].Status = status
		result.Effects[index].Evidence = append(
			[]EvidenceRef(nil),
			evidence...,
		)
		return
	}
}

func networkTransitionEvidence(
	code string,
	environmentID string,
	proof NetworkTransitionProof,
) []EvidenceRef {
	ref := fmt.Sprintf(
		"environment:%s:route-generation:%d:secret-generation:%d:"+
			"connections-retained:%d",
		environmentID,
		proof.RouteGeneration,
		proof.SecretGeneration,
		proof.ConnectionsRetained,
	)
	if proof.MediatedResolver != "" {
		ref += ":resolver:" + proof.MediatedResolver
	}
	if proof.RuntimeBootID != "" {
		ref += ":boot:" + proof.RuntimeBootID
	}
	return []EvidenceRef{{
		Code:       code,
		Ref:        ref,
		ObservedAt: proof.ObservedAt.Round(0).UTC(),
	}}
}

func rollbackNetworkTransition(
	ctx context.Context,
	result NetworkTransitionResult,
	plan NetworkTransitionPlan,
	handle NetworkStagedCandidate,
	operationErr error,
	candidateGeneration uint64,
	candidateActivated bool,
) (NetworkTransitionResult, error) {
	proof, rollbackErr := handle.RollbackNetworkCandidate(ctx)
	if rollbackErr == nil {
		rollbackErr = validateNetworkTransitionRollbackProof(
			plan,
			proof,
		)
	}
	// Route/posture candidates allocate a new route generation. Their rollback
	// must therefore return to a different (and, after activation, newer)
	// generation. A DNS-only candidate deliberately retains the immutable
	// gateway generation and proves restoration through its exact resolver and
	// boot-bound runtime evidence instead.
	if rollbackErr == nil &&
		plan.Kind != NetworkTransitionDNS &&
		candidateGeneration != 0 &&
		proof.RouteGeneration == candidateGeneration {
		rollbackErr = ErrInvalidNetworkTransition
	}
	if rollbackErr == nil &&
		plan.Kind != NetworkTransitionDNS &&
		candidateActivated &&
		proof.RouteGeneration <= candidateGeneration {
		rollbackErr = ErrInvalidNetworkTransition
	}
	if rollbackErr != nil {
		for index := range result.Effects {
			switch result.Effects[index].Status {
			case EffectSucceeded, EffectRunning:
				result.Effects[index].Status = EffectUnproved
			}
		}
		result.Phase = NetworkTransitionRollbackUnproved
		result.Effective = NetworkRouteConfiguration{}
		result.EffectiveProved = false
		result.Recovery = networkTransitionRollbackUnprovedRecovery()
		return result, newNetworkTransitionApplyError(
			ErrNetworkTransitionRollbackUnproved,
			operationErr,
			rollbackErr,
		)
	}
	for index := range result.Effects {
		switch result.Effects[index].Status {
		case EffectSucceeded, EffectRunning:
			result.Effects[index].Status = EffectRolledBack
			result.Effects[index].Evidence = networkTransitionEvidence(
				"network-route-restored",
				plan.EnvironmentID,
				proof,
			)
		}
	}
	result.Phase = NetworkTransitionRolledBack
	result.Effective = plan.From
	result.EffectiveProved = true
	result.ConnectionsRetained = proof.ConnectionsRetained
	result.Recovery = networkTransitionRolledBackRecovery()
	return result, newNetworkTransitionApplyError(
		ErrNetworkTransitionRolledBack,
		operationErr,
	)
}

func proveNetworkTransitionUnchangedAfterStageFailure(
	ctx context.Context,
	provider NetworkTransitionProvider,
	result NetworkTransitionResult,
	plan NetworkTransitionPlan,
	stageErr error,
) (NetworkTransitionResult, error) {
	current, observeErr := provider.ObserveNetworkRoute(
		ctx,
		plan.EnvironmentID,
	)
	if observeErr == nil && current == plan.From {
		result.Phase = NetworkTransitionRolledBack
		result.Effective = plan.From
		result.EffectiveProved = true
		result.Recovery = networkTransitionRolledBackRecovery()
		return result, newNetworkTransitionApplyError(
			ErrNetworkTransitionRolledBack,
			stageErr,
		)
	}
	result.Phase = NetworkTransitionRollbackUnproved
	result.Effective = NetworkRouteConfiguration{}
	result.EffectiveProved = false
	result.Recovery = networkTransitionRollbackUnprovedRecovery()
	causes := []error{
		ErrNetworkTransitionRollbackUnproved,
		stageErr,
	}
	if observeErr != nil {
		causes = append(causes, observeErr)
	} else {
		causes = append(causes, ErrNetworkTransitionStale)
	}
	return result, newNetworkTransitionApplyError(causes...)
}

func networkTransitionBlockedRecovery() Recovery {
	return Recovery{
		Code: "network-transition-blocked",
		Summary: "The current session set is not eligible for this network " +
			"posture change.",
		NextAction: "Inspect every listed session and retry after all blockers " +
			"are safely resolved.",
	}
}

func networkTransitionRollbackUnprovedRecovery() Recovery {
	return Recovery{
		Code: "network-rollback-unproved",
		Summary: "Hideout could not prove whether the previous route was " +
			"restored.",
		NextAction: "Stop new attaches, run hideout doctor --network, " +
			"then explicitly recover or stop the affected environment.",
	}
}

func networkTransitionRolledBackRecovery() Recovery {
	return Recovery{
		Code:    "network-transition-rolled-back",
		Summary: "The candidate failed and the prior route was restored.",
		NextAction: "Inspect proxy and DNS readiness, then review a fresh " +
			"transition before retrying.",
	}
}

type networkTransitionApplyError struct {
	causes []error
}

func newNetworkTransitionApplyError(
	causes ...error,
) error {
	filtered := make([]error, 0, len(causes))
	for _, cause := range causes {
		if cause != nil {
			filtered = append(filtered, cause)
		}
	}
	return &networkTransitionApplyError{causes: filtered}
}

func (transitionErr *networkTransitionApplyError) Error() string {
	if transitionErr == nil {
		return ""
	}
	for _, cause := range transitionErr.causes {
		if errors.Is(cause, ErrNetworkTransitionRollbackUnproved) {
			return ErrNetworkTransitionRollbackUnproved.Error()
		}
	}
	return ErrNetworkTransitionRolledBack.Error()
}

func (transitionErr *networkTransitionApplyError) Unwrap() []error {
	if transitionErr == nil {
		return nil
	}
	return append([]error(nil), transitionErr.causes...)
}

func newNetworkTransitionProviderError(
	phase string,
	cause error,
) error {
	return &networkTransitionProviderError{
		phase: phase,
		cause: cause,
	}
}

type networkTransitionProviderError struct {
	phase string
	cause error
}

func (providerErr *networkTransitionProviderError) Error() string {
	if providerErr == nil {
		return ""
	}
	return "network transition provider failed during " +
		providerErr.phase
}

func (providerErr *networkTransitionProviderError) Unwrap() []error {
	if providerErr == nil {
		return nil
	}
	return []error{
		ErrNetworkTransitionProviderUnavailable,
		providerErr.cause,
	}
}
