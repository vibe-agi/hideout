package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/native"
	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/manager"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/operatorhelp"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
	workloadredact "github.com/vibe-agi/hideout/internal/workloadobs/redact"
	workloadstore "github.com/vibe-agi/hideout/internal/workloadobs/store"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

const (
	lockName  = "daemon.lock"
	auditName = "daemon-audit.jsonl"

	credentialRuntimeCheckInterval   = time.Second
	credentialRotationRetryMin       = time.Second
	credentialRotationRetryMax       = time.Minute
	credentialRuntimeShutdownTimeout = 10 * time.Second
)

// errAlreadyRunning is returned when a live daemon already serves the store.
var errAlreadyRunning = errors.New("daemon: already running for this store")

// IsAlreadyRunning reports whether err indicates an existing live daemon.
func IsAlreadyRunning(err error) bool { return errors.Is(err, errAlreadyRunning) }

// Options configures a daemon start.
type Options struct {
	Store              profile.Store
	BuildID            string
	TTL                time.Duration
	CredentialGrace    time.Duration
	Now                func() time.Time
	RunBackend         manager.RunBackendFactory
	RunOpener          manager.RunOpenerFactory
	RunServiceBackend  manager.RunServiceBackendFactory
	RunServiceOpener   manager.RunServiceOpenerFactory
	LifecycleBackend   manager.EnvironmentLifecycleBackendFactory
	LifecycleIdleGrace time.Duration
	// LifecycleAutomaticStop is enabled only after the real-backend lifecycle
	// evidence gate has passed. A backend factory alone must not turn on VM stop.
	LifecycleAutomaticStop bool
	// BackendShutdown releases process-scoped backend transports after sessions
	// and lifecycle work have drained. It must not carry capability authority.
	BackendShutdown    func() error
	WorkspaceProviders workspaceattach.ProviderFactory
	SessionCapacity    int
	// SecretStore is daemon-owned. Tests may inject an in-memory provider;
	// production defaults to the platform Keychain implementation.
	SecretStore secrets.RuntimeStore
	// HelpCatalog is a render-only projection supplied by the CLI entrypoint.
	// It carries no dispatch or mutation authority.
	HelpCatalog operatorhelp.Catalog
	// NetworkTransitionCheckpoints is an optional process-local evidence and
	// fault-injection seam. The production app leaves it nil; when supplied it
	// observes boundaries only after the durable Manager checkpoint.
	NetworkTransitionCheckpoints manager.NetworkTransitionEffectCheckpoint
	// ActivityStore is injectable for recovery and lifecycle tests. Production
	// opens the private bounded store rooted under Store.Root.
	ActivityStore *workloadstore.Store
	// MigrationProvider is optional and never enlarges the base backend
	// interface. Production injects the package-backed Lima provider; absence
	// leaves full-state migration explicitly unavailable.
	MigrationProvider backend.MigrationProvider
	ProductVersion    string
	// LiveResources lists resources that could survive a restart (running
	// environments). Defaults to none; the daemon reports any it cannot prove it
	// owns as orphans. Injectable for tests.
	LiveResources func(storeRoot string) []LiveResource
}

// Daemon is the single per-store local control-plane process.
type Daemon struct {
	store              profile.Store
	buildID            string
	runtimeDir         string
	socket             string
	instanceID         string
	limaHome           string
	startedAt          time.Time
	credentials        *credentialManager
	api                manager.API
	audit              *auditLog
	bus                *eventBus
	bg                 *bgRegistry
	own                *ownership
	orphans            []LiveResource
	ln                 net.Listener
	sessionListener    *SessionListener
	sessionServer      *sessionServer
	sessions           *sessionRegistry
	activityStore      *workloadstore.Store
	activityOwned      bool
	activityCleanup    *manager.ActivityCleanupService
	migrationWorkers   *migrationWorkerSet
	migrationSecrets   *manager.MigrationSecretInputStore
	migrationCache     *manager.MigrationInspectionCache
	secrets            *daemonSecretService
	environmentActions *manager.EnvironmentActionService
	lifecycle          *lifecycle.Coordinator
	lifecycleBackend   manager.EnvironmentLifecycleBackendFactory
	backendShutdown    func() error
	networkGateways    *netpolicy.GatewayRegistry
	lifecycleCtx       context.Context
	lifecycleCancel    context.CancelFunc
	lifecycleWG        sync.WaitGroup
	lifecycleSlots     chan struct{}
	server             *http.Server
	uiServer           *http.Server
	uiURL              string
	helpCatalog        operatorhelp.Catalog
	lockFile           *os.File
	tailStop           chan struct{}
	hostFSStop         chan struct{}
	credentialStop     chan struct{}
	credentialDone     chan struct{}
	done               chan struct{}

	mu    sync.Mutex
	state string
}

