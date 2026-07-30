package sessionwire

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"sync"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	ObserverWireSchema = "hideout.observer-wire.v1"
	ObservationSchema  = "hideout.observation.v1"

	ObserverFrameHeaderSize            = 8
	MaxObserverFrameSize        uint32 = 1 << 20
	MaxObserverQueuedFrameBytes        = int(MaxObserverFrameSize) + ObserverFrameHeaderSize
	ObserverTransportCPU        uint64 = math.MaxUint32
	ObserverControlCPU          uint64 = math.MaxUint32 - 1
	ObserverStreamTokenBytes           = 32
	DefaultObserverQueueBytes          = 16 << 20
	MaxObserverQueueBytes              = 64 << 20
	maxObserverQueueEntries            = 65536
)

var (
	ErrObserverSchema             = errors.New("observer schema is invalid")
	ErrObserverKind               = errors.New("observer observation kind is invalid")
	ErrObserverIdentity           = errors.New("observer identity does not match the active boundary")
	ErrObserverTargetAuthority    = errors.New("observer endpoint is controlled by the target")
	ErrObserverAuthentication     = errors.New("observer authentication failed")
	ErrObserverCRC                = errors.New("observer frame checksum is invalid")
	ErrObserverFrameTooLarge      = errors.New("observer frame exceeds the size limit")
	ErrObserverTruncated          = errors.New("observer frame is truncated")
	ErrObserverGenerationRollback = errors.New("observer generation rolled back")
	ErrObserverBackpressure       = errors.New("observer send queue exceeded its bound")
	ErrObserverQueueClosed        = errors.New("observer send queue is closed")
)

const (
	ObserverStreamOpenType = "observer.stream.open"
	ObserverHelloType      = "observer.hello"
	ObserverAcceptedType   = "observer.accepted"
)

// ObserverStreamToken is daemon-issued, single-session authority for opening
// the dedicated observer channel. Its String method is deliberately redacted
// so diagnostics cannot accidentally copy the credential into logs.
type ObserverStreamToken [ObserverStreamTokenBytes]byte

func NewObserverStreamToken() (ObserverStreamToken, error) {
	var token ObserverStreamToken
	if _, err := io.ReadFull(rand.Reader, token[:]); err != nil {
		return ObserverStreamToken{}, fmt.Errorf("generate observer stream token: %w", err)
	}
	if err := token.Validate(); err != nil {
		token.Destroy()
		return ObserverStreamToken{}, err
	}
	return token, nil
}

func (token ObserverStreamToken) Validate() error {
	var zero ObserverStreamToken
	if subtle.ConstantTimeCompare(token[:], zero[:]) == 1 {
		return fmt.Errorf("%w: observer stream token is empty", ErrObserverAuthentication)
	}
	return nil
}

func (token ObserverStreamToken) Equal(other ObserverStreamToken) bool {
	return subtle.ConstantTimeCompare(token[:], other[:]) == 1
}

func (token ObserverStreamToken) String() string {
	return "[REDACTED observer stream token]"
}

func (token ObserverStreamToken) GoString() string {
	return "sessionwire.ObserverStreamToken([REDACTED])"
}

func (token ObserverStreamToken) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, token.String())
}

func (token ObserverStreamToken) MarshalJSON() ([]byte, error) {
	if err := token.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(base64.RawURLEncoding.EncodeToString(token[:]))
}

func (token *ObserverStreamToken) UnmarshalJSON(data []byte) error {
	if token == nil {
		return fmt.Errorf("%w: observer stream token target is nil", ErrObserverAuthentication)
	}
	token.Destroy()
	var encoded string
	if err := json.Unmarshal(data, &encoded); err != nil {
		return fmt.Errorf("%w: decode observer stream token", ErrObserverAuthentication)
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) != ObserverStreamTokenBytes ||
		base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		clear(decoded)
		return fmt.Errorf("%w: observer stream token encoding", ErrObserverAuthentication)
	}
	copy(token[:], decoded)
	clear(decoded)
	if err := token.Validate(); err != nil {
		token.Destroy()
		return err
	}
	return nil
}

