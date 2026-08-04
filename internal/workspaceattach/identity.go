package workspaceattach

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/vibe-agi/hideout/internal/workspacepath"
)

const (
	identityKeySchema = "hideout.workspace-identity-key/v1"
	identityDomain    = "hideout.workspace.identity"
)

type identityKeyRecord struct {
	Schema    string    `json:"schema"`
	Key       string    `json:"key"`
	CreatedAt time.Time `json:"createdAt"`
}

type RootFileIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

func (identity RootFileIdentity) Validate() error {
	if identity.Inode == 0 {
		return errors.New("workspace root file identity is invalid")
	}
	return nil
}

// LoadOrCreateIdentityKey initializes the private correlation key only when no
// attachment/evidence state exists. Existing state plus a missing or corrupt
// key is an explicit recovery condition, never an implicit rotation.
func LoadOrCreateIdentityKey(storeRoot string, stateExists bool) ([]byte, error) {
	if !filepath.IsAbs(storeRoot) {
		return nil, errors.New("workspace identity store root must be absolute")
	}
	dir := filepath.Join(storeRoot, "workspace-identity")
	path := filepath.Join(dir, "key.json")
	key, err := loadIdentityKey(path)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("workspace identity key is corrupt: %w", err)
	}
	if stateExists {
		return nil, errors.New("workspace identity key is missing while attachment state exists; explicit recovery is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("workspace identity directory must be a private real directory")
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	record := identityKeyRecord{Schema: identityKeySchema, Key: hex.EncodeToString(key), CreatedAt: time.Now().UTC()}
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(dir, ".workspace-key-*.tmp")
	if err != nil {
		return nil, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return nil, err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return nil, err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return nil, err
	}
	if err := temporary.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		return loadIdentityKey(path)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return nil, errors.Join(syncErr, closeErr)
	}
	return append([]byte(nil), key...), nil
}

func loadIdentityKey(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 4096 {
		return nil, errors.New("workspace identity key file must be a bounded private regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record identityKeyRecord
	if err := decoder.Decode(&record); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("workspace identity key contains trailing data")
	}
	key, err := hex.DecodeString(record.Key)
	if record.Schema != identityKeySchema || len(key) != 32 || record.CreatedAt.IsZero() || err != nil {
		return nil, errors.New("workspace identity key record is invalid")
	}
	return key, nil
}

func CaptureRootIdentity(path string) (canonical string, identity RootFileIdentity, err error) {
	if !filepath.IsAbs(path) {
		return "", RootFileIdentity{}, errors.New("workspace root must be absolute")
	}
	canonical, err = filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", RootFileIdentity{}, err
	}
	file, err := os.Open(canonical)
	if err != nil {
		return "", RootFileIdentity{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", RootFileIdentity{}, err
	}
	if !info.IsDir() {
		return "", RootFileIdentity{}, errors.New("workspace root must be a directory")
	}
	canonical, err = canonicalPathFromOpenFile(file, canonical)
	if err != nil {
		return "", RootFileIdentity{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", RootFileIdentity{}, errors.New("workspace root file identity is unavailable")
	}
	identity = RootFileIdentity{Device: uint64(stat.Dev), Inode: uint64(stat.Ino)}
	if err := identity.Validate(); err != nil {
		return "", RootFileIdentity{}, err
	}
	return canonical, identity, nil
}

func DeriveWorkspaceID(key []byte, canonicalRoot string, identity RootFileIdentity) (string, error) {
	if len(key) != 32 || !filepath.IsAbs(canonicalRoot) {
		return "", errors.New("workspace identity inputs are invalid")
	}
	if err := identity.Validate(); err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(identityDomain))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(filepath.Clean(canonicalRoot)))
	_, _ = mac.Write([]byte{0})
	_, _ = fmt.Fprintf(mac, "%d:%d", identity.Device, identity.Inode)
	return "wrk_" + hex.EncodeToString(mac.Sum(nil)), nil
}

func validWorkspaceID(id string) bool {
	return workspacepath.ValidWorkspaceID(id)
}
