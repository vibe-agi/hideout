package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func readEventStream(t *testing.T, d *Daemon, token string, timeout time.Duration) []Event {
	t.Helper()
	snapshot, err := d.operatorSnapshot(
		context.Background(),
		manager.OperatorSnapshotQuery{ActivityLimit: 100},
	)
	if err != nil {
		t.Fatalf("read authoritative event-stream sequence: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", d.Socket())
		},
	}}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"http://localhost"+eventsPath+"?since="+
			strconv.Itoa(snapshot.Sequence),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "localhost"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var events []Event
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev Event
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) == nil {
			assertValidDaemonEvent(t, ev)
			events = append(events, ev)
			if ev.Kind == "terminal" {
				return events
			}
		}
	}
	return events
}

// T022: events are redacted, ordered, and non-durable (a late subscriber sees no
// history).
func TestEventBusRedactsOrdersAndKeepsNoHistory(t *testing.T) {
	bus := newEventBus()
	sub := bus.subscribe(16)

	bus.OperationEvent("operation", "start", map[string]any{
		"capabilityToken": "cap_0123456789abcdef0123456789abcdef",
		"note":            "keep-me",
	})
	bus.publishAudit("host.open", "allow", map[string]any{"target": "user-url"})

	first := <-sub.ch
	if first.Seq != 1 || first.Kind != liveconsole.KindSession {
		t.Fatalf("unexpected first event: %+v", first)
	}
	assertValidDaemonEvent(t, first)
	if got := first.Payload.Details["capabilityToken"]; got != "REDACTED" && got != nil {
		t.Fatalf("control-plane token not redacted on stream: %v", first.Payload)
	}
	if first.Payload.Details["note"] != "keep-me" {
		t.Fatalf("local user data should be verbatim: %v", first.Payload)
	}
	second := <-sub.ch
	if second.Seq != 2 || second.Kind != liveconsole.KindAudit {
		t.Fatalf("events out of order: %+v", second)
	}
	assertValidDaemonEvent(t, second)

	// No history: a late subscriber receives nothing already published.
	late := bus.subscribe(4)
	select {
	case ev := <-late.ch:
		t.Fatalf("late subscriber replayed history: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEventBusV2BindsIdentityAndPublishesBoundedProjectionDeltas(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	generation := uint64(4)
	bus := newEventBusV2("daemon_fixture", func() uint64 { return generation })
	sub := bus.subscribe(16)
	desired := profile.Default("default")
	profileProjection := manager.ProfileProjection{
		Schema: manager.ProfileProjectionSchema, Profile: "default", Revision: 2,
		ContentDigest: "sha256:" + strings.Repeat("a", 64), Desired: desired,
		Effective: manager.ProfileEffective{
			Status: manager.EffectiveNotObserved, Sessions: []manager.EffectiveSessionSnapshot{},
		},
		UpdatedAt: now,
	}
	transition := liveconsole.TransitionProjection{
		Profile: "default",
		Transition: manager.ProfileTransition{
			OperationID: "op_projection01", Kind: "network.proxy", Phase: "staging",
			StartedAt: now,
		},
	}
	operation := manager.Operation{
		Schema: manager.OperationSchema, ID: "op_projection01", Kind: "profile.update",
		Owner:      manager.OperationOwner{Kind: "profile", ID: "default"},
		PlanDigest: "sha256:" + strings.Repeat("b", 64), BaseRevision: 2,
		Phase: manager.OperationPlanned, Effects: []manager.EffectResult{},
		Recovery: manager.Recovery{
			Code: "retry-operation", Summary: "Retry with the same operation identity.",
		},
		CreatedAt: now, UpdatedAt: now,
	}
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-fixture")
	if err != nil {
		t.Fatal(err)
	}
	coverage := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema, ID: "cov_alpha000001",
		Owner: owner, SessionID: "ses_alpha", Subsystem: workloadtypes.SubsystemFile,
		State: workloadtypes.CoverageAvailable, Reason: "observer-ready",
		CollectorGeneration: 1, StartedAt: now,
	}
	risk := liveconsole.RiskFinding{
		ID: "risk_alpha000001", RuleID: "file.outside-workspace", RuleVersion: "v1",
		Severity: "high", Title: "wrote outside workspace",
		Explanation:  "a descendant wrote outside the workspace",
		EvidenceRefs: []string{"act_alpha000001"}, Confidence: "exact",
		PolicyStatus: "not-evaluated", FirstAt: now, LastAt: now, Count: 1,
		NextAction: "activity.files",
	}
	capability := liveconsole.CapabilityProjection{
		ID: "activity.file", Status: workloadtypes.CoverageAvailable,
		Provider: "ebpf", Mutable: false, ActionRefs: []string{"activity.files"},
	}

	publishers := []func() error{
		func() error { return bus.publishProfileProjection(profileProjection) },
		func() error { return bus.publishTransitionProjection(transition) },
		func() error { return bus.publishOperationProjection(operation) },
		func() error {
			bus.OperationEvent(liveconsole.KindSession, "progress", map[string]any{
				"id": "ses_alpha", "profile": "default", "status": "running",
			})
			return nil
		},
		func() error {
			return bus.publishActivityProjection("default", "ses_alpha", liveconsole.ActivityProjectionDelta{
				Cursor: "cursor-1", Counts: []liveconsole.ActivityCount{{Kind: "file", Count: 1}},
				Appended: 1, LastAt: now,
			})
		},
		func() error {
			return bus.publishCoverageProjection("default", "ses_alpha", []workloadtypes.CoverageInterval{coverage})
		},
		func() error { return bus.publishRiskProjection("default", "ses_alpha", risk) },
		func() error { return bus.publishCapabilityProjection(capability) },
	}
	for index, publish := range publishers {
		if err := publish(); err != nil {
			t.Fatalf("publisher %d: %v", index, err)
		}
		event := <-sub.ch
		if event.Version != liveconsole.EventVersionV2 ||
			event.InstanceID != "daemon_fixture" ||
			event.CredentialGeneration != generation ||
			event.Seq != index+1 {
			t.Fatalf("event %d identity/sequence mismatch: %+v", index, event)
		}
		assertValidDaemonEvent(t, event)
	}
	before := bus.seq
	if err := bus.publishCoverageProjection(
		"default", "ses_alpha", make([]workloadtypes.CoverageInterval, 65),
	); err == nil {
		t.Fatal("oversized coverage projection was published")
	}
	if bus.seq != before {
		t.Fatalf("rejected projection consumed sequence: before=%d after=%d", before, bus.seq)
	}
	terminal := bus.terminalEvent(sub, "subscriber-overflow")
	if terminal.Version != liveconsole.EventVersionV2 || terminal.Seq != 0 ||
		terminal.InstanceID != "daemon_fixture" || terminal.CredentialGeneration != generation {
		t.Fatalf("v2 terminal invented broadcast sequence or lost identity: %+v", terminal)
	}
	assertValidDaemonEvent(t, terminal)
}

// T023: a slow subscriber whose bounded buffer fills is dropped with a terminal
// signal rather than stalling the bus.
func TestEventBusBackpressureDropsSlowSubscriber(t *testing.T) {
	bus := newEventBus()
	slow := bus.subscribe(1) // tiny buffer, never drained
	fast := bus.subscribe(64)

	for i := 0; i < 5; i++ {
		bus.publish(Event{
			Kind:  liveconsole.KindSession,
			Phase: "progress",
			Entity: liveconsole.EntityRef{
				Kind: liveconsole.KindSession,
				ID:   "op",
			},
			Payload: liveconsole.EventPayload{
				ID:      "op",
				Status:  "running",
				Details: map[string]any{"i": i},
			},
		})
	}

	select {
	case <-slow.done:
	case <-time.After(time.Second):
		t.Fatal("slow subscriber should have been terminated")
	}
	// The fast subscriber is unaffected.
	select {
	case ev := <-fast.ch:
		if ev.Kind != liveconsole.KindSession {
			t.Fatalf("fast subscriber got wrong event: %+v", ev)
		}
		assertValidDaemonEvent(t, ev)
	case <-time.After(time.Second):
		t.Fatal("fast subscriber should still receive events")
	}
}

func TestEventBusBackpressureDoesNotBlockOtherSubscribers(t *testing.T) {
	bus := newEventBus()
	slow := bus.subscribe(1)
	fastA := bus.subscribe(16)
	fastB := bus.subscribe(16)

	for i := 0; i < 6; i++ {
		bus.OperationEvent(liveconsole.KindSession, "progress", map[string]any{"id": "op"})
	}
	select {
	case <-slow.done:
	case <-time.After(time.Second):
		t.Fatal("slow subscriber should be terminated when its buffer fills")
	}
	for name, sub := range map[string]*subscriber{"fastA": fastA, "fastB": fastB} {
		for i := 0; i < 6; i++ {
			select {
			case ev := <-sub.ch:
				if ev.Kind != liveconsole.KindSession || ev.Payload.ID != "op" {
					t.Fatalf("%s got unexpected event: %+v", name, ev)
				}
			case <-time.After(time.Second):
				t.Fatalf("%s was blocked by the slow subscriber", name)
			}
		}
	}
}

func TestTerminalEventDoesNotConsumeBroadcastSequence(t *testing.T) {
	bus := newEventBus()
	expiring := bus.subscribe(8)
	other := bus.subscribe(8)
	bus.OperationEvent(liveconsole.KindEnvironment, "complete", map[string]any{"id": "env-1"})
	if ev := <-expiring.ch; ev.Seq != 1 {
		t.Fatalf("expiring subscriber first seq=%d", ev.Seq)
	}
	if ev := <-other.ch; ev.Seq != 1 {
		t.Fatalf("other subscriber first seq=%d", ev.Seq)
	}
	terminal := bus.terminalEvent(expiring, "credential invalidated")
	if terminal.Seq != 2 {
		t.Fatalf("terminal seq=%d want subscriber-local 2", terminal.Seq)
	}
	bus.OperationEvent(liveconsole.KindEnvironment, "complete", map[string]any{"id": "env-2"})
	next := <-other.ch
	if next.Seq != 2 {
		t.Fatalf("terminal event consumed global sequence; other subscriber got seq=%d, want 2", next.Seq)
	}
	state := liveconsole.NewState(liveconsole.BuildSeed(liveconsole.SeedInput{StreamHealth: liveconsole.HealthLive}))
	if result := liveconsole.Apply(&state, liveconsole.Event{Version: liveconsole.EventVersion, Kind: liveconsole.KindEnvironment, Seq: 1, Payload: liveconsole.EventPayload{ID: "env-1"}}); result.Status != liveconsole.ResultApplied {
		t.Fatalf("apply first event: %+v", result)
	}
	if result := liveconsole.Apply(&state, liveconsole.Event{Version: liveconsole.EventVersion, Kind: liveconsole.KindEnvironment, Seq: next.Seq, Payload: liveconsole.EventPayload{ID: "env-2"}}); result.Status != liveconsole.ResultApplied {
		t.Fatalf("other subscriber should not see a sequence gap: %+v", result)
	}
}

func TestEventBusBuildsExportAndCleanupPayloadsFromOperationEvents(t *testing.T) {
	bus := newEventBus()
	sub := bus.subscribe(8)
	bus.OperationEvent(liveconsole.KindExport, "complete", map[string]any{
		"source":          "audit",
		"artifactPath":    "/tmp/export.json",
		"decision":        "redact",
		"capabilityToken": "cap_0123456789abcdef0123456789abcdef",
	})
	exportEvent := <-sub.ch
	if exportEvent.Kind != liveconsole.KindExport || exportEvent.Payload.Source != "audit" || exportEvent.Payload.ArtifactPath != "/tmp/export.json" || exportEvent.Payload.Status != "completed" {
		t.Fatalf("export payload mismatch: %+v", exportEvent)
	}
	data, _ := json.Marshal(exportEvent)
	if strings.Contains(string(data), "cap_0123456789abcdef") {
		t.Fatalf("export event leaked control-plane material: %s", data)
	}

	bus.OperationEvent(liveconsole.KindCleanup, "complete", map[string]any{
		"id":                           "ses_cleanup",
		"sessions":                     1,
		"removedTypes":                 []string{"tmp", "brokerSocket"},
		"secretState":                  "removed",
		"machineId":                    "0123456789abcdef0123456789abcdef",
		"HIDEOUT_SECRET_DEFAULT_PROXY": "socks5://127.0.0.1:1",
	})
	cleanupEvent := <-sub.ch
	if cleanupEvent.Kind != liveconsole.KindCleanup || cleanupEvent.Payload.ID != "ses_cleanup" || cleanupEvent.Payload.Sessions != 1 || cleanupEvent.Payload.SecretState != "removed" {
		t.Fatalf("cleanup payload mismatch: %+v", cleanupEvent)
	}
	if len(cleanupEvent.Payload.Removed) != 2 || cleanupEvent.Payload.Removed[0] != "tmp" {
		t.Fatalf("cleanup removed types mismatch: %+v", cleanupEvent.Payload.Removed)
	}
	data, _ = json.Marshal(cleanupEvent)
	if strings.Contains(string(data), "0123456789abcdef0123456789abcdef") || strings.Contains(string(data), "socks5://127.0.0.1:1") {
		t.Fatalf("cleanup event leaked control-plane material: %s", data)
	}
}

func TestEventBusBuildsDecisionAndNoticePayloads(t *testing.T) {
	bus := newEventBus()
	sub := bus.subscribe(8)
	bus.OperationEvent(liveconsole.KindDecision, "created", map[string]any{
		"decisionId":     "dec-1",
		"kind":           "hostfs.write",
		"status":         "pending",
		"defaultOutcome": "discard",
		"profile":        "default",
		"preview":        map[string]any{"summary": "Review staged write"},
		"revision":       3,
		"claimToken":     "claim_0123456789abcdef0123456789abcdef",
	})
	decisionEvent := <-sub.ch
	if decisionEvent.Kind != liveconsole.KindDecision ||
		decisionEvent.Payload.DecisionID != "dec-1" ||
		decisionEvent.Payload.RecordKind != "hostfs.write" ||
		decisionEvent.Payload.Revision != 3 {
		t.Fatalf("decision payload mismatch: %+v", decisionEvent)
	}
	assertValidDaemonEvent(t, decisionEvent)
	data, _ := json.Marshal(decisionEvent)
	if strings.Contains(string(data), "claim_0123456789abcdef") {
		t.Fatalf("decision event leaked claim token: %s", data)
	}

	bus.OperationEvent(liveconsole.KindNotice, "created", map[string]any{
		"noticeId":     "notice-1",
		"kind":         "privilege.status",
		"status":       "degraded",
		"severity":     "warning",
		"acknowledged": false,
		"preview":      map[string]any{"summary": "Privilege coverage degraded"},
		"revision":     2,
	})
	noticeEvent := <-sub.ch
	if noticeEvent.Kind != liveconsole.KindNotice ||
		noticeEvent.Payload.NoticeID != "notice-1" ||
		noticeEvent.Payload.RecordKind != "privilege.status" ||
		noticeEvent.Payload.Revision != 2 {
		t.Fatalf("notice payload mismatch: %+v", noticeEvent)
	}
	assertValidDaemonEvent(t, noticeEvent)
}

func TestEventBusBuildsHostFSWritePayloadsWithoutClaimTokens(t *testing.T) {
	bus := newEventBus()
	sub := bus.subscribe(8)
	statuses := []string{"pending", "claimed", "applied", "discarded", "expired", "conflict"}
	var ev liveconsole.Event
	for i, status := range statuses {
		bus.OperationEvent(liveconsole.KindHostFSWrite, status, map[string]any{
			"operationId":     "hfwop_123",
			"decisionId":      "hfwdec_123",
			"status":          status,
			"operation":       "replace",
			"path":            "/Users/alice/file.txt",
			"privilegeStatus": "enforced",
			"claimToken":      "claim_0123456789abcdef",
		})
		ev = <-sub.ch
		if ev.Kind != liveconsole.KindHostFSWrite || ev.Payload.DecisionID != "hfwdec_123" || ev.Payload.OperationID != "hfwop_123" || ev.Payload.Status != status {
			t.Fatalf("HostFS write payload mismatch at %d: %+v", i, ev)
		}
		assertValidDaemonEvent(t, ev)
		data, _ := json.Marshal(ev)
		if strings.Contains(string(data), "claim_0123456789abcdef") {
			t.Fatalf("HostFS write event leaked claim token: %s", data)
		}
	}
	state := liveconsole.NewState(liveconsole.BuildSeed(liveconsole.SeedInput{StreamHealth: liveconsole.HealthLive}))
	result := liveconsole.Apply(&state, liveconsole.Event{
		Version: ev.Version,
		Kind:    ev.Kind,
		Seq:     ev.Seq,
		Entity:  ev.Entity,
		Payload: ev.Payload,
	})
	if result.Status != liveconsole.ResultApplied || len(state.HostFSWrites) != 1 || state.HostFSWrites[0].DecisionID != "hfwdec_123" {
		t.Fatalf("HostFS write reducer result=%+v state=%+v", result, state.HostFSWrites)
	}
}

func TestEventBusProducerMappingsMatchLiveCatalog(t *testing.T) {
	mappings := liveconsole.EventProducerMappings()
	cases := []struct {
		producer string
		want     string
		details  map[string]any
	}{
		{liveconsole.KindEnvironment, liveconsole.KindEnvironment, map[string]any{"id": "env-1"}},
		{liveconsole.KindSession, liveconsole.KindSession, map[string]any{"id": "ses-1"}},
		{liveconsole.KindBackground, liveconsole.KindBackground, map[string]any{"id": "bg-1", "op": "environment-clean"}},
		{liveconsole.KindExport, liveconsole.KindExport, map[string]any{"status": "completed", "source": "audit"}},
		{liveconsole.KindCleanup, liveconsole.KindCleanup, map[string]any{"status": "completed", "id": "cleanup-1"}},
		{liveconsole.KindHostFSWrite, liveconsole.KindHostFSWrite, map[string]any{"decisionId": "hfwdec-1", "operationId": "hfwop-1", "status": "pending"}},
		{liveconsole.KindDecision, liveconsole.KindDecision, map[string]any{"decisionId": "dec-1", "kind": "evidence.share", "status": "pending"}},
		{liveconsole.KindNotice, liveconsole.KindNotice, map[string]any{"noticeId": "notice-1", "kind": "privilege.status", "status": "degraded"}},
		{"host-app", liveconsole.KindAudit, map[string]any{"action": "host.app.update", "decision": "allow", "profile": "privacy"}},
		{"run", liveconsole.KindSession, map[string]any{"id": "ses-run"}},
		{"operation", liveconsole.KindSession, map[string]any{"id": "op-1"}},
		{"future-producer", liveconsole.KindSession, map[string]any{"id": "future-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.producer, func(t *testing.T) {
			want := mappings[tc.producer]
			if want == "" {
				want = mappings["*"]
			}
			if want != tc.want {
				t.Fatalf("catalog maps %s to %s, want %s", tc.producer, want, tc.want)
			}
			bus := newEventBus()
			sub := bus.subscribe(4)
			bus.OperationEvent(tc.producer, "complete", tc.details)
			ev := <-sub.ch
			if ev.Kind != tc.want {
				t.Fatalf("producer %s emitted %s, want %s: %+v", tc.producer, ev.Kind, tc.want, ev)
			}
			assertValidDaemonEvent(t, ev)
		})
	}
}

func TestEventBusNonOperationProducersMatchLiveCatalog(t *testing.T) {
	mappings := liveconsole.EventProducerMappings()
	for _, producer := range []string{liveconsole.KindAudit, liveconsole.KindTerminal} {
		if mappings[producer] == "" {
			t.Fatalf("producer %s missing from live catalog", producer)
		}
	}
	bus := newEventBus()
	sub := bus.subscribe(4)
	bus.publishAudit("run", "allow", map[string]any{"note": "keep-me"})
	auditEvent := <-sub.ch
	if auditEvent.Kind != liveconsole.KindAudit {
		t.Fatalf("audit producer emitted %+v", auditEvent)
	}
	assertValidDaemonEvent(t, auditEvent)
	terminal := bus.terminalEvent(sub, "stream closed")
	if terminal.Kind != liveconsole.KindTerminal {
		t.Fatalf("terminal producer emitted %+v", terminal)
	}
	assertValidDaemonEvent(t, terminal)
}

// T024: the subscribe endpoint is a separate surface outside /api/v1/ with the
// same auth.
func TestEventsEndpointSeparateSurfaceAndAuth(t *testing.T) {
	d := startTestDaemon(t)
	if code, _ := daemonDo(t, d, http.MethodGet, eventsPath, ""); code != http.StatusUnauthorized {
		t.Fatalf("/daemon/events without token: want 401, got %d", code)
	}
	if code, _ := daemonDo(t, d, http.MethodGet, "/api/v1/events", d.Token()); code != http.StatusNotFound {
		t.Fatalf("/api/v1/events should not be a Manager route, got %d", code)
	}
}

// T022 (wire): an operation event flows over the SSE endpoint, redacted.
func TestEventsStreamDeliversRedactedEvents(t *testing.T) {
	d := startTestDaemon(t)
	done := make(chan []Event, 1)
	go func() { done <- readEventStream(t, d, d.Token(), 2*time.Second) }()
	time.Sleep(150 * time.Millisecond) // ensure the subscriber is attached
	d.bus.OperationEvent("operation", "start", map[string]any{
		"capabilityToken": "cap_0123456789abcdef0123456789abcdef",
		"note":            "keep-me",
	})
	time.Sleep(150 * time.Millisecond)
	_ = d.Stop(context.Background()) // triggers a terminal event so the reader returns
	events := <-done
	sawOp := false
	for _, ev := range events {
		assertValidDaemonEvent(t, ev)
		if ev.Kind == liveconsole.KindSession {
			sawOp = true
			if s, _ := json.Marshal(ev); strings.Contains(string(s), "cap_0123456789abcdef") {
				t.Fatalf("stream leaked control-plane token: %s", s)
			}
		}
	}
	if !sawOp {
		t.Fatalf("did not observe the operation event on the stream: %+v", events)
	}
}

// T026: mid-stream credential invalidation — an active subscription terminates
// when the credential expires; a resubscribe with the stale token is refused.
func TestEventsMidStreamCredentialInvalidation(t *testing.T) {
	store := testStore(t)
	d, err := Start(Options{Store: store, TTL: 400 * time.Millisecond})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })

	staleToken := d.Token()
	events := readEventStream(t, d, staleToken, 3*time.Second)
	sawTerminal := false
	for _, ev := range events {
		assertValidDaemonEvent(t, ev)
		if ev.Kind == "terminal" {
			sawTerminal = true
		}
	}
	if !sawTerminal {
		t.Fatalf("stream should terminate on credential expiry, got: %+v", events)
	}
	// The now-expired token is refused on resubscribe (and is auditable via T009).
	if code, _ := daemonDo(t, d, http.MethodGet, eventsPath, staleToken); code != http.StatusUnauthorized {
		t.Fatalf("resubscribe with expired token: want 401, got %d", code)
	}
}

func assertValidDaemonEvent(t *testing.T, ev Event) {
	t.Helper()
	if err := liveconsole.ValidateEvent(ev); err != nil {
		t.Fatalf("invalid daemon event %+v: %v", ev, err)
	}
}