// Start acquires single-instance ownership, mints the operator token, opens the
// persistent daemon audit log, and serves the parity-locked Manager API over the
// store-rooted Unix socket. It fails closed on placement, permission, or
// single-instance violations. It returns errAlreadyRunning if a live daemon is
// already serving the store (check with IsAlreadyRunning).
func Start(opts Options) (*Daemon, error) {
	if opts.Store.Root == "" {
		return nil, errors.New("daemon: store root is required")
	}
	// Compatibility secret fallback is intentionally a point-in-time startup
	// snapshot. Later exports cannot silently alter a long-running daemon.
	startupEnvironment := append([]string(nil), os.Environ()...)
	buildID, err := resolveBuildID(opts.BuildID)
	if err != nil {
		return nil, fmt.Errorf("daemon: resolve build identity: %w", err)
	}
	dir, err := ensurePlacement(opts.Store.Root)
	if err != nil {
		return nil, err
	}
	if probeSocket(filepath.Join(dir, socketName)) {
		return nil, errAlreadyRunning
	}
	lockFile, err := acquireLock(filepath.Join(dir, lockName))
	if err != nil {
		return nil, err
	}
	// The lock is held, so no live daemon owns the store; a stale socket file from a
	// crash can be safely reclaimed.
	_ = os.Remove(filepath.Join(dir, socketName))

	lifecycleRecords, missingLifecycleRecords, err := lifecycleEnvironmentRecords(opts.Store, opts.LifecycleBackend != nil)
	if err != nil {
		releaseLock(lockFile, filepath.Join(dir, lockName))
		return nil, err
	}

	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	ttl := opts.TTL
	if ttl == 0 {
		ttl = 15 * time.Minute
	}
	grace := opts.CredentialGrace
	if grace == 0 {
		grace = defaultSessionLease
		if half := ttl / 2; half < grace {
			grace = half
		}
	}
	credentials, err := newCredentialManager(dir, ttl, grace, opts.Now)
	if err != nil {
		releaseLock(lockFile, filepath.Join(dir, lockName))
		return nil, err
	}
	al, err := openAuditLog(filepath.Join(dir, auditName))
	if err != nil {
		releaseLock(lockFile, filepath.Join(dir, lockName))
		return nil, err
	}
	ln, sock, err := listenSocket(opts.Store.Root)
	if err != nil {
		_ = al.close()
		releaseLock(lockFile, filepath.Join(dir, lockName))
		return nil, err
	}
	sessionListener, err := ListenSession(opts.Store.Root)
	if err != nil {
		_ = ln.Close()
		_ = al.close()
		releaseLock(lockFile, filepath.Join(dir, lockName))
		return nil, err
	}
	connectionID, err := newConnectionID()
	if err != nil {
		_ = sessionListener.Close()
		_ = ln.Close()
		_ = al.close()
		releaseLock(lockFile, filepath.Join(dir, lockName))
		return nil, err
	}
	instanceID := "daemon_" + strings.TrimPrefix(connectionID, "conn_")
	bus := newEventBusV2(instanceID, credentials.Generation)
	al.publish = bus.publishAudit
	core := manager.New(opts.Store)
	core.Observer = bus
	secretStore := opts.SecretStore
	if secretStore == nil {
		secretStore = secrets.NewKeychainStoreForRoot(opts.Store.Root)
	}
	secretService := newDaemonSecretService(core, secretStore)
	networkSecretResolver := daemonNetworkSecretResolver{
		managed: secretService,
		startup: netpolicy.EnvSecretResolver{Env: startupEnvironment},
	}
	lifecycleCoordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: lifecycle.JournalStore{Root: opts.Store.Root}, DaemonID: instanceID,
		Now: opts.Now, IdleGrace: opts.LifecycleIdleGrace, Enabled: opts.LifecycleAutomaticStop,
		Stop: func(ctx context.Context, request lifecycle.StopRequest) (lifecycle.StopResult, error) {
			if opts.LifecycleBackend == nil {
				return lifecycle.StopResult{}, errors.New("daemon lifecycle backend is unavailable")
			}
			record, loadErr := (environment.Store{Root: opts.Store.Root}).Load(request.Incarnation.EnvironmentID)
			if loadErr != nil {
				return lifecycle.StopResult{}, loadErr
			}
			provider, providerErr := opts.LifecycleBackend(record)
			if providerErr != nil {
				return lifecycle.StopResult{}, providerErr
			}
			return core.StopEnvironmentIncarnation(ctx, request, provider)
		},
		Publish: func(event lifecycle.Event) {
			bus.publishLifecycle(event.Status, "progress")
			al.record("lifecycle."+event.Kind, lifecycleAuditDecision(event.Kind), map[string]any{
				"environmentId": event.EnvironmentID, "generation": event.Generation,
				"reasonCode": event.ReasonCode,
			})
		},
	})
	if err != nil {
		_ = sessionListener.Close()
		_ = ln.Close()
		_ = al.close()
		releaseLock(lockFile, filepath.Join(dir, lockName))
		return nil, err
	}
	for _, record := range lifecycleRecords {
		if _, err := lifecycleCoordinator.BeginReconciliation(context.Background(), record.ID); err != nil {
			_ = lifecycleCoordinator.Close()
			_ = sessionListener.Close()
			_ = ln.Close()
			_ = al.close()
			releaseLock(lockFile, filepath.Join(dir, lockName))
			return nil, err
		}
	}
	for _, environmentID := range missingLifecycleRecords {
		if _, err := lifecycleCoordinator.BeginReconciliation(context.Background(), environmentID); err != nil {
			_ = lifecycleCoordinator.Close()
			_ = sessionListener.Close()
			_ = ln.Close()
			_ = al.close()
			releaseLock(lockFile, filepath.Join(dir, lockName))
			return nil, err
		}
		if err := lifecycleCoordinator.BlockReconciliation(environmentID, "environment-record-unavailable"); err != nil {
			_ = lifecycleCoordinator.Close()
			_ = sessionListener.Close()
			_ = ln.Close()
			_ = al.close()
			releaseLock(lockFile, filepath.Join(dir, lockName))
			return nil, err
		}
	}
	core.LifecycleResources = lifecycleCoordinator
	core.LifecycleDisposals = lifecycleCoordinator
	publishBackground := func(id, op, status string) {
		bus.publishBackground(id, op, status)
		_, _ = core.CreateNotice(decision.Notice{
			ID:       "background-" + id,
			Kind:     decision.KindBackgroundStatus,
			Severity: backgroundNoticeSeverity(status),
			Status:   status,
			Source:   decision.Source{Surface: "daemon"},
			Payload: map[string]any{
				"id":     id,
				"op":     op,
				"status": status,
			},
			Preview:  decision.Preview{Summary: "background " + op + " is " + status},
			AuditRef: "audit:background:" + id,
		})
	}
	activityStore := opts.ActivityStore
	activityOwned := false
	if activityStore == nil {
		activityStore, err = workloadstore.Open(workloadstore.Options{
			Root: filepath.Join(opts.Store.Root, "activity"),
			Now:  opts.Now,
		})
		if err != nil {
			_ = lifecycleCoordinator.Close()
			_ = sessionListener.Close()
			_ = ln.Close()
			_ = al.close()
			releaseLock(lockFile, filepath.Join(dir, lockName))
			return nil, fmt.Errorf("daemon: open activity store: %w", err)
		}
		activityOwned = true
	}
	sessions := newSessionRegistry(opts.SessionCapacity, opts.Now)
	sessions.activity.setPersistentStore(activityStore)
	sessions.activity.setRedactionBuilder(workloadredact.Builder{
		Secrets: secretStore,
		ControlTokens: daemonControlTokenSource{
			credentials: credentials,
		},
		Now: opts.Now,
	})
	activityCleanup := manager.NewActivityCleanupService(
		newDaemonActivityCleanupStore(activityStore, sessions.activity),
		opts.Now,
	)
	secretService.beginApply = sessions.activity.beginSecretMutation
	sessions.setActivityCleanup(activityCleanup)
	sessions.setWorkspacePublisher(bus.publishWorkspaceViews)
	core.ActiveWorkspaceViews = sessions.workspaceViewSnapshots
	activityProvider, err := newDaemonActivityProviderWithStore(
		sessions.activity,
		activityStore,
		instanceID,
		credentials.Token(),
	)
	if err != nil {
		if activityOwned {
			_ = activityStore.Close()
		}
		_ = lifecycleCoordinator.Close()
		_ = sessionListener.Close()
		_ = ln.Close()
		_ = al.close()
		releaseLock(lockFile, filepath.Join(dir, lockName))
		return nil, fmt.Errorf("daemon: initialize activity query provider: %w", err)
	}
	profileTransactions := manager.NewProfileTransactionService(core)
	profileTransactions.Mutations = lifecycleCoordinator
	gatewayNetworkTransitions := manager.GatewayNetworkTransitionProvider{
		StoreRoot: opts.Store.Root,
		Gateways:  core.NetworkGateways,
		Now:       opts.Now,
	}
	networkRecoveryProvider :=
		manager.LiveDNSNetworkTransitionRecoveryProvider{
			StoreRoot: opts.Store.Root,
			Runtimes: startupNetworkRuntimeProvider{
				storeRoot: opts.Store.Root,
				backends:  opts.LifecycleBackend,
			},
			Now: opts.Now,
		}
	liveNetworkTransitions :=
		&manager.ProfileNetworkTransitionCoordinator{
			Core: core,
			Provider: manager.LiveNetworkTransitionProvider{
				Gateway:  gatewayNetworkTransitions,
				Runtimes: sessions,
				Now:      opts.Now,
			},
			Sessions:         sessions,
			SecretReferences: secretService,
			SecretResolver:   networkSecretResolver,
			RecoveryProvider: networkRecoveryProvider,
			Checkpoints:      opts.NetworkTransitionCheckpoints,
		}
	profileTransactions.NetworkTransitions =
		liveNetworkTransitions
	secretService.manager.NetworkTransitions =
		liveNetworkTransitions
	if opts.Now != nil {
		profileTransactions.SetClock(opts.Now)
	}
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	d := &Daemon{
		store:            opts.Store,
		buildID:          buildID,
		limaHome:         resolveLimaHome(),
		runtimeDir:       dir,
		socket:           sock,
		instanceID:       instanceID,
		startedAt:        now,
		credentials:      credentials,
		audit:            al,
		bus:              bus,
		bg:               newBGRegistry(publishBackground),
		own:              newOwnership(),
		ln:               ln,
		sessionListener:  sessionListener,
		sessions:         sessions,
		activityStore:    activityStore,
		activityOwned:    activityOwned,
		activityCleanup:  activityCleanup,
		secrets:          secretService,
		lifecycle:        lifecycleCoordinator,
		lifecycleBackend: opts.LifecycleBackend,
		backendShutdown:  opts.BackendShutdown,
		networkGateways:  core.NetworkGateways,
		helpCatalog:      opts.HelpCatalog.Clone(),
		lifecycleCtx:     lifecycleCtx,
		lifecycleCancel:  lifecycleCancel,
		lifecycleSlots:   make(chan struct{}, 4),
		lockFile:         lockFile,
		hostFSStop:       make(chan struct{}),
		credentialStop:   make(chan struct{}),
		credentialDone:   make(chan struct{}),
		done:             make(chan struct{}),
		state:            "serving",
		api: manager.API{
			Core:                core,
			Token:               credentials.Token(),
			TokenValidator:      credentials.Validate,
			AllowedHosts:        []string{"localhost", "hideoutd"},
			Now:                 opts.Now,
			RunBackend:          opts.RunBackend,
			RunOpener:           opts.RunOpener,
			RunLifecycle:        lifecycleCoordinator,
			LifecycleMutations:  lifecycleCoordinator,
			ActivityProvider:    activityProvider,
			SecretProvider:      secretService,
			RunSecretResolver:   networkSecretResolver,
			ProfileTransactions: profileTransactions,
		},
	}
	d.api.EnvironmentStopApply = d.applyEnvironmentStopPlan
	d.api.EnvironmentCleanApply = d.applyEnvironmentCleanPlan
	d.api.OperatorSnapshotProvider = manager.OperatorSnapshotProviderFunc(d.operatorSnapshot)
	d.sessionServer = &sessionServer{
		core: core, credentials: credentials, instanceID: instanceID,
		registry: sessions, backendFactory: opts.RunServiceBackend,
		openerFactory: opts.RunServiceOpener, audit: al, lifecycle: lifecycleCoordinator,
		workspaceProviders: opts.WorkspaceProviders,
		networkResolver:    networkSecretResolver,
	}
	operationStore := manager.OperationStore{
		Root: opts.Store.Root,
		Now:  opts.Now,
	}
	operationService := manager.OperationService{
		Store:    operationStore,
		Observer: bus,
	}
	migrationSecrets := manager.NewMigrationSecretInputStore(
		manager.MigrationSecretInputStoreOptions{Now: opts.Now},
	)
	migrationCache := manager.NewMigrationInspectionCache(
		manager.MigrationInspectionCacheOptions{Now: opts.Now},
	)
	migrationService := manager.MigrationService{
		Store:        manager.MigrationStore{Root: opts.Store.Root, Now: opts.Now},
		Environments: environment.Store{Root: opts.Store.Root},
		Profiles:     opts.Store,
		Export:       opts.MigrationProvider, Import: opts.MigrationProvider,
		Config: native.ConfigMigrationHarness{
			HostOS: runtime.GOOS, HostArch: runtime.GOARCH,
		},
		SecretInputs: migrationSecrets, Secrets: secretStore, Now: opts.Now,
		ProductVersion: opts.ProductVersion, HostOS: runtime.GOOS, HostArch: runtime.GOARCH,
	}
	migrationImportService := manager.MigrationImportService{
		MigrationService: migrationService,
		BundleSource: manager.CachedMigrationBundleSource{
			SecretInputs: migrationSecrets, Cache: migrationCache,
		},
		InspectionCache: migrationCache,
	}
	d.migrationSecrets = migrationSecrets
	d.migrationCache = migrationCache
	d.migrationWorkers = newMigrationWorkerSet(
		migrationService, migrationImportService, al, bus,
	)
	d.api.Migrations = &manager.MigrationAPIService{
		Service: migrationService, Import: migrationImportService,
		Inspection: manager.MigrationInspectionService{
			SecretInputs: migrationSecrets, Cache: migrationCache,
		},
		StartExport: d.migrationWorkers.StartExport,
		StartImport: d.migrationWorkers.StartImport,
		Resume:      d.migrationWorkers.Resume,
		Cancel:      d.migrationWorkers.Cancel,
		Recover:     d.migrationWorkers.Recover,
	}
	environmentActions := &manager.EnvironmentActionService{
		Core:        core,
		Operations:  operationService,
		Now:         opts.Now,
		ApplyStop:   d.applyEnvironmentStopPlan,
		ApplyClean:  d.applyEnvironmentCleanPlan,
		ApplyDelete: d.applyEnvironmentDeletePlan,
		Prove:       d.proveEnvironmentAction,
	}
	d.environmentActions = environmentActions
	d.api.EnvironmentActions = environmentActions
	networkTransitions := manager.NetworkTransitionRecoveryService{
		Store:      operationStore,
		Operations: operationService,
		Provider:   networkRecoveryProvider,
	}
	if err := (startupOperationRecovery{
		Store:      operationStore,
		Operations: operationService,
		ReconcileEnvironment: func(
			ctx context.Context,
			operationID string,
		) (manager.Operation, error) {
			return environmentActions.ReconcileOperation(ctx, operationID)
		},
		ReconcileProfile: func(
			ctx context.Context,
			operationID string,
		) (manager.Operation, error) {
			result, err := profileTransactions.ReconcileOperation(
				ctx,
				operationID,
			)
			return result.Operation, err
		},
		ReconcileSecret: func(
			ctx context.Context,
			operationID string,
		) (manager.Operation, error) {
			result, err := secretService.
				reconcileOperationWithNetworkAuthorityReset(
					ctx,
					operationID,
					&manager.NetworkAuthorityResetProof{
						AuthorityID: instanceID,
						ObservedAt:  now,
					},
				)
			return result.Operation, err
		},
		ReconcileNetwork: networkTransitions.ReconcileOperation,
		Record:           al.record,
	}).Run(context.Background()); err != nil {
		lifecycleCancel()
		if activityOwned {
			_ = activityStore.Close()
		}
		_ = core.NetworkGateways.Close()
		_ = lifecycleCoordinator.Close()
		_ = sessionListener.Close()
		_ = ln.Close()
		_ = al.close()
		releaseLock(lockFile, filepath.Join(dir, lockName))
		return nil, fmt.Errorf(
			"daemon: reconcile accepted operations: %w",
			err,
		)
	}
	if err := d.migrationWorkers.ReconcileStartup(); err != nil {
		lifecycleCancel()
		migrationSecrets.Close()
		migrationCache.Close()
		if activityOwned {
			_ = activityStore.Close()
		}
		_ = core.NetworkGateways.Close()
		_ = lifecycleCoordinator.Close()
		_ = sessionListener.Close()
		_ = ln.Close()
		_ = al.close()
		releaseLock(lockFile, filepath.Join(dir, lockName))
		return nil, fmt.Errorf("daemon: reconcile migration operations: %w", err)
	}
	d.server = &http.Server{Handler: d.buildHandler()}
	d.startAuditTail()
	d.startHostFSWriteTimeoutWorker()
	d.startLoopbackUI()
	d.startCredentialRotation()
	d.audit.record("daemon.start", "allow", map[string]any{"socket": sock})

	// Restart fail-closed: any live resource the current instance cannot prove it
	// owns (it owns nothing at start) is reported and audited as an orphan — never
	// silently re-adopted and never destroyed.
	if opts.LiveResources != nil {
		d.orphans = d.own.detectOrphans(opts.LiveResources(opts.Store.Root))
		for _, res := range d.orphans {
			d.audit.record("daemon.orphan", "deny", map[string]any{
				"resource": res.ID,
				"kind":     res.Kind,
				"reason":   "live resource not owned by current instance; not re-adopted, not destroyed",
			})
		}
	}

	go func() { _ = d.server.Serve(ln) }()
	go func() { _ = d.sessionServer.serve(sessionListener) }()
	d.startLifecycleReconciliation(lifecycleRecords)
	d.startMissingDisposableRecovery(missingLifecycleRecords)
	return d, nil
}

