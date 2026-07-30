package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
)

const (
	liveNetworkTransitionJournalSchema   = "hideout.live-network-transition-journal/v1"
	liveNetworkTransitionJournalMaxBytes = 64 << 10

	liveNetworkJournalStaged     = "staged"
	liveNetworkJournalProbed     = "probed"
	liveNetworkJournalActivated  = "activated"
	liveNetworkJournalProved     = "proved"
	liveNetworkJournalDrained    = "drained"
	liveNetworkJournalCommitted  = "committed"
	liveNetworkJournalRolledBack = "rolled-back"
)

// LiveNetworkTransitionProvider combines the daemon gateway's atomic route
// pointer with a restricted, session-owned DNS runtime. Route transitions keep
// using GatewayNetworkTransitionProvider; DNS transitions never receive a
// general backend or guest shell capability.
type LiveNetworkTransitionProvider struct {
	Gateway  GatewayNetworkTransitionProvider
	Runtimes EnvironmentNetworkRuntimeProvider
	Now      func() time.Time
}

func (provider LiveNetworkTransitionProvider) ObserveNetworkRoute(
	ctx context.Context,
	environmentID string,
) (NetworkRouteConfiguration, error) {
	return provider.Gateway.ObserveNetworkRoute(ctx, environmentID)
}

func (provider LiveNetworkTransitionProvider) SupportsNetworkTransitionKind(
	kind string,
) bool {
	switch kind {
	case NetworkTransitionRoute:
		return true
	case NetworkTransitionDNS:
		return provider.Runtimes != nil
	default:
		return false
	}
}

func (provider LiveNetworkTransitionProvider) NetworkTransitionAvailable(
	ctx context.Context,
	plan NetworkTransitionPlan,
) error {
	if plan.VerifyDigest() != nil ||
		!provider.SupportsNetworkTransitionKind(plan.Kind) {
		return ErrNetworkTransitionProviderUnavailable
	}
	if plan.Kind == NetworkTransitionDNS {
		return provider.Runtimes.EnvironmentNetworkRuntimeAvailable(
			ctx,
			plan.EnvironmentID,
		)
	}
	return nil
}

func (provider LiveNetworkTransitionProvider) StageNetworkCandidate(
	ctx context.Context,
	plan NetworkTransitionPlan,
	material NetworkCandidateMaterial,
) (NetworkStagedCandidate, NetworkTransitionProof, error) {
	switch plan.Kind {
	case NetworkTransitionRoute:
		return provider.Gateway.StageNetworkCandidate(
			ctx,
			plan,
			material,
		)
	case NetworkTransitionDNS:
		return provider.stageDNSCandidate(ctx, plan)
	default:
		return nil, NetworkTransitionProof{},
			ErrNetworkTransitionProviderUnavailable
	}
}

func (provider LiveNetworkTransitionProvider) CommitNetworkCandidates(
	ctx context.Context,
	handles []NetworkStagedCandidate,
) error {
	if len(handles) == 1 {
		if candidate, ok := handles[0].(*liveDNSNetworkCandidate); ok {
			return candidate.CommitNetworkCandidate(ctx)
		}
	}
	for _, handle := range handles {
		if _, ok := handle.(*liveDNSNetworkCandidate); ok {
			return ErrNetworkTransitionProviderUnavailable
		}
	}
	return provider.Gateway.CommitNetworkCandidates(ctx, handles)
}

func (provider LiveNetworkTransitionProvider) RollbackNetworkCandidates(
	ctx context.Context,
	handles []NetworkStagedCandidate,
) ([]NetworkTransitionProof, error) {
	if len(handles) == 1 {
		if candidate, ok := handles[0].(*liveDNSNetworkCandidate); ok {
			proof, err := candidate.RollbackNetworkCandidate(ctx)
			return []NetworkTransitionProof{proof}, err
		}
	}
	for _, handle := range handles {
		if _, ok := handle.(*liveDNSNetworkCandidate); ok {
			return nil, ErrNetworkTransitionProviderUnavailable
		}
	}
	return provider.Gateway.RollbackNetworkCandidates(ctx, handles)
}

