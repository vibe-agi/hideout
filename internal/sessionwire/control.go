package sessionwire

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	RunRequestSchema = "hideout.run-session-request/v1"
	DefaultTERM      = "xterm-256color"
)

var (
	terminalNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
	environmentKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	codePattern           = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	userPattern           = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

type Control interface {
	Validate() error
}

type TerminalMode string

const (
	TerminalNone TerminalMode = "none"
	TerminalPTY  TerminalMode = "pty"
)

type TerminalDescriptor struct {
	Mode    TerminalMode `json:"mode"`
	Rows    uint16       `json:"rows,omitempty"`
	Columns uint16       `json:"columns,omitempty"`
	Term    string       `json:"term,omitempty"`
}

func (t TerminalDescriptor) Validate() error {
	switch t.Mode {
	case TerminalNone:
		if t.Rows != 0 || t.Columns != 0 || t.Term != "" {
			return errors.New("non-PTY terminal descriptor cannot carry dimensions or TERM")
		}
	case TerminalPTY:
		if t.Rows == 0 || t.Columns == 0 {
			return errors.New("PTY terminal descriptor requires non-zero dimensions")
		}
		if !terminalNamePattern.MatchString(t.Term) {
			return errors.New("PTY terminal descriptor has invalid TERM")
		}
	default:
		return fmt.Errorf("unsupported terminal mode %q", t.Mode)
	}
	return nil
}

type Hello struct {
	Protocol      string `json:"protocol"`
	Token         string `json:"token"`
	ClientVersion string `json:"clientVersion,omitempty"`
}

func (h *Hello) Validate() error {
	if h == nil {
		return errors.New("hello is nil")
	}
	if h.Protocol != Protocol {
		return fmt.Errorf("unsupported protocol %q", h.Protocol)
	}
	if err := requireOpaque(h.Token, "operator token", 4096); err != nil {
		return err
	}
	return optionalText(h.ClientVersion, "client version", 128)
}

type HelloAccepted struct {
	Protocol             string `json:"protocol"`
	ConnectionID         string `json:"connectionId"`
	InstanceID           string `json:"instanceId"`
	CredentialGeneration uint64 `json:"credentialGeneration"`
	RenewalIntervalMs    uint32 `json:"renewalIntervalMs"`
	LeaseDurationMs      uint32 `json:"leaseDurationMs"`
}

func (h *HelloAccepted) Validate() error {
	if h == nil {
		return errors.New("hello acceptance is nil")
	}
	if h.Protocol != Protocol {
		return fmt.Errorf("unsupported protocol %q", h.Protocol)
	}
	if err := requireID(h.ConnectionID, "connection id"); err != nil {
		return err
	}
	if err := requireID(h.InstanceID, "daemon instance id"); err != nil {
		return err
	}
	if h.CredentialGeneration == 0 || h.RenewalIntervalMs == 0 || h.LeaseDurationMs == 0 {
		return errors.New("hello acceptance requires credential generation and lease timing")
	}
	if h.RenewalIntervalMs >= h.LeaseDurationMs {
		return errors.New("renewal interval must be shorter than lease duration")
	}
	return nil
}

// RunRequestMetadata keeps the wire package independent from Manager's
// canonical request type. Manager must strictly decode Request again.
type RunRequestMetadata struct {
	Schema    string          `json:"schema"`
	RequestID string          `json:"requestId"`
	Request   json.RawMessage `json:"request"`
}

func (r *RunRequestMetadata) Validate() error {
	if r == nil {
		return errors.New("run request metadata is nil")
	}
	if r.Schema != RunRequestSchema {
		return fmt.Errorf("unsupported run request schema %q", r.Schema)
	}
	if err := requireID(r.RequestID, "request id"); err != nil {
		return err
	}
	return requireJSONObject(r.Request, "run request")
}

type Confirm struct {
	PlanVersion string `json:"planVersion"`
	PlanDigest  string `json:"planDigest"`
	Accepted    bool   `json:"accepted"`
}

func (c *Confirm) Validate() error {
	if c == nil {
		return errors.New("confirmation is nil")
	}
	if err := requireText(c.PlanVersion, "plan version", 128); err != nil {
		return err
	}
	return requireSHA256(c.PlanDigest, "plan digest")
}

type Resize struct {
	Rows    uint16 `json:"rows"`
	Columns uint16 `json:"columns"`
}

func (r *Resize) Validate() error {
	if r == nil || r.Rows == 0 || r.Columns == 0 {
		return errors.New("resize requires non-zero rows and columns")
	}
	return nil
}

type Signal struct {
	Name string `json:"name"`
}

func (s *Signal) Validate() error {
	if s == nil {
		return errors.New("signal is nil")
	}
	switch s.Name {
	case "SIGHUP", "SIGINT", "SIGQUIT", "SIGTERM", "SIGTSTP", "SIGCONT", "SIGKILL":
		return nil
	default:
		return fmt.Errorf("unsupported portable signal %q", s.Name)
	}
}

type Renew struct {
	Token                string `json:"token"`
	CredentialGeneration uint64 `json:"credentialGeneration"`
}

func (r *Renew) Validate() error {
	if r == nil {
		return errors.New("renewal is nil")
	}
	if err := requireOpaque(r.Token, "operator token", 4096); err != nil {
		return err
	}
	if r.CredentialGeneration == 0 {
		return errors.New("renewal credential generation is required")
	}
	return nil
}

type Review struct {
	PlanVersion          string `json:"planVersion"`
	PlanDigest           string `json:"planDigest"`
	ConfirmationRequired bool   `json:"confirmationRequired"`
	Summary              string `json:"summary"`
}

func (r *Review) Validate() error {
	if r == nil {
		return errors.New("review is nil")
	}
	if err := requireText(r.PlanVersion, "plan version", 128); err != nil {
		return err
	}
	if err := requireSHA256(r.PlanDigest, "plan digest"); err != nil {
		return err
	}
	return requireText(r.Summary, "review summary", 8192)
}

type Started struct {
	SessionID         string             `json:"sessionId"`
	EnvironmentID     string             `json:"environmentId"`
	Terminal          TerminalDescriptor `json:"terminal"`
	RenewalIntervalMs uint32             `json:"renewalIntervalMs"`
}

func (s *Started) Validate() error {
	if s == nil {
		return errors.New("started control is nil")
	}
	if err := requireSessionID(s.SessionID); err != nil {
		return err
	}
	if err := requireID(s.EnvironmentID, "environment id"); err != nil {
		return err
	}
	if err := s.Terminal.Validate(); err != nil {
		return err
	}
	if s.RenewalIntervalMs == 0 {
		return errors.New("started control requires renewal interval")
	}
	return nil
}

type Notice struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
}

