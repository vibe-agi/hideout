package workspaceattach

import (
	"syscall"
	"testing"
)

func TestPortalOpenFlagsPreserveNoFollowSemantics(t *testing.T) {
	t.Logf("O_NOFOLLOW=%#x supported=%#x", syscall.O_NOFOLLOW, portalSupportedLocalOpenFlags())
	encoded, err := encodePortalOpenFlags(syscall.O_RDONLY | syscall.O_NOFOLLOW)
	if err != nil {
		t.Fatalf("encode no-follow open flags: %v", err)
	}
	if encoded&portalOpenNoFollow == 0 {
		t.Fatal("encoded flags omit no-follow semantics")
	}
	decoded, err := decodePortalOpenFlags(encoded)
	if err != nil {
		t.Fatalf("decode no-follow open flags: %v", err)
	}
	if decoded&syscall.O_NOFOLLOW == 0 {
		t.Fatalf("decoded flags omit no-follow semantics: %#x", decoded)
	}
}
