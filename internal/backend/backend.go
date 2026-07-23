package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	ProjectionReadiness       *ProjectionReadinessExpectation
}

type PrivilegedSetupEvent struct {
	Action   string
	Category string
	Status   string
	Setup    privilege.SetupIdentity
	Reason   string
}

type Session struct {
	ID                             string
	EnvironmentID                  string
	SessionSnapshotID              string
	Backend                        string
	HostWork                       string
	GuestWork                      string
	Workspace                      WorkspaceAttachmentSpec
	GuestHome                      string
	Env                            []string
	ShimDir                        string
	ProfileDir                     string
	IdentityMode                   string
	IdentityRoot                   string
	SessionDir                     string
	RuntimeRoot                    string
	SessionIsolationRequired       bool
	TargetUser                     string
	ConfigPath                     string
	BootstrapPath                  string
	ToolManifestPath               string
	NetworkBootstrapPath           string
	NetworkBootstrapGuestPath      string
	NetworkCleanupPath             string
	NetworkCleanupGuestPath        string
	HostFSEnabled                  bool
	HostFSGrafts                   []string
	PortBridges                    []PortBridgeEndpoint
	InstanceName                   string
	PreserveInstance               bool
	Broker                         broker.Endpoint
	NetworkPrivilegedSetup         bool
	PrivilegedSetupRequired        bool
	PrivilegeStatus                *privilege.Status
	PrivilegeStatusSink            func(privilege.Status) error
	PrivilegedSetupEventSink       func(PrivilegedSetupEvent) error
	RuntimeContract                *RuntimeContract
	RuntimeInstanceExpected        *RuntimeInstanceExpectation
	RuntimeResultSink              func(RuntimeObservationReport) error
	RuntimeCompletionSink          func(error) error
	RuntimePresentation            *RuntimePresentation
	ProjectionReadiness            *ProjectionReadinessExpectation
	ProjectionReadinessObservation *ProjectionReadinessObservation
	ActivationOwnerID              string
	ExpectedBootID                 string
	RunAttempted                   bool
	RuntimeReady                   bool
	IsolationRunStarted            bool
	IsolationCleanupProved         bool
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

// ProgressRedirector lets a session owner route human startup progress (slow
// VM boot notices, heartbeats) to the operator-facing stream. Without this, a
// daemon-hosted backend writes progress to the daemon's own stderr, which no
// operator sees. It carries presentation only, never authority.
type ProgressRedirector interface {
	WithProgress(progress io.Writer) Backend
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

const (
	ProjectionReadinessManifestSchema = "hideout.projection-readiness/v1"
	ProjectionReadinessManifestFile   = "projection-readiness.json"
	MaxProjectionReadinessEntries     = 129
	MaxProjectionReadinessEntryBytes  = 32 << 20
	MaxProjectionReadinessDeadline    = 2 * time.Second
)

type ProjectionReadinessEntryKind string

const (
	ProjectionEntryDispatcher ProjectionReadinessEntryKind = "dispatcher"
	ProjectionEntryCommand    ProjectionReadinessEntryKind = "command"
)

type ProjectionReadinessEntry struct {
	Name         string                       `json:"name"`
	RelativePath string                       `json:"relativePath"`
	SHA256       string                       `json:"sha256"`
	Kind         ProjectionReadinessEntryKind `json:"kind"`
}

type ProjectionReadinessManifest struct {
	Schema            string                     `json:"schema"`
	SessionID         string                     `json:"sessionId"`
	EnvironmentID     string                     `json:"environmentId"`
	SessionSnapshotID string                     `json:"sessionSnapshotId"`
	CatalogDigest     string                     `json:"catalogDigest"`
	Entries           []ProjectionReadinessEntry `json:"entries"`
}

type projectionReadinessCatalogPayload struct {
	Schema            string                     `json:"schema"`
	SessionID         string                     `json:"sessionId"`
	EnvironmentID     string                     `json:"environmentId"`
	SessionSnapshotID string                     `json:"sessionSnapshotId"`
	Entries           []ProjectionReadinessEntry `json:"entries"`
}

func ProjectionReadinessCatalogDigest(manifest ProjectionReadinessManifest) (string, error) {
	canonical, err := json.Marshal(projectionReadinessCatalogPayload{
		Schema: manifest.Schema, SessionID: manifest.SessionID,
		EnvironmentID: manifest.EnvironmentID, SessionSnapshotID: manifest.SessionSnapshotID,
		Entries: manifest.Entries,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (manifest ProjectionReadinessManifest) ValidateCatalogDigest() error {
	actual, err := ProjectionReadinessCatalogDigest(manifest)
	if err != nil {
		return err
	}
	if actual != manifest.CatalogDigest {
		return errors.New("projection readiness catalog digest mismatch")
	}
	return nil
}

func (manifest ProjectionReadinessManifest) Validate() error {
	if manifest.Schema != ProjectionReadinessManifestSchema {
		return fmt.Errorf("unsupported projection readiness schema %q", manifest.Schema)
	}
	if strings.TrimSpace(manifest.SessionID) == "" || strings.TrimSpace(manifest.SessionID) != manifest.SessionID ||
		strings.TrimSpace(manifest.EnvironmentID) == "" || strings.TrimSpace(manifest.EnvironmentID) != manifest.EnvironmentID {
		return errors.New("projection readiness session and environment identities are required")
	}
	if !environment.ValidConfigurationID(manifest.SessionSnapshotID) {
		return errors.New("projection readiness session snapshot identity is invalid")
	}
	if !validProjectionSHA256(manifest.CatalogDigest) {
		return errors.New("projection readiness catalog digest is invalid")
	}
	if len(manifest.Entries) == 0 || len(manifest.Entries) > MaxProjectionReadinessEntries {
		return fmt.Errorf("projection readiness entries must contain 1-%d values", MaxProjectionReadinessEntries)
	}
	dispatchers := 0
	previous := ""
	for _, entry := range manifest.Entries {
		if err := entry.Validate(); err != nil {
			return err
		}
		if previous != "" && entry.RelativePath <= previous {
			return errors.New("projection readiness entries must be sorted and unique")
		}
		previous = entry.RelativePath
		if entry.Kind == ProjectionEntryDispatcher {
			dispatchers++
		}
	}
	if dispatchers != 1 {
		return errors.New("projection readiness catalog requires exactly one dispatcher")
	}
	return nil
}

func (entry ProjectionReadinessEntry) Validate() error {
	if strings.TrimSpace(entry.Name) == "" || strings.TrimSpace(entry.Name) != entry.Name ||
		filepath.Base(entry.Name) != entry.Name || strings.ContainsAny(entry.Name, "/\\\x00\r\n") ||
		entry.RelativePath != entry.Name {
		return errors.New("projection readiness entry requires one exact basename")
	}
	if !validProjectionSHA256(entry.SHA256) {
		return fmt.Errorf("projection readiness entry %q digest is invalid", entry.Name)
	}
	switch entry.Kind {
	case ProjectionEntryDispatcher:
		if entry.Name != "hideout-shim" {
			return errors.New("projection readiness dispatcher identity is invalid")
		}
	case ProjectionEntryCommand:
		if entry.Name == "hideout-shim" {
			return errors.New("projection readiness command uses the reserved dispatcher name")
		}
	default:
		return fmt.Errorf("projection readiness entry %q kind is invalid", entry.Name)
	}
	return nil
}

type ProjectionReadinessExpectation struct {
	Manifest             ProjectionReadinessManifest `json:"manifest"`
	ManifestRelativePath string                      `json:"manifestRelativePath"`
	TargetProjected      bool                        `json:"targetProjected"`
	Deadline             time.Duration               `json:"-"`
}

func (expectation ProjectionReadinessExpectation) Validate() error {
	if expectation.ManifestRelativePath != ProjectionReadinessManifestFile {
		return errors.New("projection readiness manifest path is invalid")
	}
	if expectation.Deadline <= 0 || expectation.Deadline > MaxProjectionReadinessDeadline {
		return errors.New("projection readiness deadline is invalid")
	}
	if err := expectation.Manifest.Validate(); err != nil {
		return err
	}
	return expectation.Manifest.ValidateCatalogDigest()
}

func CloneProjectionReadinessExpectation(value *ProjectionReadinessExpectation) *ProjectionReadinessExpectation {
	if value == nil {
		return nil
	}
	out := *value
	out.Manifest.Entries = append([]ProjectionReadinessEntry(nil), value.Manifest.Entries...)
	return &out
}

type ProjectionReadinessStatus string

const (
	ProjectionReadinessReady     ProjectionReadinessStatus = "ready"
	ProjectionReadinessRefused   ProjectionReadinessStatus = "refused"
	ProjectionReadinessTimedOut  ProjectionReadinessStatus = "timed-out"
	ProjectionReadinessCancelled ProjectionReadinessStatus = "cancelled"
)

type ProjectionReadinessReason string

const (
	ProjectionReadinessManifestMissing ProjectionReadinessReason = "projection.readiness.manifest-missing"
	ProjectionReadinessTimeout         ProjectionReadinessReason = "projection.readiness.timeout"
	ProjectionReadinessCancellation    ProjectionReadinessReason = "projection.readiness.cancelled"
	ProjectionReadinessCatalogDrift    ProjectionReadinessReason = "projection.readiness.catalog-drift"
	ProjectionReadinessIdentityDrift   ProjectionReadinessReason = "projection.readiness.identity-drift"
	ProjectionReadinessEntryMissing    ProjectionReadinessReason = "projection.readiness.entry-missing"
	ProjectionReadinessEntryInvalid    ProjectionReadinessReason = "projection.readiness.entry-invalid"
	ProjectionReadinessDigestMismatch  ProjectionReadinessReason = "projection.readiness.entry-digest-mismatch"
)

type ProjectionReadinessObservation struct {
	Status          ProjectionReadinessStatus `json:"status"`
	ReasonCode      ProjectionReadinessReason `json:"reasonCode,omitempty"`
	CatalogDigest   string                    `json:"catalogDigest"`
	ExpectedEntries int                       `json:"expectedEntries"`
	ObservedEntries int                       `json:"observedEntries"`
	DurationMillis  int64                     `json:"durationMs"`
	TargetProjected bool                      `json:"targetProjected"`
}

type ProjectionReadinessError struct {
	Status     ProjectionReadinessStatus
	ReasonCode ProjectionReadinessReason
	Hint       string
	Err        error
}

func (e *ProjectionReadinessError) Error() string {
	if e == nil {
		return "projection readiness failed"
	}
	summary := string(e.ReasonCode)
	if e.Hint != "" {
		summary += ": " + e.Hint
	}
	return summary
}

func (e *ProjectionReadinessError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (observation ProjectionReadinessObservation) Validate(expectation *ProjectionReadinessExpectation) error {
	if expectation == nil {
		return errors.New("projection readiness observation requires an expectation")
	}
	if err := expectation.Validate(); err != nil {
		return err
	}
	if !validProjectionSHA256(observation.CatalogDigest) {
		return errors.New("projection readiness observation catalog digest is invalid")
	}
	if observation.ExpectedEntries != len(expectation.Manifest.Entries) ||
		observation.ObservedEntries < 0 || observation.ObservedEntries > observation.ExpectedEntries ||
		observation.DurationMillis < 0 ||
		time.Duration(observation.DurationMillis)*time.Millisecond > expectation.Deadline ||
		observation.TargetProjected != expectation.TargetProjected {
		return errors.New("projection readiness observation does not match the expectation")
	}
	switch observation.Status {
	case ProjectionReadinessReady:
		if observation.ReasonCode != "" ||
			observation.CatalogDigest != expectation.Manifest.CatalogDigest ||
			observation.ObservedEntries != observation.ExpectedEntries {
			return errors.New("ready projection observation is incomplete or mismatched")
		}
	case ProjectionReadinessRefused, ProjectionReadinessTimedOut, ProjectionReadinessCancelled:
		if !validProjectionReadinessReason(observation.ReasonCode) {
			return errors.New("failed projection readiness observation requires a stable reason")
		}
	default:
		return errors.New("projection readiness observation status is invalid")
	}
	return nil
}

func validProjectionReadinessReason(value ProjectionReadinessReason) bool {
	switch value {
	case ProjectionReadinessManifestMissing, ProjectionReadinessTimeout,
		ProjectionReadinessCancellation, ProjectionReadinessCatalogDrift,
		ProjectionReadinessIdentityDrift, ProjectionReadinessEntryMissing,
		ProjectionReadinessEntryInvalid, ProjectionReadinessDigestMismatch:
		return true
	default:
		return false
	}
}

func validProjectionSHA256(value string) bool {
	raw := strings.TrimPrefix(value, "sha256:")
	if len(raw) != 64 || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(raw)
	return err == nil && strings.ToLower(raw) == raw
}

// SessionReadyProof is emitted only after a concrete execution supervisor has
// proved its session and incarnation identity, and before the target is
// authorized to start. It carries no workspace root or control credential.
type SessionReadyProof struct {
	SessionID                 string
	EnvironmentID             string
	SessionSnapshotID         string
	InstanceName              string
	BootID                    string
	Source                    SessionReadySource
	ProjectionStatus          ProjectionReadinessStatus
	ProjectionReasonCode      ProjectionReadinessReason
	ProjectionCatalogDigest   string
	ProjectionExpectedEntries int
	ProjectionObservedEntries int
	ProjectionDurationMillis  int64
	ProjectionTargetProjected bool
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
	if session.ProjectionReadiness != nil {
		if session.ProjectionReadinessObservation == nil {
			return SessionReadyProof{}, errors.New("ready proof requires a projection readiness observation")
		}
		if err := session.ProjectionReadinessObservation.Validate(session.ProjectionReadiness); err != nil {
			return SessionReadyProof{}, err
		}
		observation := session.ProjectionReadinessObservation
		proof.ProjectionStatus = observation.Status
		proof.ProjectionReasonCode = observation.ReasonCode
		proof.ProjectionCatalogDigest = observation.CatalogDigest
		proof.ProjectionExpectedEntries = observation.ExpectedEntries
		proof.ProjectionObservedEntries = observation.ObservedEntries
		proof.ProjectionDurationMillis = observation.DurationMillis
		proof.ProjectionTargetProjected = observation.TargetProjected
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
	if proof.ProjectionStatus != "" {
		if proof.ProjectionStatus != ProjectionReadinessReady || proof.ProjectionReasonCode != "" ||
			!validProjectionSHA256(proof.ProjectionCatalogDigest) ||
			proof.ProjectionExpectedEntries <= 0 ||
			proof.ProjectionObservedEntries != proof.ProjectionExpectedEntries ||
			proof.ProjectionDurationMillis < 0 ||
			time.Duration(proof.ProjectionDurationMillis)*time.Millisecond > MaxProjectionReadinessDeadline {
			return errors.New("session ready projection proof is invalid")
		}
	} else if proof.ProjectionReasonCode != "" || proof.ProjectionCatalogDigest != "" ||
		proof.ProjectionExpectedEntries != 0 || proof.ProjectionObservedEntries != 0 ||
		proof.ProjectionDurationMillis != 0 || proof.ProjectionTargetProjected {
		return errors.New("session ready proof carries incomplete projection readiness")
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
	if session.ProjectionReadiness != nil {
		if session.ProjectionReadinessObservation == nil {
			return errors.New("prepared session has no projection readiness observation")
		}
		if err := session.ProjectionReadinessObservation.Validate(session.ProjectionReadiness); err != nil {
			return err
		}
		observation := session.ProjectionReadinessObservation
		if proof.ProjectionStatus != observation.Status ||
			proof.ProjectionReasonCode != observation.ReasonCode ||
			proof.ProjectionCatalogDigest != observation.CatalogDigest ||
			proof.ProjectionExpectedEntries != observation.ExpectedEntries ||
			proof.ProjectionObservedEntries != observation.ObservedEntries ||
			proof.ProjectionDurationMillis != observation.DurationMillis ||
			proof.ProjectionTargetProjected != observation.TargetProjected {
			return errors.New("session ready proof does not match projection readiness")
		}
	} else if proof.ProjectionStatus != "" {
		return errors.New("session ready proof carries unexpected projection readiness")
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
