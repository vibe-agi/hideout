//go:build linux

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const defaultSessionCgroupRoot = "/sys/fs/cgroup/hideout"

var (
	errSessionCgroupUnavailable = errors.New("session cgroup v2 is unavailable")
	errSessionCgroupExists      = errors.New("session cgroup leaf already exists")
	errSessionCgroupDelegated   = errors.New("session cgroup is delegated or target-writable")
	errSessionCgroupIdentity    = errors.New("session cgroup file descriptor identity is invalid")
	errSessionCgroupNotEmpty    = errors.New("session cgroup still contains processes")
	errSessionCgroupRemoved     = errors.New("session cgroup was already removed")
)

type sessionCgroupHandle struct {
	FD        int
	ID        uint64
	Delegated bool
	Close     func() error
}

type sessionCgroupBackend interface {
	Create(string) (sessionCgroupHandle, error)
	Validate(string, sessionCgroupHandle) error
	Processes(string, sessionCgroupHandle) ([]int, error)
	Remove(string, sessionCgroupHandle) error
}

type sessionCgroupKiller interface {
	Kill(string, sessionCgroupHandle) error
}

type sessionCgroupOptions struct {
	Root                       string
	SessionID                  string
	Backend                    sessionCgroupBackend
	SkipAtomicPlacementForTest bool
}

type sessionCgroup struct {
	mu      sync.Mutex
	path    string
	handle  sessionCgroupHandle
	backend sessionCgroupBackend
	removed bool
	closed  bool
}

type sessionCgroupCapability struct {
	Available bool
	Root      string
	Reason    string
}

func newSessionCgroup(options sessionCgroupOptions) (*sessionCgroup, error) {
	root := filepath.Clean(options.Root)
	if root == "." || !filepath.IsAbs(root) ||
		!sessionIDPattern.MatchString(options.SessionID) {
		return nil, errSessionCgroupIdentity
	}
	backend := options.Backend
	if backend == nil {
		if root != defaultSessionCgroupRoot &&
			!strings.HasPrefix(root, "/sys/fs/cgroup/") {
			return nil, errSessionCgroupIdentity
		}
		backend = osSessionCgroupBackend{}
	}
	path := filepath.Join(root, "sessions", options.SessionID)
	if filepath.Dir(filepath.Dir(path)) != root {
		return nil, errSessionCgroupIdentity
	}
	handle, err := backend.Create(path)
	if err != nil {
		return nil, err
	}
	closeRejected := func() {
		_ = backend.Remove(path, handle)
		if handle.Close != nil {
			_ = handle.Close()
		}
	}
	if handle.Delegated {
		closeRejected()
		return nil, errSessionCgroupDelegated
	}
	if err := backend.Validate(path, handle); err != nil {
		closeRejected()
		return nil, errors.Join(errSessionCgroupIdentity, err)
	}
	return &sessionCgroup{
		path: path, handle: handle, backend: backend,
	}, nil
}

func (group *sessionCgroup) Path() string {
	if group == nil {
		return ""
	}
	return group.path
}

func (group *sessionCgroup) ID() uint64 {
	if group == nil {
		return 0
	}
	return group.handle.ID
}

func (group *sessionCgroup) BindTarget(attributes *syscall.SysProcAttr) error {
	if group == nil || attributes == nil {
		return errSessionCgroupIdentity
	}
	group.mu.Lock()
	defer group.mu.Unlock()
	if group.removed {
		return errSessionCgroupRemoved
	}
	if group.closed || group.handle.FD < 0 || group.handle.ID == 0 {
		return errSessionCgroupIdentity
	}
	if attributes.UseCgroupFD || attributes.CgroupFD != 0 {
		return errors.New("target process attributes already carry a cgroup")
	}
	attributes.UseCgroupFD = true
	attributes.CgroupFD = group.handle.FD
	return nil
}

func (group *sessionCgroup) OwnsObservation(pid uint32, cgroupID uint64) bool {
	if group == nil || pid == 0 || pid > 4194304 {
		return false
	}
	group.mu.Lock()
	defer group.mu.Unlock()
	return !group.removed && !group.closed &&
		group.handle.ID != 0 && cgroupID == group.handle.ID
}

