package hostcap

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/vibe-agi/hideout/internal/hostcap/appopen"
)

const BundleTreeDigestPrefix = "bundle-tree-v1:sha256:"

var (
	ErrBundleTreeLimit   = errors.New("hostcap: bundle tree exceeds a Core limit")
	ErrBundleTreeChanged = errors.New("hostcap: bundle tree changed while measured")
)

type ApplicationRootClass string

const (
	ApplicationRootSystem   ApplicationRootClass = "system-applications"
	ApplicationRootGlobal   ApplicationRootClass = "applications"
	ApplicationRootOperator ApplicationRootClass = "operator-applications"
)

// ApplicationRoot is Core-owned configuration. Community package data names a
// bundle basename only and cannot add to this list.
type ApplicationRoot struct {
	Class ApplicationRootClass
	Path  string
}

func CoreApplicationRoots(home string) []ApplicationRoot {
	return []ApplicationRoot{
		{Class: ApplicationRootSystem, Path: "/System/Applications"},
		{Class: ApplicationRootGlobal, Path: "/Applications"},
		{Class: ApplicationRootOperator, Path: filepath.Join(home, "Applications")},
	}
}

type ApplicationExpectation struct {
	QualifiedAppRef string
	// BundleNames is the package-declared bounded basename set. BundleName is
	// retained only as a 030 migration field; callers must set exactly one form.
	BundleNames            []string
	BundleName             string
	ExecutableRelativePath string
	ExpectedBundleID       string
	ExpectedTeamID         string
}

type IdentityExpectations struct {
	BundleID string
	TeamID   string
}

// SigningObservation contains only facts Core observed from the host signing
// service. Package fields are checked against this structure; they never fill
// it in.
type SigningObservation struct {
	Signed       bool
	Trusted      bool
	TrustAnchor  string
	BundleID     string
	TeamID       string
	CodeIdentity string
}

type SigningObserver func(bundlePath string) (SigningObservation, error)

type AppVerification string

const (
	AppVerificationVerified   AppVerification = "verified"
	AppVerificationUnverified AppVerification = "unverified"
)

type AppOwnerClass string

const (
	AppOwnerRoot     AppOwnerClass = "root"
	AppOwnerOperator AppOwnerClass = "operator"
)

type BundleTreeLimits struct {
	MaxEntries   int
	MaxBytes     int64
	MaxFileBytes int64
	MaxPathBytes int
}

var DefaultBundleTreeLimits = BundleTreeLimits{
	MaxEntries:   25_000,
	MaxBytes:     512 << 20,
	MaxFileBytes: 256 << 20,
	MaxPathBytes: 2_048,
}

type ApplicationIdentityOptions struct {
	Roots          []ApplicationRoot
	ForbiddenRoots []string
	OperatorUID    uint32
	ObserveSigning SigningObserver
	DigestLimits   BundleTreeLimits
}

// ObservedApplicationIdentity includes host-local paths needed by the launch
// provider. Public/guest models must use a redacted projection instead.
type ObservedApplicationIdentity struct {
	QualifiedAppRef        string
	RootClass              ApplicationRootClass
	BundlePath             string
	ExecutablePath         string
	ExecutableRelativePath string
	ExecutableCodeIdentity string
	CanonicalPathDigest    string
	BundleID               string
	TeamID                 string
	CodeIdentity           string
	TrustAnchor            string
	ContentDigest          string
	Verification           AppVerification
	OwnerClass             AppOwnerClass
	ObservedAt             time.Time
	identityDigest         string
}

// IdentityDigest returns the Core-observed identity fingerprint. It is safe to
// persist in enablement and audit evidence; it contains no host path.
func (i ObservedApplicationIdentity) IdentityDigest() string { return i.identityDigest }

