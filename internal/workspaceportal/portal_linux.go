//go:build linux

package workspaceportal

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func Run(args []string) error {
	flags := flag.NewFlagSet("hideout-workspace-portal", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	endpoint := flags.String("endpoint", "", "host portal endpoint")
	credentialFile := flags.String("credential-file", "", "session credential file")
	mountPoint := flags.String("mount", "", "guest mount point")
	readyFile := flags.String("ready-file", "", "optional mount-ready marker")
	debug := flags.Bool("debug", false, "enable FUSE debug logging")
	allowOther := flags.Bool("allow-other", false, "permit the non-owner target to access the control-owned mount")
	uid := flags.Uint("uid", uint(os.Getuid()), "target-visible numeric uid")
	gid := flags.Uint("gid", uint(os.Getgid()), "target-visible numeric gid")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *endpoint == "" || !filepath.IsAbs(*credentialFile) || !filepath.IsAbs(*mountPoint) {
		return errors.New("portal-mount requires --endpoint, absolute --credential-file and absolute --mount")
	}
	credential, err := workspaceattach.ReadPortalCredential(*credentialFile)
	if err != nil {
		return err
	}
	client, err := workspaceattach.DialPortal(context.Background(), *endpoint, credential, workspaceattach.DefaultPortalLimits())
	if err != nil {
		return err
	}
	defer client.Close()
	if err := os.MkdirAll(*mountPoint, 0o700); err != nil {
		return err
	}
	if *uid > uint(^uint32(0)) || *gid > uint(^uint32(0)) {
		return errors.New("portal-mount uid and gid must fit uint32")
	}
	return runPortalFuse(client, *mountPoint, *readyFile, *debug, *allowOther, uint32(*uid), uint32(*gid))
}

type portalFuseTree struct {
	client *workspaceattach.PortalClient
	debug  bool
	mu     sync.RWMutex
	nodes  map[string]*portalFuseNode
	info   map[string]workspaceattach.PortalFileInfo
	dirs   map[string]uint64
	server *fuse.Server
	uid    uint32
	gid    uint32
}

type portalFuseNode struct {
	fs.Inode
	tree      *portalFuseTree
	path      string
	stableIno uint64
}

type portalFuseHandle struct {
	client *workspaceattach.PortalClient
	id     uint64
	path   string
	once   sync.Once
}

type portalFuseDirHandle struct {
	node       *portalFuseNode
	mu         sync.Mutex
	loaded     bool
	generation uint64
	entries    []workspaceattach.PortalDirEntry
	byName     map[string]workspaceattach.PortalFileInfo
	index      int
	errno      syscall.Errno
}

func runPortalFuse(client *workspaceattach.PortalClient, mountPoint, readyFile string, debug, allowOther bool, uid, gid uint32) error {
	tree := &portalFuseTree{
		client: client, debug: debug, nodes: make(map[string]*portalFuseNode),
		info: make(map[string]workspaceattach.PortalFileInfo), dirs: make(map[string]uint64),
		uid: uid, gid: gid,
	}
	root := &portalFuseNode{tree: tree, path: ".", stableIno: portalFuseStableInode(".", 0)}
	tree.nodes["."] = root
	cachePolicy := workspaceattach.SelectedCachePolicy()
	if err := cachePolicy.Validate(); err != nil {
		return err
	}
	entryTimeout := cachePolicy.EntryTTL
	attrTimeout := cachePolicy.AttributeTTL
	negativeTimeout := cachePolicy.NegativeTTL
	server, err := fs.Mount(mountPoint, root, &fs.Options{
		MountOptions: fuse.MountOptions{
			FsName: "hideout-workspace-portal", Name: "hideout-workspace-portal",
			Debug: debug, DisableXAttrs: true, DirectMount: true, AllowOther: allowOther,
			RememberInodes: true, EnableLocks: true,
		},
		NullPermissions: true, UID: uid, GID: gid,
		EntryTimeout: &entryTimeout, AttrTimeout: &attrTimeout, NegativeTimeout: &negativeTimeout,
	})
	if err != nil {
		return err
	}
	tree.server = server
	defer server.Unmount()
	if readyFile != "" {
		if err := os.WriteFile(readyFile, []byte("ready\n"), 0o600); err != nil {
			return err
		}
	}
	done := make(chan struct{})
	go func() {
		server.Wait()
		close(done)
	}()
	for {
		select {
		case event := <-client.Events():
			if tree.debug {
				fmt.Fprintf(os.Stderr, "workspace portal invalidate path=%q op=%d\n", event.Path, event.Op)
			}
			tree.invalidate(event.Path, event.Op)
		case terminal := <-client.Terminal():
			_ = server.Unmount()
			<-done
			return terminal
		case <-done:
			return nil
		}
	}
}

func (tree *portalFuseTree) register(node *portalFuseNode) {
	tree.mu.Lock()
	tree.nodes[node.path] = node
	tree.mu.Unlock()
}

func (tree *portalFuseTree) registerReturned(inode *fs.Inode, candidate *portalFuseNode) {
	actual := candidate
	if inode != nil {
		if resolved, ok := inode.Operations().(*portalFuseNode); ok && resolved != nil {
			actual = resolved
		}
	}
	tree.register(actual)
}

func (tree *portalFuseTree) cacheInfo(path string, info workspaceattach.PortalFileInfo) {
	tree.mu.Lock()
	tree.info[path] = info
	tree.mu.Unlock()
}

func (tree *portalFuseTree) cachedInfo(path string) (workspaceattach.PortalFileInfo, bool) {
	tree.mu.RLock()
	defer tree.mu.RUnlock()
	info, ok := tree.info[path]
	return info, ok
}

func (tree *portalFuseTree) node(path string) *portalFuseNode {
	tree.mu.RLock()
	defer tree.mu.RUnlock()
	return tree.nodes[path]
}

func (tree *portalFuseTree) directoryGeneration(path string) uint64 {
	tree.mu.RLock()
	defer tree.mu.RUnlock()
	return tree.dirs[path]
}

func (tree *portalFuseTree) stableInode(path string, hostInode uint64) uint64 {
	tree.mu.RLock()
	node := tree.nodes[path]
	tree.mu.RUnlock()
	if node != nil && node.stableIno != 0 {
		return node.stableIno
	}
	return portalFuseStableInode(path, hostInode)
}

func (tree *portalFuseTree) bumpDirectoryGeneration(path string) {
	tree.mu.Lock()
	tree.dirs[path]++
	tree.mu.Unlock()
}

func (tree *portalFuseTree) renamePath(oldPath, newPath string) {
	tree.mu.Lock()
	defer tree.mu.Unlock()
	type move struct {
		oldPath string
		newPath string
		node    *portalFuseNode
	}
	var moves []move
	for path, node := range tree.nodes {
		if path != oldPath && !strings.HasPrefix(path, oldPath+"/") {
			continue
		}
		suffix := strings.TrimPrefix(path, oldPath)
		moves = append(moves, move{oldPath: path, newPath: newPath + suffix, node: node})
	}
	for _, item := range moves {
		delete(tree.nodes, item.oldPath)
		delete(tree.info, item.oldPath)
	}
	for _, item := range moves {
		item.node.path = item.newPath
		tree.nodes[item.newPath] = item.node
	}
}

func (tree *portalFuseTree) invalidate(path string, rawOp uint32) {
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || strings.HasPrefix(path, "../") || filepath.IsAbs(path) {
		return
	}
	parentPath := filepath.ToSlash(filepath.Dir(path))
	name := filepath.Base(path)
	tree.mu.RLock()
	parent := tree.nodes[parentPath]
	node := tree.nodes[path]
	tree.mu.RUnlock()
	tree.mu.Lock()
	delete(tree.info, path)
	if fsnotify.Op(rawOp)&(fsnotify.Remove|fsnotify.Rename) != 0 {
		for candidate := range tree.nodes {
			if candidate == path || strings.HasPrefix(candidate, path+"/") {
				delete(tree.nodes, candidate)
				delete(tree.info, candidate)
			}
		}
	}
	tree.dirs[parentPath]++
	tree.mu.Unlock()
	if parent != nil {
		_ = parent.NotifyEntry(name)
		_ = parent.NotifyContent(0, 0)
	}
	if node != nil {
		_ = node.NotifyContent(0, 0)
	}
}

func (node *portalFuseNode) child(name string) string {
	if node.path == "." {
		return name
	}
	return filepath.ToSlash(filepath.Join(node.path, name))
}

func (node *portalFuseNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	path := node.child(name)
	info, ok := node.tree.cachedInfo(path)
	if !ok {
		var err error
		info, err = node.tree.client.Stat(ctx, path)
		if err != nil {
			return nil, portalFuseErrno(err)
		}
		node.tree.cacheInfo(path, info)
	}
	stableIno := node.tree.stableInode(path, info.Inode)
	if existing := node.tree.node(path); existing != nil {
		fillPortalFuseAttr(&out.Attr, info, existing.stableIno, node.tree.uid, node.tree.gid)
		return existing.EmbeddedInode(), 0
	}
	child := &portalFuseNode{tree: node.tree, path: path, stableIno: stableIno}
	fillPortalFuseAttr(&out.Attr, info, stableIno, node.tree.uid, node.tree.gid)
	inode := node.NewInode(ctx, child, fs.StableAttr{Mode: portalFuseType(info.Mode), Ino: stableIno})
	node.tree.registerReturned(inode, child)
	return inode, 0
}

func (node *portalFuseNode) Getattr(ctx context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	info, ok := node.tree.cachedInfo(node.path)
	if !ok {
		var err error
		info, err = node.tree.client.Stat(ctx, node.path)
		if err != nil {
			return portalFuseErrno(err)
		}
		node.tree.cacheInfo(node.path, info)
	}
	fillPortalFuseAttr(&out.Attr, info, node.stableIno, node.tree.uid, node.tree.gid)
	return 0
}

func (node *portalFuseNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries, err := node.tree.client.ReadDir(ctx, node.path)
	if err != nil {
		return nil, portalFuseErrno(err)
	}
	values := make([]fuse.DirEntry, 0, len(entries))
	for _, entry := range entries {
		path := node.child(entry.Name)
		node.tree.cacheInfo(path, entry.Info)
		values = append(values, fuse.DirEntry{
			Name: entry.Name, Mode: portalFuseType(entry.Info.Mode),
			Ino: node.tree.stableInode(path, entry.Info.Inode),
		})
	}
	return fs.NewListDirStream(values), 0
}

func (node *portalFuseNode) OpendirHandle(context.Context, uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return &portalFuseDirHandle{node: node}, fuse.FOPEN_CACHE_DIR, 0
}

func (handle *portalFuseDirHandle) load(ctx context.Context) syscall.Errno {
	generation := handle.node.tree.directoryGeneration(handle.node.path)
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.loaded && handle.generation == generation {
		return handle.errno
	}
	entries, err := handle.node.tree.client.ReadDir(ctx, handle.node.path)
	handle.loaded = true
	handle.generation = generation
	handle.index = 0
	handle.entries = nil
	handle.byName = nil
	handle.errno = portalFuseErrno(err)
	if err != nil {
		return handle.errno
	}
	handle.entries = entries
	handle.byName = make(map[string]workspaceattach.PortalFileInfo, len(entries))
	for _, entry := range entries {
		path := handle.node.child(entry.Name)
		handle.node.tree.cacheInfo(path, entry.Info)
		handle.byName[entry.Name] = entry.Info
	}
	return handle.errno
}

func (handle *portalFuseDirHandle) Readdirent(ctx context.Context) (*fuse.DirEntry, syscall.Errno) {
	if errno := handle.load(ctx); errno != 0 {
		return nil, errno
	}
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.index >= len(handle.entries) {
		return nil, 0
	}
	entry := handle.entries[handle.index]
	handle.index++
	path := handle.node.child(entry.Name)
	return &fuse.DirEntry{
		Name: entry.Name, Mode: portalFuseType(entry.Info.Mode),
		Ino: handle.node.tree.stableInode(path, entry.Info.Inode),
		Off: uint64(handle.index),
	}, 0
}

func (handle *portalFuseDirHandle) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if errno := handle.load(ctx); errno != 0 {
		return nil, errno
	}
	handle.mu.Lock()
	info, ok := handle.byName[name]
	handle.mu.Unlock()
	if !ok {
		return nil, syscall.ENOENT
	}
	handle.node.tree.cacheInfo(handle.node.child(name), info)
	return handle.node.Lookup(ctx, name, out)
}

