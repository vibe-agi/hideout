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
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/inittask"
	"github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
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
}

type BackendCheck struct {
	Name      string
	Isolation string
	Check     func(context.Context) error
}

type Overview struct {
	Version      string               `json:"version"`
	StorageRoot  string               `json:"storageRoot"`
	Profiles     []ProfileSummary     `json:"profiles"`
	Sessions     []SessionSummary     `json:"sessions"`
	Environments []EnvironmentSummary `json:"environments"`
	Backends     []BackendSummary     `json:"backends"`
	Capabilities CapabilitySummary    `json:"capabilities"`
	Network      NetworkSummary       `json:"network"`
	Broker       BrokerSummary        `json:"broker"`
	Secrets      []SecretSummary      `json:"secrets"`
	Audit        AuditSummary         `json:"audit"`
	Settings     SettingsSummary      `json:"settings"`
	Init         InitSummary          `json:"init"`
	Bundles      BundleSummary        `json:"bundles"`
	Projects     ProjectSummary       `json:"projects"`
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
	HostFSGrants               int                         `json:"hostfsGrants"`
	HostFSDeny                 int                         `json:"hostfsDeny"`
	PolicyEngine               string                      `json:"policyEngine"`
	ExpectedCommands           []string                    `json:"expectedCommands,omitempty"`
	ExpectedCommandDiagnostics []ExpectedCommandDiagnostic `json:"expectedCommandDiagnostics,omitempty"`
	ProfilePath                string                      `json:"profilePath"`
	IdentityPath               string                      `json:"identityPath"`
	ValidationError            string                      `json:"validationError,omitempty"`
	profile                    profile.Profile
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
	ID                 string `json:"id"`
	Profile            string `json:"profile,omitempty"`
	Path               string `json:"path"`
	AuditPath          string `json:"auditPath,omitempty"`
	HasAudit           bool   `json:"hasAudit"`
	HasBrokerEndpoint  bool   `json:"hasBrokerEndpoint"`
	HasNetworkPlan     bool   `json:"hasNetworkPlan"`
	HasProxySecretFile bool   `json:"hasProxySecretFile"`
	HasEphemeralState  bool   `json:"hasEphemeralState"`
	NetworkMode        string `json:"networkMode,omitempty"`
}

type EnvironmentSummary struct {
	ID             string    `json:"id"`
	Name           string    `json:"name,omitempty"`
	AutoNamed      bool      `json:"autoNamed,omitempty"`
	ImageRef       string    `json:"imageRef,omitempty"`
	Version        string    `json:"version,omitempty"`
	Profile        string    `json:"profile"`
	Backend        string    `json:"backend"`
	Status         string    `json:"status"`
	Workspace      string    `json:"workspace"`
	GuestWorkspace string    `json:"guestWorkspace"`
	InstanceName   string    `json:"instanceName,omitempty"`
	LastSessionID  string    `json:"lastSessionId,omitempty"`
	LastCommand    string    `json:"lastCommand,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	LastStartedAt  time.Time `json:"lastStartedAt,omitempty"`
	LastEndedAt    time.Time `json:"lastEndedAt,omitempty"`
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

type AuditEventFilter struct {
	Session  string
	Profile  string
	Action   string
	Decision string
	Limit    int
}

func New(store profile.Store) Core {
	return Core{
		Store: store,
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
	if networkMode == "tun2socks" {
		proxySecretRef = plan.RuntimeProfile.Network.ProxySecretRef
	}
	initPlan, err := c.PlanInit(inittask.Options{
		ProfileName:    plan.ProfileName,
		Backend:        plan.Backend,
		Network:        networkMode,
		ProxySecretRef: proxySecretRef,
		NoInput:        true,
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
	registry := c.commandProxy(profiles)
	capabilities := capabilitySummary(profiles, registry)
	return Overview{
		Version:      "hideout.manager/v1",
		StorageRoot:  c.Store.Root,
		Profiles:     profiles,
		Sessions:     sessions,
		Environments: environmentSummaries(c.Store.Root),
		Backends:     c.backendSummaries(ctx),
		Capabilities: capabilities,
		Network:      networkSummary(profiles),
		Broker: BrokerSummary{
			Actions:        []string{"host.open"},
			CommandProxies: registry.ShimNames(),
		},
		Secrets:  secretSummaries(profiles, c.SecretEnv),
		Audit:    AuditSummary{SessionAuditFiles: auditCount},
		Settings: SettingsSummary{StoreRoot: c.Store.Root},
		Init:     initSummary(c.Store),
		Bundles:  bundleSummary(c.Store.Root),
		Projects: ProjectSummary{
			Status: "not-configured",
		},
	}, errors.Join(profileErrors...)
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
	records, err := (environment.Store{Root: storeRoot}).List()
	if err != nil {
		return nil
	}
	out := make([]EnvironmentSummary, 0, len(records))
	for _, rec := range records {
		out = append(out, EnvironmentSummary{
			ID:             rec.ID,
			Name:           rec.Name,
			AutoNamed:      rec.AutoNamed,
			ImageRef:       rec.ImageRef,
			Version:        rec.Version,
			Profile:        rec.Profile,
			Backend:        rec.Backend,
			Status:         rec.Status,
			Workspace:      rec.Workspace,
			GuestWorkspace: rec.GuestWorkspace,
			InstanceName:   rec.InstanceName,
			LastSessionID:  rec.LastSessionID,
			LastCommand:    rec.LastCommand,
			CreatedAt:      rec.CreatedAt,
			LastStartedAt:  rec.LastStartedAt,
			LastEndedAt:    rec.LastEndedAt,
		})
	}
	return out
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
			registry, err := cmdproxy.RegistryFromProfile(p)
			if err == nil {
				summary.CommandProxies = registry.ShimNames()
			}
		}
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, errs
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
