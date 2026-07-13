package releasechannel

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	MaxJSONBytes           = int64(8 << 20)
	MaxEvidenceBundleBytes = int64(64 << 20)
)

func ValidateRelativePath(path string) error {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("artifact path %q must be a clean relative path", path)
	}
	if path == "." || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return fmt.Errorf("artifact path %q escapes its root", path)
	}
	if strings.ContainsAny(path, "\x00\r\n") {
		return errors.New("artifact path contains control characters")
	}
	return nil
}

func VerifyRootedRegularFile(root, relative, digest string, expectedBytes int64) error {
	if err := ValidateRelativePath(relative); err != nil {
		return err
	}
	if !IsSHA256(digest) {
		return fmt.Errorf("artifact %q has invalid sha256", relative)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	f, err := r.Open(relative)
	if err != nil {
		return fmt.Errorf("open artifact %q: %w", relative, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("artifact %q must be a regular file", relative)
	}
	if expectedBytes <= 0 || st.Size() != expectedBytes {
		return fmt.Errorf("artifact %q size mismatch: declared=%d actual=%d", relative, expectedBytes, st.Size())
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != digest {
		return fmt.Errorf("artifact %q digest mismatch", relative)
	}
	return nil
}

func RootedFileSHA256(root, relative string) (string, int64, error) {
	if err := ValidateRelativePath(relative); err != nil {
		return "", 0, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return "", 0, err
	}
	defer r.Close()
	f, err := r.Open(relative)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return "", 0, fmt.Errorf("artifact %q must be a regular file", relative)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), st.Size(), nil
}

func ReadRootedBounded(root, relative string, limit int64) ([]byte, error) {
	if err := ValidateRelativePath(relative); err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	f, err := r.Open(relative)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("artifact %q must be a regular file", relative)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("artifact %q exceeds %d-byte limit", relative, limit)
	}
	return data, nil
}
