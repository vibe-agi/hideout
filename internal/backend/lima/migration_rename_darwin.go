//go:build darwin

package lima

import "golang.org/x/sys/unix"

func renameMigrationDirectoryNoReplace(source, destination string) error {
	return unix.RenamexNp(source, destination, unix.RENAME_EXCL)
}
