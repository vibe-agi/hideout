package profilestate

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"
)

const (
	archiveMagic            = "HIDPST01"
	archiveSchema           = "hideout.profile-state-archive/v1"
	archiveOwnerSchema      = "hideout.profile-state-owner/v1"
	archiveCompleteSchema   = "hideout.profile-state-complete/v1"
	maxArchiveHeaderBytes   = 4096
	maxArchiveEntryBytes    = 16 << 10
	maxArchiveEntries       = 1_048_576
	maxArchivePathBytes     = 4096
	maxArchiveTargetBytes   = 4096
	maxArchiveLogicalBytes  = uint64(4) << 40
	ownerMarkerName         = ".profile-state-owner.json"
	completeMarkerName      = ".profile-state-complete.json"
	stageDirectoryPrefix    = ".migration-state-"
	archiveEntryKindDir     = "directory"
	archiveEntryKindFile    = "file"
	archiveEntryKindSymlink = "symlink"
)

var (
	ErrInvalidArchive = errors.New("profile state archive is invalid")
	ErrUnsafePath     = errors.New("profile state path is unsafe")
	ErrAliasedFile    = errors.New("profile state file has aliased storage")
	ErrSpecialFile    = errors.New("profile state contains an unsupported special file")
	ErrSourceChanged  = errors.New("profile state source changed during capture")
	ErrOwnerMismatch  = errors.New("profile state stage owner does not match")
	ErrStageExists    = errors.New("profile state stage already exists")

	ownerTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,191}$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	includedRoots     = []string{"browser", "config", "data", "home"}
)

// Owner is the non-secret durable binding for one destination profile-state
// stage. The path is derived from these facts; callers never select a cleanup
// target directly.
type Owner struct {
	OperationID   string `json:"operationId"`
	ProfileName   string `json:"profileName"`
	ComponentID   string `json:"componentId"`
	ContentDigest string `json:"contentDigest"`
	LogicalBytes  uint64 `json:"logicalBytes"`
}

func (owner Owner) Validate() error {
	if !ownerTokenPattern.MatchString(owner.OperationID) ||
		!ownerTokenPattern.MatchString(owner.ProfileName) ||
		!ownerTokenPattern.MatchString(owner.ComponentID) ||
		!digestPattern.MatchString(owner.ContentDigest) ||
		owner.LogicalBytes == 0 || owner.LogicalBytes > maxArchiveLogicalBytes {
		return ErrOwnerMismatch
	}
	return nil
}

type archiveHeader struct {
	Schema     string `json:"schema"`
	EntryCount uint64 `json:"entryCount"`
}

type archiveEntry struct {
	Kind   string `json:"kind"`
	Mode   uint32 `json:"mode"`
	Path   string `json:"path"`
	Size   uint64 `json:"size"`
	Target string `json:"target,omitempty"`
}

func (entry archiveEntry) validate() error {
	if !validArchivePath(entry.Path) || entry.Mode > 0o777 {
		return ErrUnsafePath
	}
	switch entry.Kind {
	case archiveEntryKindDir:
		if entry.Size != 0 || entry.Target != "" || entry.Mode == 0 {
			return ErrInvalidArchive
		}
	case archiveEntryKindFile:
		if entry.Target != "" || entry.Size > maxArchiveLogicalBytes {
			return ErrInvalidArchive
		}
	case archiveEntryKindSymlink:
		if entry.Mode != 0 || entry.Size != 0 || !validArchiveSymlink(entry.Path, entry.Target) {
			return ErrUnsafePath
		}
	default:
		return ErrSpecialFile
	}
	return nil
}

func IncludedRoots() []string {
	return append([]string(nil), includedRoots...)
}

func encodeArchiveHeader(entryCount uint64) ([]byte, error) {
	if entryCount == 0 || entryCount > maxArchiveEntries {
		return nil, ErrInvalidArchive
	}
	payload, err := json.Marshal(archiveHeader{Schema: archiveSchema, EntryCount: entryCount})
	if err != nil || len(payload) == 0 || len(payload) > maxArchiveHeaderBytes {
		return nil, errors.Join(ErrInvalidArchive, err)
	}
	encoded := make([]byte, len(archiveMagic)+4+len(payload))
	copy(encoded, archiveMagic)
	binary.BigEndian.PutUint32(encoded[len(archiveMagic):], uint32(len(payload)))
	copy(encoded[len(archiveMagic)+4:], payload)
	return encoded, nil
}

func encodeArchiveEntry(entry archiveEntry) ([]byte, error) {
	if err := entry.validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(entry)
	if err != nil || len(payload) == 0 || len(payload) > maxArchiveEntryBytes {
		return nil, errors.Join(ErrInvalidArchive, err)
	}
	encoded := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(encoded[:4], uint32(len(payload)))
	copy(encoded[4:], payload)
	return encoded, nil
}

func decodeCanonicalJSON(data []byte, limit int, target any) error {
	if len(data) == 0 || len(data) > limit {
		return ErrInvalidArchive
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.Join(ErrInvalidArchive, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalidArchive
	}
	canonical, err := json.Marshal(target)
	if err != nil || !bytes.Equal(canonical, data) {
		return errors.Join(ErrInvalidArchive, err)
	}
	return nil
}

func validArchivePath(value string) bool {
	if value == "" || len(value) > maxArchivePathBytes || strings.ContainsRune(value, 0) ||
		strings.HasPrefix(value, "/") || path.Clean(value) != value || value == "." ||
		strings.HasPrefix(value, "../") || value == ".." {
		return false
	}
	root, _, _ := strings.Cut(value, "/")
	if !slices.Contains(includedRoots, root) || isGeneratedPath(value) {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func validArchiveSymlink(linkPath, target string) bool {
	if target == "" || len(target) > maxArchiveTargetBytes || strings.ContainsRune(target, 0) ||
		path.IsAbs(target) || path.Clean(target) != target {
		return false
	}
	resolved := path.Clean(path.Join(path.Dir(linkPath), target))
	return validArchivePath(resolved)
}

func isGeneratedPath(value string) bool {
	switch value {
	case "home/.config", "home/.cache", "home/.local/share", "home/.gitconfig":
		return true
	default:
		return false
	}
}

func addLogical(current uint64, parts ...uint64) (uint64, error) {
	for _, part := range parts {
		if part > maxArchiveLogicalBytes || current > maxArchiveLogicalBytes-part {
			return 0, ErrInvalidArchive
		}
		current += part
	}
	return current, nil
}

func sha256Digest(sum []byte) string {
	return "sha256:" + hex.EncodeToString(sum)
}

func ownerIdentity(owner Owner) string {
	encoded, _ := json.Marshal(owner)
	digest := sha256.Sum256(append([]byte("hideout-profile-state-stage/v1\x00"), encoded...))
	return hex.EncodeToString(digest[:20])
}

func cleanSlashPath(value string) string {
	return strings.TrimPrefix(path.Clean(strings.ReplaceAll(value, "\\", "/")), "./")
}

func parentPath(value string) string {
	parent := path.Dir(value)
	if parent == "." {
		return ""
	}
	return parent
}

func compareEntries(left, right archiveEntry) int {
	return strings.Compare(left.Path, right.Path)
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func invalidArchivef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidArchive, fmt.Sprintf(format, args...))
}
