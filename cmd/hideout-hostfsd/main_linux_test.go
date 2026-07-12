//go:build linux

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/vibe-agi/hideout/internal/broker"
)

func TestHostFSDWriteClassRPCShapes(t *testing.T) {
	client, requests, cleanup := fakeHostFSDBroker(t)
	defer cleanup()
	node := &hostFSNode{client: client, path: "/Users/alice/file.txt", kind: "file"}

	if written, errno := node.Write(context.Background(), nil, []byte("hi"), 3); errno != 0 || written != 2 {
		t.Fatalf("Write written=%d errno=%v", written, errno)
	}
	assertRequest(t, <-requests, "host.fs.write.replace", "/Users/alice/file.txt", map[string]any{
		"offset":     float64(3),
		"dataBase64": base64.StdEncoding.EncodeToString([]byte("hi")),
	})

	if errno := node.client.writeClass("host.fs.write.create", "/Users/alice/new.txt", map[string]any{"dataBase64": "", "mode": "0644"}); errno != 0 {
		t.Fatalf("create writeClass errno=%v", errno)
	}
	assertRequest(t, <-requests, "host.fs.write.create", "/Users/alice/new.txt", map[string]any{"dataBase64": "", "mode": "0644"})

	if errno := node.Unlink(context.Background(), "old.txt"); errno != 0 {
		t.Fatalf("Unlink errno=%v", errno)
	}
	assertRequest(t, <-requests, "host.fs.write.delete", "/Users/alice/file.txt/old.txt", nil)

	if errno := node.client.writeClass("host.fs.write.rename", "/Users/alice/a.txt", map[string]any{"destinationPath": "/Users/alice/b.txt"}); errno != 0 {
		t.Fatalf("rename writeClass errno=%v", errno)
	}
	assertRequest(t, <-requests, "host.fs.write.rename", "/Users/alice/a.txt", map[string]any{"destinationPath": "/Users/alice/b.txt"})

	setattr := &fuse.SetAttrIn{SetAttrInCommon: fuse.SetAttrInCommon{
		Valid: fuse.FATTR_SIZE | fuse.FATTR_MODE | fuse.FATTR_UID | fuse.FATTR_GID,
		Size:  4,
		Mode:  0o640,
		Owner: fuse.Owner{Uid: 501, Gid: 20},
	}}
	if errno := node.Setattr(context.Background(), nil, setattr, &fuse.AttrOut{}); errno != 0 {
		t.Fatalf("Setattr errno=%v", errno)
	}
	assertRequest(t, <-requests, "host.fs.write.truncate", "/Users/alice/file.txt", map[string]any{"size": float64(4)})
	assertRequest(t, <-requests, "host.fs.write.chmod", "/Users/alice/file.txt", map[string]any{"mode": "0640"})
	assertRequest(t, <-requests, "host.fs.write.chown", "/Users/alice/file.txt", map[string]any{"uid": float64(501), "gid": float64(20)})
}

