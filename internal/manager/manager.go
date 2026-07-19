package manager

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/inittask"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/runtimecatalog"
	"github.com/vibe-agi/hideout/internal/runtimeverify"
	"github.com/vibe-agi/hideout/internal/session"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

type Core struct {
	Store        profile.Store
	Backends     []BackendCheck
	SecretEnv    []string
	CommandProxy cmdproxy.Registry
	// Observer, when set, receives operation-lifecycle notifications. It is nil in
	// embedded construction (New), so embedded mode emits nothing; the daemon sets
	// it to its event publisher.
	Observer EventObserver
	// LifecycleResources is set only by hideoutd. It lets API operations record
	// dynamic session resources in the daemon-owned lifecycle journal without
	// granting Manager or the journal any capability authority.
	LifecycleResources lifecycle.SessionResourceRegistrar
	// RuntimeResolver is a test seam. Production Core construction leaves it nil
	// and therefore resolves only the package-embedded catalog.
	RuntimeResolver            func(runtimecatalog.Selection) (runtimecatalog.Resolution, error)
	RuntimeDiskCheck           func(path string, requiredBytes int64) error
	RuntimeCatalogLoader       func() (runtimecatalog.Catalog, error)
	RuntimeInstanceInspector   func(context.Context, string, backend.RuntimeInstanceExpectation) (backend.RuntimeInstanceObservation, error)
	HostAppIdentityResolver    func(hostcap.ApplicationExpectation, []string) (hostcap.ObservedApplicationIdentity, error)
	HostAppIdentityRevalidator hostcap.IdentityRevalidator
	HostAppPlatform            hostcap.Platform
	// HostAppForbiddenRoots supplies active run/HostFS roots not represented by
	// persisted profile and environment state. Errors fail identity review closed.
	HostAppForbiddenRoots func(profileName string) ([]string, error)
	// ActiveWorkspaceViews is supplied only by hideoutd. It returns daemon-owned,
	// in-memory attachment observations; Manager copies only the explicitly public
	// fields below and never derives an active workspace from environment history.
	ActiveWorkspaceViews func() []WorkspaceViewSnapshot
	// NetworkGateways owns host-loopback, per-environment egress selectors. Core
	// keeps only the registry pointer; guest routing remains backend-controlled.
	NetworkGateways *network.GatewayRegistry
}

const EnvironmentSummarySchema = "hideout.environment-summary/v1"

// WorkspaceViewSnapshot is a daemon-to-Manager observation. The private fields
// support internal correlation and adversarial tests but are never serialized or
// copied into Overview.
type WorkspaceViewSnapshot struct {
	Attachment         workspaceattach.AttachmentSummary
	Profile            string
	Relations          []workspaceattach.RootRelationNotice
	CanonicalHostRoot  string `json:"-"`
	RootHandleIdentity string `json:"-"`
	ProviderCredential string `json:"-"`
}

type BackendCheck struct {
	Name      string
	Isolation string
	Check     func(context.Context) error
}

type Overview struct {
	Version        string               `json:"version"`
	StorageRoot    string               `json:"storageRoot"`
	Profiles       []ProfileSummary     `json:"profiles"`
	Sessions       []SessionSummary     `json:"sessions"`
	Environments   []EnvironmentSummary `json:"environments"`
	Backends       []BackendSummary     `json:"backends"`
	Capabilities   CapabilitySummary    `json:"capabilities"`
	Network        NetworkSummary       `json:"network"`
	Broker         BrokerSummary        `json:"broker"`
	Secrets        []SecretSummary      `json:"secrets"`
	Audit          AuditSummary         `json:"audit"`
	Settings       SettingsSummary      `json:"settings"`
	Init           InitSummary          `json:"init"`
	Bundles        BundleSummary        `json:"bundles"`
	Projects       ProjectSummary       `json:"projects"`
	AdapterPacks   []AdapterPackSummary `json:"adapterPacks,omitempty"`
	HostAppPacks   []HostAppPackSummary `json:"hostAppPacks,omitempty"`
	Decisions      DecisionSummary      `json:"decisions,omitempty"`
	Notices        NoticeSummary        `json:"notices,omitempty"`
	DecisionStatus DecisionStatus       `json:"decisionStatus,omitempty"`
}

type ProfileSummary struct {
	Name                       string                      `json:"name"`
	ProfileID                  string                      `json:"profileId,omitempty"`
	IdentityID                 string                      `json:"identityId,omitempty"`
	LineageMode                string                      `json:"lineageMode,omitempty"`
	NetworkMode                string                      `json:"networkMode"`
	ProxySecretRef             string                      `json:"proxySecretRef,omitempty"`
	ProxyEnvVisible            bool                        `json:"proxyEnvVisible"`
	EnvPublic                  []string                    `json:"envPublic,omitempty"`
	EnvInherit                 []string                    `json:"envInherit,omitempty"`
	EnvDeny                    []string                    `json:"envDeny,omitempty"`
	CommandProxies             []string                    `json:"commandProxies,omitempty"`
	CommandAdapters            []CommandAdapterSummary     `json:"commandAdapters,omitempty"`
	HostFSGrants               int                         `json:"hostfsGrants"`
	HostFSDeny                 int                         `json:"hostfsDeny"`
	HostFSVisibility           HostFSVisibilityPosture     `json:"hostfsVisibility"`
	PolicyEngine               string                      `json:"policyEngine"`
	ExpectedCommands           []string                    `json:"expectedCommands,omitempty"`
	ExpectedCommandDiagnostics []ExpectedCommandDiagnostic `json:"expectedCommandDiagnostics,omitempty"`
	ProfilePath                string                      `json:"profilePath"`
	IdentityPath               string                      `json:"identityPath"`
	ValidationError            string                      `json:"validationError,omitempty"`
	profile                    profile.Profile
}

type CommandAdapterSummary struct {
	ID                          string   `json:"id"`
	Enabled                     bool     `json:"enabled"`
	Digest                      string   `json:"digest,omitempty"`
	Commands                    []string `json:"commands,omitempty"`
	AllowedProposalCapabilities []string `json:"allowedProposalCapabilities,omitempty"`
	Builtin                     string   `json:"builtin,omitempty"`
	Description                 string   `json:"description,omitempty"`
}

