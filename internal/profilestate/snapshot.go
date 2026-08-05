package profilestate

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

type sourceFact struct {
	mode       os.FileMode
	size       int64
	modifiedNS int64
	identity   fileIdentity
}

func sourceFactFromInfo(info os.FileInfo) (sourceFact, error) {
	identity, ok := identityFromInfo(info)
	if !ok {
		return sourceFact{}, ErrSourceChanged
	}
	return sourceFact{
		mode: info.Mode(), size: info.Size(), modifiedNS: info.ModTime().UnixNano(),
		identity: identity,
	}, nil
}

func (fact sourceFact) matches(info os.FileInfo) bool {
	observed, err := sourceFactFromInfo(info)
	return err == nil && observed == fact
}

type snapshotEntry struct {
	archive archiveEntry
	fact    sourceFact
	digest  [sha256.Size]byte
}

// Snapshot is an immutable in-memory inventory. It contains paths and digests,
// never file contents. Write reopens every file beneath the captured root and
// refuses any identity, metadata, content, or directory-listing drift.
type Snapshot struct {
	root         string
	entries      []snapshotEntry
	logicalBytes uint64
	digest       string
}

func (snapshot Snapshot) LogicalBytes() uint64 { return snapshot.logicalBytes }
func (snapshot Snapshot) Digest() string       { return snapshot.digest }
func (snapshot Snapshot) EntryCount() uint64   { return uint64(len(snapshot.entries)) }

func Capture(rootPath string) (Snapshot, error) {
	clean := filepath.Clean(rootPath)
	if !filepath.IsAbs(clean) {
		return Snapshot{}, ErrUnsafePath
	}
	rootInfo, err := os.Lstat(clean)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 ||
		rootInfo.Mode().Perm()&0o077 != 0 {
		return Snapshot{}, errors.Join(ErrUnsafePath, err)
	}
	root, err := os.OpenRoot(clean)
	if err != nil {
		return Snapshot{}, err
	}
	defer root.Close()

	entries := make([]snapshotEntry, 0, 64)
	seenFiles := make(map[[2]uint64]string)
	for _, name := range includedRoots {
		if err := capturePath(root, name, &entries, seenFiles); err != nil {
			return Snapshot{}, err
		}
	}
	if len(entries) == 0 || len(entries) > maxArchiveEntries {
		return Snapshot{}, ErrInvalidArchive
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].archive.Path < entries[right].archive.Path
	})
	for index := 1; index < len(entries); index++ {
		if entries[index-1].archive.Path == entries[index].archive.Path {
			return Snapshot{}, ErrAliasedFile
		}
	}

	hasher := sha256.New()
	header, err := encodeArchiveHeader(uint64(len(entries)))
	if err != nil {
		return Snapshot{}, err
	}
	_, _ = hasher.Write(header)
	logical := uint64(len(header))
	for index := range entries {
		metadata, err := encodeArchiveEntry(entries[index].archive)
		if err != nil {
			return Snapshot{}, err
		}
		logical, err = addLogical(logical, uint64(len(metadata)), entries[index].archive.Size)
		if err != nil {
			return Snapshot{}, err
		}
		_, _ = hasher.Write(metadata)
		if entries[index].archive.Kind != archiveEntryKindFile {
			continue
		}
		fileDigest, err := hashCapturedFile(root, entries[index], hasher)
		if err != nil {
			return Snapshot{}, err
		}
		entries[index].digest = fileDigest
	}
	if err := verifySnapshotFacts(root, entries); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		root: clean, entries: entries, logicalBytes: logical,
		digest: sha256Digest(hasher.Sum(nil)),
	}, nil
}

