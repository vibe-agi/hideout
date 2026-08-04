//go:build darwin || linux

package manager

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openMigrationBundleNoFollow(path string) (*os.File, error) {
	fd, err := unix.Open(
		path,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open migration bundle: invalid file descriptor")
	}
	return file, nil
}

func openMigrationBundleReadWriteNoFollow(path string) (*os.File, error) {
	fd, err := unix.Open(
		path,
		unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open migration partial: invalid file descriptor")
	}
	return file, nil
}

func migrationBundleFileDeviceInode(file *os.File) (uint64, uint64, error) {
	if file == nil {
		return 0, 0, fmt.Errorf("migration bundle file is nil")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return 0, 0, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return 0, 0, fmt.Errorf("migration bundle is not a regular file")
	}
	return uint64(stat.Dev), uint64(stat.Ino), nil
}