func (token *ObserverStreamToken) Destroy() {
	if token != nil {
		clear(token[:])
	}
}

type ObserverStreamOpen struct {
	Type      string              `json:"type"`
	Schema    string              `json:"schema"`
	SessionID string              `json:"sessionId"`
	Token     ObserverStreamToken `json:"token"`
}

func (open ObserverStreamOpen) Validate() error {
	if open.Type != ObserverStreamOpenType || open.Schema != ObserverWireSchema {
		return fmt.Errorf("%w: observer stream open discriminator", ErrObserverSchema)
	}
	if err := requireSessionID(open.SessionID); err != nil {
		return fmt.Errorf("%w: %v", ErrObserverIdentity, err)
	}
	return open.Token.Validate()
}

type ObserverStreamPeer struct {
	UID              uint32
	TargetControlled bool
}

func AuthenticateObserverStreamOpen(
	open ObserverStreamOpen,
	expectedSessionID string,
	expectedToken ObserverStreamToken,
	peer ObserverStreamPeer,
) error {
	if peer.UID != 0 || peer.TargetControlled {
		return ErrObserverTargetAuthority
	}
	if err := requireSessionID(expectedSessionID); err != nil {
		return fmt.Errorf("%w: %v", ErrObserverIdentity, err)
	}
	if err := expectedToken.Validate(); err != nil {
		return err
	}
	if err := open.Validate(); err != nil {
		return err
	}
	if open.SessionID != expectedSessionID {
		return ErrObserverIdentity
	}
	if !open.Token.Equal(expectedToken) {
		return ErrObserverAuthentication
	}
	return nil
}

func WriteObserverStreamOpen(writer io.Writer, open ObserverStreamOpen) error {
	if err := open.Validate(); err != nil {
		return err
	}
	return writeObserverJSONFrame(writer, open)
}

func ReadObserverStreamOpen(reader io.Reader) (ObserverStreamOpen, error) {
	var open ObserverStreamOpen
	if err := readObserverJSONFrame(reader, &open); err != nil {
		return ObserverStreamOpen{}, err
	}
	if err := open.Validate(); err != nil {
		open.Token.Destroy()
		return ObserverStreamOpen{}, err
	}
	return open, nil
}

// ObserverBinding is daemon-issued authority for one active workload
// boundary. GuestBootID is execution identity, while retention ownership
// remains independent of a guest restart.
type ObserverBinding struct {
	Owner                workloadtypes.ActivityOwner `json:"owner"`
	SessionID            string                      `json:"sessionId"`
	EnvironmentID        string                      `json:"environmentId"`
	BackendIncarnationID string                      `json:"backendIncarnationId"`
	GuestBootID          string                      `json:"guestBootId"`
	CgroupID             uint64                      `json:"cgroupId"`
	ObserverGeneration   uint64                      `json:"observerGeneration"`
}

func (binding ObserverBinding) Validate() error {
	if err := binding.Owner.Validate(); err != nil {
		return fmt.Errorf("%w: owner: %v", ErrObserverIdentity, err)
	}
	if err := requireSessionID(binding.SessionID); err != nil {
		return fmt.Errorf("%w: %v", ErrObserverIdentity, err)
	}
	if err := requireID(binding.EnvironmentID, "observer environment id"); err != nil {
		return fmt.Errorf("%w: %v", ErrObserverIdentity, err)
	}
	if err := requireOpaque(binding.BackendIncarnationID, "backend incarnation id", 256); err != nil {
		return fmt.Errorf("%w: %v", ErrObserverIdentity, err)
	}
	if err := requireOpaque(binding.GuestBootID, "guest boot id", 256); err != nil {
		return fmt.Errorf("%w: %v", ErrObserverIdentity, err)
	}
	if binding.CgroupID == 0 || binding.ObserverGeneration == 0 {
		return fmt.Errorf("%w: cgroup and observer generation are required", ErrObserverIdentity)
	}
	if binding.Owner.BackendIncarnationID != binding.BackendIncarnationID {
		return fmt.Errorf("%w: backend incarnation differs from owner", ErrObserverIdentity)
	}
	switch binding.Owner.Kind {
	case workloadtypes.OwnerReusableEnvironment:
		if binding.Owner.EnvironmentID != binding.EnvironmentID {
			return fmt.Errorf("%w: environment differs from reusable owner", ErrObserverIdentity)
		}
	case workloadtypes.OwnerDisposableSession:
		if binding.Owner.SessionID != binding.SessionID {
			return fmt.Errorf("%w: session differs from disposable owner", ErrObserverIdentity)
		}
	default:
		return ErrObserverIdentity
	}
	return nil
}

