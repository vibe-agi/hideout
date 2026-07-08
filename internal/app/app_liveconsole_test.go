package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
)

func TestTUILiveConsoleRendersTypedEvents(t *testing.T) {
	seed := liveconsole.BuildSeed(liveconsole.SeedInput{StreamHealth: liveconsole.HealthLive})
	state := liveconsole.NewState(seed)
	for _, ev := range liveconsole.RepresentativeEvents() {
		liveconsole.Apply(&state, ev)
	}
	var out bytes.Buffer
	writeTUILiveDashboard(&out, state, nil, "")
	text := out.String()
	for _, want := range []string{
		"Stream: disconnected",
		"alpha-env",
		"ses_alpha",
		"bg-1  op=environment-clean  status=completed",
		"host.open",
		"HostFS Writes",
		"hfwdec_123",
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
