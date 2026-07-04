package inittask

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	Version      = "hideout.init/v1"
	StateFile    = "install-state.json"
	StateVersion = "hideout.install-state/v1"
	AuditVersion = "hideout.init-audit/v1"
	AuditFile    = "init-audit.jsonl"

	OperationInitApply      = "init.apply"
	OperationRunInitApply   = "run.init.apply"
	OperationDoctorFixApply = "doctor.fix.apply"
)

type Options struct {
	ProfileName string
	Backend     string
	Network     string
	NoInput     bool
	ToolPresets []string
	NPMGlobals  []profile.NPMGlobalPackage
}

type ApplyOptions struct {
	NoInput   bool
	DryRun    bool
	Operation string
	AuditPath string
}

type Plan struct {
	Version   string `json:"version"`
	StoreRoot string `json:"storeRoot"`
	Profile   string `json:"profile"`
	Backend   string `json:"backend"`
	Network   string `json:"network"`
	Tasks     []Task `json:"tasks"`
}

type Task struct {
	ID                 string   `json:"id"`
	Kind               string   `json:"kind"`
	Source             string   `json:"source"`
	Status             string   `json:"status"`
	TargetScope        string   `json:"targetScope"`
	CapabilityBoundary string   `json:"capabilityBoundary"`
	Risk               string   `json:"risk"`
	RequiresConfirm    bool     `json:"requiresConfirmation"`
	Inputs             []string `json:"inputs,omitempty"`
	Outputs            []string `json:"outputs,omitempty"`
	Message            string   `json:"message"`
}

type Result struct {
	Version   string `json:"version"`
	Plan      Plan   `json:"plan"`
	AuditPath string `json:"auditPath,omitempty"`
	Applied   []Task `json:"applied"`
	Skipped   []Task `json:"skipped"`
}

type StoreSummary struct {
	Version      string `json:"version"`
	StoreRoot    string `json:"storeRoot"`
	Profile      string `json:"profile"`
	Initialized  bool   `json:"initialized"`
	PendingTasks int    `json:"pendingTasks"`
	StatePath    string `json:"statePath"`
	AuditPath    string `json:"auditPath"`
	AuditEvents  int    `json:"auditEvents"`
}

type AuditEvent struct {
	Version            string    `json:"version"`
	Time               time.Time `json:"time"`
	Operation          string    `json:"operation"`
	Profile            string    `json:"profile"`
	Backend            string    `json:"backend"`
	Network            string    `json:"network"`
	TaskID             string    `json:"taskId"`
	TaskKind           string    `json:"taskKind"`
	Source             string    `json:"source"`
	TargetScope        string    `json:"targetScope"`
	CapabilityBoundary string    `json:"capabilityBoundary"`
	Risk               string    `json:"risk"`
	TaskStatus         string    `json:"taskStatus"`
	Result             string    `json:"result"`
	Decision           string    `json:"decision"`
	Inputs             []string  `json:"inputs,omitempty"`
	Outputs            []string  `json:"outputs,omitempty"`
	Message            string    `json:"message,omitempty"`
	Error              string    `json:"error,omitempty"`
}

type installState struct {
	Version       string `json:"version"`
	ProfileSchema string `json:"profileSchema"`
	UpdatedAt     string `json:"updatedAt"`
}