func (handle *portalFuseDirHandle) Fsyncdir(ctx context.Context, _ uint32) syscall.Errno {
	return portalFuseErrno(handle.node.tree.client.FsyncPath(ctx, handle.node.path))
}

func (handle *portalFuseDirHandle) Seekdir(ctx context.Context, offset uint64) syscall.Errno {
	if errno := handle.load(ctx); errno != 0 {
		return errno
	}
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if offset > uint64(len(handle.entries)) {
		return syscall.EINVAL
	}
	handle.index = int(offset)
	return 0
}

func (node *portalFuseNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	handle, err := node.tree.client.Open(ctx, node.path, int(flags), 0)
	if err != nil {
		if node.tree.debug {
			fmt.Fprintf(os.Stderr, "workspace portal open path=%q flags=%#x: %v\n", node.path, flags, err)
		}
		return nil, 0, portalFuseErrno(err)
	}
	return &portalFuseHandle{client: node.tree.client, id: handle, path: node.path}, fuse.FOPEN_KEEP_CACHE, 0
}

func (node *portalFuseNode) Fsync(ctx context.Context, handle fs.FileHandle, flags uint32) syscall.Errno {
	if file, ok := handle.(*portalFuseHandle); ok {
		return file.Fsync(ctx, flags)
	}
	return portalFuseErrno(node.tree.client.FsyncPath(ctx, node.path))
}

