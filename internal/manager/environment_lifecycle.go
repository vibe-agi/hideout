package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/runtimecatalog"
)

const (
	EnvironmentActionStop  = "stop"
	EnvironmentActionClean = "clean"
)

type EnvironmentActionOptions struct {
	IDs         []string
	StoppedOnly bool
	Idle        time.Duration
	IdleSet     bool
	Now         time.Time
}

type EnvironmentActionFilter struct {
	StoppedOnly bool   `json:"stoppedOnly,omitempty"`
	Idle        string `json:"idle,omitempty"`
}

type EnvironmentActionPlan struct {
	Action       string                    `json:"action"`
	RequestedIDs []string                  `json:"requestedIds,omitempty"`
	Filter       EnvironmentActionFilter   `json:"filter,omitempty"`
	Targets      []EnvironmentActionTarget `json:"targets"`
	Skipped      []EnvironmentActionTarget `json:"skipped"`
	Total        int                       `json:"total"`
}

type EnvironmentActionResult struct {
	Plan    EnvironmentActionPlan     `json:"plan"`
	Applied []EnvironmentActionTarget `json:"applied"`
	Skipped []EnvironmentActionTarget `json:"skipped"`
}

type EnvironmentActionTarget struct {
	ID             string    `json:"id"`
	Profile        string    `json:"profile,omitempty"`
	Backend        string    `json:"backend,omitempty"`
	Status         string    `json:"status,omitempty"`
	Workspace      string    `json:"workspace,omitempty"`
	GuestWorkspace string    `json:"guestWorkspace,omitempty"`
	InstanceName   string    `json:"instanceName,omitempty"`
	LastSessionID  string    `json:"lastSessionId,omitempty"`
	LastCommand    string    `json:"lastCommand,omitempty"`
	CreatedAt      time.Time `json:"createdAt,omitempty"`
	LastStartedAt  time.Time `json:"lastStartedAt,omitempty"`
	LastEndedAt    time.Time `json:"lastEndedAt,omitempty"`
	Reason         string    `json:"reason,omitempty"`
}

type EnvironmentOperator interface {
	StopInstance(context.Context, string) error
	Cleanup(context.Context, *backend.Session) error
}

type EnvironmentApplyOptions struct {
	Operator EnvironmentOperator
}

func (c Core) PlanEnvironmentStop(opts EnvironmentActionOptions) (EnvironmentActionPlan, error) {
	return c.planEnvironmentAction(EnvironmentActionStop, opts)
}

func (c Core) ApplyEnvironmentStop(ctx context.Context, plan EnvironmentActionPlan, opts EnvironmentApplyOptions) (EnvironmentActionResult, error) {
	if plan.Action != EnvironmentActionStop {
		return EnvironmentActionResult{Plan: plan}, fmt.Errorf("environment action plan is %q, not %q", plan.Action, EnvironmentActionStop)
	}
	store, err := c.environmentStore()
	if err != nil {
		return EnvironmentActionResult{Plan: plan}, err
	}
	operator := environmentOperatorOrDefault(opts.Operator)
	result := EnvironmentActionResult{Plan: plan, Applied: []EnvironmentActionTarget{}, Skipped: append([]EnvironmentActionTarget(nil), plan.Skipped...)}
	for _, target := range plan.Targets {
		c.emitOperation("environment", "start", environmentOperationDetails(EnvironmentActionStop, target, "running"))
		applied, skipped, err := applyEnvironmentStopTarget(ctx, store, operator, target.ID)
		if err != nil {
			c.emitOperation("environment", "failed", environmentOperationDetails(EnvironmentActionStop, target, "failed"))
			return result, err
		}
		if skipped.Reason != "" {
			result.Skipped = append(result.Skipped, skipped)
			c.emitOperation("environment", "complete", environmentOperationDetails(EnvironmentActionStop, skipped, "skipped"))
			continue
		}
		result.Applied = append(result.Applied, applied)
		c.emitOperation("environment", "complete", environmentOperationDetails(EnvironmentActionStop, applied, applied.Status))
	}
	return result, nil
}

func (c Core) PlanEnvironmentClean(opts EnvironmentActionOptions) (EnvironmentActionPlan, error) {
	return c.planEnvironmentAction(EnvironmentActionClean, opts)
}

