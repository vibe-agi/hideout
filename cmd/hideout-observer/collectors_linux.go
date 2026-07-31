//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/rlimit"
	"github.com/vibe-agi/hideout/internal/sessionwire"
	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
	dnscollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/dns"
	filecollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/file"
	networkcollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/network"
	processcollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/process"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	observerProcessCoverageID = "cov_observerprocess1"
	observerFileCoverageID    = "cov_observerfile0001"
	observerNetworkCoverageID = "cov_observernetwork1"
	observerDNSCoverageID     = "cov_observerdns00001"

	observerExecutionWait = 50 * time.Millisecond
	observerRetryInterval = time.Millisecond
	observerDrainWait     = time.Second
)

type linuxObserverCollectors struct {
	capabilities sessionwire.ObserverCapabilities

	processReader     *observerbpf.ProcessEventReader
	fileReader        observerFileReader
	networkReader     *observerbpf.NetworkEventReader
	processNormalizer *processcollector.Normalizer
	fileNormalizer    *filecollector.Normalizer
	networkCorrelator *networkcollector.Correlator
	dnsParser         *dnscollector.Parser

	processBoundary processcollector.Boundary
	fileBoundary    filecollector.Boundary
	networkBoundary networkcollector.Boundary
	processAnchor   processcollector.ClockAnchor
	fileAnchor      filecollector.ClockAnchor
	networkAnchor   networkcollector.ClockAnchor
	dnsAnchor       dnscollector.ClockAnchor

	startOnce        sync.Once
	stopOnce         sync.Once
	closeOnce        sync.Once
	wait             sync.WaitGroup
	errs             chan error
	draining         atomic.Bool
	closed           atomic.Bool
	localProcessDrop atomic.Uint64
	localFileDrop    atomic.Uint64
	localNetworkDrop atomic.Uint64
	localDNSDrop     atomic.Uint64
	stopErr          error
	closeErr         error
}

type observerFileReader interface {
	ReadFileEventInto(*observerbpf.RawFileEvent) error
	SetDeadline(time.Time)
	Counters() (observerbpf.FileCollectorCounters, error)
	FlushPending() error
	Stop() error
	Close() error
}

