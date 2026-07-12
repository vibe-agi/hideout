package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestTUILiveConsoleRendersTypedEvents(t *testing.T) {
	seed := liveconsole.BuildSeed(liveconsole.SeedInput{
		StreamHealth: liveconsole.HealthLive,
		StatusRows: []liveconsole.StatusRow{
			{ID: "doctor", Label: "Doctor", Status: "explicit", Detail: "not run on load", Next: "hideout doctor --level light"},
			{ID: "package", Label: "Package", Status: "read-only", Detail: "bundles=1 enabled=1 adapterPacks=1", Next: "hideout package verify <install-prefix>"},
			{ID: "support", Label: "Support", Status: "matrix", Detail: "platform/darwin/arm64:first-class backend/lima:first-class", Next: "hideout support matrix"},
		},
	})
	state := liveconsole.NewState(seed)
	for _, ev := range liveconsole.RepresentativeEvents() {
		liveconsole.Apply(&state, ev)
	}
	var out bytes.Buffer
	writeTUILiveDashboard(&out, state, nil, "")
	text := out.String()
	for _, want := range []string{
		"Stream: disconnected",
		"Operator Console",
		"action-required total=3 hostfs=1 decisions=1 notices=1",
		"Doctor  status=explicit",
		"hideout doctor --level light",
		"Package  status=read-only",
		"Support  status=matrix",
		"alpha-env",
		"ses_alpha",
		"bg-1  op=environment-clean  status=completed",
		"host.open",
		"HostFS Writes",
		"hfwdec_123",
		"Decisions",
		"dec_share_123",
		"Notices",
		"notice_priv_123",
		"Exports",
		"source=audit",
		"Cleanup",
		"secrets=removed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("live TUI output missing %q:\n%s", want, text)
		}
	}
}

func TestBuildTUILiveStateSeedsExistingActions(t *testing.T) {
	core := manager.New(profile.Store{Root: t.TempDir()})
	if _, err := core.CreateDecision(decision.Decision{
		ID:             "dec-existing",
		Kind:           decision.KindEvidenceShare,
		Source:         decision.Source{Profile: "alpha"},
		State:          decision.StatePending,
		Preview:        decision.Preview{Summary: "share evidence"},
		AllowedActions: []string{decision.ActionApprove, decision.ActionDeny},
		DefaultOutcome: decision.DefaultOutcomeNoRelease,
		TimeoutAt:      time.Now().Add(time.Hour),
		AuditRef:       "audit:decision:existing",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := core.CreateNotice(decision.Notice{
		ID:       "notice-existing",
		Kind:     decision.KindPrivilegeStatus,
		Source:   decision.Source{Profile: "alpha"},
		Severity: decision.NoticeSeverityWarning,
		Status:   "degraded",
		Preview:  decision.Preview{Summary: "privilege degraded"},
		AuditRef: "audit:notice:existing",
	}); err != nil {
		t.Fatal(err)
	}

	state, err := buildTUILiveState(context.Background(), core, "alpha", liveconsole.HealthDaemonless)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Decisions) != 1 || state.Decisions[0].ID != "dec-existing" {
		t.Fatalf("existing decision missing from seed: %+v", state.Decisions)
	}
	if len(state.Notices) != 1 || state.Notices[0].ID != "notice-existing" {
		t.Fatalf("existing notice missing from seed: %+v", state.Notices)
	}
	if summary := liveconsole.ActionRequired(state); summary.Decisions != 1 || summary.Notices != 1 {
		t.Fatalf("seed action summary mismatch: %+v", summary)
	}
}

func TestTUILiveConsoleDoesNotExposeUnrecognizedActionRoutes(t *testing.T) {
	state := liveconsole.NewState(liveconsole.BuildSeed(liveconsole.SeedInput{
		StreamHealth: liveconsole.HealthLive,
		StatusRows: []liveconsole.StatusRow{
			{ID: "doctor", Label: "Doctor", Status: "explicit", Next: "hideout doctor --level light"},
			{ID: "support", Label: "Support", Status: "matrix", Next: "hideout support matrix"},
		},
	}))
	for _, ev := range liveconsole.RepresentativeEvents() {
		liveconsole.Apply(&state, ev)
	}
	var out bytes.Buffer
	writeTUILiveDashboard(&out, state, nil, "")
	text := out.String()
	for _, forbidden := range []string{"/api/v1/", "/daemon/", "decision/claim", "notice/ack"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("TUI compact dashboard should not expose HTTP action route %q:\n%s", forbidden, text)
		}
	}
	for _, want := range []string{"hideout doctor --level light", "hideout support matrix"} {
		if !strings.Contains(text, want) {
			t.Fatalf("TUI compact dashboard should keep copyable guidance %q:\n%s", want, text)
		}
	}
}

