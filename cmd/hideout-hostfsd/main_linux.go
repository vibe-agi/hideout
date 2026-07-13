//go:build linux

package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/vibe-agi/hideout/internal/broker"
)

const hostFSSubject = "hostfs:daemon"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "hideout-hostfsd:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	var mountPoint string
	var debug bool
	fs := flag.NewFlagSet("hideout-hostfsd", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&mountPoint, "mount", "/hideout/hostfs", "HostFS FUSE mount point")
	fs.BoolVar(&debug, "debug", false, "enable go-fuse debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, err := newBrokerClientFromEnv()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(mountPoint, 0o755); err != nil {
		return err
	}
	root := &hostFSNode{
		client: client,
		path:   "/",
		kind:   "dir",
	}
	server, err := fsMount(mountPoint, root, debug)
	if err != nil {
		return err
	}
	server.Wait()
	return nil
}

func fsMount(mountPoint string, root *hostFSNode, debug bool) (*fuse.Server, error) {
	return fs.Mount(mountPoint, root, hostFSMountOptions(debug))
}

func hostFSMountOptions(debug bool) *fs.Options {
	entryTimeout := time.Second
	attrTimeout := time.Second
	negativeTimeout := time.Duration(0)
	return &fs.Options{
		MountOptions: fuse.MountOptions{
			FsName:        "hideout-hostfs",
			Name:          "hideout-hostfs",
			Debug:         debug,
			DisableXAttrs: true,
			DirectMount:   true,
			AllowOther:    true,
		},
		NullPermissions: true,
		EntryTimeout:    &entryTimeout,
		AttrTimeout:     &attrTimeout,
		NegativeTimeout: &negativeTimeout,
	}
}

type brokerClient struct {
	endpoint broker.Endpoint
	session  string
	token    string
}

type nodeInfo struct {
	Kind    string
	Size    uint64
	Mode    string
	ModTime time.Time
}

type dirEntry struct {
	Name string
	Kind string
	Size uint64
	Mode string
}

func newBrokerClientFromEnv() (*brokerClient, error) {
	rawEndpoint := os.Getenv(broker.EnvEndpoint)
	endpoint, err := broker.ParseEndpoint(rawEndpoint)
	if err != nil {
		return nil, err
	}
	session := os.Getenv(broker.EnvSession)
	token := os.Getenv(broker.EnvToken)
	if session == "" || token == "" {
		return nil, errors.New("broker session and token env are required")
	}
	return &brokerClient{endpoint: endpoint, session: session, token: token}, nil
}

func (c *brokerClient) stat(path string) (nodeInfo, syscall.Errno) {
	resp := c.call("host.fs.stat", map[string]any{"path": path})
	if resp.Status != "ok" {
		return nodeInfo{}, responseErrno(resp)
	}
	return parseNodeInfo(resp.Data)
}

func (c *brokerClient) read(path string, offset int64, size int) ([]byte, syscall.Errno) {
	resp := c.call("host.fs.read", map[string]any{
		"path":   path,
		"offset": offset,
		"size":   size,
	})
	if resp.Status != "ok" {
		return nil, responseErrno(resp)
	}
	raw, _ := resp.Data["dataBase64"].(string)
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, syscall.EIO
	}
	return data, 0
}

func (c *brokerClient) list(path string) ([]dirEntry, syscall.Errno) {
	resp := c.call("host.fs.list", map[string]any{"path": path})
	if resp.Status != "ok" {
		return nil, responseErrno(resp)
	}
	return parseEntries(resp.Data)
}

func (c *brokerClient) writeClass(action, path string, args map[string]any) syscall.Errno {
	if args == nil {
		args = map[string]any{}
	}
	args["path"] = path
	resp := c.call(action, args)
	if resp.Status != "ok" {
		fmt.Fprintf(os.Stderr, "hideout-hostfsd: %s path=%s status=%s stderr=%q\n", action, path, resp.Status, resp.Stderr)
		return responseErrno(resp)
	}
	return 0
}