func openObserverCollectors(
	config observerCollectorConfig,
) (observerCollectorRuntime, error) {
	if err := config.Binding.Validate(); err != nil ||
		config.Anchor.WallTime.IsZero() ||
		config.Anchor.MonotonicNS == 0 {
		return nil, errors.New("observer collector configuration is invalid")
	}
	if _, err := observerbpf.VerifyEmbeddedArtifacts(); err != nil {
		return nil, errors.New("observer embedded collector verification failed")
	}
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, errors.New("observer kernel memory lock setup failed")
	}
	baseEvidence := []string{"cgroup-v2", "core.object-verified"}
	if _, err := os.Stat("/sys/kernel/btf/vmlinux"); err == nil {
		baseEvidence = append(baseEvidence, "btf.present")
	}
	runtime := &linuxObserverCollectors{
		errs: make(chan error, 1),
		capabilities: sessionwire.ObserverCapabilities{
			Process: unavailableCapability(
				"process-collector-load-failed",
				baseEvidence,
			),
			File: unavailableCapability(
				"process-state-unavailable",
				baseEvidence,
			),
			Network: unavailableCapability(
				"process-state-unavailable",
				baseEvidence,
			),
			DNS: unavailableCapability(
				"process-state-unavailable",
				baseEvidence,
			),
		},
		processBoundary: processcollector.Boundary{
			Owner:              config.Binding.Owner,
			SessionID:          config.Binding.SessionID,
			GuestBootID:        config.Binding.GuestBootID,
			CgroupID:           config.Binding.CgroupID,
			ObserverGeneration: config.Binding.ObserverGeneration,
		},
		fileBoundary: filecollector.Boundary{
			Owner:              config.Binding.Owner,
			SessionID:          config.Binding.SessionID,
			CgroupID:           config.Binding.CgroupID,
			ObserverGeneration: config.Binding.ObserverGeneration,
		},
		networkBoundary: networkcollector.Boundary{
			Owner:              config.Binding.Owner,
			SessionID:          config.Binding.SessionID,
			CgroupID:           config.Binding.CgroupID,
			ObserverGeneration: config.Binding.ObserverGeneration,
		},
		processAnchor: processcollector.ClockAnchor{
			WallTime:    config.Anchor.WallTime,
			MonotonicNS: config.Anchor.MonotonicNS,
		},
		fileAnchor: filecollector.ClockAnchor{
			WallTime:    config.Anchor.WallTime,
			MonotonicNS: config.Anchor.MonotonicNS,
		},
		networkAnchor: networkcollector.ClockAnchor{
			WallTime:    config.Anchor.WallTime,
			MonotonicNS: config.Anchor.MonotonicNS,
		},
		dnsAnchor: dnscollector.ClockAnchor{
			WallTime:    config.Anchor.WallTime,
			MonotonicNS: config.Anchor.MonotonicNS,
		},
	}
	var err error
	runtime.processNormalizer, err = processcollector.NewNormalizer(
		runtime.processBoundary,
		runtime.processAnchor,
	)
	if err != nil {
		return nil, err
	}
	runtime.fileNormalizer, err = filecollector.NewNormalizer(
		runtime.fileBoundary,
	)
	if err != nil {
		return nil, err
	}
	runtime.networkCorrelator, err = networkcollector.NewCorrelator(
		runtime.networkBoundary,
		networkcollector.Options{MaxDNSLifetime: 24 * time.Hour},
	)
	if err != nil {
		return nil, err
	}
	runtime.dnsParser, err = dnscollector.NewParser(
		runtime.networkBoundary,
		dnscollector.Options{},
	)
	if err != nil {
		return nil, err
	}

	runtime.processReader, err = observerbpf.OpenProcessEventReader(
		config.Binding.CgroupID,
	)
	if err != nil {
		runtime.capabilities.Process.Reason = collectorFailureReason(
			"process",
			err,
		)
		runtime.capabilities.Process.Evidence = appendEvidence(
			runtime.capabilities.Process.Evidence,
			collectorFailureEvidence("process", err)...,
		)
		return runtime, nil
	}
	runtime.capabilities.Process = sessionwire.ObserverCapability{
		State: workloadtypes.CoverageAvailable,
		Evidence: appendEvidence(
			baseEvidence,
			"tracepoint.exec-argv",
			"raw-tracepoint.sched-process-fork",
			"raw-tracepoint.sched-process-exec",
			"raw-tracepoint.sched-process-exit",
		),
	}

	runtime.fileReader, err = observerbpf.OpenFileEventReader(
		config.Binding.CgroupID,
		runtime.processReader,
	)
	if err != nil {
		runtime.capabilities.File = unavailableCapability(
			collectorFailureReason("file", err),
			appendEvidence(
				baseEvidence,
				collectorFailureEvidence("file", err)...,
			),
		)
	} else {
		runtime.capabilities.File = sessionwire.ObserverCapability{
			State:  workloadtypes.CoveragePartial,
			Reason: "file-operation-coverage-partial",
			Evidence: appendEvidence(
				baseEvidence,
				"fentry.vfs",
				"fentry.security-wrapper",
				"fexit.security-wrapper",
				"bpf-lsm-active-not-required",
				"tracepoint.file",
				"path.security-wrapper-alias",
				"path-operation-outcomes-partial",
				"path.inherited-fd-unavailable",
				"system-runtime-read-noise-filtered",
			),
		}
	}

	runtime.networkReader, err = observerbpf.OpenNetworkEventReader(
		config.CgroupPath,
		config.Binding.CgroupID,
		runtime.processReader,
	)
	if err != nil {
		runtime.capabilities.Network = unavailableCapability(
			collectorFailureReason("network", err),
			appendEvidence(
				baseEvidence,
				collectorFailureEvidence("network", err)...,
			),
		)
		runtime.capabilities.DNS = unavailableCapability(
			"network-state-unavailable",
			baseEvidence,
		)
	} else {
		runtime.capabilities.Network = sessionwire.ObserverCapability{
			State:  workloadtypes.CoveragePartial,
			Reason: "route-attribution-unavailable",
			Evidence: appendEvidence(
				baseEvidence,
				"cgroup.connect4",
				"cgroup.connect6",
				"route.unavailable",
				"socket-cookie",
			),
		}
		runtime.capabilities.DNS = sessionwire.ObserverCapability{
			State:  workloadtypes.CoveragePartial,
			Reason: "encrypted-dns-metadata-unavailable",
			Evidence: appendEvidence(
				baseEvidence,
				"dns.encrypted-unavailable",
				"dns.plaintext",
				"socket-cookie",
			),
		}
	}
	if err := runtime.capabilities.Validate(); err != nil {
		_ = runtime.Close()
		return nil, err
	}
	return runtime, nil
}

