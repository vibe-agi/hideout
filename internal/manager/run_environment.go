package manager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
)

const limaBackendConfigVersion = "lima-config/v3/no-default-port-forwards"

type RunEnvironmentOptions struct {
	EnvName        string
	RemoveAfterRun bool
	Create         bool
}

type RunEnvironment struct {
	Active           bool
	Record           environment.Record
	RuntimeDir       string
	ShimDir          string
	InstanceName     string
	PreserveInstance bool
	RemoveAfterRun   bool
	Created          bool
}

func (c Core) SelectRunEnvironment(plan RunPlan, opts RunEnvironmentOptions) (RunEnvironment, error) {
	if c.Store.Root == "" {
		return RunEnvironment{}, errors.New("manager store root is required")
	}
	runEnv, err := SelectRunEnvironment(environment.Store{Root: c.Store.Root}, plan.RuntimeProfile, plan.Backend, plan.Workspace, plan.GuestWorkspace, plan.Ephemeral, opts)
	if err == nil && runEnv.Created && runEnv.Record.ID != "env_new" {
		c.emitEnvironmentAudit("env.create", "allow", map[string]any{
			"environmentName": runEnv.Record.Name,
			"environmentId":   runEnv.Record.ID,
			"autoNamed":       runEnv.Record.AutoNamed,
			"imageRef":        runEnv.Record.ImageRef,
			"workspace":       runEnv.Record.Workspace,
			"backend":         runEnv.Record.Backend,
			"profile":         runEnv.Record.Profile,
		})
	}
	var drift *DriftError
	if errors.As(err, &drift) {
		c.emitEnvironmentAudit("env.drift.denied", "deny", map[string]any{
			"environmentName": drift.Environment,
			"axes":            drift.Axes,
		})
	}
	return runEnv, err
}

func SelectRunEnvironment(store environment.Store, p profile.Profile, backendName, workspace, guestWorkspace string, ephemeral bool, opts RunEnvironmentOptions) (RunEnvironment, error) {
	if name := strings.TrimSpace(opts.EnvName); name != "" {
		if ephemeral {
			return RunEnvironment{}, errors.New("--ephemeral cannot be combined with --env; ephemeral runs are record-less")
		}
		if opts.RemoveAfterRun {
			return RunEnvironment{}, errors.New("--rm cannot be combined with --env; disposable runs are record-less")
		}
		rec, err := store.LoadByName(name)
		if err != nil {
			return RunEnvironment{}, err
		}
		spec := RunEnvironmentSpec(p, backendName, workspace, guestWorkspace)
		if err := ValidateEnvironmentRecord(rec, spec); err != nil {
			return RunEnvironment{}, err
		}
		return selectedEnvironmentWithInstance(store, rec, p, opts.Create)
	}
	if ephemeral || opts.RemoveAfterRun {
		// Disposable sessions stay record-less by contract.
		return RunEnvironment{}, nil
	}
	spec := RunEnvironmentSpec(p, backendName, workspace, guestWorkspace)
	rec, err := store.LoadByName(spec.Name)
	switch {
	case err == nil:
		if err := ValidateEnvironmentRecord(rec, spec); err != nil {
			return RunEnvironment{}, err
		}
		return selectedEnvironmentWithInstance(store, rec, p, opts.Create)
	case !errors.Is(err, environment.ErrNameNotFound):
		return RunEnvironment{}, err
	}
	if !opts.Create {
		rec := environment.Record{
			ID:                   "env_new",
			Name:                 spec.Name,
			AutoNamed:            spec.AutoNamed,
			ImageRef:             spec.ImageRef,
			Profile:              spec.Profile,
			Backend:              spec.Backend,
			BackendConfigVersion: spec.BackendConfigVersion,
			Workspace:            spec.Workspace,
			GuestWorkspace:       spec.GuestWorkspace,
			ProfileID:            spec.ProfileID,
			IdentityID:           spec.IdentityID,
			User:                 spec.User,
			Hostname:             spec.Hostname,
			Status:               "new",
		}
		if spec.Backend == "lima" {
			rec.InstanceName = lima.InstanceNameForEnvironment(p.Name, "env_new")
		}
		return selectedRunEnvironment(store, rec, true, false, true), nil
	}
	created, err := store.Create(spec)
	if err != nil {
		return RunEnvironment{}, err
	}
	if created.Backend == "lima" {
		created.InstanceName = lima.InstanceNameForEnvironment(p.Name, created.ID)
		if err := store.Save(created); err != nil {
			return RunEnvironment{}, err
		}
	}
	return selectedRunEnvironment(store, created, true, false, true), nil
}

func selectedEnvironmentWithInstance(store environment.Store, rec environment.Record, p profile.Profile, persist bool) (RunEnvironment, error) {
	if rec.Backend == "lima" && rec.InstanceName == "" {
		rec.InstanceName = lima.InstanceNameForEnvironment(p.Name, rec.ID)
		if persist {
			if err := store.Save(rec); err != nil {
				return RunEnvironment{}, err
			}
		}
	}
	return selectedRunEnvironment(store, rec, true, false, false), nil
}

func (c Core) PrepareRunEnvironment(runEnv RunEnvironment) error {
	if !runEnv.Active {
		return nil
	}
	return environment.Store{Root: c.Store.Root}.PrepareRuntime(runEnv.Record.ID)
}

