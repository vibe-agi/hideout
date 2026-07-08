package cmdadapter

import (
	"fmt"
	"strings"
	"testing"
)

func TestEvidenceRedactsControlPlaneMaterial(t *testing.T) {
	secret := "cap_0123456789abcdef0123456789abcdef"
	out := Outcome{
		Outcome: OutcomeSimulate,
		Reason:  "contains " + secret,
		Stdout:  "HIDEOUT_SECRET_PROXY=socks5://user:pass@127.0.0.1:9999",
		Audit: map[string]any{
			"machineId": "0123456789abcdef0123456789abcdef",
		},
	}
	details := Evidence(Invocation{
		Command: "tool-x",
		Argv:    []string{"tool-x", secret},
		Adapter: RuntimeAdapter{ID: "adapter", Digest: "sha256:abc"},
	}, out)
	encoded := fmt.Sprintf("%v", details)
	for _, forbidden := range []string{secret, "HIDEOUT_SECRET_PROXY", "0123456789abcdef0123456789abcdef"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("evidence leaked %q: %s", forbidden, encoded)
		}
	}
}
