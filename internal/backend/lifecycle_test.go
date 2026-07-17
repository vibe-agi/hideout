package backend

import (
	"testing"
	"time"
)

func TestLifecycleObservationValidation(t *testing.T) {
	now := time.Now().UTC()
	valid := []LifecycleObservation{
		{State: LifecycleRunning, InstanceName: "instance", BootID: "01234567-89ab-cdef-0123-456789abcdef", ObservedAt: now},
		{State: LifecycleStopped, InstanceName: "instance", ObservedAt: now},
		{State: LifecycleAbsent, InstanceName: "instance", ObservedAt: now},
		{State: LifecycleUnknown, InstanceName: "instance", ObservedAt: now, ReasonCode: "inventory-unavailable"},
	}
	for _, observation := range valid {
		if err := observation.Validate(); err != nil {
			t.Fatalf("%+v: %v", observation, err)
		}
	}
	invalid := []LifecycleObservation{
		{State: LifecycleRunning, InstanceName: "instance", ObservedAt: now},
		{State: LifecycleStopped, InstanceName: "instance", BootID: "01234567-89ab-cdef-0123-456789abcdef", ObservedAt: now},
		{State: LifecycleUnknown, InstanceName: "instance", ObservedAt: now},
	}
	for _, observation := range invalid {
		if err := observation.Validate(); err == nil {
			t.Fatalf("invalid observation accepted: %+v", observation)
		}
	}
}
