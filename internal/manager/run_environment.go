package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/runtimeverify"
	"github.com/vibe-agi/hideout/internal/session"
)

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
	Configuration    RuntimeConfiguration
	BootReconfigure  bool
}

func (c Core) SelectRunEnvironment(plan RunPlan, opts RunEnvironmentOptions) (RunEnvironment, error) {
	if c.Store.Root == "" {
		return RunEnvironment{}, errors.New("manager store root is required")
	}
	store := environment.Store{Root: c.Store.Root}
	// Ephemeral runs resolve the same shared environment as a normal run (only
	// identity is session-local), so they get the same runtime-disk precheck.
	// Only --rm stays record-less and materializes no persistent runtime.
	if opts.Create && !opts.RemoveAfterRun {
		provenance, err := runtimeProvenanceForRun(store, plan, opts)
		if err != nil {
			return RunEnvironment{}, err
		}
		if provenance != nil {
			if err := c.checkRuntimeDiskProvenance(*provenance); err != nil {
				return RunEnvironment{}, err
			}
		}
	}
	runEnv, err := SelectRunEnvironment(store, plan.RuntimeProfile, plan.Backend, plan.Workspace, plan.GuestWorkspace, plan.Ephemeral, opts)
	if err == nil && runEnv.Created && runEnv.Record.ID != "env_new" {
		details := map[string]any{
			"environmentName": runEnv.Record.Name,
			"environmentId":   runEnv.Record.ID,
			"autoNamed":       runEnv.Record.AutoNamed,
			"imageRef":        runEnv.Record.ImageRef,
			"backend":         runEnv.Record.Backend,
			"profile":         runEnv.Record.Profile,
		}
		addPinnedEnvironmentWorkspace(details, runEnv.Record)
		c.emitEnvironmentAudit("env.create", "allow", details)
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

func runtimeProvenanceForRun(store environment.Store, plan RunPlan, opts RunEnvironmentOptions) (*environment.RuntimeProvenance, error) {
	if name := strings.TrimSpace(opts.EnvName); name != "" {
		rec, err := store.LoadByName(name)
		if err != nil {
			return nil, err
		}
		return cloneRuntimeProvenance(rec.Runtime), nil
	}
	spec, err := automaticRunEnvironmentSpecForPlatform(
		plan.RuntimeProfile, plan.Backend, plan.Workspace, plan.GuestWorkspace, runtime.GOOS, runtime.GOARCH,
	)
	if err != nil {
		return nil, err
	}
	if rec, err := store.LoadByName(spec.Name); err == nil {
		return cloneRuntimeProvenance(rec.Runtime), nil
	} else if !errors.Is(err, environment.ErrNameNotFound) {
		return nil, err
	}
	return cloneRuntimeProvenance(plan.RuntimeProfile.Environment.Runtime), nil
}

func cloneRuntimeProvenance(value *environment.RuntimeProvenance) *environment.RuntimeProvenance {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func SelectRunEnvironment(store environment.Store, p profile.Profile, backendName, workspace, guestWorkspace string, ephemeral bool, opts RunEnvironmentOptions) (RunEnvironment, error) {
	if name := strings.TrimSpace(opts.EnvName); name != "" {
		if ephemeral {
			return RunEnvironment{}, errors.New("--ephemeral cannot be combined with --env; ephemeral runs use the default shared environment with session-local identity")
		}
		if opts.RemoveAfterRun {
			return RunEnvironment{}, errors.New("--rm cannot be combined with --env; disposable runs are record-less")
		}
		rec, err := store.LoadByName(name)
		if err != nil {
			return RunEnvironment{}, err
		}
		spec, err := runEnvironmentSpecForRecord(rec, p, backendName, workspace, guestWorkspace)
		if err != nil {
			return RunEnvironment{}, err
		}
		if err := ValidateEnvironmentRecord(rec, spec); err != nil {
			return RunEnvironment{}, err
		}
		return selectedEnvironmentWithInstance(store, rec, spec, p, opts.Create)
	}
	if opts.RemoveAfterRun {
		// --rm sessions stay record-less/disposable by contract. Ephemeral runs
		// deliberately do NOT: they resolve the default shared environment and
		// keep only identity session-local (see RunIdentityDir/IdentityMode), so
		// the lima daemon's isolated ready proof has the EnvironmentID and
		// InstanceName it requires.
		return RunEnvironment{}, nil
	}
	return selectAutomaticRunEnvironmentForPlatform(
		store, p, backendName, workspace, guestWorkspace, opts, runtime.GOOS, runtime.GOARCH,
	)
}

func selectAutomaticRunEnvironmentForPlatform(
	store environment.Store,
	p profile.Profile,
	backendName, workspace, guestWorkspace string,
	opts RunEnvironmentOptions,
	hostOS, hostArch string,
) (RunEnvironment, error) {
	if opts.RemoveAfterRun {
		return RunEnvironment{}, nil
	}
	spec, err := automaticRunEnvironmentSpecForPlatform(p, backendName, workspace, guestWorkspace, hostOS, hostArch)
	if err != nil {
		return RunEnvironment{}, err
	}
	var slotLock *environment.Lock
	if spec.Mode == environment.ModeShared {
		slotLock, err = store.LockSharedSlot(spec.SharedSlot)
		if err != nil {
			return RunEnvironment{}, err
		}
		defer slotLock.Unlock()
	}
	rec, err := store.LoadByName(spec.Name)
	switch {
	case err == nil:
		if err := ValidateEnvironmentRecord(rec, spec); err != nil {
			return RunEnvironment{}, err
		}
		return selectedEnvironmentWithInstance(store, rec, spec, p, opts.Create)
	case !errors.Is(err, environment.ErrNameNotFound):
		return RunEnvironment{}, err
	}
	if !opts.Create {
		rec := environment.Record{
			ID:                  "env_new",
			Version:             environment.RecordVersion,
			Name:                spec.Name,
			AutoNamed:           spec.AutoNamed,
			ImageRef:            spec.ImageRef,
			Profile:             spec.Profile,
			Backend:             spec.Backend,
			Mode:                spec.Mode,
			SharedSlot:          spec.SharedSlot,
			MachineIdentityID:   spec.MachineIdentityID,
			BootConfigurationID: spec.BootConfigurationID,
			DedicatedWorkspace:  spec.DedicatedWorkspace,
			DedicatedGuestRoot:  spec.DedicatedGuestRoot,
			BoundWorkspace:      spec.BoundWorkspace,
			BoundGuestRoot:      spec.BoundGuestRoot,
			User:                spec.User,
			Hostname:            spec.Hostname,
			Status:              "new",
		}
		if spec.Backend == "lima" {
			rec.InstanceName = lima.InstanceNameForEnvironment(p.Name, "env_new")
		}
		return selectedRunEnvironment(store, rec, spec, p, true, false, true), nil
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
	return selectedRunEnvironment(store, created, spec, p, true, false, true), nil
}

func selectedEnvironmentWithInstance(store environment.Store, rec environment.Record, spec environment.Spec, p profile.Profile, persist bool) (RunEnvironment, error) {
	if rec.Backend == "lima" && rec.InstanceName == "" {
		rec.InstanceName = lima.InstanceNameForEnvironment(p.Name, rec.ID)
		if persist {
			if err := store.Save(rec); err != nil {
				return RunEnvironment{}, err
			}
		}
	}
	return selectedRunEnvironment(store, rec, spec, p, true, false, false), nil
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
	if runEnv.Record.Runtime != nil {
		// Ordinary target execution can mutate the reusable guest after the
		// pre-run probe. Its receipt is therefore valid only for that operation;
		// explicit runtime verify is the sole producer of persistent readiness.
		cleanupErr = errors.Join(cleanupErr, (runtimeverify.Store{Root: c.Store.Root}).Remove(runEnv.Record.ID))
	}
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

func (c Core) finishConcurrentRunEnvironment(_ context.Context, held **environment.Lock, runEnv RunEnvironment, owner *session.Owner, sessionID string, cleanupErr error, lifecycleRegistration ...lifecycle.Registration) (retErr error) {
	if !runEnv.Active || owner == nil {
		return cleanupErr
	}
	store := environment.Store{Root: c.Store.Root}
	lock := *held
	if lock == nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var err error
		lock, err = store.LockContext(cleanupCtx, runEnv.Record.ID)
		if err != nil {
			failure := errors.Join(cleanupErr, err)
			updateErr := owner.Update(session.OwnerStateFailed, failure.Error())
			// Release liveness but retain failed metadata; deleting it would turn an
			// unproved cleanup into a false-success status.
			failure = errors.Join(failure, updateErr, owner.Release())
			// The transition lock protects provider cleanup, not lifecycle handle
			// ownership. Even when it cannot be acquired, seal the registration so
			// the coordinator records a blocked cleanup instead of retaining a
			// phantom active session forever.
			if len(lifecycleRegistration) != 0 && lifecycleRegistration[0] != nil {
				failure = errors.Join(failure, lifecycleRegistration[0].Finish(context.Background(), failure))
			}
			return failure
		}
	}
	defer func() {
		retErr = errors.Join(retErr, lock.Unlock())
		if *held == lock {
			*held = nil
		}
	}()

	priorCleanupErr := cleanupErr
	var finalErr error
	finalErr = errors.Join(finalErr, owner.Update(session.OwnerStateCleaning, ""))
	if runEnv.Record.Runtime != nil {
		finalErr = errors.Join(finalErr, (runtimeverify.Store{Root: c.Store.Root}).Remove(runEnv.Record.ID))
	}
	finalErr = errors.Join(finalErr, store.ClearSessionRuntime(runEnv.Record.ID, sessionID))
	_, reconcileErr := session.ReconcileStaleOwnersWithCleanup(store.OwnerRoot(runEnv.Record.ID), func(item session.OwnerObservation) error {
		return store.ClearSessionRuntime(runEnv.Record.ID, item.SessionID)
	})
	finalErr = errors.Join(finalErr, reconcileErr)
	observed, observeErr := session.ListOwners(store.OwnerRoot(runEnv.Record.ID))
	finalErr = errors.Join(finalErr, observeErr)
	siblingLive := 0
	ownersProvedIdle := reconcileErr == nil && observeErr == nil
	for _, item := range observed {
		switch item.Status {
		case session.OwnerLive:
			if item.SessionID != sessionID {
				siblingLive++
			}
		case session.OwnerUnprovable:
			ownersProvedIdle = false
			finalErr = errors.Join(finalErr, fmt.Errorf("session %s ownership is unprovable: %w", item.SessionID, session.ErrOwnerUnprovable))
		}
	}
	// Shared environment authority may be removed only when the complete owner
	// set is proved and empty. A corrupt or failed sibling record is not proof
	// that no sibling still depends on the activation receipt.
	//
	// The environment network service is deliberately NOT torn down here: it is
	// an environment-scoped resource retained across idle-grace so a later
	// same-boot run reuses it. The daemon lifecycle reconciliation scrubs it
	// once the guest is observed stopped (non-destructive automatic stop).
	if ownersProvedIdle && siblingLive == 0 {
		finalErr = errors.Join(finalErr, backend.RemoveActivationReceipt(runEnv.RuntimeDir))
	}
	combinedErr := errors.Join(priorCleanupErr, finalErr)
	if combinedErr != nil {
		if err := owner.Update(session.OwnerStateFailed, combinedErr.Error()); err != nil {
			finalErr = errors.Join(finalErr, fmt.Errorf("record failed session cleanup: %w", err))
		}
		if err := owner.Release(); err != nil {
			finalErr = errors.Join(finalErr, fmt.Errorf("release failed session owner: %w", err))
		}
	} else {
		if err := owner.Close(); err != nil {
			finalErr = errors.Join(finalErr, fmt.Errorf("close completed session owner: %w", err))
		}
	}
	combinedErr = errors.Join(priorCleanupErr, finalErr)
	if len(lifecycleRegistration) != 0 && lifecycleRegistration[0] != nil {
		lifecycleErr := lifecycleRegistration[0].Finish(context.Background(), combinedErr)
		if lifecycleErr != nil {
			finalErr = errors.Join(finalErr, fmt.Errorf("finish lifecycle registration: %w", lifecycleErr))
		}
	}
	rec, loadErr := store.Load(runEnv.Record.ID)
	if loadErr != nil {
		return errors.Join(finalErr, loadErr)
	}
	rec.LastEndedAt = time.Now().UTC()
	if siblingLive > 0 {
		rec.Status = "running"
	} else if errors.Join(priorCleanupErr, finalErr) != nil {
		rec.Status = "error"
	} else {
		rec.Status = "ready"
	}
	if err := store.Save(rec); err != nil {
		finalErr = errors.Join(finalErr, err)
	}
	// The caller already recorded priorCleanupErr at the authority boundary
	// where it occurred. Return only errors introduced by owner/environment
	// finalization so RunResult does not report the same cleanup failure twice.
	return finalErr
}

func RunEnvironmentSpec(p profile.Profile, backendName, workspace, guestWorkspace string) environment.Spec {
	return workspaceBoundRunEnvironmentSpec(p, backendName, workspace, guestWorkspace)
}

func automaticRunEnvironmentSpecForPlatform(
	p profile.Profile,
	backendName, workspace, guestWorkspace, hostOS, hostArch string,
) (environment.Spec, error) {
	spec := workspaceBoundRunEnvironmentSpec(p, backendName, workspace, guestWorkspace)
	if backendName != "lima" || hostOS != "darwin" || hostArch != "arm64" {
		return spec, nil
	}
	if p.Workspace.PathMode != profile.WorkspacePathModeAlias {
		return environment.Spec{}, fmt.Errorf("shared default Lima environments require workspace pathMode=alias; run 'hideout profile workspace-path-mode %s alias', or create a dedicated environment with 'hideout env create <name> ...' and run it with --env <name>", p.Name)
	}
	spec.Name = environment.SharedDisplayName(p.Name)
	spec.AutoNamed = true
	spec.Mode = environment.ModeShared
	spec.SharedSlot = environment.SharedSlotID(p.Name)
	spec.BoundWorkspace = ""
	spec.BoundGuestRoot = ""
	setConfigurationIDs(&spec, p, backendName, environment.ModeShared)
	return spec, nil
}

func workspaceBoundRunEnvironmentSpec(p profile.Profile, backendName, workspace, guestWorkspace string) environment.Spec {
	var runtimeProvenance *environment.RuntimeProvenance
	if p.Environment.Runtime != nil {
		copy := *p.Environment.Runtime
		runtimeProvenance = &copy
	}
	_, _, machineID, bootID, err := MachineBootConfigurationForProfile(p, backendName, environment.ModeWorkspaceBound)
	if err != nil {
		// Run plans validate profiles before this pure constructor is called. Keep
		// an invalid value so Store.Create fails closed instead of hiding the error.
		machineID, bootID = "", ""
	}
	return environment.Spec{
		Name:                environment.AutoName(p.Name, workspace),
		AutoNamed:           true,
		ImageRef:            p.BaseImageOrBuiltin(),
		Profile:             p.Name,
		Backend:             backendName,
		Mode:                environment.ModeWorkspaceBound,
		MachineIdentityID:   machineID,
		BootConfigurationID: bootID,
		BoundWorkspace:      filepath.Clean(workspace),
		BoundGuestRoot:      filepath.Clean(guestWorkspace),
		User:                p.Identity.User,
		Hostname:            p.Identity.Hostname,
		Runtime:             runtimeProvenance,
	}
}

func dedicatedRunEnvironmentSpec(p profile.Profile, backendName, workspace, guestWorkspace, name string) environment.Spec {
	spec := workspaceBoundRunEnvironmentSpec(p, backendName, workspace, guestWorkspace)
	spec.Name = name
	spec.AutoNamed = false
	spec.Mode = environment.ModeDedicated
	spec.DedicatedWorkspace = spec.BoundWorkspace
	spec.DedicatedGuestRoot = spec.BoundGuestRoot
	spec.BoundWorkspace = ""
	spec.BoundGuestRoot = ""
	setConfigurationIDs(&spec, p, backendName, environment.ModeDedicated)
	return spec
}

func runEnvironmentSpecForRecord(
	record environment.Record,
	p profile.Profile,
	backendName, workspace, guestWorkspace string,
) (environment.Spec, error) {
	switch record.Mode {
	case environment.ModeShared:
		return automaticRunEnvironmentSpecForPlatform(p, backendName, workspace, guestWorkspace, runtime.GOOS, runtime.GOARCH)
	case environment.ModeDedicated:
		spec := dedicatedRunEnvironmentSpec(p, backendName, workspace, guestWorkspace, record.Name)
		spec.ImageRef = record.ImageRef
		spec.Runtime = cloneRuntimeProvenance(record.Runtime)
		pinned := p
		pinned.Environment.BaseImage = record.ImageRef
		pinned.Environment.Runtime = cloneRuntimeProvenance(record.Runtime)
		setConfigurationIDs(&spec, pinned, backendName, environment.ModeDedicated)
		return spec, nil
	case environment.ModeWorkspaceBound:
		return workspaceBoundRunEnvironmentSpec(p, backendName, workspace, guestWorkspace), nil
	default:
		return environment.Spec{}, fmt.Errorf("environment %q has unsupported mode %q", record.Name, record.Mode)
	}
}

func setConfigurationIDs(spec *environment.Spec, p profile.Profile, backendName string, mode environment.Mode) {
	_, _, machineID, bootID, err := MachineBootConfigurationForProfile(p, backendName, mode)
	if err != nil {
		spec.MachineIdentityID = ""
		spec.BootConfigurationID = ""
		return
	}
	spec.MachineIdentityID = machineID
	spec.BootConfigurationID = bootID
}

// DriftAxis names one drifted identity input with its pinned and current
// values (verbatim operator data).
type DriftAxis struct {
	Axis    string `json:"axis"`
	Pinned  string `json:"pinned"`
	Current string `json:"current"`
}

// DriftError is the fail-closed use-time drift report: machine identity and a
// dedicated static workspace binding are the only run-selection drift axes. It
// never triggers a rebuild.
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
	var axes []DriftAxis
	if rec.Mode != spec.Mode {
		axes = append(axes, DriftAxis{Axis: "mode", Pinned: string(rec.Mode), Current: string(spec.Mode)})
	}
	if rec.SharedSlot != spec.SharedSlot {
		axes = append(axes, DriftAxis{Axis: "sharedSlot", Pinned: rec.SharedSlot, Current: spec.SharedSlot})
	}
	if rec.MachineIdentityID != spec.MachineIdentityID {
		axes = append(axes, DriftAxis{Axis: "machine", Pinned: rec.MachineIdentityID, Current: spec.MachineIdentityID})
	}
	recordBinding, recordBound := pinnedEnvironmentWorkspace(rec)
	recordWorkspace, recordGuestRoot := recordBinding.HostRoot, recordBinding.GuestRoot
	specWorkspace, specGuestRoot, specBound := specWorkspaceBinding(spec)
	if recordBound != specBound || (recordBound && (!sameWorkspaceIdentity(recordWorkspace, specWorkspace) ||
		filepath.Clean(recordGuestRoot) != filepath.Clean(specGuestRoot))) {
		axes = append(axes, DriftAxis{
			Axis:    "workspace",
			Pinned:  recordWorkspace + " -> " + recordGuestRoot,
			Current: specWorkspace + " -> " + specGuestRoot,
		})
	}
	if len(axes) > 0 {
		return &DriftError{Environment: rec.Name, Axes: axes}
	}
	return nil
}

func specWorkspaceBinding(spec environment.Spec) (hostRoot, guestRoot string, ok bool) {
	switch spec.Mode {
	case environment.ModeDedicated:
		return spec.DedicatedWorkspace, spec.DedicatedGuestRoot, true
	case environment.ModeWorkspaceBound:
		return spec.BoundWorkspace, spec.BoundGuestRoot, true
	default:
		return "", "", false
	}
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

func selectedRunEnvironment(store environment.Store, rec environment.Record, spec environment.Spec, p profile.Profile, preserve, removeAfterRun, created bool) RunEnvironment {
	configuration, configurationErr := RuntimeConfigurationForProfile(p, rec.Backend, rec.Mode)
	if configurationErr != nil {
		machine, boot, machineID, bootID, machineErr := MachineBootConfigurationForProfile(p, rec.Backend, rec.Mode)
		if machineErr == nil {
			configuration.Machine = machine
			configuration.Boot = boot
			configuration.Layers.MachineID = machineID
			configuration.Layers.BootID = bootID
		}
	}
	return RunEnvironment{
		Active:           true,
		Record:           rec,
		RuntimeDir:       store.RuntimeDir(rec.ID),
		ShimDir:          store.ShimDir(rec.ID),
		InstanceName:     rec.InstanceName,
		PreserveInstance: preserve,
		RemoveAfterRun:   removeAfterRun,
		Created:          created,
		Configuration:    configuration,
		BootReconfigure:  rec.BootConfigurationID != spec.BootConfigurationID,
	}
}
