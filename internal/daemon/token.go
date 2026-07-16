package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/vibe-agi/hideout/internal/manager"
)

const tokenName = "token"

// mintToken generates a fresh operator token and persists it 0600 in the runtime
// dir, overwriting any prior token. It is minted per start, so a restart
// invalidates prior clients' tokens (consistent with restart fail-closed and
// mid-stream credential invalidation).
func mintToken(dir string) (string, error) {
	tok, err := manager.NewUIToken()
	if err != nil {
		return "", err
	}
	if err := writeTokenFile(dir, tok); err != nil {
		return "", err
	}
	return tok, nil
}

// writeTokenFile atomically replaces the client-visible operator token. The
// temporary file lives beside the destination so rename cannot cross a
// filesystem boundary, and its mode is fixed before any token bytes are
// written.
func writeTokenFile(dir, token string) error {
	if token == "" {
		return errors.New("daemon: refusing to persist an empty operator token")
	}
	tmp, err := os.CreateTemp(dir, "."+tokenName+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keep := true
	defer func() {
		if keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filepath.Join(dir, tokenName)); err != nil {
		return err
	}
	keep = false
	return nil
}

// readToken reads the current operator token for a store's daemon (client side).
func readToken(storeRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(runtimeDir(storeRoot), tokenName))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
