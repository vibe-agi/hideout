package workspaceattach

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/lifecycle"
)

func TestAttachmentSummaryMatchesSelectedPortalSchema(t *testing.T) {
	root := t.TempDir()
	canonical, identity, err := CaptureRootIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID, err := DeriveWorkspaceID(bytes.Repeat([]byte{0x35}, 32), canonical, identity)
	if err != nil {
		t.Fatal(err)
	}
	attachment := Attachment{
		ID: "att_0123456789abcdef", SessionID: "ses_fixture", EnvironmentID: "env_fixture",
		Incarnation: lifecycle.EnvironmentRef{
			EnvironmentID: "env_fixture", StartGeneration: 1, InstanceName: "hideout-fixture",
			BootID: "01234567-89ab-cdef-0123-456789abcdef",
		},
		WorkspaceID: workspaceID, CanonicalHostRoot: canonical, RootFileIdentity: identity,
		RootHandleIdentity: "root-handle-fixture", LogicalGuestRoot: LogicalWorkspaceRoot,
		PhysicalGuestRoot: PhysicalWorkspaceBase + "/" + workspaceID, Transport: SelectedTransport,
		ProviderRef:  lifecycle.ResourceRef{Kind: lifecycle.KindWorkspaceHostProvider, ID: "provider-fixture", Generation: 1},
		GuestViewRef: lifecycle.ResourceRef{Kind: lifecycle.KindWorkspaceGuestView, ID: "view-fixture", Generation: 1},
		State:        AttachmentReady, CreatedAt: time.Now().UTC(),
	}
	if err := attachment.Validate(); err != nil {
		t.Fatalf("attachment validation: %v", err)
	}
	encoded, err := json.Marshal(attachment.Summary())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{canonical, "root-handle-fixture", `"device"`, `"inode"`, "workspace.environment-service"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("public attachment leaked %q: %s", forbidden, encoded)
		}
	}
	schema := compileAttachmentSummarySchema(t)
	validateAttachmentSummaryJSON(t, schema, encoded, true)

	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{
		"environmentServiceRef": map[string]any{"kind": "workspace.environment-service", "id": "service", "generation": 1},
		"canonicalHostRoot":     canonical,
	} {
		mutated := cloneJSONDocument(t, document)
		mutated[name] = value
		data, _ := json.Marshal(mutated)
		validateAttachmentSummaryJSON(t, schema, data, false)
	}
	mutated := cloneJSONDocument(t, document)
	mutated["transport"] = "vz-live-multiple-share"
	data, _ := json.Marshal(mutated)
	validateAttachmentSummaryJSON(t, schema, data, false)
}

func TestAttachmentTerminalCleanupProofIsTyped(t *testing.T) {
	proof := CleanupProof{Status: CleanupUnproved, ObservedAt: time.Now().UTC(), ReasonCode: "workspace.cleanup.unproved"}
	if err := proof.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []CleanupProof{
		{},
		{Status: CleanupAbsent, ObservedAt: time.Now().UTC(), ReasonCode: "unexpected"},
		{Status: CleanupUnproved, ObservedAt: time.Now().UTC()},
		{Status: "complete", ObservedAt: time.Now().UTC()},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("invalid cleanup proof accepted: %+v", invalid)
		}
	}
}

func compileAttachmentSummarySchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "schemas", "workspace-attachment.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("attachment.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("attachment.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateAttachmentSummaryJSON(t *testing.T, schema *jsonschema.Schema, data []byte, wantValid bool) {
	t.Helper()
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	err = schema.Validate(document)
	if wantValid && err != nil {
		t.Fatalf("attachment summary schema: %v\n%s", err, data)
	}
	if !wantValid && err == nil {
		t.Fatalf("attachment summary schema accepted invalid document: %s", data)
	}
}

func cloneJSONDocument(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}