type ObserverCapability struct {
	State    string   `json:"state"`
	Reason   string   `json:"reason,omitempty"`
	Evidence []string `json:"evidence"`
}

func (capability ObserverCapability) Validate() error {
	switch capability.State {
	case workloadtypes.CoverageAvailable, workloadtypes.CoveragePartial,
		workloadtypes.CoverageUnavailable:
	default:
		return fmt.Errorf("%w: capability state", ErrObserverSchema)
	}
	if capability.Reason != "" {
		if err := requireCode(capability.Reason); err != nil {
			return fmt.Errorf("%w: capability reason: %v", ErrObserverSchema, err)
		}
	}
	if len(capability.Evidence) > 64 {
		return fmt.Errorf("%w: capability evidence exceeds the bound", ErrObserverSchema)
	}
	seen := make(map[string]struct{}, len(capability.Evidence))
	for _, evidence := range capability.Evidence {
		if err := requireCode(evidence); err != nil {
			return fmt.Errorf("%w: capability evidence: %v", ErrObserverSchema, err)
		}
		if _, exists := seen[evidence]; exists {
			return fmt.Errorf("%w: duplicate capability evidence", ErrObserverSchema)
		}
		seen[evidence] = struct{}{}
	}
	return nil
}

type ObserverCapabilities struct {
	Process ObserverCapability `json:"process"`
	File    ObserverCapability `json:"file"`
	Network ObserverCapability `json:"network"`
	DNS     ObserverCapability `json:"dns"`
}

func (capabilities ObserverCapabilities) Validate() error {
	for name, capability := range map[string]ObserverCapability{
		workloadtypes.SubsystemProcess: capabilities.Process,
		workloadtypes.SubsystemFile:    capabilities.File,
		workloadtypes.SubsystemNetwork: capabilities.Network,
		workloadtypes.SubsystemDNS:     capabilities.DNS,
	} {
		if err := capability.Validate(); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrObserverSchema, name, err)
		}
	}
	return nil
}

func (capabilities ObserverCapabilities) Coverage() []SupervisorCoverageSummary {
	values := []struct {
		subsystem string
		value     ObserverCapability
	}{
		{workloadtypes.SubsystemProcess, capabilities.Process},
		{workloadtypes.SubsystemFile, capabilities.File},
		{workloadtypes.SubsystemNetwork, capabilities.Network},
		{workloadtypes.SubsystemDNS, capabilities.DNS},
	}
	result := make([]SupervisorCoverageSummary, 0, len(values))
	for _, item := range values {
		reason := item.value.Reason
		if reason == "" {
			switch item.value.State {
			case workloadtypes.CoverageAvailable:
				reason = "collector-ready"
			case workloadtypes.CoveragePartial:
				reason = "collector-partial"
			default:
				reason = "collector-unavailable"
			}
		}
		result = append(result, SupervisorCoverageSummary{
			Subsystem: item.subsystem,
			State:     item.value.State,
			Reason:    reason,
			Evidence:  append([]string(nil), item.value.Evidence...),
		})
	}
	return result
}

type ObserverHello struct {
	Type                 string                      `json:"type"`
	Schema               string                      `json:"schema"`
	Owner                workloadtypes.ActivityOwner `json:"owner"`
	SessionID            string                      `json:"sessionId"`
	EnvironmentID        string                      `json:"environmentId"`
	BackendIncarnationID string                      `json:"backendIncarnationId"`
	GuestBootID          string                      `json:"guestBootId"`
	CgroupID             uint64                      `json:"cgroupId"`
	ObserverGeneration   uint64                      `json:"observerGeneration"`
	HelperDigest         string                      `json:"helperDigest"`
	Capabilities         ObserverCapabilities        `json:"capabilities"`
}