func TestTUILiveConsoleRendersHostFSReadActionsAndTerminalReopen(t *testing.T) {
	state := liveconsole.NewState(liveconsole.BuildSeed(liveconsole.SeedInput{StreamHealth: liveconsole.HealthLive}))
	state.Decisions = []liveconsole.DecisionRow{
		{ID: "dec_hfr_pending", Kind: decision.KindHostFSRead, Status: decision.StatePending, Profile: "alpha", Session: "ses_alpha", Reason: "untrusted: inspect referenced specification"},
		{ID: "dec_hfr_denied", Kind: decision.KindHostFSRead, Status: decision.StateDenied, Profile: "alpha", Session: "ses_alpha"},
	}
	var out bytes.Buffer
	writeTUILiveDashboard(&out, state, nil, "alpha")
	text := out.String()
	for _, want := range []string{
		"dec_hfr_pending  kind=hostfs.read  status=pending",
		"claim=hideout decision claim dec_hfr_pending",
		"approve=hideout decision approve dec_hfr_pending",
		"deny=hideout decision deny dec_hfr_pending",
		"hideout decision reopen dec_hfr_denied",
		"untrusted: inspect referenced specification",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("HostFS read TUI action missing %q:\n%s", want, text)
		}
	}
}

func TestTUILiveConsoleRedactsActionRows(t *testing.T) {
	state := liveconsole.NewState(liveconsole.BuildSeed(liveconsole.SeedInput{StreamHealth: liveconsole.HealthLive}))
	liveconsole.Apply(&state, liveconsole.Event{
		Version: liveconsole.EventVersion,
		Kind:    liveconsole.KindHostFSWrite,
		Seq:     1,
		Payload: liveconsole.EventPayload{
			DecisionID:  "hfwdec_secret",
			OperationID: "hfwop_secret",
			Status:      "pending",
			Operation:   "replace",
			Path:        "/hostfs-overlay/objects/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Reason:      "HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:1 keep-me",
		},
	})
	var out bytes.Buffer
	writeTUILiveDashboard(&out, state, nil, "")
	text := out.String()
	for _, forbidden := range []string{"0123456789abcdef0123456789abcdef", "socks5://127.0.0.1:1", "HIDEOUT_SECRET_DEFAULT_PROXY"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("TUI console leaked %q:\n%s", forbidden, text)
		}
	}
	if !strings.Contains(text, "keep-me") || !strings.Contains(text, "hfwdec_secret") {
		t.Fatalf("TUI console should keep local context:\n%s", text)
	}
}

func TestTUILiveConsoleDoesNotIntervalPollWhileStreamHealthy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	events := make(chan liveconsole.Event)
	state := liveconsole.NewState(liveconsole.BuildSeed(liveconsole.SeedInput{StreamHealth: liveconsole.HealthLive}))
	var renders atomic.Int32
	err := watchLiveDashboard(ctx, events, 5*time.Millisecond, &state, func(liveconsole.State) error {
		renders.Add(1)
		return nil
	}, func() error {
		t.Fatal("fallback polling should not run while the event stream is healthy")
		return nil
	})
	if err != nil {
		t.Fatalf("watchLiveDashboard: %v", err)
	}
	if got := renders.Load(); got != 1 {
		t.Fatalf("healthy idle stream should render only the seed, got %d renders", got)
	}
}

func TestTUILiveConsoleRendersOncePerEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan liveconsole.Event)
	state := liveconsole.NewState(liveconsole.BuildSeed(liveconsole.SeedInput{StreamHealth: liveconsole.HealthLive}))
	rendered := make(chan int, 4)
	var renders atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- watchLiveDashboard(ctx, events, time.Hour, &state, func(liveconsole.State) error {
			rendered <- int(renders.Add(1))
			return nil
		}, func() error { return nil })
	}()
	<-rendered // seed
	events <- liveconsole.RepresentativeEvents()[0]
	<-rendered // event
	events <- liveconsole.RepresentativeEvents()[1]
	<-rendered // event
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("watchLiveDashboard: %v", err)
	}
	if got := renders.Load(); got != 3 {
		t.Fatalf("expected seed + 2 event renders, got %d", got)
	}
}

func TestTUILiveConsoleMarksDisconnectedBeforeFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan liveconsole.Event)
	state := liveconsole.NewState(liveconsole.BuildSeed(liveconsole.SeedInput{StreamHealth: liveconsole.HealthLive}))
	rendered := make(chan string, 4)
	fallback := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- watchLiveDashboard(ctx, events, 5*time.Millisecond, &state, func(state liveconsole.State) error {
			rendered <- state.StreamHealth.State
			return nil
		}, func() error {
			select {
			case fallback <- struct{}{}:
			default:
			}
			return nil
		})
	}()
	if got := <-rendered; got != liveconsole.HealthLive {
		t.Fatalf("initial health = %s", got)
	}
	close(events)
	if got := <-rendered; got != liveconsole.HealthDisconnected {
		t.Fatalf("stream close should render disconnected before fallback, got %s", got)
	}
	select {
	case <-fallback:
	case <-time.After(time.Second):
		t.Fatal("daemon-less interval fallback did not start after stream close")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("watchLiveDashboard: %v", err)
	}
}

func TestTUILiveConsoleProofArtifact(t *testing.T) {
	state := liveconsole.NewState(liveconsole.BuildSeed(liveconsole.SeedInput{StreamHealth: liveconsole.HealthLive}))
	var before bytes.Buffer
	writeTUILiveDashboard(&before, state, nil, "")
	if strings.Contains(before.String(), "alpha-env") {
		t.Fatalf("before proof unexpectedly contains future event state:\n%s", before.String())
	}

	ev := liveconsole.RepresentativeEvents()[0]
	liveconsole.Apply(&state, ev)
	liveconsole.Apply(&state, liveconsole.Event{
		Version: liveconsole.EventVersion,
		Kind:    liveconsole.KindAudit,
		Seq:     2,
		Payload: liveconsole.EventPayload{
			Action:   "host.open",
			Decision: "allow",
			Details: map[string]any{
				"capabilityToken": "cap_0123456789abcdef0123456789abcdef",
				"note":            "keep-me",
				"message":         "HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:1",
			},
		},
	})
	var after bytes.Buffer
	writeTUILiveDashboard(&after, state, nil, "")
	auditData, err := json.Marshal(state.AuditTail)
	if err != nil {
		t.Fatal(err)
	}
	proof := struct {
		BeforeClean   bool
		AfterUpdated  bool
		OverviewReads int
		AuditReads    int
		RedactionOK   bool
	}{
		BeforeClean:   !strings.Contains(before.String(), "alpha-env"),
		AfterUpdated:  strings.Contains(after.String(), "alpha-env"),
		OverviewReads: 0,
		AuditReads:    0,
		RedactionOK: !strings.Contains(string(auditData), "cap_"+"0123456789abcdef") &&
			!strings.Contains(string(auditData), "socks5://127.0.0.1:1") &&
			strings.Contains(string(auditData), "keep-me"),
	}
	if !proof.BeforeClean || !proof.AfterUpdated || proof.OverviewReads != 0 || proof.AuditReads != 0 || !proof.RedactionOK {
		t.Fatalf("TUI live proof failed: %+v\nbefore:\n%s\nafter:\n%s", proof, before.String(), after.String())
	}
	t.Logf("TUI live proof: %+v", proof)
}
