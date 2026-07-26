package manager

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/broker"
)

func TestBrokerTransportObservationDetailsAreRedactedAndStable(t *testing.T) {
	details := brokerTransportObservationDetails(broker.TransportObservation{
		Accepted: 2, RejectedAfterClose: 1, RequestParsed: 1,
		RequestParseFailed: 1, ResponseWritten: 1, ResponseWriteFailed: 1,
	})
	encoded, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, required := range []string{
		`"scope":"session-window"`,
		`"accepted":2`,
		`"requestParseFailed":1`,
		`"responseWriteFailed":1`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("broker transport observation missing %s: %s", required, text)
		}
	}
	for _, forbidden := range []string{
		"address", "credential", "destination", "environmentId",
		"error", "fingerprint", "password", "proxy", "target", "token", "url", "username",
	} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Fatalf("broker transport observation contains forbidden field %q: %s", forbidden, text)
		}
	}
}
