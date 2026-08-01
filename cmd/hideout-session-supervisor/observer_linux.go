//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/vibe-agi/hideout/internal/sessionwire"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
	"golang.org/x/sys/unix"
)

const (
	fixedGuestObserverPath = "/hideout/session/shims/hideout-observer"
	observerExecutableFD   = 3
	observerHandshakeWait  = 3 * time.Second
	observerShutdownWait   = 3 * time.Second
)

type observerLaunchSpec struct {
	Path         string
	Digest       string
	Binding      sessionwire.ObserverBinding
	CgroupPath   string
	Heartbeat    time.Duration
	ExpectedRoot bool
}

type observerHelperProcess struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	wait   func() error
	kill   func() error
}

type verifiedObserverHelper struct {
	file        *os.File
	displayPath string
}

type observerSessionOptions struct {
	Cgroup          sessionCgroupOptions
	ObserverPath    string
	Launch          func(observerLaunchSpec) (*observerHelperProcess, error)
	MonotonicNS     func() (uint64, error)
	HandshakeWait   time.Duration
	ShutdownWait    time.Duration
	ObserverPeerUID uint32
	Relay           observerRelayOptions
}

type observerSession struct {
	group       *sessionCgroup
	binding     sessionwire.ObserverBinding
	boundary    workloadtypes.WorkloadBoundary
	digest      string
	process     *observerHelperProcess
	tracker     *sessionwire.ObserverSequenceTracker
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	processDone chan error
	readerDone  chan struct{}
	firstEvent  chan error
	relay       *observerRelay

	coverageMu sync.Mutex
	coverage   []sessionwire.SupervisorCoverageSummary
	stopping   atomic.Bool
	stopOnce   sync.Once
	stopErr    error
}

func prepareSupervisorActivity(spec startSpec) (*observerSession, error) {
	return prepareObserverSession(spec, observerSessionOptions{
		Cgroup: sessionCgroupOptions{
			Root: defaultSessionCgroupRoot, SessionID: spec.SessionID,
		},
		ObserverPath:    fixedGuestObserverPath,
		Launch:          launchObserverHelper,
		MonotonicNS:     supervisorMonotonicNS,
		HandshakeWait:   observerHandshakeWait,
		ShutdownWait:    observerShutdownWait,
		ObserverPeerUID: 0,
	})
}