func PlanMachine(store profile.Store, opts Options) (Plan, error) {
	normalized, err := normalizeOptions(opts)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		Version:   Version,
		StoreRoot: store.Root,
		Profile:   normalized.ProfileName,
		Backend:   normalized.Backend,
		Network:   normalized.Network,
	}
	if strings.TrimSpace(store.Root) == "" {
		return Plan{}, errors.New("init store root is required")
	}
	plan.Tasks = append(plan.Tasks, storeTask(store.Root))
	plan.Tasks = append(plan.Tasks, stateTask(store.Root))
	profileTask, profileExists, err := profileTask(store, normalized.ProfileName)
	if err != nil {
		return Plan{}, err
	}
	plan.Tasks = append(plan.Tasks, profileTask)
	if profileExists {
		identityTask, err := identityTask(store, normalized.ProfileName)
		if err != nil {
			return Plan{}, err
		}
		plan.Tasks = append(plan.Tasks, identityTask)
	}
	networkTask, err := networkTask(store, normalized.ProfileName, normalized.Network, profileExists)
	if err != nil {
		return Plan{}, err
	}
	plan.Tasks = append(plan.Tasks, networkTask)
	toolPresets := append([]string(nil), normalized.ToolPresets...)
	if len(normalized.NPMGlobals) > 0 {
		toolPresets = appendUniqueString(toolPresets, "node-dev")
	}
	for _, preset := range toolPresets {
		task, err := toolPresetTask(store, normalized.ProfileName, preset, profileExists)
		if err != nil {
			return Plan{}, err
		}
		plan.Tasks = append(plan.Tasks, task)
	}
	for _, pkg := range normalized.NPMGlobals {
		task, err := npmGlobalTask(store, normalized.ProfileName, pkg, profileExists)
		if err != nil {
			return Plan{}, err
		}
		plan.Tasks = append(plan.Tasks, task)
	}
	plan.Tasks = append(plan.Tasks, Task{
		ID:                 "init_backend_probe",
		Kind:               "backend.probe",
		Source:             "builtin",
		Status:             "ok",
		TargetScope:        "machine",
		CapabilityBoundary: "backend",
		Risk:               "safe",
		Inputs:             []string{normalized.Backend},
		Message:            "backend prerequisite checks are handled by doctor/run gates",
	})
	if normalized.Backend == "lima" {
		plan.Tasks = append(plan.Tasks,
			helperTask(store.Root, "install_lima_linux_shim", "helper.install.linux-shim", "hideout-shim", helperbin.DefaultLinuxShimPath(store.Root, runtime.GOARCH)),
			helperTask(store.Root, "install_lima_linux_hostfsd", "helper.install.linux-hostfsd", "hideout-hostfsd", helperbin.DefaultLinuxHostFSDPath(store.Root, runtime.GOARCH)),
		)
	}
	plan.Tasks = append(plan.Tasks, Task{
		ID:                 "init_doctor_light",
		Kind:               "doctor.check.light",
		Source:             "builtin",
		Status:             "ok",
		TargetScope:        "machine",
		CapabilityBoundary: "diagnostics",
		Risk:               "safe",
		Message:            "doctor can run after initialization",
	})
	return plan, nil
}

func ApplyMachine(store profile.Store, plan Plan, opts ApplyOptions) (Result, error) {
	result := Result{Version: Version, Plan: plan}
	if opts.Operation == "" {
		opts.Operation = OperationInitApply
	}
	if !opts.DryRun {
		if opts.AuditPath == "" {
			opts.AuditPath = DefaultAuditPath(store.Root)
		}
		result.AuditPath = opts.AuditPath
	}
	aw, err := openAudit(opts)
	if err != nil {
		return result, err
	}
	if aw != nil {
		defer aw.Close()
	}
	for _, task := range plan.Tasks {
		if task.Status != "pending" {
			result.Skipped = append(result.Skipped, task)
			if err := emitTaskAudit(aw, plan, task, opts.Operation, "audit-only", "skipped", nil); err != nil {
				return result, err
			}
			continue
		}
		if task.RequiresConfirm || task.Risk == "requires-confirmation" {
			if opts.NoInput {
				err := fmt.Errorf("init task %s requires confirmation", task.Kind)
				_ = emitTaskAudit(aw, plan, task, opts.Operation, "error", "blocked", err)
				return result, err
			}
			err := fmt.Errorf("init task %s requires interactive confirmation; TUI apply is not implemented yet", task.Kind)
			_ = emitTaskAudit(aw, plan, task, opts.Operation, "error", "blocked", err)
			return result, err
		}
		if opts.DryRun {
			result.Skipped = append(result.Skipped, task)
			continue
		}
		if err := applyTask(store, plan, task); err != nil {
			_ = emitTaskAudit(aw, plan, task, opts.Operation, "error", "error", err)
			return result, err
		}
		task.Status = "applied"
		result.Applied = append(result.Applied, task)
		if err := emitTaskAudit(aw, plan, task, opts.Operation, "allow", "applied", nil); err != nil {
			return result, err
		}
	}
	return result, nil
}

