//go:build linux

package process

import (
	"errors"
	"time"

	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
)

var ErrKernelEventSourceClosed = errors.New("workload observer process event source is closed")

type KernelEventSource struct {
	boundary Boundary
	reader   *observerbpf.ProcessEventReader
}

func OpenKernelEventSource(boundary Boundary) (*KernelEventSource, error) {
	if err := boundary.Validate(); err != nil {
		return nil, err
	}
	reader, err := observerbpf.OpenProcessEventReader(boundary.CgroupID)
	if err != nil {
		return nil, err
	}
	return &KernelEventSource{boundary: boundary, reader: reader}, nil
}

func (source *KernelEventSource) Read() (Event, error) {
	if source == nil || source.reader == nil {
		return Event{}, ErrKernelEventSourceClosed
	}
	raw, err := source.reader.ReadProcessEvent()
	if err != nil {
		return Event{}, err
	}
	return EventFromKernelRecord(source.boundary, raw)
}

func (source *KernelEventSource) SetDeadline(deadline time.Time) {
	if source != nil && source.reader != nil {
		source.reader.SetDeadline(deadline)
	}
}

func (source *KernelEventSource) AttachedHooks() []string {
	if source == nil || source.reader == nil {
		return nil
	}
	return source.reader.AttachedHooks()
}

func (source *KernelEventSource) ArtifactManifest() observerbpf.ArtifactManifest {
	if source == nil || source.reader == nil {
		return observerbpf.ArtifactManifest{}
	}
	return source.reader.ArtifactManifest()
}

func (source *KernelEventSource) Counters() (observerbpf.ProcessCollectorCounters, error) {
	if source == nil || source.reader == nil {
		return observerbpf.ProcessCollectorCounters{}, ErrKernelEventSourceClosed
	}
	return source.reader.Counters()
}

func (source *KernelEventSource) Close() error {
	if source == nil || source.reader == nil {
		return nil
	}
	return source.reader.Close()
}