func prepareObserverSession(
	spec startSpec,
	options observerSessionOptions,
) (*observerSession, error) {
	if spec.Activity == nil {
		return nil, nil
	}
	if err := spec.Activity.Validate(spec.SessionID); err != nil {
		return nil, err
	}
	if spec.ProjectionReadiness == nil {
		return nil, errors.New("activity observation requires an exact environment")
	}
	if options.Cgroup.Root == "" {
		options.Cgroup.Root = defaultSessionCgroupRoot
	}
	options.Cgroup.SessionID = spec.SessionID
	group, err := newSessionCgroup(options.Cgroup)
	if err != nil {
		return nil, fmt.Errorf("create observed workload cgroup: %w", err)
	}
	cleanupGroup := func() {
		_ = group.ProveEmptyAndRemove()
		_ = group.Close()
	}
	binding := sessionwire.ObserverBinding{
		Owner: spec.Activity.Owner, SessionID: spec.SessionID,
		EnvironmentID:        spec.ProjectionReadiness.EnvironmentID,
		BackendIncarnationID: spec.Activity.Owner.BackendIncarnationID,
		GuestBootID:          spec.ExpectedBootID,
		CgroupID:             group.ID(),
		ObserverGeneration:   spec.Activity.ObserverGeneration,
	}
	if err := binding.Validate(); err != nil {
		cleanupGroup()
		return nil, err
	}
	if options.ObserverPath == "" {
		options.ObserverPath = fixedGuestObserverPath
	}
	if options.Launch == nil {
		options.Launch = launchObserverHelper
	}
	if options.MonotonicNS == nil {
		options.MonotonicNS = supervisorMonotonicNS
	}
	if options.HandshakeWait <= 0 {
		options.HandshakeWait = observerHandshakeWait
	}
	if options.ShutdownWait <= 0 {
		options.ShutdownWait = observerShutdownWait
	}
	createdAtMonoNS, err := options.MonotonicNS()
	if err != nil || createdAtMonoNS == 0 {
		cleanupGroup()
		return nil, errors.New("observe monotonic boundary creation time")
	}
	process, err := options.Launch(observerLaunchSpec{
		Path: options.ObserverPath, Digest: spec.Activity.ObserverHelperDigest,
		Binding: binding, CgroupPath: group.Path(), Heartbeat: time.Second,
		ExpectedRoot: true,
	})
	if err != nil {
		cleanupGroup()
		return nil, fmt.Errorf("launch fixed observer helper: %w", err)
	}
	var relay *observerRelay
	terminateRejected := func() {
		if relay != nil {
			_ = relay.Close()
		}
		if process.stdin != nil {
			_ = process.stdin.Close()
		}
		if process.kill != nil {
			_ = process.kill()
		}
		if process.wait != nil {
			_ = process.wait()
		}
		if process.stdout != nil {
			_ = process.stdout.Close()
		}
		cleanupGroup()
	}
	if process.stdin == nil || process.stdout == nil ||
		process.wait == nil || process.kill == nil {
		terminateRejected()
		return nil, errors.New("observer helper endpoints are incomplete")
	}
	type helloResult struct {
		value sessionwire.ObserverHello
		err   error
	}
	helloResults := make(chan helloResult, 1)
	go func() {
		hello, readErr := sessionwire.ReadObserverHello(process.stdout)
		helloResults <- helloResult{value: hello, err: readErr}
	}()
	var hello sessionwire.ObserverHello
	select {
	case result := <-helloResults:
		if result.err != nil {
			terminateRejected()
			return nil, fmt.Errorf("read observer registration: %w", result.err)
		}
		hello = result.value
	case <-time.After(options.HandshakeWait):
		terminateRejected()
		return nil, errors.New("observer registration exceeded the readiness bound")
	}
	accepted, err := sessionwire.AcceptObserverHello(hello, binding, sessionwire.ObserverPeer{
		UID: options.ObserverPeerUID, HelperDigest: spec.Activity.ObserverHelperDigest,
	})
	if err != nil {
		terminateRejected()
		return nil, err
	}
	if err := sessionwire.WriteObserverAccepted(process.stdin, accepted); err != nil {
		terminateRejected()
		return nil, fmt.Errorf("accept observer registration: %w", err)
	}
	tracker, err := sessionwire.NewObserverSequenceTracker(binding)
	if err != nil {
		terminateRejected()
		return nil, err
	}
	coverage := hello.Capabilities.Coverage()
	if err := validateObserverCoverage(coverage); err != nil {
		terminateRejected()
		return nil, err
	}
	relay, err = newObserverRelay(
		binding,
		hello,
		spec.Activity.ObserverStreamToken,
		options.Relay,
	)
	if err != nil {
		terminateRejected()
		return nil, fmt.Errorf("create dedicated observer relay: %w", err)
	}
	boundary := workloadtypes.WorkloadBoundary{
		Schema: workloadtypes.WorkloadBoundarySchema, Owner: binding.Owner,
		SessionID: binding.SessionID, CgroupPath: group.Path(), CgroupID: group.ID(),
		TargetUser: spec.TargetUser, State: workloadtypes.BoundaryReady,
		ObserverGeneration: binding.ObserverGeneration, GuestBootID: binding.GuestBootID,
		CreatedAtMonoNS: createdAtMonoNS,
	}
	session := &observerSession{
		group: group, binding: binding, boundary: boundary,
		digest:  spec.Activity.ObserverHelperDigest,
		process: process, tracker: tracker, stdin: process.stdin, stdout: process.stdout,
		processDone: make(chan error, 1), readerDone: make(chan struct{}),
		firstEvent: make(chan error, 1), coverage: coverage, relay: relay,
	}
	go func() {
		session.processDone <- process.wait()
	}()
	go session.readObservations()
	go session.watchRelay()
	select {
	case firstErr := <-session.firstEvent:
		if firstErr != nil {
			_ = session.Abort(options.ShutdownWait)
			return nil, fmt.Errorf("observe initial helper heartbeat: %w", firstErr)
		}
	case <-time.After(options.HandshakeWait):
		_ = session.Abort(options.ShutdownWait)
		return nil, errors.New("observer heartbeat exceeded the readiness bound")
	}
	ready := &sessionwire.SupervisorActivityReady{
		Boundary: boundary, ObserverHelperDigest: session.digest,
		Coverage: session.coverageSnapshot(),
	}
	if err := ready.ValidateExpectation(spec.SessionID, spec.Activity); err != nil {
		_ = session.Abort(options.ShutdownWait)
		return nil, err
	}
	return session, nil
}