func unavailableCapability(
	reason string,
	evidence []string,
) sessionwire.ObserverCapability {
	return sessionwire.ObserverCapability{
		State:    workloadtypes.CoverageUnavailable,
		Reason:   reason,
		Evidence: append([]string(nil), evidence...),
	}
}

func collectorFailureReason(subsystem string, err error) string {
	var verifier *ebpf.VerifierError
	switch {
	case errors.As(err, &verifier):
		return subsystem + "-collector-verifier-rejected"
	case errors.Is(err, os.ErrPermission):
		return subsystem + "-collector-permission-denied"
	case errors.Is(err, os.ErrNotExist):
		return subsystem + "-collector-hook-unavailable"
	case errors.Is(err, os.ErrInvalid):
		return subsystem + "-collector-kernel-incompatible"
	case strings.Contains(err.Error(), "load workload observer"):
		return subsystem + "-collector-program-load-failed"
	case strings.Contains(err.Error(), "open workload observer") &&
		strings.Contains(err.Error(), "ring"):
		return subsystem + "-collector-ring-open-failed"
	case strings.Contains(err.Error(), "attach workload observer"):
		return subsystem + "-collector-hook-attach-failed"
	case strings.Contains(err.Error(), "cgroup target"):
		return subsystem + "-collector-target-bind-failed"
	default:
		return subsystem + "-collector-load-failed"
	}
}

// collectorFailureEvidence exposes only a bounded, allow-listed attachment
// point. Raw kernel errors can contain host paths or implementation details and
// therefore must not cross the observer protocol or enter retained activity.
func collectorFailureEvidence(subsystem string, err error) []string {
	if err == nil {
		return nil
	}
	message := err.Error()
	type attachment struct {
		subsystem string
		marker    string
		evidence  string
	}
	for _, candidate := range []attachment{
		{"process", "tracepoint syscalls/sys_enter_execve:", "tracepoint.syscalls.sys_enter_execve.attach-failed"},
		{"process", "tracepoint syscalls/sys_enter_execveat:", "tracepoint.syscalls.sys_enter_execveat.attach-failed"},
		{"process", "tracepoint sched/sched_process_fork:", "tracepoint.sched.sched_process_fork.attach-failed"},
		{"process", "tracepoint sched/sched_process_exec:", "tracepoint.sched.sched_process_exec.attach-failed"},
		{"process", "tracepoint sched/sched_process_exit:", "tracepoint.sched.sched_process_exit.attach-failed"},
		{"process", "raw_tracepoint/sched_process_fork:", "raw-tracepoint.sched-process-fork.attach-failed"},
		{"process", "raw_tracepoint/sched_process_exec:", "raw-tracepoint.sched-process-exec.attach-failed"},
		{"process", "raw_tracepoint/sched_process_exit:", "raw-tracepoint.sched-process-exit.attach-failed"},
	} {
		if subsystem == candidate.subsystem &&
			strings.Contains(message, candidate.marker) {
			return []string{candidate.evidence}
		}
	}
	return nil
}

func appendEvidence(base []string, extra ...string) []string {
	result := append([]string(nil), base...)
	result = append(result, extra...)
	return result
}

func (collectors *linuxObserverCollectors) Capabilities() sessionwire.ObserverCapabilities {
	if collectors == nil {
		return sessionwire.ObserverCapabilities{}
	}
	return collectors.capabilities
}

func (collectors *linuxObserverCollectors) Start(
	ctx context.Context,
	sink observerRecordSink,
) {
	if collectors == nil || ctx == nil || sink == nil {
		return
	}
	collectors.startOnce.Do(func() {
		if collectors.processReader != nil {
			collectors.spawn(func() {
				collectors.readProcess(ctx, sink)
			})
		}
		if collectors.fileReader != nil {
			collectors.spawn(func() {
				collectors.readFile(ctx, sink)
			})
		}
		if collectors.networkReader != nil {
			collectors.spawn(func() {
				collectors.readNetwork(ctx, sink)
			})
			collectors.spawn(func() {
				collectors.readDNS(ctx, sink)
			})
		}
	})
}

