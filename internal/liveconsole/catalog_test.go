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
		for _, field := range requiredPayloadFields(ev.Kind) {
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
	representative := RepresentativeEventKinds()
	for panel, kinds := range PanelEventCoverage() {
		t.Run(panel, func(t *testing.T) {
			for _, kind := range kinds {
				if !representative[kind] {
					t.Fatalf("panel %s depends on event kind %s but no representative event covers it", panel, kind)
				}
			}
		})
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

func requiredPayloadFields(kind string) []string {
	switch kind {
	case KindEnvironment:
		return []string{"id"}
	case KindSession:
		return []string{"id"}
	case KindBackground:
		return []string{"id", "op", "status"}
	case KindAudit:
		return []string{"action", "decision"}
	case KindExport:
		return []string{"status"}
	case KindCleanup:
		return []string{"status"}
	case KindHostFSWrite:
		return []string{"decisionId", "operationId", "status"}
	case KindDecision:
		return []string{"decisionId", "recordKind", "status"}
	case KindNotice:
		return []string{"noticeId", "recordKind", "status"}
	case KindTerminal:
		return []string{"reason"}
	default:
		return nil
	}
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
	}
}