func (hello ObserverHello) binding() ObserverBinding {
	return ObserverBinding{
		Owner: hello.Owner, SessionID: hello.SessionID,
		EnvironmentID: hello.EnvironmentID, BackendIncarnationID: hello.BackendIncarnationID,
		GuestBootID: hello.GuestBootID, CgroupID: hello.CgroupID,
		ObserverGeneration: hello.ObserverGeneration,
	}
}

func (hello ObserverHello) Validate() error {
	if hello.Type != ObserverHelloType || hello.Schema != ObserverWireSchema {
		return fmt.Errorf("%w: observer hello discriminator", ErrObserverSchema)
	}
	if err := hello.binding().Validate(); err != nil {
		return err
	}
	if err := requirePrefixedSHA256(hello.HelperDigest, "observer helper digest"); err != nil {
		return fmt.Errorf("%w: %v", ErrObserverAuthentication, err)
	}
	return hello.Capabilities.Validate()
}

// ObserverPeer contains identity derived from the privileged transport and
// packaged helper, never from the hello payload.
type ObserverPeer struct {
	UID              uint32
	TargetControlled bool
	HelperDigest     string
}

type ObserverAccepted struct {
	Type                 string                      `json:"type"`
	Schema               string                      `json:"schema"`
	Owner                workloadtypes.ActivityOwner `json:"owner"`
	SessionID            string                      `json:"sessionId"`
	CgroupID             uint64                      `json:"cgroupId"`
	ObserverGeneration   uint64                      `json:"observerGeneration"`
	ExpectedNextSequence uint64                      `json:"expectedNextSequence"`
	MaxFrameBytes        uint32                      `json:"maxFrameBytes"`
}

func (accepted ObserverAccepted) Validate() error {
	if accepted.Type != ObserverAcceptedType || accepted.Schema != ObserverWireSchema ||
		accepted.Owner.Validate() != nil {
		return fmt.Errorf("%w: observer acceptance", ErrObserverSchema)
	}
	if err := requireSessionID(accepted.SessionID); err != nil {
		return fmt.Errorf("%w: %v", ErrObserverIdentity, err)
	}
	if accepted.CgroupID == 0 || accepted.ObserverGeneration == 0 ||
		accepted.ExpectedNextSequence == 0 ||
		accepted.MaxFrameBytes == 0 || accepted.MaxFrameBytes > MaxObserverFrameSize {
		return fmt.Errorf("%w: observer acceptance bounds", ErrObserverSchema)
	}
	return nil
}

func (accepted ObserverAccepted) ValidateBinding(binding ObserverBinding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	if err := accepted.Validate(); err != nil {
		return err
	}
	if !accepted.Owner.Equal(binding.Owner) ||
		accepted.SessionID != binding.SessionID ||
		accepted.CgroupID != binding.CgroupID ||
		accepted.ObserverGeneration != binding.ObserverGeneration ||
		accepted.ExpectedNextSequence != 1 ||
		accepted.MaxFrameBytes != MaxObserverFrameSize {
		return ErrObserverIdentity
	}
	return nil
}

func AcceptObserverHello(
	hello ObserverHello,
	expected ObserverBinding,
	peer ObserverPeer,
) (ObserverAccepted, error) {
	if peer.UID != 0 || peer.TargetControlled {
		return ObserverAccepted{}, ErrObserverTargetAuthority
	}
	if err := requirePrefixedSHA256(peer.HelperDigest, "observer peer helper digest"); err != nil {
		return ObserverAccepted{}, fmt.Errorf("%w: %v", ErrObserverAuthentication, err)
	}
	if err := hello.Validate(); err != nil {
		return ObserverAccepted{}, err
	}
	if hello.HelperDigest != peer.HelperDigest {
		return ObserverAccepted{}, ErrObserverAuthentication
	}
	if err := expected.Validate(); err != nil {
		return ObserverAccepted{}, err
	}
	actual := hello.binding()
	if !actual.Owner.Equal(expected.Owner) ||
		actual.SessionID != expected.SessionID ||
		actual.EnvironmentID != expected.EnvironmentID ||
		actual.BackendIncarnationID != expected.BackendIncarnationID ||
		actual.GuestBootID != expected.GuestBootID ||
		actual.CgroupID != expected.CgroupID ||
		actual.ObserverGeneration != expected.ObserverGeneration {
		return ObserverAccepted{}, ErrObserverIdentity
	}
	accepted := ObserverAccepted{
		Type: ObserverAcceptedType, Schema: ObserverWireSchema,
		Owner: expected.Owner, SessionID: expected.SessionID,
		CgroupID: expected.CgroupID, ObserverGeneration: expected.ObserverGeneration,
		ExpectedNextSequence: 1, MaxFrameBytes: MaxObserverFrameSize,
	}
	return accepted, accepted.Validate()
}

