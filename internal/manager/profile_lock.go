package manager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

var profileMutationLocks sync.Map

func (c Core) withProfileMutationLock(profileName string, fn func() error) (err error) {
	profileName, err = normalizeManagerProfileName(profileName)
	if err != nil {
		return err
	}
	key := filepath.Join(c.Store.Root, ".locks", "profile-mutations", profileName+".lock")
	value, _ := profileMutationLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	lock, err := openProfileMutationLock(c.Store.Root, profileName)
	if err != nil {
		return err
	}
	fd := int(lock.Fd())
	defer func() {
		err = errors.Join(err, unix.Flock(fd, unix.LOCK_UN), lock.Close())
	}()
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock profile mutation: %w", err)
	}
	return fn()
}

func openProfileMutationLock(root, profileName string) (*os.File, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open profile store for mutation lock: %w", err)
	}
	defer unix.Close(rootFD)

	locksFD, err := openOrCreateLockDirectory(rootFD, ".locks")
	if err != nil {
		return nil, err
	}
	defer unix.Close(locksFD)
	profilesFD, err := openOrCreateLockDirectory(locksFD, "profile-mutations")
	if err != nil {
		return nil, err
	}
	defer unix.Close(profilesFD)

	name := profileName + ".lock"
	fd, err := unix.Openat(profilesFD, name, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open profile mutation lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), filepath.Join(root, ".locks", "profile-mutations", name))
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open profile mutation lock: invalid file descriptor")
	}
	return file, nil
}

func openOrCreateLockDirectory(parentFD int, name string) (int, error) {
	if err := unix.Mkdirat(parentFD, name, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return -1, fmt.Errorf("create profile mutation lock directory: %w", err)
	}
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, fmt.Errorf("open profile mutation lock directory: %w", err)
	}
	return fd, nil
}