func (session *observerSession) Ready() *sessionwire.SupervisorActivityReady {
	if session == nil {
		return nil
	}
	return &sessionwire.SupervisorActivityReady{
		Boundary:             session.boundary,
		ObserverHelperDigest: session.digest,
		Coverage:             session.coverageSnapshot(),
	}
}

func (session *observerSession) readObservations() {
	defer close(session.readerDone)
	first := true
	reportFirst := func(err error) {
		if first {
			first = false
			session.firstEvent <- err
		}
	}
	for {
		envelope, err := sessionwire.ReadObserverEnvelope(session.stdout)
		if err != nil {
			reportFirst(err)
			if !session.stopping.Load() {
				session.degradeCoverage("observer-stream-ended", 1)
			}
			return
		}
		if envelope.ObserverGeneration != session.binding.ObserverGeneration {
			reportFirst(sessionwire.ErrObserverIdentity)
			session.degradeCoverage("observer-generation-changed", 1)
			return
		}
		result, err := session.tracker.Observe(envelope)
		if err != nil {
			reportFirst(err)
			session.degradeCoverage("observer-sequence-invalid", 1)
			return
		}
		reportFirst(nil)
		switch result.Disposition {
		case sessionwire.ObserverSequenceGap:
			missing := result.MissingTo - result.MissingFrom + 1
			session.degradeCoverage("observer-sequence-gap", missing)
		case sessionwire.ObserverSequenceRestart:
			session.degradeCoverage("observer-generation-changed", 1)
		}
		if err := session.relay.EnqueueWait(envelope); err != nil {
			if errors.Is(err, sessionwire.ErrObserverBackpressure) {
				session.degradeCoverage("observer-send-queue-overflow", 1)
				continue
			}
			if session.stopping.Load() && errors.Is(err, sessionwire.ErrObserverQueueClosed) {
				return
			}
			session.degradeCoverage("observer-relay-invalid", 1)
			return
		}
	}
}

func (session *observerSession) watchRelay() {
	if session == nil || session.relay == nil {
		return
	}
	<-session.relay.Done()
	if !session.stopping.Load() {
		session.degradeCoverage("observer-daemon-stream-ended", 1)
	}
}

func (session *observerSession) degradeCoverage(reason string, dropped uint64) {
	session.coverageMu.Lock()
	defer session.coverageMu.Unlock()
	for index := range session.coverage {
		coverage := &session.coverage[index]
		if coverage.State == workloadtypes.CoverageAvailable {
			coverage.State = workloadtypes.CoveragePartial
		}
		coverage.Reason = reason
		if dropped > math.MaxUint64-coverage.DroppedEventCount {
			coverage.DroppedEventCount = math.MaxUint64
		} else {
			coverage.DroppedEventCount += dropped
		}
		if !containsString(coverage.Evidence, reason) {
			coverage.Evidence = append(coverage.Evidence, reason)
		}
	}
}

func (session *observerSession) coverageSnapshot() []sessionwire.SupervisorCoverageSummary {
	if session == nil {
		return nil
	}
	session.coverageMu.Lock()
	defer session.coverageMu.Unlock()
	result := make([]sessionwire.SupervisorCoverageSummary, len(session.coverage))
	copy(result, session.coverage)
	for index := range result {
		result[index].Evidence = append([]string(nil), session.coverage[index].Evidence...)
	}
	return result
}

