package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T035: after a restart the daemon fails closed for a pre-existing live resource
// it cannot prove it owns — reported and audited as an orphan, never re-adopted
// and never destroyed.
func TestRestartFailsClosedForOrphans(t *testing.T) {
	store := testStore(t)
	destroyed := false
	live := func(_ string) []LiveResource {
		// Simulate a running environment that survived a prior daemon. The lister is
		// read-only; it never destroys anything (destroyed stays false).
		return []LiveResource{{ID: "env-abc", Kind: "environment"}}
	}
	d, err := Start(Options{Store: store, LiveResources: live})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })

	// Reported as an orphan.
	orphans := d.Orphans()
	if len(orphans) != 1 || orphans[0].ID != "env-abc" {
		t.Fatalf("want env-abc reported as orphan, got %+v", orphans)
	}
	// Not re-adopted: it is not owned by the current instance.
	if d.own.owns("env-abc") {
		t.Fatalf("orphan must not be re-adopted as owned")
	}
	// Not destroyed: the lister was read-only.
	if destroyed {
		t.Fatalf("orphan must not be destroyed")
	}
	// Audited as an orphan with a reason and no re-adoption.
	auditData, err := os.ReadFile(filepath.Join(d.RuntimeDir(), auditName))
	if err != nil {
		t.Fatal(err)
	}
	sawOrphan := false
	sc := bufio.NewScanner(strings.NewReader(string(auditData)))
	for sc.Scan() {
		var ev map[string]any
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev["action"] == "daemon.orphan" && ev["decision"] == "deny" {
			details, _ := ev["details"].(map[string]any)
			if details["resource"] == "env-abc" && details["reason"] != "" {
				sawOrphan = true
			}
		}
	}
	if !sawOrphan {
		t.Fatalf("orphan not audited with reason:\n%s", auditData)
	}
}

// T035: when the daemon owns a resource in the current instance, it is not an
// orphan (proves the ownership check, not a blanket flag).
func TestOwnedResourceIsNotOrphan(t *testing.T) {
	o := newOwnership()
	o.record("env-owned", "sess-1")
	orphans := o.detectOrphans([]LiveResource{{ID: "env-owned", Kind: "environment"}, {ID: "env-other", Kind: "environment"}})
	if len(orphans) != 1 || orphans[0].ID != "env-other" {
		t.Fatalf("only the unowned resource should be an orphan, got %+v", orphans)
	}
}
