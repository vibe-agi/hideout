package profilestate

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

type stageMarker struct {
	Schema string `json:"schema"`
	Owner  Owner  `json:"owner"`
}

type Materializer struct {
	profilesRoot string
	path         string
	owner        Owner
	root         *os.Root

	buffer          []byte
	hasher          hash.Hash
	received        uint64
	headerParsed    bool
	expectedEntries uint64
	entriesDone     uint64
	previousPath    string
	kinds           map[string]string
	directoryFacts  map[string]fileIdentity
	directoryModes  []archiveEntry
	current         *archiveEntry
	file            *os.File
	remaining       uint64
	finished        bool
}

func StagePath(profilesRoot string, owner Owner) (string, error) {
	if err := owner.Validate(); err != nil {
		return "", err
	}
	clean := filepath.Clean(profilesRoot)
	if !filepath.IsAbs(clean) || filepath.Base(clean) != "profiles" {
		return "", ErrUnsafePath
	}
	return filepath.Join(clean, stageDirectoryPrefix+ownerIdentity(owner)), nil
}

func NewMaterializer(profilesRoot string, owner Owner) (*Materializer, error) {
	stage, err := StagePath(profilesRoot, owner)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(profilesRoot, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(profilesRoot, 0o700); err != nil {
		return nil, err
	}
	parentInfo, err := os.Lstat(profilesRoot)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 ||
		parentInfo.Mode().Perm()&0o077 != 0 {
		return nil, errors.Join(ErrUnsafePath, err)
	}
	if err := os.Mkdir(stage, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrStageExists
		}
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(stage)
		}
	}()
	root, err := os.OpenRoot(stage)
	if err != nil {
		return nil, err
	}
	materializer := &Materializer{
		profilesRoot: profilesRoot, path: stage, owner: owner, root: root,
		hasher: sha256.New(), kinds: make(map[string]string),
		directoryFacts: make(map[string]fileIdentity),
	}
	if err := materializer.writeMarker(ownerMarkerName, archiveOwnerSchema); err != nil {
		_ = root.Close()
		return nil, err
	}
	if err := syncDirectory(stage); err != nil {
		_ = root.Close()
		return nil, err
	}
	keep = true
	return materializer, nil
}

func (materializer *Materializer) Path() string {
	if materializer == nil {
		return ""
	}
	return materializer.path
}

// Abort closes any in-flight file/root handles and removes only the stage whose
// marker still matches this materializer's immutable owner binding.
func (materializer *Materializer) Abort() error {
	if materializer == nil {
		return nil
	}
	var result error
	if materializer.file != nil {
		result = errors.Join(result, materializer.file.Close())
		materializer.file = nil
	}
	if materializer.root != nil {
		result = errors.Join(result, materializer.root.Close())
		materializer.root = nil
	}
	if materializer.path != "" {
		result = errors.Join(result, RemoveStage(materializer.path, materializer.owner))
	}
	return result
}

func (materializer *Materializer) Consume(data []byte) error {
	if materializer == nil || materializer.root == nil || materializer.finished || len(data) == 0 {
		return ErrInvalidArchive
	}
	if uint64(len(data)) > materializer.owner.LogicalBytes-materializer.received {
		return ErrInvalidArchive
	}
	_, _ = materializer.hasher.Write(data)
	materializer.received += uint64(len(data))
	materializer.buffer = append(materializer.buffer, data...)
	if len(materializer.buffer) > 4<<20+maxArchiveEntryBytes+len(archiveMagic)+4 {
		return ErrInvalidArchive
	}
	return materializer.parse()
}

func (materializer *Materializer) parse() error {
	for {
		if materializer.current != nil {
			if len(materializer.buffer) == 0 {
				return nil
			}
			n := uint64(len(materializer.buffer))
			if materializer.remaining < n {
				n = materializer.remaining
			}
			if n > 0 {
				if err := writeAll(materializer.file, materializer.buffer[:int(n)]); err != nil {
					return err
				}
				materializer.buffer = materializer.buffer[int(n):]
				materializer.remaining -= n
			}
			if materializer.remaining == 0 {
				if err := materializer.finishFile(); err != nil {
					return err
				}
			}
			continue
		}

		if !materializer.headerParsed {
			if len(materializer.buffer) < len(archiveMagic)+4 {
				return nil
			}
			if !bytes.Equal(materializer.buffer[:len(archiveMagic)], []byte(archiveMagic)) {
				return ErrInvalidArchive
			}
			length := int(binary.BigEndian.Uint32(materializer.buffer[len(archiveMagic):]))
			if length <= 0 || length > maxArchiveHeaderBytes {
				return ErrInvalidArchive
			}
			end := len(archiveMagic) + 4 + length
			if len(materializer.buffer) < end {
				return nil
			}
			var header archiveHeader
			if err := decodeCanonicalJSON(
				materializer.buffer[len(archiveMagic)+4:end], maxArchiveHeaderBytes, &header,
			); err != nil || header.Schema != archiveSchema || header.EntryCount == 0 ||
				header.EntryCount > maxArchiveEntries {
				return errors.Join(ErrInvalidArchive, err)
			}
			materializer.expectedEntries = header.EntryCount
			materializer.headerParsed = true
			materializer.buffer = materializer.buffer[end:]
			continue
		}

		if materializer.entriesDone == materializer.expectedEntries {
			if len(materializer.buffer) != 0 {
				return ErrInvalidArchive
			}
			return nil
		}
		if len(materializer.buffer) < 4 {
			return nil
		}
		length := int(binary.BigEndian.Uint32(materializer.buffer[:4]))
		if length <= 0 || length > maxArchiveEntryBytes {
			return ErrInvalidArchive
		}
		if len(materializer.buffer) < 4+length {
			return nil
		}
		var entry archiveEntry
		if err := decodeCanonicalJSON(materializer.buffer[4:4+length], maxArchiveEntryBytes, &entry); err != nil {
			return err
		}
		materializer.buffer = materializer.buffer[4+length:]
		if err := materializer.beginEntry(entry); err != nil {
			return err
		}
	}
}

