//go:build linux

package workspaceportal

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestPortalMountRetriesOnlyDNSTimeouts(t *testing.T) {
	credential := workspaceattach.PortalCredential{}
	limits := workspaceattach.DefaultPortalLimits()
	calls := 0
	wantClient := &workspaceattach.PortalClient{}
	client, err := dialPortalForMount(context.Background(), "host.lima.internal:43127", credential, limits, time.Millisecond,
		func(context.Context, string, workspaceattach.PortalCredential, workspaceattach.PortalLimits) (*workspaceattach.PortalClient, error) {
			calls++
			if calls < 3 {
				return nil, &net.DNSError{Err: "temporary timeout", Name: "host.lima.internal", IsTimeout: true, IsTemporary: true}
			}
			return wantClient, nil
		})
	if err != nil || client != wantClient || calls != 3 {
		t.Fatalf("DNS timeout retry client=%v calls=%d err=%v", client, calls, err)
	}

	want := errors.New("portal credential refused")
	calls = 0
	_, err = dialPortalForMount(context.Background(), "host.lima.internal:43127", credential, limits, time.Millisecond,
		func(context.Context, string, workspaceattach.PortalCredential, workspaceattach.PortalLimits) (*workspaceattach.PortalClient, error) {
			calls++
			return nil, want
		})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("non-DNS error was retried calls=%d err=%v", calls, err)
	}
}

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

func TestPortalFuseTreeRemoveInvalidationDropsCachedSubtreeImmediately(t *testing.T) {
	tree := &portalFuseTree{
		nodes: map[string]*portalFuseNode{},
		info: map[string]workspaceattach.PortalFileInfo{
			"removed":       {Inode: 41},
			"removed/child": {Inode: 42},
		},
		dirs: map[string]uint64{".": 7},
	}
	parent := &portalFuseNode{tree: tree, path: "."}
	removed := &portalFuseNode{tree: tree, path: "removed"}
	child := &portalFuseNode{tree: tree, path: "removed/child"}
	tree.register(parent)
	tree.register(removed)
	tree.register(child)

	tree.invalidatePath("removed", true, false)

	if tree.nodes["removed"] != nil || tree.nodes["removed/child"] != nil ||
		tree.info["removed"].Inode != 0 || tree.info["removed/child"].Inode != 0 {
		t.Fatalf("removed subtree remained cached: nodes=%#v info=%#v", tree.nodes, tree.info)
	}
	if tree.nodes["."] != parent || tree.directoryGeneration(".") != 8 {
		t.Fatalf("parent invalidation state nodes=%#v generation=%d", tree.nodes, tree.directoryGeneration("."))
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
