//go:build linux

package fanotify

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
	"golang.org/x/sys/unix"
)

const (
	metadataSize            uint32 = 24
	fanotifyMetadataVersion uint8  = 3
	maxMountPaths                  = 32
	maxReadBufferBytes             = 64 * 1024
	maxObservedPathBytes           = 4096
)

var (
	ErrSourceConfig     = errors.New("fanotify source configuration is invalid")
	ErrSourceOpen       = errors.New("fanotify source could not be opened")
	ErrSourceClosed     = errors.New("fanotify source is closed")
	ErrSourceSequence   = errors.New("fanotify source sequence is exhausted")
	ErrSourceClock      = errors.New("fanotify source clock is invalid")
	ErrMetadata         = errors.New("fanotify metadata is invalid")
	ErrSourcePoll       = errors.New("fanotify source poll failed")
	ErrSourceRead       = errors.New("fanotify source read failed")
	ErrFileResolution   = errors.New("fanotify file metadata is unavailable")
	ErrActorResolution  = errors.New("fanotify actor metadata is unavailable")
	ErrMembershipFilter = errors.New("fanotify process membership is unresolved")
)

type PIDMatcher func(pid uint32) (bool, error)
type ActorResolver func(pid uint32) (workloadtypes.Actor, bool)

type SourceConfig struct {
	MountPaths   []string
	MatchPID     PIDMatcher
	ResolveActor ActorResolver
	Now          func() time.Time
}

type Loss struct {
	Kind              LossKind
	Sequence          uint64
	At                time.Time
	DroppedLowerBound uint64
	Reason            string
}

type Observation struct {
	File *RawEvent
	Loss *Loss
}

type SourceCounters struct {
	Received         uint64
	Emitted          uint64
	Filtered         uint64
	QueueOverflows   uint64
	FilterFailures   uint64
	DecodeFailures   uint64
	UnsupportedMasks uint64
	PathFailures     uint64
	IdentityFailures uint64
	ActorUnresolved  uint64
	CloseFailures    uint64
}

type sourceCounterSet struct {
	received         atomic.Uint64
	emitted          atomic.Uint64
	filtered         atomic.Uint64
	queueOverflows   atomic.Uint64
	filterFailures   atomic.Uint64
	decodeFailures   atomic.Uint64
	unsupportedMasks atomic.Uint64
	pathFailures     atomic.Uint64
	identityFailures atomic.Uint64
	actorUnresolved  atomic.Uint64
	closeFailures    atomic.Uint64
}

type Source struct {
	readMu  sync.Mutex
	stateMu sync.Mutex

	fd           int
	wakeFD       int
	closed       bool
	closeDone    chan struct{}
	closeErr     error
	deadline     time.Time
	mountPaths   []string
	matchPID     PIDMatcher
	resolveActor ActorResolver
	now          func() time.Time
	pending      []Observation
	sequence     atomic.Uint64
	counters     sourceCounterSet
}

type metadataRecord struct {
	Mask      uint64
	FD        int32
	PID       uint32
	Overflow  bool
	CloseOnly bool
}

type resolvedFile struct {
	path      string
	pathState string
	fileType  string
	device    uint64
	inode     uint64
	mountID   uint64
}

func OpenSource(config SourceConfig) (*Source, error) {
	mountPaths, err := validateSourceConfig(config)
	if err != nil {
		return nil, err
	}
	fd, err := unix.FanotifyInit(
		uint(unix.FAN_CLASS_NOTIF|unix.FAN_CLOEXEC|unix.FAN_NONBLOCK),
		uint(unix.O_RDONLY|unix.O_LARGEFILE|unix.O_CLOEXEC),
	)
	if err != nil {
		return nil, errors.Join(ErrSourceOpen, err)
	}
	wakeFD, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		return nil, errors.Join(ErrSourceOpen, err, unix.Close(fd))
	}
	closeOnError := func(sourceErr error) (*Source, error) {
		return nil, errors.Join(
			sourceErr,
			unix.Close(wakeFD),
			unix.Close(fd),
		)
	}
	mask := uint64(
		unix.FAN_OPEN |
			unix.FAN_ACCESS |
			unix.FAN_MODIFY |
			unix.FAN_ONDIR,
	)
	for _, mountPath := range mountPaths {
		if err := unix.FanotifyMark(
			fd,
			uint(unix.FAN_MARK_ADD|unix.FAN_MARK_MOUNT),
			mask,
			unix.AT_FDCWD,
			mountPath,
		); err != nil {
			return closeOnError(errors.Join(
				ErrSourceOpen,
				fmt.Errorf("mark mount %q: %w", mountPath, err),
			))
		}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Source{
		fd: fd, wakeFD: wakeFD, closeDone: make(chan struct{}),
		mountPaths: mountPaths,
		matchPID:   config.MatchPID, resolveActor: config.ResolveActor,
		now: now,
	}, nil
}