func Summary(store profile.Store, profileName string) StoreSummary {
	if profileName == "" {
		profileName = "default"
	}
	plan, err := PlanMachine(store, Options{ProfileName: profileName, Backend: "auto", Network: "direct", NoInput: true})
	summary := StoreSummary{
		Version:   Version,
		StoreRoot: store.Root,
		Profile:   profileName,
		StatePath: filepath.Join(store.Root, StateFile),
		AuditPath: DefaultAuditPath(store.Root),
	}
	if err != nil {
		return summary
	}
	for _, task := range plan.Tasks {
		if task.Status == "pending" {
			summary.PendingTasks++
		}
	}
	summary.Initialized = summary.PendingTasks == 0
	summary.AuditEvents = countAuditEvents(summary.AuditPath)
	return summary
}

func DefaultAuditPath(storeRoot string) string {
	return filepath.Join(storeRoot, "logs", AuditFile)
}

func normalizeOptions(opts Options) (Options, error) {
	if opts.ProfileName == "" {
		opts.ProfileName = "default"
	}
	if err := profile.ValidateName(opts.ProfileName); err != nil {
		return opts, err
	}
	if opts.Backend == "" || opts.Backend == "auto" {
		opts.Backend = "lima"
	}
	if opts.Backend != "native" && opts.Backend != "lima" {
		return opts, fmt.Errorf("backend %q is not implemented yet", opts.Backend)
	}
	if opts.Network == "" {
		opts.Network = "direct"
	}
	if opts.Network != "direct" && opts.Network != "tun2socks" {
		return opts, fmt.Errorf("unsupported network mode %q", opts.Network)
	}
	opts.ToolPresets = normalizeStringList(opts.ToolPresets)
	opts.NPMGlobals = normalizeNPMGlobals(opts.NPMGlobals)
	if err := validateToolOptions(opts.ToolPresets, opts.NPMGlobals); err != nil {
		return opts, err
	}
	return opts, nil
}

func storeTask(root string) Task {
	missing := missingPaths(storeDirs(root))
	status := "ok"
	message := "store directories already exist"
	if len(missing) > 0 {
		status = "pending"
		message = "create store directories"
	}
	return Task{
		ID:                 "init_store_create",
		Kind:               "store.create",
		Source:             "builtin",
		Status:             status,
		TargetScope:        "machine",
		CapabilityBoundary: "store",
		Risk:               "safe",
		Outputs:            storeDirs(root),
		Message:            message,
	}
}

func stateTask(root string) Task {
	path := filepath.Join(root, StateFile)
	status := "ok"
	message := "install state metadata is current"
	switch currentStateStatus(path) {
	case "missing":
		status = "pending"
		message = "write install state metadata"
	case "stale", "invalid":
		status = "pending"
		message = "rewrite install state metadata for the current schema"
	}
	return Task{
		ID:                 "init_schema_metadata",
		Kind:               "schema.metadata.write",
		Source:             "builtin",
		Status:             status,
		TargetScope:        "machine",
		CapabilityBoundary: "schema",
		Risk:               "safe",
		Outputs:            []string{path},
		Message:            message,
	}
}

func profileTask(store profile.Store, name string) (Task, bool, error) {
	p, err := store.Load(name)
	if err == nil {
		return Task{
			ID:                 "init_profile_create",
			Kind:               "profile.create",
			Source:             "builtin",
			Status:             "ok",
			TargetScope:        "profile",
			CapabilityBoundary: "identity",
			Risk:               "safe",
			Outputs:            []string{store.ProfilePath(name)},
			Message:            "profile already exists",
		}, true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Task{}, false, err
	}
	_ = p
	return Task{
		ID:                 "init_profile_create",
		Kind:               "profile.create",
		Source:             "builtin",
		Status:             "pending",
		TargetScope:        "profile",
		CapabilityBoundary: "identity",
		Risk:               "safe",
		Outputs:            []string{store.ProfilePath(name)},
		Message:            "create default profile and identity state",
	}, false, nil
}

