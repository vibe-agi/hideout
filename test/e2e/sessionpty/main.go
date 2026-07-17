//go:build darwin || linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/vibe-agi/hideout/internal/environment"
	runsession "github.com/vibe-agi/hideout/internal/session"
	"golang.org/x/term"
)

const probeTimeout = 20 * time.Second

type options struct {
	hideout   string
	store     string
	limaHome  string
	workspace string
	profile   string
	out       string
}

type ptyClient struct {
	cmd    *exec.Cmd
	master *os.File
	slave  *os.File
	before *term.State

	mu      sync.Mutex
	output  bytes.Buffer
	done    chan struct{}
	waitErr error
}

func main() {
	var opts options
	flag.StringVar(&opts.hideout, "hideout", "", "hideout executable")
	flag.StringVar(&opts.store, "store", "", "isolated Hideout store")
	flag.StringVar(&opts.limaHome, "lima-home", "", "isolated Lima home")
	flag.StringVar(&opts.workspace, "workspace", "", "host workspace")
	flag.StringVar(&opts.profile, "profile", "", "profile name")
	flag.StringVar(&opts.out, "out", "", "evidence log path")
	flag.Parse()
	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "session-pty-gate:", err)
		os.Exit(1)
	}
}

func run(opts options) error {
	for name, value := range map[string]string{
		"hideout": opts.hideout, "store": opts.store, "lima-home": opts.limaHome,
		"workspace": opts.workspace, "profile": opts.profile, "out": opts.out,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if err := runResizeAndFullscreen(opts); err != nil {
		return err
	}
	if err := runInterrupt(opts); err != nil {
		return err
	}
	crash, err := runDaemonCrash(opts)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(map[string]any{
		"schema":                "hideout.session-pty-gate/v1",
		"status":                "passed",
		"initialSize":           "24x80",
		"resizedSize":           "31x97",
		"fullscreenFixture":     true,
		"interruptExit":         130,
		"daemonCrashClients":    2,
		"daemonCrashSessionIds": crash.sessionIDs,
		"terminalRestore":       true,
		"targetsReaped":         true,
		"restartFailedClosed":   true,
		"explicitRecovery":      true,
		"postRecoveryRun":       true,
	}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(opts.out, data, 0o600)
}

func runResizeAndFullscreen(opts options) error {
	script := `printf 'initial:'; stty size; printf '\033[?1049hfullscreen-ready\033[?1049l\n'; trap 'printf "resized:"; stty size; exit 0' WINCH; printf 'resize-ready\n'; while :; do sleep 1; done`
	client, err := startPTYClient(opts, "sh", "-c", script)
	if err != nil {
		return err
	}
	defer client.close()
	if err := client.waitOutput("initial:24 80", probeTimeout); err != nil {
		return err
	}
	if err := client.waitOutput("fullscreen-ready", probeTimeout); err != nil {
		return err
	}
	if err := client.waitOutput("resize-ready", probeTimeout); err != nil {
		return err
	}
	if err := pty.Setsize(client.master, &pty.Winsize{Rows: 31, Cols: 97}); err != nil {
		return fmt.Errorf("resize PTY master: %w", err)
	}
	if err := client.cmd.Process.Signal(syscall.SIGWINCH); err != nil {
		return err
	}
	if err := client.waitOutput("resized:31 97", probeTimeout); err != nil {
		return err
	}
	if err := client.wait(probeTimeout); err != nil {
		return fmt.Errorf("resize client: %w; output=%q", err, client.text())
	}
	return client.requireRestored()
}

func runInterrupt(opts options) error {
	client, err := startPTYClient(opts, "sh", "-c", `trap 'printf "caught-int\n"; exit 130' INT; printf 'interrupt-ready\n'; while :; do sleep 1; done`)
	if err != nil {
		return err
	}
	defer client.close()
	if err := client.waitOutput("interrupt-ready", probeTimeout); err != nil {
		return err
	}
	if _, err := client.master.Write([]byte{3}); err != nil {
		return err
	}
	err = client.wait(probeTimeout)
	if exitCode(err) != 130 {
		return fmt.Errorf("interrupt exit=%d want=130 err=%v output=%q", exitCode(err), err, client.text())
	}
	if !strings.Contains(client.text(), "caught-int") {
		return fmt.Errorf("interrupt marker missing: %q", client.text())
	}
	return client.requireRestored()
}

type crashResult struct {
	sessionIDs []string
}

func runDaemonCrash(opts options) (crashResult, error) {
	clients := make([]*ptyClient, 0, 2)
	for index := 1; index <= 2; index++ {
		marker := fmt.Sprintf("crash-ready-%d", index)
		script := fmt.Sprintf(`printf 'session:%%s %s\n' "$HIDEOUT_SESSION_ID"; trap 'exit 130' HUP INT TERM; while :; do sleep 1; done`, marker)
		client, err := startPTYClient(opts, "sh", "-c", script)
		if err != nil {
			closeClients(clients)
			return crashResult{}, err
		}
		clients = append(clients, client)
	}
	defer closeClients(clients)
	for index, client := range clients {
		if err := client.waitOutput(fmt.Sprintf("crash-ready-%d", index+1), probeTimeout); err != nil {
			return crashResult{}, err
		}
	}
	sessionIDs := make([]string, len(clients))
	for index, client := range clients {
		id := outputSessionID(client.text())
		if id == "" {
			return crashResult{}, fmt.Errorf("client %d did not report a session id: %q", index+1, client.text())
		}
		sessionIDs[index] = id
	}
	daemonPID, err := findDaemonPID(opts.hideout)
	if err != nil {
		return crashResult{}, err
	}
	if err := syscall.Kill(daemonPID, syscall.SIGKILL); err != nil {
		return crashResult{}, err
	}
	for index, client := range clients {
		waitErr := client.wait(probeTimeout)
		if waitErr == nil {
			return crashResult{}, fmt.Errorf("client %d returned success after daemon crash", index+1)
		}
		if err := client.requireRestored(); err != nil {
			return crashResult{}, fmt.Errorf("client %d: %w", index+1, err)
		}
	}

	record, err := matchingEnvironment(opts)
	if err != nil {
		return crashResult{}, err
	}
	if err := waitTargetsGone(opts, record.InstanceName, sessionIDs); err != nil {
		return crashResult{}, err
	}
	failureOutput, failureErr := runHideout(opts, "run", "--terminal", "never", "--profile", opts.profile, "--backend", "lima", "--network", "direct", "--workspace", opts.workspace, "--guest-workspace", "/workspace", "--", "true")
	if failureErr == nil || (!strings.Contains(failureOutput, "session.cleanup.failed") && !strings.Contains(failureOutput, "explicit recovery")) {
		return crashResult{}, fmt.Errorf("restart did not fail closed for stale owners: err=%v output=%q", failureErr, failureOutput)
	}
	if err := waitReconciliationSettled(opts, record.ID); err != nil {
		return crashResult{}, err
	}
	if output, err := runHideout(opts, "stop", record.Name); err != nil {
		return crashResult{}, fmt.Errorf("explicit stop recovery: %w output=%q", err, output)
	}
	owners, err := runsession.ListOwners((environment.Store{Root: opts.store}).OwnerRoot(record.ID))
	if err != nil || len(owners) != 0 {
		return crashResult{}, fmt.Errorf("owner evidence remained after explicit recovery: owners=%+v err=%v", owners, err)
	}
	if output, err := runHideout(opts, "run", "--terminal", "never", "--profile", opts.profile, "--backend", "lima", "--network", "direct", "--workspace", opts.workspace, "--guest-workspace", "/workspace", "--", "true"); err != nil {
		return crashResult{}, fmt.Errorf("post-recovery run: %w output=%q", err, output)
	}
	if output, err := runHideout(opts, "stop", record.Name); err != nil {
		return crashResult{}, fmt.Errorf("post-recovery stop: %w output=%q", err, output)
	}
	_, _ = runHideout(opts, "daemon", "stop")
	return crashResult{sessionIDs: sessionIDs}, nil
}

func waitReconciliationSettled(opts options, environmentID string) error {
	deadline := time.Now().Add(probeTimeout)
	for time.Now().Before(deadline) {
		output, err := runHideout(opts, "daemon", "status")
		if err == nil {
			var status struct {
				Lifecycle []struct {
					EnvironmentID  string `json:"environmentId"`
					Reconciliation string `json:"reconciliation"`
				} `json:"lifecycle"`
			}
			if json.Unmarshal([]byte(output), &status) == nil {
				for _, item := range status.Lifecycle {
					if item.EnvironmentID == environmentID && item.Reconciliation != "pending" {
						return nil
					}
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("replacement daemon reconciliation remained pending for environment %s", environmentID)
}

func startPTYClient(opts options, target ...string) (*ptyClient, error) {
	master, slave, err := pty.Open()
	if err != nil {
		return nil, err
	}
	if err := pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		master.Close()
		slave.Close()
		return nil, fmt.Errorf("set initial PTY size: %w", err)
	}
	before, err := term.GetState(int(master.Fd()))
	if err != nil {
		master.Close()
		slave.Close()
		return nil, fmt.Errorf("read initial PTY state: %w", err)
	}
	args := []string{"run", "--terminal", "always", "--profile", opts.profile, "--backend", "lima", "--network", "direct", "--workspace", opts.workspace, "--guest-workspace", "/workspace", "--"}
	args = append(args, target...)
	cmd := exec.Command(opts.hideout, args...)
	cmd.Env = gateEnvironment(opts)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}
	client := &ptyClient{cmd: cmd, master: master, slave: slave, before: before, done: make(chan struct{})}
	go func() {
		_, _ = io.Copy(lockedWriter{client: client}, master)
	}()
	go func() {
		client.waitErr = cmd.Wait()
		close(client.done)
	}()
	return client, nil
}

type lockedWriter struct{ client *ptyClient }

func (w lockedWriter) Write(data []byte) (int, error) {
	w.client.mu.Lock()
	defer w.client.mu.Unlock()
	return w.client.output.Write(data)
}

func (c *ptyClient) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.output.String()
}

func (c *ptyClient) waitOutput(value string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(c.text(), value) {
			return nil
		}
		select {
		case <-c.done:
			return fmt.Errorf("client exited before %q: err=%v output=%q", value, c.waitErr, c.text())
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %q: output=%q", value, c.text())
}

func (c *ptyClient) wait(timeout time.Duration) error {
	select {
	case <-c.done:
		return c.waitErr
	case <-time.After(timeout):
		_ = c.cmd.Process.Kill()
		<-c.done
		return errors.New("PTY client did not exit within the bound")
	}
}

func (c *ptyClient) requireRestored() error {
	after, err := term.GetState(int(c.master.Fd()))
	if err != nil {
		return fmt.Errorf("read restored PTY state: %w", err)
	}
	if !reflect.DeepEqual(c.before, after) {
		return errors.New("host terminal state was not restored")
	}
	return nil
}

func (c *ptyClient) close() {
	if c == nil {
		return
	}
	select {
	case <-c.done:
	default:
		_ = c.cmd.Process.Kill()
		<-c.done
	}
	_ = c.master.Close()
	_ = c.slave.Close()
}

func closeClients(clients []*ptyClient) {
	for _, client := range clients {
		client.close()
	}
}

func gateEnvironment(opts options) []string {
	env := append([]string(nil), os.Environ()...)
	env = replaceEnv(env, "HIDEOUT_STORE_ROOT", opts.store)
	env = replaceEnv(env, "LIMA_HOME", opts.limaHome)
	return env
}

func replaceEnv(env []string, name, value string) []string {
	prefix := name + "="
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, prefix) {
			out = append(out, item)
		}
	}
	return append(out, prefix+value)
}

func runHideout(opts options, args ...string) (string, error) {
	cmd := exec.Command(opts.hideout, args...)
	cmd.Env = gateEnvironment(opts)
	data, err := cmd.CombinedOutput()
	return string(data), err
}

func findDaemonPID(binary string) (int, error) {
	data, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return 0, err
	}
	want, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return 0, fmt.Errorf("resolve daemon executable: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[2] != "__daemon-serve" {
			continue
		}
		pid, parseErr := strconv.Atoi(fields[0])
		if parseErr != nil {
			continue
		}
		command, resolveErr := filepath.EvalSymlinks(fields[1])
		if resolveErr == nil && command == want {
			return pid, nil
		}
	}
	return 0, fmt.Errorf("daemon process %q not found", want+" __daemon-serve")
}

