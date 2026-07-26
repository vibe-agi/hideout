package supportreport

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func WriteAtomic(path string, data []byte, overwrite bool) error {
	if len(data) > MaxBytes {
		return fmt.Errorf("support report is %d bytes; maximum is %d", len(data), MaxBytes)
	}
	resolved, err := safeDestination(path, overwrite)
	if err != nil {
		return err
	}
	parent := filepath.Dir(resolved)
	tmp, err := os.CreateTemp(parent, "."+filepath.Base(resolved)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
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

	if overwrite {
		if err := os.Rename(tmpPath, resolved); err != nil {
			return err
		}
		keepTemp = false
		return nil
	}
	if err := os.Link(tmpPath, resolved); err != nil {
		if os.IsExist(err) {
			return errors.New("support report destination already exists; use --overwrite after inspection")
		}
		return err
	}
	if err := os.Remove(tmpPath); err != nil {
		_ = os.Remove(resolved)
		return err
	}
	keepTemp = false
	return nil
}

func ValidateDestination(path string, overwrite bool) error {
	_, err := safeDestination(path, overwrite)
	return err
}

func safeDestination(path string, overwrite bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("--out is required")
	}
	if filepath.Clean(path) != path {
		return "", errors.New("--out must be a clean path")
	}
	if strings.Contains(path, "://") {
		return "", errors.New("--out must be a local path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(abs); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("support report destination must not be a symbolic link")
		}
		if !overwrite {
			return "", errors.New("support report destination already exists; use --overwrite after inspection")
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("support report destination must be a regular file")
		}
		if err := requireCurrentOwner(info, "destination"); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("support report parent must already exist: %w", err)
	}
	info, err := os.Stat(parent)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("support report parent is not a directory")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("support report parent must not be group/world writable")
	}
	if err := requireCurrentOwner(info, "parent"); err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func requireCurrentOwner(info os.FileInfo, label string) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("support report %s ownership is unavailable", label)
	}
	if int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("support report %s is not owned by the current user", label)
	}
	return nil
}
