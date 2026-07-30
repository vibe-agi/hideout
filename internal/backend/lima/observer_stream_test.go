package lima

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/vibe-agi/hideout/internal/sessionwire"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestLimaObserverStreamReadsBufferedFramesAsOrderedBatch(t *testing.T) {
	t.Parallel()

	binding, _, token := limaObserverFixture(t)
	defer token.Destroy()
	tracker, err := sessionwire.NewObserverSequenceTracker(binding)
	if err != nil {
		t.Fatal(err)
	}
	var wire bytes.Buffer
	for sequence := uint64(1); sequence <= 3; sequence++ {
		if err := sessionwire.WriteObserverEnvelope(
			&wire,
			limaObserverEnvelope(binding, 2, sequence, "process.exec"),
		); err != nil {
			t.Fatal(err)
		}
	}
	stream := &limaObserverStream{
		stdout: bufio.NewReaderSize(
			bytes.NewReader(wire.Bytes()),
			limaObserverReadBufferBytes,
		),
		tracker: tracker,
	}
	envelopes, err := stream.ReadBatch(limaObserverBatchEnvelopes)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelopes) != 3 {
		t.Fatalf("batch size=%d want=3", len(envelopes))
	}
	for index, envelope := range envelopes {
		if envelope.Sequence != uint64(index+1) {
			t.Fatalf("batch[%d] sequence=%d", index, envelope.Sequence)
		}
	}
}

