package manager

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/adapterpack"
	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/cmdadapter"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/hostcap/appopen"
	"github.com/vibe-agi/hideout/internal/hostfs"
	hostfsoverlay "github.com/vibe-agi/hideout/internal/hostfs/overlay"
	"github.com/vibe-agi/hideout/internal/hostfs/readgrant"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

type RunDataPlaneOptions struct {
	HostFSRun                  hostfs.Config
	DisableProfileHostFSGrants bool
	OperatorHome               string
	Backend                    backend.Backend
	Opener                     broker.Opener
	PortBridges                []RunPortBridgeRequest
	OpenTargets                []RunOpenTargetOwner
	EndpointCandidates         []RunEndpointCandidate
	EndpointExposures          []RunEndpointExposureRequest
	RequireSessionSupervisor   bool
	Lifecycle                  lifecycle.Registration
}

type RunDataPlane struct {
	Registry             cmdproxy.Registry
	ProjectionReadiness  *backend.ProjectionReadinessExpectation
	ObserverHelperDigest string
	HostFSPolicy         hostfs.EffectivePolicy
	HostFSEnabled        bool
	HostFSGrafts         []string
	PortBridges          []backend.PortBridgeEndpoint
	BrokerListenEndpoint broker.Endpoint
	BrokerGuestEndpoint  broker.Endpoint
	Env                  []string
	Broker               *broker.Server `json:"-"`
	portBridgeLeases     []runPortBridgeLease
	audit                *audit.Writer
	auditSession         string
	auditProfile         string
	auditBackend         string
	cancel               context.CancelFunc
	hostFSReadOwner      *readgrant.Owner
	projectionGrantIDs   []string
	lifecycle            lifecycle.Registration
	lifecycleRefs        []lifecycle.ResourceRef
}

