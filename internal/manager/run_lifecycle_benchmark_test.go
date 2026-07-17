package manager

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/lifecycle"
)

func BenchmarkLifecycleWarmCommandRegistration(b *testing.B) {
	root := b.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		b.Fatal(err)
	}
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store: lifecycle.JournalStore{Root: root}, DaemonID: "daemon-benchmark", IdleGrace: time.Hour,
	})
	if err != nil {
		b.Fatal(err)
	}
	observation := backend.LifecycleObservation{
		State: backend.LifecycleRunning, InstanceName: "hideout-benchmark",
		BootID: "01234567-89ab-cdef-0123-456789abcdef", ObservedAt: time.Now().UTC(),
	}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		registration, err := coordinator.BeginAttach(context.Background(), lifecycle.AttachRequest{
			EnvironmentID: "env-benchmark", InstanceName: observation.InstanceName,
			SessionID: fmt.Sprintf("ses-%d", index+1), Observation: observation,
		})
		if err != nil {
			b.Fatal(err)
		}
		if err := registration.BindBoot(context.Background(), observation.BootID); err != nil {
			b.Fatal(err)
		}
		if err := registration.Transition(context.Background(), registration.Session(), lifecycle.StateActive); err != nil {
			b.Fatal(err)
		}
		if err := registration.Finish(context.Background(), nil); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if err := coordinator.Close(); err != nil {
		b.Fatal(err)
	}
}
