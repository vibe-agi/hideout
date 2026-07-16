package lima

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
)

func TestLimaFullActivationWritesReceiptAndWarmAttachCarriesBootProof(t *testing.T) {
	root := t.TempDir()
	first := isolatedActivationSession(t, root, "ses_20260716T120000Z_0123456789abcdef")
	runner := &activationCommandRunner{}
	setup := &activationProofRunner{bootID: "01234567-89ab-cdef-0123-456789abcdef"}
	b := Backend{Runner: runner, SetupRunner: setup, ControlStdout: io.Discard, ControlStderr: io.Discard}

	if err := b.Activate(context.Background(), first, []string{"PATH=/usr/bin"}); err != nil {
		t.Fatal(err)
	}
	if !first.RuntimeReady {
		t.Fatal("full activation did not mark runtime ready")
	}
	receipt, err := backend.LoadActivationReceipt(root)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.OwnerSessionID != first.ID || receipt.BootID != setup.bootID || !receipt.NamespaceProbe {
		t.Fatalf("activation receipt=%+v", receipt)
	}
	if !reflect.DeepEqual(runner.calls, [][]string{
		{"list", "--quiet"},
		{"start", "--tty=false", "--name", first.InstanceName, first.ConfigPath},
	}) {
		t.Fatalf("full activation calls=%q", runner.calls)
	}

	second := isolatedActivationSession(t, root, "ses_20260716T120001Z_fedcba9876543210")
	owner, err := b.WarmActivationOwner(second)
	if err != nil {
		t.Fatal(err)
	}
	if owner != first.ID {
		t.Fatalf("warm owner=%q want=%q", owner, first.ID)
	}
	second.ActivationOwnerID = owner
	runner.listOutput = second.InstanceName + "\n"
	runner.calls = nil
	if err := b.WarmActivate(context.Background(), second, []string{"PATH=/usr/bin", "VALUE=warm"}); err != nil {
		t.Fatal(err)
	}
	if !second.RuntimeReady || !second.RunAttempted || !reflect.DeepEqual(second.Env, []string{"PATH=/usr/bin", "VALUE=warm"}) {
		t.Fatalf("warm session=%+v", second)
	}
	if second.ExpectedBootID != receipt.BootID {
		t.Fatalf("warm expected boot=%q want=%q", second.ExpectedBootID, receipt.BootID)
	}
	if !reflect.DeepEqual(runner.calls, [][]string{{"list", "--quiet"}}) {
		t.Fatalf("warm attach started or reconfigured Lima: %q", runner.calls)
	}
	if setup.calls != 1 {
		t.Fatalf("authenticated setup probes=%d want=1 full-activation probe", setup.calls)
	}
}

func TestLimaActivationAndWarmAttachFailClosedWithoutAuthenticatedProof(t *testing.T) {
	root := t.TempDir()
	session := isolatedActivationSession(t, root, "ses_20260716T120000Z_0123456789abcdef")
	runner := &activationCommandRunner{}
	setup := &activationProofRunner{err: errors.New("root SSH unavailable")}
	b := Backend{Runner: runner, SetupRunner: setup, ControlStdout: io.Discard, ControlStderr: io.Discard}
	if err := b.Activate(context.Background(), session, nil); err == nil || !strings.Contains(err.Error(), "session isolation primitives unavailable") {
		t.Fatalf("full activation error=%v", err)
	}
	if session.RuntimeReady {
		t.Fatal("failed primitive proof left runtime ready")
	}
	if _, err := backend.LoadActivationReceipt(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed proof wrote activation receipt: %v", err)
	}

	runner.calls = nil
	session.ActivationOwnerID = "ses_20260716T115959Z_aaaaaaaaaaaaaaaa"
	if err := b.WarmActivate(context.Background(), session, nil); err == nil || !strings.Contains(err.Error(), "receipt") {
		t.Fatalf("warm attach without receipt error=%v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("warm attach fell back to an unproved running instance: %q", runner.calls)
	}
}

func TestLimaWarmAttachDefersBootIdentityCheckToTrustedSessionView(t *testing.T) {
	root := t.TempDir()
	first := isolatedActivationSession(t, root, "ses_20260716T120000Z_0123456789abcdef")
	runner := &activationCommandRunner{}
	setup := &activationProofRunner{bootID: "01234567-89ab-cdef-0123-456789abcdef"}
	b := Backend{Runner: runner, SetupRunner: setup, ControlStdout: io.Discard, ControlStderr: io.Discard}
	if err := b.Activate(context.Background(), first, nil); err != nil {
		t.Fatal(err)
	}
	second := isolatedActivationSession(t, root, "ses_20260716T120001Z_fedcba9876543210")
	second.ActivationOwnerID = first.ID
	runner.listOutput = second.InstanceName + "\n"
	setup.bootID = "11111111-2222-3333-4444-555555555555"
	if err := b.WarmActivate(context.Background(), second, nil); err != nil {
		t.Fatal(err)
	}
	if second.ExpectedBootID != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Fatalf("warm attach trusted a fresh unbound boot id: %q", second.ExpectedBootID)
	}
	command, err := BuildSessionViewCommand(SessionViewSpec{
		SessionID: second.ID, TargetUser: "developer", GuestWork: "/workspace",
		Command: []string{"true"}, ExpectedBootID: second.ExpectedBootID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command[9], "cat /proc/sys/kernel/random/boot_id") ||
		!strings.Contains(command[9], second.ExpectedBootID) ||
		!strings.Contains(command[9], "exit 125") {
		t.Fatalf("session view does not fail before target on changed boot:\n%s", command[9])
	}
}

func isolatedActivationSession(t *testing.T, root, id string) *backend.Session {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "lima.yaml")
	if err := os.WriteFile(config, []byte("vmType: vz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &backend.Session{
		ID: id, EnvironmentID: "env_20260716t120000z0123456789abcdef",
		InstanceName: "hideout-default-env-test", ConfigPath: config,
		RuntimeRoot: root, PreserveInstance: true, SessionIsolationRequired: true,
	}
}

type activationCommandRunner struct {
	listOutput string
	calls      [][]string
}

func (r *activationCommandRunner) LookPath(string) (string, error) {
	return "/opt/homebrew/bin/limactl", nil
}

func (r *activationCommandRunner) Run(_ context.Context, _ string, args []string, _ []string, _ io.Reader, stdout, _ io.Writer) error {
	r.calls = append(r.calls, append([]string(nil), args...))
	if len(args) == 2 && args[0] == "list" && args[1] == "--quiet" {
		_, _ = io.WriteString(stdout, r.listOutput)
	}
	return nil
}

type activationProofRunner struct {
	bootID string
	err    error
	calls  int
}

func (r *activationProofRunner) Check(context.Context, string) error { return r.err }

func (r *activationProofRunner) Run(_ context.Context, _ string, _ string, _ []string, _ []string, _ io.Reader, stdout io.Writer, _ io.Writer) error {
	r.calls++
	if r.err != nil {
		return r.err
	}
	_, _ = io.WriteString(stdout, r.bootID+"\n")
	return nil
}