func identityTask(store profile.Store, name string) (Task, error) {
	p, err := store.Load(name)
	if err != nil {
		return Task{}, err
	}
	path := filepath.Join(store.ProfileDir(name), "machine", "machine-id")
	status := "ok"
	message := "profile identity state is materialized"
	if _, err := os.Stat(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Task{}, err
		}
		status = "pending"
		message = "materialize profile identity state"
	}
	return Task{
		ID:                 "init_identity_materialize",
		Kind:               "identity.materialize",
		Source:             "builtin",
		Status:             status,
		TargetScope:        "profile",
		CapabilityBoundary: "identity",
		Risk:               "safe",
		Inputs:             []string{p.Metadata["identityId"]},
		Outputs:            []string{store.ProfileDir(name)},
		Message:            message,
	}, nil
}

func networkTask(store profile.Store, name, mode string, profileExists bool) (Task, error) {
	task := Task{
		ID:                 "init_network_select",
		Kind:               "network.mode.select",
		Source:             "builtin",
		Status:             "ok",
		TargetScope:        "profile",
		CapabilityBoundary: "network",
		Risk:               "safe",
		Inputs:             []string{mode},
		Message:            "network mode is selected",
	}
	if !profileExists {
		return task, nil
	}
	p, err := store.Load(name)
	if err != nil {
		return Task{}, err
	}
	if p.Network.Mode == mode {
		return task, nil
	}
	task.Status = "pending"
	task.Risk = "requires-confirmation"
	task.RequiresConfirm = true
	task.Message = "changing existing profile network mode requires confirmation"
	return task, nil
}

func toolPresetTask(store profile.Store, name, preset string, profileExists bool) (Task, error) {
	task := Task{
		ID:                 "init_tool_preset_" + safeTaskID(preset),
		Kind:               "tools.preset.add",
		Source:             "builtin",
		Status:             "pending",
		TargetScope:        "profile",
		CapabilityBoundary: "tool-supply",
		Risk:               "safe",
		Inputs:             []string{preset},
		Outputs:            []string{store.ProfilePath(name)},
		Message:            "add tool preset " + preset + " to profile",
	}
	if !profileExists {
		return task, nil
	}
	p, err := store.Load(name)
	if err != nil {
		return Task{}, err
	}
	if containsString(p.Tools.Presets, preset) {
		task.Status = "ok"
		task.Message = "tool preset " + preset + " is already configured"
	}
	return task, nil
}

func npmGlobalTask(store profile.Store, name string, pkg profile.NPMGlobalPackage, profileExists bool) (Task, error) {
	task := Task{
		ID:                 "init_tool_npm_" + safeTaskID(pkg.Package),
		Kind:               "tools.npm-global.add",
		Source:             "builtin",
		Status:             "pending",
		TargetScope:        "profile",
		CapabilityBoundary: "tool-supply",
		Risk:               "safe",
		Inputs:             append([]string{pkg.Package}, pkg.Commands...),
		Outputs:            []string{store.ProfilePath(name)},
		Message:            "add npm global tool " + pkg.Package + " to profile",
	}
	if !profileExists {
		return task, nil
	}
	p, err := store.Load(name)
	if err != nil {
		return Task{}, err
	}
	for _, existing := range p.Tools.NPMGlobals {
		if strings.TrimSpace(existing.Package) == pkg.Package && equalStringSlices(existing.Commands, pkg.Commands) {
			task.Status = "ok"
			task.Message = "npm global tool " + pkg.Package + " is already configured"
			break
		}
	}
	return task, nil
}

func helperTask(storeRoot, id, kind, command, output string) Task {
	status := "ok"
	message := command + " linux helper already discoverable"
	if !linuxHelperDiscoverable(storeRoot, command) {
		if _, err := findHelperSourceRoot(); err != nil {
			status = "blocked"
			message = command + " linux helper missing and Hideout source root is unavailable; install packaged helper or run explicit build command"
		} else {
			status = "pending"
			message = "build " + command + " linux helper into store"
		}
	}
	return Task{
		ID:                 id,
		Kind:               kind,
		Source:             "builtin",
		Status:             status,
		TargetScope:        "machine",
		CapabilityBoundary: "distribution",
		Risk:               "safe",
		Inputs:             []string{command, "linux/" + runtime.GOARCH},
		Outputs:            []string{output, helperbin.ManifestPath(output)},
		Message:            message,
	}
}

