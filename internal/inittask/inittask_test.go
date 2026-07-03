package inittask

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestPlanMachineLimaHelpersRequireStoreManifest(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HIDEOUT_LINUX_SHIM_PATH", "")
	t.Setenv("HIDEOUT_LINUX_HOSTFSD_PATH", "")
	shim := helperbin.DefaultLinuxShimPath(store.Root, runtime.GOARCH)
	hostfsd := helperbin.DefaultLinuxHostFSDPath(store.Root, runtime.GOARCH)
	for _, path := range []string{shim, hostfsd} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(filepath.Base(path)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := PlanMachine(store, Options{ProfileName: "default", Backend: "lima", Network: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range helperTasks(t, plan) {
		if task.Status != "pending" {
			t.Fatalf("%s status=%q want pending", task.Kind, task.Status)
		}
		if len(task.Outputs) != 2 || task.Outputs[1] != helperbin.ManifestPath(task.Outputs[0]) {
			t.Fatalf("%s outputs=%v should include helper manifest", task.Kind, task.Outputs)
		}
	}
	if err := helperbin.WriteStoreHelperManifest(shim, "hideout-shim", runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	if err := helperbin.WriteStoreHelperManifest(hostfsd, "hideout-hostfsd", runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
	plan, err = PlanMachine(store, Options{ProfileName: "default", Backend: "lima", Network: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range helperTasks(t, plan) {
		if task.Status != "ok" {
			t.Fatalf("%s status=%q want ok", task.Kind, task.Status)
		}
	}
}

func TestApplyMachineWritesTaskAudit(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	plan, err := PlanMachine(store, Options{ProfileName: "default", Backend: "native", Network: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyMachine(store, plan, ApplyOptions{NoInput: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.AuditPath != DefaultAuditPath(store.Root) {
		t.Fatalf("AuditPath=%q want %q", result.AuditPath, DefaultAuditPath(store.Root))
	}
	validateInitAuditJSONLWithSchema(t, result.AuditPath)
	events := readInitAuditEvents(t, result.AuditPath)
	if len(events) == 0 {
		t.Fatalf("init audit should contain task events")
	}
	seenApplied := false
	seenSkipped := false
	for _, event := range events {
		if event.Version != AuditVersion ||
			event.Operation != OperationInitApply ||
			event.Profile != "default" ||
			event.Backend != "native" ||
			event.Network != "direct" ||
			event.TaskKind == "" ||
			event.Source != "builtin" {
			t.Fatalf("unexpected audit event: %+v", event)
		}
		switch event.Result {
		case "applied":
			seenApplied = true
			if event.Decision != "allow" {
				t.Fatalf("applied event decision=%q want allow: %+v", event.Decision, event)
			}
		case "skipped":
			seenSkipped = true
			if event.Decision != "audit-only" {
				t.Fatalf("skipped event decision=%q want audit-only: %+v", event.Decision, event)
			}
		default:
			t.Fatalf("unexpected audit result %q: %+v", event.Result, event)
		}
	}
	if !seenApplied || !seenSkipped {
		t.Fatalf("audit missing applied/skipped events: %+v", events)
	}
	summary := Summary(store, "default")
	if summary.AuditPath != result.AuditPath || summary.AuditEvents != len(events) {
		t.Fatalf("summary audit mismatch: %+v events=%d", summary, len(events))
	}
}

func TestApplyMachineDryRunDoesNotWriteTaskAudit(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	plan, err := PlanMachine(store, Options{ProfileName: "default", Backend: "native", Network: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyMachine(store, plan, ApplyOptions{NoInput: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.AuditPath != "" {
		t.Fatalf("dry run AuditPath=%q want empty", result.AuditPath)
	}
	if _, err := os.Stat(DefaultAuditPath(store.Root)); !os.IsNotExist(err) {
		t.Fatalf("dry run should not write audit, stat err=%v", err)
	}
}

func TestPlanAndApplyStaleInstallStateRewritesCurrentMetadata(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	statePath := filepath.Join(store.Root, StateFile)
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte(`{"version":"draft","profileSchema":"draft"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanMachine(store, Options{ProfileName: "default", Backend: "native", Network: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	stateTask := schemaMetadataTask(t, plan)
	if stateTask.Status != "pending" ||
		stateTask.Risk != "safe" ||
		stateTask.Message != "rewrite install state metadata for the current schema" {
		t.Fatalf("state metadata repair task mismatch: %+v", stateTask)
	}
	result, err := ApplyMachine(store, plan, ApplyOptions{NoInput: true})
	if err != nil {
		t.Fatal(err)
	}
	if !stateCurrent(statePath) {
		data, _ := os.ReadFile(statePath)
		t.Fatalf("rewritten state is not current: %s", data)
	}
	events := readInitAuditEvents(t, result.AuditPath)
	found := false
	for _, event := range events {
		if event.TaskKind == "schema.metadata.write" && event.Result == "applied" && event.Decision == "allow" {
			found = true
		}
	}
	if !found {
		t.Fatalf("state metadata repair audit event missing: %+v", events)
	}
}

func TestInvalidInstallStatePlansCurrentMetadataRewrite(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	statePath := filepath.Join(store.Root, StateFile)
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanMachine(store, Options{ProfileName: "default", Backend: "native", Network: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	task := schemaMetadataTask(t, plan)
	if task.Status != "pending" || task.Message != "rewrite install state metadata for the current schema" {
		t.Fatalf("invalid install state should plan current metadata rewrite: %+v", task)
	}
}

func helperTasks(t *testing.T, plan Plan) []Task {
	t.Helper()
	var tasks []Task
	for _, task := range plan.Tasks {
		switch task.Kind {
		case "helper.install.linux-shim", "helper.install.linux-hostfsd":
			tasks = append(tasks, task)
		}
	}
	if len(tasks) != 2 {
		t.Fatalf("expected two helper tasks, got %d in %+v", len(tasks), plan.Tasks)
	}
	return tasks
}

func schemaMetadataTask(t *testing.T, plan Plan) Task {
	t.Helper()
	for _, task := range plan.Tasks {
		if task.Kind == "schema.metadata.write" {
			return task
		}
	}
	t.Fatalf("schema metadata task missing in %+v", plan.Tasks)
	return Task{}
}

func validateInitAuditJSONLWithSchema(t *testing.T, path string) {
	t.Helper()
	schema := compileInitAuditSchema(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(line)))
		if err != nil {
			t.Fatalf("decode init audit event: %v\n%s", err, line)
		}
		if err := schema.Validate(doc); err != nil {
			t.Fatalf("init audit event does not match schema: %v\n%s", err, line)
		}
	}
}

func compileInitAuditSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "init-audit-event.schema.json"))
	if err != nil {
		t.Fatalf("read init audit schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode init audit schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("init-audit-event.schema.json", doc); err != nil {
		t.Fatalf("add init audit schema: %v", err)
	}
	schema, err := compiler.Compile("init-audit-event.schema.json")
	if err != nil {
		t.Fatalf("compile init audit schema: %v", err)
	}
	return schema
}

func readInitAuditEvents(t *testing.T, path string) []AuditEvent {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var events []AuditEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var event AuditEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode audit event: %v\n%s", err, line)
		}
		events = append(events, event)
	}
	return events
}