// operatorSnapshot holds the event-bus sequence lock while the Manager reads
// its authoritative projections. Any event caused after the seed is captured
// therefore receives a strictly later sequence; clients cannot silently skip a
// delta published between projection reads and sequence capture.
func (d *Daemon) operatorSnapshot(
	ctx context.Context,
	query manager.OperatorSnapshotQuery,
) (manager.OperatorSnapshot, error) {
	// Reject malformed reads before decision maintenance. The API validates the
	// same boundary, but this provider is also exercised directly in tests and
	// must remain side-effect-free for invalid input.
	if err := query.Validate(); err != nil {
		return manager.OperatorSnapshot{}, err
	}
	service := manager.OperatorSnapshotService{
		Core: d.api.Core,
		Observation: manager.OperatorObservationProviderFunc(
			func(
				ctx context.Context,
				query manager.OperatorSnapshotQuery,
			) (manager.OperatorObservation, error) {
				return daemonOperatorObservationWithProvider(
					ctx,
					d.sessions.activity,
					d.api.ActivityProvider,
					d.activityStore,
					query,
				)
			},
		),
		NetworkRoutes: manager.GatewayNetworkTransitionProvider{
			StoreRoot: d.store.Root,
			Gateways:  d.networkGateways,
			Now:       d.api.Now,
		},
		MutationCapabilities: manager.DefaultConfigurationCapabilities(true),
		Now:                  d.api.Now,
	}
	// Decision timeout/lease maintenance can publish an event. Complete it
	// before taking the sequence fence so projection seeding cannot deadlock on
	// a re-entrant event publication.
	if err := service.Prepare(); err != nil {
		return manager.OperatorSnapshot{}, err
	}
	d.bus.mu.Lock()
	defer d.bus.mu.Unlock()
	sequence := d.bus.seq
	service.Connection = manager.OperatorConnectionProviderFunc(
		func(context.Context) (manager.OperatorConnectionProjection, error) {
			return manager.OperatorConnectionProjection{
				InstanceID: d.instanceID, CredentialGeneration: d.credentials.Generation(),
				Sequence:     sequence,
				StreamHealth: manager.OperatorStreamHealth{State: manager.OperatorHealthLive},
			}, nil
		},
	)
	return service.BuildPrepared(ctx, query)
}

