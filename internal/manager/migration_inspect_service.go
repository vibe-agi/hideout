package manager

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"

	"github.com/vibe-agi/hideout/internal/migration"
)

type MigrationBundleFileProbe struct {
	BundleID      migration.BundleID         `json:"bundleId"`
	FormatVersion uint16                     `json:"formatVersion"`
	CreatedAt     string                     `json:"createdAt"`
	EncodedBytes  uint64                     `json:"encodedBytes"`
	BundleFile    MigrationBundleFileBinding `json:"bundleFile"`
}

type MigrationReadOnlyInspectRequest struct {
	BundlePath        string
	ExpectedFile      MigrationBundleFileBinding
	SecretInputHandle string
	ClientBinding     string
}

type MigrationReadOnlyInspection struct {
	Binding    migration.BundleBinding             `json:"binding"`
	BundleFile MigrationBundleFileBinding          `json:"bundleFile"`
	Inventory  MigrationBundleInspectionProjection `json:"inventory"`
}

// MigrationInspectionService owns the read-only file/key boundary. It has no
// Manager store, backend, Keychain, profile, environment, or staging authority.
type MigrationInspectionService struct {
	SecretInputs *MigrationSecretInputStore
	Cache        *MigrationInspectionCache
}

// ProbeMigrationBundleFile returns the stable public facts needed to bind a
// one-shot inspect handle before the passphrase prompt. It never derives a key.
func ProbeMigrationBundleFile(path string) (MigrationBundleFileProbe, error) {
	file, binding, public, err := openAndBindMigrationBundleFile(path)
	if err != nil {
		return MigrationBundleFileProbe{}, err
	}
	if closeErr := file.Close(); closeErr != nil {
		return MigrationBundleFileProbe{}, closeErr
	}
	return MigrationBundleFileProbe{
		BundleID: public.BundleID, FormatVersion: public.FormatVersion,
		CreatedAt: public.CreatedAt, EncodedBytes: public.EncodedBytes,
		BundleFile: binding,
	}, nil
}

// probeMigrationBundleHeaderFile binds either a sealed bundle or an
// in-progress export partial. It is intentionally package-private: only the
// secret-input boundary needs to issue resume handles for unsealed artifacts.
func probeMigrationBundleHeaderFile(
	path string,
	requireSealed bool,
) (*os.File, MigrationBundleFileBinding, migration.PublicBundleInspection, error) {
	if !validMigrationAbsolutePath(path) {
		return nil, MigrationBundleFileBinding{}, migration.PublicBundleInspection{},
			ErrMigrationRequestInvalid
	}
	file, err := openMigrationBundleNoFollow(path)
	if err != nil {
		return nil, MigrationBundleFileBinding{}, migration.PublicBundleInspection{}, err
	}
	binding, public, err := bindOpenMigrationBundleHeaderFile(path, file, requireSealed)
	if err != nil {
		_ = file.Close()
		return nil, MigrationBundleFileBinding{}, migration.PublicBundleInspection{}, err
	}
	return file, binding, public, nil
}

func authenticateMigrationBundleHeaderFile(
	path string,
	expected MigrationBundleFileBinding,
	passphrase []byte,
	requireSealed bool,
) (migration.PublicBundleInspection, error) {
	file, binding, public, err := probeMigrationBundleHeaderFile(path, requireSealed)
	if err != nil {
		return migration.PublicBundleInspection{}, err
	}
	defer file.Close()
	if binding != expected {
		return migration.PublicBundleInspection{}, migration.ErrBundleChanged
	}
	authenticated, err := migration.AuthenticateBundleHeader(
		file, binding.Size, passphrase,
	)
	if err != nil {
		return migration.PublicBundleInspection{}, err
	}
	after, afterPublic, err := bindOpenMigrationBundleHeaderFile(
		path, file, requireSealed,
	)
	if err != nil {
		return migration.PublicBundleInspection{}, err
	}
	if after != binding || afterPublic != public || authenticated != public {
		return migration.PublicBundleInspection{}, migration.ErrBundleChanged
	}
	return authenticated, nil
}