func (session *observerSession) Stop(timeout time.Duration) error {
	if session == nil {
		return nil
	}
	session.stopOnce.Do(func() {
		session.stopping.Store(true)
		if timeout <= 0 {
			timeout = observerShutdownWait
		}
		deadline := time.Now().Add(timeout)
		remaining := func() time.Duration {
			value := time.Until(deadline)
			if value <= 0 {
				return time.Millisecond
			}
			return value
		}
		closeErr := observerEndpointCloseError(session.stdin.Close())
		var waitErr error
		processStopped := false
		processTimer := time.NewTimer(remaining())
		select {
		case waitErr = <-session.processDone:
			processStopped = true
			if !processTimer.Stop() {
				<-processTimer.C
			}
		case <-processTimer.C:
			waitErr = errors.New("observer helper did not stop within the bound")
			if killErr := session.process.kill(); killErr != nil {
				waitErr = errors.Join(waitErr, killErr)
			}
			reapTimer := time.NewTimer(remaining())
			select {
			case reapedErr := <-session.processDone:
				waitErr = errors.Join(waitErr, reapedErr)
				processStopped = true
				if !reapTimer.Stop() {
					<-reapTimer.C
				}
			case <-reapTimer.C:
				waitErr = errors.Join(waitErr, errors.New("observer helper was not reaped"))
			}
		}
		readerDrained := false
		readerTimer := time.NewTimer(remaining())
		select {
		case <-session.readerDone:
			readerDrained = true
			if !readerTimer.Stop() {
				<-readerTimer.C
			}
		case <-readerTimer.C:
			waitErr = errors.Join(waitErr, errors.New("observer reader did not stop"))
		}
		closeErr = errors.Join(
			closeErr,
			observerEndpointCloseError(session.stdout.Close()),
		)
		var relayErr error
		// A drain marker claims that the collector completed cleanly and that
		// every admitted observation has an exact final receipt. Reaping the
		// helper and draining its stdout are necessary but not sufficient: an
		// externally killed or otherwise unsuccessful helper reaches both states
		// without publishing that receipt. Close the relay destructively in that
		// case so the host accounts an unexpected transport loss instead of
		// receiving a false collector.goodbye.
		if processStopped && readerDrained && waitErr == nil && closeErr == nil {
			relayErr = session.relay.DrainAndClose(remaining())
		} else {
			relayErr = session.relay.Close()
		}
		session.stopErr = errors.Join(closeErr, waitErr, relayErr)
		if session.stopErr != nil {
			session.degradeCoverage("observer-shutdown-unproved", 1)
		}
	})
	return session.stopErr
}

func observerEndpointCloseError(err error) error {
	if errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}

func (session *observerSession) ObserverStreamAuthenticated() <-chan struct{} {
	if session == nil || session.relay == nil {
		return nil
	}
	return session.relay.Authenticated()
}

func (session *observerSession) Abort(timeout time.Duration) error {
	if session == nil {
		return nil
	}
	stopErr := session.Stop(timeout)
	removeErr := session.group.ProveEmptyAndRemove()
	closeErr := session.group.Close()
	return errors.Join(stopErr, removeErr, closeErr)
}

func (session *observerSession) Completion(cleanupErr error) *sessionwire.SupervisorActivityCompletion {
	if session == nil {
		return nil
	}
	state := workloadtypes.BoundaryRemoved
	cleanupProved := cleanupErr == nil
	if !cleanupProved {
		state = workloadtypes.BoundaryUnproved
	}
	return &sessionwire.SupervisorActivityCompletion{
		Owner: session.binding.Owner, SessionID: session.binding.SessionID,
		CgroupID:           session.binding.CgroupID,
		ObserverGeneration: session.binding.ObserverGeneration,
		BoundaryState:      state, Coverage: session.coverageSnapshot(),
		CleanupProved: cleanupProved,
	}
}

func launchObserverHelper(spec observerLaunchSpec) (*observerHelperProcess, error) {
	if spec.Path != fixedGuestObserverPath || !spec.ExpectedRoot {
		return nil, errors.New("observer helper path or authority is not fixed")
	}
	helper, err := openFixedObserverHelper(spec.Path, spec.Digest)
	if err != nil {
		return nil, err
	}
	defer helper.Close()
	ownerJSON, err := json.Marshal(spec.Binding.Owner)
	if err != nil {
		return nil, err
	}
	args := []string{
		"--owner", base64.RawURLEncoding.EncodeToString(ownerJSON),
		"--session", spec.Binding.SessionID,
		"--environment", spec.Binding.EnvironmentID,
		"--backend-incarnation", spec.Binding.BackendIncarnationID,
		"--guest-boot", spec.Binding.GuestBootID,
		"--cgroup-path", spec.CgroupPath,
		"--cgroup-id", strconv.FormatUint(spec.Binding.CgroupID, 10),
		"--generation", strconv.FormatUint(spec.Binding.ObserverGeneration, 10),
		"--helper-digest", spec.Digest,
		"--heartbeat", spec.Heartbeat.String(),
	}
	command := helper.Command(args...)
	command.Dir = "/"
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	stderr := &boundedObserverBuffer{limit: 4096}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	return &observerHelperProcess{
		stdin: stdin, stdout: stdout,
		wait: func() error {
			if err := command.Wait(); err != nil {
				return errors.New("observer helper exited unsuccessfully")
			}
			return nil
		},
		kill: func() error {
			err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			if errors.Is(err, syscall.ESRCH) {
				return nil
			}
			return err
		},
	}, nil
}