func linuxHelperDiscoverable(storeRoot, command string) bool {
	switch command {
	case "hideout-shim":
		return helperbin.ResolveLinuxShimPath(storeRoot, runtime.GOARCH) != ""
	case "hideout-hostfsd":
		return helperbin.ResolveLinuxHostFSDPath(storeRoot, runtime.GOARCH) != ""
	default:
		return false
	}
}

type auditWriter struct {
	file *os.File
	enc  *json.Encoder
}

func openAudit(opts ApplyOptions) (*auditWriter, error) {
	if opts.DryRun || opts.AuditPath == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(opts.AuditPath), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(opts.AuditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &auditWriter{file: file, enc: json.NewEncoder(file)}, nil
}

func (w *auditWriter) Close() error {
	if w == nil || w.file == nil {
		return nil
	}
	return w.file.Close()
}

func emitTaskAudit(w *auditWriter, plan Plan, task Task, operation, decision, result string, taskErr error) error {
	if w == nil {
		return nil
	}
	event := AuditEvent{
		Version:            AuditVersion,
		Time:               time.Now().UTC(),
		Operation:          operation,
		Profile:            plan.Profile,
		Backend:            plan.Backend,
		Network:            plan.Network,
		TaskID:             task.ID,
		TaskKind:           task.Kind,
		Source:             task.Source,
		TargetScope:        task.TargetScope,
		CapabilityBoundary: task.CapabilityBoundary,
		Risk:               task.Risk,
		TaskStatus:         task.Status,
		Result:             result,
		Decision:           decision,
		Inputs:             append([]string(nil), task.Inputs...),
		Outputs:            append([]string(nil), task.Outputs...),
		Message:            task.Message,
	}
	if taskErr != nil {
		event.Error = taskErr.Error()
	}
	return w.enc.Encode(event)
}

func applyTask(store profile.Store, plan Plan, task Task) error {
	switch task.Kind {
	case "store.create":
		for _, dir := range storeDirs(store.Root) {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return err
			}
		}
		return nil
	case "schema.metadata.write":
		return writeState(filepath.Join(store.Root, StateFile))
	case "profile.create":
		_, err := store.LoadOrInit(plan.Profile)
		return err
	case "identity.materialize":
		p, err := store.Load(plan.Profile)
		if err != nil {
			return err
		}
		return profile.MaterializeIdentityState(store.ProfileDir(plan.Profile), p)
	case "tools.preset.add":
		if len(task.Inputs) != 1 {
			return fmt.Errorf("tools.preset.add task requires one preset input")
		}
		p, err := store.LoadOrInit(plan.Profile)
		if err != nil {
			return err
		}
		p.Tools.Presets = appendUniqueString(p.Tools.Presets, task.Inputs[0])
		return store.Save(p)
	case "tools.npm-global.add":
		if len(task.Inputs) < 2 {
			return fmt.Errorf("tools.npm-global.add task requires package and command inputs")
		}
		p, err := store.LoadOrInit(plan.Profile)
		if err != nil {
			return err
		}
		pkg := profile.NPMGlobalPackage{
			Package:  task.Inputs[0],
			Commands: append([]string(nil), task.Inputs[1:]...),
		}
		p.Tools.NPMGlobals = upsertNPMGlobal(p.Tools.NPMGlobals, pkg)
		return store.Save(p)
	case "helper.install.linux-shim":
		return buildLinuxHelper(store.Root, "hideout-shim")
	case "helper.install.linux-hostfsd":
		return buildLinuxHelper(store.Root, "hideout-hostfsd")
	default:
		return fmt.Errorf("unsupported init task %q", task.Kind)
	}
}

func normalizeStringList(values []string) []string {
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = appendUniqueString(out, value)
	}
	return out
}

