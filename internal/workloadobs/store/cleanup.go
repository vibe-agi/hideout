package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func (store *Store) Owners(
	ctx context.Context,
) ([]workloadtypes.ActivityOwner, error) {
	if ctx == nil {
		return nil, ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, ErrClosed
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.usableLocked(); err != nil {
		return nil, err
	}
	owners, err := store.listOwnersLocked()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]workloadtypes.ActivityOwner(nil), owners...), nil
}

func (store *Store) DeleteOwner(
	ctx context.Context,
	owner workloadtypes.ActivityOwner,
) (DeletionProof, error) {
	if ctx == nil {
		return DeletionProof{}, ErrInvalidOptions
	}
	if err := ctx.Err(); err != nil {
		return DeletionProof{}, err
	}
	if err := owner.Validate(); err != nil {
		return DeletionProof{}, err
	}
	if store == nil {
		return DeletionProof{}, ErrClosed
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.usableLocked(); err != nil {
		return DeletionProof{}, err
	}

	ownersRoot := filepath.Join(store.root, ownersDirectory)
	deletingRoot := filepath.Join(store.root, deletingDirectory)
	ownerRoot := ownerPath(store.root, owner)
	pendingRoot := filepath.Join(deletingRoot, owner.Key())
	sourceInfo, sourceErr := os.Lstat(ownerRoot)
	pendingInfo, pendingErr := os.Lstat(pendingRoot)
	switch {
	case sourceErr == nil && pendingErr == nil:
		return DeletionProof{}, ErrStoreCorrupt
	case sourceErr == nil:
		if err := validatePrivateDirectory(ownerRoot, sourceInfo); err != nil {
			return DeletionProof{}, err
		}
		if _, err := store.readOwnerMetadataLocked(ownerRoot, owner); err != nil {
			return DeletionProof{}, err
		}
		if err := ctx.Err(); err != nil {
			return DeletionProof{}, err
		}
		if err := os.Rename(ownerRoot, pendingRoot); err != nil {
			return DeletionProof{}, err
		}
		if err := syncDirectory(ownersRoot); err != nil {
			return DeletionProof{}, err
		}
		if err := syncDirectory(deletingRoot); err != nil {
			return DeletionProof{}, err
		}
	case !errors.Is(sourceErr, os.ErrNotExist):
		return DeletionProof{}, sourceErr
	case pendingErr == nil:
		if err := validatePrivateDirectory(pendingRoot, pendingInfo); err != nil {
			return DeletionProof{}, err
		}
		metadata, err := readOwnerMetadata(pendingRoot)
		if err != nil || !metadata.Owner.Equal(owner) {
			return DeletionProof{}, errors.Join(ErrStoreCorrupt, err)
		}
	case !errors.Is(pendingErr, os.ErrNotExist):
		return DeletionProof{}, pendingErr
	default:
		proof := DeletionProof{
			Schema: deletionProofSchema, Owner: owner, OwnerKey: owner.Key(),
			Status: DeletionAbsent, AlreadyAbsent: true,
			ObservedAt: store.nowLocked(),
		}
		return proof, proof.Validate()
	}

	bytes, files, err := privateTreeUsage(pendingRoot)
	if err != nil {
		return DeletionProof{}, err
	}
	// Cancellation is observed before the atomic authority rename. Once the
	// owner has moved into the private deleting area, cleanup runs to completion
	// so a client disconnect cannot strand a partially visible owner.
	if err := removePrivateTree(pendingRoot); err != nil {
		return DeletionProof{}, err
	}
	if err := syncDirectory(deletingRoot); err != nil {
		return DeletionProof{}, err
	}
	if _, err := os.Lstat(ownerRoot); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return DeletionProof{}, ErrStoreCorrupt
		}
		return DeletionProof{}, err
	}
	if _, err := os.Lstat(pendingRoot); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return DeletionProof{}, ErrStoreCorrupt
		}
		return DeletionProof{}, err
	}
	proof := DeletionProof{
		Schema: deletionProofSchema, Owner: owner, OwnerKey: owner.Key(),
		Status: DeletionAbsent, RemovedBytes: bytes, RemovedFiles: files,
		ObservedAt: store.nowLocked(),
	}
	return proof, proof.Validate()
}

func (store *Store) recoverPendingDeletionsLocked() error {
	root := filepath.Join(store.root, deletingDirectory)
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if err := validatePrivateDirectory(path, info); err != nil {
			return err
		}
		metadata, err := readOwnerMetadata(path)
		if err != nil {
			return err
		}
		if entry.Name() != metadata.Owner.Key() {
			return ErrStoreCorrupt
		}
		if err := removePrivateTree(path); err != nil {
			return err
		}
	}
	return syncDirectory(root)
}

func privateTreeUsage(root string) (uint64, uint64, error) {
	var bytes, files uint64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return validatePrivateDirectory(path, info)
		}
		if err := validatePrivateRegular(path, info); err != nil {
			return err
		}
		bytes += uint64(info.Size())
		files++
		return nil
	})
	return bytes, files, err
}

func removePrivateTree(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if err := validatePrivateDirectory(root, info); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := removePrivateTree(path); err != nil {
				return err
			}
			continue
		}
		if err := validatePrivateRegular(path, info); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return os.Remove(root)
}
