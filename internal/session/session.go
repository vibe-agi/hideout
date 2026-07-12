package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Layout struct {
	Root                   string
	ID                     string
	Dir                    string
	TmpDir                 string
	ShimDir                string
	AuditPath              string
	BrokerSock             string
	BrokerEndpointPath     string
	HostFSReadDir          string
	HostFSReadOwnerLock    string
	HostFSReadProviderLock string
	HostFSReadStatePath    string
	HostFSReadGrantsPath   string
}

type CleanupResult struct {
	Sessions int
	Removed  []string
}

func New(root string) (Layout, error) {
	id, err := newID()
	if err != nil {
		return Layout{}, err
	}
	dir := filepath.Join(root, "sessions", id)
	brokerSock := filepath.Join(dir, "broker.sock")
	if len(brokerSock) > 100 {
		brokerSock = filepath.Join(shortSocketDir(), "hideout-"+id+".sock")
	}
	hostFSReadDir := filepath.Join(dir, "hostfs-read")
	layout := Layout{
		Root:                   root,
		ID:                     id,
		Dir:                    dir,
		TmpDir:                 filepath.Join(dir, "tmp"),
		ShimDir:                filepath.Join(dir, "shims"),
		AuditPath:              filepath.Join(dir, "audit.jsonl"),
		BrokerSock:             brokerSock,
		BrokerEndpointPath:     filepath.Join(dir, "broker-endpoint.json"),
		HostFSReadDir:          hostFSReadDir,
		HostFSReadOwnerLock:    filepath.Join(hostFSReadDir, "owner.lock"),
		HostFSReadProviderLock: filepath.Join(hostFSReadDir, "provider.lock"),
		HostFSReadStatePath:    filepath.Join(hostFSReadDir, "state.json"),
		HostFSReadGrantsPath:   filepath.Join(hostFSReadDir, "grants.json"),
	}
	if err := os.MkdirAll(layout.TmpDir, 0o700); err != nil {
		return Layout{}, err
	}
	if err := os.MkdirAll(layout.ShimDir, 0o700); err != nil {
		return Layout{}, err
	}
	if err := os.MkdirAll(layout.HostFSReadDir, 0o700); err != nil {
		return Layout{}, err
	}
	return layout, nil
}

func CleanupEphemeral(root, sessionID string, dryRun bool) (CleanupResult, error) {
	if root == "" {
		return CleanupResult{}, errors.New("session cleanup root is required")
	}
	sessionsDir := filepath.Join(root, "sessions")
	ids, err := cleanupSessionIDs(sessionsDir, sessionID)
	if err != nil {
		return CleanupResult{}, err
	}
	result := CleanupResult{Sessions: len(ids)}
	for _, id := range ids {
		dir := filepath.Join(sessionsDir, id)
		paths := ephemeralPaths(dir, id)
		for _, path := range paths {
			if _, err := os.Lstat(path); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return result, err
			}
			result.Removed = append(result.Removed, path)
			if !dryRun {
				if err := os.RemoveAll(path); err != nil {
					return result, err
				}
			}
		}
		if !dryRun {
			removeEmptyDir(filepath.Join(dir, "network"))
			removeEmptyDir(dir)
		}
	}
	return result, nil
}

func cleanupSessionIDs(sessionsDir, sessionID string) ([]string, error) {
	if sessionID != "" {
		if !ValidID(sessionID) {
			return nil, fmt.Errorf("invalid session id %q", sessionID)
		}
		return []string{sessionID}, nil
	}
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && ValidID(entry.Name()) {
			ids = append(ids, entry.Name())
		}
	}
	return ids, nil
}

func ValidID(sessionID string) bool {
	if strings.TrimSpace(sessionID) != sessionID || !strings.HasPrefix(sessionID, "ses_") {
		return false
	}
	if strings.ContainsAny(sessionID, `/\`) || sessionID == "." || sessionID == ".." {
		return false
	}
	return true
}

func ephemeralPaths(dir, id string) []string {
	return []string{
		filepath.Join(dir, "tmp"),
		filepath.Join(dir, "shims"),
		filepath.Join(dir, "bootstrap"),
		filepath.Join(dir, "identity"),
		filepath.Join(dir, "broker.sock"),
		filepath.Join(shortSocketDir(), "hideout-"+id+".sock"),
		filepath.Join(dir, "broker-endpoint.json"),
		filepath.Join(dir, "network-plan.json"),
		filepath.Join(dir, "network"),
		filepath.Join(dir, "hostfs-read"),
	}
}

func removeEmptyDir(path string) {
	_ = os.Remove(path)
}

func shortSocketDir() string {
	if st, err := os.Stat("/tmp"); err == nil && st.IsDir() {
		return "/tmp"
	}
	return os.TempDir()
}

func newID() (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "ses_" + time.Now().UTC().Format("20060102T150405Z_") + hex.EncodeToString(b[:]), nil
}