type ExpectedCommandDiagnostic struct {
	Command            string `json:"command"`
	Status             string `json:"status"`
	Backend            string `json:"backend,omitempty"`
	Reason             string `json:"reason,omitempty"`
	BlocksRequestedRun bool   `json:"blocksRequestedRun,omitempty"`
}

type ExpectedCommandCheckContext struct {
	Backend          string
	Checkable        bool
	PresentCommands  map[string]bool
	BlockedReason    string
	RequestedCommand string
}

const (
	ExpectedCommandPresent      = "present"
	ExpectedCommandMissing      = "missing"
	ExpectedCommandNotCheckable = "not-checkable"
	ExpectedCommandBlocked      = "blocked"
)

type SessionSummary struct {
	ID                     string                               `json:"id"`
	Profile                string                               `json:"profile,omitempty"`
	Path                   string                               `json:"path"`
	AuditPath              string                               `json:"auditPath,omitempty"`
	HasAudit               bool                                 `json:"hasAudit"`
	HasBrokerEndpoint      bool                                 `json:"hasBrokerEndpoint"`
	HasNetworkPlan         bool                                 `json:"hasNetworkPlan"`
	HasProxySecretFile     bool                                 `json:"hasProxySecretFile"`
	HasEphemeralState      bool                                 `json:"hasEphemeralState"`
	NetworkMode            string                               `json:"networkMode,omitempty"`
	GuestPrivilege         *BoundaryPrivilegeSummary            `json:"guestPrivilege,omitempty"`
	EnvironmentID          string                               `json:"environmentId,omitempty"`
	State                  session.OwnerState                   `json:"state,omitempty"`
	OwnerStatus            session.OwnerStatus                  `json:"ownerStatus,omitempty"`
	TerminalMode           session.TerminalMode                 `json:"terminalMode,omitempty"`
	StartedAt              time.Time                            `json:"startedAt,omitempty"`
	UpdatedAt              time.Time                            `json:"updatedAt,omitempty"`
	CommandClass           string                               `json:"commandClass,omitempty"`
	CleanupError           string                               `json:"cleanupError,omitempty"`
	WorkspaceID            string                               `json:"workspaceId,omitempty"`
	SessionSnapshotID      string                               `json:"sessionSnapshotId,omitempty"`
	WorkspaceLabel         string                               `json:"workspaceLabel,omitempty"`
	GuestWorkspace         string                               `json:"guestWorkspace,omitempty"`
	WorkspaceTransport     string                               `json:"workspaceTransport,omitempty"`
	WorkspaceViewState     workspaceattach.AttachmentState      `json:"workspaceViewState,omitempty"`
	WorkspaceRelations     []workspaceattach.RootRelationNotice `json:"workspaceRelations,omitempty"`
	WorkspaceCleanupStatus string                               `json:"workspaceCleanupStatus,omitempty"`
	WorkspaceBlockerCode   string                               `json:"workspaceBlockerCode,omitempty"`
}

type EnvironmentSummary struct {
	Schema                 string                    `json:"schema"`
	ID                     string                    `json:"id"`
	Name                   string                    `json:"name,omitempty"`
	AutoNamed              bool                      `json:"autoNamed,omitempty"`
	ImageRef               string                    `json:"imageRef,omitempty"`
	Profile                string                    `json:"profile"`
	Backend                string                    `json:"backend"`
	Status                 string                    `json:"status"`
	Mode                   environment.Mode          `json:"mode"`
	SharedSlot             string                    `json:"sharedSlot,omitempty"`
	MachineIdentityID      string                    `json:"machineIdentityId"`
	BootConfigurationID    string                    `json:"bootConfigurationId"`
	ServiceConfigurationID string                    `json:"serviceConfigurationId,omitempty"`
	ServiceFingerprint     string                    `json:"serviceFingerprint,omitempty"`
	ServiceMode            string                    `json:"serviceMode,omitempty"`
	ServiceStatus          string                    `json:"serviceStatus,omitempty"`
	RecordVersion          string                    `json:"recordVersion,omitempty"`
	Workspace              string                    `json:"workspace,omitempty"`
	GuestWorkspace         string                    `json:"guestWorkspace,omitempty"`
	WorkspaceLabel         string                    `json:"workspaceLabel,omitempty"`
	InstanceName           string                    `json:"instanceName,omitempty"`
	LastSessionID          string                    `json:"lastSessionId,omitempty"`
	LastCommand            string                    `json:"lastCommand,omitempty"`
	CreatedAt              time.Time                 `json:"createdAt"`
	LastStartedAt          *time.Time                `json:"lastStartedAt,omitempty"`
	LastEndedAt            *time.Time                `json:"lastEndedAt,omitempty"`
	Runtime                *runtimeverify.StatusView `json:"runtime,omitempty"`
	ActiveSessions         int                       `json:"activeSessions"`
	ActiveWorkspaceViews   int                       `json:"activeWorkspaceViews"`
	WorkspaceProviderState string                    `json:"workspaceProviderState,omitempty"`
	OwnerHealth            string                    `json:"ownerHealth,omitempty"`
}

type BackendSummary struct {
	Name      string `json:"name"`
	Isolation string `json:"isolation"`
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
}

type CapabilitySummary struct {
	MaxCapabilities []string                `json:"maxCapabilities"`
	CommandProxies  []cmdproxy.Registration `json:"commandProxies"`
	HostOpen        HostOpenSummary         `json:"hostOpen"`
}

type HostOpenSummary struct {
	Mode                string `json:"mode"`
	AllowURLs           bool   `json:"allowUrls"`
	URLScope            string `json:"urlScope"`
	LocalNetworkPolicy  string `json:"localNetworkPolicy"`
	AllowWorkspaceFiles bool   `json:"allowWorkspaceFiles"`
	BrowserProfile      string `json:"browserProfile"`
	BrowserControl      string `json:"browserControl"`
	Profiles            int    `json:"profiles"`
}

