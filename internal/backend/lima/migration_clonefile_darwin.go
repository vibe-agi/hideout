//go:build darwin

package lima

import "golang.org/x/sys/unix"

func platformMigrationCloneFile(source, destination string) error {
	return unix.Clonefile(source, destination, 0)
}