func validateSourceConfig(config SourceConfig) ([]string, error) {
	if config.MatchPID == nil ||
		len(config.MountPaths) == 0 ||
		len(config.MountPaths) > maxMountPaths {
		return nil, ErrSourceConfig
	}
	result := make([]string, 0, len(config.MountPaths))
	seen := make(map[string]struct{}, len(config.MountPaths))
	for _, source := range config.MountPaths {
		if !filepath.IsAbs(source) ||
			filepath.Clean(source) != source ||
			len(source) > maxObservedPathBytes ||
			strings.IndexByte(source, 0) >= 0 ||
			!utf8.ValidString(source) {
			return nil, ErrSourceConfig
		}
		if _, exists := seen[source]; exists {
			return nil, ErrSourceConfig
		}
		info, err := os.Stat(source)
		if err != nil || !info.IsDir() {
			return nil, errors.Join(ErrSourceConfig, err)
		}
		seen[source] = struct{}{}
		result = append(result, source)
	}
	return result, nil
}

func (source *Source) Read() (Observation, error) {
	if source == nil {
		return Observation{}, ErrSourceClosed
	}
	source.readMu.Lock()
	defer source.readMu.Unlock()

	for {
		if source.isClosed() {
			return Observation{}, ErrSourceClosed
		}
		if len(source.pending) != 0 {
			result := source.pending[0]
			source.pending[0] = Observation{}
			source.pending = source.pending[1:]
			return result, nil
		}
		data, err := source.readBatch()
		if err != nil {
			return Observation{}, err
		}
		records, decodeErr := decodeMetadataBatch(data)
		observations, materializeErr := source.materializeRecords(records)
		if materializeErr != nil {
			return Observation{}, materializeErr
		}
		if decodeErr != nil {
			incrementCounter(&source.counters.decodeFailures)
			loss, lossErr := source.newLoss(
				LossDecodeFailure,
				"malformed-kernel-metadata",
				0,
			)
			if lossErr != nil {
				return Observation{}, lossErr
			}
			observations = append(observations, Observation{Loss: &loss})
		}
		source.pending = append(source.pending, observations...)
	}
}

func (source *Source) materializeRecords(
	records []metadataRecord,
) ([]Observation, error) {
	result := make([]Observation, 0, len(records))
	for index := range records {
		record := records[index]
		if record.CloseOnly {
			source.closeRecordFDs(records[index : index+1])
			records[index].FD = unix.FAN_NOFD
			continue
		}
		incrementCounter(&source.counters.received)
		observations, err := source.materialize(record)
		records[index].FD = unix.FAN_NOFD
		if err != nil {
			source.closeRecordFDs(records[index+1:])
			return nil, err
		}
		result = append(result, observations...)
	}
	return result, nil
}