func (c Core) StartRunDataPlane(ctx context.Context, runSession RunSession, runNetwork RunNetwork, opts RunDataPlaneOptions) (result RunDataPlane, retErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := hostcap.EnsureExternalUnmanagedHandoff(hostcap.CapabilityAppOpenResource); err != nil {
		return RunDataPlane{}, fmt.Errorf("host-app lifecycle invariant: %w", err)
	}
	workspaceAuthority, err := workspaceAuthorityForDataPlane(runSession)
	if err != nil {
		return RunDataPlane{}, fmt.Errorf("resolve run data-plane workspace authority: %w", err)
	}
	aw := runSession.Audit
	if aw == nil {
		aw = audit.NewDiscard()
	}
	lifecycleEffects := newRunLifecycleEffects(opts.Lifecycle, runSession.Layout.ID)
	lifecycleTracker := newRunLifecycleTracker(opts.Lifecycle)
	lifecycleTransferred := false
	var lifecycleStartupCleanupErr error
	defer func() {
		if !lifecycleTransferred {
			retErr = errors.Join(retErr, lifecycleTracker.rollback(lifecycleStartupCleanupErr))
		}
	}()
	portBridges, err := resolveRunEndpointExposures(runSession, opts.OpenTargets, opts.EndpointCandidates, opts.EndpointExposures)
	if err != nil {
		_ = emitEndpointExposureDeny(aw, runSession, opts.EndpointExposures, err)
		return RunDataPlane{}, err
	}
	portBridges = append(portBridges, opts.PortBridges...)
	if err := validateRunPortBridgeRequests(runSession, portBridges, aw); err != nil {
		return RunDataPlane{}, err
	}
	token, err := broker.NewToken()
	if err != nil {
		return RunDataPlane{}, err
	}
	registry, err := cmdproxy.RegistryFromProfile(runSession.Plan.RuntimeProfile)
	if err != nil {
		return RunDataPlane{}, err
	}
	hostFSPolicy, err := hostfs.Build(hostfs.BuildInput{
		Profile:   HostFSProfileForRun(runSession.Plan.RuntimeProfile, opts.DisableProfileHostFSGrants),
		Run:       opts.HostFSRun,
		StoreRoot: c.Store.Root,
		Home:      opts.OperatorHome,
	})
	if err != nil {
		return RunDataPlane{}, err
	}
	hostAppForbiddenRoots, err := c.hostAppRunForbiddenRoots(runSession, workspaceAuthority, hostFSPolicy)
	if err != nil {
		return RunDataPlane{}, err
	}
	hostAppBindings, hostAppRegistrations, err := c.CompileHostAppCatalog(runSession.Plan.ProfileName, runSession.Layout.ID, hostAppForbiddenRoots)
	if err != nil {
		return RunDataPlane{}, err
	}
	hostAppLifecycle, err := c.hostAppBindingLifecycleValidator(runSession.Plan.ProfileName)
	if err != nil {
		return RunDataPlane{}, err
	}
	registry, err = cmdproxy.WithProjection(registry, hostAppRegistrations...)
	if err != nil {
		return RunDataPlane{}, err
	}
	projectionCatalogDigest, err := BuildProjectionCatalogReviewDigest(registry)
	if err != nil {
		return RunDataPlane{}, err
	}
	if runSession.Plan.ProjectionCatalogDigest == "" ||
		projectionCatalogDigest != runSession.Plan.ProjectionCatalogDigest {
		return RunDataPlane{}, ErrRunPlanStale
	}
	if err := MaterializeCommandProxyShims(runSession.RuntimeShimDir, runSession.Plan.Backend, registry, runNetwork.Plan); err != nil {
		return RunDataPlane{}, err
	}
	if err := MaterializeSessionSupervisor(runSession.RuntimeShimDir, runSession.Plan.Backend, opts.RequireSessionSupervisor); err != nil {
		return RunDataPlane{}, err
	}
	observerHelperDigest, err := MaterializeObserver(
		runSession.RuntimeShimDir,
		runSession.Plan.Backend,
		opts.RequireSessionSupervisor,
	)
	if err != nil {
		return RunDataPlane{}, err
	}
	if err := MaterializeWorkspacePortal(
		runSession.RuntimeShimDir,
		runSession.Plan.Backend,
		runSession.WorkspaceAttachment.Transport == workspaceattach.SelectedTransport,
	); err != nil {
		return RunDataPlane{}, err
	}
	var projectionReadiness *backend.ProjectionReadinessExpectation
	if runSession.Plan.Backend == "lima" && runSession.Environment.Active {
		environmentID := runSession.Environment.Record.ID
		if environmentID == "" {
			return RunDataPlane{}, errors.New("Lima projection readiness requires an environment identity")
		}
		projectionReadiness, err = MaterializeProjectionReadiness(
			runSession.RuntimeSessionDir,
			runSession.Layout.ID,
			environmentID,
			runSession.SessionSnapshotID,
			runSession.Plan.Command,
			registry,
		)
		if err != nil {
			return RunDataPlane{}, err
		}
	}
	grantBindings, requiredGrantBindings := compileRunProjectionGrants(runSession, workspaceAuthority, hostAppBindings.Bindings())
	projectionGrantIDs := []string{}
	projectionGrantTransferred := false
	defer func() {
		if !projectionGrantTransferred {
			for _, decisionID := range projectionGrantIDs {
				_ = c.invalidateProjectionGrant(decisionID, "run-start-failed")
			}
		}
	}()
	for _, grantBinding := range requiredGrantBindings {
		grantDecision, err := c.ensureRunProjectionDecision(grantBinding)
		if err != nil {
			return RunDataPlane{}, err
		}
		projectionGrantIDs = append(projectionGrantIDs, grantDecision.ID)
	}
	// Keep safe host-app state in a compact store-owned root so session cleanup
	// cannot delete it while the detached GUI is alive and GUI-local Unix sockets
	// retain enough path budget on Darwin. Session IDs are store-global and the
	// qualified state root also binds the exact app, so every run remains isolated.
	safeUserDataDir := filepath.Join(c.Store.Root, "ha")
	if err := prepareProjectionSafeDataDir(safeUserDataDir); err != nil {
		return RunDataPlane{}, err
	}
	hostAppProjection := &hostcap.ProjectionConfig{
		Platform:                  hostcap.CurrentPlatform(),
		SafeUserDataDir:           safeUserDataDir,
		Grants:                    runProjectionGrantChecker{storeRoot: c.Store.Root, bindings: grantBindings},
		Launcher:                  appopen.ExecLauncher{},
		Deduper:                   newProjectionDeduper(),
		Bindings:                  hostAppBindings,
		RunID:                     runSession.Layout.ID,
		WorkspaceID:               workspaceAuthority.WorkspaceID,
		GrantScopeBase:            projectionGrantScopeBase(runSession, workspaceAuthority),
		ResolveIdentityContext:    c.hostAppRunBindingIdentityResolverContext(runSession, workspaceAuthority, hostFSPolicy, hostAppForbiddenRoots),
		RevalidateIdentityContext: c.hostAppRunIdentityRevalidatorContext(runSession, workspaceAuthority, hostFSPolicy, hostAppForbiddenRoots),
		ValidateLifecycle:         hostAppLifecycle,
		BeginHandoff:              lifecycleEffects.beginHandoff,
	}
	adapters, err := cmdadapter.CompileWithResolver(runSession.Plan.RuntimeProfile, runSession.ProfileDir, adapterpack.RuntimeResolver{Store: adapterpack.NewStore(c.Store.Root)})
	if err != nil {
		return RunDataPlane{}, err
	}
	hostFSEnabled := len(hostFSPolicy.Grants) > 0
	if err := MaterializeHostFSD(runSession.RuntimeShimDir, runSession.Plan.Backend, hostFSEnabled); err != nil {
		return RunDataPlane{}, err
	}
	var hostFSRef lifecycle.ResourceRef
	var brokerRef lifecycle.ResourceRef
	bridgeRefs := make([]lifecycle.ResourceRef, 0, len(portBridges))
	if opts.Lifecycle != nil {
		if hostFSEnabled {
			hostFSRef, err = lifecycleTracker.plan(ctx, lifecycle.RegistrationSpec{
				Kind: lifecycle.KindHostFSReadProvider, ID: runSession.Layout.ID,
				OwnerKind: "session", OwnerID: runSession.Layout.ID,
				Dependencies: []lifecycle.DependencySpec{{Ref: opts.Lifecycle.Session(), StopMode: lifecycle.StopModeDrain}},
				Persistence:  lifecycle.PersistenceEphemeral, ClosePolicy: lifecycle.ClosePreStopDrain,
				PossibleVMDependency: true,
			})
			if err != nil {
				return RunDataPlane{}, err
			}
		}
		brokerRef, err = lifecycleTracker.plan(ctx, lifecycle.RegistrationSpec{
			Kind: lifecycle.KindBrokerListener, ID: runSession.Layout.ID,
			OwnerKind: "session", OwnerID: runSession.Layout.ID,
			Dependencies: []lifecycle.DependencySpec{{Ref: opts.Lifecycle.Session(), StopMode: lifecycle.StopModeDrain}},
			Persistence:  lifecycle.PersistenceEphemeral, ClosePolicy: lifecycle.ClosePreStopDrain,
			PossibleVMDependency: true,
		})
		if err != nil {
			return RunDataPlane{}, err
		}
		for index := range portBridges {
			ref, planErr := lifecycleTracker.plan(ctx, lifecycle.RegistrationSpec{
				Kind: lifecycle.KindRunBridge, ID: fmt.Sprintf("%s-%d", runSession.Layout.ID, index+1),
				OwnerKind: "session", OwnerID: runSession.Layout.ID,
				Dependencies: []lifecycle.DependencySpec{
					{Ref: opts.Lifecycle.Root(), StopMode: lifecycle.StopModePin},
					{Ref: opts.Lifecycle.Session(), StopMode: lifecycle.StopModeDrain},
				},
				Persistence: lifecycle.PersistenceEphemeral, ClosePolicy: lifecycle.ClosePreStopDrain,
				PossibleVMDependency: true,
			})
			if planErr != nil {
				return RunDataPlane{}, planErr
			}
			bridgeRefs = append(bridgeRefs, ref)
		}
		if err := lifecycleTracker.commit(ctx); err != nil {
			return RunDataPlane{}, err
		}
	}
	hostFSService := hostfs.NewService(hostFSPolicy)
	hostFSService.BeginStage = lifecycleEffects.beginHostFSStage
	var hostFSReadOwner *readgrant.Owner
	var hostFSReadProvider *hostFSReadProvider
	ownerTransferred := false
	defer func() {
		if !ownerTransferred && hostFSReadOwner != nil {
			lifecycleStartupCleanupErr = errors.Join(lifecycleStartupCleanupErr, hostFSReadOwner.Close())
		}
	}()
	if hostFSEnabled {
		hostFSReadOwner, err = readgrant.AcquireOwner(runSession.Layout.HostFSReadOwnerLock)
		if err != nil {
			return RunDataPlane{}, err
		}
		hostFSReadProvider, err = newHostFSReadProvider(c, runSession.Layout.ID, runSession.Layout.HostFSReadDir, runSession.Layout.HostFSReadOwnerLock, hostFSPolicy)
		if err != nil {
			return RunDataPlane{}, err
		}
		hostFSService.ReadAuthority = newHostAppRunResourceAuthority(hostFSReadProvider, runSession.Plan.ProfileName)
		if err := lifecycleTracker.activate(ctx, hostFSRef); err != nil {
			return RunDataPlane{}, err
		}
	}
	if hostFSPolicyHasOverlay(hostFSPolicy) {
		overlayStore, err := hostfsoverlay.NewStore(filepath.Join(runSession.Layout.Dir, "hostfs-overlay"))
		if err != nil {
			return RunDataPlane{}, err
		}
		hostFSService.Overlay = overlayStore
		hostFSService.Context = hostfs.OverlayContext{
			SessionID: runSession.Layout.ID,
			Profile:   runSession.Plan.ProfileName,
			Backend:   runSession.Plan.Backend,
			Privilege: hostfsoverlay.Privilege{
				Status: "unknown",
				Reason: "guest privilege separation is recorded by 009; HostFS write apply remains operator-gated",
			},
		}
	}
	brokerCtx, cancel := context.WithCancel(ctx)
	listenEndpoint := BrokerEndpointForBackend(runSession.Plan.Backend, runSession.Layout)
	opener := opts.Opener
	if opener == nil {
		opener = broker.NoopOpener{}
	}
	server := &broker.Server{
		SessionID:           runSession.Layout.ID,
		Token:               token,
		Socket:              runSession.Layout.BrokerSock,
		Endpoint:            listenEndpoint,
		HostRoot:            workspaceAuthority.HostRoot,
		GuestRoot:           workspaceAuthority.GuestRoot,
		PhysicalGuestRoot:   workspaceAuthority.PhysicalGuestRoot,
		WorkspaceID:         workspaceAuthority.WorkspaceID,
		Profile:             runSession.Plan.ProfileName,
		ProfileDir:          filepath.Join(runSession.RuntimeSessionDir, "policy"),
		Backend:             runSession.Plan.Backend,
		WorkspaceMode:       runSession.Plan.RuntimeProfile.Workspace.Mode,
		NetworkMode:         runSession.Plan.RuntimeProfile.Network.Mode,
		Commands:            registry.ShimNames(),
		CommandRegistry:     registry,
		CommandAdapters:     adapters,
		ScriptRefs:          runSession.PolicyScriptRefs,
		Evaluator:           policy.NewEvaluator(runSession.Plan.RuntimeProfile),
		Audit:               runSession.Audit,
		Opener:              opener,
		HostFS:              &hostFSService,
		HostFSReadDecisions: hostFSReadProvider,
		HostApp:             hostAppProjection,
	}
	if err := server.StartEndpoint(brokerCtx, listenEndpoint); err != nil {
		lifecycleStartupCleanupErr = errors.Join(lifecycleStartupCleanupErr, server.Close())
		cancel()
		return RunDataPlane{}, err
	}
	if err := lifecycleTracker.activate(ctx, brokerRef); err != nil {
		lifecycleStartupCleanupErr = errors.Join(lifecycleStartupCleanupErr, server.Close())
		cancel()
		return RunDataPlane{}, err
	}
	guestEndpoint, err := BrokerEndpointForGuest(runSession.Plan.Backend, server.Endpoint)
	if err != nil {
		lifecycleStartupCleanupErr = errors.Join(lifecycleStartupCleanupErr, server.Close())
		cancel()
		return RunDataPlane{}, err
	}
	if err := WriteBrokerEndpoint(runSession.Layout.BrokerEndpointPath, guestEndpoint); err != nil {
		lifecycleStartupCleanupErr = errors.Join(lifecycleStartupCleanupErr, server.Close())
		cancel()
		return RunDataPlane{}, err
	}
	env := AppendBrokerEnv(runSession.Env.Env, guestEndpoint, runSession.Layout.ID, token, runSession.Layout.BrokerSock)
	if runSession.Plan.Backend == "lima" {
		env = lima.GuestEnv(env)
	}
	if err := aw.Emit(audit.Event{
		Session:  runSession.Layout.ID,
		Profile:  runSession.Plan.ProfileName,
		Backend:  runSession.Plan.Backend,
		Action:   "session.start",
		Decision: "allow",
		Details: func() map[string]any {
			visibility := summarizeHostFSVisibility(runSession.Plan.RuntimeProfile, hostFSPolicy)
			return map[string]any{
				"workspace":                workspaceAuthority.HostRoot,
				"guestWork":                workspaceAuthority.GuestRoot,
				"workspaceId":              workspaceAuthority.WorkspaceID,
				"network":                  runSession.Plan.RuntimeProfile.Network.Mode,
				"networkPlan":              presence(runNetwork.Plan.ManifestPath),
				"command":                  strings.Join(runSession.Plan.Command, " "),
				"brokerEndpoint":           "present",
				"brokerTransport":          guestEndpoint.Network,
				"hostfsVisibilityPosture":  visibility.Posture,
				"hostfsDiscoverGrants":     visibility.DiscoverGrants,
				"hostfsDiscoverDeny":       visibility.DiscoverDeny,
				"hostfsMaxListEntries":     visibility.MaxListEntries,
				"hostfsMaxDepth":           visibility.MaxDepth,
				"hostfsVisibilityNonClaim": visibility.NonClaim,
			}
		}(),
	}); err != nil {
		lifecycleStartupCleanupErr = errors.Join(lifecycleStartupCleanupErr, server.Close())
		cancel()
		return RunDataPlane{}, err
	}
	portBridgeLeases, portBridgeEndpoints, err := startRunPortBridges(brokerCtx, runSession, portBridges, env, opts.Backend, aw)
	if err != nil {
		lifecycleStartupCleanupErr = errors.Join(lifecycleStartupCleanupErr,
			closeRunPortBridgeLeases(portBridgeLeases, aw, runSession.Layout.ID, runSession.Plan.ProfileName, runSession.Plan.Backend),
			server.Close())
		cancel()
		return RunDataPlane{}, err
	}
	for _, ref := range bridgeRefs {
		if err := lifecycleTracker.activate(ctx, ref); err != nil {
			lifecycleStartupCleanupErr = errors.Join(lifecycleStartupCleanupErr,
				closeRunPortBridgeLeases(portBridgeLeases, aw, runSession.Layout.ID, runSession.Plan.ProfileName, runSession.Plan.Backend),
				server.Close())
			cancel()
			return RunDataPlane{}, err
		}
	}
	ownerTransferred = true
	projectionGrantTransferred = true
	lifecycleTransferred = true
	lifecycleRefs := lifecycleTracker.transfer()
	return RunDataPlane{
		Registry:             registry,
		ProjectionReadiness:  projectionReadiness,
		ObserverHelperDigest: observerHelperDigest,
		HostFSPolicy:         hostFSPolicy,
		HostFSEnabled:        hostFSEnabled,
		HostFSGrafts:         HostFSGrafts(hostFSPolicy),
		PortBridges:          portBridgeEndpoints,
		BrokerListenEndpoint: server.Endpoint,
		BrokerGuestEndpoint:  guestEndpoint,
		Env:                  env,
		Broker:               server,
		portBridgeLeases:     portBridgeLeases,
		audit:                aw,
		auditSession:         runSession.Layout.ID,
		auditProfile:         runSession.Plan.ProfileName,
		auditBackend:         runSession.Plan.Backend,
		cancel:               cancel,
		hostFSReadOwner:      hostFSReadOwner,
		projectionGrantIDs:   append([]string(nil), projectionGrantIDs...),
		lifecycle:            opts.Lifecycle,
		lifecycleRefs:        lifecycleRefs,
	}, nil
}

