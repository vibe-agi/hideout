package liveconsole

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestEventCatalogRepresentativeEventsValidate(t *testing.T) {
	schema := compileSchema(t, "../../schemas/daemon-event.schema.json")
	for _, ev := range RepresentativeEvents() {
		t.Run(ev.Kind, func(t *testing.T) {
			if err := ValidateEvent(ev); err != nil {
				t.Fatalf("ValidateEvent(%s): %v", ev.Kind, err)
			}
			if err := validateJSON(schema, ev); err != nil {
				t.Fatalf("schema validation for %s: %v", ev.Kind, err)
			}
		})
	}
}

func TestEventCatalogRejectsMissingRequiredFields(t *testing.T) {
	schema := compileSchema(t, "../../schemas/daemon-event.schema.json")
	for _, ev := range RepresentativeEvents() {
		for _, field := range RequiredPayloadFields(ev.Kind) {
			t.Run(ev.Kind+"."+field, func(t *testing.T) {
				mutated := ev
				clearPayloadField(&mutated.Payload, field)
				if err := ValidateEvent(mutated); err == nil {
					t.Fatalf("%s event without %s should fail Go validation: %+v", ev.Kind, field, mutated)
				}
				if err := validateJSON(schema, mutated); err == nil {
					t.Fatalf("%s event without %s should fail schema validation: %+v", ev.Kind, field, mutated)
				}
			})
		}
	}
}

func TestEventCatalogCoversEveryLivePanel(t *testing.T) {
	cataloged := catalogKinds()
	for panel, kinds := range PanelEventCoverage() {
		t.Run(panel, func(t *testing.T) {
			for _, kind := range kinds {
				entry, ok := cataloged[kind]
				if !ok {
					t.Fatalf("panel %s depends on event kind %s but no catalog entry covers it", panel, kind)
				}
				if entry.Source == "" {
					t.Fatalf("panel %s depends on event kind %s without source classification", panel, kind)
				}
			}
		})
	}
}

func TestEventCatalogRowsAreStructuredAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, entry := range EventCatalog() {
		if entry.Kind == "" {
			t.Fatal("catalog entry missing kind")
		}
		if seen[entry.Kind] {
			t.Fatalf("duplicate catalog entry for %s", entry.Kind)
		}
		seen[entry.Kind] = true
		if entry.Source == "" {
			t.Fatalf("%s missing source classification", entry.Kind)
		}
		if entry.Source == EventSourceProduction && entry.ProductionSite == "" {
			t.Fatalf("%s production row missing source", entry.Kind)
		}
		if entry.Redaction != RedactionControlPlaneStripped {
			t.Fatalf("%s redaction=%q", entry.Kind, entry.Redaction)
		}
		if !entry.GoReducer {
			t.Fatalf("%s missing Go reducer coverage", entry.Kind)
		}
		if !entry.JSReducer {
			t.Fatalf("%s missing JS reducer coverage", entry.Kind)
		}
		if len(entry.RequiredFields) == 0 {
			t.Fatalf("%s missing required field declaration", entry.Kind)
		}
	}
}

func TestEventCatalogCoversReducerBranches(t *testing.T) {
	cataloged := catalogKinds()
	for _, kind := range ReducerEventKinds() {
		if _, ok := cataloged[kind]; !ok {
			t.Fatalf("reducer branch %s has no catalog row", kind)
		}
	}
}

func TestEventCatalogProducerMappingsAreExplicit(t *testing.T) {
	mappings := EventProducerMappings()
	for _, producer := range []string{
		KindEnvironment, KindSession, KindBackground, KindAudit, KindExport,
		KindWorkspaceView, KindCleanup, KindHostFSWrite, KindDecision, KindNotice, KindLifecycle, KindTerminal,
		"host-app", "run", "operation", "*",
	} {
		if mappings[producer] == "" {
			t.Fatalf("producer/remap %s is not cataloged", producer)
		}
	}
	if mappings["run"] != KindSession || mappings["operation"] != KindSession || mappings["*"] != KindSession {
		t.Fatalf("session remaps not cataloged correctly: %+v", mappings)
	}
	if mappings["host-app"] != KindAudit {
		t.Fatalf("host-app lifecycle producer must map to audit, got %+v", mappings)
	}
}

func compileSchema(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	for _, dependency := range []string{"../../schemas/lifecycle-status.schema.json"} {
		dependencyData, readErr := os.ReadFile(filepath.Clean(dependency))
		if readErr != nil {
			t.Fatalf("read dependency schema: %v", readErr)
		}
		dependencyDoc, parseErr := jsonschema.UnmarshalJSON(bytes.NewReader(dependencyData))
		if parseErr != nil {
			t.Fatalf("parse dependency schema: %v", parseErr)
		}
		if addErr := compiler.AddResource("https://hideout.local/schemas/"+filepath.Base(dependency), dependencyDoc); addErr != nil {
			t.Fatalf("add dependency schema: %v", addErr)
		}
	}
	if err := compiler.AddResource("schema.json", doc); err != nil {
		t.Fatalf("add schema: %v", err)
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return schema
}

func validateJSON(schema *jsonschema.Schema, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return schema.Validate(doc)
}

func clearPayloadField(payload *EventPayload, field string) {
	switch field {
	case "id":
		payload.ID = ""
	case "op":
		payload.Op = ""
	case "status":
		payload.Status = ""
	case "action":
		payload.Action = ""
	case "decision":
		payload.Decision = ""
	case "reason":
		payload.Reason = ""
	case "decisionId":
		payload.DecisionID = ""
	case "operationId":
		payload.OperationID = ""
	case "recordKind":
		payload.RecordKind = ""
	case "noticeId":
		payload.NoticeID = ""
	case "attachmentId":
		payload.AttachmentID = ""
	case "session":
		payload.Session = ""
	case "environmentId":
		payload.EnvironmentID = ""
	case "workspaceId":
		payload.WorkspaceID = ""
	case "workspaceLabel":
		payload.WorkspaceLabel = ""
	case "guestWorkspace":
		payload.GuestWorkspace = ""
	case "workspaceTransport":
		payload.WorkspaceTransport = ""
	case "workspaceViewState":
		payload.WorkspaceViewState = ""
	case "lifecycle":
		payload.Lifecycle = nil
	}
}

func catalogKinds() map[string]EventCatalogEntry {
	out := map[string]EventCatalogEntry{}
	for _, entry := range EventCatalog() {
		out[entry.Kind] = entry
	}
	return out
}