func WriteObserverHello(writer io.Writer, hello ObserverHello) error {
	if err := hello.Validate(); err != nil {
		return err
	}
	return writeObserverJSONFrame(writer, hello)
}

func ReadObserverHello(reader io.Reader) (ObserverHello, error) {
	var hello ObserverHello
	if err := readObserverJSONFrame(reader, &hello); err != nil {
		return ObserverHello{}, err
	}
	if err := hello.Validate(); err != nil {
		return ObserverHello{}, err
	}
	return hello, nil
}

func WriteObserverAccepted(writer io.Writer, accepted ObserverAccepted) error {
	if err := accepted.Validate(); err != nil {
		return err
	}
	return writeObserverJSONFrame(writer, accepted)
}

func ReadObserverAccepted(reader io.Reader) (ObserverAccepted, error) {
	var accepted ObserverAccepted
	if err := readObserverJSONFrame(reader, &accepted); err != nil {
		return ObserverAccepted{}, err
	}
	if err := accepted.Validate(); err != nil {
		return ObserverAccepted{}, err
	}
	return accepted, nil
}

type ObservationEnvelope struct {
	Schema             string                      `json:"schema"`
	Owner              workloadtypes.ActivityOwner `json:"owner"`
	SessionID          string                      `json:"sessionId"`
	CgroupID           uint64                      `json:"cgroupId"`
	ObserverGeneration uint64                      `json:"observerGeneration"`
	CPU                uint64                      `json:"cpu"`
	Sequence           uint64                      `json:"sequence"`
	MonotonicNS        uint64                      `json:"monotonicNs"`
	Kind               string                      `json:"kind"`
	Payload            json.RawMessage             `json:"payload"`
}

var observerKinds = map[string]struct{}{
	"process.fork": {}, "process.exec": {}, "process.exit": {},
	"process.execution": {},
	"file.open":         {}, "file.access": {}, "file.read": {}, "file.write": {},
	"file.metadata": {}, "file.mmap": {}, "file.create": {},
	"file.truncate": {}, "file.unlink": {}, "file.rename": {},
	"file.hardlink": {}, "file.symlink": {}, "file.mkdir": {}, "file.rmdir": {},
	"network.connect": {}, "network.close": {},
	"dns.query": {}, "dns.response": {}, "proxy.target": {},
	"coverage.changed": {}, "collector.loss": {}, "collector.heartbeat": {},
}

func (envelope ObservationEnvelope) Validate() error {
	if envelope.Schema != ObservationSchema {
		return fmt.Errorf("%w: observation schema", ErrObserverSchema)
	}
	if err := envelope.Owner.Validate(); err != nil {
		return fmt.Errorf("%w: owner: %v", ErrObserverIdentity, err)
	}
	if err := requireSessionID(envelope.SessionID); err != nil {
		return fmt.Errorf("%w: %v", ErrObserverIdentity, err)
	}
	if envelope.Owner.Kind == workloadtypes.OwnerDisposableSession &&
		envelope.Owner.SessionID != envelope.SessionID {
		return fmt.Errorf("%w: disposable owner session", ErrObserverIdentity)
	}
	if envelope.CgroupID == 0 || envelope.ObserverGeneration == 0 ||
		envelope.Sequence == 0 || envelope.MonotonicNS == 0 {
		return fmt.Errorf("%w: observation identity or ordering", ErrObserverIdentity)
	}
	if envelope.CPU > math.MaxUint32 {
		return fmt.Errorf("%w: cpu exceeds the supported bound", ErrObserverSchema)
	}
	if _, ok := observerKinds[envelope.Kind]; !ok {
		return fmt.Errorf("%w: %q", ErrObserverKind, envelope.Kind)
	}
	if envelope.CPU == ObserverTransportCPU && envelope.Kind != "collector.loss" {
		return fmt.Errorf("%w: reserved observer transport cpu", ErrObserverSchema)
	}
	payload := bytes.TrimSpace(envelope.Payload)
	if len(payload) < 2 || payload[0] != '{' || payload[len(payload)-1] != '}' ||
		!json.Valid(payload) {
		return fmt.Errorf("%w: observation payload must be one JSON object", ErrObserverSchema)
	}
	return nil
}