func (source *Source) materialize(record metadataRecord) ([]Observation, error) {
	if record.Overflow {
		incrementCounter(&source.counters.queueOverflows)
		loss, err := source.newLoss(
			LossQueueOverflow,
			"queue-overflow-target-count-unknown",
			0,
		)
		if err != nil {
			return nil, err
		}
		return []Observation{{Loss: &loss}}, nil
	}
	fd := int(record.FD)
	defer func() {
		if err := unix.Close(fd); err != nil {
			incrementCounter(&source.counters.closeFailures)
		}
	}()

	matches, err := source.matchPID(record.PID)
	if err != nil {
		incrementCounter(&source.counters.filterFailures)
		loss, lossErr := source.newLoss(
			LossFilterUnresolved,
			"pid-cgroup-membership-unresolved",
			0,
		)
		if lossErr != nil {
			return nil, lossErr
		}
		return []Observation{{Loss: &loss}}, nil
	}
	if !matches {
		incrementCounter(&source.counters.filtered)
		return nil, nil
	}
	kinds, err := KindsForMask(record.Mask)
	if err != nil {
		incrementCounter(&source.counters.unsupportedMasks)
		loss, lossErr := source.newLoss(
			LossUnsupportedMask,
			"target-event-mask-unsupported",
			1,
		)
		if lossErr != nil {
			return nil, lossErr
		}
		return []Observation{{Loss: &loss}}, nil
	}

	resolved, pathOK, identityOK := resolveFanotifyFile(fd)
	if !pathOK {
		incrementCounter(&source.counters.pathFailures)
	}
	if !identityOK {
		incrementCounter(&source.counters.identityFailures)
	}
	actor := workloadtypes.Actor{}
	actorResolved := false
	if source.resolveActor != nil {
		actor, actorResolved = source.resolveActor(record.PID)
		if actorResolved &&
			(actor.Validate() != nil || actor.PID != record.PID) {
			return nil, ErrActorResolution
		}
	}
	if !actorResolved {
		actor = workloadtypes.Actor{}
		incrementCounter(&source.counters.actorUnresolved)
	}
	at := source.now().UTC()
	if at.IsZero() {
		return nil, ErrSourceClock
	}
	result := make([]Observation, 0, len(kinds))
	for _, kind := range kinds {
		sequence, err := source.nextSequence()
		if err != nil {
			return nil, err
		}
		event := RawEvent{
			Kind: kind, Sequence: sequence, At: at, PID: record.PID,
			Actor: actor, ActorResolved: actorResolved,
			Path: resolved.path, PathState: resolved.pathState,
			FileType: resolved.fileType,
			Device:   resolved.device, Inode: resolved.inode,
			MountID: resolved.mountID,
		}
		result = append(result, Observation{File: &event})
		incrementCounter(&source.counters.emitted)
	}
	return result, nil
}

func (source *Source) newLoss(
	kind LossKind,
	reason string,
	droppedLowerBound uint64,
) (Loss, error) {
	at := source.now().UTC()
	if at.IsZero() {
		return Loss{}, ErrSourceClock
	}
	sequence, err := source.nextSequence()
	if err != nil {
		return Loss{}, err
	}
	incrementCounter(&source.counters.emitted)
	return Loss{
		Kind: kind, Sequence: sequence, At: at,
		DroppedLowerBound: droppedLowerBound,
		Reason:            reason,
	}, nil
}

func (source *Source) nextSequence() (uint64, error) {
	for {
		current := source.sequence.Load()
		if current == math.MaxUint64 {
			return 0, ErrSourceSequence
		}
		if source.sequence.CompareAndSwap(current, current+1) {
			return current + 1, nil
		}
	}
}

func (source *Source) readBatch() ([]byte, error) {
	for {
		fd, wakeFD, deadline, err := source.readState()
		if err != nil {
			return nil, err
		}
		timeout, err := pollTimeout(deadline)
		if err != nil {
			return nil, err
		}
		pollFDs := []unix.PollFd{
			{Fd: int32(fd), Events: unix.POLLIN},
			{Fd: int32(wakeFD), Events: unix.POLLIN},
		}
		count, pollErr := unix.Poll(pollFDs, timeout)
		if errors.Is(pollErr, unix.EINTR) {
			continue
		}
		if pollErr != nil {
			return nil, errors.Join(ErrSourcePoll, pollErr)
		}
		if count == 0 {
			return nil, os.ErrDeadlineExceeded
		}
		wakeEvents := pollFDs[1].Revents
		if wakeEvents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			if source.isClosed() {
				return nil, ErrSourceClosed
			}
			return nil, ErrSourcePoll
		}
		if wakeEvents&unix.POLLIN != 0 {
			if err := drainEventFD(wakeFD); err != nil {
				return nil, errors.Join(ErrSourcePoll, err)
			}
			continue
		}
		revents := pollFDs[0].Revents
		if revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			if source.isClosed() {
				return nil, ErrSourceClosed
			}
			return nil, ErrSourcePoll
		}
		if revents&unix.POLLIN == 0 {
			continue
		}
		buffer := make([]byte, maxReadBufferBytes)
		size, readErr := unix.Read(fd, buffer)
		if errors.Is(readErr, unix.EINTR) ||
			errors.Is(readErr, unix.EAGAIN) {
			continue
		}
		if readErr != nil {
			if source.isClosed() {
				return nil, ErrSourceClosed
			}
			return nil, errors.Join(ErrSourceRead, readErr)
		}
		if size == 0 {
			return nil, ErrSourceClosed
		}
		return buffer[:size], nil
	}
}

