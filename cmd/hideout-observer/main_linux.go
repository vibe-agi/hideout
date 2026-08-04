//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vibe-agi/hideout/internal/sessionwire"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
	"github.com/vibe-agi/hideout/internal/workspacepath"
	"golang.org/x/sys/unix"
)

const defaultObserverHeartbeat = time.Second

type observerConfig struct {
	Binding        sessionwire.ObserverBinding
	CgroupPath     string
	ExpectedDigest string
	Heartbeat      time.Duration
	Workspace      *workspacepath.Binding
}

type observerDependencies struct {
	EUID             func() int
	ExecutableDigest func() (string, error)
	ValidateBoundary func(string, uint64) error
	OpenCollectors   func(observerCollectorConfig) (observerCollectorRuntime, error)
	Now              func() time.Time
	MonotonicNS      func() (uint64, error)
}

type observerCollectorConfig struct {
	Binding    sessionwire.ObserverBinding
	CgroupPath string
	Anchor     observerClockAnchor
	Workspace  *workspacepath.Binding
}

type observerClockAnchor struct {
	WallTime    time.Time
	MonotonicNS uint64
}

type observerDropCounters struct {
	Kernel uint64
	Ring   uint64
	Local  observerLocalDropCounters
	File   observerFileCollectorCounters
}

type observerLocalDropCounters struct {
	Process uint64 `json:"process"`
	File    uint64 `json:"file"`
	Network uint64 `json:"network"`
	DNS     uint64 `json:"dns"`
}

type observerFileCollectorCounters struct {
	MatchedEvents     uint64 `json:"matchedEvents"`
	ReservedEvents    uint64 `json:"reservedEvents"`
	RingbufDrops      uint64 `json:"ringbufDrops"`
	StateDrops        uint64 `json:"stateDrops"`
	StateDegradations uint64 `json:"stateDegradations"`
	PathFailures      uint64 `json:"pathFailures"`
	IdentityFailures  uint64 `json:"identityFailures"`
}

type observerRecord struct {
	Record      workloadtypes.ActivityRecord
	Execution   *workloadtypes.Execution
	CPU         uint64
	MonotonicNS uint64
}

type observerRecordSink func(observerRecord) error

type observerCollectorRuntime interface {
	Capabilities() sessionwire.ObserverCapabilities
	Start(context.Context, observerRecordSink)
	Errors() <-chan error
	Counters() (observerDropCounters, error)
	Stop() error
	Close() error
}

type observerEmitter struct {
	mu        sync.Mutex
	writer    io.Writer
	binding   sessionwire.ObserverBinding
	nextByCPU map[uint64]uint64
}

func runObserverCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	config, err := parseObserverConfig(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runObserver(ctx, stdin, stdout, config, systemObserverDependencies())
}

