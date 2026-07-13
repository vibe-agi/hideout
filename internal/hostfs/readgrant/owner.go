package readgrant

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

var (
	ErrSessionNotLive    = errors.New("session is not live")
	ErrSessionUnprovable = errors.New("session liveness is unprovable")
)

type Owner struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
}

func AcquireOwner(path string) (*Owner, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errors.New("session owner lock is already held")
		}
		return nil, err
	}
	return &Owner{file: f}, nil
}

func ProbeOwner(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return errors.Join(ErrSessionUnprovable, err)
	}
	defer f.Close()
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return nil
	}
	if err != nil {
		return errors.Join(ErrSessionUnprovable, err)
	}
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return ErrSessionNotLive
}

func (o *Owner) Close() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	o.closed = true
	if o.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(o.file.Fd()), unix.LOCK_UN)
	closeErr := o.file.Close()
	return errors.Join(unlockErr, closeErr)
}