func (c Core) StartRunEnvironment(runEnv RunEnvironment, sessionID string, command []string) (RunEnvironment, error) {
	if !runEnv.Active {
		return runEnv, nil
	}
	rec := runEnv.Record
	rec.Status = "running"
	rec.LastStartedAt = time.Now().UTC()
	rec.LastSessionID = sessionID
	rec.LastCommand = strings.Join(command, " ")
	if err := (environment.Store{Root: c.Store.Root}).Save(rec); err != nil {
		return runEnv, err
	}
	runEnv.Record = rec
	return runEnv, nil
}

func (c Core) FinishRunEnvironment(runEnv RunEnvironment, cleanupErr error) (RunEnvironment, error) {
	if !runEnv.Active {
		return runEnv, cleanupErr
	}
	store := environment.Store{Root: c.Store.Root}
	if err := store.ClearRuntime(runEnv.Record.ID); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("environment runtime cleanup: %w", err))
	}
	rec := runEnv.Record
	rec.LastEndedAt = time.Now().UTC()
	if cleanupErr != nil {
		rec.Status = "error"
	} else {
		rec.Status = "ready"
	}
	if runEnv.RemoveAfterRun && cleanupErr == nil {
		if err := store.Remove(runEnv.Record.ID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("environment remove: %w", err))
		}
	} else if err := store.Save(rec); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("environment save: %w", err))
	}
	runEnv.Record = rec
	return runEnv, cleanupErr
}

func RunEnvironmentSpec(p profile.Profile, backendName, workspace, guestWorkspace string) environment.Spec {
	return environment.Spec{
		Name:                 environment.AutoName(p.Name, workspace),
		AutoNamed:            true,
		ImageRef:             p.BaseImageOrBuiltin(),
		Profile:              p.Name,
		Backend:              backendName,
		BackendConfigVersion: backendConfigVersion(backendName),
		Workspace:            workspace,
		GuestWorkspace:       guestWorkspace,
		ProfileID:            p.Metadata["profileId"],
		IdentityID:           p.Metadata["identityId"],
		User:                 p.Identity.User,
		Hostname:             p.Identity.Hostname,
	}
}

func backendConfigVersion(backendName string) string {
	if backendName == "lima" {
		return limaBackendConfigVersion
	}
	return ""
}

// DriftAxis names one drifted identity input with its pinned and current
// values (verbatim operator data).
type DriftAxis struct {
	Axis    string `json:"axis"`
	Pinned  string `json:"pinned"`
	Current string `json:"current"`
}

// DriftError is the fail-closed use-time drift report: backend configuration
// and pinned workspace are the two drift axes. It never triggers a rebuild.
type DriftError struct {
	Environment string
	Axes        []DriftAxis
}

func (e *DriftError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "environment %q drifted from its pinned identity:", e.Environment)
	for _, axis := range e.Axes {
		fmt.Fprintf(&b, "\n  %s: pinned=%q current=%q", axis.Axis, axis.Pinned, axis.Current)
	}
	fmt.Fprintf(&b, "\nrecreate it: hideout env recreate %s", e.Environment)
	return b.String()
}

func ValidateEnvironmentRecord(rec environment.Record, spec environment.Spec) error {
	if _, err := environment.ParseImageDeclaration(rec.ImageRef); err != nil {
		return fmt.Errorf("environment %q has an unusable pinned image declaration (%v); clean and recreate it: hideout env remove %s", rec.Name, err, rec.Name)
	}
	if rec.Profile != spec.Profile {
		return fmt.Errorf("environment %s belongs to profile %q, not %q", rec.ID, rec.Profile, spec.Profile)
	}
	if rec.Backend != spec.Backend {
		return fmt.Errorf("environment %s uses backend %q, not %q", rec.ID, rec.Backend, spec.Backend)
	}
	if rec.ProfileID != spec.ProfileID || rec.IdentityID != spec.IdentityID || rec.User != spec.User || rec.Hostname != spec.Hostname {
		return fmt.Errorf("environment %q identity no longer matches the selected profile; recreate it: hideout env recreate %s", rec.Name, rec.Name)
	}
	var axes []DriftAxis
	if rec.BackendConfigVersion != spec.BackendConfigVersion {
		axes = append(axes, DriftAxis{Axis: "backendConfig", Pinned: rec.BackendConfigVersion, Current: spec.BackendConfigVersion})
	}
	if !sameWorkspaceIdentity(rec.Workspace, spec.Workspace) ||
		filepath.Clean(rec.GuestWorkspace) != filepath.Clean(spec.GuestWorkspace) {
		axes = append(axes, DriftAxis{
			Axis:    "workspace",
			Pinned:  rec.Workspace + " -> " + rec.GuestWorkspace,
			Current: spec.Workspace + " -> " + spec.GuestWorkspace,
		})
	}
	if len(axes) > 0 {
		return &DriftError{Environment: rec.Name, Axes: axes}
	}
	return nil
}

// sameWorkspaceIdentity compares workspaces by real file identity, not string
// paths: a symlink or case variant of the pinned workspace is the same
// workspace.
func sameWorkspaceIdentity(pinned, current string) bool {
	if filepath.Clean(pinned) == filepath.Clean(current) {
		return true
	}
	pi, err1 := os.Stat(pinned)
	ci, err2 := os.Stat(current)
	return err1 == nil && err2 == nil && os.SameFile(pi, ci)
}

func selectedRunEnvironment(store environment.Store, rec environment.Record, preserve, removeAfterRun, created bool) RunEnvironment {
	return RunEnvironment{
		Active:           true,
		Record:           rec,
		RuntimeDir:       store.RuntimeDir(rec.ID),
		ShimDir:          store.ShimDir(rec.ID),
		InstanceName:     rec.InstanceName,
		PreserveInstance: preserve,
		RemoveAfterRun:   removeAfterRun,
		Created:          created,
	}
}
