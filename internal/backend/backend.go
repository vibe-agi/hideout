package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/privilege"
	"github.com/vibe-agi/hideout/internal/profile"
)

type RunSpec struct {
	SessionID                 string
	EnvironmentID             string
	ImageRef                  string
	Profile                   profile.Profile
	Command                   []string
	Env                       []string
	HostWork                  string
	GuestWork                 string
	GuestHome                 string
	ShimDir                   string
	ProfileDir                string
	IdentityMode              string
	IdentityRoot              string
	SessionDir                string
	RuntimeRoot               string
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
	InstanceName              string
	PreserveInstance          bool
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
	Backend                   string
	HostWork                  string
	GuestWork                 string
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
	Ready    func(*Session) error
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
	StopEnvironmentNetwork(ctx context.Context, session *Session, workdir, cleanupPath string, env []string) error
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
