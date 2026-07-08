package packagekit

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
)

func CleanRoot(root, label string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New(label + " is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs, nil
}

func JoinRelative(root, rel string) (string, error) {
	clean, err := CleanRelative(rel)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(clean)), nil
}

func CleanRelative(rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", errors.New("path must be relative")
	}
	if strings.Contains(rel, `\`) {
		return "", errors.New("path must use slash separators")
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path must stay inside root")
	}
	return clean, nil
}

func installPathForArtifact(rel string) (string, error) {
	clean, err := CleanRelative(rel)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(clean, "bin/") {
		return clean, nil
	}
	return path.Join(packageMetadataRoot, clean), nil
}