type NetworkSummary struct {
	ProfileDefaults []ProfileNetworkSummary `json:"profileDefaults"`
}

type ProfileNetworkSummary struct {
	Profile         string `json:"profile"`
	Mode            string `json:"mode"`
	ProxySecretRef  string `json:"proxySecretRef,omitempty"`
	ProxyEnvVisible bool   `json:"proxyEnvVisible"`
}

type BrokerSummary struct {
	Actions        []string `json:"actions"`
	CommandProxies []string `json:"commandProxies"`
}

type SecretSummary struct {
	Ref       string `json:"ref"`
	Source    string `json:"source"`
	Available bool   `json:"available"`
}

type AuditSummary struct {
	SessionAuditFiles int `json:"sessionAuditFiles"`
}

type SettingsSummary struct {
	StoreRoot string `json:"storeRoot"`
}

type InitSummary struct {
	Version      string              `json:"version"`
	Initialized  bool                `json:"initialized"`
	PendingTasks int                 `json:"pendingTasks"`
	Profile      string              `json:"profile"`
	StatePath    string              `json:"statePath"`
	AuditPath    string              `json:"auditPath"`
	AuditEvents  int                 `json:"auditEvents"`
	NextSteps    []inittask.NextStep `json:"nextSteps,omitempty"`
}

type BundleSummary struct {
	Status    string `json:"status"`
	Installed int    `json:"installed"`
	Enabled   int    `json:"enabled"`
}

type ProjectSummary struct {
	Status             string `json:"status"`
	HideoutfilePresent bool   `json:"hideoutfilePresent"`
	LockPresent        bool   `json:"lockPresent"`
}

type DecisionSummary struct {
	Pending  int `json:"pending"`
	Claimed  int `json:"claimed"`
	Terminal int `json:"terminal"`
}

type NoticeSummary struct {
	Unacknowledged int `json:"unacknowledged"`
	Total          int `json:"total"`
}

type AuditEventFilter struct {
	Session  string
	Profile  string
	Action   string
	Decision string
	Limit    int
}

func New(store profile.Store) Core {
	return Core{
		Store:           store,
		NetworkGateways: network.NewGatewayRegistry(),
	}
}

func (c Core) PlanInit(opts inittask.Options) (inittask.Plan, error) {
	return inittask.PlanMachine(c.Store, opts)
}

func (c Core) ApplyInit(plan inittask.Plan, opts inittask.ApplyOptions) (inittask.Result, error) {
	if opts.Operation == "" {
		opts.Operation = inittask.OperationInitApply
	}
	return inittask.ApplyMachine(c.Store, plan, opts)
}

func (c Core) PlanDoctorFix(opts inittask.Options) (inittask.Plan, error) {
	return c.PlanInit(opts)
}

func (c Core) ApplyDoctorFix(plan inittask.Plan, opts inittask.ApplyOptions) (inittask.Result, error) {
	if opts.Operation == "" {
		opts.Operation = inittask.OperationDoctorFixApply
	}
	return c.ApplyInit(plan, opts)
}

func (c Core) EnsureRunInitialized(plan RunPlan) (inittask.Result, error) {
	networkMode, err := c.runInitNetworkMode(plan)
	if err != nil {
		return inittask.Result{}, err
	}
	proxySecretRef := ""
	mediatedResolver := ""
	if networkMode == "tun2socks" {
		proxySecretRef = plan.RuntimeProfile.Network.ProxySecretRef
		mediatedResolver = plan.RuntimeProfile.Network.MediatedResolver
	}
	initPlan, err := c.PlanInit(inittask.Options{
		ProfileName:      plan.ProfileName,
		Backend:          plan.Backend,
		Network:          networkMode,
		ProxySecretRef:   proxySecretRef,
		MediatedResolver: mediatedResolver,
		NoInput:          true,
	})
	if err != nil {
		return inittask.Result{}, err
	}
	initPlan.Tasks = runAutoInitTasks(initPlan.Tasks)
	if !initPlanHasPendingTasks(initPlan) {
		return inittask.Result{Version: inittask.Version, Plan: initPlan}, nil
	}
	return c.ApplyInit(initPlan, inittask.ApplyOptions{
		NoInput:   true,
		Operation: inittask.OperationRunInitApply,
	})
}

func (c Core) runInitNetworkMode(plan RunPlan) (string, error) {
	p, err := c.Store.Load(plan.ProfileName)
	if err != nil {
		return "", err
	}
	if p.Network.Mode == "" {
		return "direct", nil
	}
	return p.Network.Mode, nil
}

func runAutoInitTasks(tasks []inittask.Task) []inittask.Task {
	out := make([]inittask.Task, 0, len(tasks))
	for _, task := range tasks {
		switch task.Kind {
		case "helper.install.linux-shim", "helper.install.linux-hostfsd":
			continue
		default:
			out = append(out, task)
		}
	}
	return out
}

func initPlanHasPendingTasks(plan inittask.Plan) bool {
	for _, task := range plan.Tasks {
		if task.Status == "pending" {
			return true
		}
	}
	return false
}