func (c Core) ApplyEnvironmentClean(ctx context.Context, plan EnvironmentActionPlan, opts EnvironmentApplyOptions) (EnvironmentActionResult, error) {
	if plan.Action != EnvironmentActionClean {
		return EnvironmentActionResult{Plan: plan}, fmt.Errorf("environment action plan is %q, not %q", plan.Action, EnvironmentActionClean)
	}
	store, err := c.environmentStore()
	if err != nil {
		return EnvironmentActionResult{Plan: plan}, err
	}
	operator := environmentOperatorOrDefault(opts.Operator)
	result := EnvironmentActionResult{Plan: plan, Applied: []EnvironmentActionTarget{}, Skipped: append([]EnvironmentActionTarget(nil), plan.Skipped...)}
	for _, target := range plan.Targets {
		c.emitOperation("environment", "start", environmentOperationDetails(EnvironmentActionClean, target, "running"))
		applied, err := applyEnvironmentCleanTarget(ctx, store, operator, target.ID)
		if err != nil {
			c.emitOperation("environment", "failed", environmentOperationDetails(EnvironmentActionClean, target, "failed"))
			return result, err
		}
		result.Applied = append(result.Applied, applied)
		c.emitOperation("environment", "complete", environmentOperationDetails(EnvironmentActionClean, applied, "removed"))
	}
	return result, nil
}

func environmentOperationDetails(action string, target EnvironmentActionTarget, status string) map[string]any {
	details := map[string]any{
		"action":         string(action),
		"id":             target.ID,
		"profile":        target.Profile,
		"backend":        target.Backend,
		"status":         status,
		"workspace":      target.Workspace,
		"guestWorkspace": target.GuestWorkspace,
		"instanceName":   target.InstanceName,
		"lastSessionId":  target.LastSessionID,
		"lastCommand":    target.LastCommand,
		"createdAt":      target.CreatedAt,
		"lastStartedAt":  target.LastStartedAt,
		"lastEndedAt":    target.LastEndedAt,
	}
	if details["status"] == "" {
		details["status"] = target.Status
	}
	if target.Reason != "" {
		details["reason"] = target.Reason
	}
	return details
}

func (c Core) planEnvironmentAction(action string, opts EnvironmentActionOptions) (EnvironmentActionPlan, error) {
	store, err := c.environmentStore()
	if err != nil {
		return EnvironmentActionPlan{Action: action}, err
	}
	records, err := selectEnvironmentRecords(store, opts.IDs)
	if err != nil {
		return EnvironmentActionPlan{Action: action}, err
	}
	records = filterEnvironmentActionRecords(records, opts)
	plan := EnvironmentActionPlan{
		Action:       action,
		RequestedIDs: cleanEnvironmentIDs(opts.IDs),
		Filter:       environmentActionFilter(opts),
		Targets:      []EnvironmentActionTarget{},
		Skipped:      []EnvironmentActionTarget{},
		Total:        len(records),
	}
	for _, rec := range records {
		target := environmentActionTargetFromRecord(rec, "")
		switch action {
		case EnvironmentActionStop:
			switch {
			case rec.Status == environment.StatusCreated:
				target.Reason = "never-booted"
				plan.Skipped = append(plan.Skipped, target)
			case rec.Status == "stopped":
				target.Reason = "already-stopped"
				plan.Skipped = append(plan.Skipped, target)
			case rec.Backend != "lima" || strings.TrimSpace(rec.InstanceName) == "":
				target.Reason = "no-lima-instance"
				plan.Skipped = append(plan.Skipped, target)
			default:
				plan.Targets = append(plan.Targets, target)
			}
		case EnvironmentActionClean:
			plan.Targets = append(plan.Targets, target)
		default:
			return plan, fmt.Errorf("unknown environment action %q", action)
		}
	}
	return plan, nil
}

func (c Core) environmentStore() (environment.Store, error) {
	if c.Store.Root == "" {
		return environment.Store{}, errors.New("manager store root is required")
	}
	return environment.Store{Root: c.Store.Root}, nil
}

func selectEnvironmentRecords(store environment.Store, ids []string) ([]environment.Record, error) {
	ids = cleanEnvironmentIDs(ids)
	if len(ids) == 0 {
		return store.List()
	}
	records := make([]environment.Record, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		rec, err := loadByNameOrID(store, id)
		if err != nil {
			return nil, err
		}
		if seen[rec.ID] {
			continue
		}
		seen[rec.ID] = true
		records = append(records, rec)
	}
	return records, nil
}

