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
	opts := &fs.Options{
		MountOptions: fuse.MountOptions{
			FsName:        "hideout-hostfs",
			Name:          "hideout-hostfs",
			Debug:         debug,
			DisableXAttrs: true,
			DirectMount:   true,
			AllowOther:    true,
		},
		NullPermissions: true,
	}
	return fs.Mount(mountPoint, root, opts)
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
	switch resp.Stderr {
	case "hostfs path not found":
		return syscall.ENOENT
	case "hostfs operation unsupported":
		return syscall.EROFS
	default:
		if resp.Status == "broker-unavailable" {
			return syscall.EIO
		}
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

func (n *hostFSNode) Open(_ context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if flags&uint32(syscall.O_ACCMODE) != uint32(os.O_RDONLY) {
		return nil, 0, syscall.EROFS
	}
	if n.kind == "dir" {
		return nil, 0, syscall.EISDIR
	}
	return nil, fuse.FOPEN_DIRECT_IO, 0
}

func (n *hostFSNode) Read(_ context.Context, _ fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data, errno := n.client.read(n.path, off, len(dest))
	if errno != 0 {
		return nil, errno
	}
	return fuse.ReadResultData(data), 0
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
	if kind == "dir" {
		return syscall.S_IFDIR
	}
	return syscall.S_IFREG
}

func fillAttr(attr *fuse.Attr, info nodeInfo) {
	mode := uint32(0o444)
	if info.Kind == "dir" {
		mode = 0o555
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