func (n *Notice) Validate() error {
	if n == nil {
		return errors.New("notice is nil")
	}
	if err := requireCode(n.Code); err != nil {
		return err
	}
	return requireText(n.Summary, "notice summary", 4096)
}

type Error struct {
	Code      string `json:"code"`
	Summary   string `json:"summary"`
	Retryable bool   `json:"retryable"`
}

func (e *Error) Validate() error {
	if e == nil {
		return errors.New("error control is nil")
	}
	if err := requireCode(e.Code); err != nil {
		return err
	}
	return requireText(e.Summary, "error summary", 4096)
}

type CompletionKind string

const (
	CompletionExit          CompletionKind = "exit"
	CompletionSignal        CompletionKind = "signal"
	CompletionCancelled     CompletionKind = "cancelled"
	CompletionProtocolError CompletionKind = "protocol-error"
	CompletionCleanupError  CompletionKind = "cleanup-error"
)

type Completion struct {
	Kind             CompletionKind  `json:"kind"`
	ExitCode         int             `json:"exitCode"`
	Signal           string          `json:"signal,omitempty"`
	TargetCompleted  bool            `json:"targetCompleted"`
	CleanupCompleted bool            `json:"cleanupCompleted"`
	SessionID        string          `json:"sessionId"`
	Summary          string          `json:"summary"`
	Result           json.RawMessage `json:"result,omitempty"`
}