func (node *portalFuseNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	path := node.child(name)
	handle, err := node.tree.client.Open(ctx, path, int(flags)|os.O_CREATE, os.FileMode(mode))
	if err != nil {
		return nil, nil, 0, portalFuseErrno(err)
	}
	info, err := node.tree.client.Stat(ctx, path)
	if err != nil {
		_ = node.tree.client.CloseHandle(context.Background(), handle)
		return nil, nil, 0, portalFuseErrno(err)
	}
	stableIno := node.tree.stableInode(path, info.Inode)
	child := &portalFuseNode{tree: node.tree, path: path, stableIno: stableIno}
	fillPortalFuseAttr(&out.Attr, info, stableIno, node.tree.uid, node.tree.gid)
	inode := node.NewInode(ctx, child, fs.StableAttr{Mode: portalFuseType(info.Mode), Ino: stableIno})
	node.tree.registerReturned(inode, child)
	return inode, &portalFuseHandle{client: node.tree.client, id: handle, path: path}, fuse.FOPEN_KEEP_CACHE, 0
}

func (node *portalFuseNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	path := node.child(name)
	if err := node.tree.client.Mkdir(ctx, path, os.FileMode(mode)); err != nil {
		return nil, portalFuseErrno(err)
	}
	info, err := node.tree.client.Stat(ctx, path)
	if err != nil {
		return nil, portalFuseErrno(err)
	}
	stableIno := node.tree.stableInode(path, info.Inode)
	child := &portalFuseNode{tree: node.tree, path: path, stableIno: stableIno}
	fillPortalFuseAttr(&out.Attr, info, stableIno, node.tree.uid, node.tree.gid)
	inode := node.NewInode(ctx, child, fs.StableAttr{Mode: syscall.S_IFDIR, Ino: stableIno})
	node.tree.registerReturned(inode, child)
	return inode, 0
}