func TestLimaObserverStreamUsesDedicatedSSHChannelAndHidesAuthorityFromCommand(t *testing.T) {
	binding, hello, token := limaObserverFixture(t)
	envelope := limaObserverEnvelope(binding, 2, 1, "process.exec")
	controlOpened := make(chan struct{})
	releaseControl := make(chan struct{})
	var controlOnce sync.Once
	client, server := newObserverStreamSSHTestClient(t, func(command string, channel ssh.Channel) error {
		if command == "hold-control-channel" {
			controlOnce.Do(func() { close(controlOpened) })
			<-releaseControl
			return nil
		}
		open, err := sessionwire.ReadObserverStreamOpen(channel)
		if err != nil {
			return err
		}
		if err := sessionwire.AuthenticateObserverStreamOpen(
			open,
			binding.SessionID,
			token,
			sessionwire.ObserverStreamPeer{UID: 0},
		); err != nil {
			return err
		}
		if err := sessionwire.WriteObserverHello(channel, hello); err != nil {
			return err
		}
		accepted, err := sessionwire.ReadObserverAccepted(channel)
		if err != nil {
			return err
		}
		if err := accepted.ValidateBinding(binding); err != nil {
			return err
		}
		return sessionwire.WriteObserverEnvelope(channel, envelope)
	})

	control, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Start("hold-control-channel"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-controlOpened:
	case <-time.After(time.Second):
		t.Fatal("control SSH channel did not open")
	}

	stream, err := openLimaObserverStream(context.Background(), client, observerStreamExpectation{
		Binding:      binding,
		Token:        token,
		HelperDigest: hello.HelperDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, sequence, err := stream.Read()
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != envelope.Kind || got.Sequence != envelope.Sequence ||
		sequence.Disposition != sessionwire.ObserverSequenceAccepted {
		t.Fatalf("observation=%+v sequence=%+v", got, sequence)
	}
	if !stream.Hello().Owner.Equal(binding.Owner) {
		t.Fatalf("stream hello=%+v", stream.Hello())
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	close(releaseControl)
	if err := control.Wait(); err != nil {
		t.Fatal(err)
	}
	_ = control.Close()
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	result := waitObserverStreamSSHServer(t, server)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if len(result.commands) != 2 ||
		result.commands[0] != "hold-control-channel" ||
		!strings.Contains(result.commands[1], "observer-stream") {
		t.Fatalf("SSH commands=%q", result.commands)
	}
	tokenJSON, err := json.Marshal(token)
	if err != nil {
		t.Fatal(err)
	}
	tokenText := string(tokenJSON[1 : len(tokenJSON)-1])
	for _, command := range result.commands {
		if strings.Contains(command, tokenText) ||
			strings.Contains(command, hello.HelperDigest) ||
			strings.Contains(command, binding.Owner.Key()) {
			t.Fatalf("observer authority leaked into SSH command %q", command)
		}
	}
}

func TestLimaObserverStreamRejectsHelperIdentityBeforeReturning(t *testing.T) {
	binding, hello, token := limaObserverFixture(t)
	hello.HelperDigest = "sha256:" + strings.Repeat("b", 64)
	client, server := newObserverStreamSSHTestClient(t, func(_ string, channel ssh.Channel) error {
		if _, err := sessionwire.ReadObserverStreamOpen(channel); err != nil {
			return err
		}
		return sessionwire.WriteObserverHello(channel, hello)
	})
	_, err := openLimaObserverStream(context.Background(), client, observerStreamExpectation{
		Binding:      binding,
		Token:        token,
		HelperDigest: "sha256:" + strings.Repeat("a", 64),
	})
	if !errors.Is(err, sessionwire.ErrObserverAuthentication) {
		t.Fatalf("helper mismatch error=%v want %v", err, sessionwire.ErrObserverAuthentication)
	}
	_ = client.Close()
	result := waitObserverStreamSSHServer(t, server)
	if result.err != nil {
		t.Fatal(result.err)
	}
}

func TestLimaObserverStreamHandshakeCancellationClosesOnlyObserverChannel(t *testing.T) {
	binding, hello, token := limaObserverFixture(t)
	client, server := newObserverStreamSSHTestClient(t, func(_ string, channel ssh.Channel) error {
		if _, err := sessionwire.ReadObserverStreamOpen(channel); err != nil {
			return err
		}
		_, err := io.Copy(io.Discard, channel)
		return err
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := openLimaObserverStream(ctx, client, observerStreamExpectation{
		Binding: binding, Token: token, HelperDigest: hello.HelperDigest,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled handshake error=%v", err)
	}
	if time.Since(started) >= time.Second {
		t.Fatalf("cancelled handshake took %s", time.Since(started))
	}
	_ = client.Close()
	result := waitObserverStreamSSHServer(t, server)
	if result.err != nil {
		t.Fatal(result.err)
	}
}

func TestLimaObserverStreamReadPreservesOversizedFrameFailure(t *testing.T) {
	binding, hello, token := limaObserverFixture(t)
	client, server := newObserverStreamSSHTestClient(t, func(_ string, channel ssh.Channel) error {
		if _, err := sessionwire.ReadObserverStreamOpen(channel); err != nil {
			return err
		}
		if err := sessionwire.WriteObserverHello(channel, hello); err != nil {
			return err
		}
		if _, err := sessionwire.ReadObserverAccepted(channel); err != nil {
			return err
		}
		var header [sessionwire.ObserverFrameHeaderSize]byte
		binary.BigEndian.PutUint32(header[:4], sessionwire.MaxObserverFrameSize+1)
		_, err := channel.Write(header[:])
		return err
	})
	stream, err := openLimaObserverStream(context.Background(), client, observerStreamExpectation{
		Binding: binding, Token: token, HelperDigest: hello.HelperDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := stream.Read(); !errors.Is(err, sessionwire.ErrObserverFrameTooLarge) {
		t.Fatalf("oversized observer read error=%v want %v", err, sessionwire.ErrObserverFrameTooLarge)
	}
	_ = stream.Close()
	_ = client.Close()
	result := waitObserverStreamSSHServer(t, server)
	if result.err != nil {
		t.Fatal(result.err)
	}
}

type observerStreamSSHServerResult struct {
	commands []string
	err      error
}

func newObserverStreamSSHTestClient(
	t *testing.T,
	handle func(string, ssh.Channel) error,
) (*ssh.Client, <-chan observerStreamSSHServerResult) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, _ := testSSHSigner(t)
	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(hostSigner)
	done := make(chan observerStreamSSHServerResult, 1)
	go func() {
		defer listener.Close()
		result := observerStreamSSHServerResult{}
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			result.err = acceptErr
			done <- result
			return
		}
		defer connection.Close()
		serverConn, incoming, requests, err := ssh.NewServerConn(connection, serverConfig)
		if err != nil {
			result.err = err
			done <- result
			return
		}
		go ssh.DiscardRequests(requests)
		var (
			mu       sync.Mutex
			failures []error
			wg       sync.WaitGroup
		)
		for next := range incoming {
			if next.ChannelType() != "session" {
				_ = next.Reject(ssh.UnknownChannelType, "session channel required")
				continue
			}
			channel, channelRequests, err := next.Accept()
			if err != nil {
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer channel.Close()
				request, ok := <-channelRequests
				if !ok || request.Type != "exec" {
					mu.Lock()
					failures = append(failures, errors.New("observer SSH exec request missing"))
					mu.Unlock()
					return
				}
				var payload struct{ Command string }
				if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
					mu.Lock()
					failures = append(failures, err)
					mu.Unlock()
					return
				}
				if err := request.Reply(true, nil); err != nil {
					mu.Lock()
					failures = append(failures, err)
					mu.Unlock()
					return
				}
				mu.Lock()
				result.commands = append(result.commands, payload.Command)
				mu.Unlock()
				go ssh.DiscardRequests(channelRequests)
				handlerErr := handle(payload.Command, channel)
				if handlerErr != nil && !errors.Is(handlerErr, io.EOF) {
					mu.Lock()
					failures = append(failures, handlerErr)
					mu.Unlock()
				}
				_, _ = channel.SendRequest(
					"exit-status",
					false,
					ssh.Marshal(struct{ Status uint32 }{0}),
				)
			}()
		}
		wg.Wait()
		_ = serverConn.Close()
		result.err = errors.Join(failures...)
		done <- result
	}()

	clientConn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	clientConfig := &ssh.ClientConfig{
		User: "root", HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout: time.Second,
	}
	connection, channels, requests, err := ssh.NewClientConn(
		clientConn,
		listener.Addr().String(),
		clientConfig,
	)
	if err != nil {
		_ = clientConn.Close()
		t.Fatal(err)
	}
	return ssh.NewClient(connection, channels, requests), done
}

func waitObserverStreamSSHServer(
	t *testing.T,
	done <-chan observerStreamSSHServerResult,
) observerStreamSSHServerResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(time.Second):
		t.Fatal("observer SSH test server did not stop")
		return observerStreamSSHServerResult{}
	}
}

func limaObserverFixture(
	t *testing.T,
) (sessionwire.ObserverBinding, sessionwire.ObserverHello, sessionwire.ObserverStreamToken) {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	binding := sessionwire.ObserverBinding{
		Owner: owner, SessionID: "ses_20260729T120000Z_limaobserver",
		EnvironmentID: "env_fixture", BackendIncarnationID: "incarnation-a",
		GuestBootID: "01234567-89ab-cdef-0123-456789abcdef",
		CgroupID:    3141, ObserverGeneration: 1,
	}
	unavailable := sessionwire.ObserverCapability{
		State:  workloadtypes.CoverageUnavailable,
		Reason: "collector-not-loaded", Evidence: []string{"cgroup-v2"},
	}
	hello := sessionwire.ObserverHello{
		Type: sessionwire.ObserverHelloType, Schema: sessionwire.ObserverWireSchema,
		Owner: binding.Owner, SessionID: binding.SessionID,
		EnvironmentID: binding.EnvironmentID, BackendIncarnationID: binding.BackendIncarnationID,
		GuestBootID: binding.GuestBootID, CgroupID: binding.CgroupID,
		ObserverGeneration: binding.ObserverGeneration,
		HelperDigest:       "sha256:" + strings.Repeat("a", 64),
		Capabilities: sessionwire.ObserverCapabilities{
			Process: unavailable, File: unavailable, Network: unavailable, DNS: unavailable,
		},
	}
	token, err := sessionwire.NewObserverStreamToken()
	if err != nil {
		t.Fatal(err)
	}
	return binding, hello, token
}

func limaObserverEnvelope(
	binding sessionwire.ObserverBinding,
	cpu, sequence uint64,
	kind string,
) sessionwire.ObservationEnvelope {
	return sessionwire.ObservationEnvelope{
		Schema: sessionwire.ObservationSchema, Owner: binding.Owner,
		SessionID: binding.SessionID, CgroupID: binding.CgroupID,
		ObserverGeneration: binding.ObserverGeneration,
		CPU:                cpu, Sequence: sequence, MonotonicNS: 55,
		Kind: kind, Payload: json.RawMessage(`{"pid":42}`),
	}
}