func (group *sessionCgroup) ProveEmptyAndRemove() error {
	if group == nil {
		return errSessionCgroupIdentity
	}
	group.mu.Lock()
	defer group.mu.Unlock()
	if group.removed {
		return nil
	}
	if group.closed {
		return errSessionCgroupIdentity
	}
	processes, err := group.backend.Processes(group.path, group.handle)
	if err != nil {
		return err
	}
	if len(processes) != 0 {
		return fmt.Errorf("%w: %v", errSessionCgroupNotEmpty, processes)
	}
	if err := group.backend.Remove(group.path, group.handle); err != nil {
		return err
	}
	group.removed = true
	return nil
}

func (group *sessionCgroup) Kill() error {
	if group == nil {
		return errSessionCgroupIdentity
	}
	group.mu.Lock()
	defer group.mu.Unlock()
	if group.removed {
		return nil
	}
	if group.closed {
		return errSessionCgroupIdentity
	}
	if killer, ok := group.backend.(sessionCgroupKiller); ok {
		return killer.Kill(group.path, group.handle)
	}
	processes, err := group.backend.Processes(group.path, group.handle)
	if err != nil {
		return err
	}
	var failures []error
	for _, pid := range processes {
		if err := unix.Kill(pid, unix.SIGKILL); err != nil &&
			!errors.Is(err, unix.ESRCH) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (group *sessionCgroup) Close() error {
	if group == nil {
		return nil
	}
	group.mu.Lock()
	defer group.mu.Unlock()
	if group.closed {
		return nil
	}
	group.closed = true
	if group.handle.Close == nil {
		return nil
	}
	return group.handle.Close()
}

func probeSessionCgroup(root string) sessionCgroupCapability {
	if root == "" {
		root = defaultSessionCgroupRoot
	}
	sessionID := fmt.Sprintf(
		"ses_probe_%d_%d",
		os.Getpid(),
		time.Now().UTC().UnixNano(),
	)
	group, err := newSessionCgroup(sessionCgroupOptions{
		Root: root, SessionID: sessionID,
	})
	if err != nil {
		return sessionCgroupCapability{
			Root: root, Reason: boundedSummary(err),
		}
	}
	defer group.Close()
	if err := group.ProveEmptyAndRemove(); err != nil {
		return sessionCgroupCapability{
			Root: root, Reason: boundedSummary(err),
		}
	}
	return sessionCgroupCapability{
		Available: true, Root: root, Reason: "cgroup-v2-leaf-ready",
	}
}

type osSessionCgroupBackend struct{}

func (osSessionCgroupBackend) Create(path string) (sessionCgroupHandle, error) {
	root := filepath.Dir(filepath.Dir(path))
	if err := requireCgroupV2(root); err != nil {
		return sessionCgroupHandle{}, err
	}
	if err := ensureRootOwnedDirectory(root); err != nil {
		return sessionCgroupHandle{}, err
	}
	sessionsRoot := filepath.Join(root, "sessions")
	if err := ensureRootOwnedDirectory(sessionsRoot); err != nil {
		return sessionCgroupHandle{}, err
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return sessionCgroupHandle{}, errSessionCgroupExists
		}
		return sessionCgroupHandle{}, fmt.Errorf("create session cgroup: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(path)
		}
	}()
	fd, err := unix.Open(
		path,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return sessionCgroupHandle{}, fmt.Errorf("open session cgroup: %w", err)
	}
	var closeOnce sync.Once
	closeHandle := func() error {
		var closeErr error
		closeOnce.Do(func() { closeErr = unix.Close(fd) })
		return closeErr
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = closeHandle()
		return sessionCgroupHandle{}, fmt.Errorf("stat session cgroup fd: %w", err)
	}
	kernelID, err := kernelCgroupID(path)
	if err != nil || kernelID != stat.Ino {
		_ = closeHandle()
		return sessionCgroupHandle{}, errors.Join(errSessionCgroupIdentity, err)
	}
	delegated, err := sessionCgroupDelegated(path)
	if err != nil {
		_ = closeHandle()
		return sessionCgroupHandle{}, err
	}
	cleanup = false
	return sessionCgroupHandle{
		FD: fd, ID: kernelID, Delegated: delegated, Close: closeHandle,
	}, nil
}

func (osSessionCgroupBackend) Validate(path string, handle sessionCgroupHandle) error {
	if handle.FD < 0 || handle.ID == 0 {
		return errSessionCgroupIdentity
	}
	var descriptor unix.Stat_t
	if err := unix.Fstat(handle.FD, &descriptor); err != nil {
		return err
	}
	var filesystem unix.Statfs_t
	if err := unix.Fstatfs(handle.FD, &filesystem); err != nil {
		return err
	}
	if filesystem.Type != unix.CGROUP2_SUPER_MAGIC {
		return errSessionCgroupUnavailable
	}
	var pathStat unix.Stat_t
	if err := unix.Fstatat(
		unix.AT_FDCWD,
		path,
		&pathStat,
		unix.AT_SYMLINK_NOFOLLOW,
	); err != nil {
		return err
	}
	if descriptor.Ino != handle.ID ||
		descriptor.Ino != pathStat.Ino ||
		descriptor.Dev != pathStat.Dev ||
		descriptor.Mode&unix.S_IFMT != unix.S_IFDIR ||
		pathStat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errSessionCgroupIdentity
	}
	kernelID, err := kernelCgroupID(path)
	if err != nil || kernelID != handle.ID {
		return errors.Join(errSessionCgroupIdentity, err)
	}
	delegated, err := sessionCgroupDelegated(path)
	if err != nil {
		return err
	}
	if delegated {
		return errSessionCgroupDelegated
	}
	return nil
}

