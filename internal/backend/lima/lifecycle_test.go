package lima

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
)

type lifecycleRunner struct {
	status string
	bootID string
	err    error
	calls  [][]string
}

func (r *lifecycleRunner) Run(_ context.Context, _ string, args []string, _ []string, _ io.Reader, stdout, _ io.Writer) error {
	r.calls = append(r.calls, append([]string(nil), args...))
	if r.err != nil {
		return r.err
	}
	if len(args) > 0 && args[0] == "list" {
		if r.status == "absent" {
			return nil
		}
		_, _ = io.WriteString(stdout, `{"name":"instance","status":"`+r.status+`"}`)
		return nil
	}
	_, _ = io.WriteString(stdout, r.bootID+"\n")
	return nil
}

func (*lifecycleRunner) LookPath(file string) (string, error) { return file, nil }

func TestObserveLifecycleStatesAndBootIdentity(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   backend.LifecycleState
	}{
		{"Running", backend.LifecycleRunning},
		{"Stopped", backend.LifecycleStopped},
		{"absent", backend.LifecycleAbsent},
		{"Broken", backend.LifecycleUnknown},
	} {
		runner := &lifecycleRunner{status: tc.status, bootID: "01234567-89ab-cdef-0123-456789abcdef"}
		observation := (Backend{Runner: runner}).ObserveLifecycle(context.Background(), "instance")
		if observation.State != tc.want {
			t.Fatalf("status %s: got %+v", tc.status, observation)
		}
		if tc.want == backend.LifecycleRunning {
			if observation.BootID != runner.bootID || len(runner.calls) != 2 || !slices.Contains(runner.calls[1], "/proc/sys/kernel/random/boot_id") {
				t.Fatalf("running observation did not independently read boot id: %+v calls=%v", observation, runner.calls)
			}
		}
	}
}

func TestObserveLifecycleReturnsBoundedUnknownReason(t *testing.T) {
	runner := &lifecycleRunner{err: errors.New("secret backend stderr /Users/alice")}
	observation := (Backend{Runner: runner}).ObserveLifecycle(context.Background(), "instance")
	if observation.State != backend.LifecycleUnknown || observation.ReasonCode != "inventory-unavailable" {
		t.Fatalf("unexpected observation: %+v", observation)
	}
	if strings.Contains(observation.ReasonCode, "alice") {
		t.Fatal("backend detail leaked into observation")
	}
}