// loadByNameOrID resolves an operator-supplied handle: environment names win,
// record ids (and unique prefixes) remain accepted for cleanup ergonomics.
func loadByNameOrID(store environment.Store, handle string) (environment.Record, error) {
	rec, err := store.LoadByName(handle)
	if err == nil {
		return rec, nil
	}
	if !errors.Is(err, environment.ErrNameNotFound) {
		return environment.Record{}, err
	}
	return store.Load(handle)
}

func cleanEnvironmentIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func filterEnvironmentActionRecords(records []environment.Record, opts EnvironmentActionOptions) []environment.Record {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := records[:0]
	for _, rec := range records {
		if opts.StoppedOnly && rec.Status != "stopped" {
			continue
		}
		if opts.IdleSet {
			if rec.Status == "running" || rec.LastEndedAt.IsZero() || now.Sub(rec.LastEndedAt) < opts.Idle {
				continue
			}
		}
		out = append(out, rec)
	}
	return out
}

func environmentActionFilter(opts EnvironmentActionOptions) EnvironmentActionFilter {
	filter := EnvironmentActionFilter{StoppedOnly: opts.StoppedOnly}
	if opts.IdleSet {
		filter.Idle = opts.Idle.String()
	}
	return filter
}

func environmentActionTargetFromRecord(rec environment.Record, reason string) EnvironmentActionTarget {
	return EnvironmentActionTarget{
		ID:             rec.ID,
		Profile:        rec.Profile,
		Backend:        rec.Backend,
		Status:         rec.Status,
		Workspace:      rec.Workspace,
		GuestWorkspace: rec.GuestWorkspace,
		InstanceName:   rec.InstanceName,
		LastSessionID:  rec.LastSessionID,
		LastCommand:    rec.LastCommand,
		CreatedAt:      rec.CreatedAt,
		LastStartedAt:  rec.LastStartedAt,
		LastEndedAt:    rec.LastEndedAt,
		Reason:         reason,
	}
}

func applyEnvironmentStopTarget(ctx context.Context, store environment.Store, operator EnvironmentOperator, id string) (EnvironmentActionTarget, EnvironmentActionTarget, error) {
	lock, err := store.Lock(id)
	if err != nil {
		return EnvironmentActionTarget{}, EnvironmentActionTarget{}, err
	}
	defer lock.Unlock()
	rec, err := store.Load(id)
	if err != nil {
		return EnvironmentActionTarget{}, EnvironmentActionTarget{}, err
	}
	switch {
	case rec.Status == environment.StatusCreated:
		return EnvironmentActionTarget{}, environmentActionTargetFromRecord(rec, "never-booted"), nil
	case rec.Status == "stopped":
		return EnvironmentActionTarget{}, environmentActionTargetFromRecord(rec, "already-stopped"), nil
	case rec.Backend != "lima" || strings.TrimSpace(rec.InstanceName) == "":
		return EnvironmentActionTarget{}, environmentActionTargetFromRecord(rec, "no-lima-instance"), nil
	}
	if err := operator.StopInstance(ctx, rec.InstanceName); err != nil {
		return EnvironmentActionTarget{}, EnvironmentActionTarget{}, err
	}
	rec.Status = "stopped"
	if err := store.Save(rec); err != nil {
		return EnvironmentActionTarget{}, EnvironmentActionTarget{}, err
	}
	return environmentActionTargetFromRecord(rec, ""), EnvironmentActionTarget{}, nil
}

func applyEnvironmentCleanTarget(ctx context.Context, store environment.Store, operator EnvironmentOperator, id string) (EnvironmentActionTarget, error) {
	lock, err := store.Lock(id)
	if err != nil {
		return EnvironmentActionTarget{}, err
	}
	defer lock.Unlock()
	rec, err := store.Load(id)
	if err != nil {
		return EnvironmentActionTarget{}, err
	}
	target := environmentActionTargetFromRecord(rec, "")
	if rec.Backend == "lima" && strings.TrimSpace(rec.InstanceName) != "" {
		if err := operator.Cleanup(ctx, &backend.Session{InstanceName: rec.InstanceName}); err != nil {
			return EnvironmentActionTarget{}, err
		}
	}
	if err := store.Remove(rec.ID); err != nil {
		return EnvironmentActionTarget{}, err
	}
	return target, nil
}

