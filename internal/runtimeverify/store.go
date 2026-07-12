package runtimeverify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/vibe-agi/hideout/internal/environment"
)

const receiptFile = "runtime-verification.json"

type Store struct {
	Root string
}

func (s Store) Path(environmentID string) string {
	return filepath.Join(s.Root, "environments", environmentID, receiptFile)
}

func (s Store) Write(receipt Receipt) error {
	receipt.Normalize()
	if err := receipt.Validate(); err != nil {
		return err
	}
	dir, err := s.ensurePrivateEnvironmentDir(receipt.EnvironmentID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(dir, receiptFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s Store) Load(environmentID string) (Receipt, error) {
	if !environment.ValidID(environmentID) {
		return Receipt{}, fmt.Errorf("invalid runtime verification environment id %q", environmentID)
	}
	data, err := os.ReadFile(s.Path(environmentID))
	if err != nil {
		return Receipt{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var receipt Receipt
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, fmt.Errorf("decode runtime verification: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Receipt{}, errors.New("runtime verification has trailing JSON")
	}
	if err := receipt.Validate(); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// Remove invalidates the previous observation before a new live probe starts.
// A canceled or malformed probe therefore cannot leave historical success
// looking current.
func (s Store) Remove(environmentID string) error {
	if !environment.ValidID(environmentID) {
		return fmt.Errorf("invalid runtime verification environment id %q", environmentID)
	}
	err := os.Remove(s.Path(environmentID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s Store) ensurePrivateEnvironmentDir(environmentID string) (string, error) {
	if !environment.ValidID(environmentID) {
		return "", fmt.Errorf("invalid runtime verification environment id %q", environmentID)
	}
	parents := []string{filepath.Join(s.Root, "environments"), filepath.Join(s.Root, "environments", environmentID)}
	for _, dir := range parents {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return "", err
			}
			info, err = os.Lstat(dir)
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("runtime verification directory %q must not be a symlink", dir)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("runtime verification directory %q is not a directory", dir)
		}
		if info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("runtime verification directory %q must be private", dir)
		}
	}
	return parents[1], nil
}