func WriteObserverEnvelope(writer io.Writer, envelope ObservationEnvelope) error {
	if writer == nil {
		return errors.New("observer writer is nil")
	}
	if err := envelope.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("%w: encode observation: %v", ErrObserverSchema, err)
	}
	defer clear(payload)
	if len(payload) > int(MaxObserverFrameSize) {
		return fmt.Errorf("%w: size=%d limit=%d", ErrObserverFrameTooLarge, len(payload), MaxObserverFrameSize)
	}
	header := make([]byte, ObserverFrameHeaderSize)
	binary.BigEndian.PutUint32(header[:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(header[4:], crc32.Checksum(payload, crc32.MakeTable(crc32.Castagnoli)))
	if err := writeObserverBytes(writer, header); err != nil {
		return err
	}
	return writeObserverBytes(writer, payload)
}

func ReadObserverEnvelope(reader io.Reader) (ObservationEnvelope, error) {
	if reader == nil {
		return ObservationEnvelope{}, errors.New("observer reader is nil")
	}
	header := make([]byte, ObserverFrameHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return ObservationEnvelope{}, fmt.Errorf("%w: header: %v", ErrObserverTruncated, err)
	}
	size := binary.BigEndian.Uint32(header[:4])
	if size == 0 {
		return ObservationEnvelope{}, fmt.Errorf("%w: empty payload", ErrObserverSchema)
	}
	if size > MaxObserverFrameSize {
		return ObservationEnvelope{}, fmt.Errorf("%w: size=%d limit=%d", ErrObserverFrameTooLarge, size, MaxObserverFrameSize)
	}
	payload := make([]byte, size)
	defer clear(payload)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return ObservationEnvelope{}, fmt.Errorf("%w: payload: %v", ErrObserverTruncated, err)
	}
	checksum := binary.BigEndian.Uint32(header[4:])
	if crc32.Checksum(payload, crc32.MakeTable(crc32.Castagnoli)) != checksum {
		return ObservationEnvelope{}, ErrObserverCRC
	}
	var envelope ObservationEnvelope
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return ObservationEnvelope{}, fmt.Errorf("%w: decode observation: %v", ErrObserverSchema, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ObservationEnvelope{}, fmt.Errorf("%w: trailing observation JSON", ErrObserverSchema)
	}
	if err := envelope.Validate(); err != nil {
		return ObservationEnvelope{}, err
	}
	return envelope, nil
}

