package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/privilege"
	"github.com/vibe-agi/hideout/internal/profile"
)

type WorkspaceTransport string

const (
	WorkspaceTransportStatic WorkspaceTransport = "static"
	WorkspaceTransportPortal WorkspaceTransport = "workspace-portal"
)

// MachineActivationSpec is intentionally incapable of naming a project. A
// shared machine can therefore be created and started before any workspace
// authority exists.
type MachineActivationSpec struct {
	EnvironmentID    string
	ImageRef         string
	Profile          profile.Profile
	ProfileDir       string
	IdentityMode     string
	IdentityRoot     string
	RuntimeRoot      string
	InstanceName     string
	PreserveInstance bool
	Mode             environment.Mode
	Runtime          *RuntimePresentation
}

// RuntimePresentation carries package-owned, non-secret facts for honest
// first-start progress. It grants no runtime or image authority.
type RuntimePresentation struct {
	Family        string
	Revision      string
	Maturity      string
	DownloadBytes int64
}

func (spec MachineActivationSpec) Validate() error {
	if spec.Profile.Name == "" || spec.ImageRef == "" || spec.ProfileDir == "" || !filepath.IsAbs(spec.ProfileDir) {
		return errors.New("machine activation profile, image, and profile directory are required")
	}
	if spec.IdentityRoot != "" && !filepath.IsAbs(spec.IdentityRoot) {
		return errors.New("machine activation identity root must be absolute")
	}
	switch spec.Mode {
	case environment.ModeShared:
		if spec.EnvironmentID == "" || spec.RuntimeRoot == "" || !filepath.IsAbs(spec.RuntimeRoot) {
			return errors.New("shared machine activation requires environment identity and runtime root")
		}
	case environment.ModeDedicated, environment.ModeWorkspaceBound:
	default:
		return fmt.Errorf("unsupported machine activation mode %q", spec.Mode)
	}
	return nil
}

// WorkspaceAttachmentSpec is the exact project authority admitted after
// machine selection. It is never inferred from MachineActivationSpec.
type WorkspaceAttachmentSpec struct {
	HostRoot  string
	GuestRoot string
	Transport WorkspaceTransport
	Portal    *WorkspacePortalBinding
}

type WorkspacePortalBinding struct {
	PhysicalGuestRoot   string
	Endpoint            string
	CredentialGuestPath string
}

func (spec WorkspaceAttachmentSpec) Validate(machineMode environment.Mode) error {
	if !filepath.IsAbs(spec.HostRoot) || filepath.Clean(spec.HostRoot) != spec.HostRoot ||
		!filepath.IsAbs(spec.GuestRoot) || filepath.Clean(spec.GuestRoot) != spec.GuestRoot {
		return errors.New("workspace attachment requires clean absolute host and guest roots")
	}
	if machineMode == environment.ModeShared {
		if spec.Transport != WorkspaceTransportPortal {
			return errors.New("shared machine requires the selected dynamic workspace transport")
		}
		if spec.Portal == nil || !filepath.IsAbs(spec.Portal.PhysicalGuestRoot) ||
			filepath.Clean(spec.Portal.PhysicalGuestRoot) != spec.Portal.PhysicalGuestRoot ||
			!strings.HasPrefix(spec.Portal.PhysicalGuestRoot, "/hideout/workspaces/") ||
			!filepath.IsAbs(spec.Portal.CredentialGuestPath) ||
			filepath.Clean(spec.Portal.CredentialGuestPath) != spec.Portal.CredentialGuestPath ||
			strings.TrimSpace(spec.Portal.Endpoint) == "" {
			return errors.New("shared machine requires a complete workspace Portal binding")
		}
		host, port, err := net.SplitHostPort(spec.Portal.Endpoint)
		if err != nil || strings.TrimSpace(host) == "" || port == "" || port == "0" {
			return errors.New("shared machine workspace Portal endpoint is invalid")
		}
		return nil
	}
	if spec.Transport != WorkspaceTransportStatic {
		return errors.New("dedicated and workspace-bound machines require an exact static workspace mapping")
	}
	if spec.Portal != nil {
		return errors.New("static workspace mapping cannot carry Portal authority")
	}
	return nil
}

type RunSpec struct {
	Machine                   MachineActivationSpec
	Workspace                 WorkspaceAttachmentSpec
	SessionID                 string
	Command                   []string
	Env                       []string
	GuestHome                 string
	ShimDir                   string
	SessionDir                string
	SessionIsolationRequired  bool
	TargetUser                string
	Broker                    broker.Endpoint
	NetworkBootstrapPath      string
	NetworkBootstrapGuestPath string
	NetworkCleanupPath        string
	NetworkCleanupGuestPath   string
	HostFSEnabled             bool
	HostFSGrafts              []string
	PortBridges               []PortBridgeEndpoint
	AuditPath                 string
	NetworkPrivilegedSetup    bool
	PrivilegedSetupRequired   bool
	PrivilegeStatusSink       func(privilege.Status) error
	PrivilegedSetupEventSink  func(PrivilegedSetupEvent) error
	RuntimeContract           *RuntimeContract
	RuntimeInstanceExpected   *RuntimeInstanceExpectation
	RuntimeResultSink         func(RuntimeObservationReport) error
	RuntimeCompletionSink     func(error) error
}