func (provider LiveNetworkTransitionProvider) stageDNSCandidate(
	ctx context.Context,
	plan NetworkTransitionPlan,
) (NetworkStagedCandidate, NetworkTransitionProof, error) {
	if provider.Runtimes == nil ||
		plan.Kind != NetworkTransitionDNS ||
		plan.From.Mode != netpolicy.ModeTun2Socks ||
		plan.Desired.Mode != netpolicy.ModeTun2Socks ||
		plan.From.ProxySecretRef != plan.Desired.ProxySecretRef ||
		plan.From.ProxySecretGeneration !=
			plan.Desired.ProxySecretGeneration ||
		plan.From.MediatedResolver ==
			plan.Desired.MediatedResolver {
		return nil, NetworkTransitionProof{},
			ErrNetworkTransitionProviderUnavailable
	}
	runtime, err := provider.Runtimes.
		AcquireEnvironmentNetworkRuntime(ctx, plan.EnvironmentID)
	if err != nil {
		return nil, NetworkTransitionProof{}, err
	}
	candidate := &liveDNSNetworkCandidate{
		provider: provider,
		plan:     plan,
		runtime:  runtime,
	}
	serviceDir := (environment.Store{
		Root: provider.Gateway.StoreRoot,
	}).RuntimeNetworkServiceDir(plan.EnvironmentID)
	candidate.statePath = filepath.Join(serviceDir, "state.json")
	candidate.journalPath = filepath.Join(
		serviceDir,
		"transition.json",
	)
	proof, err := candidate.stage(ctx)
	return candidate, proof, err
}

func (provider LiveNetworkTransitionProvider) nowUTC(
	startedAt time.Time,
) time.Time {
	var now time.Time
	if provider.Now != nil {
		now = provider.Now().Round(0).UTC()
	} else {
		now = time.Now().Round(0).UTC()
	}
	if now.Before(startedAt) {
		return startedAt
	}
	return now
}

type liveDNSNetworkCandidate struct {
	mu sync.Mutex

	provider    LiveNetworkTransitionProvider
	plan        NetworkTransitionPlan
	runtime     EnvironmentNetworkRuntimeLease
	statePath   string
	journalPath string
	previous    netpolicy.ServiceState
	journal     liveNetworkTransitionJournal

	staged            bool
	mutationAttempted bool
	activated         bool
	rollbackProved    bool
	proved            bool
	terminal          bool
}

func (candidate *liveDNSNetworkCandidate) stage(
	ctx context.Context,
) (NetworkTransitionProof, error) {
	candidate.mu.Lock()
	defer candidate.mu.Unlock()
	if candidate.runtime == nil ||
		candidate.runtime.EnvironmentID() !=
			candidate.plan.EnvironmentID {
		return NetworkTransitionProof{},
			ErrEnvironmentNetworkRuntimeUnavailable
	}
	state, err := netpolicy.LoadServiceState(candidate.statePath)
	if err != nil {
		return NetworkTransitionProof{}, err
	}
	if state.EnvironmentID != candidate.plan.EnvironmentID ||
		state.Status != netpolicy.ServiceReady ||
		state.Mode != netpolicy.ModeTun2Socks ||
		state.Resolver != candidate.plan.From.MediatedResolver ||
		state.BootID != candidate.runtime.BootID() {
		return NetworkTransitionProof{}, ErrNetworkTransitionStale
	}
	if existing, loadErr := loadLiveNetworkTransitionJournal(
		candidate.journalPath,
	); loadErr == nil {
		if existing.Phase != liveNetworkJournalCommitted &&
			existing.Phase != liveNetworkJournalRolledBack {
			return NetworkTransitionProof{},
				ErrNetworkTransitionRecoveryRequired
		}
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return NetworkTransitionProof{}, loadErr
	}
	proof, err := candidate.routeProof(
		candidate.plan.From.MediatedResolver,
	)
	if err != nil {
		return NetworkTransitionProof{}, err
	}
	if err := ctx.Err(); err != nil {
		return NetworkTransitionProof{}, err
	}
	candidate.previous = state
	candidate.journal = liveNetworkTransitionJournal{
		Schema:                   liveNetworkTransitionJournalSchema,
		PlanDigest:               candidate.plan.PlanDigest,
		EnvironmentID:            candidate.plan.EnvironmentID,
		Kind:                     candidate.plan.Kind,
		From:                     candidate.plan.From,
		Desired:                  candidate.plan.Desired,
		CandidateRouteGeneration: proof.RouteGeneration,
		RuntimeBootID:            candidate.runtime.BootID(),
		PreviousState:            state,
		Phase:                    liveNetworkJournalStaged,
		CompletedEffects:         1,
		UpdatedAt: candidate.provider.nowUTC(
			state.StartedAt,
		),
	}
	candidate.staged = true
	if err := writeLiveNetworkTransitionJournal(
		candidate.journalPath,
		candidate.journal,
	); err != nil {
		return NetworkTransitionProof{}, err
	}
	return proof, nil
}