// SafetyIdentity projects only signing facts used by the closed Core safety
// catalog. Package data cannot populate these fields.
func (i ObservedApplicationIdentity) SafetyIdentity(platform Platform) appopen.SafetyIdentity {
	return appopen.SafetyIdentity{
		Signed: i.Verification == AppVerificationVerified, Platform: string(platform),
		BundleID: i.BundleID, TeamID: i.TeamID, CodeIdentity: i.CodeIdentity,
		ExecutableRelativePath: i.ExecutableRelativePath, ExecutableCodeIdentity: i.ExecutableCodeIdentity,
	}
}

func ResolveApplicationIdentity(expectation ApplicationExpectation, opts ApplicationIdentityOptions) (ObservedApplicationIdentity, error) {
	if err := validateApplicationExpectation(expectation); err != nil {
		return ObservedApplicationIdentity{}, identityError(err.Error())
	}
	if len(opts.Roots) == 0 || opts.ObserveSigning == nil {
		return ObservedApplicationIdentity{}, identityError("application roots and a Core signing observer are required")
	}
	if opts.DigestLimits == (BundleTreeLimits{}) {
		opts.DigestLimits = DefaultBundleTreeLimits
	}
	if err := validateBundleTreeLimits(opts.DigestLimits); err != nil {
		return ObservedApplicationIdentity{}, identityError(err.Error())
	}

	names := applicationBundleNames(expectation)
	type candidateIdentity struct {
		configuredRoot ApplicationRoot
		bundlePath     string
		executablePath string
		bundleInfo     os.FileInfo
		executableInfo os.FileInfo
	}
	var candidates []candidateIdentity
	for _, configuredRoot := range opts.Roots {
		if _, err := os.Lstat(configuredRoot.Path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return ObservedApplicationIdentity{}, identityError("application root cannot be inspected")
		}
		root, err := validateApplicationRoot(configuredRoot, opts.OperatorUID)
		if err != nil {
			return ObservedApplicationIdentity{}, identityError(err.Error())
		}
		for _, name := range names {
			candidate := filepath.Join(root, name)
			if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				return ObservedApplicationIdentity{}, identityError("application bundle cannot be inspected")
			}
			bundlePath, err := filepath.EvalSymlinks(candidate)
			if err != nil || !pathWithin(root, bundlePath) {
				return ObservedApplicationIdentity{}, identityError("application bundle escapes its Core-owned root")
			}
			bundleInfo, err := os.Stat(bundlePath)
			if err != nil || !bundleInfo.IsDir() {
				return ObservedApplicationIdentity{}, identityError("application bundle is not a directory")
			}
			if err := rejectForbiddenOverlap(bundlePath, opts.ForbiddenRoots); err != nil {
				return ObservedApplicationIdentity{}, identityError(err.Error())
			}

			executableJoined := filepath.Join(bundlePath, filepath.FromSlash(expectation.ExecutableRelativePath))
			executablePath, err := filepath.EvalSymlinks(executableJoined)
			if err != nil || !pathWithin(bundlePath, executablePath) {
				return ObservedApplicationIdentity{}, identityError("application executable escapes its bundle")
			}
			if err := validateOwnedPathChain(root, bundlePath, executablePath, opts.OperatorUID); err != nil {
				return ObservedApplicationIdentity{}, identityError(err.Error())
			}
			executableInfo, err := os.Stat(executablePath)
			if err != nil || !executableInfo.Mode().IsRegular() || executableInfo.Mode().Perm()&0o111 == 0 {
				return ObservedApplicationIdentity{}, identityError("application executable is not a regular executable file")
			}
			candidates = append(candidates, candidateIdentity{
				configuredRoot: configuredRoot,
				bundlePath:     bundlePath,
				executablePath: executablePath,
				bundleInfo:     bundleInfo,
				executableInfo: executableInfo,
			})
		}
	}
	if len(candidates) == 0 {
		return ObservedApplicationIdentity{}, &Error{Code: CodeAppAbsent, Reason: "application bundle was not found below an allowed application root"}
	}
	if len(candidates) != 1 {
		return ObservedApplicationIdentity{}, identityError("application bundle selection is ambiguous across allowed roots or names")
	}
	selected := candidates[0]
	executableCodeIdentity, err := digestExecutableIdentity(selected.executablePath, selected.executableInfo, opts.DigestLimits.MaxFileBytes)
	if err != nil {
		return ObservedApplicationIdentity{}, identityError("application executable identity could not be measured")
	}
	signing, err := opts.ObserveSigning(selected.bundlePath)
	if err != nil {
		return ObservedApplicationIdentity{}, identityError("Core could not observe a valid application identity")
	}
	if err := ValidateSigningExpectations(signing, IdentityExpectations{BundleID: expectation.ExpectedBundleID, TeamID: expectation.ExpectedTeamID}); err != nil {
		return ObservedApplicationIdentity{}, identityError(err.Error())
	}

	verification := AppVerificationVerified
	contentDigest := ""
	if !signing.Signed || !signing.Trusted || signing.BundleID == "" || signing.TeamID == "" || signing.CodeIdentity == "" {
		verification = AppVerificationUnverified
		contentDigest, err = digestBundleTree(selected.bundlePath, opts.DigestLimits, nil)
		if err != nil {
			return ObservedApplicationIdentity{}, identityError("unsigned application content could not be measured: " + err.Error())
		}
	}
	ownerClass, err := classifyOwner(selected.bundleInfo, opts.OperatorUID)
	if err != nil {
		return ObservedApplicationIdentity{}, identityError(err.Error())
	}
	identityDigest, err := applicationIdentityDigest(selected.bundlePath, selected.executablePath, selected.bundleInfo, selected.executableInfo, signing, executableCodeIdentity, contentDigest)
	if err != nil {
		return ObservedApplicationIdentity{}, identityError(err.Error())
	}
	return ObservedApplicationIdentity{
		QualifiedAppRef:        expectation.QualifiedAppRef,
		RootClass:              selected.configuredRoot.Class,
		BundlePath:             selected.bundlePath,
		ExecutablePath:         selected.executablePath,
		ExecutableRelativePath: filepath.ToSlash(expectation.ExecutableRelativePath),
		ExecutableCodeIdentity: executableCodeIdentity,
		CanonicalPathDigest:    sha256String(selected.bundlePath),
		BundleID:               signing.BundleID,
		TeamID:                 signing.TeamID,
		CodeIdentity:           signing.CodeIdentity,
		TrustAnchor:            signing.TrustAnchor,
		ContentDigest:          contentDigest,
		Verification:           verification,
		OwnerClass:             ownerClass,
		ObservedAt:             time.Now().UTC(),
		identityDigest:         identityDigest,
	}, nil
}

