package manager

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	OperatorSnapshotSchema        = "hideout.operator-snapshot.v1"
	DefaultOperatorActivityLimit  = 100
	MaxOperatorActivityLimit      = 500
	DefaultOperatorOperationLimit = 100

	OperatorHealthSeeding           = "seeding"
	OperatorHealthLive              = "live"
	OperatorHealthIdleLive          = "idle-live"
	OperatorHealthStale             = "stale"
	OperatorHealthDisconnected      = "disconnected"
	OperatorHealthCredentialExpired = "credential-expired"
	OperatorHealthSchemaMismatch    = "schema-mismatch"
	OperatorHealthDaemonless        = "daemon-less"

	OperatorCapabilityAvailable   = "available"
	OperatorCapabilityPartial     = "partial"
	OperatorCapabilityUnavailable = "unavailable"
)

var (
	operatorDaemonIDPattern      = regexp.MustCompile(`^daemon_[A-Za-z0-9_-]{1,124}$`)
	operatorSessionIDPattern     = regexp.MustCompile(`^ses_[A-Za-z0-9_-]{1,124}$`)
	operatorEnvironmentIDPattern = regexp.MustCompile(
		`^env_[A-Za-z0-9_-]{1,124}$`,
	)
	operatorRiskIDPattern = regexp.MustCompile(`^risk_[A-Za-z0-9_-]{8,124}$`)
	operatorCodePattern   = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
)

// ActivityProjection and CoverageProjection intentionally reuse the persisted,
// redacted workload contracts. The Manager never defines a less strict console
// representation of activity evidence.
type ActivityProjection = workloadtypes.ActivityRecord
type CoverageProjection = workloadtypes.CoverageInterval

type OperatorSnapshot struct {
	Schema                 string                                    `json:"schema"`
	GeneratedAt            time.Time                                 `json:"generatedAt"`
	InstanceID             string                                    `json:"instanceId"`
	CredentialGeneration   uint64                                    `json:"credentialGeneration"`
	Sequence               int                                       `json:"sequence"`
	StreamHealth           OperatorStreamHealth                      `json:"streamHealth"`
	Profiles               []ProfileProjection                       `json:"profiles"`
	Sessions               []OperatorSessionProjection               `json:"sessions"`
	Environments           []OperatorEnvironmentProjection           `json:"environments"`
	Activity               []ActivityProjection                      `json:"activity"`
	ActivityCursor         string                                    `json:"activityCursor,omitempty"`
	Coverage               []CoverageProjection                      `json:"coverage"`
	ActivityRetention      []OperatorActivityRetentionProjection     `json:"activityRetention"`
	ActivityStoreRetention *OperatorActivityStoreRetentionProjection `json:"activityStoreRetention,omitempty"`
	Risks                  []RiskFinding                             `json:"risks"`
	Operations             []Operation                               `json:"operations"`
	Capabilities           []OperatorCapabilityProjection            `json:"capabilities"`
	NextActions            []string                                  `json:"nextActions"`
}

type OperatorStreamHealth struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

type OperatorSessionProjection struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environmentId,omitempty"`
	Profile       string    `json:"profile,omitempty"`
	State         string    `json:"state"`
	Command       string    `json:"command"`
	StartedAt     time.Time `json:"startedAt,omitempty"`
}

// OperatorEnvironmentProjection is the bounded environment inventory needed
// to review lifecycle actions. It deliberately excludes runtime internals and
// carries no mutation authority; exact stop/clean targets are still resolved
// and authorized by the Manager plan/apply routes.
type OperatorEnvironmentProjection struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name,omitempty"`
	Profile              string    `json:"profile,omitempty"`
	Backend              string    `json:"backend,omitempty"`
	Status               string    `json:"status"`
	Workspace            string    `json:"workspace,omitempty"`
	InstanceName         string    `json:"instanceName,omitempty"`
	LastSessionID        string    `json:"lastSessionId,omitempty"`
	LastCommand          string    `json:"lastCommand,omitempty"`
	ActiveSessions       int       `json:"activeSessions"`
	ActiveWorkspaceViews int       `json:"activeWorkspaceViews"`
	OwnerHealth          string    `json:"ownerHealth,omitempty"`
	CreatedAt            time.Time `json:"createdAt,omitempty"`
	LastStartedAt        time.Time `json:"lastStartedAt,omitempty"`
	LastEndedAt          time.Time `json:"lastEndedAt,omitempty"`
}

type RiskFinding struct {
	ID           string    `json:"id"`
	RuleID       string    `json:"ruleId"`
	RuleVersion  string    `json:"ruleVersion"`
	Severity     string    `json:"severity"`
	Title        string    `json:"title"`
	Explanation  string    `json:"explanation,omitempty"`
	EvidenceRefs []string  `json:"evidenceRefs,omitempty"`
	Confidence   string    `json:"confidence"`
	PolicyStatus string    `json:"policyStatus,omitempty"`
	FirstAt      time.Time `json:"firstAt,omitempty"`
	LastAt       time.Time `json:"lastAt,omitempty"`
	Count        uint64    `json:"count,omitempty"`
	NextAction   string    `json:"nextAction,omitempty"`
}