func pollTimeout(deadline time.Time) (int, error) {
	if deadline.IsZero() {
		return -1, nil
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, os.ErrDeadlineExceeded
	}
	timeout := remaining.Milliseconds() + 1
	if timeout > math.MaxInt32 {
		timeout = math.MaxInt32
	}
	return int(timeout), nil
}

func (source *Source) readState() (int, int, time.Time, error) {
	source.stateMu.Lock()
	defer source.stateMu.Unlock()
	if source.closed || source.fd < 0 || source.wakeFD < 0 {
		return -1, -1, time.Time{}, ErrSourceClosed
	}
	return source.fd, source.wakeFD, source.deadline, nil
}

func (source *Source) SetDeadline(deadline time.Time) {
	if source == nil {
		return
	}
	source.stateMu.Lock()
	if source.closed {
		source.stateMu.Unlock()
		return
	}
	source.deadline = deadline
	wakeFD := source.wakeFD
	source.stateMu.Unlock()
	_ = signalEventFD(wakeFD)
}

func (source *Source) MountPaths() []string {
	if source == nil {
		return nil
	}
	return append([]string(nil), source.mountPaths...)
}

func (source *Source) Counters() SourceCounters {
	if source == nil {
		return SourceCounters{}
	}
	return SourceCounters{
		Received:         source.counters.received.Load(),
		Emitted:          source.counters.emitted.Load(),
		Filtered:         source.counters.filtered.Load(),
		QueueOverflows:   source.counters.queueOverflows.Load(),
		FilterFailures:   source.counters.filterFailures.Load(),
		DecodeFailures:   source.counters.decodeFailures.Load(),
		UnsupportedMasks: source.counters.unsupportedMasks.Load(),
		PathFailures:     source.counters.pathFailures.Load(),
		IdentityFailures: source.counters.identityFailures.Load(),
		ActorUnresolved:  source.counters.actorUnresolved.Load(),
		CloseFailures:    source.counters.closeFailures.Load(),
	}
}

func (source *Source) Close() error {
	if source == nil {
		return nil
	}
	source.stateMu.Lock()
	if source.closeDone == nil {
		source.closeDone = make(chan struct{})
	}
	if source.closed {
		done := source.closeDone
		source.stateMu.Unlock()
		<-done
		source.stateMu.Lock()
		err := source.closeErr
		source.stateMu.Unlock()
		return err
	}
	source.closed = true
	done := source.closeDone
	wakeFD := source.wakeFD
	source.stateMu.Unlock()

	wakeErr := signalEventFD(wakeFD)
	source.readMu.Lock()
	source.stateMu.Lock()
	fd := source.fd
	wakeFD = source.wakeFD
	source.fd = -1
	source.wakeFD = -1
	source.stateMu.Unlock()
	closeErr := wakeErr
	if fd >= 0 {
		closeErr = errors.Join(closeErr, unix.Close(fd))
	}
	if wakeFD >= 0 {
		closeErr = errors.Join(closeErr, unix.Close(wakeFD))
	}
	source.readMu.Unlock()

	source.stateMu.Lock()
	source.closeErr = closeErr
	close(done)
	source.stateMu.Unlock()
	return closeErr
}

func (source *Source) closeRecordFDs(records []metadataRecord) {
	for _, record := range records {
		if record.FD >= 0 {
			if err := unix.Close(int(record.FD)); err != nil {
				incrementCounter(&source.counters.closeFailures)
			}
		}
	}
}

func (source *Source) isClosed() bool {
	source.stateMu.Lock()
	defer source.stateMu.Unlock()
	return source.closed
}

func signalEventFD(fd int) error {
	if fd < 0 {
		return nil
	}
	var value [8]byte
	binary.NativeEndian.PutUint64(value[:], 1)
	for {
		_, err := unix.Write(fd, value[:])
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EAGAIN):
			return nil
		default:
			return err
		}
	}
}