func (c *Completion) Validate() error {
	if c == nil {
		return errors.New("completion is nil")
	}
	switch c.Kind {
	case CompletionExit, CompletionSignal, CompletionCancelled, CompletionProtocolError, CompletionCleanupError:
	default:
		return fmt.Errorf("unsupported completion kind %q", c.Kind)
	}
	if c.ExitCode < 0 || c.ExitCode > 255 {
		return errors.New("completion exit code must be between 0 and 255")
	}
	if c.Kind == CompletionSignal {
		if err := validateCompletionSignal(c.Signal); err != nil {
			return err
		}
	} else if c.Signal != "" {
		return errors.New("only signal completion may carry a signal")
	}
	if err := requireSessionID(c.SessionID); err != nil {
		return err
	}
	if len(c.Result) > 48<<10 {
		return errors.New("completion result exceeds limit")
	}
	if len(c.Result) != 0 && !json.Valid(c.Result) {
		return errors.New("completion result is invalid JSON")
	}
	return optionalText(c.Summary, "completion summary", 4096)
}

func validateCompletionSignal(name string) error {
	if len(name) < 4 || len(name) > 16 || !strings.HasPrefix(name, "SIG") {
		return fmt.Errorf("invalid completion signal %q", name)
	}
	for _, r := range name[3:] {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return fmt.Errorf("invalid completion signal %q", name)
		}
	}
	return nil
}

type SupervisorStart struct {
	Protocol            string                                    `json:"protocol"`
	SessionID           string                                    `json:"sessionId"`
	TargetUser          string                                    `json:"targetUser"`
	GuestWork           string                                    `json:"guestWork"`
	Argv                []string                                  `json:"argv"`
	Env                 map[string]string                         `json:"env,omitempty"`
	Terminal            TerminalDescriptor                        `json:"terminal"`
	ExpectedBootID      string                                    `json:"expectedBootId"`
	SessionSource       string                                    `json:"sessionSource"`
	ProjectionReadiness *SupervisorProjectionReadinessExpectation `json:"projectionReadiness,omitempty"`
}

type SupervisorProjectionReadinessExpectation struct {
	EnvironmentID     string `json:"environmentId"`
	SessionSnapshotID string `json:"sessionSnapshotId"`
	CatalogDigest     string `json:"catalogDigest"`
	ExpectedEntries   int    `json:"expectedEntries"`
	TargetProjected   bool   `json:"targetProjected"`
}

func (p *SupervisorProjectionReadinessExpectation) Validate() error {
	if p == nil {
		return errors.New("supervisor projection readiness expectation is nil")
	}
	if err := requireID(p.EnvironmentID, "projection environment id"); err != nil {
		return err
	}
	if err := requirePrefixedSHA256(p.SessionSnapshotID, "projection session snapshot id"); err != nil {
		return err
	}
	if err := requirePrefixedSHA256(p.CatalogDigest, "projection catalog digest"); err != nil {
		return err
	}
	if p.ExpectedEntries <= 0 || p.ExpectedEntries > 129 {
		return errors.New("projection readiness expected entry count is invalid")
	}
	return nil
}

