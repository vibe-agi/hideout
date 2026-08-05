//go:build !darwin && !linux

package profilestate

import "os"

type fileIdentity struct {
	device uint64
	inode  uint64
	links  uint64
}

func identityFromInfo(os.FileInfo) (fileIdentity, bool) {
	return fileIdentity{}, false
}
