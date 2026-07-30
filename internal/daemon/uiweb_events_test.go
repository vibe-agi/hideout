package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestBrowserSSEGapIsStickyReadOnlyUntilAuthoritativeReseed(t *testing.T) {
	d := startTestDaemon(t)
	client := &http.Client{Timeout: 3 * time.Second}
	snapshot := browserOperatorSnapshot(t, client, d)
	state, err := liveconsole.NewStateFromOperatorSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !state.CanMutate() {
		t.Fatalf("authoritative browser seed is unexpectedly read-only: %+v", state)
	}

	stream := openBrowserSSE(t, d, d.Token())
	defer stream.Close()
	waitForBrowserSubscriberCount(t, d, 1)
	firstID := "browser.gap.first"
	secondID := "browser.gap.second"
	if err := d.bus.publishCapabilityProjection(
		browserCapabilityProjection(firstID),
	); err != nil {
		t.Fatal(err)
	}
	if err := d.bus.publishCapabilityProjection(
		browserCapabilityProjection(secondID),
	); err != nil {
		t.Fatal(err)
	}
	first := nextBrowserSSEMatching(t, stream, func(event Event) bool {
		return event.Kind == liveconsole.KindCapability &&
			event.Entity.ID == firstID
	})
	second := nextBrowserSSEMatching(t, stream, func(event Event) bool {
		return event.Kind == liveconsole.KindCapability &&
			event.Entity.ID == secondID
	})
	if second.Seq != first.Seq+1 || second.Seq <= snapshot.Sequence+1 {
		t.Fatalf(
			"fixture did not create one skipped event: seed=%d first=%d second=%d",
			snapshot.Sequence,
			first.Seq,
			second.Seq,
		)
	}

	result := liveconsole.Apply(&state, second)
	if result.Status != liveconsole.ResultStale ||
		state.StreamHealth.State != liveconsole.HealthStale ||
		!state.ReadOnly ||
		!state.RequiresReseed ||
		state.CanMutate() ||
		state.LastSeq != snapshot.Sequence {
		t.Fatalf("browser gap did not fail closed: result=%+v state=%+v", result, state)
	}
	if result := liveconsole.Apply(&state, first); result.Status != liveconsole.ResultStale ||
		result.Reason != "authoritative reseed required" {
		t.Fatalf("late delivery escaped sticky read-only state: %+v", result)
	}

	reseed := browserOperatorSnapshot(t, client, d)
	reseedState, err := liveconsole.NewStateFromOperatorSnapshot(reseed)
	if err != nil {
		t.Fatal(err)
	}
	if !reseedState.CanMutate() ||
		reseedState.RequiresReseed ||
		reseedState.LastSeq < second.Seq ||
		reseedState.DaemonInstanceID != snapshot.InstanceID {
		t.Fatalf("authoritative browser reseed did not restore live state: %+v", reseedState)
	}
}

func TestBrowserSSESlowSubscriberIsTerminatedWithoutBlockingPublisher(
	t *testing.T,
) {
	d := startTestDaemon(t)
	writer := newBlockingBrowserSSEWriter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"http://localhost"+eventsPath,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "localhost"
	request.Header.Set("Authorization", "Bearer "+d.Token())
	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		d.serveEvents(writer, request)
	}()
	select {
	case <-writer.headerFlushed:
	case <-time.After(time.Second):
		t.Fatal("browser SSE response headers were not flushed")
	}
	waitForBrowserSubscriberCount(t, d, 1)

	if err := d.bus.publishCapabilityProjection(
		browserCapabilityProjection("browser.slow.first"),
	); err != nil {
		t.Fatal(err)
	}
	select {
	case <-writer.firstEventStarted:
	case <-time.After(time.Second):
		t.Fatal("slow browser never received its first event write")
	}

	started := time.Now()
	for index := 0; index < 70; index++ {
		if err := d.bus.publishCapabilityProjection(
			browserCapabilityProjection("browser.slow.overflow"),
		); err != nil {
			t.Fatalf("publish overflow event %d: %v", index, err)
		}
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("slow browser blocked event publication for %s", elapsed)
	}
	waitForBrowserSubscriberCount(t, d, 0)
	close(writer.releaseFirstEvent)
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("overflowed browser SSE handler did not terminate")
	}

	events := decodeBrowserSSEBody(t, writer.Bytes())
	if len(events) < 2 {
		t.Fatalf("slow browser stream lacks data and terminal events: %+v", events)
	}
	terminal := events[len(events)-1]
	if terminal.Kind != liveconsole.KindTerminal ||
		terminal.Seq != 0 ||
		terminal.Payload.Reason != "subscriber-overflow" {
		t.Fatalf("slow browser termination is ambiguous: %+v", terminal)
	}
}

