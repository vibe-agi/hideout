//go:build darwin || linux

package profilestate

import (
	"os"
	"syscall"
)

type fileIdentity struct {
	device uint64
	inode  uint64
	links  uint64
}

func identityFromInfo(info os.FileInfo) (fileIdentity, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return fileIdentity{}, false
	}
	return fileIdentity{
		device: uint64(stat.Dev), inode: uint64(stat.Ino), links: uint64(stat.Nlink),
	}, true
}