func (candidate *liveDNSNetworkCandidate) ProbeNetworkCandidate(
	ctx context.Context,
) (NetworkTransitionProof, error) {
	candidate.mu.Lock()
	defer candidate.mu.Unlock()
	if !candidate.staged || candidate.terminal ||
		candidate.mutationAttempted {
		return NetworkTransitionProof{}, ErrInvalidNetworkTransition
	}
	if err := candidate.runtime.VerifyDNS(
		ctx,
		candidate.plan.From.MediatedResolver,
	); err != nil {
		return NetworkTransitionProof{}, err
	}
	if err := candidate.writeJournalPhase(
		liveNetworkJournalProbed,
	); err != nil {
		return NetworkTransitionProof{}, err
	}
	return candidate.routeProof(
		candidate.plan.From.MediatedResolver,
	)
}

func (candidate *liveDNSNetworkCandidate) ActivateNetworkCandidate(
	ctx context.Context,
) (NetworkTransitionProof, error) {
	candidate.mu.Lock()
	defer candidate.mu.Unlock()
	if !candidate.staged || candidate.terminal ||
		candidate.mutationAttempted {
		return NetworkTransitionProof{}, ErrInvalidNetworkTransition
	}
	switching := candidate.previous
	switching.Status = netpolicy.ServiceSwitching
	switching.UpdatedAt = candidate.provider.nowUTC(
		switching.StartedAt,
	)
	switching.LastError = ""
	if err := netpolicy.WriteServiceState(
		candidate.statePath,
		switching,
	); err != nil {
		return NetworkTransitionProof{}, err
	}
	candidate.mutationAttempted = true
	err := candidate.runtime.ReconfigureDNS(
		ctx,
		candidate.plan.From.MediatedResolver,
		candidate.plan.Desired.MediatedResolver,
	)
	if err != nil {
		var reconfigureErr backend.EnvironmentServiceReconfigureError
		if errors.As(err, &reconfigureErr) &&
			reconfigureErr.RollbackProved {
			restoreErr := netpolicy.WriteServiceState(
				candidate.statePath,
				candidate.previous,
			)
			candidate.rollbackProved = restoreErr == nil
			return NetworkTransitionProof{},
				errors.Join(err, restoreErr)
		}
		failed := switching
		failed.Status = netpolicy.ServiceFailed
		failed.UpdatedAt = candidate.provider.nowUTC(
			failed.StartedAt,
		)
		_ = netpolicy.WriteServiceState(candidate.statePath, failed)
		return NetworkTransitionProof{}, err
	}
	candidate.activated = true
	if err := candidate.writeJournalPhase(
		liveNetworkJournalActivated,
	); err != nil {
		return NetworkTransitionProof{}, err
	}
	return candidate.routeProof(
		candidate.plan.Desired.MediatedResolver,
	)
}

func (candidate *liveDNSNetworkCandidate) ProveNetworkCandidate(
	ctx context.Context,
) (NetworkTransitionProof, error) {
	candidate.mu.Lock()
	defer candidate.mu.Unlock()
	if !candidate.activated || candidate.terminal {
		return NetworkTransitionProof{}, ErrInvalidNetworkTransition
	}
	if err := candidate.runtime.VerifyDNS(
		ctx,
		candidate.plan.Desired.MediatedResolver,
	); err != nil {
		return NetworkTransitionProof{}, err
	}
	proof, err := candidate.routeProof(
		candidate.plan.Desired.MediatedResolver,
	)
	if err == nil {
		err = candidate.writeJournalPhase(
			liveNetworkJournalProved,
		)
	}
	if err == nil {
		candidate.proved = true
	}
	return proof, err
}

func (candidate *liveDNSNetworkCandidate) DrainPreviousConnections(
	_ context.Context,
) (NetworkTransitionProof, error) {
	candidate.mu.Lock()
	defer candidate.mu.Unlock()
	if !candidate.proved || candidate.terminal {
		return NetworkTransitionProof{}, ErrInvalidNetworkTransition
	}
	if err := candidate.writeJournalPhase(
		liveNetworkJournalDrained,
	); err != nil {
		return NetworkTransitionProof{}, err
	}
	return candidate.routeProof(
		candidate.plan.Desired.MediatedResolver,
	)
}