func parseObserverConfig(args []string) (observerConfig, error) {
	flags := flag.NewFlagSet("hideout-observer", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	ownerValue := flags.String("owner", "", "base64url-encoded activity owner")
	sessionID := flags.String("session", "", "session identity")
	environmentID := flags.String("environment", "", "environment identity")
	backendIncarnationID := flags.String("backend-incarnation", "", "backend incarnation identity")
	guestBootID := flags.String("guest-boot", "", "guest boot identity")
	cgroupPath := flags.String("cgroup-path", "", "fixed session cgroup path")
	cgroupID := flags.Uint64("cgroup-id", 0, "kernel cgroup identity")
	generation := flags.Uint64("generation", 0, "observer generation")
	digest := flags.String("helper-digest", "", "expected packaged helper digest")
	heartbeat := flags.Duration("heartbeat", defaultObserverHeartbeat, "heartbeat interval")
	workspaceID := flags.String("workspace-id", "", "trusted workspace identity")
	workspaceLogicalRoot := flags.String("workspace-logical-root", "", "trusted logical workspace root")
	workspacePhysicalRoot := flags.String("workspace-physical-root", "", "trusted physical workspace root")
	if err := flags.Parse(args); err != nil {
		return observerConfig{}, err
	}
	if flags.NArg() != 0 {
		return observerConfig{}, errors.New("observer does not accept positional arguments")
	}
	owner, err := decodeActivityOwner(*ownerValue)
	if err != nil {
		return observerConfig{}, err
	}
	config := observerConfig{
		Binding: sessionwire.ObserverBinding{
			Owner: owner, SessionID: *sessionID, EnvironmentID: *environmentID,
			BackendIncarnationID: *backendIncarnationID, GuestBootID: *guestBootID,
			CgroupID: *cgroupID, ObserverGeneration: *generation,
		},
		CgroupPath:     filepath.Clean(*cgroupPath),
		ExpectedDigest: *digest,
		Heartbeat:      *heartbeat,
	}
	if *workspaceID != "" || *workspaceLogicalRoot != "" || *workspacePhysicalRoot != "" {
		binding, bindingErr := workspacepath.NewBinding(
			*workspaceID,
			*workspaceLogicalRoot,
			*workspacePhysicalRoot,
		)
		if bindingErr != nil {
			return observerConfig{}, errors.New("observer workspace identity is invalid")
		}
		config.Workspace = &binding
	}
	if err := config.Validate(); err != nil {
		return observerConfig{}, err
	}
	return config, nil
}

func (config observerConfig) Validate() error {
	if err := config.Binding.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(config.CgroupPath) ||
		!strings.HasPrefix(config.CgroupPath, "/sys/fs/cgroup/") {
		return errors.New("observer cgroup path is invalid")
	}
	if !validDigest(config.ExpectedDigest) {
		return errors.New("observer helper digest is invalid")
	}
	if config.Heartbeat < 100*time.Millisecond || config.Heartbeat > time.Minute {
		return errors.New("observer heartbeat interval is outside the supported bound")
	}
	if config.Workspace != nil && config.Workspace.Validate() != nil {
		return errors.New("observer workspace identity is invalid")
	}
	return nil
}

func runObserver(
	ctx context.Context,
	stdin io.Reader,
	stdout io.Writer,
	config observerConfig,
	dependencies observerDependencies,
) (resultErr error) {
	if ctx == nil || stdin == nil || stdout == nil {
		return errors.New("observer endpoints are required")
	}
	if err := config.Validate(); err != nil {
		return err
	}
	if dependencies.EUID == nil || dependencies.ExecutableDigest == nil ||
		dependencies.ValidateBoundary == nil ||
		dependencies.OpenCollectors == nil ||
		dependencies.Now == nil ||
		dependencies.MonotonicNS == nil {
		return errors.New("observer platform dependencies are incomplete")
	}
	if dependencies.EUID() != 0 {
		return sessionwire.ErrObserverTargetAuthority
	}
	actualDigest, err := dependencies.ExecutableDigest()
	if err != nil {
		return fmt.Errorf("measure observer helper: %w", err)
	}
	if actualDigest != config.ExpectedDigest {
		return sessionwire.ErrObserverAuthentication
	}
	if err := dependencies.ValidateBoundary(config.CgroupPath, config.Binding.CgroupID); err != nil {
		return fmt.Errorf("validate observer workload boundary: %w", err)
	}
	anchorMonotonicNS, err := dependencies.MonotonicNS()
	if err != nil {
		return fmt.Errorf("anchor observer monotonic clock: %w", err)
	}
	anchor := observerClockAnchor{
		WallTime:    dependencies.Now().UTC(),
		MonotonicNS: anchorMonotonicNS,
	}
	if anchor.WallTime.IsZero() || anchor.MonotonicNS == 0 {
		return errors.New("observer clock anchor is invalid")
	}
	collectors, err := dependencies.OpenCollectors(observerCollectorConfig{
		Binding: config.Binding, CgroupPath: config.CgroupPath,
		Anchor: anchor, Workspace: config.Workspace,
	})
	if err != nil {
		return fmt.Errorf("open observer collectors: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, collectors.Close())
	}()
	capabilities := collectors.Capabilities()
	if err := capabilities.Validate(); err != nil {
		return err
	}
	hello := sessionwire.ObserverHello{
		Type: "observer.hello", Schema: sessionwire.ObserverWireSchema,
		Owner: config.Binding.Owner, SessionID: config.Binding.SessionID,
		EnvironmentID:        config.Binding.EnvironmentID,
		BackendIncarnationID: config.Binding.BackendIncarnationID,
		GuestBootID:          config.Binding.GuestBootID, CgroupID: config.Binding.CgroupID,
		ObserverGeneration: config.Binding.ObserverGeneration,
		HelperDigest:       actualDigest,
		Capabilities:       capabilities,
	}
	if err := sessionwire.WriteObserverHello(stdout, hello); err != nil {
		return err
	}
	accepted, err := sessionwire.ReadObserverAccepted(stdin)
	if err != nil {
		return err
	}
	if err := accepted.ValidateBinding(config.Binding); err != nil {
		return err
	}

	shutdown := make(chan struct{})
	go func() {
		var value [1]byte
		_, _ = stdin.Read(value[:])
		close(shutdown)
	}()

	emitter := &observerEmitter{
		writer: stdout, binding: config.Binding,
		nextByCPU: make(map[uint64]uint64),
	}
	writeHeartbeat := func(final bool) error {
		monotonicNS, err := dependencies.MonotonicNS()
		if err != nil {
			return err
		}
		counters, err := collectors.Counters()
		if err != nil {
			return fmt.Errorf("read observer drop counters: %w", err)
		}
		return emitter.heartbeat(monotonicNS, counters, final)
	}
	if err := writeHeartbeat(false); err != nil {
		return err
	}
	// Parent cancellation is a request to begin the bounded drain, not authority
	// for individual read loops to exit ahead of FlushPending. Keep collector
	// reads on an owner-controlled context until Stop has consumed the ring tail.
	collectorCtx, cancelCollectors := context.WithCancel(context.Background())
	defer cancelCollectors()
	collectors.Start(collectorCtx, emitter.record)
	ticker := time.NewTicker(config.Heartbeat)
	defer ticker.Stop()
	stopAndHeartbeat := func() error {
		stopErr := collectors.Stop()
		cancelCollectors()
		heartbeatErr := writeHeartbeat(true)
		return errors.Join(stopErr, heartbeatErr)
	}
	for {
		select {
		case <-ctx.Done():
			return stopAndHeartbeat()
		case <-shutdown:
			// Short sessions can finish before the periodic heartbeat. Publish one
			// final counter snapshot while the BPF maps are still open so kernel
			// and ring loss cannot disappear merely because the target was fast.
			return stopAndHeartbeat()
		case err := <-collectors.Errors():
			if err != nil {
				return errors.Join(
					fmt.Errorf("observer collector failed: %w", err),
					stopAndHeartbeat(),
				)
			}
		case <-ticker.C:
			if err := writeHeartbeat(false); err != nil {
				return errors.Join(err, collectors.Stop())
			}
		}
	}
}

func systemObserverDependencies() observerDependencies {
	return observerDependencies{
		EUID:             os.Geteuid,
		ExecutableDigest: executableDigest,
		ValidateBoundary: validateObserverBoundary,
		OpenCollectors:   openObserverCollectors,
		Now:              func() time.Time { return time.Now().UTC() },
		MonotonicNS:      monotonicNowNS,
	}
}

func executableDigest() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

func validateObserverBoundary(path string, expectedID uint64) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 ||
		stat.Ino != expectedID {
		return sessionwire.ErrObserverIdentity
	}
	processes, err := os.ReadFile(filepath.Join(path, "cgroup.procs"))
	if err != nil {
		return err
	}
	self := strconv.Itoa(os.Getpid())
	for _, value := range strings.Fields(string(processes)) {
		if value == self {
			return sessionwire.ErrObserverTargetAuthority
		}
	}
	return nil
}

func monotonicNowNS() (uint64, error) {
	var value unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &value); err != nil {
		return 0, err
	}
	if value.Sec < 0 || value.Nsec < 0 {
		return 0, errors.New("monotonic clock returned a negative value")
	}
	return uint64(value.Sec)*uint64(time.Second) + uint64(value.Nsec), nil
}