func kernelCgroupID(path string) (uint64, error) {
	handle, _, err := unix.NameToHandleAt(unix.AT_FDCWD, path, 0)
	if err != nil {
		return 0, fmt.Errorf("read cgroup kernel identity: %w", err)
	}
	value := handle.Bytes()
	if len(value) != 8 {
		return 0, errSessionCgroupIdentity
	}
	identity := binary.NativeEndian.Uint64(value)
	if identity == 0 {
		return 0, errSessionCgroupIdentity
	}
	return identity, nil
}

func (osSessionCgroupBackend) Processes(
	path string,
	handle sessionCgroupHandle,
) ([]int, error) {
	if err := (osSessionCgroupBackend{}).Validate(path, handle); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(path, "cgroup.procs"))
	if err != nil {
		return nil, fmt.Errorf("read session cgroup membership: %w", err)
	}
	fields := strings.Fields(string(data))
	processes := make([]int, 0, len(fields))
	seen := make(map[int]struct{}, len(fields))
	for _, field := range fields {
		pid, err := strconv.Atoi(field)
		if err != nil || pid <= 0 {
			return nil, errSessionCgroupIdentity
		}
		if _, exists := seen[pid]; exists {
			continue
		}
		seen[pid] = struct{}{}
		processes = append(processes, pid)
	}
	sort.Ints(processes)
	return processes, nil
}

func (backend osSessionCgroupBackend) Remove(
	path string,
	handle sessionCgroupHandle,
) error {
	processes, err := backend.Processes(path, handle)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(processes) != 0 {
		return fmt.Errorf("%w: %v", errSessionCgroupNotEmpty, processes)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove session cgroup: %w", err)
	}
	return nil
}

func (backend osSessionCgroupBackend) Kill(
	path string,
	handle sessionCgroupHandle,
) error {
	if err := backend.Validate(path, handle); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(path, "cgroup.kill"), []byte("1\n"), 0); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) &&
		!errors.Is(err, unix.EOPNOTSUPP) {
		return fmt.Errorf("kill session cgroup: %w", err)
	}
	processes, err := backend.Processes(path, handle)
	if err != nil {
		return err
	}
	var failures []error
	for _, pid := range processes {
		if err := unix.Kill(pid, unix.SIGKILL); err != nil &&
			!errors.Is(err, unix.ESRCH) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func requireCgroupV2(root string) error {
	mount := "/sys/fs/cgroup"
	if root != mount && !strings.HasPrefix(root, mount+"/") {
		return errSessionCgroupIdentity
	}
	var filesystem unix.Statfs_t
	if err := unix.Statfs(mount, &filesystem); err != nil {
		return fmt.Errorf("%w: %v", errSessionCgroupUnavailable, err)
	}
	if filesystem.Type != unix.CGROUP2_SUPER_MAGIC {
		return errSessionCgroupUnavailable
	}
	if _, err := os.Stat(filepath.Join(mount, "cgroup.controllers")); err != nil {
		return fmt.Errorf("%w: %v", errSessionCgroupUnavailable, err)
	}
	return nil
}

func ensureRootOwnedDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil {
			return fmt.Errorf("create Hideout cgroup directory: %w", err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
		return errSessionCgroupDelegated
	}
	return nil
}

func sessionCgroupDelegated(path string) (bool, error) {
	for _, candidate := range []string{path, filepath.Join(path, "cgroup.procs")} {
		info, err := os.Lstat(candidate)
		if err != nil {
			return false, err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || info.Mode()&os.ModeSymlink != 0 {
			return false, errSessionCgroupIdentity
		}
		if stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
			return true, nil
		}
	}
	return false, nil
}
