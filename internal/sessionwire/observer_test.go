package sessionwire

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
	"testing"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestObserverStreamOpenAuthenticatesRootSessionTokenStrictly(t *testing.T) {
	token, err := NewObserverStreamToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := token.Validate(); err != nil {
		t.Fatal(err)
	}
	open := ObserverStreamOpen{
		Type:      ObserverStreamOpenType,
		Schema:    ObserverWireSchema,
		SessionID: "ses_20260729T120000Z_observer",
		Token:     token,
	}
	var encoded bytes.Buffer
	if err := WriteObserverStreamOpen(&encoded, open); err != nil {
		t.Fatal(err)
	}
	decoded, err := ReadObserverStreamOpen(&encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := AuthenticateObserverStreamOpen(
		decoded,
		open.SessionID,
		token,
		ObserverStreamPeer{UID: 0},
	); err != nil {
		t.Fatal(err)
	}

	other, err := NewObserverStreamToken()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		open    ObserverStreamOpen
		session string
		token   ObserverStreamToken
		peer    ObserverStreamPeer
		want    error
	}{
		{
			name: "wrong token", open: open, session: open.SessionID,
			token: other, peer: ObserverStreamPeer{UID: 0},
			want: ErrObserverAuthentication,
		},
		{
			name: "wrong session", open: open, session: "ses_other",
			token: token, peer: ObserverStreamPeer{UID: 0},
			want: ErrObserverIdentity,
		},
		{
			name: "non root peer", open: open, session: open.SessionID,
			token: token, peer: ObserverStreamPeer{UID: 1000},
			want: ErrObserverTargetAuthority,
		},
		{
			name: "target controlled peer", open: open, session: open.SessionID,
			token: token, peer: ObserverStreamPeer{UID: 0, TargetControlled: true},
			want: ErrObserverTargetAuthority,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if err := AuthenticateObserverStreamOpen(
				testCase.open,
				testCase.session,
				testCase.token,
				testCase.peer,
			); !errors.Is(err, testCase.want) {
				t.Fatalf("error=%v want %v", err, testCase.want)
			}
		})
	}
}

func TestObserverStreamTokenJSONIsCanonicalStrictAndRedactedForFormatting(t *testing.T) {
	token, err := NewObserverStreamToken()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(token)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 45 || data[0] != '"' || data[len(data)-1] != '"' {
		t.Fatalf("token JSON is not one canonical raw-base64 string: %q", data)
	}
	for _, formatted := range []string{
		fmt.Sprint(token),
		fmt.Sprintf("%+v", token),
		fmt.Sprintf("%#v", token),
		fmt.Sprintf("%x", token),
	} {
		if strings.Contains(formatted, string(data[1:len(data)-1])) {
			t.Fatalf("formatted observer token exposed its credential: %q", formatted)
		}
	}
	var decoded ObserverStreamToken
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(token) {
		t.Fatal("observer token JSON round trip changed the token")
	}

	for _, malformed := range []string{
		`""`,
		`"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"`,
		`"not-base64"`,
		`"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="`,
		`123`,
	} {
		var value ObserverStreamToken
		if err := json.Unmarshal([]byte(malformed), &value); !errors.Is(err, ErrObserverAuthentication) {
			t.Fatalf("malformed token %s error=%v want %v", malformed, err, ErrObserverAuthentication)
		}
	}

	open := ObserverStreamOpen{
		Type: ObserverStreamOpenType, Schema: ObserverWireSchema,
		SessionID: "ses_token", Token: token,
	}
	payload, err := json.Marshal(open)
	if err != nil {
		t.Fatal(err)
	}
	payload[len(payload)-1] = ','
	payload = append(payload, []byte(`"unknown":true}`)...)
	if _, err := ReadObserverStreamOpen(bytes.NewReader(observerRawFrame(payload))); !errors.Is(err, ErrObserverSchema) {
		t.Fatalf("unknown stream-open field error=%v want %v", err, ErrObserverSchema)
	}
}

