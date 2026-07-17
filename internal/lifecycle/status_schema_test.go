package lifecycle

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/backend"
)

func TestLifecycleStatusSchemaAcceptsAllActivitiesAndRejectsUnknownFields(t *testing.T) {
	schema := compileLifecycleSchema(t, "../../schemas/lifecycle-status.schema.json")
	for _, activity := range []Activity{
		ActivityPinned, ActivityIdleGrace, ActivityIdleEligible, ActivityBlocked,
		ActivityStopping, ActivityStoppingUnknown, ActivityStopped, ActivityNotApplicable,
	} {
		status := Status{
			Schema: StatusSchema, EnvironmentID: "env_schema", StartGeneration: 1,
			BackendState: "running", BackendObservedAt: time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC),
			Activity: activity, Reconciliation: "complete",
			Pins: []ResourceSummary{{Kind: KindRunSession, ID: "ses_schema", State: StateActive}},
		}
		if activity == ActivityNotApplicable {
			status.BackendState = "not-applicable"
		}
		if err := validateLifecycleJSON(schema, status); err != nil {
			t.Fatalf("activity %s: %v", activity, err)
		}
	}
	invalid := map[string]any{
		"schema": StatusSchema, "environmentId": "env_schema", "backendState": "running", "backendObservedAt": "2026-07-16T05:00:00Z",
		"activity": string(ActivityPinned), "reconciliation": "complete", "rawToken": "secret",
	}
	if err := validateLifecycleJSON(schema, invalid); err == nil {
		t.Fatal("status schema accepted an unknown public field")
	}
}

func TestLifecycleJournalSchemaIsStrictAtNestedBoundaries(t *testing.T) {
	schema := compileLifecycleSchema(t, "../../schemas/lifecycle-journal.schema.json")
	now := time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)
	incarnation := EnvironmentRef{
		EnvironmentID: "env_schema", StartGeneration: 1, InstanceName: "hideout-schema",
		BootID: "11111111-2222-3333-4444-555555555555",
	}
	journal := Journal{
		Schema: JournalSchema, EnvironmentID: incarnation.EnvironmentID, StartGeneration: 1,
		Incarnation: &incarnation,
		Resources:   []Resource{newRootResource(incarnation, "daemon_0123456789abcdef01234567", now)},
		IdleDeadline: &IdleDeadline{
			Incarnation: incarnation, DaemonInstanceID: "daemon_0123456789abcdef01234567",
			ScheduledAt: now, Deadline: now.Add(15 * time.Second), Generation: 1,
		},
		Reconciliation: Reconciliation{DaemonInstanceID: "daemon_0123456789abcdef01234567", State: "complete", ObservedAt: now},
		UpdatedAt:      now,
	}
	if err := journal.Validate(); err != nil {
		t.Fatalf("Go validation: %v", err)
	}
	if err := validateLifecycleJSON(schema, journal); err != nil {
		t.Fatalf("schema validation: %v", err)
	}
	data, _ := json.Marshal(journal)
	var mutated map[string]any
	_ = json.Unmarshal(data, &mutated)
	resources := mutated["resources"].([]any)
	resources[0].(map[string]any)["rawHandle"] = "fd=9"
	if err := validateLifecycleJSON(schema, mutated); err == nil {
		t.Fatal("journal schema accepted an unknown nested resource field")
	}
}