func environmentOperatorOrDefault(operator EnvironmentOperator) EnvironmentOperator {
	if operator != nil {
		return operator
	}
	return lima.Backend{Stdout: io.Discard, Stderr: io.Discard}
}

// EnvironmentCreateOptions are the inputs for creating a named environment.
// The record is written without booting a guest and without any network
// activity; the first run boots it.
type EnvironmentCreateOptions struct {
	Name            string
	ImageRef        string
	Profile         string
	Backend         string
	Workspace       string
	GuestWorkspace  string
	AutoNamed       bool
	RuntimeFamily   string
	RuntimeRevision string
}

const EnvironmentCreatePlanVersion = "hideout.environment-create-plan/v1"

type EnvironmentCreatePlan struct {
	Version              string                         `json:"version"`
	Name                 string                         `json:"name"`
	AutoNamed            bool                           `json:"autoNamed,omitempty"`
	Profile              string                         `json:"profile"`
	ProfileWillCreate    bool                           `json:"profileWillCreate,omitempty"`
	Backend              string                         `json:"backend"`
	Workspace            string                         `json:"workspace"`
	GuestWorkspace       string                         `json:"guestWorkspace"`
	ImageRef             string                         `json:"imageRef"`
	ImageSource          string                         `json:"imageSource"`
	Runtime              *environment.RuntimeProvenance `json:"runtime,omitempty"`
	RuntimeDownloadBytes int64                          `json:"runtimeDownloadBytes,omitempty"`
	RuntimeVirtualBytes  int64                          `json:"runtimeVirtualBytes,omitempty"`
}

// PlanEnvironmentCreate resolves immutable inputs without writing profile or
// environment state. Apply revalidates every mutable dependency.
func (c Core) PlanEnvironmentCreate(opts EnvironmentCreateOptions) (EnvironmentCreatePlan, error) {
	if _, err := c.environmentStore(); err != nil {
		return EnvironmentCreatePlan{}, err
	}
	profileName := strings.TrimSpace(opts.Profile)
	if profileName == "" {
		profileName = "default"
	}
	if err := profile.ValidateName(profileName); err != nil {
		return EnvironmentCreatePlan{}, err
	}
	if err := environment.ValidateName(opts.Name); err != nil {
		return EnvironmentCreatePlan{}, err
	}
	backendName := ResolveBackendName(opts.Backend)
	imageRef := strings.TrimSpace(opts.ImageRef)
	runtimeFamily := strings.TrimSpace(opts.RuntimeFamily)
	if imageRef != "" && runtimeFamily != "" {
		return EnvironmentCreatePlan{}, errors.New("--runtime and --image are mutually exclusive")
	}
	if runtimeFamily != "" && backendName != "lima" {
		return EnvironmentCreatePlan{}, errors.New("catalog runtimes require the Lima backend")
	}
	p, err := c.Store.Load(profileName)
	profileWillCreate := false
	if errors.Is(err, os.ErrNotExist) {
		p = profile.Default(profileName)
		profileWillCreate = true
	} else if err != nil {
		return EnvironmentCreatePlan{}, err
	}
	var runtimeProvenance *environment.RuntimeProvenance
	var runtimeArtifact *runtimecatalog.Artifact
	imageSource := "explicit"
	if runtimeFamily != "" {
		resolver := c.RuntimeResolver
		if resolver == nil {
			resolver = runtimecatalog.ResolveEmbedded
		}
		resolved, err := resolver(runtimecatalog.Selection{
			Family: runtimeFamily, Revision: strings.TrimSpace(opts.RuntimeRevision), HostOS: runtime.GOOS, HostArch: runtime.GOARCH,
		})
		if err != nil {
			return EnvironmentCreatePlan{}, fmt.Errorf("resolve runtime %q: %w", runtimeFamily, err)
		}
		imageRef = resolved.ImageRef
		imageSource = "runtime-explicit"
		provenance := resolved.Provenance
		runtimeProvenance = &provenance
		artifact := resolved.Artifact
		runtimeArtifact = &artifact
	} else if imageRef == "" {
		imageRef = p.BaseImageOrBuiltin()
		imageSource = "profile"
		if p.Environment.Runtime != nil {
			resolver := c.RuntimeResolver
			if resolver == nil {
				resolver = runtimecatalog.ResolveEmbedded
			}
			resolved, err := resolver(runtimecatalog.Selection{
				Family: p.Environment.Runtime.Family, Revision: p.Environment.Runtime.Revision, HostOS: runtime.GOOS, HostArch: runtime.GOARCH,
			})
			if err != nil || resolved.Provenance != *p.Environment.Runtime || resolved.ImageRef != imageRef {
				return EnvironmentCreatePlan{}, errors.New("profile runtime provenance no longer matches the package catalog; existing environments remain pinned but a new environment cannot be created")
			}
			imageSource = "runtime-profile"
			provenance := resolved.Provenance
			runtimeProvenance = &provenance
			artifact := resolved.Artifact
			runtimeArtifact = &artifact
		}
	}
	if _, err := environment.ParseImageDeclaration(imageRef); err != nil {
		return EnvironmentCreatePlan{}, err
	}
	workspace, guestWorkspace, err := ResolveWorkspaceMapping(opts.Workspace, opts.GuestWorkspace, p)
	if err != nil {
		return EnvironmentCreatePlan{}, err
	}
	if err := ValidateWorkspaceMountSafety(workspace, c.Store.Root); err != nil {
		return EnvironmentCreatePlan{}, err
	}
	if runtimeArtifact != nil {
		if err := c.checkRuntimeDisk(*runtimeArtifact); err != nil {
			return EnvironmentCreatePlan{}, err
		}
	}
	plan := EnvironmentCreatePlan{
		Version: EnvironmentCreatePlanVersion, Name: opts.Name, AutoNamed: opts.AutoNamed,
		Profile: profileName, ProfileWillCreate: profileWillCreate, Backend: backendName,
		Workspace: workspace, GuestWorkspace: guestWorkspace, ImageRef: imageRef,
		ImageSource: imageSource, Runtime: runtimeProvenance,
	}
	if runtimeArtifact != nil {
		plan.RuntimeDownloadBytes = runtimeArtifact.DownloadBytes
		plan.RuntimeVirtualBytes = runtimeArtifact.VirtualBytes
	}
	return plan, nil
}