func TestObserverHandshakeBindsExactIdentityAndRejectsTargetAuthority(t *testing.T) {
	binding := observerTestBinding(t)
	hello := observerTestHello(binding)
	accepted, err := AcceptObserverHello(hello, binding, ObserverPeer{
		UID: 0, HelperDigest: hello.HelperDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Owner.Key() != binding.Owner.Key() ||
		accepted.ExpectedNextSequence != 1 ||
		accepted.MaxFrameBytes != MaxObserverFrameSize {
		t.Fatalf("accepted binding=%+v", accepted)
	}

	tests := []struct {
		name   string
		mutate func(*ObserverHello, *ObserverBinding, *ObserverPeer)
		want   error
	}{
		{
			name: "wrong session",
			mutate: func(value *ObserverHello, _ *ObserverBinding, _ *ObserverPeer) {
				value.SessionID = "ses_20260729T120001Z_other"
			},
			want: ErrObserverIdentity,
		},
		{
			name: "wrong owner",
			mutate: func(value *ObserverHello, _ *ObserverBinding, _ *ObserverPeer) {
				value.Owner, _ = workloadtypes.NewDisposableOwner(
					value.SessionID, "lima", "incarnation-a",
				)
			},
			want: ErrObserverIdentity,
		},
		{
			name: "wrong cgroup",
			mutate: func(value *ObserverHello, _ *ObserverBinding, _ *ObserverPeer) {
				value.CgroupID++
			},
			want: ErrObserverIdentity,
		},
		{
			name: "wrong boot",
			mutate: func(value *ObserverHello, _ *ObserverBinding, _ *ObserverPeer) {
				value.GuestBootID = "other-boot"
			},
			want: ErrObserverIdentity,
		},
		{
			name: "target controlled endpoint",
			mutate: func(_ *ObserverHello, _ *ObserverBinding, peer *ObserverPeer) {
				peer.UID = 1000
				peer.TargetControlled = true
			},
			want: ErrObserverTargetAuthority,
		},
		{
			name: "helper digest mismatch",
			mutate: func(_ *ObserverHello, _ *ObserverBinding, peer *ObserverPeer) {
				peer.HelperDigest = "sha256:" + strings.Repeat("1", 64)
			},
			want: ErrObserverAuthentication,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			value := hello
			expected := binding
			peer := ObserverPeer{UID: 0, HelperDigest: hello.HelperDigest}
			testCase.mutate(&value, &expected, &peer)
			if _, err := AcceptObserverHello(value, expected, peer); !errors.Is(err, testCase.want) {
				t.Fatalf("error=%v want %v", err, testCase.want)
			}
		})
	}
}

func TestObserverHandshakeFramesRoundTripStrictly(t *testing.T) {
	binding := observerTestBinding(t)
	hello := observerTestHello(binding)
	var guestToSupervisor bytes.Buffer
	if err := WriteObserverHello(&guestToSupervisor, hello); err != nil {
		t.Fatal(err)
	}
	decodedHello, err := ReadObserverHello(&guestToSupervisor)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := AcceptObserverHello(decodedHello, binding, ObserverPeer{
		UID: 0, HelperDigest: hello.HelperDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	var supervisorToGuest bytes.Buffer
	if err := WriteObserverAccepted(&supervisorToGuest, accepted); err != nil {
		t.Fatal(err)
	}
	decodedAccepted, err := ReadObserverAccepted(&supervisorToGuest)
	if err != nil {
		t.Fatal(err)
	}
	if err := decodedAccepted.ValidateBinding(binding); err != nil {
		t.Fatal(err)
	}

	payload, err := json.Marshal(hello)
	if err != nil {
		t.Fatal(err)
	}
	payload[len(payload)-1] = ','
	payload = append(payload, []byte(`"unknown":true}`)...)
	if _, err := ReadObserverHello(bytes.NewReader(observerRawFrame(payload))); !errors.Is(err, ErrObserverSchema) {
		t.Fatalf("unknown hello field error=%v want %v", err, ErrObserverSchema)
	}
}

func TestObserverFrameEnforcesCRCStrictSchemaAndBounds(t *testing.T) {
	binding := observerTestBinding(t)
	envelope := observerTestEnvelope(binding, 2, 1, "process.exec")
	var encoded bytes.Buffer
	if err := WriteObserverEnvelope(&encoded, envelope); err != nil {
		t.Fatal(err)
	}
	decoded, err := ReadObserverEnvelope(&encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Sequence != envelope.Sequence || decoded.Kind != envelope.Kind ||
		decoded.Owner.Key() != envelope.Owner.Key() {
		t.Fatalf("decoded envelope=%+v", decoded)
	}

	badCRC := append([]byte(nil), encodedObserverEnvelope(t, envelope)...)
	badCRC[len(badCRC)-1] ^= 0xff
	if _, err := ReadObserverEnvelope(bytes.NewReader(badCRC)); !errors.Is(err, ErrObserverCRC) {
		t.Fatalf("bad CRC error=%v want %v", err, ErrObserverCRC)
	}

	oversized := make([]byte, ObserverFrameHeaderSize)
	binary.BigEndian.PutUint32(oversized[:4], uint32(MaxObserverFrameSize+1))
	if _, err := ReadObserverEnvelope(bytes.NewReader(oversized)); !errors.Is(err, ErrObserverFrameTooLarge) {
		t.Fatalf("oversized error=%v want %v", err, ErrObserverFrameTooLarge)
	}

	for _, payload := range [][]byte{
		[]byte(`{"schema":"hideout.observation.v1","unknown":true}`),
		[]byte(`{"schema":"hideout.observation.v2"}`),
	} {
		frame := observerRawFrame(payload)
		if _, err := ReadObserverEnvelope(bytes.NewReader(frame)); !errors.Is(err, ErrObserverSchema) {
			t.Fatalf("strict schema payload=%s error=%v want %v", payload, err, ErrObserverSchema)
		}
	}

	unknownKind := envelope
	unknownKind.Kind = "packet.capture"
	if err := WriteObserverEnvelope(&bytes.Buffer{}, unknownKind); !errors.Is(err, ErrObserverKind) {
		t.Fatalf("unknown kind error=%v want %v", err, ErrObserverKind)
	}
	reservedCPU := envelope
	reservedCPU.CPU = ObserverTransportCPU
	if err := WriteObserverEnvelope(&bytes.Buffer{}, reservedCPU); !errors.Is(err, ErrObserverSchema) {
		t.Fatalf("reserved transport CPU error=%v want %v", err, ErrObserverSchema)
	}
	reservedCPU.Kind = "collector.loss"
	if err := WriteObserverEnvelope(&bytes.Buffer{}, reservedCPU); err != nil {
		t.Fatalf("reserved transport loss was rejected: %v", err)
	}
}

func TestObserverSequenceTracksCPUWithoutDoubleCountAndReportsGapRestart(t *testing.T) {
	binding := observerTestBinding(t)
	tracker, err := NewObserverSequenceTracker(binding)
	if err != nil {
		t.Fatal(err)
	}
	first := observerTestEnvelope(binding, 2, 1, "process.exec")
	result, err := tracker.Observe(first)
	if err != nil || result.Disposition != ObserverSequenceAccepted {
		t.Fatalf("first result=%+v err=%v", result, err)
	}
	result, err = tracker.Observe(first)
	if err != nil || result.Disposition != ObserverSequenceDuplicate {
		t.Fatalf("duplicate result=%+v err=%v", result, err)
	}

	gapped := observerTestEnvelope(binding, 2, 4, "process.exit")
	result, err = tracker.Observe(gapped)
	if err != nil || result.Disposition != ObserverSequenceGap ||
		result.MissingFrom != 2 || result.MissingTo != 3 {
		t.Fatalf("gap result=%+v err=%v", result, err)
	}

	otherCPU := observerTestEnvelope(binding, 7, 1, "collector.heartbeat")
	result, err = tracker.Observe(otherCPU)
	if err != nil || result.Disposition != ObserverSequenceAccepted {
		t.Fatalf("per-CPU result=%+v err=%v", result, err)
	}

	restartedBinding := binding
	restartedBinding.ObserverGeneration++
	restarted := observerTestEnvelope(restartedBinding, 2, 1, "collector.heartbeat")
	result, err = tracker.Observe(restarted)
	if err != nil || result.Disposition != ObserverSequenceRestart {
		t.Fatalf("restart result=%+v err=%v", result, err)
	}

	if _, err := tracker.Observe(gapped); !errors.Is(err, ErrObserverGenerationRollback) {
		t.Fatalf("generation rollback error=%v want %v", err, ErrObserverGenerationRollback)
	}
	wrongOwner := restarted
	wrongOwner.CgroupID++
	if _, err := tracker.Observe(wrongOwner); !errors.Is(err, ErrObserverIdentity) {
		t.Fatalf("wrong owner error=%v want %v", err, ErrObserverIdentity)
	}
}

func TestObserverQueueFailsClosedAndReservesLossSummary(t *testing.T) {
	queue, err := NewObserverQueueWithByteLimit(2, 5)
	if err != nil {
		t.Fatal(err)
	}
	first := []byte("first")
	if err := queue.Enqueue(first); err != nil {
		t.Fatal(err)
	}
	if err := queue.Enqueue([]byte("x")); !errors.Is(err, ErrObserverBackpressure) {
		t.Fatalf("overflow error=%v want %v", err, ErrObserverBackpressure)
	}
	loss := queue.LossSummary()
	if loss.Dropped != 1 || loss.DroppedBytes != 1 ||
		loss.Reason != "observer-send-queue-overflow" {
		t.Fatalf("loss summary=%+v", loss)
	}
	got, ok := queue.Dequeue()
	if !ok || !bytes.Equal(got, first) {
		t.Fatalf("dequeue=%q/%v want first/true", got, ok)
	}
	if _, ok := queue.Dequeue(); ok {
		t.Fatal("overflow silently replaced queued observation")
	}
	if queue.LossSummary().Dropped != 0 {
		t.Fatal("loss summary was not acknowledged exactly once")
	}
	queue.Close()
	queue.Close()
	if err := queue.Enqueue([]byte("later")); !errors.Is(err, ErrObserverQueueClosed) {
		t.Fatalf("closed queue error=%v want %v", err, ErrObserverQueueClosed)
	}
	select {
	case <-queue.Done():
	default:
		t.Fatal("closed observer queue did not close its done signal")
	}
}

func TestObserverQueueAccountsForFramingOverheadAtWireLimit(t *testing.T) {
	queue, err := NewObserverQueueWithByteLimit(1, MaxObserverQueuedFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	atLimit := make([]byte, MaxObserverQueuedFrameBytes)
	atLimit[0] = 1
	if err := queue.Enqueue(atLimit); err != nil {
		t.Fatalf("maximum framed observer value was rejected: %v", err)
	}
	if got, ok := queue.Dequeue(); !ok || len(got) != MaxObserverQueuedFrameBytes {
		t.Fatalf("maximum framed dequeue len=%d ok=%v", len(got), ok)
	}
	if err := queue.Enqueue(make([]byte, MaxObserverQueuedFrameBytes+1)); !errors.Is(err, ErrObserverFrameTooLarge) {
		t.Fatalf("over-limit framed value error=%v want %v", err, ErrObserverFrameTooLarge)
	}
}

func observerTestBinding(t *testing.T) ObserverBinding {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	return ObserverBinding{
		Owner: owner, SessionID: "ses_20260729T120000Z_observer",
		EnvironmentID: "env_fixture", BackendIncarnationID: "incarnation-a",
		GuestBootID: "01234567-89ab-cdef-0123-456789abcdef",
		CgroupID:    3141, ObserverGeneration: 1,
	}
}

func observerTestHello(binding ObserverBinding) ObserverHello {
	return ObserverHello{
		Type: "observer.hello", Schema: ObserverWireSchema,
		Owner: binding.Owner, SessionID: binding.SessionID,
		EnvironmentID:        binding.EnvironmentID,
		BackendIncarnationID: binding.BackendIncarnationID,
		GuestBootID:          binding.GuestBootID, CgroupID: binding.CgroupID,
		ObserverGeneration: binding.ObserverGeneration,
		HelperDigest:       "sha256:" + strings.Repeat("a", 64),
		Capabilities: ObserverCapabilities{
			Process: ObserverCapability{State: workloadtypes.CoverageAvailable, Evidence: []string{"tracepoint.exec"}},
			File:    ObserverCapability{State: workloadtypes.CoveragePartial, Evidence: []string{"fanotify"}},
			Network: ObserverCapability{State: workloadtypes.CoverageAvailable, Evidence: []string{"cgroup.connect4"}},
			DNS:     ObserverCapability{State: workloadtypes.CoveragePartial, Evidence: []string{"encrypted-dns"}},
		},
	}
}

func observerTestEnvelope(
	binding ObserverBinding,
	cpu, sequence uint64,
	kind string,
) ObservationEnvelope {
	payload, _ := json.Marshal(map[string]any{
		"pid": uint32(42), "tid": uint32(42), "execSequence": uint64(1),
	})
	return ObservationEnvelope{
		Schema: ObservationSchema, Owner: binding.Owner,
		SessionID: binding.SessionID, CgroupID: binding.CgroupID,
		ObserverGeneration: binding.ObserverGeneration,
		CPU:                cpu, Sequence: sequence, MonotonicNS: 1000 + sequence,
		Kind: kind, Payload: payload,
	}
}

func encodedObserverEnvelope(t *testing.T, envelope ObservationEnvelope) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := WriteObserverEnvelope(&out, envelope); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func observerRawFrame(payload []byte) []byte {
	out := make([]byte, ObserverFrameHeaderSize+len(payload))
	binary.BigEndian.PutUint32(out[:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(out[4:8], crc32.Checksum(payload, crc32.MakeTable(crc32.Castagnoli)))
	copy(out[ObserverFrameHeaderSize:], payload)
	return out
}