func (materializer *Materializer) beginEntry(entry archiveEntry) error {
	if err := entry.validate(); err != nil ||
		(materializer.previousPath != "" && materializer.previousPath >= entry.Path) {
		return errors.Join(ErrInvalidArchive, err)
	}
	parent := parentPath(entry.Path)
	if parent == "" {
		if !slices.Contains(includedRoots, entry.Path) || entry.Kind != archiveEntryKindDir {
			return ErrUnsafePath
		}
	} else if materializer.kinds[parent] != archiveEntryKindDir {
		return ErrUnsafePath
	} else if err := materializer.verifyDirectory(parent); err != nil {
		return err
	}
	if _, exists := materializer.kinds[entry.Path]; exists {
		return ErrInvalidArchive
	}
	materializer.previousPath = entry.Path
	switch entry.Kind {
	case archiveEntryKindDir:
		if err := materializer.root.Mkdir(entry.Path, 0o700); err != nil {
			return errors.Join(ErrUnsafePath, err)
		}
		info, err := materializer.root.Lstat(entry.Path)
		if err != nil || info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.Join(ErrUnsafePath, err)
		}
		identity, ok := identityFromInfo(info)
		if !ok {
			return ErrUnsafePath
		}
		materializer.kinds[entry.Path] = entry.Kind
		materializer.directoryFacts[entry.Path] = identity
		materializer.directoryModes = append(materializer.directoryModes, entry)
		materializer.entriesDone++
	case archiveEntryKindSymlink:
		if err := materializer.root.Symlink(entry.Target, entry.Path); err != nil {
			return errors.Join(ErrUnsafePath, err)
		}
		materializer.kinds[entry.Path] = entry.Kind
		materializer.entriesDone++
	case archiveEntryKindFile:
		file, err := materializer.root.OpenFile(
			entry.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600,
		)
		if err != nil {
			return errors.Join(ErrUnsafePath, err)
		}
		copyEntry := entry
		materializer.current = &copyEntry
		materializer.file = file
		materializer.remaining = entry.Size
		materializer.kinds[entry.Path] = entry.Kind
		if entry.Size == 0 {
			return materializer.finishFile()
		}
	default:
		return ErrSpecialFile
	}
	return nil
}

func (materializer *Materializer) finishFile() error {
	if materializer.current == nil || materializer.file == nil || materializer.remaining != 0 {
		return ErrInvalidArchive
	}
	entry := *materializer.current
	if err := materializer.file.Sync(); err != nil {
		return err
	}
	if err := materializer.file.Close(); err != nil {
		return err
	}
	materializer.file = nil
	if err := materializer.root.Chmod(entry.Path, os.FileMode(entry.Mode)); err != nil {
		return err
	}
	materializer.current = nil
	materializer.entriesDone++
	return nil
}

func (materializer *Materializer) verifyDirectory(relative string) error {
	info, err := materializer.root.Lstat(relative)
	if err != nil || info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(ErrUnsafePath, err)
	}
	identity, ok := identityFromInfo(info)
	want, exists := materializer.directoryFacts[relative]
	// A directory's link count legitimately changes when child directories are
	// created. Device and inode are the stable substitution boundary here.
	if !ok || !exists || identity.device != want.device || identity.inode != want.inode {
		return ErrUnsafePath
	}
	return nil
}

func (materializer *Materializer) Finish() error {
	if materializer == nil || materializer.root == nil || materializer.finished ||
		materializer.received != materializer.owner.LogicalBytes || !materializer.headerParsed ||
		materializer.entriesDone != materializer.expectedEntries || materializer.current != nil ||
		len(materializer.buffer) != 0 ||
		sha256Digest(materializer.hasher.Sum(nil)) != materializer.owner.ContentDigest {
		return ErrInvalidArchive
	}
	for index := len(materializer.directoryModes) - 1; index >= 0; index-- {
		entry := materializer.directoryModes[index]
		if err := materializer.verifyDirectory(entry.Path); err != nil {
			return err
		}
		if err := materializer.root.Chmod(entry.Path, os.FileMode(entry.Mode)); err != nil {
			return err
		}
		if err := syncRootDirectory(materializer.root, entry.Path); err != nil {
			return err
		}
	}
	if err := materializer.writeMarker(completeMarkerName, archiveCompleteSchema); err != nil {
		return err
	}
	if err := syncDirectory(materializer.path); err != nil {
		return err
	}
	if err := materializer.root.Close(); err != nil {
		return err
	}
	materializer.root = nil
	materializer.finished = true
	return nil
}