func (node *portalFuseNode) Mknod(context.Context, string, uint32, uint32, *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// Portal ownership is synthetic. Device, FIFO, socket, and other mknod
	// operations are not projected to the host filesystem.
	return nil, syscall.ENOTSUP
}

func (node *portalFuseNode) Unlink(ctx context.Context, name string) syscall.Errno {
	return portalFuseErrno(node.tree.client.Remove(ctx, node.child(name), false))
}

func (node *portalFuseNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	return portalFuseErrno(node.tree.client.Remove(ctx, node.child(name), true))
}

func (node *portalFuseNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	if flags != 0 {
		return syscall.ENOTSUP
	}
	parent, ok := newParent.EmbeddedInode().Operations().(*portalFuseNode)
	if !ok || parent == nil {
		return syscall.EIO
	}
	oldPath := node.child(name)
	newPath := parent.child(newName)
	if err := node.tree.client.Rename(ctx, oldPath, newPath); err != nil {
		return portalFuseErrno(err)
	}
	node.tree.renamePath(oldPath, newPath)
	return 0
}

func (node *portalFuseNode) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	path := node.child(name)
	if err := node.tree.client.Symlink(ctx, target, path); err != nil {
		return nil, portalFuseErrno(err)
	}
	info, err := node.tree.client.Stat(ctx, path)
	if err != nil {
		return nil, portalFuseErrno(err)
	}
	stableIno := node.tree.stableInode(path, info.Inode)
	child := &portalFuseNode{tree: node.tree, path: path, stableIno: stableIno}
	fillPortalFuseAttr(&out.Attr, info, stableIno, node.tree.uid, node.tree.gid)
	inode := node.NewInode(ctx, child, fs.StableAttr{Mode: syscall.S_IFLNK, Ino: stableIno})
	node.tree.registerReturned(inode, child)
	return inode, 0
}

