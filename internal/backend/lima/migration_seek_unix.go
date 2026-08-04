//go:build darwin || linux

package lima

import "golang.org/x/sys/unix"

func migrationSeekData(fd int, offset int64) (int64, error) {
	return unix.Seek(fd, offset, unix.SEEK_DATA)
}

func migrationSeekHole(fd int, offset int64) (int64, error) {
	return unix.Seek(fd, offset, unix.SEEK_HOLE)
}

func migrationSeekNoMoreData(err error) bool {
	return err == unix.ENXIO
}
