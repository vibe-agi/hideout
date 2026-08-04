//go:build !darwin && !linux

package manager

import (
	"errors"
	"os"
)

var errMigrationBundleFileUnsupported = errors.New("migration bundle file inspection is unavailable on this host")

func openMigrationBundleNoFollow(string) (*os.File, error) {
	return nil, errMigrationBundleFileUnsupported
}

func openMigrationBundleReadWriteNoFollow(string) (*os.File, error) {
	return nil, errMigrationBundleFileUnsupported
}

func migrationBundleFileDeviceInode(*os.File) (uint64, uint64, error) {
	return 0, 0, errMigrationBundleFileUnsupported
}
