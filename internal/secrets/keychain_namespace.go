package secrets

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const keychainStoreNamespaceDomain = "hideout.keychain-store.v1"

func keychainServiceForStoreRoot(storeRoot string) (string, error) {
	canonical, err := canonicalKeychainStoreRoot(storeRoot)
	if err != nil {
		return "", err
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		defaultRoot, defaultErr := canonicalKeychainStoreRoot(
			filepath.Join(home, ".hideout"),
		)
		if defaultErr == nil && canonical == defaultRoot {
			// Preserve the original service for the normal user store so an
			// upgrade does not silently orphan existing managed secrets.
			return KeychainServiceName, nil
		}
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(keychainStoreNamespaceDomain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(canonical))
	digest := hash.Sum(nil)
	return KeychainServiceName + ".store." +
		hex.EncodeToString(digest[:12]), nil
}

func canonicalKeychainStoreRoot(storeRoot string) (string, error) {
	storeRoot = strings.TrimSpace(storeRoot)
	if storeRoot == "" || !filepath.IsAbs(storeRoot) {
		return "", errors.New("Keychain store root must be absolute")
	}
	canonical := filepath.Clean(storeRoot)
	if physical, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = physical
	}
	return canonical, nil
}
