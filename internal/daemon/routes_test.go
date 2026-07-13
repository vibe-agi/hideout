package daemon

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/manager"
)

func TestDaemonEndpointInventoryRecognizesEveryEndpoint(t *testing.T) {
	seen := map[string]bool{}
	for _, endpoint := range DaemonEndpoints() {
		if endpoint.Class != RouteClassDaemonSpecific {
			t.Fatalf("%s %s class=%q", endpoint.Method, endpoint.Path, endpoint.Class)
		}
		if strings.HasPrefix(endpoint.Path, "/api/v1/") {
			t.Fatalf("%s %s must not be a Manager route", endpoint.Method, endpoint.Path)
		}
		key := endpoint.Method + " " + endpoint.Path
		if seen[key] {
			t.Fatalf("duplicate daemon endpoint %s", key)
		}
		seen[key] = true
		got, ok := RecognizeDaemonEndpoint(endpoint.Method, endpoint.Path)
		if !ok {
			t.Fatalf("daemon endpoint %s is not recognized", key)
		}
		if got.Path != endpoint.Path || got.Method != endpoint.Method {
			t.Fatalf("recognized %+v, want %+v", got, endpoint)
		}
		if _, ok := manager.RecognizeManagerRoute(endpoint.Method, endpoint.Path); ok {
			t.Fatalf("daemon endpoint %s recognized as Manager route", key)
		}
	}
}

func TestDaemonEndpointRecognizerRejectsUnknownAndWrongMethod(t *testing.T) {
	if _, ok := RecognizeDaemonEndpoint(http.MethodGet, "/daemon/does-not-exist"); ok {
		t.Fatal("unknown daemon endpoint recognized")
	}
	if _, ok := RecognizeDaemonEndpoint(http.MethodGet, backgroundPath); ok {
		t.Fatal("wrong method recognized for background endpoint")
	}
}

func TestDaemonEndpointInventoryIncludesLoopbackUIRoot(t *testing.T) {
	endpoint, ok := RecognizeDaemonEndpoint(http.MethodGet, loopbackUIPath)
	if !ok || endpoint.Owner != "internal/daemon.startLoopbackUI" {
		t.Fatalf("loopback UI root missing from inventory: %+v ok=%t", endpoint, ok)
	}
}

func TestRuntimeRoutesRemainManagerOwnedUnderDaemonTransport(t *testing.T) {
	for _, path := range []string{
		"/api/v1/runtime/catalog",
		"/api/v1/runtime/status",
		"/api/v1/runtime/verify/plan",
		"/api/v1/runtime/verify/apply",
	} {
		method := http.MethodGet
		if strings.Contains(path, "/verify/") {
			method = http.MethodPost
		}
		if _, ok := manager.RecognizeManagerRoute(method, path); !ok {
			t.Fatalf("runtime Manager route is not recognized: %s %s", method, path)
		}
		if _, ok := RecognizeDaemonEndpoint(method, path); ok {
			t.Fatalf("runtime Manager route duplicated as daemon-specific endpoint: %s %s", method, path)
		}
	}
}
