package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"
)

var processBuildIdentity struct {
	sync.Once
	value string
	err   error
}

// currentProcessBuildID derives an exact identity from the running executable.
// Hideout's app layer normally supplies its cheaper link-time build identity;
// direct daemon embedders still get a strict identity rather than an empty one.
func currentProcessBuildID() (string, error) {
	processBuildIdentity.Do(func() {
		path, err := os.Executable()
		if err != nil {
			processBuildIdentity.err = fmt.Errorf("resolve executable: %w", err)
			return
		}
		file, err := os.Open(path)
		if err != nil {
			processBuildIdentity.err = fmt.Errorf("open executable: %w", err)
			return
		}
		defer file.Close()
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			processBuildIdentity.err = fmt.Errorf("hash executable: %w", err)
			return
		}
		processBuildIdentity.value = hex.EncodeToString(hash.Sum(nil))
	})
	return processBuildIdentity.value, processBuildIdentity.err
}

func resolveBuildID(value string) (string, error) {
	if value != "" {
		if !validBuildID(value) {
			return "", fmt.Errorf("invalid build identity")
		}
		return value, nil
	}
	return currentProcessBuildID()
}
