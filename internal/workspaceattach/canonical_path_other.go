//go:build !darwin

package workspaceattach

import (
	"errors"
	"os"
	"path/filepath"
)

func canonicalPathFromOpenFile(file *os.File, fallback string) (string, error) {
	if file == nil {
		return "", errors.New("workspace root file is required")
	}
	path, err := filepath.EvalSymlinks(filepath.Clean(fallback))
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("workspace root canonical path is not absolute")
	}
	return filepath.Clean(path), nil
}
