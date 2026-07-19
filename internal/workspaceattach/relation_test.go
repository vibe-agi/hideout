package workspaceattach

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/lifecycle"
)

func TestClassifyRootsPreservesNestedDirection(t *testing.T) {
	ancestor := t.TempDir()
	descendant := filepath.Join(ancestor, "project")
	if err := os.Mkdir(descendant, 0o700); err != nil {
		t.Fatal(err)
	}
	disjoint := t.TempDir()

	tests := []struct {
		name     string
		first    string
		second   string
		relation RootRelation
		nesting  RootNesting
	}{
		{name: "same", first: ancestor, second: ancestor, relation: RootSame, nesting: RootNestingNone},
		{name: "ancestor first", first: ancestor, second: descendant, relation: RootNested, nesting: RootFirstContainsSecond},
		{name: "descendant first", first: descendant, second: ancestor, relation: RootNested, nesting: RootFirstContainedBySecond},
		{name: "disjoint", first: ancestor, second: disjoint, relation: RootDisjoint, nesting: RootNestingNone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ClassifyRoots(test.first, test.second)
			if err != nil {
				t.Fatal(err)
			}
			if got.Relation != test.relation || got.Nesting != test.nesting {
				t.Fatalf("relation=%+v", got)
			}
		})
	}
}

func TestRootRelationNoticeIsDirectionalAndNonAuthoritative(t *testing.T) {
	ancestor := relationAttachment(t, t.TempDir(), "a", "ses_relation_a")
	descendantPath := filepath.Join(ancestor.CanonicalHostRoot, "child")
	if err := os.Mkdir(descendantPath, 0o700); err != nil {
		t.Fatal(err)
	}
	descendant := relationAttachment(t, descendantPath, "b", "ses_relation_b")

	ancestorNotice, err := BuildRootRelationNotice(ancestor, descendant)
	if err != nil {
		t.Fatal(err)
	}
	descendantNotice, err := BuildRootRelationNotice(descendant, ancestor)
	if err != nil {
		t.Fatal(err)
	}
	if ancestorNotice.Relation != RootNested || ancestorNotice.SelectedPosition != RelationPositionAncestor ||
		descendantNotice.SelectedPosition != RelationPositionDescendant {
		t.Fatalf("directional notices: ancestor=%+v descendant=%+v", ancestorNotice, descendantNotice)
	}
	encoded, err := json.Marshal([]RootRelationNotice{ancestorNotice, descendantNotice})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{ancestor.CanonicalHostRoot, descendant.CanonicalHostRoot, ancestor.RootHandleIdentity, descendant.RootHandleIdentity} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("relation notice leaked authority %q: %s", forbidden, encoded)
		}
	}
	if ancestor.ProviderRef == descendant.ProviderRef || ancestor.WorkspaceID == descendant.WorkspaceID {
		t.Fatal("nested relation widened or deduplicated attachment authority")
	}
}

func relationAttachment(t *testing.T, root, marker, sessionID string) Attachment {
	t.Helper()
	canonical, identity, err := CaptureRootIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := "wrk_" + strings.Repeat(marker, 64)
	return Attachment{
		ID: "att_" + strings.Repeat(marker, 32), SessionID: sessionID, EnvironmentID: "env_relation",
		Incarnation: lifecycle.EnvironmentRef{
			EnvironmentID: "env_relation", StartGeneration: 1, InstanceName: "hideout-relation",
			BootID: "01234567-89ab-cdef-0123-456789abcdef",
		},
		WorkspaceID: workspaceID, CanonicalHostRoot: canonical, RootFileIdentity: identity,
		RootHandleIdentity: "root-handle-" + marker, LogicalGuestRoot: LogicalWorkspaceRoot,
		PhysicalGuestRoot: PhysicalWorkspaceBase + "/" + workspaceID, Transport: SelectedTransport,
		ProviderRef:  lifecycle.ResourceRef{Kind: lifecycle.KindWorkspaceHostProvider, ID: "provider-relation-" + marker, Generation: 1},
		GuestViewRef: lifecycle.ResourceRef{Kind: lifecycle.KindWorkspaceGuestView, ID: "view-relation-" + marker, Generation: 1},
		State:        AttachmentPlanned, CreatedAt: time.Now().UTC(),
	}
}
