package daemon

import (
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
	if err := os.WriteFile(filepath.Join(dir, tokenName), []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}

// readToken reads the current operator token for a store's daemon (client side).
func readToken(storeRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(runtimeDir(storeRoot), tokenName))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
