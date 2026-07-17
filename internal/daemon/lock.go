package daemon

import (
	"os"
	"syscall"
)

// acquireLock takes an exclusive, non-blocking advisory lock on one stable inode.
// The file is intentionally retained after unlock: unlinking a flock file allows
// one process to hold the old inode while another process locks a newly-created
// inode at the same path.
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
}
