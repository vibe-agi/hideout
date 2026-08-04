//go:build darwin || linux

package lima

import (
	"errors"
	"os"
	"syscall"
)

func platformMigrationStageFileIdentity(
	info os.FileInfo,
) (migrationStageFileIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 || info.Size() <= 0 || !info.Mode().IsRegular() {
		return migrationStageFileIdentity{}, errors.New("migration stage file identity is unproved")
	}
	return migrationStageFileIdentity{
		Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Links: uint64(stat.Nlink),
		Size: info.Size(), Mode: uint32(info.Mode()),
		ModTimeUnixNano: info.ModTime().UnixNano(),
	}, nil
}
