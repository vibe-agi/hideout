package lima

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/privilege"
	"golang.org/x/crypto/ssh"
)

const setupCancelWait = 2 * time.Second

const (
	setupSSHConnectAttempts = 3
	setupSSHConnectBackoff  = 50 * time.Millisecond
)

type sshCommandSession interface {
	Start(string) error
	Wait() error
	Signal(ssh.Signal) error
}

type sshConnectionCloser interface {
	Close() error
}

const (
	setupCategoryNetwork     = "network"
	setupCategoryHostFS      = "hostfs"
	setupCategorySessionView = "session-view"
	setupCategoryBoot        = "boot-configuration"
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
	lease, err := r.backend.acquireSSHClientForUser(ctx, instanceName, "root")
	if err != nil {
		return err
	}
	defer lease.Close()
	client := lease.Client()
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("open lima setup ssh session: %w", err)
	}
	defer session.Close()
	session.Stdin = stdin
	session.Stdout = stdout
	session.Stderr = stderr
	return runCancelableSSHCommand(ctx, session, session, setupShellCommand(workdir, env, command))
}

func retrySetupSSHConnect(ctx context.Context, connect func() (*ssh.Client, error)) (*ssh.Client, error) {
	var lastErr error
	for attempt := 0; attempt < setupSSHConnectAttempts; attempt++ {
		client, err := connect()
		if err == nil {
			return client, nil
		}
		lastErr = err
		if !errors.Is(err, io.EOF) || attempt+1 == setupSSHConnectAttempts {
			break
		}
		timer := time.NewTimer(setupSSHConnectBackoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, errors.Join(ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func runCancelableSSHCommand(ctx context.Context, session sshCommandSession, connection sshConnectionCloser, command string) error {
	if err := session.Start(command); err != nil {
		return err
	}
	wait := make(chan error, 1)
	go func() { wait <- session.Wait() }()
	select {
	case err := <-wait:
		return err
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		_ = connection.Close()
		select {
		case <-wait:
		case <-time.After(setupCancelWait):
		}
		return ctx.Err()
	}
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
	setup := rootControlSSHSetupIdentity()
	if err := b.setupRunner().Check(ctx, session.InstanceName); err != nil {
		setup.Available = false
		setup.Proof = boundedCheckError(err, "")
	}
	return setup
}

func (b Backend) supervisorSetupIdentity(ctx context.Context, session *backend.Session) (privilege.SetupIdentity, []string, error) {
	if session == nil || !session.PrivilegedSetupRequired {
		return b.setupIdentity(ctx, session), nil, nil
	}
	setup := b.setupIdentity(ctx, session)
	categories := supervisorSetupCategories(session)
	if setup.Available {
		return setup, categories, nil
	}
	reason := "privileged setup identity is unavailable"
	for _, category := range categories {
		if err := b.emitPrivilegedSetup(session, backend.PrivilegedSetupEvent{
			Action: privilege.ActionPrivilegedSetup, Category: category, Status: "failed",
			Setup: setup, Reason: reason,
		}); err != nil {
			return setup, categories, err
		}
	}
	return setup, categories, fmt.Errorf("%s setup: %s: %s", categories[0], reason, setup.Proof)
}

func supervisorSetupCategories(session *backend.Session) []string {
	if session == nil {
		return nil
	}
	var categories []string
	if session.NetworkPrivilegedSetup {
		categories = append(categories, setupCategoryNetwork)
	}
	if session.HostFSEnabled {
		categories = append(categories, setupCategoryHostFS)
	}
	if len(categories) == 0 && session.PrivilegedSetupRequired {
		categories = append(categories, setupCategorySessionView)
	}
	return categories
}

func (b Backend) emitSupervisorSetupStatus(session *backend.Session, setup privilege.SetupIdentity, categories []string, status, reason string) error {
	for _, category := range categories {
		if err := b.emitPrivilegedSetup(session, backend.PrivilegedSetupEvent{
			Action: privilege.ActionPrivilegedSetup, Category: category, Status: status,
			Setup: setup, Reason: boundedCheckError(errors.New(reason), ""),
		}); err != nil {
			return err
		}
	}
	return nil
}

func rootControlSSHSetupIdentity() privilege.SetupIdentity {
	return privilege.SetupIdentity{
		Kind:               privilege.SetupRootControlSSH,
		Available:          true,
		SeparateFromTarget: true,
		CredentialLocation: privilege.CredentialLocationClass("lima-ssh-config"),
		Proof:              "root ssh control path accepted a fixed setup command",
	}
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
