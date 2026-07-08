package lima

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/privilege"
)

const (
	setupCategoryNetwork = "network"
	setupCategoryHostFS  = "hostfs"
)

// SetupCommandRunner executes Go-owned privileged setup/cleanup commands. It is
// intentionally separate from CommandRunner so target commands cannot silently
// borrow setup authority.
type SetupCommandRunner interface {
	Check(ctx context.Context, instanceName string) error
	Run(ctx context.Context, instanceName, workdir string, env []string, command []string, stdin io.Reader, stdout, stderr io.Writer) error
}

type rootSSHSetupRunner struct {
	backend Backend
}

func (b Backend) setupRunner() SetupCommandRunner {
	if b.SetupRunner != nil {
		return b.SetupRunner
	}
	return rootSSHSetupRunner{backend: b}
}

func (r rootSSHSetupRunner) Check(ctx context.Context, instanceName string) error {
	return r.Run(ctx, instanceName, "/", []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, []string{"true"}, nil, io.Discard, io.Discard)
}

func (r rootSSHSetupRunner) Run(ctx context.Context, instanceName, workdir string, env []string, command []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if strings.TrimSpace(instanceName) == "" {
		return errors.New("lima setup identity requires instance name")
	}
	if len(command) == 0 {
		return errors.New("lima setup identity requires command")
	}
	client, err := r.backend.newSSHClientForUser(ctx, instanceName, "root")
	if err != nil {
		return err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("open lima setup ssh session: %w", err)
	}
	defer session.Close()
	session.Stdin = stdin
	session.Stdout = stdout
	session.Stderr = stderr
	return session.Run(setupShellCommand(workdir, env, command))
}

func setupShellCommand(workdir string, env []string, command []string) string {
	if strings.TrimSpace(workdir) == "" {
		workdir = "/"
	}
	var b strings.Builder
	b.WriteString("cd ")
	b.WriteString(shellQuote(workdir))
	b.WriteString(" && exec env -i")
	for _, kv := range env {
		if strings.TrimSpace(kv) == "" {
			continue
		}
		b.WriteByte(' ')
		b.WriteString(shellQuote(kv))
	}
	for _, arg := range command {
		b.WriteByte(' ')
		b.WriteString(shellQuote(arg))
	}
	return b.String()
}

func (b Backend) setupIdentity(ctx context.Context, session *backend.Session) privilege.SetupIdentity {
	if session == nil || !session.PrivilegedSetupRequired {
		return privilege.SetupIdentity{
			Kind:               privilege.SetupNoneRequired,
			Available:          true,
			SeparateFromTarget: true,
			Proof:              "no privileged setup requested",
		}
	}
	setup := privilege.SetupIdentity{
		Kind:               privilege.SetupRootControlSSH,
		Available:          true,
		SeparateFromTarget: true,
		CredentialLocation: privilege.CredentialLocationClass("lima-ssh-config"),
		Proof:              "root ssh control path accepted a fixed setup command",
	}
	if err := b.setupRunner().Check(ctx, session.InstanceName); err != nil {
		setup.Available = false
		setup.Proof = boundedCheckError(err, "")
	}
	return setup
}

func (b Backend) runSetupCommand(ctx context.Context, session *backend.Session, category string, workdir string, env []string, command []string, stdin io.Reader) error {
	setup := b.setupIdentity(ctx, session)
	if !setup.Available {
		reason := "privileged setup identity is unavailable"
		if sinkErr := b.emitPrivilegedSetup(session, backend.PrivilegedSetupEvent{
			Action:   privilege.ActionPrivilegedSetup,
			Category: category,
			Status:   "failed",
			Setup:    setup,
			Reason:   reason,
		}); sinkErr != nil {
			return sinkErr
		}
		return fmt.Errorf("%s setup: %s: %s", category, reason, setup.Proof)
	}
	err := b.setupRunner().Run(ctx, session.InstanceName, workdir, env, command, stdin, b.controlStdout(), b.controlStderr())
	status := "succeeded"
	reason := "privileged setup completed through root-control-ssh"
	if err != nil {
		status = "failed"
		reason = err.Error()
	}
	if sinkErr := b.emitPrivilegedSetup(session, backend.PrivilegedSetupEvent{
		Action:   privilege.ActionPrivilegedSetup,
		Category: category,
		Status:   status,
		Setup:    setup,
		Reason:   reason,
	}); sinkErr != nil {
		return sinkErr
	}
	if err != nil {
		return fmt.Errorf("%s setup: %w", category, err)
	}
	return nil
}

func (b Backend) runSetupCleanup(ctx context.Context, session *backend.Session, category string, workdir string, env []string, command []string) error {
	setup := b.setupIdentity(ctx, session)
	if !setup.Available {
		reason := "privileged cleanup identity is unavailable"
		if sinkErr := b.emitPrivilegedSetup(session, backend.PrivilegedSetupEvent{
			Action:   privilege.ActionPrivilegedCleanup,
			Category: category,
			Status:   "failed",
			Setup:    setup,
			Reason:   reason,
		}); sinkErr != nil {
			return sinkErr
		}
		return fmt.Errorf("%s cleanup: %s: %s", category, reason, setup.Proof)
	}
	err := b.setupRunner().Run(ctx, session.InstanceName, workdir, env, command, nil, b.controlStdout(), b.controlStderr())
	status := "succeeded"
	reason := "privileged cleanup completed through root-control-ssh"
	if err != nil {
		status = "failed"
		reason = err.Error()
	}
	if sinkErr := b.emitPrivilegedSetup(session, backend.PrivilegedSetupEvent{
		Action:   privilege.ActionPrivilegedCleanup,
		Category: category,
		Status:   status,
		Setup:    setup,
		Reason:   reason,
	}); sinkErr != nil {
		return sinkErr
	}
	if err != nil {
		return fmt.Errorf("%s cleanup: %w", category, err)
	}
	return nil
}

func (b Backend) emitPrivilegedSetup(session *backend.Session, event backend.PrivilegedSetupEvent) error {
	if session == nil || session.PrivilegedSetupEventSink == nil {
		return nil
	}
	return session.PrivilegedSetupEventSink(event)
}