func openFixedObserverHelper(
	path string,
	expectedDigest string,
) (*verifiedObserverHelper, error) {
	return openVerifiedObserverHelper(path, expectedDigest, 0)
}

func openVerifiedObserverHelper(
	path string,
	expectedDigest string,
	expectedUID uint32,
) (*verifiedObserverHelper, error) {
	fd, err := unix.Open(
		path,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("observer helper file descriptor is unavailable")
	}
	keep := false
	defer func() {
		if !keep {
			_ = file.Close()
		}
	}()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, err
	}
	mode := os.FileMode(stat.Mode)
	if stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		mode.Perm()&0o111 == 0 || mode.Perm()&0o022 != 0 ||
		stat.Uid != expectedUID {
		return nil, errors.New(
			"observer helper is not a root-owned non-writable executable",
		)
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return nil, err
	}
	actual := "sha256:" + hex.EncodeToString(digest.Sum(nil))
	if actual != expectedDigest {
		return nil, sessionwire.ErrObserverAuthentication
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	keep = true
	return &verifiedObserverHelper{
		file:        file,
		displayPath: path,
	}, nil
}

func (helper *verifiedObserverHelper) Command(args ...string) *exec.Cmd {
	command := exec.Command(
		"/proc/self/fd/"+strconv.Itoa(observerExecutableFD),
		args...,
	)
	command.Args[0] = helper.displayPath
	command.ExtraFiles = []*os.File{helper.file}
	return command
}

func (helper *verifiedObserverHelper) Close() error {
	if helper == nil || helper.file == nil {
		return nil
	}
	file := helper.file
	helper.file = nil
	return file.Close()
}

func supervisorMonotonicNS() (uint64, error) {
	var value unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &value); err != nil {
		return 0, err
	}
	if value.Sec < 0 || value.Nsec < 0 {
		return 0, errors.New("monotonic clock returned a negative value")
	}
	return uint64(value.Sec)*uint64(time.Second) + uint64(value.Nsec), nil
}

func validateObserverCoverage(coverage []sessionwire.SupervisorCoverageSummary) error {
	ready := sessionwire.SupervisorActivityReady{
		Boundary: workloadtypes.WorkloadBoundary{
			Schema: workloadtypes.WorkloadBoundarySchema,
			Owner: workloadtypes.ActivityOwner{
				Kind: workloadtypes.OwnerDisposableSession, SessionID: "ses_validation",
				Backend: "validation", BackendIncarnationID: "validation",
			},
			SessionID: "ses_validation", CgroupPath: "/sys/fs/cgroup/validation",
			CgroupID: 1, TargetUser: "validation", State: workloadtypes.BoundaryReady,
			ObserverGeneration: 1, GuestBootID: "validation", CreatedAtMonoNS: 1,
		},
		ObserverHelperDigest: "sha256:" + strings.Repeat("0", 64),
		Coverage:             coverage,
	}
	return ready.Validate("ses_validation")
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

type boundedObserverBuffer struct {
	mu    sync.Mutex
	limit int
	data  bytes.Buffer
}

func (buffer *boundedObserverBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	accepted := len(value)
	if buffer.limit <= 0 || buffer.data.Len() >= buffer.limit {
		return accepted, nil
	}
	remaining := buffer.limit - buffer.data.Len()
	if len(value) > remaining {
		value = value[:remaining]
	}
	_, _ = buffer.data.Write(value)
	return accepted, nil
}