func TestBrowserSSECredentialRotationExpiresStreamAndRequiresFreshSeed(
	t *testing.T,
) {
	store := testStore(t)
	d, err := Start(Options{
		Store:           store,
		TTL:             time.Hour,
		CredentialGrace: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })
	client := &http.Client{Timeout: 3 * time.Second}
	staleToken := d.Token()
	staleURLToken := browserUIFragmentToken(t, d.UIURL())
	if staleURLToken != staleToken {
		t.Fatal("initial browser link does not carry the current credential")
	}
	snapshot := browserOperatorSnapshot(t, client, d)
	state, err := liveconsole.NewStateFromOperatorSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	stream := openBrowserSSE(t, d, staleToken)
	defer stream.Close()
	waitForBrowserSubscriberCount(t, d, 1)

	freshToken, err := d.credentials.Rotate()
	if err != nil {
		t.Fatalf("rotate browser credential: %v", err)
	}
	if freshToken == staleToken ||
		d.credentials.Generation() != snapshot.CredentialGeneration+1 {
		t.Fatalf(
			"credential did not rotate: seed=%d current=%d",
			snapshot.CredentialGeneration,
			d.credentials.Generation(),
		)
	}
	if got := browserUIFragmentToken(t, d.UIURL()); got != freshToken {
		t.Fatal("newly requested browser link retained the stale startup credential")
	}
	if err := d.bus.publishCapabilityProjection(
		browserCapabilityProjection("browser.rotation"),
	); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for state.CanMutate() {
		select {
		case event, ok := <-stream.Events:
			if !ok {
				t.Fatal("rotated browser stream closed without a typed terminal or generation event")
			}
			result := liveconsole.Apply(&state, event)
			if result.Status == liveconsole.ResultStale {
				break
			}
		case err := <-stream.Errors:
			if err != nil {
				t.Fatalf("rotated browser stream: %v", err)
			}
		case <-deadline:
			t.Fatal("rotated browser stream did not become read-only")
		}
	}
	if state.StreamHealth.State != liveconsole.HealthCredentialExpired ||
		!state.RequiresReseed ||
		state.CanMutate() {
		t.Fatalf("credential rotation did not fail closed: %+v", state)
	}

	time.Sleep(5 * time.Millisecond)
	base := strings.TrimSuffix(strings.Split(d.UIURL(), "#")[0], "/")
	if code := browserAuthedStatus(
		t,
		base+"/api/v1/operator/snapshot",
		staleToken,
	); code != http.StatusUnauthorized {
		t.Fatalf("expired browser credential status=%d want 401", code)
	}
	freshSnapshot := browserOperatorSnapshot(t, client, d)
	freshState, err := liveconsole.NewStateFromOperatorSnapshot(freshSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if freshSnapshot.CredentialGeneration != snapshot.CredentialGeneration+1 ||
		!freshState.CanMutate() {
		t.Fatalf("fresh browser seed did not restore authority: %+v", freshSnapshot)
	}

	freshStream := openBrowserSSE(t, d, freshToken)
	defer freshStream.Close()
	waitForBrowserSubscriberCount(t, d, 1)
	const freshEventID = "browser.rotation.fresh"
	if err := d.bus.publishCapabilityProjection(
		browserCapabilityProjection(freshEventID),
	); err != nil {
		t.Fatal(err)
	}
	freshEvent := nextBrowserSSEMatching(t, freshStream, func(event Event) bool {
		return event.Kind == liveconsole.KindCapability &&
			event.Entity.ID == freshEventID
	})
	if result := liveconsole.Apply(&freshState, freshEvent); result.Status != liveconsole.ResultApplied ||
		!freshState.CanMutate() {
		t.Fatalf("fresh credential stream did not resume live reduction: result=%+v state=%+v", result, freshState)
	}
}

func TestBrowserSSEDaemonRestartRejectsOldInstanceUntilReseed(t *testing.T) {
	store := testStore(t)
	first, err := Start(Options{Store: store})
	if err != nil {
		t.Fatalf("start first daemon: %v", err)
	}
	firstStopped := false
	t.Cleanup(func() {
		if !firstStopped {
			_ = first.Stop(context.Background())
		}
	})
	client := &http.Client{Timeout: 3 * time.Second}
	firstToken := first.Token()
	firstSnapshot := browserOperatorSnapshot(t, client, first)
	state, err := liveconsole.NewStateFromOperatorSnapshot(firstSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Stop(context.Background()); err != nil {
		t.Fatalf("stop first daemon: %v", err)
	}
	firstStopped = true

	second, err := Start(Options{Store: store})
	if err != nil {
		t.Fatalf("start second daemon: %v", err)
	}
	t.Cleanup(func() { _ = second.Stop(context.Background()) })
	if second.instanceID == firstSnapshot.InstanceID ||
		second.Token() == firstToken {
		t.Fatal("daemon restart reused instance or credential identity")
	}
	secondBase := strings.TrimSuffix(strings.Split(second.UIURL(), "#")[0], "/")
	if code := browserAuthedStatus(
		t,
		secondBase+"/api/v1/operator/snapshot",
		firstToken,
	); code != http.StatusUnauthorized {
		t.Fatalf("pre-restart browser credential status=%d want 401", code)
	}

	stream := openBrowserSSE(t, second, second.Token())
	defer stream.Close()
	waitForBrowserSubscriberCount(t, second, 1)
	const restartedEventID = "browser.restart"
	if err := second.bus.publishCapabilityProjection(
		browserCapabilityProjection(restartedEventID),
	); err != nil {
		t.Fatal(err)
	}
	restartedEvent := nextBrowserSSEMatching(t, stream, func(event Event) bool {
		return event.Kind == liveconsole.KindCapability &&
			event.Entity.ID == restartedEventID
	})
	result := liveconsole.Apply(&state, restartedEvent)
	if result.Status != liveconsole.ResultStale ||
		state.StreamHealth.State != liveconsole.HealthStale ||
		!state.RequiresReseed ||
		state.CanMutate() {
		t.Fatalf("old browser accepted a new daemon instance: result=%+v state=%+v", result, state)
	}

	secondSnapshot := browserOperatorSnapshot(t, client, second)
	reseedState, err := liveconsole.NewStateFromOperatorSnapshot(secondSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if secondSnapshot.InstanceID != second.instanceID ||
		secondSnapshot.InstanceID == firstSnapshot.InstanceID ||
		secondSnapshot.Sequence < restartedEvent.Seq ||
		!reseedState.CanMutate() ||
		reseedState.RequiresReseed {
		t.Fatalf("restart reseed did not establish new authority: %+v", secondSnapshot)
	}
}

type browserSSEStream struct {
	Events <-chan Event
	Errors <-chan error

	cancel context.CancelFunc
	body   io.Closer
}

func (stream *browserSSEStream) Close() {
	if stream == nil {
		return
	}
	stream.cancel()
	_ = stream.body.Close()
}

func openBrowserSSE(
	t *testing.T,
	d *Daemon,
	token string,
) *browserSSEStream {
	t.Helper()
	base := strings.TrimSuffix(strings.Split(d.UIURL(), "#")[0], "/")
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		base+eventsPath+"?token="+url.QueryEscape(token),
		nil,
	)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	request.Header.Set("Origin", base)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		cancel()
		t.Fatalf("open browser SSE: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		cancel()
		t.Fatalf("open browser SSE status=%d body=%s", response.StatusCode, raw)
	}
	events := make(chan Event, 128)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var event Event
			if err := json.Unmarshal(
				[]byte(strings.TrimPrefix(line, "data: ")),
				&event,
			); err != nil {
				errs <- err
				return
			}
			if err := liveconsole.ValidateEvent(event); err != nil {
				errs <- err
				return
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil &&
			!errors.Is(err, context.Canceled) &&
			ctx.Err() == nil {
			errs <- err
		}
	}()
	return &browserSSEStream{
		Events: events,
		Errors: errs,
		cancel: cancel,
		body:   response.Body,
	}
}

func nextBrowserSSEMatching(
	t *testing.T,
	stream *browserSSEStream,
	matches func(Event) bool,
) Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event, ok := <-stream.Events:
			if !ok {
				t.Fatal("browser SSE closed before matching event")
			}
			if matches(event) {
				return event
			}
		case err := <-stream.Errors:
			if err != nil {
				t.Fatalf("browser SSE: %v", err)
			}
		case <-deadline:
			t.Fatal("timed out waiting for browser SSE event")
		}
	}
}