func prepareProjectionSafeDataDir(path string) error {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return errors.New("projection safe user-data directory must be absolute")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("projection safe user-data path must be a real directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

// compileRunProjectionGrants derives approval work only from immutable binding
// access. Profile host-app-mode compatibility has already been compiled into the
// built-in VS Code binding by CompileHostAppCatalog.
func compileRunProjectionGrants(runSession RunSession, authority runSessionWorkspaceAuthority, appBindings []hostcap.OpenResourceBinding) (map[string]projectionGrantBinding, []projectionGrantBinding) {
	byCommand := map[string]projectionGrantBinding{}
	var required []projectionGrantBinding
	for _, appBinding := range appBindings {
		if appBinding.Access != hostcap.BindingAccessAskEachRun {
			continue
		}
		for _, command := range appBinding.Commands {
			binding := projectionGrantBindingForRun(runSession, authority, appBinding, command)
			byCommand[command] = binding
			required = append(required, binding)
		}
	}
	return byCommand, required
}

// ensureRunProjectionDecision creates the generic run-scoped approval used by
// ask-each-run bindings. It intentionally does not consult profile host-app-mode;
// that setting is only a compatibility input to the built-in catalog binding.
func (c Core) ensureRunProjectionDecision(binding projectionGrantBinding) (decision.Decision, error) {
	store, err := c.decisionStore()
	if err != nil {
		return decision.Decision{}, err
	}
	id := binding.decisionID()
	if existing, err := store.RawDecision(id); err == nil {
		if !projectionGrantMatches(existing, binding) {
			return decision.Decision{}, errors.New("host-app decision binding mismatch")
		}
		return decision.RedactDecision(existing), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return decision.Decision{}, err
	}
	now := time.Now().UTC()
	workspaceWritable := strings.Contains(","+binding.ResourceClasses+",", ",workspace,")
	d := decision.Decision{
		ID:   id,
		Kind: decision.KindHostAppOpenResource,
		Source: decision.Source{
			Profile: binding.Profile,
			Session: binding.SessionID,
			Backend: binding.Backend,
			Surface: "projection",
		},
		State: decision.StatePending,
		Risk:  map[string]any{"riskClass": "high", "workspaceWritable": workspaceWritable},
		ProposedAction: map[string]any{
			"capability": hostcap.CapabilityAppOpenResource,
			"mode":       ProjectionHostAppModeTrusted,
			"binding":    binding.data(),
		},
		Preview: decision.Preview{
			Summary: "Allow this exact host-app binding to open its declared resource classes for this run",
			Facts: map[string]any{
				"mode":            ProjectionHostAppModeTrusted,
				"workspaceId":     binding.WorkspaceID,
				"environmentId":   binding.EnvironmentID,
				"subject":         binding.Subject,
				"resourceClasses": binding.ResourceClasses,
				"hostfsAuthority": binding.HostFSAuthority,
				"hostfsOwner":     binding.HostFSOwner,
			},
		},
		AllowedActions: []string{decision.ActionApprove, decision.ActionDeny, decision.ActionRevoke},
		DefaultOutcome: decision.DefaultOutcomeDeny,
		TimeoutAt:      now.Add(15 * time.Minute),
		ProviderRef: decision.ProviderRef{
			Provider:  decision.KindHostAppOpenResource,
			SessionID: binding.SessionID,
			Data:      binding.data(),
		},
		AuditRef:  "audit:host-app-open-resource:" + id,
		CreatedAt: now,
	}
	return c.CreateDecision(d)
}

// runProjectionGrantChecker admits the exact approved decision for a binding
// selected as ask-each-run when the run catalog was compiled.
type runProjectionGrantChecker struct {
	storeRoot string
	bindings  map[string]projectionGrantBinding
}

func (g runProjectionGrantChecker) TrustedGrantActive(scope hostcap.GrantScope) bool {
	binding, ok := g.bindings[scope.Command]
	if !ok || g.storeRoot == "" || scope != binding.scope() {
		return false
	}
	// A durable trusted-host-app workspace grant (operator profile policy) authorizes
	// the open without a per-run decision — this is what makes one-shot `code .`
	// usable in trusted mode. It requires trusted host-app-mode and an exact match on
	// the Core-derived workspace + binding identity; safe mode and any drift fail
	// closed here and fall through to the per-run decision path below.
	if trustedHostAppGrantMatches(g.storeRoot, scope) {
		return true
	}
	// No persistent grant. In trusted mode, record a request so `allow
	// host-app` can promote the exact run-observed identity (best-effort hint,
	// never authority), then fall through to the per-run decision path.
	maybeRecordTrustedHostAppRequest(g.storeRoot, scope)
	d, err := decision.NewStore(g.storeRoot).RawDecision(binding.decisionID())
	return err == nil && d.State == decision.StateApproved && projectionGrantMatches(d, binding)
}

func (g runProjectionGrantChecker) TrustedGrantActiveForResource(scope hostcap.GrantScope, resource hostcap.ResourceRef) bool {
	binding, ok := g.bindings[scope.Command]
	class := hostcap.PublicResourceClass(resource.Kind)
	if !ok || class == "" || !strings.Contains(","+binding.ResourceClasses+",", ","+class+",") {
		return false
	}
	return g.TrustedGrantActive(scope)
}

func (c Core) CloseRunDataPlane(dataPlane RunDataPlane) error {
	var errs []error
	if err := closeRunPortBridgeLeases(dataPlane.portBridgeLeases, dataPlane.audit, dataPlane.auditSession, dataPlane.auditProfile, dataPlane.auditBackend); err != nil {
		errs = append(errs, fmt.Errorf("close run port bridges: %w", err))
	}
	if dataPlane.cancel != nil {
		dataPlane.cancel()
	}
	if dataPlane.Broker != nil {
		if err := dataPlane.Broker.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close run broker: %w", err))
		}
		if dataPlane.audit != nil {
			observation := dataPlane.Broker.TransportObservation()
			if err := dataPlane.audit.Emit(audit.Event{
				Session:  dataPlane.auditSession,
				Profile:  dataPlane.auditProfile,
				Backend:  dataPlane.auditBackend,
				Action:   "broker.transport.observe",
				Decision: "allow",
				Details:  brokerTransportObservationDetails(observation),
			}); err != nil {
				errs = append(errs, fmt.Errorf("emit run broker transport observation: %w", err))
			}
		}
	}
	if dataPlane.hostFSReadOwner != nil {
		if err := dataPlane.hostFSReadOwner.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close HostFS read owner: %w", err))
		}
	}
	for _, decisionID := range dataPlane.projectionGrantIDs {
		if err := c.invalidateProjectionGrant(decisionID, "session-ended"); err != nil {
			errs = append(errs, fmt.Errorf("invalidate host-app projection grant: %w", err))
		}
	}
	providerErr := errors.Join(errs...)
	for index := len(dataPlane.lifecycleRefs) - 1; index >= 0; index-- {
		if dataPlane.lifecycle == nil {
			break
		}
		if err := dataPlane.lifecycle.Release(context.Background(), dataPlane.lifecycleRefs[index], providerErr); err != nil {
			errs = append(errs, fmt.Errorf("release lifecycle resource %s: %w", dataPlane.lifecycleRefs[index].Kind, err))
		}
	}
	return errors.Join(errs...)
}