func (c Core) Overview(ctx context.Context) (Overview, error) {
	if c.Store.Root == "" {
		return Overview{}, errors.New("manager store root is required")
	}
	profiles, profileErrors := c.profileSummaries()
	sessions, auditCount := c.sessionSummaries()
	active, activeErr := c.ActiveSessionSummaries()
	sessions = mergeActiveSessionSummaries(sessions, active)
	workspaceViews := c.activeWorkspaceViewSnapshots()
	sessions = mergeWorkspaceViewSummaries(sessions, workspaceViews)
	environments := environmentSummaries(c.Store.Root)
	environments = annotateEnvironmentOwners(environments, active)
	environments = annotateEnvironmentWorkspaceViews(environments, workspaceViews)
	registry := c.commandProxy(profiles)
	capabilities := capabilitySummary(profiles, registry)
	decisionStatus, _ := c.DecisionStatus()
	notices, _ := c.ListNotices(NoticeListRequest{})
	hostAppPacks, _ := c.ListHostAppPacks()
	return Overview{
		Version:      "hideout.manager/v1",
		StorageRoot:  c.Store.Root,
		Profiles:     profiles,
		Sessions:     sessions,
		Environments: environments,
		Backends:     c.backendSummaries(ctx),
		Capabilities: capabilities,
		Network:      networkSummary(profiles),
		Broker: BrokerSummary{
			Actions:        []string{"host.open"},
			CommandProxies: registry.ShimNames(),
		},
		Secrets:      secretSummaries(profiles, c.SecretEnv),
		Audit:        AuditSummary{SessionAuditFiles: auditCount},
		Settings:     SettingsSummary{StoreRoot: c.Store.Root},
		Init:         initSummary(c.Store),
		Bundles:      bundleSummary(c.Store.Root),
		AdapterPacks: adapterPackSummaries(c),
		HostAppPacks: hostAppPacks,
		Projects: ProjectSummary{
			Status: "not-configured",
		},
		Decisions: DecisionSummary{
			Pending:  decisionStatus.PendingDecisions,
			Claimed:  decisionStatus.ClaimedDecisions,
			Terminal: decisionStatus.TerminalDecisions,
		},
		Notices: NoticeSummary{
			Unacknowledged: decisionStatus.UnackedNotices,
			Total:          len(notices),
		},
		DecisionStatus: decisionStatus,
	}, errors.Join(append(profileErrors, activeErr)...)
}

func (c Core) activeWorkspaceViewSnapshots() []WorkspaceViewSnapshot {
	if c.ActiveWorkspaceViews == nil {
		return nil
	}
	observed := c.ActiveWorkspaceViews()
	out := make([]WorkspaceViewSnapshot, 0, len(observed))
	for _, value := range observed {
		summary := value.Attachment
		if summary.Schema != workspaceattach.AttachmentSummarySchema || summary.SessionID == "" ||
			summary.EnvironmentID == "" || summary.WorkspaceID == "" || summary.LogicalGuestRoot != workspaceattach.LogicalWorkspaceRoot ||
			summary.Transport != workspaceattach.SelectedTransport {
			continue
		}
		copyValue := WorkspaceViewSnapshot{Attachment: summary, Profile: value.Profile}
		for _, relation := range value.Relations {
			if relation.WorkspaceID == summary.WorkspaceID && relation.OtherWorkspaceID != "" {
				copyValue.Relations = append(copyValue.Relations, relation)
			}
		}
		out = append(out, copyValue)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Attachment.SessionID < out[j].Attachment.SessionID
	})
	return out
}