func normalizeNPMGlobals(values []profile.NPMGlobalPackage) []profile.NPMGlobalPackage {
	out := make([]profile.NPMGlobalPackage, 0, len(values))
	for _, value := range values {
		pkg := profile.NPMGlobalPackage{
			Package:  strings.TrimSpace(value.Package),
			Commands: normalizeStringList(value.Commands),
		}
		if pkg.Package == "" && len(pkg.Commands) == 0 {
			continue
		}
		out = append(out, pkg)
	}
	return out
}

func validateToolOptions(presets []string, npmGlobals []profile.NPMGlobalPackage) error {
	if len(presets) == 0 && len(npmGlobals) == 0 {
		return nil
	}
	p := profile.Default("tool-check")
	if len(presets) > 0 {
		p.Tools.Presets = presets
	}
	p.Tools.NPMGlobals = npmGlobals
	return p.Validate()
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func containsString(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func upsertNPMGlobal(values []profile.NPMGlobalPackage, pkg profile.NPMGlobalPackage) []profile.NPMGlobalPackage {
	out := make([]profile.NPMGlobalPackage, 0, len(values)+1)
	replaced := false
	for _, existing := range values {
		if strings.TrimSpace(existing.Package) == pkg.Package {
			out = append(out, pkg)
			replaced = true
			continue
		}
		out = append(out, existing)
	}
	if !replaced {
		out = append(out, pkg)
	}
	return out
}

func safeTaskID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "value"
	}
	return out
}

func buildLinuxHelper(storeRoot, command string) error {
	source, err := findHelperSourceRoot()
	if err != nil {
		return err
	}
	out := helperbin.DefaultLinuxShimPath(storeRoot, runtime.GOARCH)
	if command == "hideout-hostfsd" {
		out = helperbin.DefaultLinuxHostFSDPath(storeRoot, runtime.GOARCH)
	}
	return helperbin.BuildLinuxCommand(helperbin.BuildOptions{
		Out:     out,
		GOARCH:  runtime.GOARCH,
		Source:  source,
		Command: command,
	})
}

func findHelperSourceRoot() (string, error) {
	var errs []error
	if explicit := strings.TrimSpace(os.Getenv("HIDEOUT_SOURCE_ROOT")); explicit != "" {
		root, err := helperbin.FindSourceRoot(explicit)
		if err == nil {
			return root, nil
		}
		errs = append(errs, fmt.Errorf("HIDEOUT_SOURCE_ROOT: %w", err))
	}
	if exe, err := os.Executable(); err == nil {
		root, err := helperbin.FindSourceRoot(exe)
		if err == nil {
			return root, nil
		}
		errs = append(errs, fmt.Errorf("executable path: %w", err))
	} else {
		errs = append(errs, fmt.Errorf("executable path: %w", err))
	}
	root, err := helperbin.FindSourceRoot(".")
	if err == nil {
		return root, nil
	}
	errs = append(errs, fmt.Errorf("working directory: %w", err))
	return "", errors.Join(errs...)
}

func storeDirs(root string) []string {
	return []string{
		root,
		filepath.Join(root, "profiles"),
		filepath.Join(root, "sessions"),
		filepath.Join(root, "environments"),
		filepath.Join(root, "bin"),
		filepath.Join(root, "cache"),
		filepath.Join(root, "logs"),
		filepath.Join(root, "schemas"),
		filepath.Join(root, "runtime"),
	}
}

func missingPaths(paths []string) []string {
	var missing []string
	for _, path := range paths {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			missing = append(missing, path)
		}
	}
	return missing
}

func stateCurrent(path string) bool {
	return currentStateStatus(path) == "current"
}

func currentStateStatus(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "missing"
		}
		return "invalid"
	}
	var state installState
	if err := json.Unmarshal(data, &state); err != nil {
		return "invalid"
	}
	if state.Version == StateVersion && state.ProfileSchema == profile.SchemaVersion {
		return "current"
	}
	return "stale"
}

func writeState(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	state := installState{
		Version:       StateVersion,
		ProfileSchema: profile.SchemaVersion,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func countAuditEvents(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		count++
	}
	return count
}
