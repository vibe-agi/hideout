//go:build linux

package bpf

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
)

var ErrProcessCollectorTarget = errors.New("workload observer process collector target is invalid")

type ProcessEventReader struct {
	manifest ArtifactManifest
	objects  observerObjects
	reader   *ringbuf.Reader
	links    []link.Link
	hooks    []string

	closeOnce sync.Once
	closeErr  error
}

type ProcessCollectorCounters struct {
	MatchedEvents  uint64
	ReservedEvents uint64
	RingbufDrops   uint64
	StateDrops     uint64
}

// OpenProcessEventReader verifies the embedded object, binds its immutable
// cgroup selector before kernel load, and attaches the complete process hook
// set. It returns no reader if even one required hook is unavailable, so a
// caller cannot accidentally claim Available process coverage from a partial
// execution tree.
func OpenProcessEventReader(targetCgroupID uint64) (*ProcessEventReader, error) {
	if targetCgroupID == 0 {
		return nil, ErrProcessCollectorTarget
	}
	spec, manifest, err := LoadEmbeddedSpec()
	if err != nil {
		return nil, err
	}
	target := spec.Variables[observerVarTargetCgroupId]
	if target == nil || !target.Constant() {
		return nil, errors.New("workload observer target_cgroup_id constant is missing")
	}
	if err := target.Set(targetCgroupID); err != nil {
		return nil, fmt.Errorf("set workload observer cgroup target: %w", err)
	}

	result := &ProcessEventReader{manifest: manifest}
	if err := spec.LoadAndAssign(&result.objects, nil); err != nil {
		return nil, errors.Join(
			fmt.Errorf("load workload observer process programs: %w", err),
			closeObserverObjects(&result.objects),
		)
	}
	result.reader, err = ringbuf.NewReader(result.objects.ObservationEvents)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("open workload observer process ring: %w", err),
			closeObserverObjects(&result.objects),
		)
	}

	hooks := []struct {
		kind    string
		group   string
		name    string
		program *ebpf.Program
	}{
		{kind: "tracepoint", group: "syscalls", name: "sys_enter_execve", program: result.objects.HideoutCaptureExecArgv},
		{kind: "tracepoint", group: "syscalls", name: "sys_enter_execveat", program: result.objects.HideoutCaptureExecveatArgv},
		{kind: "raw_tracepoint", name: "sched_process_fork", program: result.objects.HideoutObserveProcessFork},
		{kind: "raw_tracepoint", name: "sched_process_exec", program: result.objects.HideoutObserveProcessExec},
		{kind: "raw_tracepoint", name: "sched_process_exit", program: result.objects.HideoutObserveProcessExit},
	}
	for _, hook := range hooks {
		var attached link.Link
		var attachErr error
		hookName := hook.kind + "/" + hook.name
		if hook.kind == "tracepoint" {
			hookName = hook.kind + "/" + hook.group + "/" + hook.name
			attached, attachErr = link.Tracepoint(
				hook.group,
				hook.name,
				hook.program,
				nil,
			)
		} else {
			attached, attachErr = link.AttachRawTracepoint(
				link.RawTracepointOptions{
					Name:    hook.name,
					Program: hook.program,
				},
			)
		}
		if attachErr != nil {
			return nil, errors.Join(
				fmt.Errorf(
					"attach workload observer %s: %w",
					hookName,
					attachErr,
				),
				result.Close(),
			)
		}
		result.links = append(result.links, attached)
		result.hooks = append(result.hooks, hookName)
	}
	return result, nil
}

func (reader *ProcessEventReader) ReadProcessEvent() (RawProcessEvent, error) {
	if reader == nil || reader.reader == nil {
		return RawProcessEvent{}, ringbuf.ErrClosed
	}
	record, err := reader.reader.Read()
	if err != nil {
		return RawProcessEvent{}, err
	}
	return DecodeProcessEvent(record.RawSample)
}

func (reader *ProcessEventReader) SetDeadline(deadline time.Time) {
	if reader != nil && reader.reader != nil {
		reader.reader.SetDeadline(deadline)
	}
}

