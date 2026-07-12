package hostapppack

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEvidenceUsesStableLifecycleActionsAndValidatedFactsOnly(t *testing.T) {
	for _, tc := range []struct {
		operation string
		action    string
	}{
		{operation: "add", action: "host.app.install"},
		{operation: "add-install-only", action: "host.app.install"},
		{operation: "add-install-only-failed", action: "host.app.install"},
		{operation: "validate", action: "host.app.validate"},
		{operation: "test", action: "host.app.test"},
		{operation: "enable", action: "host.app.enable"},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			event := Evidence(tc.operation, "allow", "privacy", "community.editor", "rev_0123", "sha256:source", "sha256:permission", "sha256:identity", "")
			if event.Action != tc.action || event.Profile != "privacy" || event.Decision != "allow" {
				t.Fatalf("event=%+v", event)
			}
			for _, key := range []string{"packId", "revisionId", "sourceDigest", "permissionFingerprint", "observedIdentityDigest"} {
				if event.Details[key] == nil {
					t.Fatalf("validated evidence fact %q is absent: %+v", key, event.Details)
				}
			}
			for _, forbidden := range []string{"sourcepath", "sourceurl", "description", "installhint", "argv", "hostpath"} {
				for key := range event.Details {
					if strings.EqualFold(key, forbidden) {
						t.Fatalf("untrusted or path-bearing field %q escaped evidence: %+v", forbidden, event)
					}
				}
			}
		})
	}
}

func TestOpenResourceEvidenceKeepsHostFSClassAndSafeRelativeTargetOnly(t *testing.T) {
	details := OpenResourceEvidence(ResourceHostFSPortal, "report.txt")
	if details["resourceClass"] != ResourceHostFSPortal || details["relativeTarget"] != "report.txt" || details["workspaceWritable"] != nil {
		t.Fatalf("HostFS evidence=%+v", details)
	}
	raw, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"/Users/", "/hideout/hostfs", "portalRef", "providerToken", "cap_", "claim_"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("HostFS evidence leaked %q: %s", forbidden, raw)
		}
	}

	workspace := OpenResourceEvidence(ResourceWorkspace, "src/main.go")
	if workspace["resourceClass"] != ResourceWorkspace || workspace["relativeTarget"] != "src/main.go" || workspace["workspaceWritable"] != true {
		t.Fatalf("workspace evidence=%+v", workspace)
	}

	for name, target := range map[string]string{
		"absolute-host-path":  "/Users/example/private.txt",
		"portal-spelling":     "hideout/hostfs/Users/example/private.txt",
		"lower-relative-path": "Users/example/private.txt",
		"parent-escape":       "../private.txt",
		"provider-token":      "nested/cap_0123456789abcdef",
		"claim-token":         "nested/claim_0123456789abcdef",
	} {
		t.Run(name, func(t *testing.T) {
			got := OpenResourceEvidence(ResourceHostFSPortal, target)
			if got["resourceClass"] != ResourceHostFSPortal {
				t.Fatalf("resource class was lost: %+v", got)
			}
			if _, ok := got["relativeTarget"]; ok {
				t.Fatalf("unsafe relative target escaped: %+v", got)
			}
		})
	}
}

func TestLifecycleEvidenceNeverPersistsInjectedFailureProse(t *testing.T) {
	reason := "argv=[editor /Users/alice/private.go] cap_0123456789abcdef0123456789abcdef " +
		"claim_0123456789abcdef0123456789abcdef https://user:secret@example.test/repo.git"
	event := Evidence("update-failed", "deny", "privacy", "community.editor", "rev_123",
		"sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64),
		"sha256:"+strings.Repeat("c", 64), reason)
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"/Users/alice", "private.go", "cap_012345", "claim_012345", "user:secret", "argv=["} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("lifecycle evidence leaked %q: %s", leak, raw)
		}
	}
	if event.Details["reason"] != "host-app lifecycle operation failed" {
		t.Fatalf("lifecycle evidence did not use a stable typed summary: %+v", event.Details)
	}
}