func (emitter *observerEmitter) record(observation observerRecord) error {
	hasRecord := observation.Record.Schema != ""
	hasExecution := observation.Execution != nil
	if hasRecord == hasExecution ||
		observation.CPU >= sessionwire.ObserverControlCPU ||
		observation.MonotonicNS == 0 {
		return errors.New("observer collector produced an invalid activity record")
	}
	kind := "process.execution"
	payloadValue := any(observation.Execution)
	if hasRecord {
		if err := observation.Record.Validate(); err != nil ||
			observation.Record.RedactionStatus != workloadtypes.RedactionPending {
			return errors.New("observer collector produced an invalid activity record")
		}
		var err error
		kind, err = observerRecordKind(observation.Record)
		if err != nil {
			return err
		}
		payloadValue = observation.Record
	} else if observation.Execution.Validate() != nil ||
		!observation.Execution.Owner.Equal(emitter.binding.Owner) ||
		observation.Execution.SessionID != emitter.binding.SessionID ||
		observation.Execution.GuestBootID != emitter.binding.GuestBootID ||
		observation.Execution.ObserverGeneration !=
			emitter.binding.ObserverGeneration {
		return errors.New("observer collector produced an invalid execution snapshot")
	}
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return err
	}
	return emitter.write(
		observation.CPU,
		observation.MonotonicNS,
		kind,
		payload,
		nil,
	)
}