func (c Core) ApplyEnvironmentCreate(plan EnvironmentCreatePlan) (environment.Record, error) {
	if plan.Version != EnvironmentCreatePlanVersion {
		return environment.Record{}, fmt.Errorf("unsupported environment create plan %q", plan.Version)
	}
	store, err := c.environmentStore()
	if err != nil {
		return environment.Record{}, err
	}
	if _, err := environment.ParseImageDeclaration(plan.ImageRef); err != nil {
		return environment.Record{}, err
	}
	if plan.Runtime != nil {
		resolver := c.RuntimeResolver
		if resolver == nil {
			resolver = runtimecatalog.ResolveEmbedded
		}
		resolved, err := resolver(runtimecatalog.Selection{
			Family: plan.Runtime.Family, Revision: plan.Runtime.Revision, HostOS: runtime.GOOS, HostArch: runtime.GOARCH,
		})
		if err != nil || resolved.Provenance != *plan.Runtime || resolved.ImageRef != plan.ImageRef || resolved.Artifact.DownloadBytes != plan.RuntimeDownloadBytes || resolved.Artifact.VirtualBytes != plan.RuntimeVirtualBytes {
			return environment.Record{}, errors.New("runtime catalog changed between environment plan and apply")
		}
		if err := c.checkRuntimeDisk(resolved.Artifact); err != nil {
			return environment.Record{}, err
		}
	}
	p, err := c.Store.Load(plan.Profile)
	if plan.ProfileWillCreate {
		if err == nil {
			return environment.Record{}, errors.New("profile appeared between environment plan and apply")
		}
		if !errors.Is(err, os.ErrNotExist) {
			return environment.Record{}, err
		}
		p = profile.Default(plan.Profile)
		if err := c.Store.Create(p); err != nil {
			return environment.Record{}, err
		}
		p, err = c.Store.Load(plan.Profile)
	} else if err != nil {
		return environment.Record{}, err
	}
	if err != nil {
		return environment.Record{}, err
	}
	workspace, guestWorkspace, err := ResolveWorkspaceMapping(plan.Workspace, plan.GuestWorkspace, p)
	if err != nil || workspace != plan.Workspace || guestWorkspace != plan.GuestWorkspace {
		return environment.Record{}, errors.New("profile workspace mapping changed between environment plan and apply")
	}
	if plan.ImageSource == "profile" || plan.ImageSource == "runtime-profile" {
		if p.BaseImageOrBuiltin() != plan.ImageRef {
			return environment.Record{}, errors.New("profile image changed between environment plan and apply")
		}
		if (plan.Runtime == nil && p.Environment.Runtime != nil) || (plan.Runtime != nil && (p.Environment.Runtime == nil || *p.Environment.Runtime != *plan.Runtime)) {
			return environment.Record{}, errors.New("profile runtime changed between environment plan and apply")
		}
	}
	spec := RunEnvironmentSpec(p, plan.Backend, workspace, guestWorkspace)
	spec.Name = plan.Name
	spec.AutoNamed = plan.AutoNamed
	spec.ImageRef = plan.ImageRef
	if plan.Runtime != nil {
		provenance := *plan.Runtime
		spec.Runtime = &provenance
	} else {
		spec.Runtime = nil
	}
	rec, err := store.Create(spec)
	if err != nil {
		return environment.Record{}, err
	}
	if plan.Backend == "lima" {
		rec.InstanceName = lima.InstanceNameForEnvironment(p.Name, rec.ID)
		if err := store.Save(rec); err != nil {
			return environment.Record{}, err
		}
	}
	c.emitEnvironmentAudit("env.create", "allow", map[string]any{
		"environmentName": rec.Name,
		"environmentId":   rec.ID,
		"autoNamed":       rec.AutoNamed,
		"imageRef":        rec.ImageRef,
		"workspace":       rec.Workspace,
		"backend":         rec.Backend,
		"profile":         rec.Profile,
	})
	return rec, nil
}