func lifecycleAuditDecision(kind string) string {
	switch kind {
	case "backend-incarnation-superseded", "destructive-mutation-failed",
		"reconciliation-blocked", "resource-orphaned", "stop-deferred", "stop-unknown":
		return "deny"
	default:
		return "allow"
	}
}

func backgroundNoticeSeverity(status string) string {
	switch status {
	case "failed":
		return decision.NoticeSeverityError
	case "queued", "running":
		return decision.NoticeSeverityInfo
	default:
		return decision.NoticeSeverityWarning
	}
}

// SubmitBackground runs an allowed background operation (v1: environment
// stop/clean) in a daemon goroutine with queryable status, recording ownership
// for the current instance. It rejects any other op class.
func (d *Daemon) SubmitBackground(op string, run func(context.Context) error) (string, error) {
	id, err := d.bg.submit(op, run)
	if err != nil {
		return "", err
	}
	d.own.record(id, d.startedAt.Format(time.RFC3339))
	return id, nil
}

// BackgroundStatusOf returns the status of a background operation.
func (d *Daemon) BackgroundStatusOf(id string) (string, bool) {
	return d.bg.status(id)
}

// Orphans returns the live resources reported as orphans at start.
func (d *Daemon) Orphans() []LiveResource {
	return append([]LiveResource(nil), d.orphans...)
}

