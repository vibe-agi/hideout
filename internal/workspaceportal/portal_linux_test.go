//go:build linux

package workspaceportal

import (
	"context"
	"os"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestPortalFuseTreeRenameUpdatesNodeAndDescendantPaths(t *testing.T) {
	tree := &portalFuseTree{nodes: make(map[string]*portalFuseNode)}
	parent := &portalFuseNode{tree: tree, path: "old"}
	child := &portalFuseNode{tree: tree, path: "old/child"}
	tree.register(parent)
	tree.register(child)

	tree.renamePath("old", "new")

	if parent.path != "new" || child.path != "new/child" {
		t.Fatalf("renamed paths = %q, %q", parent.path, child.path)
	}
	if tree.nodes["old"] != nil || tree.nodes["old/child"] != nil || tree.nodes["new"] != parent || tree.nodes["new/child"] != child {
		t.Fatalf("renamed path index = %#v", tree.nodes)
	}
}

func TestPortalFuseStableInodeBindsHostIdentityToPortalPath(t *testing.T) {
	const hostInode = 42
	before := portalFuseStableInode("before.txt", hostInode)
	after := portalFuseStableInode("after.txt", hostInode)
	if before == 0 || after == 0 || before == after {
		t.Fatalf("stable inode values = %d, %d", before, after)
	}
	if repeated := portalFuseStableInode("before.txt", hostInode); repeated != before {
		t.Fatalf("repeated stable inode = %d, want %d", repeated, before)
	}
}

func TestPortalFuseAttrsUseSyntheticTargetOwnership(t *testing.T) {
	info := workspaceattach.PortalFileInfo{
		Mode: os.FileMode(0o640), Size: 17, Inode: 42, UID: 501, GID: 20, Nlink: 1,
	}
	var attr fuse.Attr
	fillPortalFuseAttr(&attr, info, 99, 1000, 1001)
	if attr.Owner.Uid != 1000 || attr.Owner.Gid != 1001 {
		t.Fatalf("synthetic owner=%d:%d", attr.Owner.Uid, attr.Owner.Gid)
	}
	if attr.Owner.Uid == info.UID || attr.Owner.Gid == info.GID {
		t.Fatalf("host ownership escaped into guest attr: host=%d:%d attr=%d:%d", info.UID, info.GID, attr.Owner.Uid, attr.Owner.Gid)
	}
}

func TestPortalFuseRejectsOwnershipAndDeviceMutationWithStableErrno(t *testing.T) {
	node := &portalFuseNode{}
	for _, input := range []*fuse.SetAttrIn{
		{SetAttrInCommon: fuse.SetAttrInCommon{Valid: fuse.FATTR_UID, Owner: fuse.Owner{Uid: uint32(os.Getuid())}}},
		{SetAttrInCommon: fuse.SetAttrInCommon{Valid: fuse.FATTR_GID, Owner: fuse.Owner{Gid: uint32(os.Getgid())}}},
	} {
		if got := node.Setattr(context.Background(), nil, input, &fuse.AttrOut{}); got != syscall.EPERM {
			t.Fatalf("ownership mutation errno=%v want EPERM", got)
		}
	}
	for _, mode := range []uint32{syscall.S_IFBLK | 0o600, syscall.S_IFCHR | 0o600, syscall.S_IFIFO | 0o600, syscall.S_IFSOCK | 0o600} {
		if _, got := node.Mknod(context.Background(), "unsupported", mode, 0, &fuse.EntryOut{}); got != syscall.ENOTSUP {
			t.Fatalf("mknod mode=%#o errno=%v want ENOTSUP", mode, got)
		}
	}
}