func drainEventFD(fd int) error {
	var value [8]byte
	for {
		_, err := unix.Read(fd, value[:])
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EAGAIN):
			return nil
		default:
			return err
		}
	}
}

func decodeMetadataBatch(data []byte) ([]metadataRecord, error) {
	result := make([]metadataRecord, 0, len(data)/int(metadataSize))
	offset := 0
	for offset < len(data) {
		if len(data)-offset < int(metadataSize) {
			return result, ErrMetadata
		}
		current := data[offset:]
		eventLength := binary.NativeEndian.Uint32(current[0:4])
		version := current[4]
		reserved := current[5]
		metadataLength := binary.NativeEndian.Uint16(current[6:8])
		mask := binary.NativeEndian.Uint64(current[8:16])
		fd := int32(binary.NativeEndian.Uint32(current[16:20]))
		pidValue := int32(binary.NativeEndian.Uint32(current[20:24]))
		if eventLength != metadataSize ||
			version != fanotifyMetadataVersion ||
			reserved != 0 ||
			metadataLength != uint16(metadataSize) ||
			int(eventLength) > len(data)-offset {
			return result, ErrMetadata
		}
		record := metadataRecord{
			Mask: mask, FD: fd,
			Overflow: mask == MaskQueueOverflow,
		}
		if pidValue > 0 {
			record.PID = uint32(pidValue)
		}
		if record.Overflow {
			if fd != unix.FAN_NOFD || pidValue != 0 {
				if fd >= 0 {
					record.CloseOnly = true
					result = append(result, record)
				}
				return result, ErrMetadata
			}
		} else {
			if fd < 0 || pidValue <= 0 || pidValue > 4194304 {
				if fd >= 0 {
					record.CloseOnly = true
					result = append(result, record)
				}
				return result, ErrMetadata
			}
		}
		result = append(result, record)
		offset += int(eventLength)
	}
	if offset != len(data) {
		return result, ErrMetadata
	}
	return result, nil
}

func resolveFanotifyFile(fd int) (resolvedFile, bool, bool) {
	result := resolvedFile{pathState: "unknown", fileType: "unknown"}
	pathOK := false
	identityOK := false

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err == nil && stat.Ino != 0 {
		result.device = uint64(stat.Dev)
		result.inode = stat.Ino
		result.fileType = fanotifyFileType(stat.Mode)
		identityOK = true
		var statx unix.Statx_t
		if err := unix.Statx(
			fd,
			"",
			unix.AT_EMPTY_PATH|unix.AT_STATX_DONT_SYNC,
			unix.STATX_MNT_ID,
			&statx,
		); err == nil && statx.Mask&unix.STATX_MNT_ID != 0 {
			result.mountID = statx.Mnt_id
		}
	}

	pathValue, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd))
	if err != nil ||
		pathValue == "" ||
		len(pathValue) > maxObservedPathBytes ||
		strings.IndexByte(pathValue, 0) >= 0 ||
		!utf8.ValidString(pathValue) {
		return result, false, identityOK
	}
	const deletedSuffix = " (deleted)"
	switch {
	case strings.HasSuffix(pathValue, deletedSuffix):
		pathValue = strings.TrimSuffix(pathValue, deletedSuffix)
		if pathValue == "" {
			return result, false, identityOK
		}
		result.pathState = "raced"
	case filepath.IsAbs(pathValue):
		result.pathState = "resolved"
	default:
		result.pathState = "aliased"
	}
	result.path = pathValue
	pathOK = true
	return result, pathOK, identityOK
}

func fanotifyFileType(mode uint32) string {
	switch mode & unix.S_IFMT {
	case unix.S_IFREG:
		return "regular"
	case unix.S_IFDIR:
		return "directory"
	case unix.S_IFLNK:
		return "symlink"
	case unix.S_IFSOCK:
		return "socket"
	case unix.S_IFIFO:
		return "fifo"
	case unix.S_IFCHR, unix.S_IFBLK:
		return "device"
	default:
		return "unknown"
	}
}

func incrementCounter(counter *atomic.Uint64) {
	for {
		current := counter.Load()
		if current == math.MaxUint64 {
			return
		}
		if counter.CompareAndSwap(current, current+1) {
			return
		}
	}
}
