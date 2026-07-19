//go:build linux && arm64

package workspaceattach

import "testing"

func TestPortalOpenFlagsIgnoreLinuxArm64KernelLargeFile(t *testing.T) {
	encoded, err := encodePortalOpenFlags(portalLinuxArm64KernelLargeFile)
	if err != nil {
		t.Fatalf("encode kernel O_LARGEFILE: %v", err)
	}
	if encoded != portalOpenReadOnly {
		t.Fatalf("kernel O_LARGEFILE changed semantic flags: %#x", encoded)
	}
}