func writeObserverJSONFrame(writer io.Writer, value any) error {
	if writer == nil {
		return errors.New("observer writer is nil")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: encode observer control: %v", ErrObserverSchema, err)
	}
	defer clear(payload)
	if len(payload) == 0 || len(payload) > int(MaxObserverFrameSize) {
		return fmt.Errorf("%w: size=%d limit=%d", ErrObserverFrameTooLarge, len(payload), MaxObserverFrameSize)
	}
	header := make([]byte, ObserverFrameHeaderSize)
	binary.BigEndian.PutUint32(header[:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(header[4:], crc32.Checksum(payload, crc32.MakeTable(crc32.Castagnoli)))
	if err := writeObserverBytes(writer, header); err != nil {
		return err
	}
	return writeObserverBytes(writer, payload)
}

func readObserverJSONFrame(reader io.Reader, target any) error {
	if reader == nil || target == nil {
		return errors.New("observer control endpoint is nil")
	}
	header := make([]byte, ObserverFrameHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return fmt.Errorf("%w: control header: %v", ErrObserverTruncated, err)
	}
	size := binary.BigEndian.Uint32(header[:4])
	if size == 0 {
		return fmt.Errorf("%w: empty observer control", ErrObserverSchema)
	}
	if size > MaxObserverFrameSize {
		return fmt.Errorf("%w: size=%d limit=%d", ErrObserverFrameTooLarge, size, MaxObserverFrameSize)
	}
	payload := make([]byte, size)
	defer clear(payload)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return fmt.Errorf("%w: control payload: %v", ErrObserverTruncated, err)
	}
	checksum := binary.BigEndian.Uint32(header[4:])
	if crc32.Checksum(payload, crc32.MakeTable(crc32.Castagnoli)) != checksum {
		return ErrObserverCRC
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: decode observer control: %v", ErrObserverSchema, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing observer control JSON", ErrObserverSchema)
	}
	return nil
}

func writeObserverBytes(writer io.Writer, value []byte) error {
	for len(value) != 0 {
		n, err := writer.Write(value)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(value) {
			return io.ErrShortWrite
		}
		value = value[n:]
	}
	return nil
}

type ObserverSequenceDisposition string

const (
	ObserverSequenceAccepted  ObserverSequenceDisposition = "accepted"
	ObserverSequenceDuplicate ObserverSequenceDisposition = "duplicate"
	ObserverSequenceGap       ObserverSequenceDisposition = "gap"
	ObserverSequenceRestart   ObserverSequenceDisposition = "restart"
)

type ObserverSequenceResult struct {
	Disposition ObserverSequenceDisposition
	CPU         uint64
	Sequence    uint64
	MissingFrom uint64
	MissingTo   uint64
}

type ObserverSequenceTracker struct {
	mu         sync.Mutex
	binding    ObserverBinding
	generation uint64
	byCPU      map[uint64]uint64
}

func NewObserverSequenceTracker(binding ObserverBinding) (*ObserverSequenceTracker, error) {
	if err := binding.Validate(); err != nil {
		return nil, err
	}
	return &ObserverSequenceTracker{
		binding: binding, generation: binding.ObserverGeneration,
		byCPU: make(map[uint64]uint64),
	}, nil
}