func (s *SupervisorStart) Validate() error {
	if s == nil {
		return errors.New("supervisor start is nil")
	}
	if s.Protocol != SupervisorProtocol {
		return fmt.Errorf("unsupported supervisor protocol %q", s.Protocol)
	}
	if err := requireSessionID(s.SessionID); err != nil {
		return err
	}
	if !userPattern.MatchString(s.TargetUser) || s.TargetUser == "root" {
		return errors.New("supervisor target user must be a validated non-root user")
	}
	if err := requireCleanAbsolutePath(s.GuestWork, "guest work directory"); err != nil {
		return err
	}
	if len(s.Argv) == 0 || len(s.Argv) > 1024 {
		return errors.New("supervisor argv must contain 1 through 1024 arguments")
	}
	for _, arg := range s.Argv {
		if err := requireOpaque(arg, "supervisor argument", 8192); err != nil {
			return err
		}
	}
	if len(s.Env) > 1024 {
		return errors.New("supervisor environment has too many entries")
	}
	for key, value := range s.Env {
		if !environmentKeyPattern.MatchString(key) {
			return fmt.Errorf("invalid supervisor environment key %q", key)
		}
		if err := requireEnvironmentValue(value, 8192); err != nil {
			return err
		}
	}
	if err := s.Terminal.Validate(); err != nil {
		return err
	}
	if err := requireOpaque(s.ExpectedBootID, "expected boot id", 256); err != nil {
		return err
	}
	wantSource := "/hideout/runtime/sessions/" + s.SessionID
	if s.SessionSource != wantSource {
		return fmt.Errorf("session source must be %q", wantSource)
	}
	if s.ProjectionReadiness != nil {
		if err := s.ProjectionReadiness.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type SupervisorReady struct {
	Protocol            string                              `json:"protocol"`
	SessionID           string                              `json:"sessionId"`
	Terminal            TerminalDescriptor                  `json:"terminal"`
	ProjectionReadiness *SupervisorProjectionReadinessReady `json:"projectionReadiness,omitempty"`
}

type SupervisorProjectionReadinessReady struct {
	Status            string `json:"status"`
	EnvironmentID     string `json:"environmentId"`
	SessionSnapshotID string `json:"sessionSnapshotId"`
	CatalogDigest     string `json:"catalogDigest"`
	ExpectedEntries   int    `json:"expectedEntries"`
	ObservedEntries   int    `json:"observedEntries"`
	DurationMillis    int64  `json:"durationMs"`
	TargetProjected   bool   `json:"targetProjected"`
}

func (p *SupervisorProjectionReadinessReady) Validate() error {
	if p == nil {
		return errors.New("supervisor projection readiness result is nil")
	}
	if p.Status != "ready" {
		return errors.New("supervisor may report only ready projection status")
	}
	expectation := SupervisorProjectionReadinessExpectation{
		EnvironmentID: p.EnvironmentID, SessionSnapshotID: p.SessionSnapshotID,
		CatalogDigest: p.CatalogDigest, ExpectedEntries: p.ExpectedEntries,
		TargetProjected: p.TargetProjected,
	}
	if err := expectation.Validate(); err != nil {
		return err
	}
	if p.ObservedEntries != p.ExpectedEntries || p.DurationMillis < 0 || p.DurationMillis > 2000 {
		return errors.New("supervisor projection readiness observation is incomplete or unbounded")
	}
	return nil
}

func (r *SupervisorReady) Validate() error {
	if r == nil {
		return errors.New("supervisor ready is nil")
	}
	if r.Protocol != SupervisorProtocol {
		return fmt.Errorf("unsupported supervisor protocol %q", r.Protocol)
	}
	if err := requireSessionID(r.SessionID); err != nil {
		return err
	}
	if err := r.Terminal.Validate(); err != nil {
		return err
	}
	if r.ProjectionReadiness != nil {
		return r.ProjectionReadiness.Validate()
	}
	return nil
}

type emptyControl struct{}

func (*emptyControl) Validate() error { return nil }

func MarshalControl(control Control) ([]byte, error) {
	if control == nil {
		return nil, fmt.Errorf("%w: control is nil", ErrInvalidControl)
	}
	if err := control.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidControl, err)
	}
	payload, err := json.Marshal(control)
	if err != nil {
		return nil, fmt.Errorf("%w: encode JSON: %v", ErrInvalidControl, err)
	}
	if len(payload) > int(MaxPayloadSize) {
		return nil, fmt.Errorf("%w: control size=%d limit=%d", ErrPayloadTooLarge, len(payload), MaxPayloadSize)
	}
	return payload, nil
}

func UnmarshalControl(payload []byte, control Control) error {
	if control == nil {
		return fmt.Errorf("%w: control is nil", ErrInvalidControl)
	}
	if len(payload) > int(MaxPayloadSize) {
		return fmt.Errorf("%w: control size=%d limit=%d", ErrPayloadTooLarge, len(payload), MaxPayloadSize)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(control); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrInvalidControl, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: trailing JSON value", ErrInvalidControl)
		}
		return fmt.Errorf("%w: trailing JSON data: %v", ErrInvalidControl, err)
	}
	if err := control.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidControl, err)
	}
	return nil
}

func DecodeControl(frameType Type, payload []byte) (Control, error) {
	control, empty, err := controlForType(frameType)
	if err != nil {
		return nil, err
	}
	if empty {
		if len(payload) != 0 {
			return nil, fmt.Errorf("%w: %s payload must be empty", ErrInvalidControl, frameType)
		}
		return control, nil
	}
	if err := UnmarshalControl(payload, control); err != nil {
		return nil, err
	}
	return control, nil
}