// Inspect consumes one inspect-purpose handle, authenticates every frame, and
// returns only the sealed binding plus the shared secret-free inventory.
func (service MigrationInspectionService) Inspect(
	ctx context.Context,
	request MigrationReadOnlyInspectRequest,
) (MigrationReadOnlyInspection, error) {
	if ctx == nil || service.SecretInputs == nil ||
		!validMigrationAbsolutePath(request.BundlePath) ||
		request.ExpectedFile.Validate() != nil ||
		!migrationSecretHandlePattern.MatchString(request.SecretInputHandle) ||
		!validClientBinding(request.ClientBinding) {
		return MigrationReadOnlyInspection{}, ErrMigrationRequestInvalid
	}
	file, binding, public, err := openAndBindMigrationBundleFile(request.BundlePath)
	if err != nil {
		return MigrationReadOnlyInspection{}, err
	}
	defer file.Close()
	if binding != request.ExpectedFile {
		return MigrationReadOnlyInspection{}, migration.ErrBundleChanged
	}

	var result MigrationReadOnlyInspection
	err = service.SecretInputs.Consume(MigrationSecretInputUse{
		Handle: request.SecretInputHandle, Purpose: MigrationSecretPurposeInspect,
		ClientBinding: request.ClientBinding, BundleID: public.BundleID,
		BundleFile: &binding,
	}, func(passphrase []byte) error {
		sealed, inspectErr := migration.InspectSealedBundle(
			ctx, file, binding.Size, passphrase,
		)
		if inspectErr != nil {
			return inspectErr
		}
		if sealed.Binding.BundleID != public.BundleID ||
			sealed.Binding.FormatVersion != public.FormatVersion ||
			sealed.CreatedAt != public.CreatedAt ||
			sealed.Summary.EncodedBytes != public.EncodedBytes {
			return migration.ErrBundleChanged
		}
		after, afterPublic, bindErr := bindOpenMigrationBundleFile(
			request.BundlePath, file,
		)
		if bindErr != nil {
			return bindErr
		}
		if after != binding || afterPublic != public {
			return migration.ErrBundleChanged
		}
		inventory, projectionErr := ProjectMigrationBundleInspection(sealed)
		if projectionErr != nil {
			return projectionErr
		}
		if service.Cache != nil {
			if cacheErr := service.Cache.Put(sealed, binding); cacheErr != nil {
				return cacheErr
			}
		}
		result = MigrationReadOnlyInspection{
			Binding: sealed.Binding, BundleFile: binding, Inventory: inventory,
		}
		return nil
	})
	if err != nil {
		return MigrationReadOnlyInspection{}, err
	}
	return result, nil
}

// CachedMigrationBundleSource revalidates an exact previously authenticated
// file without using its passphrase. The import-purpose handle stays unconsumed
// for the materialization worker.
type CachedMigrationBundleSource struct {
	SecretInputs *MigrationSecretInputStore
	Cache        *MigrationInspectionCache
}

var _ MigrationBundleSource = CachedMigrationBundleSource{}

func (source CachedMigrationBundleSource) InspectMigrationBundle(
	ctx context.Context,
	request MigrationBundleInspectRequest,
) (MigrationBundleInspection, error) {
	if ctx == nil || source.SecretInputs == nil || source.Cache == nil ||
		!validMigrationAbsolutePath(request.BundlePath) ||
		validateMigrationBundleBinding(request.ExpectedBinding) != nil ||
		!migrationSecretHandlePattern.MatchString(request.SecretInputHandle) ||
		!validClientBinding(request.ClientBinding) {
		return MigrationBundleInspection{}, ErrMigrationRequestInvalid
	}
	file, binding, public, err := openAndBindMigrationBundleFile(request.BundlePath)
	if err != nil {
		return MigrationBundleInspection{}, err
	}
	defer file.Close()
	cached, err := source.Cache.Get(request.ExpectedBinding, binding)
	if err != nil {
		return MigrationBundleInspection{}, err
	}
	if public.BundleID != request.ExpectedBinding.BundleID ||
		public.FormatVersion != request.ExpectedBinding.FormatVersion {
		return MigrationBundleInspection{}, migration.ErrBundleChanged
	}
	if err := migration.VerifySealedBundleFile(
		ctx, file, binding.Size, request.ExpectedBinding,
	); err != nil {
		return MigrationBundleInspection{}, err
	}
	after, afterPublic, err := bindOpenMigrationBundleFile(request.BundlePath, file)
	if err != nil {
		return MigrationBundleInspection{}, err
	}
	if after != binding || afterPublic != public {
		return MigrationBundleInspection{}, migration.ErrBundleChanged
	}
	handle, err := source.SecretInputs.Lookup(MigrationSecretInputLookup{
		Handle: request.SecretInputHandle, Purpose: MigrationSecretPurposeImport,
		ClientBinding: request.ClientBinding, BundleFile: &binding,
	})
	if err != nil {
		return MigrationBundleInspection{}, err
	}
	if handle.BundleID != request.ExpectedBinding.BundleID ||
		cached.Binding != request.ExpectedBinding {
		return MigrationBundleInspection{}, ErrMigrationSecretInputMismatch
	}
	return MigrationBundleInspection{
		Binding: request.ExpectedBinding, BundleFile: binding,
		Manifest: cloneMigrationManifest(cached.Manifest),
	}, nil
}