// ActiveSessionSummaries is the single public owner-summary builder. It never
// exposes lock paths, PIDs, raw workspace paths, or control-plane material.
func (c Core) ActiveSessionSummaries() ([]session.ActiveSessionSummary, error) {
	store := environment.Store{Root: c.Store.Root}
	records, err := store.List()
	if err != nil {
		return nil, err
	}
	out := make([]session.ActiveSessionSummary, 0)
	for _, environmentRecord := range records {
		if environmentRecord.Status == environment.StatusUnsupportedVersion {
			continue
		}
		observed, err := session.ListOwners(store.OwnerRoot(environmentRecord.ID))
		if err != nil {
			return nil, err
		}
		for _, owner := range observed {
			record := owner.Record
			if record.Schema != session.ActiveSessionSchema {
				startedAt := environmentRecord.CreatedAt
				if startedAt.IsZero() {
					startedAt = time.Unix(0, 0).UTC()
				}
				record = session.OwnerRecord{
					Schema:        session.ActiveSessionSchema,
					SessionID:     owner.SessionID,
					EnvironmentID: environmentRecord.ID,
					Profile:       environmentRecord.Profile,
					Backend:       environmentRecord.Backend,
					WorkspaceID:   "",
					State:         session.OwnerStateFailed,
					TerminalMode:  session.TerminalNone,
					StartedAt:     startedAt,
					UpdatedAt:     startedAt,
					CleanupError:  "session ownership metadata is unprovable; run hideout doctor --feature sessions",
				}
			}
			out = append(out, record.Summary(owner.Status))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func mergeActiveSessionSummaries(existing []SessionSummary, active []session.ActiveSessionSummary) []SessionSummary {
	byID := make(map[string]int, len(existing))
	for i := range existing {
		byID[existing[i].ID] = i
	}
	for _, item := range active {
		index, ok := byID[item.ID]
		if !ok {
			existing = append(existing, SessionSummary{ID: item.ID})
			index = len(existing) - 1
			byID[item.ID] = index
		}
		summary := &existing[index]
		summary.Profile = item.Profile
		summary.EnvironmentID = item.EnvironmentID
		summary.State = item.State
		summary.OwnerStatus = item.OwnerStatus
		summary.TerminalMode = item.TerminalMode
		summary.StartedAt = item.StartedAt
		summary.UpdatedAt = item.UpdatedAt
		summary.CommandClass = item.CommandClass
		summary.CleanupError = item.CleanupError
		summary.SessionSnapshotID = item.SessionSnapshotID
		// Raw implementation paths are not part of an active owner view.
		summary.Path = ""
	}
	sort.Slice(existing, func(i, j int) bool { return existing[i].ID < existing[j].ID })
	return existing
}

func mergeWorkspaceViewSummaries(existing []SessionSummary, views []WorkspaceViewSnapshot) []SessionSummary {
	byID := make(map[string]int, len(existing))
	for i := range existing {
		byID[existing[i].ID] = i
	}
	for _, view := range views {
		attachment := view.Attachment
		index, ok := byID[attachment.SessionID]
		if !ok {
			existing = append(existing, SessionSummary{ID: attachment.SessionID})
			index = len(existing) - 1
			byID[attachment.SessionID] = index
		}
		summary := &existing[index]
		if summary.EnvironmentID != "" && summary.EnvironmentID != attachment.EnvironmentID {
			continue
		}
		if summary.WorkspaceID != "" && summary.WorkspaceID != attachment.WorkspaceID {
			continue
		}
		summary.EnvironmentID = attachment.EnvironmentID
		summary.WorkspaceID = attachment.WorkspaceID
		summary.WorkspaceLabel = attachment.DisplayLabel
		summary.GuestWorkspace = attachment.LogicalGuestRoot
		summary.WorkspaceTransport = attachment.Transport
		summary.WorkspaceViewState = attachment.State
		summary.WorkspaceRelations = append([]workspaceattach.RootRelationNotice(nil), view.Relations...)
		if attachment.CleanupProof != nil {
			summary.WorkspaceCleanupStatus = attachment.CleanupProof.Status
			summary.WorkspaceBlockerCode = attachment.CleanupProof.ReasonCode
		}
		// Active workspace rows never expose the implementation session directory.
		summary.Path = ""
	}
	sort.Slice(existing, func(i, j int) bool { return existing[i].ID < existing[j].ID })
	return existing
}

func annotateEnvironmentOwners(environments []EnvironmentSummary, active []session.ActiveSessionSummary) []EnvironmentSummary {
	for i := range environments {
		health := ""
		for _, owner := range active {
			if owner.EnvironmentID != environments[i].ID {
				continue
			}
			switch owner.OwnerStatus {
			case session.OwnerLive:
				environments[i].ActiveSessions++
				if health == "" {
					health = "live"
				}
			case session.OwnerUnprovable:
				health = "unprovable"
			case session.OwnerStale:
				if health == "" {
					health = "stale"
				}
			}
		}
		environments[i].OwnerHealth = health
	}
	return environments
}

func annotateEnvironmentWorkspaceViews(environments []EnvironmentSummary, views []WorkspaceViewSnapshot) []EnvironmentSummary {
	for i := range environments {
		providerState := ""
		for _, view := range views {
			if view.Attachment.EnvironmentID != environments[i].ID {
				continue
			}
			environments[i].ActiveWorkspaceViews++
			providerState = mergeWorkspaceProviderState(providerState, view.Attachment.State)
		}
		environments[i].WorkspaceProviderState = providerState
	}
	return environments
}

func mergeWorkspaceProviderState(current string, state workspaceattach.AttachmentState) string {
	next := "not-started"
	switch state {
	case workspaceattach.AttachmentProviderStarting:
		next = "starting"
	case workspaceattach.AttachmentProviderReady, workspaceattach.AttachmentViewMounting, workspaceattach.AttachmentReady:
		next = "ready"
	case workspaceattach.AttachmentDraining:
		next = "draining"
	case workspaceattach.AttachmentReleased:
		next = "released"
	case workspaceattach.AttachmentUnproved:
		next = "unproved"
	}
	rank := map[string]int{"": 0, "not-started": 1, "released": 2, "starting": 3, "ready": 4, "draining": 5, "unproved": 6}
	if rank[next] > rank[current] {
		return next
	}
	return current
}

func adapterPackSummaries(c Core) []AdapterPackSummary {
	packs, err := c.ListAdapterPacks()
	if err != nil {
		return []AdapterPackSummary{}
	}
	return packs
}

func (c Core) AuditEvents(filter AuditEventFilter) ([]audit.Event, error) {
	groups, err := c.AuditEventGroups(filter)
	if len(groups) == 0 {
		return []audit.Event{}, err
	}
	return groups[0], err
}

func (c Core) AuditEventGroups(filters ...AuditEventFilter) ([][]audit.Event, error) {
	if c.Store.Root == "" {
		return nil, errors.New("manager store root is required")
	}
	if len(filters) == 0 {
		return [][]audit.Event{}, nil
	}
	limits := make([]int, len(filters))
	for i, filter := range filters {
		if filter.Session != "" && !session.ValidID(filter.Session) {
			return nil, errors.New("invalid session id")
		}
		limits[i] = filter.Limit
		if limits[i] <= 0 {
			limits[i] = 200
		}
	}
	sessionsDir := filepath.Join(c.Store.Root, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyAuditEventGroups(len(filters)), nil
		}
		return nil, err
	}
	groups := emptyAuditEventGroups(len(filters))
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		if !session.ValidID(sessionID) {
			continue
		}
		if !sessionMatchesAnyFilter(sessionID, filters) {
			continue
		}
		events, err := readAuditEventGroups(filepath.Join(sessionsDir, sessionID, "audit.jsonl"), filters)
		if err != nil {
			errs = append(errs, err)
		}
		for i := range groups {
			groups[i] = append(groups[i], events[i]...)
		}
	}
	for i := range groups {
		sortAuditEvents(groups[i])
		if len(groups[i]) > limits[i] {
			groups[i] = groups[i][:limits[i]]
		}
	}
	return groups, errors.Join(errs...)
}

func emptyAuditEventGroups(count int) [][]audit.Event {
	groups := make([][]audit.Event, count)
	for i := range groups {
		groups[i] = []audit.Event{}
	}
	return groups
}

func sessionMatchesAnyFilter(sessionID string, filters []AuditEventFilter) bool {
	for _, filter := range filters {
		if filter.Session == "" || filter.Session == sessionID {
			return true
		}
	}
	return false
}

func sortAuditEvents(events []audit.Event) {
	sort.SliceStable(events, func(i, j int) bool {
		left, right := events[i], events[j]
		if !left.Time.Equal(right.Time) {
			if left.Time.IsZero() {
				return false
			}
			if right.Time.IsZero() {
				return true
			}
			return left.Time.After(right.Time)
		}
		if left.Session != right.Session {
			return left.Session > right.Session
		}
		return left.Action > right.Action
	})
}

func environmentSummaries(storeRoot string) []EnvironmentSummary {
	environmentStore := environment.Store{Root: storeRoot}
	records, err := environmentStore.List()
	if err != nil {
		return nil
	}
	out := make([]EnvironmentSummary, 0, len(records))
	for _, rec := range records {
		var receipt *runtimeverify.Receipt
		if loaded, loadErr := (runtimeverify.Store{Root: storeRoot}).Load(rec.ID); loadErr == nil {
			receipt = &loaded
		}
		runtimeStatus := runtimeverify.BuildStatus(rec, runtimeEnvironmentRunning(rec), receipt)
		summary := EnvironmentSummary{
			Schema:              EnvironmentSummarySchema,
			ID:                  rec.ID,
			Name:                rec.Name,
			AutoNamed:           rec.AutoNamed,
			ImageRef:            rec.ImageRef,
			Profile:             rec.Profile,
			Backend:             rec.Backend,
			Status:              rec.Status,
			Mode:                rec.Mode,
			SharedSlot:          rec.SharedSlot,
			MachineIdentityID:   rec.MachineIdentityID,
			BootConfigurationID: rec.BootConfigurationID,
			RecordVersion:       rec.Version,
			InstanceName:        rec.InstanceName,
			CreatedAt:           rec.CreatedAt,
			LastStartedAt:       optionalTime(rec.LastStartedAt),
			LastEndedAt:         optionalTime(rec.LastEndedAt),
			Runtime:             &runtimeStatus,
		}
		serviceStatePath := filepath.Join(environmentStore.RuntimeNetworkServiceDir(rec.ID), "state.json")
		if serviceState, serviceErr := network.LoadServiceState(serviceStatePath); serviceErr == nil {
			summary.ServiceConfigurationID = serviceState.ConfigurationID
			summary.ServiceFingerprint = serviceState.ConfigurationFingerprint
			summary.ServiceMode = serviceState.Mode
			summary.ServiceStatus = string(serviceState.Status)
		} else if !errors.Is(serviceErr, os.ErrNotExist) {
			summary.ServiceStatus = "unprovable"
		}
		if binding, ok := pinnedEnvironmentWorkspace(rec); ok {
			summary.Workspace = binding.HostRoot
			summary.GuestWorkspace = binding.GuestRoot
			summary.WorkspaceLabel = workspaceSummaryLabel(binding.HostRoot, rec.ID)
			summary.LastSessionID = rec.LastSessionID
			summary.LastCommand = rec.LastCommand
		}
		out = append(out, summary)
	}
	return out
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

func workspaceSummaryLabel(hostRoot, environmentID string) string {
	name := filepath.Base(filepath.Clean(hostRoot))
	if name == "." || name == string(filepath.Separator) || strings.TrimSpace(name) == "" {
		name = "workspace"
	}
	shortID := strings.TrimPrefix(environmentID, "env_")
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return name + " [" + shortID + "]"
}

// ScopeOverview is the single machine/view profile filter used by CLI, TUI,
// WebUI seeds, and tests. Labels and counts never select authority.
func ScopeOverview(in Overview, profileName string) Overview {
	if profileName == "" {
		return in
	}
	profiles := make([]ProfileSummary, 0, len(in.Profiles))
	for _, value := range in.Profiles {
		if value.Name == profileName {
			profiles = append(profiles, value)
		}
	}
	environments := make([]EnvironmentSummary, 0, len(in.Environments))
	for _, value := range in.Environments {
		if value.Profile == profileName {
			environments = append(environments, value)
		}
	}
	sessions := make([]SessionSummary, 0, len(in.Sessions))
	for _, value := range in.Sessions {
		if value.Profile == profileName {
			sessions = append(sessions, value)
		}
	}
	network := make([]ProfileNetworkSummary, 0, len(in.Network.ProfileDefaults))
	for _, value := range in.Network.ProfileDefaults {
		if value.Profile == profileName {
			network = append(network, value)
		}
	}
	in.Profiles = profiles
	in.Environments = environments
	in.Sessions = sessions
	in.Network.ProfileDefaults = network
	return in
}

func readAuditEvents(path string, filter AuditEventFilter) ([]audit.Event, error) {
	groups, err := readAuditEventGroups(path, []AuditEventFilter{filter})
	if len(groups) == 0 {
		return nil, err
	}
	return groups[0], err
}

func readAuditEventGroups(path string, filters []AuditEventFilter) ([][]audit.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyAuditEventGroups(len(filters)), nil
		}
		return nil, err
	}
	defer f.Close()
	out := emptyAuditEventGroups(len(filters))
	var errs []error
	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		line++
		var event audit.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			errs = append(errs, errors.New(filepath.Base(path)+":"+strconv.Itoa(line)+": "+err.Error()))
			continue
		}
		event.Details = audit.RedactDetails(event.Details)
		for i, filter := range filters {
			if !matchesAuditFilter(event, filter) {
				continue
			}
			out[i] = append(out[i], event)
		}
	}
	if err := scanner.Err(); err != nil {
		errs = append(errs, err)
	}
	return out, errors.Join(errs...)
}

