//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/sessionwire"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestObserverStreamBridgeUsesKernelRootCredentialAndSeparateFullDuplexPipe(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("kernel root peer-credential proof requires a root test process")
	}
	binding, hello, token := observerRelayFixture(t)
	relay, err := newObserverRelay(binding, hello, token, observerRelayOptions{
		Root:          shortObserverRelayRoot(t),
		QueueEntries:  4,
		QueueBytes:    4096,
		HandshakeWait: time.Second,
		MonotonicNS:   func() (uint64, error) { return 5, nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	bridgeInput, daemonWriter := io.Pipe()
	daemonReader, bridgeOutput := io.Pipe()
	bridgeDone := make(chan error, 1)
	go func() {
		bridgeDone <- runObserverStreamBridge(
			binding.SessionID,
			relay.root,
			bridgeInput,
			bridgeOutput,
		)
	}()
	authenticateObserverRelayIO(t, daemonReader, daemonWriter, binding, hello.HelperDigest, token)
	if err := daemonWriter.Close(); err != nil {
		t.Fatal(err)
	}

	envelope := relayObservation(binding, 0, 1, "network.connect", `{"ip":"203.0.113.7","port":443}`)
	if err := relay.Enqueue(envelope); err != nil {
		t.Fatal(err)
	}
	got, err := sessionwire.ReadObserverEnvelope(daemonReader)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != envelope.Kind || got.Sequence != envelope.Sequence {
		t.Fatalf("bridged envelope=%+v", got)
	}
	if err := relay.Close(); err != nil {
		t.Fatal(err)
	}
	_ = bridgeInput.Close()
	_ = bridgeOutput.Close()
	_ = daemonReader.Close()
	select {
	case err := <-bridgeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("observer stream bridge did not stop")
	}

	if err := runObserverStreamBridge(
		"../foreign",
		relay.root,
		bytes.NewReader(nil),
		io.Discard,
	); err == nil {
		t.Fatal("observer bridge accepted an unvalidated session path")
	}
}

func TestObserverRelayAuthenticatesThenStreamsExactBoundObservation(t *testing.T) {
	binding, hello, token := observerRelayFixture(t)
	relay, err := newObserverRelay(binding, hello, token, observerRelayOptions{
		Root:                      shortObserverRelayRoot(t),
		QueueEntries:              4,
		QueueBytes:                4096,
		HandshakeWait:             time.Second,
		PeerUID:                   func(*net.UnixConn) (uint32, error) { return 0, nil },
		MonotonicNS:               func() (uint64, error) { return 9001, nil },
		SkipRootOwnerCheckForTest: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	rootInfo, err := os.Stat(relay.root)
	if err != nil {
		t.Fatal(err)
	}
	socketInfo, err := os.Stat(relay.socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if rootInfo.Mode().Perm() != 0o700 || socketInfo.Mode().Perm() != 0o600 {
		t.Fatalf("relay permissions root=%#o socket=%#o", rootInfo.Mode().Perm(), socketInfo.Mode().Perm())
	}

	wrongToken, err := sessionwire.NewObserverStreamToken()
	if err != nil {
		t.Fatal(err)
	}
	rejected := dialObserverRelay(t, relay.socketPath)
	if err := sessionwire.WriteObserverStreamOpen(rejected, sessionwire.ObserverStreamOpen{
		Type: sessionwire.ObserverStreamOpenType, Schema: sessionwire.ObserverWireSchema,
		SessionID: binding.SessionID, Token: wrongToken,
	}); err != nil {
		t.Fatal(err)
	}
	_ = rejected.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := sessionwire.ReadObserverHello(rejected); err == nil {
		t.Fatal("relay accepted the wrong session token")
	}
	_ = rejected.Close()

	connection := dialObserverRelay(t, relay.socketPath)
	acceptedHello := authenticateObserverRelay(t, connection, binding, hello.HelperDigest, token)
	if acceptedHello.SessionID != binding.SessionID ||
		acceptedHello.CgroupID != binding.CgroupID {
		t.Fatalf("relay hello=%+v", acceptedHello)
	}
	select {
	case <-relay.Authenticated():
	case <-time.After(time.Second):
		t.Fatal("relay did not publish authenticated readiness")
	}

	envelope := relayObservation(binding, 0, 1, "process.exec", `{"pid":42}`)
	if err := relay.Enqueue(envelope); err != nil {
		t.Fatal(err)
	}
	got, err := sessionwire.ReadObserverEnvelope(connection)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sequence != envelope.Sequence || got.Kind != envelope.Kind ||
		!got.Owner.Equal(binding.Owner) {
		t.Fatalf("streamed envelope=%+v", got)
	}
	_ = connection.Close()
}

func TestObserverRelayQueueOverflowUsesReservedLossEnvelope(t *testing.T) {
	binding, hello, token := observerRelayFixture(t)
	relay, err := newObserverRelay(binding, hello, token, observerRelayOptions{
		Root:                      shortObserverRelayRoot(t),
		QueueEntries:              1,
		QueueBytes:                4096,
		HandshakeWait:             time.Second,
		PeerUID:                   func(*net.UnixConn) (uint32, error) { return 0, nil },
		MonotonicNS:               func() (uint64, error) { return 777, nil },
		SkipRootOwnerCheckForTest: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	first := relayObservation(binding, 1, 1, "collector.heartbeat", `{"latestSequence":1}`)
	if err := relay.Enqueue(first); err != nil {
		t.Fatal(err)
	}
	if err := relay.Enqueue(relayObservation(
		binding,
		1,
		2,
		"process.exec",
		`{"pid":43}`,
	)); !errors.Is(err, sessionwire.ErrObserverBackpressure) {
		t.Fatalf("overflow error=%v want %v", err, sessionwire.ErrObserverBackpressure)
	}

	connection := dialObserverRelay(t, relay.socketPath)
	defer connection.Close()
	authenticateObserverRelay(t, connection, binding, hello.HelperDigest, token)
	got, err := sessionwire.ReadObserverEnvelope(connection)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != first.Kind || got.Sequence != first.Sequence {
		t.Fatalf("first queued envelope=%+v", got)
	}
	lossEnvelope, err := sessionwire.ReadObserverEnvelope(connection)
	if err != nil {
		t.Fatal(err)
	}
	if lossEnvelope.Kind != "collector.loss" ||
		lossEnvelope.CPU != sessionwire.ObserverTransportCPU ||
		lossEnvelope.Sequence != 1 ||
		lossEnvelope.MonotonicNS != 777 {
		t.Fatalf("reserved loss envelope=%+v", lossEnvelope)
	}
	var loss struct {
		Dropped      uint64 `json:"dropped"`
		DroppedBytes uint64 `json:"droppedBytes"`
		Reason       string `json:"reason"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(lossEnvelope.Payload, &loss); err != nil {
		t.Fatal(err)
	}
	if loss.Dropped != 1 || loss.DroppedBytes == 0 ||
		loss.Reason != "observer-send-queue-overflow" ||
		loss.Scope != "guest-observer-transport" {
		t.Fatalf("loss payload=%+v", loss)
	}
}

func TestObserverRelayRejectsTargetUIDBeforeHello(t *testing.T) {
	binding, hello, token := observerRelayFixture(t)
	relay, err := newObserverRelay(binding, hello, token, observerRelayOptions{
		Root:                      shortObserverRelayRoot(t),
		QueueEntries:              1,
		QueueBytes:                4096,
		HandshakeWait:             100 * time.Millisecond,
		PeerUID:                   func(*net.UnixConn) (uint32, error) { return 1000, nil },
		MonotonicNS:               func() (uint64, error) { return 1, nil },
		SkipRootOwnerCheckForTest: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	connection := dialObserverRelay(t, relay.socketPath)
	defer connection.Close()
	if err := sessionwire.WriteObserverStreamOpen(connection, sessionwire.ObserverStreamOpen{
		Type: sessionwire.ObserverStreamOpenType, Schema: sessionwire.ObserverWireSchema,
		SessionID: binding.SessionID, Token: token,
	}); err != nil {
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := sessionwire.ReadObserverHello(connection); err == nil {
		t.Fatal("target UID received the observer hello")
	}
	select {
	case <-relay.Authenticated():
		t.Fatal("target UID authenticated the observer stream")
	default:
	}
}

func observerRelayFixture(
	t *testing.T,
) (sessionwire.ObserverBinding, sessionwire.ObserverHello, sessionwire.ObserverStreamToken) {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	binding := sessionwire.ObserverBinding{
		Owner: owner, SessionID: "ses_20260729T120000Z_relay",
		EnvironmentID: "env_fixture", BackendIncarnationID: "incarnation-a",
		GuestBootID: "01234567-89ab-cdef-0123-456789abcdef",
		CgroupID:    3141, ObserverGeneration: 1,
	}
	hello := sessionwire.ObserverHello{
		Type: sessionwire.ObserverHelloType, Schema: sessionwire.ObserverWireSchema,
		Owner: binding.Owner, SessionID: binding.SessionID,
		EnvironmentID: binding.EnvironmentID, BackendIncarnationID: binding.BackendIncarnationID,
		GuestBootID: binding.GuestBootID, CgroupID: binding.CgroupID,
		ObserverGeneration: binding.ObserverGeneration,
		HelperDigest:       "sha256:" + strings.Repeat("a", 64),
		Capabilities:       observerUnavailableCapabilities(),
	}
	token, err := sessionwire.NewObserverStreamToken()
	if err != nil {
		t.Fatal(err)
	}
	return binding, hello, token
}

func shortObserverRelayRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "hideout-observer-relay-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func relayObservation(
	binding sessionwire.ObserverBinding,
	cpu, sequence uint64,
	kind, payload string,
) sessionwire.ObservationEnvelope {
	return sessionwire.ObservationEnvelope{
		Schema: sessionwire.ObservationSchema, Owner: binding.Owner,
		SessionID: binding.SessionID, CgroupID: binding.CgroupID,
		ObserverGeneration: binding.ObserverGeneration,
		CPU:                cpu, Sequence: sequence, MonotonicNS: 100,
		Kind: kind, Payload: json.RawMessage(payload),
	}
}

func dialObserverRelay(t *testing.T, path string) *net.UnixConn {
	t.Helper()
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func authenticateObserverRelay(
	t *testing.T,
	connection *net.UnixConn,
	binding sessionwire.ObserverBinding,
	digest string,
	token sessionwire.ObserverStreamToken,
) sessionwire.ObserverHello {
	t.Helper()
	return authenticateObserverRelayIO(t, connection, connection, binding, digest, token)
}

func authenticateObserverRelayIO(
	t *testing.T,
	reader io.Reader,
	writer io.Writer,
	binding sessionwire.ObserverBinding,
	digest string,
	token sessionwire.ObserverStreamToken,
) sessionwire.ObserverHello {
	t.Helper()
	if err := sessionwire.WriteObserverStreamOpen(writer, sessionwire.ObserverStreamOpen{
		Type: sessionwire.ObserverStreamOpenType, Schema: sessionwire.ObserverWireSchema,
		SessionID: binding.SessionID, Token: token,
	}); err != nil {
		t.Fatal(err)
	}
	hello, err := sessionwire.ReadObserverHello(reader)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := sessionwire.AcceptObserverHello(
		hello,
		binding,
		sessionwire.ObserverPeer{UID: 0, HelperDigest: digest},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessionwire.WriteObserverAccepted(writer, accepted); err != nil {
		t.Fatal(err)
	}
	return hello
}