func capturePath(
	root *os.Root,
	relative string,
	entries *[]snapshotEntry,
	seenFiles map[[2]uint64]string,
) error {
	if !validArchivePath(relative) {
		return ErrUnsafePath
	}
	info, err := root.Lstat(relative)
	if err != nil {
		return errors.Join(ErrSourceChanged, err)
	}
	fact, err := sourceFactFromInfo(info)
	if err != nil {
		return err
	}
	entry := archiveEntry{Path: relative}
	switch {
	case info.IsDir():
		if info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return ErrSpecialFile
		}
		entry.Kind = archiveEntryKindDir
		entry.Mode = uint32(info.Mode().Perm())
		if entry.Mode == 0 {
			return ErrSpecialFile
		}
		*entries = append(*entries, snapshotEntry{archive: entry, fact: fact})
		directory, err := root.Open(relative)
		if err != nil {
			return errors.Join(ErrSourceChanged, err)
		}
		children, readErr := directory.ReadDir(-1)
		closeErr := directory.Close()
		if readErr != nil || closeErr != nil {
			return errors.Join(ErrSourceChanged, readErr, closeErr)
		}
		slices.SortFunc(children, func(left, right os.DirEntry) int {
			if left.Name() < right.Name() {
				return -1
			}
			if left.Name() > right.Name() {
				return 1
			}
			return 0
		})
		for _, child := range children {
			childPath := relative + "/" + child.Name()
			if isGeneratedPath(childPath) {
				continue
			}
			if err := capturePath(root, childPath, entries, seenFiles); err != nil {
				return err
			}
		}
		return nil
	case info.Mode().IsRegular():
		if info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Size() < 0 ||
			uint64(info.Size()) > maxArchiveLogicalBytes {
			return ErrSpecialFile
		}
		if fact.identity.links != 1 {
			return ErrAliasedFile
		}
		key := [2]uint64{fact.identity.device, fact.identity.inode}
		if previous, exists := seenFiles[key]; exists && previous != relative {
			return ErrAliasedFile
		}
		seenFiles[key] = relative
		entry.Kind = archiveEntryKindFile
		entry.Mode = uint32(info.Mode().Perm())
		entry.Size = uint64(info.Size())
		*entries = append(*entries, snapshotEntry{archive: entry, fact: fact})
		return nil
	case info.Mode()&os.ModeSymlink != 0:
		target, err := root.Readlink(relative)
		if err != nil {
			return errors.Join(ErrSourceChanged, err)
		}
		entry.Kind = archiveEntryKindSymlink
		entry.Target = target
		if err := entry.validate(); err != nil {
			return err
		}
		*entries = append(*entries, snapshotEntry{archive: entry, fact: fact})
		return nil
	default:
		return ErrSpecialFile
	}
}

func hashCapturedFile(root *os.Root, entry snapshotEntry, archive io.Writer) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	file, err := root.Open(entry.archive.Path)
	if err != nil {
		return zero, errors.Join(ErrSourceChanged, err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !entry.fact.matches(before) || !before.Mode().IsRegular() {
		return zero, errors.Join(ErrSourceChanged, err)
	}
	fileHasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(archive, fileHasher), io.LimitReader(file, int64(entry.archive.Size)+1))
	if err != nil || written != int64(entry.archive.Size) {
		return zero, errors.Join(ErrSourceChanged, err)
	}
	after, err := file.Stat()
	if err != nil || !entry.fact.matches(after) {
		return zero, errors.Join(ErrSourceChanged, err)
	}
	var digest [sha256.Size]byte
	copy(digest[:], fileHasher.Sum(nil))
	return digest, nil
}

func verifySnapshotFacts(root *os.Root, entries []snapshotEntry) error {
	for _, entry := range entries {
		info, err := root.Lstat(entry.archive.Path)
		if err != nil || !entry.fact.matches(info) {
			return errors.Join(ErrSourceChanged, err)
		}
		if entry.archive.Kind == archiveEntryKindSymlink {
			target, readErr := root.Readlink(entry.archive.Path)
			if readErr != nil || target != entry.archive.Target {
				return errors.Join(ErrSourceChanged, readErr)
			}
		}
	}
	return nil
}

