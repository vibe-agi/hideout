package manager

import (
	"encoding/json"
	"strings"
	"testing"

	netpolicy "github.com/vibe-agi/hideout/internal/network"
)

func TestGatewayObservationDetailsAreRedactedAndStable(t *testing.T) {
	details := gatewayObservationDetails(netpolicy.GatewayObservation{
		Accepted: 2, Authenticated: 1, AuthenticationFailed: 1,
		RequestParsed: 1, RequestRejected: 1, RouteMissing: 1,
		UpstreamDialStarted: 1, UpstreamDialFailed: 1, UpstreamConnected: 1,
	}, true)
	encoded, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, required := range []string{
		`"scope":"environment-window"`,
		`"available":true`,
		`"accepted":2`,
		`"authenticationFailed":1`,
		`"upstreamConnected":1`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("gateway observation missing %s: %s", required, text)
		}
	}
	for _, forbidden := range []string{
		"address", "credential", "destination", "environmentId",
		"error", "fingerprint", "password", "proxy", "target", "url", "username",
	} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Fatalf("gateway observation contains forbidden field %q: %s", forbidden, text)
		}
	}
}

func TestRunNetworkGatewayObservationUsesBaseline(t *testing.T) {
	registry := netpolicy.NewGatewayRegistry()
	defer registry.Close()
	_, change, err := registry.Stage("env_observation", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := change.Rollback(); err != nil {
			t.Errorf("roll back gateway stage: %v", err)
		}
	}()
	baseline, ok := registry.Observation("env_observation")
	if !ok {
		t.Fatal("gateway baseline is unavailable")
	}
	runNetwork := RunNetwork{
		Gateway:                    registry,
		gatewayEnvironmentID:       "env_observation",
		gatewayObservationBaseline: baseline,
		gatewayObservationReady:    true,
	}
	got, ok := runNetwork.gatewayObservation()
	if !ok || got != (netpolicy.GatewayObservation{}) {
		t.Fatalf("gateway delta=%+v available=%t", got, ok)
	}
}