func (collectors *linuxObserverCollectors) spawn(run func()) {
	collectors.wait.Add(1)
	go func() {
		defer collectors.wait.Done()
		run()
	}()
}

func (collectors *linuxObserverCollectors) Errors() <-chan error {
	if collectors == nil || collectors.errs == nil {
		return nil
	}
	return collectors.errs
}

func (collectors *linuxObserverCollectors) readProcess(
	ctx context.Context,
	sink observerRecordSink,
) {
	for !collectors.stopping(ctx) {
		raw, err := collectors.processReader.ReadProcessEvent()
		if err != nil {
			if errors.Is(err, observerbpf.ErrProcessRecord) {
				collectors.noteDrop("process")
				continue
			}
			collectors.failRead(ctx, "process")
			return
		}
		event, err := processcollector.EventFromKernelRecord(
			collectors.processBoundary,
			raw,
		)
		if err != nil {
			collectors.noteDrop("process")
			continue
		}
		var previous workloadtypes.Execution
		previousPresent := false
		if event.Kind == processcollector.EventExec {
			previous, previousPresent =
				collectors.processNormalizer.LookupCurrentExecution(event.PID)
		}
		if err := collectors.processNormalizer.Apply(event); err != nil {
			collectors.noteDrop("process")
			continue
		}
		if previousPresent {
			updated, ok := collectors.processNormalizer.LookupExecution(
				previous.PID,
				previous.ExecSequence,
			)
			if !ok || updated.Exit == nil ||
				updated.Exit.AtMonoNS != event.MonotonicNS {
				collectors.noteDrop("process")
				continue
			}
			if err := sink(observerRecord{
				Execution:   &updated,
				CPU:         event.CPU,
				MonotonicNS: event.MonotonicNS,
			}); err != nil {
				collectors.fail(err)
				return
			}
		}
		if event.Kind == processcollector.EventExit {
			updated, ok := collectors.processNormalizer.LookupExecution(
				event.PID,
				event.ExecSequence,
			)
			if !ok || updated.Exit == nil {
				collectors.noteDrop("process")
				continue
			}
			if err := sink(observerRecord{
				Execution:   &updated,
				CPU:         event.CPU,
				MonotonicNS: event.MonotonicNS,
			}); err != nil {
				collectors.fail(err)
				return
			}
			continue
		}
		if event.Kind != processcollector.EventExec {
			continue
		}
		execution, ok := collectors.processNormalizer.LookupExecution(
			event.PID,
			event.ExecSequence,
		)
		if !ok {
			collectors.noteDrop("process")
			continue
		}
		if err := sink(observerRecord{
			Execution:   &execution,
			CPU:         event.CPU,
			MonotonicNS: event.MonotonicNS,
		}); err != nil {
			collectors.fail(err)
			return
		}
		record, err := collectors.processNormalizer.ExecActivity(
			event,
			observerProcessCoverageID,
		)
		if err != nil {
			collectors.noteDrop("process")
			continue
		}
		if err := sink(observerRecord{
			Record:      record,
			CPU:         event.CPU,
			MonotonicNS: event.MonotonicNS,
		}); err != nil {
			collectors.fail(err)
			return
		}
	}
}