// CreateEnvironment preserves the existing direct Core API while enforcing
// the typed plan/apply contract internally.
func (c Core) CreateEnvironment(opts EnvironmentCreateOptions) (environment.Record, error) {
	plan, err := c.PlanEnvironmentCreate(opts)
	if err != nil {
		return environment.Record{}, err
	}
	return c.ApplyEnvironmentCreate(plan)
}

func (c Core) checkRuntimeDisk(artifact runtimecatalog.Artifact) error {
	return c.checkRuntimeDiskSizes(artifact.DownloadBytes, artifact.VirtualBytes)
}

func (c Core) checkRuntimeDiskProvenance(provenance environment.RuntimeProvenance) error {
	return c.checkRuntimeDiskSizes(provenance.DownloadBytes, provenance.VirtualBytes)
}

func (c Core) checkRuntimeDiskSizes(downloadBytes, virtualBytes int64) error {
	const workingBytes = int64(1 << 30)
	required := downloadBytes + virtualBytes + workingBytes
	root, err := limaDataRoot()
	if err != nil {
		return fmt.Errorf("runtime.disk.insufficient: cannot determine Lima data root: %w", err)
	}
	checker := c.RuntimeDiskCheck
	if checker == nil {
		checker = requireFreeBytes
	}
	if err := checker(root, required); err != nil {
		return fmt.Errorf("runtime.disk.insufficient: need %d bytes on Lima data filesystem: %w", required, err)
	}
	return nil
}

func limaDataRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("LIMA_HOME")); root != "" {
		if !filepath.IsAbs(root) {
			return "", errors.New("LIMA_HOME must be absolute")
		}
		return root, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lima"), nil
}

func requireFreeBytes(path string, required int64) error {
	probe := filepath.Clean(path)
	for {
		if _, err := os.Stat(probe); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return errors.New("no existing ancestor for disk probe")
		}
		probe = parent
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(probe, &stat); err != nil {
		return err
	}
	available := int64(stat.Bavail) * int64(stat.Bsize)
	if available < required {
		return fmt.Errorf("available %d bytes", available)
	}
	return nil
}

// EnvironmentByName resolves a named environment record.
func (c Core) EnvironmentByName(name string) (environment.Record, error) {
	store, err := c.environmentStore()
	if err != nil {
		return environment.Record{}, err
	}
	return store.LoadByName(name)
}

