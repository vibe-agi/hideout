//go:build !darwin

package lima

import "errors"

func platformMigrationCloneFile(string, string) error {
	return errors.New("copy-on-write file cloning is unavailable on this platform")
}
