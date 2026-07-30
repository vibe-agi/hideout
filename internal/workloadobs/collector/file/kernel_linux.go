//go:build linux

package file

import (
	"errors"
	"time"

	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
)

var (
	ErrKernelEventSourceClosed = errors.New("workload observer file event source is closed")
	ErrKernelEventSourceConfig = errors.New("workload observer file event source configuration is invalid")
)

// KernelEventSource owns the file observer reader while sharing execution and
// sequence maps with the already-open process observer. It deliberately does
// not own processReader; callers must close the file source before closing the
// process reader whose maps it references.
type KernelEventSource struct {
	boundary   Boundary
	anchor     ClockAnchor
	coverageID string
	lookup     ExecutionLookup
	classify   PathClassifier
	reader     *observerbpf.FileEventReader
}

func OpenKernelEventSource(
	boundary Boundary,
	processReader *observerbpf.ProcessEventReader,
	anchor ClockAnchor,
	coverageID string,
	lookup ExecutionLookup,
	classify PathClassifier,
) (*KernelEventSource, error) {
	if err := boundary.Validate(); err != nil {
		return nil, err
	}
	if anchor.Validate() != nil ||
		!validKernelCoverageID(coverageID) ||
		lookup == nil {
		return nil, ErrKernelEventSourceConfig
	}
	reader, err := observerbpf.OpenFileEventReader(
		boundary.CgroupID,
		processReader,
	)
	if err != nil {
		return nil, err
	}
	return &KernelEventSource{
		boundary: boundary, anchor: anchor, coverageID: coverageID,
		lookup: lookup, classify: classify, reader: reader,
	}, nil
}

func (source *KernelEventSource) Read() (Event, error) {
	if source == nil || source.reader == nil {
		return Event{}, ErrKernelEventSourceClosed
	}
	raw, err := source.reader.ReadFileEvent()
	if err != nil {
		return Event{}, err
	}
	return EventFromKernelRecord(
		source.boundary,
		source.anchor,
		source.coverageID,
		raw,
		source.lookup,
		source.classify,
	)
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

func (source *KernelEventSource) Counters() (observerbpf.FileCollectorCounters, error) {
	if source == nil || source.reader == nil {
		return observerbpf.FileCollectorCounters{}, ErrKernelEventSourceClosed
	}
	return source.reader.Counters()
}

func (source *KernelEventSource) Close() error {
	if source == nil || source.reader == nil {
		return nil
	}
	return source.reader.Close()
}

func validKernelCoverageID(value string) bool {
	if len(value) < len("cov_")+8 || len(value) > 128 ||
		value[:len("cov_")] != "cov_" {
		return false
	}
	for _, current := range value[len("cov_"):] {
		if (current >= 'a' && current <= 'z') ||
			(current >= 'A' && current <= 'Z') ||
			(current >= '0' && current <= '9') ||
			current == '_' || current == '-' {
			continue
		}
		return false
	}
	return true
}
