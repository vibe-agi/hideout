package packagekit

import (
	"errors"
	"fmt"
	"os"
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

// rootedPackagePath validates every ancestor immediately before a destructive
// operation. os.Root then keeps the operation confined even if the tree is
// renamed or raced after validation.
func rootedPackagePath(root *os.Root, rel string) (string, error) {
	clean, err := CleanRelative(rel)
	if err != nil {
		return "", err
	}
	parts := strings.Split(clean, "/")
	for i := 1; i < len(parts); i++ {
		ancestor := filepath.FromSlash(path.Join(parts[:i]...))
		info, err := root.Lstat(ancestor)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("package path ancestor %q is a symbolic link", path.Join(parts[:i]...))
		}
		if !info.IsDir() {
			return "", fmt.Errorf("package path ancestor %q is not a directory", path.Join(parts[:i]...))
		}
	}
	return filepath.FromSlash(clean), nil
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