func TestResponseErrnoUsesOnlyValidatedTypedRecord(t *testing.T) {
	tests := []struct {
		code string
		want syscall.Errno
	}{
		{broker.HostFSErrorPathHidden, syscall.ENOENT},
		{broker.HostFSErrorReadDenied, syscall.EACCES},
		{broker.HostFSErrorDirectoryIncomplete, syscall.EOVERFLOW},
		{broker.HostFSErrorHostPrerequisiteFailed, syscall.EIO},
		{broker.HostFSErrorOperationUnsupported, syscall.EROFS},
	}
	for _, tt := range tests {
		record, err := broker.NewErrorRecord(tt.code, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := responseErrno(broker.Response{Error: record, Stderr: "misleading hostfs path not found"}); got != tt.want {
			t.Fatalf("responseErrno(%s)=%v want %v", tt.code, got, tt.want)
		}
	}
	if got := responseErrno(broker.Response{Stderr: "hostfs path not found"}); got != syscall.EIO {
		t.Fatalf("legacy stderr became errno authority: %v", got)
	}
	bad := &broker.ErrorRecord{Code: broker.HostFSErrorPathHidden, Errno: broker.ErrnoEACCES}
	if got := responseErrno(broker.Response{Error: bad}); got != syscall.EIO {
		t.Fatalf("mismatched typed error did not fail to EIO: %v", got)
	}
}

func TestHostFSMountOptionsBoundPresentationCachesWithoutKernelAuthority(t *testing.T) {
	opts := hostFSMountOptions(false)
	if !opts.NullPermissions {
		t.Fatal("HostFS must retain NullPermissions")
	}
	if opts.EntryTimeout == nil || *opts.EntryTimeout != time.Second || opts.AttrTimeout == nil || *opts.AttrTimeout != time.Second {
		t.Fatalf("unexpected positive TTLs: entry=%v attr=%v", opts.EntryTimeout, opts.AttrTimeout)
	}
	if opts.NegativeTimeout == nil || *opts.NegativeTimeout != 0 {
		t.Fatalf("negative TTL=%v want zero", opts.NegativeTimeout)
	}
	if stableMode("symlink") != syscall.S_IFLNK {
		t.Fatal("discovered symlink lost its coarse kind")
	}
}

func TestFillAttrUsesGenericLockedAndOrdinaryGrantedMetadata(t *testing.T) {
	var coarse fuse.Attr
	fillAttr(&coarse, nodeInfo{Kind: "file"})
	if coarse.Size != 0 || coarse.Mode&0o7777 != 0o666 {
		t.Fatalf("coarse attr exposed non-generic metadata: size=%d mode=%#o", coarse.Size, coarse.Mode)
	}

	var ordinary fuse.Attr
	fillAttr(&ordinary, nodeInfo{Kind: "file", Size: 29, Mode: "-rw-r-----"})
	if ordinary.Size != 29 || ordinary.Mode&0o7777 != 0o640 {
		t.Fatalf("ordinary attr lost broker metadata: size=%d mode=%#o", ordinary.Size, ordinary.Mode)
	}
	for input, want := range map[string]uint32{
		"0750":       0o750,
		"-rwsr-x---": 0o4750,
		"drwxrwxr-t": 0o1775,
	} {
		got, ok := permissionBits(input)
		if !ok || got != want {
			t.Fatalf("permissionBits(%q)=(%#o,%t) want %#o", input, got, ok, want)
		}
	}
	if _, ok := permissionBits("not-a-mode"); ok {
		t.Fatal("invalid permission string was accepted")
	}
}

func fakeHostFSDBroker(t *testing.T) (*brokerClient, <-chan broker.Request, func()) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "broker.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan broker.Request, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				var req broker.Request
				if err := json.NewDecoder(conn).Decode(&req); err != nil {
					return
				}
				requests <- req
				data := map[string]any{"staged": true, "hostChanged": false}
				if req.Action == "host.fs.stat" {
					data = map[string]any{"kind": "file", "size": 4, "mode": "-rw-r-----"}
				}
				_ = json.NewEncoder(conn).Encode(broker.Response{
					ID:       req.ID,
					Decision: "allow",
					Status:   "ok",
					ExitCode: 0,
					Data:     data,
				})
			}(conn)
		}
	}()
	cleanup := func() {
		_ = ln.Close()
		<-done
		_ = os.Remove(socket)
	}
	return &brokerClient{endpoint: broker.UnixEndpoint(socket), session: "ses_1", token: "cap_good"}, requests, cleanup
}

func assertRequest(t *testing.T, req broker.Request, action, path string, args map[string]any) {
	t.Helper()
	if req.Subject != hostFSSubject || req.Route != "host-broker" || req.SessionID != "ses_1" || req.CapabilityToken != "cap_good" {
		t.Fatalf("bad broker envelope: %+v", req)
	}
	if req.Action != action {
		t.Fatalf("action=%q want %q", req.Action, action)
	}
	if req.Args["path"] != path {
		t.Fatalf("path=%v want %q in %+v", req.Args["path"], path, req.Args)
	}
	for key, want := range args {
		if got := req.Args[key]; got != want {
			t.Fatalf("arg %s=%v want %v in %+v", key, got, want, req.Args)
		}
	}
}

var _ fs.NodeWriter = (*hostFSNode)(nil)
var _ fs.NodeUnlinker = (*hostFSNode)(nil)
var _ fs.NodeSetattrer = (*hostFSNode)(nil)
var _ = syscall.EROFS
