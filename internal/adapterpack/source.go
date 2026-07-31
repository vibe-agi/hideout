package adapterpack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/vibe-agi/hideout/internal/packsnapshot"
)

func LockSource(root string, spec SourceSpec, dest string) (Manifest, Revision, error) {
	// Adapter-pack staging is private scratch state. Preserve the 011 lifecycle,
	// which replaces an abandoned staging snapshot before publishing a revision.
	if err := os.RemoveAll(dest); err != nil {
		return Manifest{}, Revision{}, err
	}
	snapshot, err := packsnapshot.Acquire(packsnapshot.SourceSpec{
		Kind:   spec.Kind,
		Path:   spec.Path,
		URL:    spec.URL,
		Commit: spec.Commit,
	}, dest, packsnapshot.Options{
		WorkRoot:    root,
		Limits:      adapterSnapshotLimits(),
		DigestStyle: packsnapshot.DigestLegacyPathContentV1,
	})
	if err != nil {
		return Manifest{}, Revision{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(dest)
		}
	}()
	manifest, manifestBytes, err := LoadManifest(filepath.Join(dest, ManifestFileName))
	if err != nil {
		return Manifest{}, Revision{}, fmt.Errorf("load adapter pack manifest: %w", err)
	}
	adapterDigests, err := adapterDigests(dest, manifest)
	if err != nil {
		return Manifest{}, Revision{}, err
	}
	rev := Revision{
		RevisionID:       RevisionID(snapshot.Digest),
		Version:          manifest.Version,
		Source:           adapterSourceLock(snapshot.Source),
		ManifestDigest:   DigestBytes(manifestBytes),
		SourceDigest:     snapshot.Digest,
		AdapterDigests:   adapterDigests,
		ValidationStatus: "passed",
	}
	cleanup = false
	return manifest, rev, nil
}

func DigestBytes(data []byte) string {
	return packsnapshot.DigestBytes(data)
}

func RevisionID(digest string) string {
	return packsnapshot.RevisionID(digest)
}

func TestResultID(packID, revisionID string) string {
	key := packID + ":" + revisionID
	sum := sha256.Sum256([]byte(key))
	return "test_" + hex.EncodeToString(sum[:8])
}

func DigestTree(root string) (string, error) {
	digest, _, err := packsnapshot.DigestTree(root, packsnapshot.DigestLegacyPathContentV1, adapterSnapshotLimits())
	return digest, err
}

func adapterDigests(root string, manifest Manifest) (map[string]string, error) {
	out := make(map[string]string, len(manifest.Adapters))
	for _, adapter := range manifest.Adapters {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(adapter.Script)))
		if err != nil {
			return nil, err
		}
		out[adapter.ID] = DigestBytes(data)
	}
	return out, nil
}

func adapterSourceLock(lock packsnapshot.SourceLock) SourceLock {
	return SourceLock{Kind: lock.Kind, Path: lock.Path, URL: lock.URL, Commit: lock.Commit}
}

func adapterSnapshotLimits() packsnapshot.Limits {
	// 011 did not impose source-size limits. Keep its accepted lifecycle while
	// sharing the hardened snapshot implementation; host-app packs opt into the
	// bounded 032 limits independently.
	return packsnapshot.Limits{MaxFiles: math.MaxInt, MaxTotalBytes: math.MaxInt64, MaxFileBytes: math.MaxInt64}
}
