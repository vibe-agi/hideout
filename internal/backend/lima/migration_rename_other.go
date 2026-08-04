//go:build !darwin && !linux

package lima

import "errors"

func renameMigrationDirectoryNoReplace(_, _ string) error {
	return errors.New("no-replace migration promotion is unsupported on this host")
}
