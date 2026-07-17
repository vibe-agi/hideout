package lima

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
)

const lifecycleInventoryLimit = 1 << 20

// ObserveLifecycle reports backend state without requiring a supported runtime
// package contract. It intentionally returns fixed reason codes rather than
// backend stderr.
func (b Backend) ObserveLifecycle(ctx context.Context, instanceName string) backend.LifecycleObservation {
	if ctx == nil {
		ctx = context.Background()
	}
	instanceName = strings.TrimSpace(instanceName)
	now := time.Now().UTC()
	unknown := func(code string) backend.LifecycleObservation {
		return backend.LifecycleObservation{State: backend.LifecycleUnknown, InstanceName: instanceName, ObservedAt: now, ReasonCode: code}
	}
	if instanceName == "" {
		return unknown("instance-name-invalid")
	}
	capture := &boundedRuntimeCapture{limit: lifecycleInventoryLimit}
	if err := b.runner().Run(ctx, b.limactl(), []string{"list", "--format", "json", "--all-fields"}, HostCommandEnv(os.Environ()), nil, capture, io.Discard); err != nil {
		return unknown("inventory-unavailable")
	}
	if capture.truncated {
		return unknown("inventory-oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(capture.buf.Bytes()))
	var matches []runtimeLimaInstance
	for {
		var info runtimeLimaInstance
		if err := decoder.Decode(&info); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return unknown("inventory-malformed")
		}
		if info.Name == instanceName {
			matches = append(matches, info)
		}
	}
	if len(matches) == 0 {
		return backend.LifecycleObservation{State: backend.LifecycleAbsent, InstanceName: instanceName, ObservedAt: now}
	}
	if len(matches) != 1 {
		return unknown("inventory-ambiguous")
	}
	switch strings.ToLower(strings.TrimSpace(matches[0].Status)) {
	case "stopped":
		return backend.LifecycleObservation{State: backend.LifecycleStopped, InstanceName: instanceName, ObservedAt: now}
	case "running":
		session := &backend.Session{ID: "lifecycle-observer", EnvironmentID: "lifecycle-observer", InstanceName: instanceName, GuestWork: "/"}
		bootID, err := b.runtimeGuestFact(ctx, b.runner(), HostCommandEnv(os.Environ()), session, []string{"cat", "/proc/sys/kernel/random/boot_id"})
		if err != nil {
			return unknown("boot-id-unavailable")
		}
		observation := backend.LifecycleObservation{State: backend.LifecycleRunning, InstanceName: instanceName, BootID: bootID, ObservedAt: now}
		if err := observation.Validate(); err != nil {
			return unknown("boot-id-malformed")
		}
		return observation
	default:
		return unknown("backend-state-unknown")
	}
}