func matchesAuditFilter(event audit.Event, filter AuditEventFilter) bool {
	if filter.Session != "" && event.Session != filter.Session {
		return false
	}
	if filter.Profile != "" && event.Profile != filter.Profile {
		return false
	}
	if filter.Action != "" && event.Action != filter.Action {
		return false
	}
	if filter.Decision != "" && event.Decision != filter.Decision {
		return false
	}
	return true
}

func (c Core) commandProxy(profiles []ProfileSummary) cmdproxy.Registry {
	if len(c.CommandProxy.ShimNames()) == 0 {
		return profileCommandProxyRegistry(profiles)
	}
	return c.CommandProxy
}

func (c Core) profileSummaries() ([]ProfileSummary, []error) {
	profilesDir := filepath.Join(c.Store.Root, "profiles")
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []ProfileSummary{}, nil
		}
		return nil, []error{err}
	}
	out := make([]ProfileSummary, 0, len(entries))
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		p, err := c.Store.Load(name)
		summary := ProfileSummary{
			Name:         name,
			ProfilePath:  c.Store.ProfilePath(name),
			IdentityPath: filepath.Join(c.Store.ProfileDir(name), "identity.json"),
		}
		if err != nil {
			summary.ValidationError = err.Error()
			errs = append(errs, err)
		} else {
			summary.profile = p
			summary.ProfileID = p.Metadata["profileId"]
			summary.IdentityID = p.Metadata["identityId"]
			summary.LineageMode = p.Metadata["lineageMode"]
			summary.NetworkMode = p.Network.Mode
			summary.ProxySecretRef = p.Network.ProxySecretRef
			summary.ProxyEnvVisible = p.Network.ProxyEnvVisible
			summary.EnvPublic = sortedProfileEnvPublicKeys(p.Env.Public)
			summary.EnvInherit = sortedStringsForManager(p.Env.Inherit)
			summary.EnvDeny = sortedStringsForManager(p.Env.Deny)
			summary.PolicyEngine = p.Policy.Engine
			summary.ExpectedCommands = sortedStringsForManager(p.Tools.ExpectedCommands)
			summary.ExpectedCommandDiagnostics = BuildExpectedCommandDiagnostics(summary.ExpectedCommands, ExpectedCommandCheckContext{
				Backend:   "profile",
				Checkable: false,
			})
			summary.HostFSGrants = len(p.HostFS.Grants)
			summary.HostFSDeny = len(p.HostFS.Deny)
			policy, policyErr := hostfs.Build(hostfs.BuildInput{Profile: p.HostFS, StoreRoot: c.Store.Root})
			if policyErr == nil {
				summary.HostFSVisibility = summarizeHostFSVisibility(p, policy)
			}
			summary.CommandProxies = commandProxyNames(p)
			summary.CommandAdapters = commandAdapterSummaries(p)
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, errs
}