func brokerTransportObservationDetails(observation broker.TransportObservation) map[string]any {
	return map[string]any{
		"scope":               "session-window",
		"accepted":            observation.Accepted,
		"rejectedAfterClose":  observation.RejectedAfterClose,
		"requestParsed":       observation.RequestParsed,
		"requestParseFailed":  observation.RequestParseFailed,
		"responseWritten":     observation.ResponseWritten,
		"responseWriteFailed": observation.ResponseWriteFailed,
	}
}

func BrokerEndpointForBackend(backendName string, layout session.Layout) broker.Endpoint {
	if backendName == "lima" {
		return broker.TCPEndpoint("0.0.0.0:0")
	}
	return broker.UnixEndpoint(layout.BrokerSock)
}

func BrokerEndpointForGuest(backendName string, listen broker.Endpoint) (broker.Endpoint, error) {
	if backendName == "lima" {
		return lima.GuestBrokerEndpoint(listen)
	}
	return listen, nil
}

func AppendBrokerEnv(env []string, endpoint broker.Endpoint, sessionID, token, socket string) []string {
	env = append(env,
		broker.EnvEndpoint+"="+endpoint.String(),
		broker.EnvSession+"="+sessionID,
		broker.EnvToken+"="+token,
	)
	if endpoint.Network == broker.EndpointUnix {
		env = append(env, broker.EnvSock+"="+socket)
	}
	return env
}

