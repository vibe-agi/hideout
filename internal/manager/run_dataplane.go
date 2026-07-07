package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/hostfs"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/session"
)

type RunDataPlaneOptions struct {
	HostFSRun                  hostfs.Config
	DisableProfileHostFSGrants bool
	Backend                    backend.Backend
	Opener                     broker.Opener
	PortBridges                []RunPortBridgeRequest
	OpenTargets                []RunOpenTargetOwner
	EndpointCandidates         []RunEndpointCandidate
	EndpointExposures          []RunEndpointExposureRequest
}

type RunDataPlane struct {
	Registry             cmdproxy.Registry
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
}

func (c Core) StartRunDataPlane(ctx context.Context, runSession RunSession, runNetwork RunNetwork, opts RunDataPlaneOptions) (RunDataPlane, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	aw := runSession.Audit
	if aw == nil {
		aw = audit.NewDiscard()
	}
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
	if err := MaterializeCommandProxyShims(runSession.RuntimeShimDir, runSession.Plan.Backend, registry, runNetwork.Plan); err != nil {
		return RunDataPlane{}, err
	}
	hostFSPolicy, err := hostfs.Build(hostfs.BuildInput{
		Profile:   HostFSProfileForRun(runSession.Plan.RuntimeProfile, opts.DisableProfileHostFSGrants),
		Run:       opts.HostFSRun,
		StoreRoot: c.Store.Root,
	})
	if err != nil {
		return RunDataPlane{}, err
	}
	hostFSEnabled := len(hostFSPolicy.Grants) > 0
	if err := MaterializeHostFSD(runSession.RuntimeShimDir, runSession.Plan.Backend, hostFSEnabled); err != nil {
		return RunDataPlane{}, err
	}
	hostFSService := hostfs.NewService(hostFSPolicy)
	brokerCtx, cancel := context.WithCancel(ctx)
	listenEndpoint := BrokerEndpointForBackend(runSession.Plan.Backend, runSession.Layout)
	opener := opts.Opener
	if opener == nil {
		opener = broker.NoopOpener{}
	}
	server := &broker.Server{
		SessionID:     runSession.Layout.ID,
		Token:         token,
		Socket:        runSession.Layout.BrokerSock,
		Endpoint:      listenEndpoint,
		HostRoot:      runSession.Plan.Workspace,
		GuestRoot:     runSession.Plan.GuestWorkspace,
		Profile:       runSession.Plan.ProfileName,
		ProfileDir:    runSession.ProfileDir,
		Backend:       runSession.Plan.Backend,
		WorkspaceMode: runSession.Plan.RuntimeProfile.Workspace.Mode,
		NetworkMode:   runSession.Plan.RuntimeProfile.Network.Mode,
		Commands:      registry.ShimNames(),
		ScriptRefs:    runSession.Plan.RuntimeProfile.Policy.ScriptRefs,
		Evaluator:     policy.NewEvaluator(runSession.Plan.RuntimeProfile),
		Audit:         runSession.Audit,
		Opener:        opener,
		HostFS:        &hostFSService,
	}
	if err := server.StartEndpoint(brokerCtx, listenEndpoint); err != nil {
		cancel()
		return RunDataPlane{}, err
	}
	guestEndpoint, err := BrokerEndpointForGuest(runSession.Plan.Backend, server.Endpoint)
	if err != nil {
		_ = server.Close()
		cancel()
		return RunDataPlane{}, err
	}
	if err := WriteBrokerEndpoint(runSession.Layout.BrokerEndpointPath, guestEndpoint); err != nil {
		_ = server.Close()
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
		Details: map[string]any{
			"workspace":       runSession.Plan.Workspace,
			"guestWork":       runSession.Plan.GuestWorkspace,
			"network":         runSession.Plan.RuntimeProfile.Network.Mode,
			"networkPlan":     presence(runNetwork.Plan.ManifestPath),
			"command":         strings.Join(runSession.Plan.Command, " "),
			"brokerEndpoint":  "present",
			"brokerTransport": guestEndpoint.Network,
		},
	}); err != nil {
		_ = server.Close()
		cancel()
		return RunDataPlane{}, err
	}
	portBridgeLeases, portBridgeEndpoints, err := startRunPortBridges(brokerCtx, runSession, portBridges, env, opts.Backend, aw)
	if err != nil {
		_ = closeRunPortBridgeLeases(portBridgeLeases, aw, runSession.Layout.ID, runSession.Plan.ProfileName, runSession.Plan.Backend)
		_ = server.Close()
		cancel()
		return RunDataPlane{}, err
	}
	return RunDataPlane{
		Registry:             registry,
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
	}, nil
}

func (c Core) CloseRunDataPlane(dataPlane RunDataPlane) error {
	var errs []error
	if err := closeRunPortBridgeLeases(dataPlane.portBridgeLeases, dataPlane.audit, dataPlane.auditSession, dataPlane.auditProfile, dataPlane.auditBackend); err != nil {
		errs = append(errs, err)
	}
	if dataPlane.cancel != nil {
		dataPlane.cancel()
	}
	if dataPlane.Broker != nil {
		if err := dataPlane.Broker.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
	script := ""
	if shimPath := ResolveShimPath(); shimPath != "" {
		script = fmt.Sprintf("#!/bin/sh\nexec %q \"$(basename \"$0\")\" \"$@\"\n", shimPath)
	} else {
		script = "#!/bin/sh\necho 'hideout-shim unavailable' >&2\nexit 69\n"
	}
	for _, name := range registry.ShimNames() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			return err
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
	script := "#!/bin/sh\nshim_dir=$(CDPATH= cd -- \"$(dirname -- \"$0\")\" && pwd)\nexec \"$shim_dir/hideout-shim\" \"$(basename \"$0\")\" \"$@\"\n"
	for _, name := range registry.ShimNames() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			return err
		}
	}
	if netPlan.Mode == netpolicy.ModeTun2Socks {
		if source := ResolveLinuxTun2SocksPath(); source != "" {
			if err := copyExecutable(source, filepath.Join(dir, "tun2socks")); err != nil {
				return err
			}
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
	return helperbin.ResolveLinuxTun2SocksPath(runtime.GOARCH)
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