// Socket returns the daemon's Unix socket path.
func (d *Daemon) Socket() string { return d.socket }

// Token returns the current operator token.
func (d *Daemon) Token() string {
	if d == nil || d.credentials == nil {
		return ""
	}
	return d.credentials.Token()
}

// RuntimeDir returns the daemon's runtime directory.
func (d *Daemon) RuntimeDir() string { return d.runtimeDir }

// Status returns the current daemon status inventory.
func (d *Daemon) Status() Status {
	d.mu.Lock()
	state := d.state
	d.mu.Unlock()
	status := Status{
		Version: statusVersion, BuildID: d.buildID, State: state, InstanceID: d.instanceID,
		LimaHome:  d.limaHome,
		StartedAt: d.startedAt.Format(time.RFC3339),
		Transport: StatusTransport{
			Socket: d.socket, SessionSocket: d.sessionListener.Socket(),
			SessionProtocol: SessionProtocolVersion, BrowserURL: d.uiURL,
		},
		Background: d.bg.inventory(),
	}
	if d.credentials != nil {
		status.CredentialGeneration = d.credentials.Generation()
	}
	for _, snapshot := range d.sessions.snapshots() {
		if snapshot.SessionID == "" || snapshot.EnvironmentID == "" {
			continue
		}
		status.Sessions = append(status.Sessions, SessionStatus{
			Schema: "hideout.active-session/v1", ID: snapshot.SessionID,
			EnvironmentID: snapshot.EnvironmentID, Profile: snapshot.Profile,
			Backend: snapshot.Backend, WorkspaceID: snapshot.WorkspaceID,
			SessionSnapshotID: snapshot.SessionSnapshotID,
			State:             snapshot.State, OwnerStatus: "live", TerminalMode: snapshot.TerminalMode,
			StartedAt: snapshot.StartedAt, CommandClass: snapshot.CommandClass,
		})
	}
	for _, snapshot := range d.sessions.workspaceViewSnapshots() {
		status.WorkspaceAttachments = append(status.WorkspaceAttachments, snapshot.Attachment)
	}
	if d.lifecycle != nil {
		status.Lifecycle = d.lifecycle.Snapshot()
	}
	return status
}