func outputSessionID(output string) string {
	for _, field := range strings.Fields(output) {
		if strings.HasPrefix(field, "session:ses_") {
			return strings.TrimPrefix(field, "session:")
		}
	}
	return ""
}

func matchingEnvironment(opts options) (environment.Record, error) {
	records, err := (environment.Store{Root: opts.store}).List()
	if err != nil {
		return environment.Record{}, err
	}
	cleanWorkspace, err := filepath.EvalSymlinks(opts.workspace)
	if err != nil {
		return environment.Record{}, err
	}
	for _, record := range records {
		recordWorkspace, evalErr := filepath.EvalSymlinks(record.Workspace)
		if evalErr == nil && record.Profile == opts.profile && recordWorkspace == cleanWorkspace {
			return record, nil
		}
	}
	return environment.Record{}, errors.New("matching environment record not found")
}

func waitTargetsGone(opts options, instance string, sessionIDs []string) error {
	configCmd := exec.Command("limactl", "list", "--format", "{{.SSHConfigFile}}", instance)
	configCmd.Env = gateEnvironment(opts)
	data, err := configCmd.Output()
	if err != nil {
		return err
	}
	config := strings.TrimSpace(string(data))
	if config == "" {
		return errors.New("Lima SSH config path is empty")
	}
	script := `for wanted in "$@"; do
  for env_file in /proc/[0-9]*/environ; do
    [ -r "$env_file" ] || continue
    if tr '\000' '\n' 2>/dev/null <"$env_file" | grep -Fqx "HIDEOUT_SESSION_ID=$wanted"; then
      exit 1
    fi
  done
done
exit 0
`
	deadline := time.Now().Add(probeTimeout)
	for time.Now().Before(deadline) {
		args := []string{"-F", config, "-o", "BatchMode=yes", "-o", "User=root", "-o", "ControlMaster=no", "-o", "ControlPath=none", "lima-" + instance, "sh", "-s", "--"}
		args = append(args, sessionIDs...)
		cmd := exec.Command("ssh", args...)
		cmd.Stdin = strings.NewReader(script)
		if err := cmd.Run(); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("guest target processes remained after daemon crash: %v", sessionIDs)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