func (node *portalFuseNode) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	target, err := node.tree.client.Readlink(ctx, node.path)
	return []byte(target), portalFuseErrno(err)
}

func (node *portalFuseNode) Setattr(ctx context.Context, _ fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if _, ok := in.GetUID(); ok {
		return syscall.EPERM
	}
	if _, ok := in.GetGID(); ok {
		return syscall.EPERM
	}
	if size, ok := in.GetSize(); ok {
		if err := node.tree.client.Truncate(ctx, node.path, int64(size)); err != nil {
			return portalFuseErrno(err)
		}
	}
	if mode, ok := in.GetMode(); ok {
		if err := node.tree.client.Chmod(ctx, node.path, os.FileMode(mode)); err != nil {
			return portalFuseErrno(err)
		}
	}
	if atime, setA := in.GetATime(); setA {
		mtime, setM := in.GetMTime()
		if !setM {
			info, err := node.tree.client.Stat(ctx, node.path)
			if err != nil {
				return portalFuseErrno(err)
			}
			mtime = info.ModTime
		}
		if err := node.tree.client.Chtimes(ctx, node.path, atime, mtime); err != nil {
			return portalFuseErrno(err)
		}
	} else if mtime, setM := in.GetMTime(); setM {
		info, err := node.tree.client.Stat(ctx, node.path)
		if err != nil {
			return portalFuseErrno(err)
		}
		if err := node.tree.client.Chtimes(ctx, node.path, info.ModTime, mtime); err != nil {
			return portalFuseErrno(err)
		}
	}
	return node.Getattr(ctx, nil, out)
}

func (handle *portalFuseHandle) Read(ctx context.Context, destination []byte, offset int64) (fuse.ReadResult, syscall.Errno) {
	data, err := handle.client.Read(ctx, handle.id, offset, len(destination))
	if err != nil {
		return nil, portalFuseErrno(err)
	}
	return fuse.ReadResultData(data), 0
}

func (handle *portalFuseHandle) Write(ctx context.Context, data []byte, offset int64) (uint32, syscall.Errno) {
	written, err := handle.client.Write(ctx, handle.id, offset, data)
	return uint32(written), portalFuseErrno(err)
}