func (c *brokerClient) call(action string, args map[string]any) broker.Response {
	id, err := broker.NewRequestID()
	if err != nil {
		return broker.Response{Decision: "deny", Status: "error", ExitCode: 1, Stderr: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return broker.ClientOpenEndpoint(ctx, c.endpoint, broker.Request{
		ID:              id,
		SessionID:       c.session,
		CapabilityToken: c.token,
		Subject:         hostFSSubject,
		Route:           "host-broker",
		Action:          action,
		Args:            args,
	})
}

func responseErrno(resp broker.Response) syscall.Errno {
	if err := broker.ValidateErrorRecord(resp.Error); err != nil {
		return syscall.EIO
	}
	switch resp.Error.Errno {
	case broker.ErrnoENOENT:
		return syscall.ENOENT
	case broker.ErrnoEACCES:
		return syscall.EACCES
	case broker.ErrnoEOVERFLOW:
		return syscall.EOVERFLOW
	case broker.ErrnoEROFS:
		return syscall.EROFS
	case broker.ErrnoEIO:
		return syscall.EIO
	default:
		return syscall.EIO
	}
}

type hostFSNode struct {
	fs.Inode
	client *brokerClient
	path   string
	kind   string
	size   uint64
}

type hostFSFileHandle struct {
	node *hostFSNode
}

func (h *hostFSFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	return h.node.Read(ctx, h, dest, off)
}

func (h *hostFSFileHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	return h.node.Write(ctx, h, data, off)
}

func (n *hostFSNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	child := childPath(n.path, name)
	if n.path == "/" && fixedRootKind(name) == "dir" {
		childNode := &hostFSNode{client: n.client, path: child, kind: "dir"}
		fillAttr(&out.Attr, nodeInfo{Kind: "dir"})
		return n.NewInode(ctx, childNode, fs.StableAttr{Mode: syscall.S_IFDIR}), 0
	}
	info, errno := n.client.stat(child)
	if errno != 0 {
		return nil, errno
	}
	childNode := &hostFSNode{client: n.client, path: child, kind: info.Kind, size: info.Size}
	fillAttr(&out.Attr, info)
	return n.NewInode(ctx, childNode, fs.StableAttr{Mode: stableMode(info.Kind)}), 0
}

func (n *hostFSNode) Getattr(_ context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	if n.path == "/" || fixedRootKind(strings.TrimPrefix(n.path, "/")) == "dir" {
		fillAttr(&out.Attr, nodeInfo{Kind: "dir"})
		return 0
	}
	info, errno := n.client.stat(n.path)
	if errno != 0 {
		return errno
	}
	fillAttr(&out.Attr, info)
	return 0
}

func (n *hostFSNode) Readdir(_ context.Context) (fs.DirStream, syscall.Errno) {
	if n.path == "/" {
		return fs.NewListDirStream([]fuse.DirEntry{
			{Name: "Users", Mode: syscall.S_IFDIR},
			{Name: "Volumes", Mode: syscall.S_IFDIR},
			{Name: "private", Mode: syscall.S_IFDIR},
		}), 0
	}
	entries, errno := n.client.list(n.path)
	if errno != 0 {
		return nil, errno
	}
	out := make([]fuse.DirEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, fuse.DirEntry{
			Name: entry.Name,
			Mode: stableMode(entry.Kind),
		})
	}
	return fs.NewListDirStream(out), 0
}

func (n *hostFSNode) Open(_ context.Context, _ uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if n.kind == "dir" {
		return nil, 0, syscall.EISDIR
	}
	return &hostFSFileHandle{node: n}, fuse.FOPEN_DIRECT_IO, 0
}

func (n *hostFSNode) Read(_ context.Context, _ fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data, errno := n.client.read(n.path, off, len(dest))
	if errno != 0 {
		return nil, errno
	}
	return fuse.ReadResultData(data), 0
}

func (n *hostFSNode) Write(_ context.Context, _ fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno) {
	errno := n.client.writeClass("host.fs.write.replace", n.path, map[string]any{
		"offset":     off,
		"dataBase64": base64.StdEncoding.EncodeToString(data),
	})
	if errno != 0 {
		return 0, errno
	}
	return uint32(len(data)), 0
}

func (n *hostFSNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	path := childPath(n.path, name)
	args := map[string]any{
		"dataBase64": "",
		"mode":       fmt.Sprintf("%04o", mode&0o7777),
	}
	action := "host.fs.write.create"
	if flags&syscall.O_EXCL == 0 && flags&syscall.O_TRUNC != 0 {
		action = "host.fs.write.truncate"
		args = map[string]any{"size": int64(0)}
	}
	errno := n.client.writeClass(action, path, args)
	if errno != 0 {
		return nil, nil, 0, errno
	}
	child := &hostFSNode{client: n.client, path: path, kind: "file"}
	fillAttr(&out.Attr, nodeInfo{Kind: "file", Mode: fmt.Sprintf("%04o", mode&0o7777)})
	return n.NewInode(ctx, child, fs.StableAttr{Mode: syscall.S_IFREG}), &hostFSFileHandle{node: child}, fuse.FOPEN_DIRECT_IO, 0
}

func (n *hostFSNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	path := childPath(n.path, name)
	errno := n.client.writeClass("host.fs.write.mkdir", path, map[string]any{"mode": fmt.Sprintf("%04o", mode&0o7777)})
	if errno != 0 {
		return nil, errno
	}
	child := &hostFSNode{client: n.client, path: path, kind: "dir"}
	fillAttr(&out.Attr, nodeInfo{Kind: "dir", Mode: fmt.Sprintf("%04o", mode&0o7777)})
	return n.NewInode(ctx, child, fs.StableAttr{Mode: syscall.S_IFDIR}), 0
}

func (n *hostFSNode) Unlink(_ context.Context, name string) syscall.Errno {
	return n.client.writeClass("host.fs.write.delete", childPath(n.path, name), nil)
}

func (n *hostFSNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	return n.Unlink(ctx, name)
}

func (n *hostFSNode) Rename(_ context.Context, name string, newParent fs.InodeEmbedder, newName string, _ uint32) syscall.Errno {
	parent, ok := newParent.EmbeddedInode().Operations().(*hostFSNode)
	if !ok || parent == nil {
		return syscall.EIO
	}
	return n.client.writeClass("host.fs.write.rename", childPath(n.path, name), map[string]any{"destinationPath": childPath(parent.path, newName)})
}

func (n *hostFSNode) Setattr(_ context.Context, _ fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if size, ok := in.GetSize(); ok {
		if errno := n.client.writeClass("host.fs.write.truncate", n.path, map[string]any{"size": int64(size)}); errno != 0 {
			return errno
		}
	}
	if mode, ok := in.GetMode(); ok {
		if errno := n.client.writeClass("host.fs.write.chmod", n.path, map[string]any{"mode": fmt.Sprintf("%04o", mode&0o7777)}); errno != 0 {
			return errno
		}
	}
	args := map[string]any{}
	if uid, ok := in.GetUID(); ok {
		args["uid"] = int64(uid)
	}
	if gid, ok := in.GetGID(); ok {
		args["gid"] = int64(gid)
	}
	if len(args) > 0 {
		if errno := n.client.writeClass("host.fs.write.chown", n.path, args); errno != 0 {
			return errno
		}
	}
	info, errno := n.client.stat(n.path)
	if errno != 0 {
		return errno
	}
	fillAttr(&out.Attr, info)
	return 0
}

func childPath(parent, name string) string {
	if parent == "/" {
		return "/" + name
	}
	return filepath.Join(parent, name)
}

func fixedRootKind(name string) string {
	switch name {
	case "Users", "Volumes", "private":
		return "dir"
	default:
		return ""
	}
}

func stableMode(kind string) uint32 {
	switch kind {
	case "dir":
		return syscall.S_IFDIR
	case "symlink":
		return syscall.S_IFLNK
	default:
		return syscall.S_IFREG
	}
}

func fillAttr(attr *fuse.Attr, info nodeInfo) {
	mode := uint32(0o666)
	switch info.Kind {
	case "dir":
		mode = 0o777
	case "symlink":
		mode = 0o777
	}
	if ordinary, ok := permissionBits(info.Mode); ok {
		mode = ordinary
	}
	attr.Mode = stableMode(info.Kind) | mode
	attr.Size = info.Size
	attr.Nlink = 1
	if info.Kind == "dir" {
		attr.Nlink = 2
	}
	if !info.ModTime.IsZero() {
		attr.SetTimes(nil, &info.ModTime, nil)
	}
}

func permissionBits(value string) (uint32, bool) {
	value = strings.TrimSpace(value)
	if len(value) == 4 {
		parsed, err := strconv.ParseUint(value, 8, 32)
		if err == nil {
			return uint32(parsed) & 0o7777, true
		}
	}
	if len(value) < 9 {
		return 0, false
	}
	perm := value[len(value)-9:]
	var mode uint32
	for i, ch := range perm {
		shift := uint(8 - i)
		switch ch {
		case 'r':
			if i%3 != 0 {
				return 0, false
			}
			mode |= 1 << shift
		case 'w':
			if i%3 != 1 {
				return 0, false
			}
			mode |= 1 << shift
		case 'x':
			if i%3 != 2 {
				return 0, false
			}
			mode |= 1 << shift
		case 's', 'S':
			if i != 2 && i != 5 {
				return 0, false
			}
			if ch == 's' {
				mode |= 1 << shift
			}
			if i == 2 {
				mode |= 0o4000
			} else {
				mode |= 0o2000
			}
		case 't', 'T':
			if i != 8 {
				return 0, false
			}
			if ch == 't' {
				mode |= 1
			}
			mode |= 0o1000
		case '-':
		default:
			return 0, false
		}
	}
	return mode, true
}

func parseNodeInfo(data map[string]any) (nodeInfo, syscall.Errno) {
	kind, _ := data["kind"].(string)
	if kind == "" {
		return nodeInfo{}, syscall.EIO
	}
	size, ok := numberToUint64(data["size"])
	if !ok {
		size = 0
	}
	info := nodeInfo{Kind: kind, Size: size}
	if mode, ok := data["mode"].(string); ok {
		info.Mode = mode
	}
	if raw, ok := data["modTime"].(string); ok {
		if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			info.ModTime = ts
		}
	}
	return info, 0
}

