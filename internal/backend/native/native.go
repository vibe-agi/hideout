package native

import (
	"context"
	"errors"
	"io"
	"os/exec"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/privilege"
)

type Backend struct {
	AllowWeakIsolation bool
	Stdout             io.Writer
	Stderr             io.Writer
	Stdin              io.Reader
}

func (b Backend) Name() string {
	return "native"
}

func (b Backend) Available(context.Context) error {
	if !b.AllowWeakIsolation {
		return errors.New("native backend is weak isolation; pass --backend native --allow-weak-isolation")
	}
	return nil
}

func (b Backend) Prepare(_ context.Context, spec backend.RunSpec) (*backend.Session, error) {
	privilegedSetupRequired := spec.PrivilegedSetupRequired || spec.NetworkPrivilegedSetup || spec.HostFSEnabled
	return &backend.Session{
		ID:                        spec.SessionID,
		EnvironmentID:             spec.EnvironmentID,
		Backend:                   b.Name(),
		HostWork:                  spec.HostWork,
		GuestWork:                 spec.GuestWork,
		GuestHome:                 spec.GuestHome,
		Env:                       append([]string(nil), spec.Env...),
		ShimDir:                   spec.ShimDir,
		ProfileDir:                spec.ProfileDir,
		IdentityMode:              spec.IdentityMode,
		IdentityRoot:              spec.IdentityRoot,
		SessionDir:                spec.SessionDir,
		RuntimeRoot:               spec.RuntimeRoot,
		SessionIsolationRequired:  false,
		TargetUser:                spec.TargetUser,
		Broker:                    spec.Broker,
		NetworkBootstrapPath:      spec.NetworkBootstrapPath,
		NetworkBootstrapGuestPath: spec.NetworkBootstrapGuestPath,
		NetworkCleanupPath:        spec.NetworkCleanupPath,
		NetworkCleanupGuestPath:   spec.NetworkCleanupGuestPath,
		PortBridges:               append([]backend.PortBridgeEndpoint(nil), spec.PortBridges...),
		InstanceName:              spec.InstanceName,
		PreserveInstance:          spec.PreserveInstance,
		NetworkPrivilegedSetup:    spec.NetworkPrivilegedSetup,
		PrivilegedSetupRequired:   privilegedSetupRequired,
		PrivilegeStatusSink:       spec.PrivilegeStatusSink,
		PrivilegedSetupEventSink:  spec.PrivilegedSetupEventSink,
	}, nil
}

func (b Backend) Activate(context.Context, *backend.Session, []string) error {
	return nil
}

func (b Backend) WarmActivate(context.Context, *backend.Session, []string) error {
	return nil
}

func (b Backend) Run(ctx context.Context, session *backend.Session, command []string, env []string) error {
	if len(command) == 0 {
		return errors.New("command is required")
	}
	if err := b.recordPrivilegeStatus(session); err != nil {
		return err
	}
	path, err := backend.LookPathInEnv(command[0], env)
	if err != nil {
		return backend.CommandNotFoundError{
			Backend:   b.Name(),
			Command:   command[0],
			Path:      backend.EnvValue(env, "PATH"),
			Workspace: session.HostWork,
			Hint:      "native backend uses the explicitly selected weak host PATH; no fallback was attempted",
		}
	}
	cmd := exec.CommandContext(ctx, path, command[1:]...)
	cmd.Dir = session.HostWork
	cmd.Env = env
	cmd.Stdout = b.Stdout
	cmd.Stderr = b.Stderr
	cmd.Stdin = b.Stdin
	return cmd.Run()
}

func (b Backend) recordPrivilegeStatus(session *backend.Session) error {
	if session == nil || session.PrivilegeStatusSink == nil {
		return nil
	}
	status, err := privilege.Classify(privilege.ClassificationInput{
		Backend: session.Backend,
		Target:  privilege.TargetIdentity{},
		Setup:   privilege.SetupIdentity{Kind: privilege.SetupNoneRequired},
		Checks: []privilege.CheckResult{{
			Name:   privilege.CheckTargetUID,
			Status: privilege.CheckUnsupported,
			Error:  "native backend is a weak development harness and cannot prove guest privilege state",
		}},
	})
	if err != nil {
		return err
	}
	if err := session.PrivilegeStatusSink(status); err != nil {
		return err
	}
	session.PrivilegeStatus = &status
	return nil
}

func (b Backend) Cleanup(context.Context, *backend.Session) error {
	return nil
}
