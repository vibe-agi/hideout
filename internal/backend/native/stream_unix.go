//go:build darwin || linux

package native

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
	"github.com/vibe-agi/hideout/internal/backend"
)

// RunWithStreams is the weak native harness implementation of the daemon stream
// contract. It exists for tests and diagnostics; supported isolation still uses
// the Lima guest supervisor path.
func (b Backend) RunWithStreams(ctx context.Context, session *backend.Session, command []string, env []string, streams backend.RunStreams) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if session == nil {
		return errors.New("native stream session is required")
	}
	if len(command) == 0 {
		return errors.New("command is required")
	}
	if err := b.recordPrivilegeStatus(session); err != nil {
		return err
	}
	path, err := backend.LookPathInEnv(command[0], env)
	if err != nil {
		return backend.CommandNotFoundError{
			Backend: b.Name(), Command: command[0], Path: backend.EnvValue(env, "PATH"),
			Workspace: session.HostWork,
			Hint:      "native backend uses the explicitly selected weak host PATH; no fallback was attempted",
		}
	}
	if streams.Terminal {
		return b.runPTYStreams(ctx, session, path, command[1:], env, streams)
	}
	return b.runPipeStreams(ctx, session, path, command[1:], env, streams)
}

func (b Backend) runPipeStreams(ctx context.Context, session *backend.Session, path string, args, env []string, streams backend.RunStreams) error {
	cmd := exec.Command(path, args...)
	cmd.Dir = session.HostWork
	cmd.Env = env
	var childStdin io.WriteCloser
	if streams.Stdin != nil {
		var err error
		childStdin, err = cmd.StdinPipe()
		if err != nil {
			return err
		}
	}
	ready := make(chan struct{})
	cmd.Stdout = nativeReadyWriter{ready: ready, dst: writerOrDiscard(streams.Stdout)}
	cmd.Stderr = nativeReadyWriter{ready: ready, dst: writerOrDiscard(streams.Stderr)}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	if childStdin != nil {
		go func() {
			_, _ = io.Copy(childStdin, streams.Stdin)
			_ = childStdin.Close()
		}()
	}
	if err := notifyNativeReady(streams, session); err != nil {
		close(ready)
		terminateNativeProcess(cmd.Process.Pid)
		_ = cmd.Wait()
		return err
	}
	close(ready)
	controlCtx, cancelControls := context.WithCancel(ctx)
	defer cancelControls()
	go controlNativeProcess(controlCtx, cmd.Process.Pid, nil, streams.Controls)
	err := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (b Backend) runPTYStreams(ctx context.Context, session *backend.Session, path string, args, env []string, streams backend.RunStreams) error {
	cmd := exec.Command(path, args...)
	cmd.Dir = session.HostWork
	cmd.Env = env
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: streams.Rows, Cols: streams.Columns})
	if err != nil {
		return err
	}
	if err := notifyNativeReady(streams, session); err != nil {
		terminateNativeProcess(cmd.Process.Pid)
		_ = ptmx.Close()
		_ = cmd.Wait()
		return err
	}
	outputDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(writerOrDiscard(streams.PTY), ptmx)
		if errors.Is(copyErr, syscall.EIO) || errors.Is(copyErr, io.ErrClosedPipe) {
			copyErr = nil
		}
		outputDone <- copyErr
	}()
	if streams.Stdin != nil {
		go func() { _, _ = io.Copy(ptmx, streams.Stdin) }()
	}
	controlCtx, cancelControls := context.WithCancel(ctx)
	defer cancelControls()
	go controlNativeProcess(controlCtx, cmd.Process.Pid, ptmx, streams.Controls)
	waitErr := cmd.Wait()
	_ = ptmx.Close()
	outputErr := <-outputDone
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.Join(waitErr, outputErr)
}

func notifyNativeReady(streams backend.RunStreams, session *backend.Session) error {
	if streams.Ready == nil {
		return nil
	}
	return streams.Ready(session)
}

func controlNativeProcess(ctx context.Context, pid int, terminal *os.File, controls <-chan backend.RunControl) {
	for {
		select {
		case <-ctx.Done():
			terminateNativeProcess(pid)
			return
		case control, ok := <-controls:
			if !ok {
				controls = nil
				continue
			}
			switch control.Kind {
			case backend.RunControlResize:
				if terminal != nil && control.Rows != 0 && control.Columns != 0 {
					_ = pty.Setsize(terminal, &pty.Winsize{Rows: control.Rows, Cols: control.Columns})
				}
			case backend.RunControlSignal:
				if signal, ok := nativeSignal(control.Signal); ok {
					_ = syscall.Kill(-pid, signal)
				}
			}
		}
	}
}

func terminateNativeProcess(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

func nativeSignal(name string) (syscall.Signal, bool) {
	switch name {
	case "SIGHUP":
		return syscall.SIGHUP, true
	case "SIGINT":
		return syscall.SIGINT, true
	case "SIGQUIT":
		return syscall.SIGQUIT, true
	case "SIGTERM":
		return syscall.SIGTERM, true
	case "SIGTSTP":
		return syscall.SIGTSTP, true
	case "SIGCONT":
		return syscall.SIGCONT, true
	case "SIGKILL":
		return syscall.SIGKILL, true
	default:
		return 0, false
	}
}

type nativeReadyWriter struct {
	ready <-chan struct{}
	dst   io.Writer
}

func (w nativeReadyWriter) Write(data []byte) (int, error) {
	<-w.ready
	return w.dst.Write(data)
}

func writerOrDiscard(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
}

var _ backend.StreamRunner = Backend{}