func commandAdapterSummaries(p profile.Profile) []CommandAdapterSummary {
	ids := make([]string, 0, len(p.CommandAdapters.Adapters))
	for id := range p.CommandAdapters.Adapters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]CommandAdapterSummary, 0, len(ids))
	for _, id := range ids {
		adapter := p.CommandAdapters.Adapters[id]
		commands := append([]string(nil), adapter.Commands...)
		sort.Strings(commands)
		capabilities := append([]string(nil), adapter.AllowedProposalCapabilities...)
		sort.Strings(capabilities)
		out = append(out, CommandAdapterSummary{
			ID:                          id,
			Enabled:                     adapter.Enabled,
			Digest:                      adapter.Digest,
			Commands:                    commands,
			AllowedProposalCapabilities: capabilities,
			Builtin:                     adapter.Builtin,
			Description:                 adapter.Description,
		})
	}
	return out
}

func BuildExpectedCommandDiagnostics(expected []string, check ExpectedCommandCheckContext) []ExpectedCommandDiagnostic {
	commands := sortedStringsForManager(expected)
	out := make([]ExpectedCommandDiagnostic, 0, len(commands))
	for _, command := range commands {
		diagnostic := ExpectedCommandDiagnostic{
			Command: command,
			Backend: check.Backend,
		}
		switch {
		case strings.TrimSpace(check.BlockedReason) != "":
			diagnostic.Status = ExpectedCommandBlocked
			diagnostic.Reason = check.BlockedReason
			diagnostic.BlocksRequestedRun = command == check.RequestedCommand
		case !check.Checkable:
			diagnostic.Status = ExpectedCommandNotCheckable
			diagnostic.Reason = "selected environment is not inspectable in this context"
			diagnostic.BlocksRequestedRun = command == check.RequestedCommand
		case check.PresentCommands[command]:
			diagnostic.Status = ExpectedCommandPresent
			diagnostic.Reason = "command observed in selected environment"
		default:
			diagnostic.Status = ExpectedCommandMissing
			diagnostic.Reason = "command not observed in selected environment"
			diagnostic.BlocksRequestedRun = command == check.RequestedCommand
		}
		out = append(out, diagnostic)
	}
	return out
}

func (c Core) sessionSummaries() ([]SessionSummary, int) {
	sessionsDir := filepath.Join(c.Store.Root, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return []SessionSummary{}, 0
	}
	out := make([]SessionSummary, 0, len(entries))
	auditCount := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		if !session.ValidID(id) {
			continue
		}
		dir := filepath.Join(sessionsDir, id)
		summary := SessionSummary{
			ID:                 id,
			Path:               dir,
			AuditPath:          filepath.Join(dir, "audit.jsonl"),
			HasAudit:           exists(filepath.Join(dir, "audit.jsonl")),
			HasBrokerEndpoint:  exists(filepath.Join(dir, "broker-endpoint.json")),
			HasNetworkPlan:     exists(filepath.Join(dir, "network-plan.json")),
			HasProxySecretFile: exists(filepath.Join(dir, "network", "proxy.url")),
			HasEphemeralState:  exists(filepath.Join(dir, "tmp")) || exists(filepath.Join(dir, "shims")) || exists(filepath.Join(dir, "network")),
		}
		if summary.HasAudit {
			auditCount++
			summary.Profile = readSessionAuditProfile(summary.AuditPath)
			summary.GuestPrivilege = readSessionPrivilegeSummary(summary.AuditPath)
		}
		if mode := readNetworkMode(filepath.Join(dir, "network-plan.json")); mode != "" {
			summary.NetworkMode = mode
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, auditCount
}

func readSessionAuditProfile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 16*1024), 256*1024)
	for scanner.Scan() {
		var event audit.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.Profile != "" {
			return event.Profile
		}
	}
	return ""
}

func readSessionPrivilegeSummary(path string) *BoundaryPrivilegeSummary {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 16*1024), 256*1024)
	var latest *BoundaryPrivilegeSummary
	for scanner.Scan() {
		var event audit.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.Action == "guest.privilege.status" {
			latest = boundaryPrivilege(event.Details)
		}
	}
	return latest
}

