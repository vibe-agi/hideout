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

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/profiletemplate"
	"github.com/vibe-agi/hideout/internal/runtimecatalog"
	"github.com/vibe-agi/hideout/internal/secrets"
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
	ProfileName                string
	Backend                    string
	Network                    string
	ProxySecretRef             string
	MediatedResolver           string
	TemplateID                 string
	PrivilegeStatus            string
	PrivilegeReason            string
	PrivilegeGuidance          string
	PrivilegeSource            string
	AllowDegradedTemplate      bool
	Onboarding                 bool
	ExplicitProfile            bool
	ExplicitTemplate           bool
	ExplicitBackend            bool
	ExplicitNetwork            bool
	NoInput                    bool
	VisibilitySelection        string
	VisibilityRoots            []string
	NameDisclosureAcknowledged bool
	ExplicitVisibility         bool
	ToolPresets                []string
	NPMGlobals                 []profile.NPMGlobalPackage
	RuntimeFamily              string
	ImageRef                   string
	ResolveRuntime             func(runtimecatalog.Selection) (runtimecatalog.Resolution, error)
}

type ApplyOptions struct {
	NoInput        bool
	DryRun         bool
	Operation      string
	AuditPath      string
	ResolveRuntime func(runtimecatalog.Selection) (runtimecatalog.Resolution, error)
}

type Plan struct {
	Version                    string                         `json:"version"`
	StoreRoot                  string                         `json:"storeRoot"`
	Profile                    string                         `json:"profile"`
	Backend                    string                         `json:"backend"`
	Network                    string                         `json:"network"`
	ProxySecretRef             string                         `json:"proxySecretRef,omitempty"`
	MediatedResolver           string                         `json:"mediatedResolver,omitempty"`
	TemplateID                 string                         `json:"templateId,omitempty"`
	EffectivePosture           string                         `json:"effectivePosture,omitempty"`
	PrivilegeStatus            string                         `json:"privilegeStatus,omitempty"`
	PrivilegeReason            string                         `json:"privilegeReason,omitempty"`
	PrivilegeGuidance          string                         `json:"privilegeGuidance,omitempty"`
	PrivilegeSource            string                         `json:"privilegeSource,omitempty"`
	AllowDegradedTemplate      bool                           `json:"allowDegradedTemplate,omitempty"`
	EvidencePath               string                         `json:"evidencePath,omitempty"`
	Warnings                   []string                       `json:"warnings,omitempty"`
	NonClaims                  []string                       `json:"nonClaims,omitempty"`
	ReviewLines                []string                       `json:"reviewLines,omitempty"`
	HostFSVisibility           string                         `json:"hostfsVisibility,omitempty"`
	HostFSVisibilityRoots      []string                       `json:"hostfsVisibilityRoots,omitempty"`
	HostFSVisibilityRuleIDs    []string                       `json:"hostfsVisibilityRuleIds,omitempty"`
	NameDisclosureAcknowledged bool                           `json:"nameDisclosureAcknowledged,omitempty"`
	RuntimeSelection           *environment.RuntimeProvenance `json:"runtimeSelection,omitempty"`
	ImageRef                   string                         `json:"imageRef,omitempty"`
	Tasks                      []Task                         `json:"tasks"`
	NextSteps                  []NextStep                     `json:"nextSteps"`
}