func validateControlPayload(frameType Type, payload []byte) error {
	_, err := DecodeControl(frameType, payload)
	return err
}

func controlForType(frameType Type) (Control, bool, error) {
	switch frameType {
	case TypeHello:
		return &Hello{}, false, nil
	case TypeHelloAccepted:
		return &HelloAccepted{}, false, nil
	case TypeRunRequest:
		return &RunRequestMetadata{}, false, nil
	case TypeConfirm:
		return &Confirm{}, false, nil
	case TypeReview:
		return &Review{}, false, nil
	case TypeStarted:
		return &Started{}, false, nil
	case TypeStdinEOF, TypeCancel, TypeSupervisorCommit, TypeHeartbeat:
		return &emptyControl{}, true, nil
	case TypeResize:
		return &Resize{}, false, nil
	case TypeSignal:
		return &Signal{}, false, nil
	case TypeRenew:
		return &Renew{}, false, nil
	case TypeNotice:
		return &Notice{}, false, nil
	case TypeError, TypeSupervisorError:
		return &Error{}, false, nil
	case TypeCompletion:
		return &Completion{}, false, nil
	case TypeSupervisorStart:
		return &SupervisorStart{}, false, nil
	case TypeSupervisorReady:
		return &SupervisorReady{}, false, nil
	default:
		return nil, false, fmt.Errorf("%w: %s is not a JSON control frame", ErrInvalidControl, frameType)
	}
}

func requireID(value, name string) error {
	if len(value) == 0 || len(value) > 128 || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s is required and bounded", name)
	}
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._:-+", r) {
			return fmt.Errorf("%s contains an invalid character", name)
		}
	}
	return nil
}

func requireSessionID(value string) error {
	if !strings.HasPrefix(value, "ses_") {
		return errors.New("session id must start with ses_")
	}
	return requireID(value, "session id")
}

func requireCode(value string) error {
	if !codePattern.MatchString(value) {
		return fmt.Errorf("invalid control code %q", value)
	}
	return nil
}

func requireSHA256(value, name string) error {
	if len(value) != 64 {
		return fmt.Errorf("%s must be a lowercase SHA-256", name)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 || strings.ToLower(value) != value {
		return fmt.Errorf("%s must be a lowercase SHA-256", name)
	}
	return nil
}

func requirePrefixedSHA256(value, name string) error {
	if !strings.HasPrefix(value, "sha256:") {
		return fmt.Errorf("%s must use the sha256 prefix", name)
	}
	return requireSHA256(strings.TrimPrefix(value, "sha256:"), name)
}

func requireOpaque(value, name string, limit int) error {
	if value == "" || len(value) > limit || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%s is required, NUL-free, and bounded", name)
	}
	return nil
}

func requireEnvironmentValue(value string, limit int) error {
	if len(value) > limit || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("supervisor environment value must be NUL-free and bounded")
	}
	return nil
}

func requireText(value, name string, limit int) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	return safeText(value, name, limit)
}

func optionalText(value, name string, limit int) error {
	if value == "" {
		return nil
	}
	return safeText(value, name, limit)
}

func safeText(value, name string, limit int) error {
	if len(value) > limit || !utf8.ValidString(value) {
		return fmt.Errorf("%s is invalid or exceeds %d bytes", name, limit)
	}
	for _, r := range value {
		if r == '\n' || r == '\t' {
			continue
		}
		if !unicode.IsPrint(r) || r == '\u001b' {
			return fmt.Errorf("%s contains unsafe control text", name)
		}
	}
	return nil
}

func requireJSONObject(value []byte, name string) error {
	if len(value) == 0 {
		return fmt.Errorf("%s is required", name)
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return fmt.Errorf("%s must be one JSON object", name)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s must contain one JSON value", name)
	}
	return nil
}

func requireCleanAbsolutePath(value, name string) error {
	if value == "" || !path.IsAbs(value) || path.Clean(value) != value || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%s must be a clean absolute path", name)
	}
	return nil
}
