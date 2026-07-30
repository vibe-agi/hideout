package main

import (
	"strings"

	filecollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/file"
)

// fileReadNoiseRoots are guest runtime namespaces whose non-mutating file
// activity is normally loader, library, device, or kernel-introspection noise.
// Mutations below these roots are never filtered.
var fileReadNoiseRoots = [...]string{
	"/bin",
	"/dev",
	"/lib",
	"/lib64",
	"/proc",
	"/sbin",
	"/sys",
	"/usr",
}

// retainFileObservation keeps the security-relevant local file view bounded
// before it reaches the observer transport. All mutations are retained,
// including mutations under system roots. Open/read/mmap activity remains
// visible for workspace, profile, HostFS, home, temporary, configuration, and
// other non-runtime paths.
//
// Unknown paths and future event kinds are retained conservatively. Coverage
// advertises this deliberate system-runtime read filter as a Partial
// limitation; it must never be interpreted as a complete host audit trail.
func retainFileObservation(event filecollector.Event) bool {
	switch event.Kind {
	case filecollector.EventWrite,
		filecollector.EventCreate,
		filecollector.EventTruncate,
		filecollector.EventRename,
		filecollector.EventUnlink,
		filecollector.EventMetadata,
		filecollector.EventHardlink,
		filecollector.EventSymlink,
		filecollector.EventMkdir,
		filecollector.EventRmdir:
		return true
	case filecollector.EventOpen,
		filecollector.EventRead,
		filecollector.EventMmap:
		if event.Path == "" {
			return true
		}
		if event.Path == "/etc/ld.so.cache" {
			return false
		}
		for _, root := range fileReadNoiseRoots {
			if pathWithinRoot(event.Path, root) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

func pathWithinRoot(value, root string) bool {
	return value == root ||
		(strings.HasPrefix(value, root) &&
			len(value) > len(root) &&
			value[len(root)] == '/')
}
