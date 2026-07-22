//go:build linux

package workspaceattach

import (
	"testing"

	"github.com/hanwen/go-fuse/v2/fuse"
)

func TestPortalOpenFlagsAcceptFuseExecutionHint(t *testing.T) {
	encoded, err := encodePortalOpenFlags(fuse.FMODE_EXEC | portalIgnoredKernelOpenFlags())
	if err != nil {
		t.Fatalf("encode FUSE execution hint: %v", err)
	}
	if encoded != portalOpenReadOnly {
		t.Fatalf("FUSE execution hint changed semantic flags: %#x", encoded)
	}
}
