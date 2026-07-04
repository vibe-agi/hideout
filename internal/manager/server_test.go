package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestStartLocalServerServesUIAndAPI(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	if err := store.Save(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := StartLocalServer(ctx, LocalServerOptions{
		Core: Core{
			Store: store,
			Backends: []BackendCheck{
				{Name: "native", Isolation: "weak"},
			},
		},
		Token: "ui_token",
		TTL:   time.Minute,
	})
	if err != nil {
		t.Fatalf("StartLocalServer: %v", err)
	}
	defer server.Close()
	if !strings.HasPrefix(server.URL, "http://127.0.0.1:") || !strings.Contains(server.UIURL, "#token=ui_token") {
		t.Fatalf("unexpected URLs: %+v", server)
	}
	rootResp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer rootResp.Body.Close()
	if rootResp.StatusCode != http.StatusOK || rootResp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected root response: status=%d headers=%v", rootResp.StatusCode, rootResp.Header)
	}
	rootHTML, err := io.ReadAll(rootResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`data-panel="profiles"`,
		`data-panel="environments"`,
		`data-panel="setup"`,
		`data-panel="run"`,
		`data-panel="capabilities"`,
		`data-panel="network"`,
		`data-panel="audit"`,
		`Denied`,
		`Recent denied`,
		`direct exposes network identity`,
		`proxy hides origin path, not data egress`,
		`init/plan`,
		`init/apply`,
		`run/plan`,
		`run/apply`,
		`environment/`,
		`setupPayloadFromForm`,
		`runPayloadFromForm`,
		`environmentPayloadFromForm`,
		`data-environment-mode="plan"`,
		`environmentResult`,
		`splitArgv`,
		`panelRowLimit`,
		`visibleEnvironmentsForPanel`,
		`visibleSessionsForPanel`,
		`Showing newest`,
		`audit/events?limit=20`,
		`audit/events?decision=deny&limit=20`,
		`auditFilterForm`,
		`auditFilterFromForm`,
		`data-audit-action="filter"`,
		`auditExplorerResult`,
		`overview.environments`,
		`No reusable environments`,
		`Private operations console`,
		`host.open`,
		`allowUrls`,
		`urlScope`,
		`localNetworkPolicy`,
		`allowWorkspaceFiles`,
		`browserControl`,
		`history.replaceState`,
	} {
		if !strings.Contains(string(rootHTML), want) {
			t.Fatalf("UI HTML missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"ui_token",
		"localStorage",
		"sessionStorage",
	} {
		if strings.Contains(string(rootHTML), forbidden) {
			t.Fatalf("UI HTML should not contain %q", forbidden)
		}
	}

	postRootReq, err := http.NewRequest(http.MethodPost, server.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	postRootResp, err := http.DefaultClient.Do(postRootReq)
	if err != nil {
		t.Fatal(err)
	}
	defer postRootResp.Body.Close()
	if postRootResp.StatusCode != http.StatusMethodNotAllowed || postRootResp.Header.Get("Allow") != http.MethodGet {
		t.Fatalf("expected UI root to be GET-only, status=%d headers=%v", postRootResp.StatusCode, postRootResp.Header)
	}

	rootOriginReq, err := http.NewRequest(http.MethodGet, server.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	rootOriginReq.Header.Set("Origin", server.URL)
	rootOriginResp, err := http.DefaultClient.Do(rootOriginReq)
	if err != nil {
		t.Fatal(err)
	}
	defer rootOriginResp.Body.Close()
	if rootOriginResp.StatusCode != http.StatusOK {
		t.Fatalf("expected same-origin UI root request to succeed, got %d", rootOriginResp.StatusCode)
	}

	badOriginRootReq, err := http.NewRequest(http.MethodGet, server.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	badOriginRootReq.Header.Set("Origin", "https://example.com")
	badOriginRootResp, err := http.DefaultClient.Do(badOriginRootReq)
	if err != nil {
		t.Fatal(err)
	}
	defer badOriginRootResp.Body.Close()
	if badOriginRootResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected UI root bad Origin rejection, got %d", badOriginRootResp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, server.APIURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Hideout-UI-Token", "ui_token")
	req.Header.Set("Origin", server.URL)
	apiResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer apiResp.Body.Close()
	if apiResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected API status: %d", apiResp.StatusCode)
	}
	var decoded APIResponse
	if err := json.NewDecoder(apiResp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != APIVersion || decoded.Resource != "overview" || decoded.Data == nil {
		t.Fatalf("unexpected API response: %+v", decoded)
	}

	badHostReq, err := http.NewRequest(http.MethodGet, server.APIURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	badHostReq.Host = "evil.example"
	badHostReq.Header.Set("X-Hideout-UI-Token", "ui_token")
	badHostResp, err := http.DefaultClient.Do(badHostReq)
	if err != nil {
		t.Fatal(err)
	}
	defer badHostResp.Body.Close()
	if badHostResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected bad Host rejection, got %d", badHostResp.StatusCode)
	}

	badRootReq, err := http.NewRequest(http.MethodGet, server.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	badRootReq.Host = "evil.example"
	badRootResp, err := http.DefaultClient.Do(badRootReq)
	if err != nil {
		t.Fatal(err)
	}
	defer badRootResp.Body.Close()
	if badRootResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected root bad Host rejection, got %d", badRootResp.StatusCode)
	}
}

func TestStartLocalServerRejectsNonLoopbackBind(t *testing.T) {
	_, err := StartLocalServer(context.Background(), LocalServerOptions{
		Core: Core{Store: profile.Store{Root: t.TempDir()}},
		Addr: "0.0.0.0:0",
	})
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("expected loopback bind error, got %v", err)
	}
}

func TestStartLocalServerRunApplyUsesConfiguredBackend(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	fake := &applyRunFakeBackend{}
	openerCalled := false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := StartLocalServer(ctx, LocalServerOptions{
		Core:  Core{Store: store},
		Token: "ui_token",
		TTL:   time.Minute,
		RunBackend: func(req RunAPIRequest, plan RunPlan) (backend.Backend, error) {
			if req.Backend != "native" || plan.Backend != "native" {
				t.Fatalf("unexpected backend request=%q plan=%q", req.Backend, plan.Backend)
			}
			return fake, nil
		},
		RunOpener: func(req RunAPIRequest, plan RunPlan, runSession RunSession) broker.Opener {
			if req.ProfileName != "server-api" || plan.ProfileName != "server-api" || runSession.IdentityDir == "" {
				t.Fatalf("unexpected opener context: req=%+v plan=%+v session=%+v", req, plan, runSession)
			}
			openerCalled = true
			return broker.NoopOpener{}
		},
	})
	if err != nil {
		t.Fatalf("StartLocalServer: %v", err)
	}
	defer server.Close()
	body, err := json.Marshal(RunAPIRequest{
		ProfileName:        "server-api",
		Backend:            "native",
		Workspace:          t.TempDir(),
		Command:            []string{"tool"},
		AllowWeakIsolation: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/run/apply", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer ui_token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", server.URL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected run/apply status=%d body=%s", resp.StatusCode, responseBody)
	}
	var decoded APIResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Resource != "run/apply" || decoded.Data == nil {
		t.Fatalf("unexpected run/apply response: %+v", decoded)
	}
	if strings.Contains(string(responseBody), "cap_") || strings.Contains(string(responseBody), "broker.sock") {
		t.Fatalf("run/apply response leaked broker token or socket path: %s", responseBody)
	}
	if len(fake.calls) == 0 || fake.calls[0] != "available" {
		t.Fatalf("fake backend was not used: %v", fake.calls)
	}
	if !openerCalled {
		t.Fatal("local server did not pass run opener into API run/apply")
	}
}

func TestStartLocalServerTokenExpires(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Unix(100, 0)
	server, err := StartLocalServer(ctx, LocalServerOptions{
		Core: Core{
			Store: profile.Store{Root: t.TempDir()},
			Backends: []BackendCheck{
				{Name: "native", Isolation: "weak"},
			},
		},
		Token: "ui_token",
		TTL:   time.Nanosecond,
		Now:   func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("StartLocalServer: %v", err)
	}
	defer server.Close()
	now = time.Unix(101, 0)
	req, err := http.NewRequest(http.MethodGet, server.APIURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer ui_token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected expired token rejection, got %d", resp.StatusCode)
	}
}