type PrivilegedSetupEvent struct {
	Action   string
	Category string
	Status   string
	Setup    privilege.SetupIdentity
	Reason   string
}

type Session struct {
	ID                        string
	EnvironmentID             string
	SessionSnapshotID         string
	Backend                   string
	HostWork                  string
	GuestWork                 string
	Workspace                 WorkspaceAttachmentSpec
	GuestHome                 string
	Env                       []string
	ShimDir                   string
	ProfileDir                string
	IdentityMode              string
	IdentityRoot              string
	SessionDir                string
	RuntimeRoot               string
	SessionIsolationRequired  bool
	TargetUser                string
	ConfigPath                string
	BootstrapPath             string
	ToolManifestPath          string
	NetworkBootstrapPath      string
	NetworkBootstrapGuestPath string
	NetworkCleanupPath        string
	NetworkCleanupGuestPath   string
	HostFSEnabled             bool
	HostFSGrafts              []string
	PortBridges               []PortBridgeEndpoint
	InstanceName              string
	PreserveInstance          bool
	Broker                    broker.Endpoint
	NetworkPrivilegedSetup    bool
	PrivilegedSetupRequired   bool
	PrivilegeStatus           *privilege.Status
	PrivilegeStatusSink       func(privilege.Status) error
	PrivilegedSetupEventSink  func(PrivilegedSetupEvent) error
	RuntimeContract           *RuntimeContract
	RuntimeInstanceExpected   *RuntimeInstanceExpectation
	RuntimeResultSink         func(RuntimeObservationReport) error
	RuntimeCompletionSink     func(error) error
	RuntimePresentation       *RuntimePresentation
	ActivationOwnerID         string
	ExpectedBootID            string
	RunAttempted              bool
	RuntimeReady              bool
	IsolationRunStarted       bool
	IsolationCleanupProved    bool
}

type PortBridgeEndpoint struct {
	ID               string `json:"id"`
	Owner            string `json:"owner"`
	Action           string `json:"action,omitempty"`
	Source           string `json:"source,omitempty"`
	ClosePolicy      string `json:"closePolicy,omitempty"`
	Lifetime         string `json:"lifetime"`
	Direction        string `json:"direction"`
	ListenScope      string `json:"listenScope"`
	ListenAddress    string `json:"listenAddress"`
	TargetScope      string `json:"targetScope"`
	TargetAddress    string `json:"targetAddress,omitempty"`
	EndpointCategory string `json:"endpointCategory"`
}

type Backend interface {
	Name() string
	Available(ctx context.Context) error
	Prepare(ctx context.Context, spec RunSpec) (*Session, error)
	Run(ctx context.Context, session *Session, command []string, env []string) error
	Cleanup(ctx context.Context, session *Session) error
}

type RunControlKind string

const (
	RunControlResize RunControlKind = "resize"
	RunControlSignal RunControlKind = "signal"
)

// RunControl is a transport-neutral target control. Only the fields belonging
// to Kind are meaningful; terminal file descriptors remain in the client.
type RunControl struct {
	Kind    RunControlKind
	Rows    uint16
	Columns uint16
	Signal  string
}

// RunStreams binds one daemon-owned worker to one client connection. Terminal
// output is used only in PTY mode; stdout and stderr remain separate in pipe
// mode. Closing Stdin represents EOF and context cancellation represents loss.
type RunStreams struct {
	Terminal bool
	Rows     uint16
	Columns  uint16
	Term     string
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	PTY      io.Writer
	Controls <-chan RunControl
	Ready    func(SessionReadyProof) error
}

type SessionReadySource string

const (
	SessionReadyAuthenticatedSupervisor SessionReadySource = "authenticated-guest-supervisor"
	SessionReadyNativeHarness           SessionReadySource = "native-harness"
)

// SessionReadyProof is emitted only after a concrete execution supervisor has
// proved its session and incarnation identity, and before the target is
// authorized to start. It carries no workspace root or control credential.
type SessionReadyProof struct {
	SessionID         string
	EnvironmentID     string
	SessionSnapshotID string
	InstanceName      string
	BootID            string
	Source            SessionReadySource
}

func ReadyProofForSession(session *Session, source SessionReadySource) (SessionReadyProof, error) {
	if session == nil {
		return SessionReadyProof{}, errors.New("ready proof requires a prepared session")
	}
	proof := SessionReadyProof{
		SessionID: session.ID, EnvironmentID: session.EnvironmentID,
		SessionSnapshotID: session.SessionSnapshotID,
		InstanceName:      session.InstanceName, BootID: session.ExpectedBootID, Source: source,
	}
	if err := proof.Validate(); err != nil {
		return SessionReadyProof{}, err
	}
	return proof, nil
}