type NextStep struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Command string `json:"command"`
	Message string `json:"message"`
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
	Version      string `json:"version"`
	Plan         Plan   `json:"plan"`
	AuditPath    string `json:"auditPath,omitempty"`
	EvidencePath string `json:"evidencePath,omitempty"`
	Applied      []Task `json:"applied"`
	Skipped      []Task `json:"skipped"`
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
	if err := resolvePlanRuntime(&plan, normalized); err != nil {
		return Plan{}, err
	}
	if strings.TrimSpace(store.Root) == "" {
		return Plan{}, errors.New("init store root is required")
	}
	if normalized.Onboarding {
		rendered, err := profiletemplate.Render(onboardingRequestFromOptions(store, normalized))
		if err != nil {
			return Plan{}, err
		}
		plan.TemplateID = rendered.Template.ID
		plan.EffectivePosture = rendered.EffectivePosture
		plan.PrivilegeStatus = rendered.Evidence.Privilege.Status
		plan.PrivilegeReason = rendered.Evidence.Privilege.Reason
		plan.PrivilegeGuidance = rendered.Evidence.Privilege.Guidance
		plan.PrivilegeSource = rendered.Evidence.Privilege.Source
		plan.AllowDegradedTemplate = normalized.AllowDegradedTemplate
		plan.ProxySecretRef = normalized.ProxySecretRef
		plan.MediatedResolver = normalized.MediatedResolver
		plan.EvidencePath = rendered.EvidencePath
		plan.Warnings = append([]string(nil), rendered.Warnings...)
		plan.NonClaims = append([]string(nil), rendered.NonClaims...)
		plan.ReviewLines = append([]string(nil), rendered.Review.Lines...)
		plan.HostFSVisibility = normalized.VisibilitySelection
		plan.HostFSVisibilityRoots = append([]string(nil), rendered.Evidence.HostFSVisibilityRoots...)
		plan.HostFSVisibilityRuleIDs = append([]string(nil), rendered.VisibilityRuleIDs...)
		plan.NameDisclosureAcknowledged = normalized.NameDisclosureAcknowledged
	}
	plan.Tasks = append(plan.Tasks, storeTask(store.Root))
	plan.Tasks = append(plan.Tasks, stateTask(store.Root))
	profileTask, profileExists, err := profileTask(store, normalized.ProfileName)
	if err != nil {
		return Plan{}, err
	}
	plan.Tasks = append(plan.Tasks, profileTask)
	if task, ok, err := profileEnvironmentTask(store, normalized.ProfileName, profileExists, plan); err != nil {
		return Plan{}, err
	} else if ok {
		plan.Tasks = append(plan.Tasks, task)
	}
	if profileExists {
		identityTask, err := identityTask(store, normalized.ProfileName)
		if err != nil {
			return Plan{}, err
		}
		plan.Tasks = append(plan.Tasks, identityTask)
	}
	networkTask, err := networkTask(store, normalized.ProfileName, normalized.Network, normalized.ProxySecretRef, normalized.MediatedResolver, profileExists)
	if err != nil {
		return Plan{}, err
	}
	plan.Tasks = append(plan.Tasks, networkTask)
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
	plan.NextSteps = initNextSteps(plan)
	return plan, nil
}

func initNextSteps(plan Plan) []NextStep {
	if initPlanHasBlockedTasks(plan) {
		return []NextStep{{
			ID:      "resolve-blocked",
			Label:   "Resolve blocked tasks",
			Command: initDoctorFixCommand(plan),
			Message: "Fix blocked tasks above, then rerun doctor fix.",
		}}
	}
	steps := []NextStep{
		{
			ID:      "doctor-check",
			Label:   "Check setup",
			Command: initDoctorCommand(plan),
			Message: "Verify store, profile, backend, network, and host integration before running a target command.",
		},
		{
			ID:      "smoke-run",
			Label:   "Smoke run",
			Command: initRunCommand(plan, "pwd"),
			Message: "Run a minimal command in the configured backend to verify workspace and identity plumbing.",
		},
	}
	return steps
}

func initDoctorCommand(plan Plan) string {
	return strings.Join([]string{"hideout", "doctor", "--profile", plan.Profile, "--backend", plan.Backend}, " ")
}

func initDoctorFixCommand(plan Plan) string {
	return strings.Join([]string{"hideout", "doctor", "--fix", "--apply", "--profile", plan.Profile, "--backend", plan.Backend}, " ")
}

func initRunCommand(plan Plan, command string) string {
	args := []string{"hideout", "run", "--profile", plan.Profile, "--backend", plan.Backend}
	if plan.Backend == "native" {
		args = append(args, "--allow-weak-isolation")
	}
	args = append(args, "--", command)
	return strings.Join(args, " ")
}

func initPlanHasBlockedTasks(plan Plan) bool {
	for _, task := range plan.Tasks {
		if task.Status == "blocked" {
			return true
		}
	}
	return false
}