// Stop performs an ordered shutdown: drain in-flight requests, record the stop,
// remove the socket, and release the stable lock inode. It is idempotent.
func (d *Daemon) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	d.mu.Lock()
	if d.state == "stopped" {
		d.mu.Unlock()
		return nil
	}
	if d.state == "stopping" {
		done := d.done
		d.mu.Unlock()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return fmt.Errorf("wait for ordered daemon shutdown: %w", ctx.Err())
		}
	}
	d.state = "stopping"
	d.mu.Unlock()
	var stopErr error
	if d.lifecycleCancel != nil {
		d.lifecycleCancel()
		done := make(chan struct{})
		go func() {
			d.lifecycleWG.Wait()
			close(done)
		}()
		wait := 3 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			if remaining := time.Until(deadline); remaining < wait {
				wait = remaining
			}
		}
		if wait <= 0 {
			wait = time.Millisecond
		}
		select {
		case <-done:
		case <-time.After(wait):
			stopErr = errors.Join(stopErr, errors.New("lifecycle reconciliation did not terminate during bounded shutdown"))
		}
	}
	if d.sessionListener != nil {
		stopErr = errors.Join(stopErr, d.sessionListener.Close())
	}
	if d.sessions != nil {
		drainTimeout := 5 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			drainTimeout = time.Until(deadline)
			if drainTimeout <= 0 {
				drainTimeout = time.Millisecond
			}
		}
		drainCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
		stopErr = errors.Join(stopErr, d.sessions.drain(drainCtx))
		cancel()
	}
	if d.bg != nil {
		// Ordered, fail-closed: in-flight background work finishes or is cancelled
		// and failed within a bound; stop never hangs on a stuck op.
		drainTimeout := 5 * time.Second
		if dl, ok := ctx.Deadline(); ok {
			if remaining := time.Until(dl); remaining > 0 {
				drainTimeout = remaining
			}
		}
		d.bg.drain(drainTimeout)
	}
	if d.migrationWorkers != nil {
		d.migrationWorkers.Stop(ctx)
	}
	if d.migrationSecrets != nil {
		d.migrationSecrets.Close()
	}
	if d.migrationCache != nil {
		d.migrationCache.Close()
	}
	if d.lifecycle != nil {
		stopErr = errors.Join(stopErr, d.lifecycle.Close())
	}
	if d.networkGateways != nil {
		stopErr = errors.Join(stopErr, d.networkGateways.Close())
	}
	if d.backendShutdown != nil {
		stopErr = errors.Join(stopErr, d.backendShutdown())
	}
	if d.tailStop != nil {
		close(d.tailStop)
	}
	if d.hostFSStop != nil {
		close(d.hostFSStop)
	}
	if d.credentialStop != nil {
		close(d.credentialStop)
		if d.credentialDone != nil {
			select {
			case <-d.credentialDone:
			case <-ctx.Done():
				stopErr = errors.Join(stopErr, ctx.Err())
			}
		}
	}
	if d.bus != nil {
		d.bus.closeAll()
	}
	if d.uiServer != nil {
		stopErr = errors.Join(stopErr, d.uiServer.Shutdown(ctx))
	}
	if d.server != nil {
		stopErr = errors.Join(stopErr, d.server.Shutdown(ctx))
	}
	if d.activityOwned && d.activityStore != nil {
		stopErr = errors.Join(stopErr, d.activityStore.Close())
	}
	if d.ln != nil {
		stopErr = errors.Join(stopErr, d.ln.Close())
	}
	d.audit.record("daemon.stop", "allow", map[string]any{"socket": d.socket})
	_ = d.audit.close()
	releaseLock(d.lockFile, filepath.Join(d.runtimeDir, lockName))

	d.mu.Lock()
	d.state = "stopped"
	d.mu.Unlock()
	close(d.done)
	return stopErr
}