func (tracker *ObserverSequenceTracker) Observe(
	envelope ObservationEnvelope,
) (ObserverSequenceResult, error) {
	if tracker == nil {
		return ObserverSequenceResult{}, errors.New("observer sequence tracker is nil")
	}
	if err := envelope.Validate(); err != nil {
		return ObserverSequenceResult{}, err
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if !envelope.Owner.Equal(tracker.binding.Owner) ||
		envelope.SessionID != tracker.binding.SessionID ||
		envelope.CgroupID != tracker.binding.CgroupID {
		return ObserverSequenceResult{}, ErrObserverIdentity
	}
	if envelope.ObserverGeneration < tracker.generation {
		return ObserverSequenceResult{}, ErrObserverGenerationRollback
	}
	result := ObserverSequenceResult{CPU: envelope.CPU, Sequence: envelope.Sequence}
	if envelope.ObserverGeneration > tracker.generation {
		tracker.generation = envelope.ObserverGeneration
		tracker.byCPU = map[uint64]uint64{envelope.CPU: envelope.Sequence}
		result.Disposition = ObserverSequenceRestart
		if envelope.Sequence > 1 {
			result.MissingFrom = 1
			result.MissingTo = envelope.Sequence - 1
		}
		return result, nil
	}
	last := tracker.byCPU[envelope.CPU]
	switch {
	case envelope.Sequence <= last:
		result.Disposition = ObserverSequenceDuplicate
	case envelope.Sequence == last+1:
		result.Disposition = ObserverSequenceAccepted
		tracker.byCPU[envelope.CPU] = envelope.Sequence
	default:
		result.Disposition = ObserverSequenceGap
		result.MissingFrom = last + 1
		result.MissingTo = envelope.Sequence - 1
		tracker.byCPU[envelope.CPU] = envelope.Sequence
	}
	return result, nil
}

type ObserverLossSummary struct {
	Dropped      uint64 `json:"dropped"`
	DroppedBytes uint64 `json:"droppedBytes"`
	Reason       string `json:"reason,omitempty"`
}

type ObserverQueue struct {
	mu           sync.Mutex
	capacity     int
	byteLimit    int
	bytes        int
	values       [][]byte
	dropped      uint64
	droppedBytes uint64
	notify       chan struct{}
	done         chan struct{}
	closed       bool
}

func NewObserverQueue(capacity int) (*ObserverQueue, error) {
	return NewObserverQueueWithByteLimit(capacity, DefaultObserverQueueBytes)
}

func NewObserverQueueWithByteLimit(capacity, byteLimit int) (*ObserverQueue, error) {
	if capacity <= 0 || capacity > maxObserverQueueEntries {
		return nil, errors.New("observer queue capacity is invalid")
	}
	if byteLimit <= 0 || byteLimit > MaxObserverQueueBytes {
		return nil, errors.New("observer queue byte limit is invalid")
	}
	return &ObserverQueue{
		capacity:  capacity,
		byteLimit: byteLimit,
		values:    make([][]byte, 0, capacity),
		notify:    make(chan struct{}, 1),
		done:      make(chan struct{}),
	}, nil
}

func (queue *ObserverQueue) Enqueue(value []byte) error {
	if queue == nil {
		return errors.New("observer queue is nil")
	}
	if len(value) == 0 || len(value) > MaxObserverQueuedFrameBytes {
		return ErrObserverFrameTooLarge
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.closed {
		return ErrObserverQueueClosed
	}
	if len(queue.values) >= queue.capacity || len(value) > queue.byteLimit-queue.bytes {
		if queue.dropped != math.MaxUint64 {
			queue.dropped++
		}
		size := uint64(len(value))
		if size > math.MaxUint64-queue.droppedBytes {
			queue.droppedBytes = math.MaxUint64
		} else {
			queue.droppedBytes += size
		}
		queue.signalLocked()
		return ErrObserverBackpressure
	}
	queue.values = append(queue.values, append([]byte(nil), value...))
	queue.bytes += len(value)
	queue.signalLocked()
	return nil
}

func (queue *ObserverQueue) Dequeue() ([]byte, bool) {
	if queue == nil {
		return nil, false
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if len(queue.values) == 0 {
		return nil, false
	}
	value := queue.values[0]
	copy(queue.values, queue.values[1:])
	queue.values[len(queue.values)-1] = nil
	queue.values = queue.values[:len(queue.values)-1]
	queue.bytes -= len(value)
	return value, true
}

// LossSummary uses storage independent of the data queue, so reporting loss
// cannot overwrite the observation whose preservation exposed backpressure.
// Reading acknowledges the accumulated counter exactly once.
func (queue *ObserverQueue) LossSummary() ObserverLossSummary {
	if queue == nil {
		return ObserverLossSummary{}
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	dropped := queue.dropped
	droppedBytes := queue.droppedBytes
	queue.dropped = 0
	queue.droppedBytes = 0
	if dropped == 0 {
		return ObserverLossSummary{}
	}
	return ObserverLossSummary{
		Dropped:      dropped,
		DroppedBytes: droppedBytes,
		Reason:       "observer-send-queue-overflow",
	}
}

func (queue *ObserverQueue) Notify() <-chan struct{} {
	if queue == nil {
		return nil
	}
	return queue.notify
}

func (queue *ObserverQueue) Done() <-chan struct{} {
	if queue == nil {
		return nil
	}
	return queue.done
}

func (queue *ObserverQueue) Close() {
	if queue == nil {
		return
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if queue.closed {
		return
	}
	queue.closed = true
	for index := range queue.values {
		clear(queue.values[index])
		queue.values[index] = nil
	}
	queue.values = nil
	queue.bytes = 0
	close(queue.done)
	queue.signalLocked()
}

func (queue *ObserverQueue) signalLocked() {
	select {
	case queue.notify <- struct{}{}:
	default:
	}
}