func (candidate *liveDNSNetworkCandidate) CommitNetworkCandidate(
	_ context.Context,
) error {
	candidate.mu.Lock()
	defer candidate.mu.Unlock()
	if !candidate.proved || candidate.terminal {
		return ErrInvalidNetworkTransition
	}
	ready := candidate.previous
	ready.Status = netpolicy.ServiceReady
	ready.Resolver = candidate.plan.Desired.MediatedResolver
	ready.UpdatedAt = candidate.provider.nowUTC(ready.StartedAt)
	ready.LastError = ""
	if err := netpolicy.WriteServiceState(
		candidate.statePath,
		ready,
	); err != nil {
		return err
	}
	if err := candidate.writeJournalPhase(
		liveNetworkJournalCommitted,
	); err != nil {
		return err
	}
	candidate.terminal = true
	candidate.runtime.Release()
	return nil
}

func (candidate *liveDNSNetworkCandidate) RollbackNetworkCandidate(
	ctx context.Context,
) (proof NetworkTransitionProof, retErr error) {
	candidate.mu.Lock()
	defer candidate.mu.Unlock()
	if candidate.runtime == nil {
		return NetworkTransitionProof{},
			ErrEnvironmentNetworkRuntimeUnavailable
	}
	defer candidate.runtime.Release()
	if candidate.terminal {
		return NetworkTransitionProof{}, ErrInvalidNetworkTransition
	}
	candidate.terminal = true
	if !candidate.staged {
		return candidate.routeProof(
			candidate.plan.From.MediatedResolver,
		)
	}
	if candidate.mutationAttempted &&
		!candidate.activated &&
		!candidate.rollbackProved {
		return NetworkTransitionProof{},
			ErrNetworkTransitionRollbackUnproved
	}
	if candidate.activated {
		if err := candidate.runtime.ReconfigureDNS(
			ctx,
			candidate.plan.Desired.MediatedResolver,
			candidate.plan.From.MediatedResolver,
		); err != nil {
			return NetworkTransitionProof{}, err
		}
	}
	if err := candidate.runtime.VerifyDNS(
		ctx,
		candidate.plan.From.MediatedResolver,
	); err != nil {
		return NetworkTransitionProof{}, err
	}
	if err := netpolicy.WriteServiceState(
		candidate.statePath,
		candidate.previous,
	); err != nil {
		return NetworkTransitionProof{}, err
	}
	if err := candidate.writeJournalPhase(
		liveNetworkJournalRolledBack,
	); err != nil {
		return NetworkTransitionProof{}, err
	}
	return candidate.routeProof(
		candidate.plan.From.MediatedResolver,
	)
}

func (candidate *liveDNSNetworkCandidate) routeProof(
	resolver string,
) (
	NetworkTransitionProof,
	error,
) {
	if candidate.provider.Gateway.Gateways == nil {
		return NetworkTransitionProof{},
			ErrNetworkTransitionProviderUnavailable
	}
	observation, ok :=
		candidate.provider.Gateway.Gateways.RouteObservation(
			candidate.plan.EnvironmentID,
		)
	if !ok || !observation.ActiveAvailable ||
		observation.Active.Mode != netpolicy.ModeTun2Socks ||
		observation.Active.ProxySecretRef !=
			candidate.plan.Desired.ProxySecretRef ||
		observation.Active.SecretGeneration !=
			candidate.plan.Desired.ProxySecretGeneration {
		return NetworkTransitionProof{},
			ErrNetworkTransitionStale
	}
	proof := proofForGatewayObservation(
		observation,
		observation.Active,
		candidate.provider.nowUTC(candidate.previous.StartedAt),
	)
	proof.MediatedResolver = resolver
	proof.RuntimeBootID = candidate.runtime.BootID()
	return proof, nil
}

func (candidate *liveDNSNetworkCandidate) writeJournalPhase(
	phase string,
) error {
	candidate.journal.Phase = phase
	switch phase {
	case liveNetworkJournalStaged:
		candidate.journal.CompletedEffects = 1
	case liveNetworkJournalProbed:
		candidate.journal.CompletedEffects = 2
	case liveNetworkJournalActivated:
		candidate.journal.CompletedEffects = 3
	case liveNetworkJournalProved:
		candidate.journal.CompletedEffects = 4
	case liveNetworkJournalDrained,
		liveNetworkJournalCommitted:
		candidate.journal.CompletedEffects = 5
	case liveNetworkJournalRolledBack:
		// Preserve the durable prefix that was actually entered before
		// restoration; pending effects were never executed.
	default:
		return ErrInvalidNetworkTransition
	}
	candidate.journal.UpdatedAt = candidate.provider.nowUTC(
		candidate.previous.StartedAt,
	)
	return writeLiveNetworkTransitionJournal(
		candidate.journalPath,
		candidate.journal,
	)
}