func RevalidateApplicationIdentity(expectation ApplicationExpectation, previous ObservedApplicationIdentity, opts ApplicationIdentityOptions) (ObservedApplicationIdentity, error) {
	current, err := ResolveApplicationIdentity(expectation, opts)
	if err != nil {
		if CodeOf(err) == CodeAppAbsent {
			return ObservedApplicationIdentity{}, identityError("application disappeared before launch")
		}
		return ObservedApplicationIdentity{}, err
	}
	if previous.QualifiedAppRef != current.QualifiedAppRef ||
		previous.RootClass != current.RootClass ||
		previous.BundlePath != current.BundlePath ||
		previous.ExecutablePath != current.ExecutablePath ||
		previous.ExecutableRelativePath != current.ExecutableRelativePath ||
		previous.ExecutableCodeIdentity != current.ExecutableCodeIdentity ||
		previous.Verification != current.Verification ||
		previous.BundleID != current.BundleID ||
		previous.TeamID != current.TeamID ||
		previous.CodeIdentity != current.CodeIdentity ||
		previous.TrustAnchor != current.TrustAnchor ||
		previous.ContentDigest != current.ContentDigest ||
		previous.identityDigest != current.identityDigest {
		return ObservedApplicationIdentity{}, identityError("application identity changed before launch")
	}
	return current, nil
}