func ApplyMachine(store profile.Store, plan Plan, opts ApplyOptions) (Result, error) {
	result := Result{Version: Version, Plan: plan}
	if err := revalidatePlanRuntime(plan, opts.ResolveRuntime); err != nil {
		return result, err
	}
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
	if plan.TemplateID != "" && !opts.DryRun {
		rendered, err := profiletemplate.Render(onboardingRequestFromPlan(store, plan))
		if err != nil {
			return result, err
		}
		evidence := rendered.Evidence
		evidence.InitAuditPath = result.AuditPath
		evidence.NextSteps = initNextStepCommands(plan.NextSteps)
		evidence = profiletemplate.RedactEvidence(evidence)
		evidencePath := plan.EvidencePath
		if evidencePath == "" {
			evidencePath = profiletemplate.EvidencePath(store, plan.Profile)
		}
		if err := profiletemplate.WriteEvidence(evidencePath, evidence); err != nil {
			return result, err
		}
		result.EvidencePath = evidencePath
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
	if len(opts.ToolPresets) > 0 || len(opts.NPMGlobals) > 0 {
		return opts, errors.New("legacy tool-supply init options are no longer supported; declare tools.expectedCommands for diagnostics only")
	}
	opts.ProxySecretRef = strings.TrimSpace(opts.ProxySecretRef)
	opts.MediatedResolver = strings.TrimSpace(opts.MediatedResolver)
	switch opts.Network {
	case "direct":
		if opts.ProxySecretRef != "" {
			return opts, errors.New("direct network mode does not use a proxy secret ref")
		}
		if opts.MediatedResolver != "" {
			return opts, errors.New("direct network mode does not use a mediated resolver")
		}
	case "tun2socks":
		if opts.ProxySecretRef == "" {
			return opts, errors.New("tun2socks network mode requires a proxy secret ref")
		}
		if err := secrets.ValidateRef(opts.ProxySecretRef); err != nil {
			return opts, fmt.Errorf("proxy secret ref: %w", err)
		}
	}
	opts.TemplateID = strings.TrimSpace(opts.TemplateID)
	opts.PrivilegeStatus = strings.TrimSpace(opts.PrivilegeStatus)
	opts.PrivilegeReason = strings.TrimSpace(opts.PrivilegeReason)
	opts.PrivilegeGuidance = strings.TrimSpace(opts.PrivilegeGuidance)
	opts.PrivilegeSource = strings.TrimSpace(opts.PrivilegeSource)
	opts.VisibilitySelection = strings.TrimSpace(opts.VisibilitySelection)
	opts.VisibilityRoots = normalizeStringList(opts.VisibilityRoots)
	opts.RuntimeFamily = strings.TrimSpace(opts.RuntimeFamily)
	opts.ImageRef = strings.TrimSpace(opts.ImageRef)
	if opts.RuntimeFamily != "" && opts.ImageRef != "" {
		return opts, errors.New("--runtime and --image are mutually exclusive")
	}
	if opts.RuntimeFamily != "" && opts.Backend != "lima" {
		return opts, errors.New("catalog runtimes require the Lima backend")
	}
	if opts.ImageRef != "" {
		if _, err := environment.ParseImageDeclaration(opts.ImageRef); err != nil {
			return opts, fmt.Errorf("init image: %w", err)
		}
	}
	return opts, nil
}

func resolvePlanRuntime(plan *Plan, opts Options) error {
	if plan == nil || opts.RuntimeFamily == "" {
		if plan != nil {
			plan.ImageRef = opts.ImageRef
		}
		return nil
	}
	resolver := opts.ResolveRuntime
	if resolver == nil {
		resolver = runtimecatalog.ResolveEmbedded
	}
	resolved, err := resolver(runtimecatalog.Selection{
		Family: opts.RuntimeFamily, HostOS: runtime.GOOS, HostArch: runtime.GOARCH,
	})
	if err != nil {
		return fmt.Errorf("resolve runtime %q: %w", opts.RuntimeFamily, err)
	}
	provenance := resolved.Provenance
	plan.RuntimeSelection = &provenance
	plan.ImageRef = resolved.ImageRef
	return nil
}

func revalidatePlanRuntime(plan Plan, resolver func(runtimecatalog.Selection) (runtimecatalog.Resolution, error)) error {
	if plan.RuntimeSelection == nil {
		if plan.ImageRef == "" {
			return nil
		}
		_, err := environment.ParseImageDeclaration(plan.ImageRef)
		return err
	}
	if resolver == nil {
		resolver = runtimecatalog.ResolveEmbedded
	}
	want := *plan.RuntimeSelection
	resolved, err := resolver(runtimecatalog.Selection{
		Family: want.Family, Revision: want.Revision, HostOS: runtime.GOOS, HostArch: runtime.GOARCH,
	})
	if err != nil {
		return fmt.Errorf("re-resolve runtime %q: %w", want.Family, err)
	}
	if resolved.Provenance != want || resolved.ImageRef != plan.ImageRef {
		return errors.New("runtime catalog changed between plan and apply; create a new plan")
	}
	return nil
}

func onboardingRequestFromOptions(store profile.Store, opts Options) profiletemplate.Request {
	return profiletemplate.Request{
		ProfileName:                opts.ProfileName,
		TemplateID:                 opts.TemplateID,
		Backend:                    opts.Backend,
		Network:                    opts.Network,
		ProxySecretRef:             opts.ProxySecretRef,
		MediatedResolver:           opts.MediatedResolver,
		Privilege:                  onboardingPrivilegeFromOptions(opts),
		AllowDegradedTemplate:      opts.AllowDegradedTemplate,
		NoInput:                    opts.NoInput,
		ExplicitProfile:            opts.ExplicitProfile,
		ExplicitTemplate:           opts.ExplicitTemplate,
		ExplicitBackend:            opts.ExplicitBackend,
		ExplicitNetwork:            opts.ExplicitNetwork,
		CheckExistingProfile:       opts.Onboarding,
		Store:                      store,
		AdditionalWarning:          "",
		AdditionalNonClaim:         "",
		VisibilitySelection:        opts.VisibilitySelection,
		VisibilityRoots:            append([]string(nil), opts.VisibilityRoots...),
		NameDisclosureAcknowledged: opts.NameDisclosureAcknowledged,
		ExplicitVisibility:         opts.ExplicitVisibility,
	}
}

func onboardingRequestFromPlan(store profile.Store, plan Plan) profiletemplate.Request {
	return profiletemplate.Request{
		ProfileName:                plan.Profile,
		TemplateID:                 plan.TemplateID,
		Backend:                    plan.Backend,
		Network:                    plan.Network,
		ProxySecretRef:             plan.ProxySecretRef,
		MediatedResolver:           plan.MediatedResolver,
		Privilege:                  onboardingPrivilegeFromPlan(plan),
		AllowDegradedTemplate:      plan.AllowDegradedTemplate,
		NoInput:                    true,
		ExplicitProfile:            true,
		ExplicitTemplate:           true,
		ExplicitBackend:            true,
		ExplicitNetwork:            true,
		CheckExistingProfile:       false,
		Store:                      store,
		AdditionalWarning:          "",
		AdditionalNonClaim:         "",
		VisibilitySelection:        plan.HostFSVisibility,
		VisibilityRoots:            append([]string(nil), plan.HostFSVisibilityRoots...),
		NameDisclosureAcknowledged: plan.NameDisclosureAcknowledged,
		ExplicitVisibility:         true,
	}
}

func onboardingPrivilegeFromOptions(opts Options) profiletemplate.PrivilegeFact {
	return profiletemplate.PrivilegeFact{
		Status:   opts.PrivilegeStatus,
		Reason:   opts.PrivilegeReason,
		Guidance: opts.PrivilegeGuidance,
		Source:   opts.PrivilegeSource,
	}
}

func onboardingPrivilegeFromPlan(plan Plan) profiletemplate.PrivilegeFact {
	return profiletemplate.PrivilegeFact{
		Status:   plan.PrivilegeStatus,
		Reason:   plan.PrivilegeReason,
		Guidance: plan.PrivilegeGuidance,
		Source:   plan.PrivilegeSource,
	}
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

func profileEnvironmentTask(store profile.Store, name string, profileExists bool, plan Plan) (Task, bool, error) {
	if plan.RuntimeSelection == nil && plan.ImageRef == "" {
		return Task{}, false, nil
	}
	task := Task{
		ID:                 "init_profile_environment_select",
		Kind:               "profile.environment.select",
		Source:             "builtin",
		Status:             "pending",
		TargetScope:        "profile",
		CapabilityBoundary: "guest-image",
		Risk:               "safe",
		Outputs:            []string{store.ProfilePath(name)},
		Message:            "select immutable profile guest image",
	}
	if plan.RuntimeSelection != nil {
		task.Inputs = []string{plan.RuntimeSelection.Family, plan.RuntimeSelection.Revision, plan.RuntimeSelection.ArtifactSHA256}
	} else {
		task.Inputs = []string{plan.ImageRef}
	}
	if !profileExists {
		return task, true, nil
	}
	p, err := store.Load(name)
	if err != nil {
		return Task{}, false, err
	}
	same := false
	if plan.RuntimeSelection != nil && p.Environment.Runtime != nil {
		same = *p.Environment.Runtime == *plan.RuntimeSelection && p.Environment.BaseImage == ""
	} else if plan.RuntimeSelection == nil {
		same = p.Environment.Runtime == nil && p.Environment.BaseImage == plan.ImageRef
	}
	if same {
		task.Status = "ok"
		task.Message = "profile guest image selection already matches"
		return task, true, nil
	}
	task.Risk = "requires-confirmation"
	task.RequiresConfirm = true
	task.Message = "changing an existing profile guest image requires confirmation and environment recreation"
	return task, true, nil
}

func networkTask(store profile.Store, name, mode, proxySecretRef, mediatedResolver string, profileExists bool) (Task, error) {
	inputs := []string{mode}
	if proxySecretRef != "" {
		inputs = append(inputs, proxySecretRef)
	}
	if mediatedResolver != "" {
		if proxySecretRef == "" {
			inputs = append(inputs, "")
		}
		inputs = append(inputs, mediatedResolver)
	}
	task := Task{
		ID:                 "init_network_select",
		Kind:               "network.mode.select",
		Source:             "builtin",
		Status:             "ok",
		TargetScope:        "profile",
		CapabilityBoundary: "network",
		Risk:               "safe",
		Inputs:             inputs,
		Outputs:            []string{store.ProfilePath(name)},
		Message:            "network mode is selected",
	}
	if !profileExists {
		if mode != "direct" || proxySecretRef != "" {
			task.Status = "pending"
			task.Message = "set profile network mode to " + mode
		}
		return task, nil
	}
	p, err := store.Load(name)
	if err != nil {
		return Task{}, err
	}
	if p.Network.Mode == mode && strings.TrimSpace(p.Network.ProxySecretRef) == proxySecretRef && strings.TrimSpace(p.Network.MediatedResolver) == mediatedResolver {
		return task, nil
	}
	task.Status = "pending"
	task.Risk = "requires-confirmation"
	task.RequiresConfirm = true
	task.Message = "changing existing profile network settings requires confirmation"
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
	// Route free-text fields through the shared deterministic control-plane
	// redaction so InitTask audit strips the same Hideout-minted material
	// (HIDEOUT_SECRET_* names/values, Core tokens, machine-id) as the rest of
	// audit, and preserves user/application data verbatim.
	event.Message = audit.RedactString(event.Message)
	event.Inputs = audit.RedactArgv(event.Inputs)
	event.Outputs = audit.RedactArgv(event.Outputs)
	event.Error = audit.RedactString(event.Error)
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
		if plan.TemplateID != "" {
			rendered, err := profiletemplate.Render(onboardingRequestFromPlan(store, plan))
			if err != nil {
				return err
			}
			return store.Create(rendered.Profile)
		}
		_, err := store.LoadOrInit(plan.Profile)
		return err
	case "identity.materialize":
		p, err := store.Load(plan.Profile)
		if err != nil {
			return err
		}
		return profile.MaterializeIdentityState(store.ProfileDir(plan.Profile), p)
	case "profile.environment.select":
		p, err := store.LoadOrInit(plan.Profile)
		if err != nil {
			return err
		}
		if plan.RuntimeSelection != nil {
			provenance := *plan.RuntimeSelection
			p.Environment.Runtime = &provenance
			p.Environment.BaseImage = ""
		} else {
			p.Environment.Runtime = nil
			p.Environment.BaseImage = plan.ImageRef
		}
		return store.Save(p)
	case "network.mode.select":
		if len(task.Inputs) < 1 || len(task.Inputs) > 3 {
			return fmt.Errorf("network.mode.select task requires mode, optional proxy secret ref, and optional mediated resolver inputs")
		}
		p, err := store.LoadOrInit(plan.Profile)
		if err != nil {
			return err
		}
		p.Network.Mode = task.Inputs[0]
		if len(task.Inputs) >= 2 {
			p.Network.ProxySecretRef = task.Inputs[1]
		} else {
			p.Network.ProxySecretRef = ""
		}
		if len(task.Inputs) == 3 {
			p.Network.MediatedResolver = task.Inputs[2]
		} else {
			p.Network.MediatedResolver = plan.MediatedResolver
		}
		return store.Save(p)
	case "helper.install.linux-shim":
		return buildLinuxHelper(store.Root, "hideout-shim")
	case "helper.install.linux-hostfsd":
		return buildLinuxHelper(store.Root, "hideout-hostfsd")
	default:
		return fmt.Errorf("unsupported init task %q", task.Kind)
	}
}

func initNextStepCommands(steps []NextStep) []string {
	out := make([]string, 0, len(steps))
	for _, step := range steps {
		if step.Command != "" {
			out = append(out, step.Command)
		}
	}
	return out
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

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
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
