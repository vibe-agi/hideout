package app

import "testing"

func TestDaemonBuildIDChangesForEveryInstalledBuild(t *testing.T) {
	oldVersion, oldCommit, oldBuildTime := Version, Commit, BuildTime
	t.Cleanup(func() {
		Version, Commit, BuildTime = oldVersion, oldCommit, oldBuildTime
	})
	Version = "0.1.0-alpha.1"
	Commit = "0123456789abcdef0123456789abcdef01234567"
	BuildTime = "2026-07-17T05:00:00Z"
	first := daemonBuildID()
	if len(first) != 64 || !validHex(first) {
		t.Fatalf("daemon build id = %q", first)
	}
	BuildTime = "2026-07-17T05:00:01Z"
	if second := daemonBuildID(); second == first {
		t.Fatalf("rebuilt binary retained daemon build id %q", second)
	}
}

func validHex(value string) bool {
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