func ValidateSigningExpectations(observed SigningObservation, expected IdentityExpectations) error {
	if (expected.BundleID != "" || expected.TeamID != "") && !observed.Signed {
		return errors.New("package identity expectations cannot authenticate an unsigned application")
	}
	if expected.BundleID != "" && expected.BundleID != observed.BundleID {
		return errors.New("observed bundle identifier does not match the package constraint")
	}
	if expected.TeamID != "" && expected.TeamID != observed.TeamID {
		return errors.New("observed team identifier does not match the package constraint")
	}
	return nil
}

func validateApplicationExpectation(expectation ApplicationExpectation) error {
	if strings.TrimSpace(expectation.QualifiedAppRef) == "" || len(expectation.QualifiedAppRef) > 256 || identityTextHasControl(expectation.QualifiedAppRef) {
		return errors.New("qualified application identity is required")
	}
	if len(expectation.BundleNames) != 0 && expectation.BundleName != "" {
		return errors.New("application bundle names must use one candidate representation")
	}
	names := applicationBundleNames(expectation)
	if len(names) == 0 || len(names) > 16 {
		return errors.New("application bundle requires 1-16 basenames")
	}
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" || len(name) > 255 || identityTextHasControl(name) || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\\$`) {
			return errors.New("application bundle must be a basename")
		}
		if seen[name] {
			return errors.New("application bundle basenames must be unique")
		}
		seen[name] = true
	}
	cleanExecutable := filepath.Clean(filepath.FromSlash(expectation.ExecutableRelativePath))
	if expectation.ExecutableRelativePath == "" || len(expectation.ExecutableRelativePath) > 1_024 || identityTextHasControl(expectation.ExecutableRelativePath) || cleanExecutable == "." || cleanExecutable == ".." || filepath.IsAbs(cleanExecutable) || strings.HasPrefix(cleanExecutable, ".."+string(filepath.Separator)) {
		return errors.New("application executable must be a clean relative path")
	}
	if len(expectation.ExpectedBundleID) > 256 || len(expectation.ExpectedTeamID) > 256 || identityTextHasControl(expectation.ExpectedBundleID) || identityTextHasControl(expectation.ExpectedTeamID) {
		return errors.New("application identity expectation exceeds its bound")
	}
	return nil
}

func identityTextHasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func applicationBundleNames(expectation ApplicationExpectation) []string {
	if len(expectation.BundleNames) != 0 {
		return expectation.BundleNames
	}
	if expectation.BundleName != "" {
		return []string{expectation.BundleName}
	}
	return nil
}

func validateApplicationRoot(root ApplicationRoot, operatorUID uint32) (string, error) {
	if root.Class != ApplicationRootSystem && root.Class != ApplicationRootGlobal && root.Class != ApplicationRootOperator {
		return "", errors.New("unknown application root class")
	}
	if !filepath.IsAbs(root.Path) {
		return "", errors.New("application root must be absolute")
	}
	canonical, err := filepath.EvalSymlinks(root.Path)
	if err != nil {
		return "", errors.New("application root cannot be canonicalized")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", errors.New("application root is not a directory")
	}
	uid, ok := ownerUID(info)
	if !ok || (uid != 0 && uid != operatorUID) {
		return "", errors.New("application root has an unsafe owner")
	}
	// /Applications is root:admin 0775 on supported macOS hosts. Core treats
	// that fixed root as a trust anchor, while still rejecting world-writable
	// roots and every writable descendant in the selected bundle chain.
	if info.Mode().Perm()&0o002 != 0 || (info.Mode().Perm()&0o020 != 0 && !(root.Class == ApplicationRootGlobal && uid == 0)) {
		return "", errors.New("application root has an unsafe write posture")
	}
	return canonical, nil
}

func validateOwnedPathChain(root, bundle, executable string, operatorUID uint32) error {
	paths := []string{bundle}
	rel, err := filepath.Rel(bundle, executable)
	if err != nil {
		return errors.New("application executable path is unavailable")
	}
	current := bundle
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		paths = append(paths, current)
	}
	for _, path := range paths {
		if !pathWithin(root, path) {
			return errors.New("application path escapes its root")
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("application path changed or is symlinked after canonicalization")
		}
		if info.Mode().Perm()&0o022 != 0 {
			return errors.New("application path has a group/world-writable ancestor")
		}
		uid, ok := ownerUID(info)
		if !ok || (uid != 0 && uid != operatorUID) {
			return errors.New("application path has an unsafe owner")
		}
	}
	return nil
}