func (handle *portalFuseHandle) Fsync(ctx context.Context, _ uint32) syscall.Errno {
	return portalFuseErrno(handle.client.Fsync(ctx, handle.id))
}

func (handle *portalFuseHandle) Flush(ctx context.Context) syscall.Errno {
	return portalFuseErrno(handle.client.Fsync(ctx, handle.id))
}

func (handle *portalFuseHandle) Release(ctx context.Context) syscall.Errno {
	var errno syscall.Errno
	handle.once.Do(func() { errno = portalFuseErrno(handle.client.CloseHandle(ctx, handle.id)) })
	return errno
}

func (handle *portalFuseHandle) Setlk(ctx context.Context, _ uint64, lock *fuse.FileLock, _ uint32) syscall.Errno {
	if lock.Typ == syscall.F_UNLCK {
		return portalFuseErrno(handle.client.Unlock(ctx, handle.id))
	}
	if lock.Typ != syscall.F_WRLCK {
		return syscall.ENOTSUP
	}
	return portalFuseErrno(handle.client.Lock(ctx, handle.id, true))
}

func (handle *portalFuseHandle) Setlkw(ctx context.Context, owner uint64, lock *fuse.FileLock, flags uint32) syscall.Errno {
	for {
		errno := handle.Setlk(ctx, owner, lock, flags)
		if errno != syscall.EWOULDBLOCK && errno != syscall.EAGAIN {
			return errno
		}
		select {
		case <-ctx.Done():
			return syscall.EINTR
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (handle *portalFuseHandle) Getlk(ctx context.Context, _ uint64, lock *fuse.FileLock, _ uint32, out *fuse.FileLock) syscall.Errno {
	available, err := handle.client.LockAvailable(ctx, handle.id)
	if err != nil {
		return portalFuseErrno(err)
	}
	*out = *lock
	if available {
		out.Typ = syscall.F_UNLCK
	}
	return 0
}

func fillPortalFuseAttr(attr *fuse.Attr, info workspaceattach.PortalFileInfo, stableIno uint64, uid, gid uint32) {
	attr.Mode = portalFuseType(info.Mode) | uint32(info.Mode.Perm())
	attr.Size = uint64(info.Size)
	attr.Ino = stableIno
	attr.Nlink = uint32(info.Nlink)
	attr.Owner.Uid = uid
	attr.Owner.Gid = gid
	attr.SetTimes(nil, &info.ModTime, nil)
}

func portalFuseStableInode(path string, hostInode uint64) uint64 {
	digest := fnv.New64a()
	_, _ = fmt.Fprintf(digest, "%s\x00%d", path, hostInode)
	value := digest.Sum64()
	if value == 0 {
		return 1
	}
	return value
}

func portalFuseType(mode os.FileMode) uint32 {
	switch {
	case mode.IsDir():
		return syscall.S_IFDIR
	case mode&os.ModeSymlink != 0:
		return syscall.S_IFLNK
	case mode&os.ModeNamedPipe != 0:
		return syscall.S_IFIFO
	case mode&os.ModeSocket != 0:
		return syscall.S_IFSOCK
	case mode&os.ModeDevice != 0:
		return syscall.S_IFBLK
	default:
		return syscall.S_IFREG
	}
}

func portalFuseErrno(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return syscall.EINTR
	case errors.Is(err, workspaceattach.ErrPortalOverloaded):
		return syscall.EBUSY
	case errors.Is(err, workspaceattach.ErrPortalRootReplaced):
		return syscall.ESTALE
	case errors.Is(err, workspaceattach.ErrPortalCredentialExpired), errors.Is(err, workspaceattach.ErrPortalCredentialRevoked):
		return syscall.EACCES
	case errors.Is(err, workspaceattach.ErrPortalHandleNotFound):
		return syscall.EBADF
	default:
		return syscall.EIO
	}
}

var _ = fmt.Sprintf