func browserOperatorSnapshot(
	t *testing.T,
	client *http.Client,
	d *Daemon,
) manager.OperatorSnapshot {
	t.Helper()
	response := browserManagerJSON(
		t,
		client,
		d,
		http.MethodGet,
		"/api/v1/operator/snapshot?activityLimit=100",
		nil,
	)
	if response.Status != http.StatusOK {
		t.Fatalf(
			"operator snapshot status=%d body=%s",
			response.Status,
			response.Body,
		)
	}
	var snapshot manager.OperatorSnapshot
	browserDecodeData(t, response.Envelope, &snapshot)
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("invalid browser operator snapshot: %v", err)
	}
	return snapshot
}

func browserCapabilityProjection(id string) liveconsole.CapabilityProjection {
	return liveconsole.CapabilityProjection{
		ID:         id,
		Status:     workloadtypes.CoverageAvailable,
		Provider:   "browser-test",
		Mutable:    false,
		ActionRefs: []string{"activity.inspect"},
	}
}

func browserSubscriberCount(d *Daemon) int {
	d.bus.mu.Lock()
	defer d.bus.mu.Unlock()
	return len(d.bus.subs)
}

func waitForBrowserSubscriberCount(t *testing.T, d *Daemon, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := browserSubscriberCount(d); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"browser subscriber count=%d want=%d",
				browserSubscriberCount(d),
				want,
			)
		}
		time.Sleep(time.Millisecond)
	}
}

