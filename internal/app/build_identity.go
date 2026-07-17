package app

import (
	"crypto/sha256"
	"encoding/hex"
	"runtime"
	"strings"
)

// daemonBuildID identifies the exact CLI build that must own the per-store
// daemon. Build time is included so a local rebuild from the same dirty source
// still replaces the previously installed daemon.
func daemonBuildID() string {
	identity := strings.Join([]string{
		strings.TrimSpace(Version),
		strings.TrimSpace(Commit),
		strings.TrimSpace(BuildTime),
		runtime.Version(),
		runtime.GOOS,
		runtime.GOARCH,
	}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}