func (collectors *linuxObserverCollectors) readFile(
	ctx context.Context,
	sink observerRecordSink,
) {
	aggregator := newFileObservationAggregator()
	var raw observerbpf.RawFileEvent
	flush := func() bool {
		if err := aggregator.Flush(
			collectors.fileNormalizer,
			sink,
		); err != nil {
			collectors.fail(err)
			return false
		}
		return true
	}
	flushAt := time.Now().Add(observerFileAggregationWindow)
	collectors.fileReader.SetDeadline(flushAt)
	defer collectors.fileReader.SetDeadline(time.Time{})

	lookup := func(pid uint32, sequence uint64) (string, bool) {
		return collectors.processNormalizer.LookupExecutionID(
			pid,
			sequence,
		)
	}
	for !collectors.stopping(ctx) {
		if !time.Now().Before(flushAt) {
			if !flush() {
				return
			}
			flushAt = time.Now().Add(observerFileAggregationWindow)
			collectors.fileReader.SetDeadline(flushAt)
		}
		err := collectors.fileReader.ReadFileEventInto(&raw)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				if !flush() {
					return
				}
				if collectors.draining.Load() {
					// The aggregation deadline and FlushPending can become ready
					// together. A deadline is not the shutdown boundary: clear the
					// expired timer and continue until Read returns ErrFlushed after
					// every record visible at the flush call.
					collectors.fileReader.SetDeadline(time.Time{})
					continue
				}
				flushAt = time.Now().Add(observerFileAggregationWindow)
				collectors.fileReader.SetDeadline(flushAt)
				continue
			}
			if errors.Is(err, observerbpf.ErrFileRecord) {
				collectors.noteDrop("file")
				continue
			}
			if !flush() {
				return
			}
			collectors.failRead(ctx, "file")
			return
		}
		if raw.ExecutionPID != 0 && raw.ExecSequence != 0 {
			collectors.waitForExecution(
				ctx,
				raw.ExecutionPID,
				raw.ExecSequence,
			)
		}
		event, err := filecollector.EventFromKernelRecordRef(
			collectors.fileBoundary,
			collectors.fileAnchor,
			observerFileCoverageID,
			&raw,
			lookup,
			nil,
		)
		if err != nil {
			collectors.noteDrop("file")
			continue
		}
		if !retainFileObservation(event) {
			continue
		}
		added, addErr := aggregator.Add(event)
		if addErr != nil {
			if !errors.Is(
				addErr,
				errObserverFileAggregationOverflow,
			) {
				collectors.fail(addErr)
				return
			}
			if !flush() {
				return
			}
			added, addErr = aggregator.Add(event)
			if addErr != nil || !added {
				collectors.fail(errors.Join(
					errors.New("observer file aggregation retry failed"),
					addErr,
				))
				return
			}
		}
		if added {
			continue
		}
		if !flush() {
			return
		}
		added, addErr = aggregator.Add(event)
		if addErr != nil {
			collectors.fail(addErr)
			return
		}
		if added {
			continue
		}
		if err := emitFileObservation(
			collectors.fileNormalizer,
			sink,
			event,
		); err != nil {
			collectors.fail(err)
			return
		}
	}
	flush()
}

func (collectors *linuxObserverCollectors) readNetwork(
	ctx context.Context,
	sink observerRecordSink,
) {
	for !collectors.stopping(ctx) {
		raw, err := collectors.networkReader.ReadNetworkEvent()
		if err != nil {
			if errors.Is(err, observerbpf.ErrNetworkRecord) {
				collectors.noteDrop("network")
				continue
			}
			collectors.failRead(ctx, "network")
			return
		}
		if raw.ExecutionPID != 0 && raw.ExecSequence != 0 {
			collectors.waitForExecution(
				ctx,
				raw.ExecutionPID,
				raw.ExecSequence,
			)
		}
		var evidence *observerbpf.NetworkSocketEvidence
		if value, evidenceErr := collectors.networkReader.SocketEvidence(
			raw,
		); evidenceErr == nil {
			evidence = &value
		}
		event, err := networkcollector.NormalizeKernelConnection(
			collectors.networkBoundary,
			collectors.networkAnchor,
			observerNetworkCoverageID,
			raw,
			evidence,
			collectors.processNormalizer.LookupActor,
			nil,
		)
		if err != nil {
			collectors.noteDrop("network")
			continue
		}
		record, err := collectors.networkCorrelator.NormalizeConnection(
			event,
		)
		if err != nil {
			collectors.noteDrop("network")
			continue
		}
		if err := sink(observerRecord{
			Record:      record,
			CPU:         uint64(raw.CPU),
			MonotonicNS: raw.MonotonicNS,
		}); err != nil {
			collectors.fail(err)
			return
		}
	}
}

