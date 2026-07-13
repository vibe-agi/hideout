package productevidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxProductEvidenceManifestBytes int64 = 8 << 20

type Writer struct {
	path     string
	manifest Manifest
}

func NewWriter(path string, manifest Manifest) (*Writer, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("product evidence path is required")
	}
	return &Writer{path: filepath.Clean(path), manifest: manifest}, nil
}

func (w *Writer) AddProof(proof ProofEntry) {
	if w == nil {
		return
	}
	w.manifest.Proofs = append(w.manifest.Proofs, proof)
}

func (w *Writer) Manifest() Manifest {
	if w == nil {
		return Manifest{}
	}
	return w.manifest.Sanitized()
}

func (w *Writer) Write() error {
	if w == nil {
		return errors.New("product evidence writer is nil")
	}
	return WriteFile(w.path, w.manifest)
}

func WriteFile(path string, manifest Manifest) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("product evidence path is required")
	}
	clean := filepath.Clean(path)
	manifest = manifest.Sanitized()
	if err := manifest.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(clean), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(clean), "."+filepath.Base(clean)+".tmp-*")
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
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, clean); err != nil {
		return err
	}
	keepTemp = false
	return nil
}

func ReadFile(path string) (Manifest, error) {
	data, err := readBoundedEvidenceFile(filepath.Clean(path), maxProductEvidenceManifestBytes)
	if err != nil {
		return Manifest{}, err
	}
	if ContainsControlPlaneBytes(data) {
		return Manifest{}, errors.New("product evidence manifest contains unredacted control-plane material")
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return Manifest{}, errors.New("product evidence manifest contains multiple JSON values")
		}
		return Manifest{}, fmt.Errorf("product evidence manifest trailing data: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func readBoundedEvidenceFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("product evidence manifest exceeds %d-byte validation limit", limit)
	}
	return data, nil
}