type liveNetworkTransitionJournal struct {
	Schema                   string                    `json:"schema"`
	PlanDigest               string                    `json:"planDigest"`
	EnvironmentID            string                    `json:"environmentId"`
	Kind                     string                    `json:"kind"`
	From                     NetworkRouteConfiguration `json:"from"`
	Desired                  NetworkRouteConfiguration `json:"desired"`
	CandidateRouteGeneration uint64                    `json:"candidateRouteGeneration"`
	RuntimeBootID            string                    `json:"runtimeBootId"`
	PreviousState            netpolicy.ServiceState    `json:"previousState"`
	Phase                    string                    `json:"phase"`
	CompletedEffects         int                       `json:"completedEffects"`
	UpdatedAt                time.Time                 `json:"updatedAt"`
}

func (journal liveNetworkTransitionJournal) Validate() error {
	if journal.Schema != liveNetworkTransitionJournalSchema ||
		!profileDigestPattern.MatchString(journal.PlanDigest) ||
		!networkTransitionEnvironmentPattern.MatchString(
			journal.EnvironmentID,
		) ||
		journal.Kind != NetworkTransitionDNS ||
		journal.From.Validate() != nil ||
		journal.Desired.Validate() != nil ||
		journal.CandidateRouteGeneration == 0 ||
		!environmentNetworkBootPattern.MatchString(
			journal.RuntimeBootID,
		) ||
		journal.PreviousState.Validate() != nil ||
		journal.PreviousState.EnvironmentID !=
			journal.EnvironmentID ||
		journal.PreviousState.Status != netpolicy.ServiceReady ||
		journal.PreviousState.Mode != netpolicy.ModeTun2Socks ||
		journal.PreviousState.Resolver !=
			journal.From.MediatedResolver ||
		journal.PreviousState.BootID != journal.RuntimeBootID ||
		journal.CompletedEffects < 1 ||
		journal.CompletedEffects > 5 ||
		journal.UpdatedAt.IsZero() {
		return ErrInvalidNetworkTransition
	}
	if kind, err := classifyNetworkTransition(
		journal.From,
		journal.Desired,
	); err != nil || kind != journal.Kind {
		return ErrInvalidNetworkTransition
	}
	switch journal.Phase {
	case liveNetworkJournalStaged:
		if journal.CompletedEffects != 1 {
			return ErrInvalidNetworkTransition
		}
	case liveNetworkJournalProbed:
		if journal.CompletedEffects != 2 {
			return ErrInvalidNetworkTransition
		}
	case liveNetworkJournalActivated:
		if journal.CompletedEffects != 3 {
			return ErrInvalidNetworkTransition
		}
	case liveNetworkJournalProved:
		if journal.CompletedEffects != 4 {
			return ErrInvalidNetworkTransition
		}
	case liveNetworkJournalDrained,
		liveNetworkJournalCommitted:
		if journal.CompletedEffects != 5 {
			return ErrInvalidNetworkTransition
		}
	case liveNetworkJournalRolledBack:
	default:
		return ErrInvalidNetworkTransition
	}
	return nil
}

func writeLiveNetworkTransitionJournal(
	path string,
	journal liveNetworkTransitionJournal,
) error {
	if journal.Validate() != nil || filepath.Base(path) !=
		"transition.json" {
		return ErrInvalidNetworkTransition
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > liveNetworkTransitionJournalMaxBytes {
		return ErrInvalidNetworkTransition
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(
		dir,
		".network-transition-*",
	)
	if err != nil {
		return err
	}
	tmp := file.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(tmp)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	remove = false
	return syncOperationDirectory(dir)
}

func loadLiveNetworkTransitionJournal(
	path string,
) (liveNetworkTransitionJournal, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return liveNetworkTransitionJournal{}, err
	}
	if !info.Mode().IsRegular() ||
		info.Mode().Perm() != 0o600 ||
		info.Size() <= 0 ||
		info.Size() > liveNetworkTransitionJournalMaxBytes {
		return liveNetworkTransitionJournal{},
			ErrInvalidNetworkTransition
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return liveNetworkTransitionJournal{}, err
	}
	var journal liveNetworkTransitionJournal
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return liveNetworkTransitionJournal{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return liveNetworkTransitionJournal{},
			ErrInvalidNetworkTransition
	}
	if err := journal.Validate(); err != nil {
		return liveNetworkTransitionJournal{}, err
	}
	return journal, nil
}