func (proof SessionReadyProof) Validate() error {
	if strings.TrimSpace(proof.SessionID) == "" || strings.TrimSpace(proof.EnvironmentID) == "" ||
		!environment.ValidConfigurationID(proof.SessionSnapshotID) {
		return errors.New("session ready proof identity is incomplete")
	}
	switch proof.Source {
	case SessionReadyAuthenticatedSupervisor:
		if strings.TrimSpace(proof.InstanceName) == "" || strings.TrimSpace(proof.BootID) == "" {
			return errors.New("authenticated session ready proof requires a boot identity")
		}
	case SessionReadyNativeHarness:
		if proof.BootID != "" {
			return errors.New("native ready proof cannot claim a guest boot identity")
		}
	default:
		return errors.New("session ready proof source is invalid")
	}
	return nil
}

func (proof SessionReadyProof) ValidateSession(session *Session, requireAuthenticated bool) error {
	if err := proof.Validate(); err != nil {
		return err
	}
	if session == nil || proof.SessionID != session.ID || proof.EnvironmentID != session.EnvironmentID ||
		proof.SessionSnapshotID != session.SessionSnapshotID || proof.InstanceName != session.InstanceName || proof.BootID != session.ExpectedBootID {
		return errors.New("session ready proof does not match the prepared session")
	}
	if requireAuthenticated && proof.Source != SessionReadyAuthenticatedSupervisor {
		return errors.New("shared workspace activation requires authenticated guest supervisor proof")
	}
	return nil
}

// StreamRunner is required for the daemon session transport. A backend that
// does not implement it may still serve legacy bounded HTTP/test adapters, but
// Manager must never substitute Backend.Run for a requested stream session.
type StreamRunner interface {
	RunWithStreams(ctx context.Context, session *Session, command []string, env []string, streams RunStreams) error
}

// Activator separates bounded environment startup/verification from target
// lifetime. Manager invokes it under the environment transition lock when a
// backend supports reusable concurrent sessions.
type Activator interface {
	Activate(ctx context.Context, session *Session, env []string) error
}

// WarmActivator authenticates attachment to an already-running environment.
// Manager may use it only while another owner lock is proved live.
type WarmActivator interface {
	WarmActivate(ctx context.Context, session *Session, env []string) error
}

type EnvironmentNetworkServiceController interface {
	StartEnvironmentNetwork(ctx context.Context, session *Session, workdir, bootstrapPath string, env []string) error
	VerifyEnvironmentNetwork(ctx context.Context, session *Session, workdir string, env []string) error
	VerifyDirectEnvironmentNetwork(ctx context.Context, session *Session, workdir string, env []string) error
	StopEnvironmentNetwork(ctx context.Context, session *Session, workdir, cleanupPath string, env []string) error
}

type EnvironmentNetworkDNSController interface {
	ReconfigureEnvironmentNetworkDNS(ctx context.Context, session *Session, workdir, oldResolver, newResolver string, env []string) error
}

// EnvironmentServiceReconfigureError reports a failed live change and whether
// the backend independently proved that the previous service generation was
// restored. Manager may retain a ready state only when RollbackProved is true.
type EnvironmentServiceReconfigureError struct {
	Operation      string
	RollbackProved bool
	Cause          error
}

func (e EnvironmentServiceReconfigureError) Error() string {
	if e.Cause == nil {
		return e.Operation + " failed"
	}
	return e.Operation + ": " + e.Cause.Error()
}

func (e EnvironmentServiceReconfigureError) Unwrap() error { return e.Cause }

// EnvironmentBootController reconciles environment-global presentation that
// can be changed safely on a running guest. It must not recreate the instance
// or execute target-supplied commands.
type EnvironmentBootController interface {
	ReconcileEnvironmentBoot(ctx context.Context, session *Session, configuration environment.BootConfiguration, env []string) error
}

type CommandNotFoundError struct {
	Backend   string
	Command   string
	Path      string
	Workspace string
	Hint      string
}

func (e CommandNotFoundError) Error() string {
	backend := e.Backend
	if backend == "" {
		backend = "unknown"
	}
	path := e.Path
	if path == "" {
		path = "empty"
	}
	msg := fmt.Sprintf("target command %q not found in %s backend PATH (%s)", e.Command, backend, path)
	if e.Workspace != "" {
		msg += fmt.Sprintf("; workspace=%s", e.Workspace)
	}
	if e.Hint != "" {
		msg += "; " + e.Hint
	}
	return msg
}

func EnvValue(env []string, name string) string {
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k == name {
			return v
		}
	}
	return ""
}

func LookPathInEnv(command string, env []string) (string, error) {
	if command == "" {
		return "", errors.New("command is required")
	}
	if strings.ContainsAny(command, `/\`) {
		if executableFile(command) {
			return command, nil
		}
		return "", fmt.Errorf("%s: executable file not found", command)
	}
	pathEnv := EnvValue(env, "PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		path := filepath.Join(dir, command)
		if executableFile(path) {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s: executable file not found in PATH", command)
}

func executableFile(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return false
	}
	return st.Mode().Perm()&0o111 != 0
}
