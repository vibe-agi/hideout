package daemon

import (
	"os"
	"syscall"
)

// acquireLock takes an exclusive, non-blocking advisory lock. The lock is held by
// the process (released by the kernel on death), so a crashed daemon's lock is
// reclaimable while a live daemon's is not — the authoritative single-instance
// signal alongside the socket liveness probe.
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, errAlreadyRunning
	}
	return f, nil
}

func releaseLock(f *os.File, path string) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
	_ = os.Remove(path)
}