func (c Core) backendSummaries(ctx context.Context) []BackendSummary {
	checks := c.Backends
	if len(checks) == 0 {
		checks = []BackendCheck{
			{
				Name:      "native",
				Isolation: "weak",
				Check:     func(context.Context) error { return nil },
			},
			{
				Name:      "lima",
				Isolation: "vm",
				Check:     (lima.Backend{}).Available,
			},
		}
	}
	out := make([]BackendSummary, 0, len(checks))
	for _, check := range checks {
		status := BackendSummary{Name: check.Name, Isolation: check.Isolation, Available: true}
		if check.Check != nil {
			if err := check.Check(ctx); err != nil {
				status.Available = false
				status.Error = err.Error()
			}
		}
		out = append(out, status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func profileCommandProxyRegistry(profiles []ProfileSummary) cmdproxy.Registry {
	names := map[string]bool{}
	for _, p := range profiles {
		if p.ValidationError != "" {
			continue
		}
		for _, name := range p.CommandProxies {
			names[name] = true
		}
	}
	if !names["open"] {
		return cmdproxy.DefaultRegistry()
	}
	registrations := make([]cmdproxy.Registration, 0, len(names))
	for name := range names {
		registrations = append(registrations, cmdproxy.Registration{
			Name:           name,
			Action:         cmdproxy.ActionHostOpen,
			ArgvSchema:     cmdproxy.ArgvSchemaOpenV1,
			StreamPolicy:   cmdproxy.StreamMetadataOnly,
			DefaultMode:    cmdproxy.DefaultModeAllow,
			AllowedTargets: []string{"url:http", "url:https", "workspace-file"},
		})
	}
	sort.Slice(registrations, func(i, j int) bool { return registrations[i].Name < registrations[j].Name })
	registry, err := cmdproxy.NewRegistry(registrations)
	if err != nil {
		return cmdproxy.DefaultRegistry()
	}
	return registry
}

func capabilitySummary(profiles []ProfileSummary, registry cmdproxy.Registry) CapabilitySummary {
	caps := map[string]bool{}
	hostOpen := HostOpenSummary{
		Mode:                "brokered",
		AllowURLs:           false,
		URLScope:            "external-http-https-only",
		LocalNetworkPolicy:  "deny-host-local-private-cgnat-benchmark-link-local-multicast",
		AllowWorkspaceFiles: false,
		BrowserProfile:      "isolated",
		BrowserControl:      "none",
	}
	for _, p := range profiles {
		if p.ValidationError != "" {
			continue
		}
		doc := p.profile
		if doc.Name == "" {
			continue
		}
		for _, cap := range doc.Policy.MaxCapabilities {
			caps[cap] = true
		}
		hostOpen.Profiles++
		hostOpen.AllowURLs = hostOpen.AllowURLs || doc.HostCapabilities.Open.AllowURLs
		hostOpen.AllowWorkspaceFiles = hostOpen.AllowWorkspaceFiles || doc.HostCapabilities.Open.AllowWorkspaceFiles
		if doc.HostCapabilities.Open.Mode != "" {
			hostOpen.Mode = doc.HostCapabilities.Open.Mode
		}
		if doc.HostCapabilities.Open.BrowserProfile != "" {
			hostOpen.BrowserProfile = doc.HostCapabilities.Open.BrowserProfile
		}
	}
	max := make([]string, 0, len(caps))
	for cap := range caps {
		max = append(max, cap)
	}
	sort.Strings(max)
	return CapabilitySummary{
		MaxCapabilities: max,
		CommandProxies:  registry.Registrations(),
		HostOpen:        hostOpen,
	}
}

func networkSummary(profiles []ProfileSummary) NetworkSummary {
	out := make([]ProfileNetworkSummary, 0, len(profiles))
	for _, p := range profiles {
		if p.ValidationError != "" {
			continue
		}
		out = append(out, ProfileNetworkSummary{
			Profile:         p.Name,
			Mode:            p.NetworkMode,
			ProxySecretRef:  p.ProxySecretRef,
			ProxyEnvVisible: p.ProxyEnvVisible,
		})
	}
	return NetworkSummary{ProfileDefaults: out}
}

func secretSummaries(profiles []ProfileSummary, env []string) []SecretSummary {
	seen := map[string]bool{}
	out := make([]SecretSummary, 0)
	for _, p := range profiles {
		if p.ProxySecretRef == "" || seen[p.ProxySecretRef] {
			continue
		}
		seen[p.ProxySecretRef] = true
		out = append(out, SecretSummary{
			Ref:       p.ProxySecretRef,
			Source:    "profile.network.proxySecretRef",
			Available: secretAvailable(p.ProxySecretRef, env),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func initSummary(store profile.Store) InitSummary {
	summary := inittask.Summary(store, "default")
	out := InitSummary{
		Version:      summary.Version,
		Initialized:  summary.Initialized,
		PendingTasks: summary.PendingTasks,
		Profile:      summary.Profile,
		StatePath:    summary.StatePath,
		AuditPath:    summary.AuditPath,
		AuditEvents:  summary.AuditEvents,
	}
	plan, err := inittask.PlanMachine(store, inittask.Options{ProfileName: "default", Backend: "auto", Network: "direct", NoInput: true})
	if err == nil {
		out.NextSteps = append([]inittask.NextStep(nil), plan.NextSteps...)
	}
	return out
}

func bundleSummary(root string) BundleSummary {
	summary := BundleSummary{Status: "not-configured"}
	entries, err := os.ReadDir(filepath.Join(root, "bundles"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return summary
		}
		summary.Status = "error"
		return summary
	}
	for _, publisher := range entries {
		if !publisher.IsDir() {
			continue
		}
		names, err := os.ReadDir(filepath.Join(root, "bundles", publisher.Name()))
		if err != nil {
			summary.Status = "error"
			return summary
		}
		for _, name := range names {
			if !name.IsDir() {
				continue
			}
			versions, err := os.ReadDir(filepath.Join(root, "bundles", publisher.Name(), name.Name()))
			if err != nil {
				summary.Status = "error"
				return summary
			}
			for _, version := range versions {
				if version.IsDir() {
					summary.Installed++
				}
			}
		}
	}
	if summary.Installed > 0 {
		summary.Status = "installed"
	}
	return summary
}

func secretAvailable(ref string, env []string) bool {
	if env == nil {
		env = os.Environ()
	}
	name := network.SecretEnvName(ref)
	if name == "" {
		return false
	}
	for _, kv := range env {
		if key, value, ok := strings.Cut(kv, "="); ok && key == name && value != "" {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readNetworkMode(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var plan network.Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return ""
	}
	return plan.Mode
}