// RemoveEnvironment tears down a named environment's guest and record. A
// running guest fails closed with a copyable stop command; the explicit force
// flag stops the guest first and then proceeds.
func (c Core) RemoveEnvironment(ctx context.Context, name string, force bool, opts EnvironmentApplyOptions) (environment.Record, error) {
	store, err := c.environmentStore()
	if err != nil {
		return environment.Record{}, err
	}
	rec, err := store.LoadByName(name)
	if err != nil {
		return environment.Record{}, err
	}
	operator := environmentOperatorOrDefault(opts.Operator)
	if rec, err = stopIfRunning(ctx, store, operator, rec, force, "remove"); err != nil {
		return environment.Record{}, err
	}
	if _, err := applyEnvironmentCleanTarget(ctx, store, operator, rec.ID); err != nil {
		return environment.Record{}, err
	}
	c.emitEnvironmentAudit("env.remove", "allow", map[string]any{
		"environmentName": rec.Name,
		"environmentId":   rec.ID,
		"imageRef":        rec.ImageRef,
		"workspace":       rec.Workspace,
		"backend":         rec.Backend,
		"force":           force,
	})
	return rec, nil
}

// RecreateEnvironment destroys the environment's guest and rebuilds the
// record from its pinned declaration under the same name and id, refreshing
// the backend configuration version and profile identity fields to current
// values. It is the explicit answer to a drift report.
func (c Core) RecreateEnvironment(ctx context.Context, name string, force bool, opts EnvironmentApplyOptions) (environment.Record, error) {
	store, err := c.environmentStore()
	if err != nil {
		return environment.Record{}, err
	}
	rec, err := store.LoadByName(name)
	if err != nil {
		return environment.Record{}, err
	}
	operator := environmentOperatorOrDefault(opts.Operator)
	if rec, err = stopIfRunning(ctx, store, operator, rec, force, "recreate"); err != nil {
		return environment.Record{}, err
	}
	if rec.Backend == "lima" && strings.TrimSpace(rec.InstanceName) != "" {
		if err := operator.Cleanup(ctx, &backend.Session{InstanceName: rec.InstanceName}); err != nil {
			return environment.Record{}, err
		}
	}
	p, err := c.Store.LoadOrInit(rec.Profile)
	if err != nil {
		return environment.Record{}, err
	}
	rec.BackendConfigVersion = backendConfigVersion(rec.Backend)
	rec.ProfileID = p.Metadata["profileId"]
	rec.IdentityID = p.Metadata["identityId"]
	rec.User = p.Identity.User
	rec.Hostname = p.Identity.Hostname
	rec.Status = "ready"
	rec.LastSessionID = ""
	rec.LastCommand = ""
	if err := store.Save(rec); err != nil {
		return environment.Record{}, err
	}
	if err := store.ClearRuntime(rec.ID); err != nil {
		return environment.Record{}, err
	}
	c.emitEnvironmentAudit("env.recreate", "allow", map[string]any{
		"environmentName": rec.Name,
		"environmentId":   rec.ID,
		"imageRef":        rec.ImageRef,
		"workspace":       rec.Workspace,
		"backend":         rec.Backend,
		"force":           force,
	})
	return rec, nil
}

// stopIfRunning enforces the destructive-command guard: refuse a running
// guest with a copyable stop hint, or stop it first under the explicit force
// flag.
func stopIfRunning(ctx context.Context, store environment.Store, operator EnvironmentOperator, rec environment.Record, force bool, verb string) (environment.Record, error) {
	if rec.Status != "running" {
		return rec, nil
	}
	if !force {
		return rec, fmt.Errorf("environment %q is running; stop it first: hideout stop %s (or pass --force to %s)", rec.Name, rec.Name, verb)
	}
	if rec.Backend == "lima" && strings.TrimSpace(rec.InstanceName) != "" {
		if err := operator.StopInstance(ctx, rec.InstanceName); err != nil {
			return rec, err
		}
	}
	rec.Status = "stopped"
	if err := store.Save(rec); err != nil {
		return rec, err
	}
	return rec, nil
}

// emitEnvironmentAudit appends an environment lifecycle event to the
// store-level environment audit log. Lifecycle events happen outside run
// sessions, so they get their own file under logs/.
func (c Core) emitEnvironmentAudit(action, decision string, details map[string]any) {
	if c.Store.Root == "" {
		return
	}
	dir := filepath.Join(c.Store.Root, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	aw, err := audit.NewFile(filepath.Join(dir, "environment-audit.jsonl"))
	if err != nil {
		return
	}
	defer aw.Close()
	_ = aw.Emit(audit.Event{
		Action:   action,
		Decision: decision,
		Details:  details,
	})
}