func (collectors *linuxObserverCollectors) readDNS(
	ctx context.Context,
	sink observerRecordSink,
) {
	for !collectors.stopping(ctx) {
		raw, err := collectors.networkReader.ReadDNSPacket()
		if err != nil {
			if errors.Is(err, observerbpf.ErrDNSPacketRecord) {
				collectors.noteDrop("dns")
				continue
			}
			collectors.failRead(ctx, "dns")
			return
		}
		cpu := uint64(raw.CPU)
		monotonicNS := raw.MonotonicNS
		if raw.ExecutionPID != 0 && raw.ExecSequence != 0 {
			collectors.waitForExecution(
				ctx,
				raw.ExecutionPID,
				raw.ExecSequence,
			)
		}
		packet, err := dnscollector.PacketFromKernelRecord(
			collectors.networkBoundary,
			collectors.dnsAnchor,
			observerDNSCoverageID,
			&raw,
			collectors.processNormalizer.LookupActor,
		)
		if err != nil {
			collectors.noteDrop("dns")
			continue
		}
		event, err := collectors.dnsParser.Consume(&packet)
		if err != nil {
			if errors.Is(
				err,
				dnscollector.ErrEncryptedMetadataUnavailable,
			) {
				continue
			}
			collectors.noteDrop("dns")
			continue
		}
		if event == nil {
			continue
		}
		record, err := collectors.networkCorrelator.ObserveDNS(*event)
		if err != nil {
			collectors.noteDrop("dns")
			continue
		}
		if err := sink(observerRecord{
			Record:      record,
			CPU:         cpu,
			MonotonicNS: monotonicNS,
		}); err != nil {
			collectors.fail(err)
			return
		}
	}
}

func (collectors *linuxObserverCollectors) waitForExecution(
	ctx context.Context,
	pid uint32,
	sequence uint64,
) {
	if collectors == nil || pid == 0 || sequence == 0 {
		return
	}
	if _, ok := collectors.processNormalizer.LookupExecutionID(
		pid,
		sequence,
	); ok {
		return
	}
	deadline := time.NewTimer(observerExecutionWait)
	defer deadline.Stop()
	ticker := time.NewTicker(observerRetryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
			if _, ok := collectors.processNormalizer.LookupExecutionID(
				pid,
				sequence,
			); ok {
				return
			}
		}
	}
}