func WriteBrokerEndpoint(path string, endpoint broker.Endpoint) error {
	data, err := json.MarshalIndent(endpoint, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func HostFSProfileForRun(p profile.Profile, disableProfileGrants bool) hostfs.Config {
	config := p.HostFS
	if disableProfileGrants {
		config.Grants = nil
	}
	return config
}

func hostFSPolicyHasOverlay(policy hostfs.EffectivePolicy) bool {
	for _, grant := range policy.Grants {
		if grant.Overlay {
			return true
		}
	}
	return false
}

func HostFSGrafts(policy hostfs.EffectivePolicy) []string {
	seen := map[string]bool{}
	var grafts []string
	for _, grant := range policy.Grants {
		path := grant.HostPath
		if grant.Scope == hostfs.ScopeExactFile {
			path = filepath.Dir(path)
		} else if grant.Scope == hostfs.ScopeGlob {
			path = hostFSGlobGraftBase(path)
		}
		if !hostFSGraftablePath(path) || seen[path] {
			continue
		}
		seen[path] = true
		grafts = append(grafts, path)
	}
	sort.Strings(grafts)
	return grafts
}

func hostFSGlobGraftBase(pattern string) string {
	index := strings.IndexAny(pattern, "*?[")
	if index < 0 {
		return filepath.Dir(pattern)
	}
	separator := string(filepath.Separator)
	prefix := pattern[:index]
	lastSeparator := strings.LastIndex(prefix, separator)
	if lastSeparator <= 0 {
		return separator
	}
	return filepath.Clean(prefix[:lastSeparator])
}

func hostFSGraftablePath(path string) bool {
	clean := filepath.Clean(path)
	for _, root := range []string{"/Users", "/Volumes", "/private"} {
		if clean == root || strings.HasPrefix(clean, root+"/") {
			return clean != root
		}
	}
	return false
}

func MaterializeCommandProxyShims(dir, backendName string, registry cmdproxy.Registry, netPlan netpolicy.Plan) error {
	if backendName == "lima" {
		return materializeLimaShims(dir, registry, netPlan)
	}
	return materializeNativeShims(dir, registry)
}

func materializeNativeShims(dir string, registry cmdproxy.Registry) error {
	shimPath := ResolveShimPath()
	for _, reg := range registry.Registrations() {
		script := nativeShimScript(shimPath, reg)
		for _, name := range append([]string{reg.Name}, reg.Aliases...) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
				return err
			}
		}
	}
	return nil
}

