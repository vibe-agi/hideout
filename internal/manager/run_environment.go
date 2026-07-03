package manager

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
)

type RunEnvironmentOptions struct {
	New            bool
	ResumeID       string
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
	return SelectRunEnvironment(environment.Store{Root: c.Store.Root}, plan.RuntimeProfile, plan.Backend, plan.Workspace, plan.GuestWorkspace, plan.Ephemeral, opts)
}

func SelectRunEnvironment(store environment.Store, p profile.Profile, backendName, workspace, guestWorkspace string, ephemeral bool, opts RunEnvironmentOptions) (RunEnvironment, error) {
	if backendName != "lima" {
		if opts.New || strings.TrimSpace(opts.ResumeID) != "" {
			return RunEnvironment{}, fmt.Errorf("environment reuse is only supported by the lima backend; got %s", backendName)
		}
		return RunEnvironment{}, nil
	}
	if ephemeral {
		return RunEnvironment{}, nil
	}
	spec := RunEnvironmentSpec(p, backendName, workspace, guestWorkspace)
	if opts.RemoveAfterRun && strings.TrimSpace(opts.ResumeID) == "" {
		return RunEnvironment{}, nil
	}
	if opts.ResumeID != "" {
		rec, err := store.Load(opts.ResumeID)
		if err != nil {
			return RunEnvironment{}, err
		}
		if err := ValidateEnvironmentRecord(rec, spec); err != nil {
			return RunEnvironment{}, err
		}
		if rec.InstanceName == "" {
			rec.InstanceName = lima.InstanceNameForEnvironment(p.Name, rec.ID)
			if opts.Create {
				if err := store.Save(rec); err != nil {
					return RunEnvironment{}, err
				}
			}
		}
		return selectedRunEnvironment(store, rec, !opts.RemoveAfterRun, opts.RemoveAfterRun, false), nil
	}
	if !opts.New {
		if rec, ok, err := store.Latest(spec); err != nil {
			return RunEnvironment{}, err
		} else if ok {
			if rec.InstanceName == "" {
				rec.InstanceName = lima.InstanceNameForEnvironment(p.Name, rec.ID)
				if opts.Create {
					if err := store.Save(rec); err != nil {
						return RunEnvironment{}, err
					}
				}
			}
			return selectedRunEnvironment(store, rec, true, false, false), nil
		}
	}
	if !opts.Create {
		rec := environment.Record{
			ID:             "env_new",
			Profile:        spec.Profile,
			Backend:        spec.Backend,
			Workspace:      spec.Workspace,
			GuestWorkspace: spec.GuestWorkspace,
			ProfileID:      spec.ProfileID,
			IdentityID:     spec.IdentityID,
			User:           spec.User,
			Hostname:       spec.Hostname,
			InstanceName:   lima.InstanceNameForEnvironment(p.Name, "env_new"),
			Status:         "new",
		}
		return selectedRunEnvironment(store, rec, true, false, true), nil
	}
	rec, err := store.Create(spec)
	if err != nil {
		return RunEnvironment{}, err
	}
	rec.InstanceName = lima.InstanceNameForEnvironment(p.Name, rec.ID)
	if err := store.Save(rec); err != nil {
		return RunEnvironment{}, err
	}
	return selectedRunEnvironment(store, rec, true, false, true), nil
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
		Profile:        p.Name,
		Backend:        backendName,
		Workspace:      workspace,
		GuestWorkspace: guestWorkspace,
		ProfileID:      p.Metadata["profileId"],
		IdentityID:     p.Metadata["identityId"],
		User:           p.Identity.User,
		Hostname:       p.Identity.Hostname,
	}
}

func ValidateEnvironmentRecord(rec environment.Record, spec environment.Spec) error {
	if rec.Profile != spec.Profile {
		return fmt.Errorf("environment %s belongs to profile %q, not %q", rec.ID, rec.Profile, spec.Profile)
	}
	if rec.Backend != spec.Backend {
		return fmt.Errorf("environment %s uses backend %q, not %q", rec.ID, rec.Backend, spec.Backend)
	}
	if filepath.Clean(rec.Workspace) != filepath.Clean(spec.Workspace) ||
		filepath.Clean(rec.GuestWorkspace) != filepath.Clean(spec.GuestWorkspace) {
		return fmt.Errorf("environment %s belongs to workspace %s -> %s", rec.ID, rec.Workspace, rec.GuestWorkspace)
	}
	if rec.ProfileID != spec.ProfileID || rec.IdentityID != spec.IdentityID || rec.User != spec.User || rec.Hostname != spec.Hostname {
		return fmt.Errorf("environment %s identity no longer matches the selected profile; use --new", rec.ID)
	}
	return nil
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