type OperatorCapabilityProjection struct {
	ID         string   `json:"id"`
	State      string   `json:"state"`
	Provider   string   `json:"provider,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Mutable    bool     `json:"mutable,omitempty"`
	ActionRefs []string `json:"actionRefs,omitempty"`
}

type OperatorSnapshotQuery struct {
	Profile       string
	Session       string
	ActivityLimit int
}

func (query OperatorSnapshotQuery) Validate() error {
	if query.Profile != "" {
		if err := profile.ValidateName(query.Profile); err != nil {
			return fmt.Errorf("invalid profile scope: %w", err)
		}
	}
	if query.Session != "" &&
		(!session.ValidID(query.Session) || !operatorSessionIDPattern.MatchString(query.Session)) {
		return errors.New("invalid session scope")
	}
	if query.ActivityLimit < 0 || query.ActivityLimit > MaxOperatorActivityLimit {
		return fmt.Errorf("activityLimit must be between 0 and %d", MaxOperatorActivityLimit)
	}
	return nil
}

type OperatorSnapshotProvider interface {
	Snapshot(context.Context, OperatorSnapshotQuery) (OperatorSnapshot, error)
}

type OperatorSnapshotProviderFunc func(context.Context, OperatorSnapshotQuery) (OperatorSnapshot, error)

func (provider OperatorSnapshotProviderFunc) Snapshot(
	ctx context.Context,
	query OperatorSnapshotQuery,
) (OperatorSnapshot, error) {
	if provider == nil {
		return OperatorSnapshot{}, errors.New("operator snapshot provider is unavailable")
	}
	return provider(ctx, query)
}

type OperatorConnectionProjection struct {
	InstanceID           string
	CredentialGeneration uint64
	Sequence             int
	StreamHealth         OperatorStreamHealth
}

type OperatorConnectionProvider interface {
	OperatorConnection(context.Context) (OperatorConnectionProjection, error)
}

type OperatorConnectionProviderFunc func(context.Context) (OperatorConnectionProjection, error)

func (provider OperatorConnectionProviderFunc) OperatorConnection(
	ctx context.Context,
) (OperatorConnectionProjection, error) {
	if provider == nil {
		return OperatorConnectionProjection{}, errors.New("operator connection provider is unavailable")
	}
	return provider(ctx)
}

type OperatorObservation struct {
	Activity               []ActivityProjection
	ActivityCursor         string
	Coverage               []CoverageProjection
	ActivityRetention      []OperatorActivityRetentionProjection
	ActivityStoreRetention *OperatorActivityStoreRetentionProjection
	Risks                  []RiskFinding
	Capabilities           []OperatorCapabilityProjection
}

// OperatorActivityRetentionProjection makes the limits of retained local
// evidence visible without exposing store paths or mutation authority.
type OperatorActivityRetentionProjection struct {
	Owner         workloadtypes.ActivityOwner `json:"owner"`
	EarliestAt    time.Time                   `json:"earliestAt,omitempty"`
	LatestAt      time.Time                   `json:"latestAt,omitempty"`
	UsedBytes     uint64                      `json:"usedBytes"`
	LimitBytes    uint64                      `json:"limitBytes"`
	MaxAgeSeconds int64                       `json:"maxAgeSeconds"`
	Pruned        bool                        `json:"pruned"`
	Corrupt       bool                        `json:"corrupt"`
	Reasons       []string                    `json:"reasons"`
}

func (projection OperatorActivityRetentionProjection) Validate() error {
	if err := projection.Owner.Validate(); err != nil {
		return err
	}
	if projection.EarliestAt.IsZero() != projection.LatestAt.IsZero() ||
		(!projection.EarliestAt.IsZero() &&
			projection.LatestAt.Before(projection.EarliestAt)) ||
		projection.MaxAgeSeconds < 0 ||
		projection.MaxAgeSeconds >
			workloadtypes.MaximumActivityRetentionMaxAgeSeconds ||
		len(projection.Reasons) > 256 ||
		!slices.IsSorted(projection.Reasons) {
		return errors.New("operator activity retention projection is invalid")
	}
	for index, reason := range projection.Reasons {
		if !operatorCodePattern.MatchString(reason) ||
			index > 0 && reason == projection.Reasons[index-1] {
			return errors.New("operator activity retention projection is invalid")
		}
	}
	return nil
}

// OperatorActivityStoreRetentionProjection exposes the daemon-wide safety
// ceiling separately from per-profile desired policy. It is intentionally
// read-only on the profile configuration surface.
type OperatorActivityStoreRetentionProjection struct {
	UsedBytes              uint64 `json:"usedBytes"`
	LimitBytes             uint64 `json:"limitBytes"`
	DefaultOwnerLimitBytes uint64 `json:"defaultOwnerLimitBytes"`
	ActiveSegmentBytes     uint64 `json:"activeSegmentBytes"`
	Owners                 int    `json:"owners"`
	Segments               int    `json:"segments"`
}

func (projection OperatorActivityStoreRetentionProjection) Validate() error {
	if projection.LimitBytes == 0 ||
		projection.DefaultOwnerLimitBytes == 0 ||
		projection.ActiveSegmentBytes == 0 ||
		projection.Owners < 0 ||
		projection.Segments < 0 {
		return errors.New(
			"operator activity store retention projection is invalid",
		)
	}
	return nil
}

type OperatorObservationProvider interface {
	OperatorObservation(context.Context, OperatorSnapshotQuery) (OperatorObservation, error)
}

type OperatorObservationProviderFunc func(context.Context, OperatorSnapshotQuery) (OperatorObservation, error)

func (provider OperatorObservationProviderFunc) OperatorObservation(
	ctx context.Context,
	query OperatorSnapshotQuery,
) (OperatorObservation, error) {
	if provider == nil {
		return OperatorObservation{}, errors.New("operator observation provider is unavailable")
	}
	return provider(ctx, query)
}

type OperatorOverviewProvider interface {
	Overview(context.Context) (Overview, error)
}

type OperatorOverviewProviderFunc func(context.Context) (Overview, error)

func (provider OperatorOverviewProviderFunc) Overview(ctx context.Context) (Overview, error) {
	if provider == nil {
		return Overview{}, errors.New("operator overview provider is unavailable")
	}
	return provider(ctx)
}

type OperatorProfileProjectionProvider interface {
	Load(string) (ProfileProjection, error)
}

type OperatorProfileProjectionProviderFunc func(string) (ProfileProjection, error)

func (provider OperatorProfileProjectionProviderFunc) Load(name string) (ProfileProjection, error) {
	if provider == nil {
		return ProfileProjection{}, errors.New("profile projection provider is unavailable")
	}
	return provider(name)
}

type OperatorNetworkRouteObserver interface {
	ObserveNetworkRoute(
		context.Context,
		string,
	) (NetworkRouteConfiguration, error)
}

type OperatorNetworkRouteObserverFunc func(
	context.Context,
	string,
) (NetworkRouteConfiguration, error)

func (observer OperatorNetworkRouteObserverFunc) ObserveNetworkRoute(
	ctx context.Context,
	environmentID string,
) (NetworkRouteConfiguration, error) {
	if observer == nil {
		return NetworkRouteConfiguration{},
			ErrNetworkTransitionProviderUnavailable
	}
	return observer(ctx, environmentID)
}

type OperatorOperationProvider interface {
	List(int) ([]Operation, error)
}

type OperatorOperationProviderFunc func(int) ([]Operation, error)

func (provider OperatorOperationProviderFunc) List(limit int) ([]Operation, error) {
	if provider == nil {
		return nil, errors.New("operation history provider is unavailable")
	}
	return provider(limit)
}

// OperatorSnapshotService is the single Manager-domain seed builder shared by
// the CLI, TUI, WebUI, and authenticated API. Optional deep-observation
// providers degrade to explicit capabilities; they never manufacture empty
// Available evidence.
type OperatorSnapshotService struct {
	Core               Core
	Overview           OperatorOverviewProvider
	Connection         OperatorConnectionProvider
	Observation        OperatorObservationProvider
	ProfileProjections OperatorProfileProjectionProvider
	NetworkRoutes      OperatorNetworkRouteObserver
	OperationHistory   OperatorOperationProvider
	// MutationCapabilities is appended before canonical sorting. Nil
	// advertises the Manager profile-transaction surface; daemon hosts may add
	// secret lifecycle support. A non-nil empty slice advertises no mutations.
	MutationCapabilities []OperatorCapabilityProjection
	Now                  func() time.Time
}

func (service OperatorSnapshotService) Snapshot(
	ctx context.Context,
	query OperatorSnapshotQuery,
) (OperatorSnapshot, error) {
	return service.Build(ctx, query)
}

func (service OperatorSnapshotService) Build(
	ctx context.Context,
	query OperatorSnapshotQuery,
) (OperatorSnapshot, error) {
	if err := query.Validate(); err != nil {
		return OperatorSnapshot{}, err
	}
	if service.Core.Store.Root == "" {
		return OperatorSnapshot{}, errors.New("manager store root is required")
	}

	overviewSource := service.Overview
	if overviewSource == nil {
		overviewSource = service.Core
	}
	overview, overviewErr := overviewSource.Overview(ctx)
	if overview.Version == "" {
		return OperatorSnapshot{}, overviewErr
	}
	sessions := scopeOperatorSessions(overview.Sessions, query)
	environments := scopeOperatorEnvironments(
		overview.Environments,
		sessions,
		query,
	)
	profiles, profileCapability := service.profileProjections(
		ctx,
		overview,
		sessions,
		environments,
		query,
	)
	operations, operationCapability := service.operations(overview, query)
	for index := range profiles {
		profiles[index].Transition = profileTransitionFromOperations(
			profiles[index].Profile,
			environments,
			operations,
		)
	}
	observation, observationCapabilities := service.observation(ctx, query)
	connection, err := service.connection(ctx)
	if err != nil {
		return OperatorSnapshot{}, err
	}
	if connection.StreamHealth.State == OperatorHealthLive && len(sessions) == 0 {
		connection.StreamHealth.State = OperatorHealthIdleLive
	} else if connection.StreamHealth.State == OperatorHealthIdleLive && len(sessions) > 0 {
		connection.StreamHealth.State = OperatorHealthLive
	}

	capabilities := append([]OperatorCapabilityProjection(nil), observationCapabilities...)
	mutationCapabilities := service.MutationCapabilities
	if mutationCapabilities == nil {
		mutationCapabilities = DefaultConfigurationCapabilities(false)
	}
	capabilities = append(capabilities, mutationCapabilities...)
	if overviewErr != nil {
		capabilities = append(capabilities, OperatorCapabilityProjection{
			ID: "manager.overview", State: OperatorCapabilityPartial,
			Provider: "manager", Reason: "one or more overview sources are unavailable",
			ActionRefs: []string{"doctor.overview"},
		})
	}
	if profileCapability != nil {
		capabilities = append(capabilities, *profileCapability)
	}
	if operationCapability != nil {
		capabilities = append(capabilities, *operationCapability)
	}
	capabilities = normalizeOperatorCapabilities(capabilities)

	snapshot := OperatorSnapshot{
		Schema:               OperatorSnapshotSchema,
		GeneratedAt:          service.now(),
		InstanceID:           connection.InstanceID,
		CredentialGeneration: connection.CredentialGeneration,
		Sequence:             connection.Sequence,
		StreamHealth:         connection.StreamHealth,
		Profiles:             nonNilSlice(profiles),
		Sessions:             nonNilSlice(sessions),
		Environments:         nonNilSlice(environments),
		Activity:             nonNilSlice(observation.Activity),
		ActivityCursor:       observation.ActivityCursor,
		Coverage:             nonNilSlice(observation.Coverage),
		ActivityRetention:    nonNilSlice(observation.ActivityRetention),
		ActivityStoreRetention: cloneOperatorActivityStoreRetention(
			observation.ActivityStoreRetention,
		),
		Risks:        nonNilSlice(observation.Risks),
		Operations:   nonNilSlice(operations),
		Capabilities: nonNilSlice(capabilities),
	}
	snapshot.Activity = scopeOperatorActivity(snapshot.Activity, overview, query)
	snapshot.Coverage = scopeOperatorCoverage(snapshot.Coverage, overview, query)
	snapshot.ActivityRetention = scopeOperatorActivityRetention(
		snapshot.ActivityRetention,
		overview,
		query,
	)
	if query.Session != "" && query.Profile != "" && len(sessions) == 0 {
		snapshot.Activity = []ActivityProjection{}
		snapshot.ActivityCursor = ""
		snapshot.Coverage = []CoverageProjection{}
		snapshot.ActivityRetention = []OperatorActivityRetentionProjection{}
		snapshot.Risks = []RiskFinding{}
		snapshot.Environments = []OperatorEnvironmentProjection{}
	}
	if len(snapshot.Activity) > query.ActivityLimit {
		snapshot.Activity = snapshot.Activity[:query.ActivityLimit]
	}
	sortOperatorSnapshot(&snapshot)
	snapshot.NextActions = operatorNextActions(snapshot)
	if err := snapshot.Validate(); err != nil {
		return OperatorSnapshot{}, fmt.Errorf("build operator snapshot: %w", err)
	}
	return snapshot, nil
}

func (service OperatorSnapshotService) connection(ctx context.Context) (OperatorConnectionProjection, error) {
	if service.Connection == nil {
		return OperatorConnectionProjection{
			InstanceID: "daemon_daemonless", CredentialGeneration: 1, Sequence: 0,
			StreamHealth: OperatorStreamHealth{
				State:  OperatorHealthDaemonless,
				Reason: "no authenticated daemon event stream",
			},
		}, nil
	}
	connection, err := service.Connection.OperatorConnection(ctx)
	if err != nil {
		return OperatorConnectionProjection{}, err
	}
	if err := connection.Validate(); err != nil {
		return OperatorConnectionProjection{}, err
	}
	return connection, nil
}

func (connection OperatorConnectionProjection) Validate() error {
	if !operatorDaemonIDPattern.MatchString(connection.InstanceID) ||
		connection.CredentialGeneration == 0 || connection.Sequence < 0 {
		return errors.New("operator connection projection is invalid")
	}
	return connection.StreamHealth.Validate()
}

func (service OperatorSnapshotService) profileProjections(
	ctx context.Context,
	overview Overview,
	sessions []OperatorSessionProjection,
	environments []OperatorEnvironmentProjection,
	query OperatorSnapshotQuery,
) ([]ProfileProjection, *OperatorCapabilityProjection) {
	source := service.ProfileProjections
	if source == nil {
		source = ProfileProjectionService{Store: service.Core.Store, Now: service.Now}
	}
	names := operatorProfileNames(overview, sessions, query)
	projections := make([]ProfileProjection, 0, len(names))
	failed := 0
	for _, name := range names {
		projection, err := source.Load(name)
		if err != nil {
			failed++
			continue
		}
		projection = service.enrichProfileProjection(
			ctx,
			projection,
			overview.Sessions,
			sessions,
			environments,
		)
		projections = append(projections, projection)
	}
	if failed == 0 {
		return projections, nil
	}
	state := OperatorCapabilityPartial
	if len(projections) == 0 {
		state = OperatorCapabilityUnavailable
	}
	return projections, &OperatorCapabilityProjection{
		ID: "profile.projection", State: state, Provider: "manager",
		Reason:     "one or more profile projections could not be loaded",
		ActionRefs: []string{"doctor.profiles"},
	}
}

func (service OperatorSnapshotService) enrichProfileProjection(
	ctx context.Context,
	projection ProfileProjection,
	sourceSessions []SessionSummary,
	scopedSessions []OperatorSessionProjection,
	environments []OperatorEnvironmentProjection,
) ProfileProjection {
	currentSnapshotID := ""
	if _, snapshotID, err := SessionSnapshotForProfile(
		projection.Desired,
	); err == nil {
		currentSnapshotID = snapshotID
	}
	selected := make(map[string]struct{}, len(scopedSessions))
	for _, scoped := range scopedSessions {
		if scoped.Profile == projection.Profile {
			selected[scoped.ID] = struct{}{}
		}
	}
	liveEnvironment := make(map[string]bool)
	effectiveSessions := make(
		[]EffectiveSessionSnapshot,
		0,
		len(selected),
	)
	sessionUnproved := false
	for _, candidate := range sourceSessions {
		if candidate.Profile != projection.Profile {
			continue
		}
		if _, ok := selected[candidate.ID]; !ok {
			continue
		}
		if candidate.OwnerStatus != session.OwnerLive {
			continue
		}
		if candidate.EnvironmentID != "" {
			liveEnvironment[candidate.EnvironmentID] = true
		}
		if !environment.ValidConfigurationID(
			candidate.SessionSnapshotID,
		) {
			sessionUnproved = true
			continue
		}
		current := currentSnapshotID != "" &&
			candidate.SessionSnapshotID == currentSnapshotID
		snapshot := EffectiveSessionSnapshot{
			SessionID:  candidate.ID,
			SnapshotID: candidate.SessionSnapshotID,
			Current:    current,
		}
		if current {
			snapshot.ProfileRevision = projection.Revision
		}
		effectiveSessions = append(
			effectiveSessions,
			snapshot,
		)
	}
	sort.Slice(effectiveSessions, func(left, right int) bool {
		return effectiveSessions[left].SessionID <
			effectiveSessions[right].SessionID
	})
	projection.Effective.Sessions = effectiveSessions

	network, observed, unproved :=
		service.observeProfileEffectiveNetwork(
			ctx,
			projection.Profile,
			environments,
			liveEnvironment,
		)
	switch {
	case unproved || sessionUnproved:
		projection.Effective.Status = EffectiveUnproved
		if unproved {
			projection.Effective.Network = nil
		} else if observed {
			projection.Effective.Network = network
		}
	case observed:
		projection.Effective.Status = EffectiveCurrent
		projection.Effective.Network = network
	default:
		projection.Effective.Status = EffectiveNotObserved
		projection.Effective.Network = nil
	}
	return projection
}

func (service OperatorSnapshotService) observeProfileEffectiveNetwork(
	ctx context.Context,
	profileName string,
	environments []OperatorEnvironmentProjection,
	liveEnvironment map[string]bool,
) (*EffectiveNetwork, bool, bool) {
	if service.NetworkRoutes == nil {
		return nil, false, len(liveEnvironment) != 0
	}
	environmentIDs := make([]string, 0, len(environments))
	profileEnvironments := make(map[string]struct{}, len(environments))
	for _, candidate := range environments {
		if candidate.Profile == profileName {
			environmentIDs = append(
				environmentIDs,
				candidate.ID,
			)
			profileEnvironments[candidate.ID] = struct{}{}
		}
	}
	sort.Strings(environmentIDs)
	var (
		effective NetworkRouteConfiguration
		observed  bool
		unproved  bool
	)
	for environmentID := range liveEnvironment {
		if _, exists := profileEnvironments[environmentID]; !exists {
			unproved = true
		}
	}
	for _, environmentID := range environmentIDs {
		current, err := service.NetworkRoutes.ObserveNetworkRoute(
			ctx,
			environmentID,
		)
		if err != nil {
			if liveEnvironment[environmentID] {
				unproved = true
			}
			continue
		}
		if current.Validate() != nil {
			unproved = true
			continue
		}
		if observed && current != effective {
			unproved = true
			continue
		}
		effective = current
		observed = true
	}
	if unproved || !observed {
		return nil, observed, unproved
	}
	mode := effective.Mode
	dns := "system"
	if mode == netpolicy.ModeTun2Socks {
		mode = "proxy"
		dns = effective.MediatedResolver
	}
	return &EffectiveNetwork{
		Mode:             mode,
		ProxySecretRef:   effective.ProxySecretRef,
		SecretGeneration: effective.ProxySecretGeneration,
		DNS:              dns,
		ObservedAt:       service.now(),
	}, true, false
}

func profileTransitionFromOperations(
	profileName string,
	environments []OperatorEnvironmentProjection,
	operations []Operation,
) *ProfileTransition {
	environmentSet := make(
		map[string]struct{},
		len(environments),
	)
	for _, candidate := range environments {
		if candidate.Profile == profileName {
			environmentSet[candidate.ID] = struct{}{}
		}
	}
	for _, operation := range operations {
		if operation.Kind != "profile.transaction" &&
			operation.Kind != networkTransitionOperationKind {
			continue
		}
		matches := operation.Owner.Kind == "profile" &&
			operation.Owner.ID == profileName
		if operation.Owner.Kind == "environment" {
			_, matches = environmentSet[operation.Owner.ID]
		}
		if !matches ||
			operation.Phase == OperationSucceeded ||
			operation.Phase == OperationCancelled {
			continue
		}
		if operation.Terminal() &&
			operation.Phase != OperationFailed &&
			operation.Phase != OperationRollbackUnproved {
			continue
		}
		phase, ok := profileTransitionPhase(operation.Phase)
		if !ok {
			continue
		}
		blockers := []string{}
		if operation.Recovery.Code != "" &&
			operation.Recovery.Code != "retry-operation" &&
			operation.Recovery.Code != "operation-terminal" {
			blockers = append(
				blockers,
				operation.Recovery.Code,
			)
		} else if operation.Result != nil &&
			operation.Result.Code != "" {
			blockers = append(
				blockers,
				operation.Result.Code,
			)
		}
		return &ProfileTransition{
			OperationID: operation.ID,
			Kind:        operation.Kind,
			Phase:       phase,
			Blockers:    blockers,
			StartedAt:   operation.CreatedAt,
		}
	}
	return nil
}

func profileTransitionPhase(operationPhase string) (string, bool) {
	switch operationPhase {
	case OperationPlanned, OperationClaimed, OperationStaging:
		return OperationStaging, true
	case OperationActivating:
		return OperationActivating, true
	case OperationProving:
		return OperationProving, true
	case OperationRollingBack:
		return OperationRollingBack, true
	case OperationRecoveryRequired, OperationFailed, OperationRollbackUnproved:
		return OperationRecoveryRequired, true
	default:
		return "", false
	}
}

func (service OperatorSnapshotService) operations(
	overview Overview,
	query OperatorSnapshotQuery,
) ([]Operation, *OperatorCapabilityProjection) {
	source := service.OperationHistory
	if source == nil {
		source = OperationStore{Root: service.Core.Store.Root, Now: service.Now}
	}
	operations, err := source.List(MaxOperatorActivityLimit)
	if err != nil {
		return nil, &OperatorCapabilityProjection{
			ID: "operations.history", State: OperatorCapabilityUnavailable,
			Provider: "manager", Reason: "durable operation history is unavailable",
			ActionRefs: []string{"doctor.operations"},
		}
	}
	operations = scopeOperatorOperations(operations, overview, query)
	if len(operations) > DefaultOperatorOperationLimit {
		operations = operations[:DefaultOperatorOperationLimit]
	}
	return operations, nil
}

func (service OperatorSnapshotService) observation(
	ctx context.Context,
	query OperatorSnapshotQuery,
) (OperatorObservation, []OperatorCapabilityProjection) {
	if service.Observation == nil {
		return OperatorObservation{}, unavailableObservationCapabilities("workload observation provider is not configured")
	}
	observation, err := service.Observation.OperatorObservation(ctx, query)
	if err != nil {
		return OperatorObservation{}, unavailableObservationCapabilities("workload observation provider failed")
	}
	return observation, fillObservationCapabilities(observation.Capabilities)
}

func (service OperatorSnapshotService) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}

func (snapshot OperatorSnapshot) Validate() error {
	if snapshot.Schema != OperatorSnapshotSchema || snapshot.GeneratedAt.IsZero() ||
		!operatorDaemonIDPattern.MatchString(snapshot.InstanceID) ||
		snapshot.CredentialGeneration == 0 || snapshot.Sequence < 0 ||
		len(snapshot.Profiles) > 256 || len(snapshot.Sessions) > 256 ||
		len(snapshot.Environments) > 256 ||
		len(snapshot.Activity) > MaxOperatorActivityLimit || len(snapshot.ActivityCursor) > 4096 ||
		len(snapshot.Coverage) > 1024 || len(snapshot.ActivityRetention) > 256 ||
		len(snapshot.Risks) > 500 ||
		len(snapshot.Operations) > 500 || len(snapshot.Capabilities) > 256 ||
		len(snapshot.NextActions) > 64 {
		return errors.New("operator snapshot is invalid")
	}
	if err := snapshot.StreamHealth.Validate(); err != nil {
		return err
	}
	for _, projection := range snapshot.Profiles {
		if err := projection.Validate(); err != nil {
			return fmt.Errorf("operator snapshot profile projection: %w", err)
		}
	}
	for _, projection := range snapshot.Sessions {
		if err := projection.Validate(); err != nil {
			return err
		}
	}
	for _, projection := range snapshot.Environments {
		if err := projection.Validate(); err != nil {
			return err
		}
	}
	for _, record := range snapshot.Activity {
		if err := record.Validate(); err != nil {
			return err
		}
	}
	for _, interval := range snapshot.Coverage {
		if err := interval.Validate(); err != nil {
			return err
		}
	}
	for _, retention := range snapshot.ActivityRetention {
		if err := retention.Validate(); err != nil {
			return err
		}
	}
	if snapshot.ActivityStoreRetention != nil {
		if err := snapshot.ActivityStoreRetention.Validate(); err != nil {
			return err
		}
	}
	for _, finding := range snapshot.Risks {
		if err := finding.Validate(); err != nil {
			return err
		}
	}
	for _, operation := range snapshot.Operations {
		if err := operation.Validate(); err != nil {
			return err
		}
	}
	for _, capability := range snapshot.Capabilities {
		if err := capability.Validate(); err != nil {
			return err
		}
	}
	for _, action := range snapshot.NextActions {
		if !operatorCodePattern.MatchString(action) {
			return errors.New("operator snapshot next action is invalid")
		}
	}
	return nil
}

func cloneOperatorActivityStoreRetention(
	value *OperatorActivityStoreRetentionProjection,
) *OperatorActivityStoreRetentionProjection {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (health OperatorStreamHealth) Validate() error {
	switch health.State {
	case OperatorHealthSeeding, OperatorHealthLive, OperatorHealthIdleLive,
		OperatorHealthStale, OperatorHealthDisconnected,
		OperatorHealthCredentialExpired, OperatorHealthSchemaMismatch,
		OperatorHealthDaemonless:
	default:
		return errors.New("operator stream health is invalid")
	}
	if len(health.Reason) > 1024 || strings.IndexByte(health.Reason, 0) >= 0 {
		return errors.New("operator stream health reason is invalid")
	}
	return nil
}

func (projection OperatorSessionProjection) Validate() error {
	if !operatorSessionIDPattern.MatchString(projection.ID) ||
		(projection.EnvironmentID != "" && len(projection.EnvironmentID) > 128) ||
		len(projection.Profile) > 128 || len(projection.State) > 64 ||
		len(projection.Command) > 8192 ||
		strings.IndexByte(projection.Command, 0) >= 0 {
		return errors.New("operator session projection is invalid")
	}
	return nil
}

func (projection OperatorEnvironmentProjection) Validate() error {
	if !operatorEnvironmentIDPattern.MatchString(projection.ID) ||
		len(projection.Name) > 128 ||
		len(projection.Profile) > 128 ||
		len(projection.Backend) > 128 ||
		len(projection.Status) == 0 ||
		len(projection.Status) > 64 ||
		len(projection.Workspace) > 4096 ||
		len(projection.InstanceName) > 256 ||
		len(projection.LastSessionID) > 128 ||
		len(projection.LastCommand) > 8192 ||
		projection.ActiveSessions < 0 ||
		projection.ActiveWorkspaceViews < 0 ||
		len(projection.OwnerHealth) > 64 ||
		containsOperatorControl(
			projection.Name,
			projection.Profile,
			projection.Backend,
			projection.Status,
			projection.Workspace,
			projection.InstanceName,
			projection.LastSessionID,
			projection.LastCommand,
			projection.OwnerHealth,
		) {
		return errors.New(
			"operator environment projection is invalid",
		)
	}
	return nil
}

func containsOperatorControl(values ...string) bool {
	for _, value := range values {
		if strings.IndexByte(value, 0) >= 0 {
			return true
		}
	}
	return false
}

func (finding RiskFinding) Validate() error {
	if !operatorRiskIDPattern.MatchString(finding.ID) ||
		!operatorCodePattern.MatchString(finding.RuleID) ||
		len(finding.RuleVersion) == 0 || len(finding.RuleVersion) > 64 ||
		len(finding.Title) == 0 || len(finding.Title) > 512 ||
		len(finding.Explanation) > 2048 || len(finding.EvidenceRefs) > 256 ||
		(finding.NextAction != "" && !operatorCodePattern.MatchString(finding.NextAction)) {
		return errors.New("operator risk finding is invalid")
	}
	switch finding.Severity {
	case "info", "low", "medium", "high", "critical":
	default:
		return errors.New("operator risk severity is invalid")
	}
	switch finding.Confidence {
	case "exact", "inferred", "limited":
	default:
		return errors.New("operator risk confidence is invalid")
	}
	switch finding.PolicyStatus {
	case "", "allowed", "denied", "not-evaluated":
	default:
		return errors.New("operator risk policy status is invalid")
	}
	if !finding.FirstAt.IsZero() && !finding.LastAt.IsZero() && finding.LastAt.Before(finding.FirstAt) {
		return errors.New("operator risk interval is invalid")
	}
	return nil
}

func (capability OperatorCapabilityProjection) Validate() error {
	if !operatorCodePattern.MatchString(capability.ID) ||
		len(capability.Provider) > 128 || len(capability.Reason) > 1024 ||
		len(capability.ActionRefs) > 64 {
		return errors.New("operator capability projection is invalid")
	}
	switch capability.State {
	case OperatorCapabilityAvailable, OperatorCapabilityPartial, OperatorCapabilityUnavailable:
	default:
		return errors.New("operator capability state is invalid")
	}
	for _, action := range capability.ActionRefs {
		if !operatorCodePattern.MatchString(action) {
			return errors.New("operator capability action is invalid")
		}
	}
	return nil
}

func scopeOperatorSessions(values []SessionSummary, query OperatorSnapshotQuery) []OperatorSessionProjection {
	out := make([]OperatorSessionProjection, 0, len(values))
	for _, value := range values {
		if query.Profile != "" && value.Profile != query.Profile {
			continue
		}
		if query.Session != "" && value.ID != query.Session {
			continue
		}
		command := value.CommandClass
		if command == "" {
			command = "unknown"
		}
		out = append(out, OperatorSessionProjection{
			ID: value.ID, EnvironmentID: value.EnvironmentID, Profile: value.Profile,
			State: string(value.State), Command: command, StartedAt: value.StartedAt,
		})
	}
	return out
}

func scopeOperatorEnvironments(
	values []EnvironmentSummary,
	sessions []OperatorSessionProjection,
	query OperatorSnapshotQuery,
) []OperatorEnvironmentProjection {
	selectedEnvironment := ""
	if query.Session != "" {
		for _, value := range sessions {
			if value.ID == query.Session {
				selectedEnvironment = value.EnvironmentID
				break
			}
		}
		if selectedEnvironment == "" {
			return []OperatorEnvironmentProjection{}
		}
	}
	out := make([]OperatorEnvironmentProjection, 0, len(values))
	for _, value := range values {
		if query.Profile != "" && value.Profile != query.Profile {
			continue
		}
		if selectedEnvironment != "" && value.ID != selectedEnvironment {
			continue
		}
		projection := OperatorEnvironmentProjection{
			ID: value.ID, Name: value.Name, Profile: value.Profile,
			Backend: value.Backend, Status: value.Status,
			Workspace: value.Workspace, InstanceName: value.InstanceName,
			LastSessionID:        value.LastSessionID,
			LastCommand:          value.LastCommand,
			ActiveSessions:       value.ActiveSessions,
			ActiveWorkspaceViews: value.ActiveWorkspaceViews,
			OwnerHealth:          value.OwnerHealth, CreatedAt: value.CreatedAt,
		}
		if value.LastStartedAt != nil {
			projection.LastStartedAt = *value.LastStartedAt
		}
		if value.LastEndedAt != nil {
			projection.LastEndedAt = *value.LastEndedAt
		}
		out = append(out, projection)
	}
	return out
}

func operatorProfileNames(
	overview Overview,
	sessions []OperatorSessionProjection,
	query OperatorSnapshotQuery,
) []string {
	if query.Profile != "" {
		return []string{query.Profile}
	}
	names := make([]string, 0, len(overview.Profiles))
	if query.Session != "" {
		for _, value := range sessions {
			if value.Profile != "" {
				names = append(names, value.Profile)
			}
		}
	} else {
		for _, value := range overview.Profiles {
			if value.Name != "" {
				names = append(names, value.Name)
			}
		}
	}
	sort.Strings(names)
	return slices.Compact(names)
}

func scopeOperatorActivity(
	values []ActivityProjection,
	overview Overview,
	query OperatorSnapshotQuery,
) []ActivityProjection {
	if query.Session == "" && query.Profile == "" {
		return append(
			make([]ActivityProjection, 0, len(values)),
			values...,
		)
	}
	profiles := operatorSessionProfiles(overview)
	out := make([]ActivityProjection, 0, len(values))
	for _, value := range values {
		if query.Session != "" && value.SessionID != query.Session {
			continue
		}
		if query.Profile != "" && profiles[value.SessionID] != query.Profile {
			continue
		}
		out = append(out, value)
	}
	return out
}

func scopeOperatorCoverage(
	values []CoverageProjection,
	overview Overview,
	query OperatorSnapshotQuery,
) []CoverageProjection {
	if query.Session == "" && query.Profile == "" {
		return append(
			make([]CoverageProjection, 0, len(values)),
			values...,
		)
	}
	profiles := operatorSessionProfiles(overview)
	out := make([]CoverageProjection, 0, len(values))
	for _, value := range values {
		if query.Session != "" && value.SessionID != query.Session {
			continue
		}
		if query.Profile != "" && profiles[value.SessionID] != query.Profile {
			continue
		}
		out = append(out, value)
	}
	return out
}

func scopeOperatorActivityRetention(
	values []OperatorActivityRetentionProjection,
	overview Overview,
	query OperatorSnapshotQuery,
) []OperatorActivityRetentionProjection {
	if query.Session == "" && query.Profile == "" {
		return append(
			make([]OperatorActivityRetentionProjection, 0, len(values)),
			values...,
		)
	}
	sessionEnvironment := make(map[string]string, len(overview.Sessions))
	sessionProfile := make(map[string]string, len(overview.Sessions))
	for _, value := range overview.Sessions {
		sessionEnvironment[value.ID] = value.EnvironmentID
		sessionProfile[value.ID] = value.Profile
	}
	environmentProfile := make(map[string]string, len(overview.Environments))
	for _, value := range overview.Environments {
		environmentProfile[value.ID] = value.Profile
	}
	out := make([]OperatorActivityRetentionProjection, 0, len(values))
	for _, value := range values {
		sessionMatch := true
		if query.Session != "" {
			switch value.Owner.Kind {
			case workloadtypes.OwnerDisposableSession:
				sessionMatch = value.Owner.SessionID == query.Session
			case workloadtypes.OwnerReusableEnvironment:
				sessionMatch = value.Owner.EnvironmentID ==
					sessionEnvironment[query.Session]
			default:
				sessionMatch = false
			}
		}
		profileMatch := true
		if query.Profile != "" {
			switch value.Owner.Kind {
			case workloadtypes.OwnerDisposableSession:
				profileMatch = sessionProfile[value.Owner.SessionID] == query.Profile
			case workloadtypes.OwnerReusableEnvironment:
				profileMatch = environmentProfile[value.Owner.EnvironmentID] ==
					query.Profile
			default:
				profileMatch = false
			}
		}
		if sessionMatch && profileMatch {
			value.Reasons = append([]string(nil), value.Reasons...)
			out = append(out, value)
		}
	}
	return out
}

func operatorSessionProfiles(overview Overview) map[string]string {
	out := make(map[string]string, len(overview.Sessions))
	for _, value := range overview.Sessions {
		out[value.ID] = value.Profile
	}
	return out
}

func scopeOperatorOperations(
	values []Operation,
	overview Overview,
	query OperatorSnapshotQuery,
) []Operation {
	if query.Profile == "" && query.Session == "" {
		return append(make([]Operation, 0, len(values)), values...)
	}
	sessionEnvironment := make(map[string]string, len(overview.Sessions))
	sessionProfile := make(map[string]string, len(overview.Sessions))
	for _, value := range overview.Sessions {
		sessionEnvironment[value.ID] = value.EnvironmentID
		sessionProfile[value.ID] = value.Profile
	}
	environmentProfile := make(map[string]string, len(overview.Environments))
	for _, value := range overview.Environments {
		environmentProfile[value.ID] = value.Profile
	}
	out := make([]Operation, 0, len(values))
	for _, value := range values {
		match := false
		if query.Session != "" {
			switch value.Owner.Kind {
			case "session":
				match = value.Owner.ID == query.Session
			case "environment":
				match = value.Owner.ID == sessionEnvironment[query.Session]
			case "profile":
				match = value.Owner.ID == sessionProfile[query.Session]
			}
		} else {
			switch value.Owner.Kind {
			case "profile":
				match = value.Owner.ID == query.Profile
			case "session":
				match = sessionProfile[value.Owner.ID] == query.Profile
			case "environment":
				match = environmentProfile[value.Owner.ID] == query.Profile
			}
		}
		if match {
			out = append(out, value)
		}
	}
	return out
}

func unavailableObservationCapabilities(reason string) []OperatorCapabilityProjection {
	out := make([]OperatorCapabilityProjection, 0, 4)
	for _, id := range []string{"activity.process", "activity.file", "activity.network", "activity.dns"} {
		out = append(out, OperatorCapabilityProjection{
			ID: id, State: OperatorCapabilityUnavailable, Provider: "none",
			Reason: reason, ActionRefs: []string{"doctor.activity"},
		})
	}
	return out
}

func fillObservationCapabilities(values []OperatorCapabilityProjection) []OperatorCapabilityProjection {
	byID := make(map[string]OperatorCapabilityProjection, len(values))
	for _, value := range values {
		byID[value.ID] = value
	}
	for _, fallback := range unavailableObservationCapabilities("subsystem capability was not reported") {
		if _, exists := byID[fallback.ID]; !exists {
			byID[fallback.ID] = fallback
		}
	}
	out := make([]OperatorCapabilityProjection, 0, len(byID))
	for _, value := range byID {
		out = append(out, value)
	}
	return out
}

func normalizeOperatorCapabilities(values []OperatorCapabilityProjection) []OperatorCapabilityProjection {
	byID := make(map[string]OperatorCapabilityProjection, len(values))
	for _, value := range values {
		if current, exists := byID[value.ID]; !exists ||
			operatorCapabilityRank(value.State) < operatorCapabilityRank(current.State) {
			value.ActionRefs = uniqueOperatorCodes(value.ActionRefs)
			byID[value.ID] = value
		}
	}
	out := make([]OperatorCapabilityProjection, 0, len(byID))
	for _, value := range byID {
		out = append(out, value)
	}
	sort.Slice(out, func(left, right int) bool { return out[left].ID < out[right].ID })
	return out
}

func operatorCapabilityRank(state string) int {
	switch state {
	case OperatorCapabilityUnavailable:
		return 0
	case OperatorCapabilityPartial:
		return 1
	default:
		return 2
	}
}

func sortOperatorSnapshot(snapshot *OperatorSnapshot) {
	sort.Slice(snapshot.Profiles, func(left, right int) bool {
		return snapshot.Profiles[left].Profile < snapshot.Profiles[right].Profile
	})
	sort.Slice(snapshot.Sessions, func(left, right int) bool {
		return snapshot.Sessions[left].ID < snapshot.Sessions[right].ID
	})
	sort.Slice(snapshot.Environments, func(left, right int) bool {
		return snapshot.Environments[left].ID <
			snapshot.Environments[right].ID
	})
	sort.Slice(snapshot.Activity, func(left, right int) bool {
		if snapshot.Activity[left].LastAt.Equal(snapshot.Activity[right].LastAt) {
			return snapshot.Activity[left].ID < snapshot.Activity[right].ID
		}
		return snapshot.Activity[left].LastAt.After(snapshot.Activity[right].LastAt)
	})
	sort.Slice(snapshot.Coverage, func(left, right int) bool {
		if snapshot.Coverage[left].StartedAt.Equal(snapshot.Coverage[right].StartedAt) {
			return snapshot.Coverage[left].ID < snapshot.Coverage[right].ID
		}
		return snapshot.Coverage[left].StartedAt.After(snapshot.Coverage[right].StartedAt)
	})
	sort.Slice(snapshot.ActivityRetention, func(left, right int) bool {
		return snapshot.ActivityRetention[left].Owner.Key() <
			snapshot.ActivityRetention[right].Owner.Key()
	})
	sort.Slice(snapshot.Risks, func(left, right int) bool {
		leftRank := operatorRiskRank(snapshot.Risks[left].Severity)
		rightRank := operatorRiskRank(snapshot.Risks[right].Severity)
		if leftRank != rightRank {
			return leftRank > rightRank
		}
		if !snapshot.Risks[left].LastAt.Equal(snapshot.Risks[right].LastAt) {
			return snapshot.Risks[left].LastAt.After(snapshot.Risks[right].LastAt)
		}
		return snapshot.Risks[left].ID < snapshot.Risks[right].ID
	})
}

func operatorRiskRank(severity string) int {
	switch severity {
	case "critical":
		return 5
	case "high":
		return 4
	case "medium":
		return 3
	case "low":
		return 2
	default:
		return 1
	}
}

func operatorNextActions(snapshot OperatorSnapshot) []string {
	actions := make([]string, 0, 8)
	switch snapshot.StreamHealth.State {
	case OperatorHealthStale, OperatorHealthDisconnected,
		OperatorHealthCredentialExpired, OperatorHealthSchemaMismatch:
		actions = append(actions, "snapshot.refresh")
	}
	if len(snapshot.Sessions) == 0 {
		actions = append(actions, "run.start")
	} else {
		actions = append(actions, "activity.inspect")
	}
	if len(snapshot.Risks) > 0 && snapshot.Risks[0].NextAction != "" {
		actions = append(actions, snapshot.Risks[0].NextAction)
	}
	for _, capability := range snapshot.Capabilities {
		if capability.State == OperatorCapabilityAvailable {
			continue
		}
		actions = append(actions, capability.ActionRefs...)
	}
	return uniqueOperatorCodes(actions)
}

func uniqueOperatorCodes(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !operatorCodePattern.MatchString(value) {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