func parseEntries(data map[string]any) ([]dirEntry, syscall.Errno) {
	rawEntries, ok := data["entries"]
	if !ok {
		return nil, syscall.EIO
	}
	var values []any
	switch typed := rawEntries.(type) {
	case []any:
		values = typed
	case []map[string]any:
		values = make([]any, 0, len(typed))
		for _, entry := range typed {
			values = append(values, entry)
		}
	default:
		return nil, syscall.EIO
	}
	out := make([]dirEntry, 0, len(values))
	for _, value := range values {
		entryMap, ok := value.(map[string]any)
		if !ok {
			return nil, syscall.EIO
		}
		name, _ := entryMap["name"].(string)
		kind, _ := entryMap["kind"].(string)
		if name == "" || kind == "" {
			return nil, syscall.EIO
		}
		size, _ := numberToUint64(entryMap["size"])
		mode, _ := entryMap["mode"].(string)
		out = append(out, dirEntry{Name: name, Kind: kind, Size: size, Mode: mode})
	}
	return out, 0
}

func numberToUint64(value any) (uint64, bool) {
	switch typed := value.(type) {
	case int:
		if typed < 0 {
			return 0, false
		}
		return uint64(typed), true
	case int64:
		if typed < 0 {
			return 0, false
		}
		return uint64(typed), true
	case uint64:
		return typed, true
	case float64:
		if typed < 0 || math.Trunc(typed) != typed {
			return 0, false
		}
		return uint64(typed), true
	default:
		return 0, false
	}
}