// Done returns a channel closed when the daemon has fully stopped (for example
// after a /daemon/stop request), so a foreground `daemon start` can exit.
func (d *Daemon) Done() <-chan struct{} { return d.done }

func (d *Daemon) startHostFSWriteTimeoutWorker() {
	ticker := time.NewTicker(time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_, _ = d.api.Core.ExpireHostFSWriteTimeouts(time.Now().UTC())
			case <-d.hostFSStop:
				return
			}
		}
	}()
}

func (d *Daemon) startCredentialRotation() {
	go func() {
		defer close(d.credentialDone)
		var retryDelay time.Duration
		var retryAt time.Time
		for {
			nextAttempt := d.credentials.RotateAt()
			if !retryAt.IsZero() {
				nextAttempt = retryAt
			}
			wait := time.Until(nextAttempt)
			if wait > credentialRuntimeCheckInterval {
				wait = credentialRuntimeCheckInterval
			}
			if wait < time.Millisecond {
				wait = time.Millisecond
			}
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
				if !d.credentials.runtimeDirectoryAvailable() {
					d.audit.record("daemon.credential.rotate", "deny", map[string]any{
						"reason": "credential runtime directory unavailable",
					})
					d.stopAfterCredentialRuntimeLoss()
					return
				}
				if !retryAt.IsZero() && time.Now().Before(retryAt) {
					continue
				}
				if rotated, err := d.credentials.RotateIfDue(); err != nil {
					reason := "credential rotation failed"
					if errors.Is(err, errCredentialRuntimeUnavailable) {
						reason = "credential runtime directory unavailable"
					}
					d.audit.record("daemon.credential.rotate", "deny", map[string]any{"reason": reason})
					if errors.Is(err, errCredentialRuntimeUnavailable) {
						d.stopAfterCredentialRuntimeLoss()
						return
					}
					if retryDelay == 0 {
						retryDelay = credentialRotationRetryMin
					} else {
						retryDelay *= 2
						if retryDelay > credentialRotationRetryMax {
							retryDelay = credentialRotationRetryMax
						}
					}
					retryAt = time.Now().Add(retryDelay)
				} else if rotated {
					retryDelay = 0
					retryAt = time.Time{}
					d.audit.record("daemon.credential.rotate", "allow", map[string]any{"generation": d.credentials.Generation()})
				} else {
					retryDelay = 0
					retryAt = time.Time{}
				}
			case <-d.credentialStop:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			}
		}
	}()
}

// stopAfterCredentialRuntimeLoss leaves the rotation worker before Stop waits
// for credentialDone, avoiding a self-deadlock during ordered shutdown.
func (d *Daemon) stopAfterCredentialRuntimeLoss() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), credentialRuntimeShutdownTimeout)
		defer cancel()
		_ = d.Stop(ctx)
	}()
}