func materializeLimaShims(dir string, registry cmdproxy.Registry, netPlan netpolicy.Plan) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	guestShim := filepath.Join(dir, "hideout-shim")
	if source := ResolveLinuxShimPath(); source != "" {
		if err := copyExecutable(source, guestShim); err != nil {
			return err
		}
	} else {
		return errors.New("lima backend requires a prebuilt linux hideout-shim; set HIDEOUT_LINUX_SHIM_PATH or install hideout-shim-linux")
	}
	for _, reg := range registry.Registrations() {
		script := limaShimScript(reg)
		for _, name := range append([]string{reg.Name}, reg.Aliases...) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
				return err
			}
		}
	}
	if netPlan.Mode == netpolicy.ModeTun2Socks {
		resolution, err := resolveLinuxTun2Socks()
		if err != nil {
			return err
		}
		if resolution.Path == "" {
			return errors.New("tun2socks privacy mode requires a verified package helper or valid explicit development override")
		}
		if err := helperbin.CopyVerifiedExecutable(
			resolution.Path,
			filepath.Join(dir, "tun2socks"),
			resolution.ExpectedSHA256,
		); err != nil {
			return err
		}
		// The DoH DNS stub mediates guest DNS over the privacy path when a
		// mediated resolver is declared. Best-effort like tun2socks: the guest
		// bootstrap fails closed if the stub is missing at run time.
		if netPlan.MediatedResolver != "" {
			if source := ResolveLinuxDNSStubPath(); source != "" {
				if err := copyExecutable(source, filepath.Join(dir, "hideout-dns-stub")); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func nativeShimScript(shimPath string, reg cmdproxy.Registration) string {
	if shimPath == "" {
		return "#!/bin/sh\necho 'hideout-shim unavailable' >&2\nexit 69\n"
	}
	return shimPreamble() + shimEnv(reg) + fmt.Sprintf("HIDEOUT_COMMAND_PROXY_SHIM_DIR=\"$shim_dir\" exec %q %s \"$(basename \"$0\")\" \"$@\"\n", shimPath, shimMetadataArgs(reg))
}

func limaShimScript(reg cmdproxy.Registration) string {
	return shimPreamble() + shimEnv(reg) + fmt.Sprintf("HIDEOUT_COMMAND_PROXY_SHIM_DIR=\"$shim_dir\" exec \"$shim_dir/hideout-shim\" %s \"$(basename \"$0\")\" \"$@\"\n", shimMetadataArgs(reg))
}

func shimPreamble() string {
	return "#!/bin/sh\nshim_dir=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\n"
}

func shimEnv(reg cmdproxy.Registration) string {
	script := fmt.Sprintf("HIDEOUT_COMMAND_PROXY_ACTION=%q \\\n", reg.Action)
	if reg.Action == cmdproxy.ActionCommandAdapter {
		script += fmt.Sprintf("HIDEOUT_COMMAND_PROXY_ADAPTER_ID=%q \\\n", reg.AdapterID)
	}
	if reg.Action == cmdproxy.ActionHostAppOpenResource && reg.OpenResourceGrammar != nil {
		raw, _ := json.Marshal(reg.OpenResourceGrammar)
		script += fmt.Sprintf("HIDEOUT_COMMAND_PROXY_BINDING_DIGEST=%q \\\n", reg.BindingDigest)
		script += fmt.Sprintf("HIDEOUT_COMMAND_PROXY_GRAMMAR_B64=%q \\\n", base64.StdEncoding.EncodeToString(raw))
	}
	return script
}

func shimMetadataArgs(reg cmdproxy.Registration) string {
	args := fmt.Sprintf("--action %q", reg.Action)
	if reg.Action == cmdproxy.ActionCommandAdapter {
		args += fmt.Sprintf(" --adapter-id %q", reg.AdapterID)
	}
	return args
}

func MaterializeHostFSD(dir, backendName string, enabled bool) error {
	if !enabled || backendName != "lima" {
		return nil
	}
	source := ResolveLinuxHostFSDPath()
	if source == "" {
		return errors.New("lima HostFS requires a prebuilt linux hideout-hostfsd; set HIDEOUT_LINUX_HOSTFSD_PATH or install hideout-hostfsd-linux")
	}
	return copyExecutable(source, filepath.Join(dir, "hideout-hostfsd"))
}

func MaterializeSessionSupervisor(dir, backendName string, required bool) error {
	if !required || backendName != "lima" {
		return nil
	}
	source := ResolveLinuxSessionSupervisorPath()
	if source == "" {
		return errors.New("lima daemon sessions require a manifest-verified linux hideout-session-supervisor")
	}
	return copyExecutable(source, filepath.Join(dir, helperbin.LinuxSessionSupervisorCommand))
}

func MaterializeObserver(dir, backendName string, required bool) (string, error) {
	if !required || backendName != "lima" {
		return "", nil
	}
	resolution, err := resolveLinuxObserver()
	if err != nil {
		return "", err
	}
	if resolution.Path == "" {
		return "", errors.New(
			"lima activity observation requires a manifest-verified linux hideout-observer",
		)
	}
	digest := strings.TrimPrefix(resolution.ExpectedDigest, "sha256:")
	if err := helperbin.CopyVerifiedExecutable(
		resolution.Path,
		filepath.Join(dir, helperbin.LinuxObserverCommand),
		digest,
	); err != nil {
		return "", err
	}
	return resolution.ExpectedDigest, nil
}

func MaterializeWorkspacePortal(dir, backendName string, required bool) error {
	if !required || backendName != "lima" {
		return nil
	}
	source := ResolveLinuxWorkspacePortalPath()
	if source == "" {
		return errors.New("shared Lima workspaces require a manifest-verified linux hideout-workspace-portal")
	}
	return copyExecutable(source, filepath.Join(dir, helperbin.LinuxWorkspacePortalCommand))
}

func ResolveShimPath() string {
	return helperbin.ResolveShimPath()
}

func ResolveLinuxShimPath() string {
	store, err := profile.DefaultStore()
	if err != nil {
		return ""
	}
	return helperbin.ResolveLinuxShimPath(store.Root, runtime.GOARCH)
}

func ResolveLinuxTun2SocksPath() string {
	resolution, err := resolveLinuxTun2Socks()
	if err != nil {
		return ""
	}
	return resolution.Path
}

func resolveLinuxTun2Socks() (helperbin.Tun2SocksResolution, error) {
	store, err := profile.DefaultStore()
	if err != nil {
		return helperbin.Tun2SocksResolution{}, err
	}
	return helperbin.ResolveLinuxTun2Socks(helperbin.Tun2SocksResolveOptions{
		StoreRoot: store.Root,
		GOARCH:    runtime.GOARCH,
		Override:  os.Getenv(helperbin.LinuxTun2SocksPathEnvironment),
	})
}

func ResolveLinuxDNSStubPath() string {
	return helperbin.ResolveLinuxDNSStubPath(runtime.GOARCH)
}

func ResolveLinuxHostFSDPath() string {
	store, err := profile.DefaultStore()
	if err != nil {
		return ""
	}
	return helperbin.ResolveLinuxHostFSDPath(store.Root, runtime.GOARCH)
}

func ResolveLinuxSessionSupervisorPath() string {
	store, err := profile.DefaultStore()
	if err != nil {
		return ""
	}
	return helperbin.ResolveLinuxSessionSupervisorPath(store.Root, runtime.GOARCH)
}

func resolveLinuxObserver() (helperbin.LinuxObserverResolution, error) {
	store, err := profile.DefaultStore()
	if err != nil {
		return helperbin.LinuxObserverResolution{}, err
	}
	return helperbin.ResolveLinuxObserver(store.Root, runtime.GOARCH)
}

func ResolveLinuxWorkspacePortalPath() string {
	store, err := profile.DefaultStore()
	if err != nil {
		return ""
	}
	return helperbin.ResolveLinuxWorkspacePortalPath(store.Root, runtime.GOARCH)
}

func DefaultLinuxShimPath(goarch string) (string, error) {
	store, err := profile.DefaultStore()
	if err != nil {
		return "", err
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return helperbin.DefaultLinuxShimPath(store.Root, goarch), nil
}

func DefaultLinuxHostFSDPath(goarch string) (string, error) {
	store, err := profile.DefaultStore()
	if err != nil {
		return "", err
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return helperbin.DefaultLinuxHostFSDPath(store.Root, goarch), nil
}

func copyExecutable(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o700)
}
