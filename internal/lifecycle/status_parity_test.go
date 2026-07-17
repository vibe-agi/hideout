package lifecycle_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/liveconsole"
)

func TestLifecycleStatusHasMachineAndEventReducerParity(t *testing.T) {
	now := time.Date(2026, 7, 16, 13, 0, 0, 0, time.UTC)
	secret := "cap_0123456789abcdef0123456789abcdef"
	status := lifecycle.BuildStatus(secret, 3, backend.LifecycleObservation{
		State: backend.LifecycleUnknown, InstanceName: "hideout-parity",
		ObservedAt: now, ReasonCode: secret,
	}, nil, []lifecycle.Fact{
		{Kind: lifecycle.KindHostAppHandoff, ID: secret, Class: lifecycle.FactHandoff, Generation: 3, RecordedAt: now},
		{Kind: lifecycle.KindHostFSStaged, ID: "overlay-one", Class: lifecycle.FactRetained, Generation: 3, RecordedAt: now},
	}, nil, lifecycle.Reconciliation{State: "blocked", ReasonCode: secret}, "unknown")

	machine := daemon.Status{
		Version: "hideout.daemon-status/v1", State: "running",
		Transport: daemon.StatusTransport{Socket: "redacted-test-socket"},
		Lifecycle: []lifecycle.Status{status},
	}
	data, err := json.Marshal(machine)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatalf("machine lifecycle status leaked injected control material: %s", data)
	}
	var decoded daemon.Status
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Lifecycle) != 1 || !reflect.DeepEqual(decoded.Lifecycle[0], status) {
		t.Fatalf("daemon machine status changed lifecycle truth: got=%+v want=%+v", decoded.Lifecycle, status)
	}

	state := liveconsole.State{}
	result := liveconsole.Apply(&state, liveconsole.Event{
		Version: liveconsole.EventVersion, Kind: liveconsole.KindLifecycle, Seq: 1,
		Entity:  liveconsole.EntityRef{Kind: liveconsole.KindLifecycle, ID: status.EnvironmentID},
		Payload: liveconsole.EventPayload{Lifecycle: &status},
	})
	if result.Status != liveconsole.ResultApplied || len(state.Lifecycle) != 1 || !reflect.DeepEqual(state.Lifecycle[0], status) {
		t.Fatalf("event reducer changed lifecycle truth: result=%+v state=%+v", result, state.Lifecycle)
	}
}