func (collectors *linuxObserverCollectors) stopping(
	ctx context.Context,
) bool {
	if collectors == nil || collectors.closed.Load() {
		return true
	}
	if collectors.draining.Load() {
		return false
	}
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

func (collectors *linuxObserverCollectors) failRead(
	ctx context.Context,
	subsystem string,
) {
	if collectors.draining.Load() || collectors.stopping(ctx) {
		return
	}
	collectors.fail(fmt.Errorf("%s collector ring became unavailable", subsystem))
}

func (collectors *linuxObserverCollectors) fail(err error) {
	if collectors == nil || err == nil ||
		collectors.draining.Load() || collectors.closed.Load() {
		return
	}
	select {
	case collectors.errs <- err:
	default:
	}
}

func (collectors *linuxObserverCollectors) noteDrop(subsystem string) {
	if collectors == nil {
		return
	}
	var target *atomic.Uint64
	switch subsystem {
	case "process":
		target = &collectors.localProcessDrop
	case "file":
		target = &collectors.localFileDrop
	case "network":
		target = &collectors.localNetworkDrop
	case "dns":
		target = &collectors.localDNSDrop
	default:
		return
	}
	for {
		current := target.Load()
		if current == math.MaxUint64 ||
			target.CompareAndSwap(current, current+1) {
			return
		}
	}
}

func (collectors *linuxObserverCollectors) Counters() (
	observerDropCounters,
	error,
) {
	if collectors == nil {
		return observerDropCounters{}, errors.New(
			"observer collectors are unavailable",
		)
	}
	result := observerDropCounters{Local: observerLocalDropCounters{
		Process: collectors.localProcessDrop.Load(),
		File:    collectors.localFileDrop.Load(),
		Network: collectors.localNetworkDrop.Load(),
		DNS:     collectors.localDNSDrop.Load(),
	}}
	addDropCounter(
		&result.Kernel,
		result.Local.Process,
		result.Local.File,
		result.Local.Network,
		result.Local.DNS,
	)
	if collectors.processReader != nil {
		counters, err := collectors.processReader.Counters()
		if err != nil {
			return observerDropCounters{}, err
		}
		addDropCounter(&result.Kernel, counters.StateDrops)
		addDropCounter(&result.Ring, counters.RingbufDrops)
	}
	if collectors.fileReader != nil {
		counters, err := collectors.fileReader.Counters()
		if err != nil {
			return observerDropCounters{}, err
		}
		addDropCounter(
			&result.Kernel,
			counters.StateDrops,
		)
		addDropCounter(&result.Ring, counters.RingbufDrops)
		result.File = observerFileCollectorCounters{
			MatchedEvents:     counters.MatchedEvents,
			ReservedEvents:    counters.ReservedEvents,
			RingbufDrops:      counters.RingbufDrops,
			StateDrops:        counters.StateDrops,
			StateDegradations: counters.StateDegradations,
			PathFailures:      counters.PathFailures,
			IdentityFailures:  counters.IdentityFailures,
		}
	}
	if collectors.networkReader != nil {
		counters, err := collectors.networkReader.Counters()
		if err != nil {
			return observerDropCounters{}, err
		}
		addDropCounter(
			&result.Kernel,
			counters.StateDrops,
			counters.UnsupportedEvents,
			counters.DNSCaptureFailures,
			counters.DNSTruncatedPackets,
			counters.DNSStateMisses,
			counters.ProxyCaptureFailures,
			counters.ProxyTruncatedChunks,
			counters.ProxyStateMisses,
			counters.ProxyBudgetExhausted,
		)
		addDropCounter(
			&result.Ring,
			counters.RingbufDrops,
			counters.DNSRingbufDrops,
			counters.ProxyRingbufDrops,
		)
	}
	return result, nil
}

func addDropCounter(target *uint64, values ...uint64) {
	for _, value := range values {
		if math.MaxUint64-*target < value {
			*target = math.MaxUint64
			continue
		}
		*target += value
	}
}

func (collectors *linuxObserverCollectors) Close() error {
	if collectors == nil {
		return nil
	}
	collectors.closeOnce.Do(func() {
		collectors.closeErr = errors.Join(collectors.closeErr, collectors.Stop())
		if collectors.networkReader != nil {
			collectors.closeErr = errors.Join(
				collectors.closeErr,
				collectors.networkReader.Close(),
			)
		}
		if collectors.fileReader != nil {
			collectors.closeErr = errors.Join(
				collectors.closeErr,
				collectors.fileReader.Close(),
			)
		}
		if collectors.processReader != nil {
			collectors.closeErr = errors.Join(
				collectors.closeErr,
				collectors.processReader.Close(),
			)
		}
	})
	return collectors.closeErr
}

func (collectors *linuxObserverCollectors) Stop() error {
	if collectors == nil {
		return nil
	}
	collectors.stopOnce.Do(func() {
		collectors.draining.Store(true)
		if collectors.networkReader != nil {
			collectors.stopErr = errors.Join(
				collectors.stopErr,
				collectors.networkReader.FlushPending(),
			)
		}
		if collectors.fileReader != nil {
			collectors.stopErr = errors.Join(
				collectors.stopErr,
				collectors.fileReader.FlushPending(),
			)
		}
		if collectors.processReader != nil {
			collectors.stopErr = errors.Join(
				collectors.stopErr,
				collectors.processReader.FlushPending(),
			)
		}
		drained := make(chan struct{})
		go func() {
			collectors.wait.Wait()
			close(drained)
		}()
		timer := time.NewTimer(observerDrainWait)
		select {
		case <-drained:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			collectors.stopErr = errors.Join(
				collectors.stopErr,
				errors.New("observer collector drain exceeded its bound"),
				collectors.forceStopReaders(),
			)
			<-drained
		}
		collectors.closed.Store(true)
		if collectors.dnsParser != nil {
			collectors.dnsParser.Close()
		}
	})
	return collectors.stopErr
}

func (collectors *linuxObserverCollectors) forceStopReaders() error {
	if collectors == nil {
		return nil
	}
	var result error
	if collectors.networkReader != nil {
		result = errors.Join(result, collectors.networkReader.Stop())
	}
	if collectors.fileReader != nil {
		result = errors.Join(result, collectors.fileReader.Stop())
	}
	if collectors.processReader != nil {
		result = errors.Join(result, collectors.processReader.Stop())
	}
	return result
}

var _ observerCollectorRuntime = (*linuxObserverCollectors)(nil)
