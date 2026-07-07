package inittask

import (
	"bytes"
	"encoding/json"
	"errors"
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

func TestPlanMachineAutoBackendMatchesRunDefaultLima(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HIDEOUT_LINUX_SHIM_PATH", "")
	t.Setenv("HIDEOUT_LINUX_HOSTFSD_PATH", "")
	plan, err := PlanMachine(store, Options{ProfileName: "default", Backend: "auto", Network: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Backend != "lima" {
		t.Fatalf("auto init backend=%q want lima", plan.Backend)
	}
	helperTasks(t, plan)
}

func TestPlanMachineNativeBackendDoesNotPlanLimaHelpers(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	plan, err := PlanMachine(store, Options{ProfileName: "default", Backend: "native", Network: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Backend != "native" {
		t.Fatalf("native init backend=%q want native", plan.Backend)
	}
	for _, task := range plan.Tasks {
		switch task.Kind {
		case "helper.install.linux-shim", "helper.install.linux-hostfsd":
			t.Fatalf("native init must not plan Lima helper task: %+v", task)
		}
	}
	assertNextStepCommand(t, plan.NextSteps, "doctor-check", "hideout doctor --profile default --backend native")
	assertNextStepCommand(t, plan.NextSteps, "smoke-run", "hideout run --profile default --backend native --allow-weak-isolation -- pwd")
	validateInitPlanWithSchema(t, plan)
}

func TestPlanMachineRejectsLegacyToolSupply(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	_, err := PlanMachine(store, Options{
		ProfileName: "agent",
		Backend:     "native",
		Network:     "direct",
		NPMGlobals: []profile.NPMGlobalPackage{{
			Package:  "@example/agent-cli@1.2.3",
			Commands: []string{"agent-cli", "agent-helper"},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "legacy tool-supply") {
		t.Fatalf("expected legacy tool-supply failure, got %v", err)
	}
}

func TestPlanMachineNextStepsBlockRunSuggestionsWhenBlocked(t *testing.T) {
	plan := Plan{
		Version:   Version,
		StoreRoot: t.TempDir(),
		Profile:   "default",
		Backend:   "lima",
		Network:   "direct",
		Tasks: []Task{{
			ID:                 "install_lima_linux_shim",
			Kind:               "helper.install.linux-shim",
			Source:             "builtin",
			Status:             "blocked",
			TargetScope:        "machine",
			CapabilityBoundary: "backend",
			Risk:               "safe",
			RequiresConfirm:    false,
			Message:            "linux helper source is unavailable",
		}},
	}
	plan.NextSteps = initNextSteps(plan)
	assertNextStepCommand(t, plan.NextSteps, "resolve-blocked", "hideout doctor --fix --profile default --backend lima")
	for _, forbidden := range []string{"doctor-check", "smoke-run", "cli-smoke"} {
		if nextStepByID(plan.NextSteps, forbidden) != nil {
			t.Fatalf("blocked init plan must not include %s: %+v", forbidden, plan.NextSteps)
		}
	}
	validateInitPlanWithSchema(t, plan)
}

func TestFindHelperSourceRootUsesExplicitEnv(t *testing.T) {
	source := fakeSourceRoot(t)
	t.Setenv("HIDEOUT_SOURCE_ROOT", filepath.Join(source, "cmd", "hideout-shim"))
	root, err := findHelperSourceRoot()
	if err != nil {
		t.Fatal(err)
	}
	if root != source {
		t.Fatalf("source root=%q want %q", root, source)
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

func assertNextStepCommand(t *testing.T, steps []NextStep, id, command string) {
	t.Helper()
	step := nextStepByID(steps, id)
	if step == nil {
		t.Fatalf("next step %q missing in %+v", id, steps)
	}
	if step.Command != command {
		t.Fatalf("next step %q command=%q want %q", id, step.Command, command)
	}
	if step.Label == "" || step.Message == "" {
		t.Fatalf("next step %q should include label and message: %+v", id, step)
	}
}

func nextStepByID(steps []NextStep, id string) *NextStep {
	for i := range steps {
		if steps[i].ID == id {
			return &steps[i]
		}
	}
	return nil
}

func validateInitPlanWithSchema(t *testing.T, plan Plan) {
	t.Helper()
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	schema := compileInitPlanSchema(t)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode init plan json: %v", err)
	}
	if err := schema.Validate(doc); err != nil {
		t.Fatalf("init plan does not match schema: %v\n%s", err, data)
	}
}

func fakeSourceRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, path := range []string{
		"go.mod",
		filepath.Join("cmd", "hideout-shim", "main.go"),
		filepath.Join("cmd", "hideout-hostfsd", "main.go"),
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
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

func compileInitPlanSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "init-plan.schema.json"))
	if err != nil {
		t.Fatalf("read init plan schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode init plan schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("init-plan.schema.json", doc); err != nil {
		t.Fatalf("add init plan schema: %v", err)
	}
	schema, err := compiler.Compile("init-plan.schema.json")
	if err != nil {
		t.Fatalf("compile init plan schema: %v", err)
	}
	return schema
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

func TestInitAuditPassesThroughDeterministicRedaction(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "init-audit.jsonl")
	w, err := openAudit(ApplyOptions{AuditPath: auditPath})
	if err != nil {
		t.Fatalf("openAudit: %v", err)
	}
	plan := Plan{Profile: "default", Backend: "lima", Network: "tun2socks"}
	task := Task{
		ID:      "t1",
		Kind:    "store.create",
		Message: "setup HIDEOUT_SECRET_DEFAULT_PROXY=socks5://user:pass@127.0.0.1:1080",
		Inputs:  []string{"token cap_0123456789abcdef", "plain-user-data"},
		Outputs: []string{"guestMachineId ui_fedcba9876543210"},
	}
	if err := emitTaskAudit(w, plan, task, "apply", "allow", "ok", errors.New("failed HIDEOUT_SECRET_DEFAULT_PROXY=socks5://leak")); err != nil {
		t.Fatalf("emitTaskAudit: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	events := readInitAuditEvents(t, auditPath)
	if len(events) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(events))
	}
	ev := events[0]
	blob := ev.Message + " " + strings.Join(ev.Inputs, " ") + " " + strings.Join(ev.Outputs, " ") + " " + ev.Error
	for _, leak := range []string{"HIDEOUT_SECRET_DEFAULT_PROXY", "cap_0123456789abcdef", "ui_fedcba9876543210"} {
		if strings.Contains(blob, leak) {
			t.Fatalf("init audit leaked control-plane material %q: %s", leak, blob)
		}
	}
	// User/application data is preserved verbatim.
	if !strings.Contains(blob, "plain-user-data") {
		t.Fatalf("init audit dropped user data: %s", blob)
	}
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