func (reader *ProcessEventReader) AttachedHooks() []string {
	if reader == nil {
		return nil
	}
	return append([]string(nil), reader.hooks...)
}

func (reader *ProcessEventReader) ArtifactManifest() ArtifactManifest {
	if reader == nil {
		return ArtifactManifest{}
	}
	return reader.manifest
}

func (reader *ProcessEventReader) Counters() (ProcessCollectorCounters, error) {
	if reader == nil || reader.objects.ProcessCounters == nil {
		return ProcessCollectorCounters{}, ringbuf.ErrClosed
	}
	cpus, err := ebpf.PossibleCPU()
	if err != nil {
		return ProcessCollectorCounters{}, err
	}
	values := make([]observerCollectorCounters, cpus)
	if err := reader.objects.ProcessCounters.Lookup(uint32(0), &values); err != nil {
		return ProcessCollectorCounters{}, err
	}
	var result ProcessCollectorCounters
	for _, value := range values {
		if ^uint64(0)-result.MatchedEvents < value.MatchedEvents ||
			^uint64(0)-result.ReservedEvents < value.ReservedEvents ||
			^uint64(0)-result.RingbufDrops < value.RingbufDrops ||
			^uint64(0)-result.StateDrops < value.StateDrops {
			return ProcessCollectorCounters{}, errors.New("workload observer process counters overflow")
		}
		result.MatchedEvents += value.MatchedEvents
		result.ReservedEvents += value.ReservedEvents
		result.RingbufDrops += value.RingbufDrops
		result.StateDrops += value.StateDrops
	}
	return result, nil
}

func (reader *ProcessEventReader) Close() error {
	if reader == nil {
		return nil
	}
	reader.closeOnce.Do(func() {
		if reader.reader != nil {
			reader.closeErr = errors.Join(reader.closeErr, reader.reader.Close())
		}
		for index := len(reader.links) - 1; index >= 0; index-- {
			reader.closeErr = errors.Join(reader.closeErr, reader.links[index].Close())
		}
		reader.closeErr = errors.Join(reader.closeErr, closeObserverObjects(&reader.objects))
	})
	return reader.closeErr
}

func closeObserverObjects(objects *observerObjects) error {
	if objects == nil {
		return nil
	}
	var result error
	if objects.HideoutCaptureExecArgv != nil {
		result = errors.Join(result, objects.HideoutCaptureExecArgv.Close())
	}
	if objects.HideoutCaptureExecveatArgv != nil {
		result = errors.Join(result, objects.HideoutCaptureExecveatArgv.Close())
	}
	if objects.HideoutObserveProcessExec != nil {
		result = errors.Join(result, objects.HideoutObserveProcessExec.Close())
	}
	if objects.HideoutObserveProcessExit != nil {
		result = errors.Join(result, objects.HideoutObserveProcessExit.Close())
	}
	if objects.HideoutObserveProcessFork != nil {
		result = errors.Join(result, objects.HideoutObserveProcessFork.Close())
	}
	if objects.ExecSequences != nil {
		result = errors.Join(result, objects.ExecSequences.Close())
	}
	if objects.ForkParents != nil {
		result = errors.Join(result, objects.ForkParents.Close())
	}
	if objects.NextExecSequence != nil {
		result = errors.Join(result, objects.NextExecSequence.Close())
	}
	if objects.ObservationEvents != nil {
		result = errors.Join(result, objects.ObservationEvents.Close())
	}
	if objects.ObserverSequences != nil {
		result = errors.Join(result, objects.ObserverSequences.Close())
	}
	if objects.PendingExecScratch != nil {
		result = errors.Join(result, objects.PendingExecScratch.Close())
	}
	if objects.PendingExecs != nil {
		result = errors.Join(result, objects.PendingExecs.Close())
	}
	if objects.ProcessCounters != nil {
		result = errors.Join(result, objects.ProcessCounters.Close())
	}
	return result
}
