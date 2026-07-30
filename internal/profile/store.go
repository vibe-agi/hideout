package profile

import "path/filepath"

const profileRevisionFileName = "projection.json"

// ProfileRevisionPath is the Manager-owned, non-secret projection sidecar.
// Callers must validate the profile name before using this path.
func (s Store) ProfileRevisionPath(name string) string {
	return filepath.Join(s.ProfileDir(name), ".manager", profileRevisionFileName)
}