func TestLifecycleSchemasUseTheProductionCatalogKinds(t *testing.T) {
	want := make([]string, 0, len(Catalog()))
	for _, descriptor := range Catalog() {
		want = append(want, string(descriptor.Kind))
	}
	slices.Sort(want)
	for _, name := range []string{"lifecycle-status.schema.json", "lifecycle-journal.schema.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "schemas", name))
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		defs := document["$defs"].(map[string]any)
		values := defs["resourceKind"].(map[string]any)["enum"].([]any)
		got := make([]string, 0, len(values))
		for _, value := range values {
			got = append(got, value.(string))
		}
		slices.Sort(got)
		expected := append([]string(nil), want...)
		if name == "lifecycle-status.schema.json" {
			for _, descriptor := range FactCatalog() {
				expected = append(expected, string(descriptor.Kind))
			}
			slices.Sort(expected)
		}
		if !slices.Equal(got, expected) {
			t.Fatalf("%s resource kinds drifted\n got: %v\nwant: %v", name, got, expected)
		}
	}
	factWant := make([]string, 0, len(FactCatalog()))
	for _, descriptor := range FactCatalog() {
		factWant = append(factWant, string(descriptor.Kind))
	}
	slices.Sort(factWant)
	data, err := os.ReadFile("../../schemas/lifecycle-journal.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	values := document["$defs"].(map[string]any)["factKind"].(map[string]any)["enum"].([]any)
	factGot := make([]string, 0, len(values))
	for _, value := range values {
		factGot = append(factGot, value.(string))
	}
	slices.Sort(factGot)
	if !slices.Equal(factGot, factWant) {
		t.Fatalf("lifecycle fact kinds drifted\n got: %v\nwant: %v", factGot, factWant)
	}
}

func TestBuildStatusRedactsControlMaterial(t *testing.T) {
	secret := "cap_0123456789abcdef0123456789abcdef"
	status := BuildStatus(secret, 1, backend.LifecycleObservation{
		State: backend.LifecycleUnknown, InstanceName: "hideout-test", ObservedAt: time.Now().UTC(), ReasonCode: secret,
	}, []Resource{{
		Ref: ResourceRef{Kind: KindRunSession, ID: secret, Generation: 1}, State: StateOrphaned,
		Persistence: PersistenceEphemeral, ClosePolicy: ClosePreStopDrain,
	}}, nil, nil, Reconciliation{State: "blocked", ReasonCode: secret}, "unknown")
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatalf("public lifecycle status leaked injected control material: %s", data)
	}
}

func TestBuildStatusDoesNotReportDrainAsIdleStopEligible(t *testing.T) {
	now := time.Date(2026, 7, 16, 13, 30, 0, 0, time.UTC)
	incarnation := EnvironmentRef{
		EnvironmentID: "env_status", StartGeneration: 1, InstanceName: "hideout-status",
		BootID: "11111111-2222-3333-4444-555555555555",
	}
	root := newRootResource(incarnation, "daemon_status", now)
	network := Resource{
		Ref:   ResourceRef{Kind: KindNetworkService, ID: "network_status", Generation: 1},
		Owner: OwnerRef{Kind: "manager", ID: "manager_status", Generation: 1},
		State: StateDraining,
		Dependencies: []DependencySpec{{
			Ref: root.Ref, StopMode: StopModeDrain,
		}},
		Persistence: PersistenceEphemeral, ClosePolicy: ClosePreStopDrain,
		UpdatedAt: now,
	}
	status := BuildStatus(incarnation.EnvironmentID, 1, backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: incarnation.InstanceName,
		BootID: incarnation.BootID, ObservedAt: now,
	}, []Resource{root, network}, nil, nil, Reconciliation{State: "complete"}, "")
	if status.Activity != ActivityPinned || len(status.Drains) != 1 {
		t.Fatalf("draining resource was reported as idle-stop-eligible: %+v", status)
	}
}

func TestBuildStatusKeepsStoppedBackendAndUnprovedCleanupOrthogonal(t *testing.T) {
	now := time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)
	status := BuildStatus("env-cleanup", 4, backend.LifecycleObservation{
		State:        backend.LifecycleStopped,
		InstanceName: "hideout-cleanup",
		ObservedAt:   now,
	}, []Resource{{
		Ref:   ResourceRef{Kind: KindRunSession, ID: "ses-cleanup", Generation: 4},
		Owner: OwnerRef{Kind: "daemon", ID: "daemon-cleanup", Generation: 4},
		State: StateOrphaned, Persistence: PersistenceEphemeral,
		ClosePolicy: CloseCoTerminateWithRoot, PossibleVMDependency: true,
		UpdatedAt: now,
	}}, nil, nil, Reconciliation{State: "blocked", ReasonCode: "cleanup-unproved"}, "committed")
	if status.BackendState != string(backend.LifecycleStopped) || status.Activity != ActivityBlocked || len(status.Orphans) != 1 {
		t.Fatalf("stopped backend hid unproved cleanup: %+v", status)
	}
	if status.ReasonCode != "cleanup-unproved" {
		t.Fatalf("stopped backend hid the cleanup recovery reason: %+v", status)
	}
}

func compileLifecycleSchema(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func validateLifecycleJSON(schema *jsonschema.Schema, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return schema.Validate(document)
}