func rejectForbiddenOverlap(bundle string, forbidden []string) error {
	for _, raw := range forbidden {
		if !filepath.IsAbs(raw) {
			return errors.New("forbidden application-overlap root must be absolute")
		}
		candidate := filepath.Clean(raw)
		if canonical, err := filepath.EvalSymlinks(raw); err == nil {
			candidate = canonical
		}
		if pathWithin(candidate, bundle) || pathWithin(bundle, candidate) {
			return errors.New("application bundle overlaps guest-writable or control state")
		}
	}
	return nil
}

func classifyOwner(info os.FileInfo, operatorUID uint32) (AppOwnerClass, error) {
	uid, ok := ownerUID(info)
	if !ok {
		return "", errors.New("application owner cannot be observed")
	}
	switch uid {
	case 0:
		return AppOwnerRoot, nil
	case operatorUID:
		return AppOwnerOperator, nil
	default:
		return "", errors.New("application owner is neither root nor the operator")
	}
}

func ownerUID(info os.FileInfo) (uint32, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Uid, true
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func identityError(reason string) error {
	return &Error{Code: CodeAppIdentityDrift, Reason: reason}
}

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", sum[:])
}

func applicationIdentityDigest(bundle, executable string, bundleInfo, executableInfo os.FileInfo, signing SigningObservation, executableCodeIdentity, contentDigest string) (string, error) {
	bundleIdentity, err := identityOf(bundleInfo)
	if err != nil {
		return "", err
	}
	executableIdentity, err := identityOf(executableInfo)
	if err != nil {
		return "", err
	}
	return sha256String(strings.Join([]string{
		bundle, executable, bundleIdentity.key(), executableIdentity.key(),
		fmt.Sprint(signing.Signed), fmt.Sprint(signing.Trusted), signing.TrustAnchor,
		signing.BundleID, signing.TeamID, signing.CodeIdentity, executableCodeIdentity, contentDigest,
	}, "\x00")), nil
}

func digestExecutableIdentity(path string, expected os.FileInfo, maxBytes int64) (string, error) {
	if maxBytes <= 0 || expected == nil || expected.Size() < 0 || expected.Size() > maxBytes {
		return "", errors.New("executable exceeds identity digest bound")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(expected, opened) || opened.Size() != expected.Size() {
		return "", ErrBundleTreeChanged
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maxBytes+1))
	if err != nil || written != opened.Size() || written > maxBytes {
		return "", ErrBundleTreeChanged
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || opened.Size() != after.Size() || opened.Mode() != after.Mode() || opened.ModTime() != after.ModTime() {
		return "", ErrBundleTreeChanged
	}
	return "sha256:" + fmt.Sprintf("%x", hash.Sum(nil)), nil
}

type treeEntry struct {
	path       string
	kind       byte
	linkTarget string
	identity   fileIdentity
}

type fileIdentity struct {
	mode    fs.FileMode
	size    int64
	modTime int64
	device  uint64
	inode   uint64
}

func (i fileIdentity) key() string {
	return fmt.Sprintf("%o:%d:%d:%d:%d", uint32(i.mode), i.size, i.modTime, i.device, i.inode)
}

func identityOf(info os.FileInfo) (fileIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdentity{}, errors.New("filesystem identity is unavailable")
	}
	return fileIdentity{
		mode: info.Mode(), size: info.Size(), modTime: info.ModTime().UnixNano(),
		device: uint64(stat.Dev), inode: uint64(stat.Ino),
	}, nil
}