func (snapshot Snapshot) Write(
	ctx context.Context,
	maxChunk int,
	emit func([]byte) error,
) error {
	if ctx == nil || emit == nil || maxChunk <= 0 || maxChunk > 4<<20 ||
		snapshot.root == "" || snapshot.logicalBytes == 0 ||
		!digestPattern.MatchString(snapshot.digest) || len(snapshot.entries) == 0 {
		return ErrInvalidArchive
	}
	root, err := os.OpenRoot(snapshot.root)
	if err != nil {
		return errors.Join(ErrSourceChanged, err)
	}
	defer root.Close()
	stream := newChunkStream(maxChunk, emit)
	header, err := encodeArchiveHeader(uint64(len(snapshot.entries)))
	if err != nil {
		return err
	}
	if err := stream.Write(ctx, header); err != nil {
		return err
	}
	for _, entry := range snapshot.entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := encodeArchiveEntry(entry.archive)
		if err != nil {
			return err
		}
		if err := stream.Write(ctx, metadata); err != nil {
			return err
		}
		if entry.archive.Kind != archiveEntryKindFile {
			continue
		}
		if err := stream.WriteFile(ctx, root, entry); err != nil {
			return err
		}
	}
	if err := verifySnapshotFacts(root, snapshot.entries); err != nil {
		return err
	}
	if err := stream.Close(ctx); err != nil {
		return err
	}
	if stream.bytes != snapshot.logicalBytes || sha256Digest(stream.hasher.Sum(nil)) != snapshot.digest {
		return ErrSourceChanged
	}
	return nil
}

type chunkStream struct {
	buffer []byte
	limit  int
	emit   func([]byte) error
	hasher hash.Hash
	bytes  uint64
}

func newChunkStream(limit int, emit func([]byte) error) *chunkStream {
	hasher := sha256.New()
	return &chunkStream{
		buffer: make([]byte, 0, limit), limit: limit, emit: emit,
		hasher: hasher,
	}
}

func (stream *chunkStream) Write(ctx context.Context, data []byte) error {
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		available := stream.limit - len(stream.buffer)
		if available > len(data) {
			available = len(data)
		}
		stream.buffer = append(stream.buffer, data[:available]...)
		_, _ = stream.hasher.Write(data[:available])
		stream.bytes += uint64(available)
		data = data[available:]
		if len(stream.buffer) == stream.limit {
			if err := stream.flush(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (stream *chunkStream) WriteFile(ctx context.Context, root *os.Root, entry snapshotEntry) error {
	file, err := root.Open(entry.archive.Path)
	if err != nil {
		return errors.Join(ErrSourceChanged, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !entry.fact.matches(info) || !info.Mode().IsRegular() {
		return errors.Join(ErrSourceChanged, err)
	}
	remaining := entry.archive.Size
	fileHasher := sha256.New()
	buffer := make([]byte, min(stream.limit, 1<<20))
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		want := uint64(len(buffer))
		if remaining < want {
			want = remaining
		}
		n, readErr := io.ReadFull(file, buffer[:int(want)])
		if readErr != nil || n != int(want) {
			return errors.Join(ErrSourceChanged, readErr)
		}
		_, _ = fileHasher.Write(buffer[:n])
		if err := stream.Write(ctx, buffer[:n]); err != nil {
			return err
		}
		remaining -= uint64(n)
	}
	var extra [1]byte
	if n, readErr := file.Read(extra[:]); n != 0 || (readErr != nil && !errors.Is(readErr, io.EOF)) {
		return errors.Join(ErrSourceChanged, readErr)
	}
	after, err := file.Stat()
	if err != nil || !entry.fact.matches(after) ||
		!slices.Equal(fileHasher.Sum(nil), entry.digest[:]) {
		return errors.Join(ErrSourceChanged, err)
	}
	return nil
}

func (stream *chunkStream) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return stream.flush()
}

func (stream *chunkStream) flush() error {
	if len(stream.buffer) == 0 {
		return nil
	}
	if err := stream.emit(stream.buffer); err != nil {
		return err
	}
	stream.buffer = stream.buffer[:0]
	return nil
}

func (stream *chunkStream) String() string {
	return fmt.Sprintf("profile-state-stream(bytes=%d)", stream.bytes)
}