func (emitter *observerEmitter) heartbeat(
	monotonicNS uint64,
	counters observerDropCounters,
	final bool,
) error {
	return emitter.write(
		sessionwire.ObserverControlCPU,
		monotonicNS,
		"collector.heartbeat",
		nil,
		func(sequence uint64) ([]byte, error) {
			return json.Marshal(struct {
				LatestSequence uint64                        `json:"latestSequence"`
				KernelDropped  uint64                        `json:"kernelDropped"`
				RingDropped    uint64                        `json:"ringDropped"`
				Local          observerLocalDropCounters     `json:"local"`
				File           observerFileCollectorCounters `json:"file"`
				Final          bool                          `json:"final"`
			}{
				LatestSequence: sequence,
				KernelDropped:  counters.Kernel,
				RingDropped:    counters.Ring,
				Local:          counters.Local,
				File:           counters.File,
				Final:          final,
			})
		},
	)
}

func (emitter *observerEmitter) write(
	cpu uint64,
	monotonicNS uint64,
	kind string,
	payload []byte,
	payloadForSequence func(uint64) ([]byte, error),
) error {
	if emitter == nil || emitter.writer == nil ||
		emitter.nextByCPU == nil ||
		monotonicNS == 0 {
		return errors.New("observer emitter is unavailable")
	}
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	sequence := emitter.nextByCPU[cpu] + 1
	if sequence == 0 {
		return errors.New("observer outbound sequence overflow")
	}
	if payloadForSequence != nil {
		var err error
		payload, err = payloadForSequence(sequence)
		if err != nil {
			return err
		}
	}
	envelope := sessionwire.ObservationEnvelope{
		Schema:             sessionwire.ObservationSchema,
		Owner:              emitter.binding.Owner,
		SessionID:          emitter.binding.SessionID,
		CgroupID:           emitter.binding.CgroupID,
		ObserverGeneration: emitter.binding.ObserverGeneration,
		CPU:                cpu, Sequence: sequence, MonotonicNS: monotonicNS,
		Kind: kind, Payload: payload,
	}
	if err := sessionwire.WriteObserverEnvelope(
		emitter.writer,
		envelope,
	); err != nil {
		return err
	}
	emitter.nextByCPU[cpu] = sequence
	return nil
}

func observerRecordKind(record workloadtypes.ActivityRecord) (string, error) {
	switch record.Kind {
	case workloadtypes.ActivityProcess:
		if record.Operation == "exec" {
			return "process.exec", nil
		}
	case workloadtypes.ActivityFile:
		switch record.Operation {
		case "open", "read", "write", "mmap", "create", "truncate",
			"rename", "unlink", "metadata", "hardlink", "symlink",
			"mkdir", "rmdir":
			return "file." + record.Operation, nil
		}
	case workloadtypes.ActivityConnection:
		if record.Operation == "connect" {
			return "network.connect", nil
		}
	case workloadtypes.ActivityDNS:
		if record.Operation == "resolve" {
			return "dns.response", nil
		}
	}
	return "", errors.New("observer activity kind has no envelope contract")
}

func decodeActivityOwner(value string) (workloadtypes.ActivityOwner, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) == 0 || len(raw) > 4096 {
		return workloadtypes.ActivityOwner{}, errors.New("observer activity owner encoding is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var owner workloadtypes.ActivityOwner
	if err := decoder.Decode(&owner); err != nil {
		return workloadtypes.ActivityOwner{}, errors.New("observer activity owner is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return workloadtypes.ActivityOwner{}, errors.New("observer activity owner has trailing data")
	}
	if err := owner.Validate(); err != nil {
		return workloadtypes.ActivityOwner{}, err
	}
	return owner, nil
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