func digestBundleTree(bundle string, limits BundleTreeLimits, beforeRead func(relative string)) (string, error) {
	if err := validateBundleTreeLimits(limits); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(bundle)
	if err != nil {
		return "", err
	}
	defer root.Close()

	before, err := snapshotBundleTree(root, limits)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, entry := range before {
		writeDigestField(h, entry.path)
		_, _ = h.Write([]byte{entry.kind})
		writeDigestField(h, fmt.Sprintf("%o", uint32(entry.identity.mode.Perm())))
		switch entry.kind {
		case 'l':
			writeDigestField(h, entry.linkTarget)
		case 'f':
			writeDigestField(h, fmt.Sprint(entry.identity.size))
			if beforeRead != nil {
				beforeRead(entry.path)
			}
			file, err := root.Open(entry.path)
			if err != nil {
				return "", ErrBundleTreeChanged
			}
			start, err := file.Stat()
			if err != nil || !start.Mode().IsRegular() {
				_ = file.Close()
				return "", ErrBundleTreeChanged
			}
			startIdentity, err := identityOf(start)
			if err != nil || startIdentity != entry.identity {
				_ = file.Close()
				return "", ErrBundleTreeChanged
			}
			read, err := io.CopyN(h, file, entry.identity.size)
			if err != nil && !errors.Is(err, io.EOF) {
				_ = file.Close()
				return "", err
			}
			if read != entry.identity.size {
				_ = file.Close()
				return "", ErrBundleTreeChanged
			}
			var extra [1]byte
			if n, _ := file.Read(extra[:]); n != 0 {
				_ = file.Close()
				return "", ErrBundleTreeChanged
			}
			end, err := file.Stat()
			_ = file.Close()
			if err != nil {
				return "", ErrBundleTreeChanged
			}
			endIdentity, err := identityOf(end)
			if err != nil || endIdentity != entry.identity {
				return "", ErrBundleTreeChanged
			}
		}
	}
	after, err := snapshotBundleTree(root, limits)
	if err != nil || !slices.EqualFunc(before, after, func(a, b treeEntry) bool { return a == b }) {
		return "", ErrBundleTreeChanged
	}
	return fmt.Sprintf("%s%x", BundleTreeDigestPrefix, h.Sum(nil)), nil
}

func snapshotBundleTree(root *os.Root, limits BundleTreeLimits) ([]treeEntry, error) {
	entries := make([]treeEntry, 0, 128)
	var totalBytes int64
	err := fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if len(entries) >= limits.MaxEntries || len(path) > limits.MaxPathBytes {
			return ErrBundleTreeLimit
		}
		info, err := root.Lstat(path)
		if err != nil {
			return ErrBundleTreeChanged
		}
		identity, err := identityOf(info)
		if err != nil {
			return err
		}
		entry := treeEntry{path: filepath.ToSlash(path), identity: identity}
		switch {
		case info.IsDir():
			entry.kind = 'd'
		case info.Mode().IsRegular():
			entry.kind = 'f'
			if info.Size() < 0 || info.Size() > limits.MaxFileBytes || totalBytes > limits.MaxBytes-info.Size() {
				return ErrBundleTreeLimit
			}
			totalBytes += info.Size()
		case info.Mode()&os.ModeSymlink != 0:
			entry.kind = 'l'
			target, err := root.Readlink(path)
			if err != nil || filepath.IsAbs(target) {
				return errors.New("hostcap: unsupported or absolute bundle symlink")
			}
			entry.linkTarget = filepath.ToSlash(target)
			// Root.Stat follows the link but refuses any target outside the root.
			if _, err := root.Stat(path); err != nil {
				return errors.New("hostcap: bundle symlink escapes or is unresolved")
			}
		default:
			return errors.New("hostcap: unsupported special file in application bundle")
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func validateBundleTreeLimits(limits BundleTreeLimits) error {
	if limits.MaxEntries < 1 || limits.MaxBytes < 1 || limits.MaxFileBytes < 1 || limits.MaxFileBytes > limits.MaxBytes || limits.MaxPathBytes < 1 {
		return errors.New("invalid bundle-tree limits")
	}
	return nil
}

func writeDigestField(w io.Writer, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = w.Write(length[:])
	_, _ = io.WriteString(w, value)
}
