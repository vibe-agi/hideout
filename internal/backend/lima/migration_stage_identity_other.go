//go:build !darwin && !linux

package lima

import (
	"errors"
	"os"
)

func platformMigrationStageFileIdentity(
	os.FileInfo,
) (migrationStageFileIdentity, error) {
	return migrationStageFileIdentity{}, errors.New(
		"migration stage file identity is unsupported on this platform",
	)
}