func browserAuthedStatus(t *testing.T, endpoint, token string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", endpoint, err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

func browserUIFragmentToken(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	values, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	return values.Get("token")
}

type blockingBrowserSSEWriter struct {
	header http.Header
	status int

	mu   sync.Mutex
	body bytes.Buffer

	headerFlushed     chan struct{}
	firstEventStarted chan struct{}
	releaseFirstEvent chan struct{}
	flushOnce         sync.Once
	eventOnce         sync.Once
}

func newBlockingBrowserSSEWriter() *blockingBrowserSSEWriter {
	return &blockingBrowserSSEWriter{
		header:            make(http.Header),
		headerFlushed:     make(chan struct{}),
		firstEventStarted: make(chan struct{}),
		releaseFirstEvent: make(chan struct{}),
	}
}

func (writer *blockingBrowserSSEWriter) Header() http.Header {
	return writer.header
}

func (writer *blockingBrowserSSEWriter) WriteHeader(status int) {
	writer.status = status
}

func (writer *blockingBrowserSSEWriter) Write(data []byte) (int, error) {
	block := false
	if bytes.HasPrefix(data, []byte("data: ")) {
		writer.eventOnce.Do(func() {
			block = true
			close(writer.firstEventStarted)
		})
	}
	if block {
		<-writer.releaseFirstEvent
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.body.Write(data)
}

func (writer *blockingBrowserSSEWriter) Flush() {
	writer.flushOnce.Do(func() { close(writer.headerFlushed) })
}

func (writer *blockingBrowserSSEWriter) Bytes() []byte {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return append([]byte(nil), writer.body.Bytes()...)
}

func decodeBrowserSSEBody(t *testing.T, body []byte) []Event {
	t.Helper()
	var events []Event
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event Event
		if err := json.Unmarshal(
			[]byte(strings.TrimPrefix(line, "data: ")),
			&event,
		); err != nil {
			t.Fatal(err)
		}
		if err := liveconsole.ValidateEvent(event); err != nil {
			t.Fatalf("invalid browser SSE event: %v", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}