func openAndBindMigrationBundleFile(
	path string,
) (*os.File, MigrationBundleFileBinding, migration.PublicBundleInspection, error) {
	if !validMigrationAbsolutePath(path) {
		return nil, MigrationBundleFileBinding{}, migration.PublicBundleInspection{},
			ErrMigrationRequestInvalid
	}
	file, err := openMigrationBundleNoFollow(path)
	if err != nil {
		return nil, MigrationBundleFileBinding{}, migration.PublicBundleInspection{}, err
	}
	binding, public, err := bindOpenMigrationBundleFile(path, file)
	if err != nil {
		_ = file.Close()
		return nil, MigrationBundleFileBinding{}, migration.PublicBundleInspection{}, err
	}
	return file, binding, public, nil
}

func bindOpenMigrationBundleFile(
	path string,
	file *os.File,
) (MigrationBundleFileBinding, migration.PublicBundleInspection, error) {
	return bindOpenMigrationBundleHeaderFile(path, file, true)
}

func bindOpenMigrationBundleHeaderFile(
	path string,
	file *os.File,
	requireSealed bool,
) (MigrationBundleFileBinding, migration.PublicBundleInspection, error) {
	if file == nil || !validMigrationAbsolutePath(path) {
		return MigrationBundleFileBinding{}, migration.PublicBundleInspection{},
			ErrMigrationRequestInvalid
	}
	info, err := file.Stat()
	if err != nil {
		return MigrationBundleFileBinding{}, migration.PublicBundleInspection{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 ||
		info.Size() <= 0 || info.ModTime().UnixNano() <= 0 {
		return MigrationBundleFileBinding{}, migration.PublicBundleInspection{},
			fmt.Errorf("%w: migration bundle file mode or shape is unsafe", migration.ErrInvalidBundle)
	}
	device, inode, err := migrationBundleFileDeviceInode(file)
	if err != nil || device == 0 || inode == 0 {
		return MigrationBundleFileBinding{}, migration.PublicBundleInspection{},
			fmt.Errorf("%w: migration bundle file identity is unavailable", migration.ErrInvalidBundle)
	}
	var public migration.PublicBundleInspection
	if requireSealed {
		public, err = migration.InspectPublicBundle(file, info.Size())
	} else {
		public, err = migration.InspectBundleHeader(file, info.Size())
	}
	if err != nil {
		return MigrationBundleFileBinding{}, migration.PublicBundleInspection{}, err
	}
	pathDigest := sha256.Sum256([]byte("hideout.migration.bundle-path/v1\x00" + path))
	binding := MigrationBundleFileBinding{
		PathDigest:   migration.Digest(fmt.Sprintf("sha256:%x", pathDigest[:])),
		HeaderDigest: public.HeaderDigest, Device: device, Inode: inode,
		Size: info.Size(), ModifiedUnixNano: info.ModTime().UnixNano(),
	}
	if err := binding.Validate(); err != nil {
		return MigrationBundleFileBinding{}, migration.PublicBundleInspection{}, err
	}
	return binding, public, nil
}