func (materializer *Materializer) writeMarker(name, schema string) error {
	data, err := json.Marshal(stageMarker{Schema: schema, Owner: materializer.owner})
	if err != nil {
		return err
	}
	file, err := materializer.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := writeAll(file, data); err != nil {
		_ = file.Close()
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func VerifyStage(stagePath string, owner Owner) error {
	if err := validateStagePath(stagePath, owner); err != nil {
		return err
	}
	if err := verifyMarker(stagePath, ownerMarkerName, archiveOwnerSchema, owner); err != nil {
		return err
	}
	if err := verifyMarker(stagePath, completeMarkerName, archiveCompleteSchema, owner); err != nil {
		return err
	}
	entries, err := os.ReadDir(stagePath)
	if err != nil {
		return err
	}
	allowed := append(IncludedRoots(), ownerMarkerName, completeMarkerName)
	sort.Strings(allowed)
	observed := make([]string, len(entries))
	for index, entry := range entries {
		observed[index] = entry.Name()
	}
	sort.Strings(observed)
	if !slices.Equal(observed, allowed) {
		return ErrInvalidArchive
	}
	snapshot, err := Capture(stagePath)
	if err != nil {
		return err
	}
	if snapshot.Digest() != owner.ContentDigest || snapshot.LogicalBytes() != owner.LogicalBytes {
		return ErrOwnerMismatch
	}
	return nil
}

// VerifyPublishedState verifies the preserved application-state subset after a
// stage has been atomically renamed to its destination profile and generated
// destination identity/policy files have been added around it.
func VerifyPublishedState(profilePath string, owner Owner) error {
	if err := owner.Validate(); err != nil {
		return err
	}
	clean := filepath.Clean(profilePath)
	if !filepath.IsAbs(clean) || filepath.Base(clean) != owner.ProfileName ||
		filepath.Base(filepath.Dir(clean)) != "profiles" {
		return ErrOwnerMismatch
	}
	info, err := os.Lstat(clean)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 {
		return errors.Join(ErrOwnerMismatch, err)
	}
	if err := verifyMarker(clean, ownerMarkerName, archiveOwnerSchema, owner); err != nil {
		return err
	}
	if err := verifyMarker(clean, completeMarkerName, archiveCompleteSchema, owner); err != nil {
		return err
	}
	return VerifyContent(clean, owner)
}

// VerifyContent binds only the included application-state roots. It remains
// valid after transaction-finalization removes the temporary owner markers.
func VerifyContent(profilePath string, owner Owner) error {
	if err := owner.Validate(); err != nil {
		return err
	}
	clean := filepath.Clean(profilePath)
	if !filepath.IsAbs(clean) || filepath.Base(clean) != owner.ProfileName ||
		filepath.Base(filepath.Dir(clean)) != "profiles" {
		return ErrOwnerMismatch
	}
	snapshot, err := Capture(clean)
	if err != nil {
		return err
	}
	if snapshot.Digest() != owner.ContentDigest || snapshot.LogicalBytes() != owner.LogicalBytes {
		return ErrOwnerMismatch
	}
	return nil
}

func MarkerNames() []string {
	return []string{ownerMarkerName, completeMarkerName}
}

func RemoveStage(stagePath string, owner Owner) error {
	if err := validateStagePath(stagePath, owner); err != nil {
		return err
	}
	if err := verifyMarker(stagePath, ownerMarkerName, archiveOwnerSchema, owner); err != nil {
		return err
	}
	if err := os.RemoveAll(stagePath); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(stagePath))
}

func validateStagePath(stagePath string, owner Owner) error {
	if err := owner.Validate(); err != nil {
		return err
	}
	clean := filepath.Clean(stagePath)
	expected, err := StagePath(filepath.Dir(clean), owner)
	if err != nil || clean != expected {
		return errors.Join(ErrOwnerMismatch, err)
	}
	info, err := os.Lstat(clean)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 {
		return errors.Join(ErrOwnerMismatch, err)
	}
	return nil
}

func verifyMarker(stagePath, name, schema string, owner Owner) error {
	info, err := os.Lstat(filepath.Join(stagePath, name))
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 || info.Size() <= 0 || info.Size() > maxArchiveHeaderBytes {
		return errors.Join(ErrOwnerMismatch, err)
	}
	data, err := os.ReadFile(filepath.Join(stagePath, name))
	if err != nil {
		return err
	}
	var marker stageMarker
	if err := decodeCanonicalJSON(data, maxArchiveHeaderBytes, &marker); err != nil ||
		marker.Schema != schema || marker.Owner != owner {
		return errors.Join(ErrOwnerMismatch, err)
	}
	return nil
}

func syncRootDirectory(root *os.Root, relative string) error {
	directory, err := root.Open(relative)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
