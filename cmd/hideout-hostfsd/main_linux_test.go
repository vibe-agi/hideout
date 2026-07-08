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
