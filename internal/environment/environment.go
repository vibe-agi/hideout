package environment

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	recordFile = "environment.json"
	lockFile   = ".lock"
	version    = "hideout.environment/v2"

	// BuiltinBaseImage is the explicit default base image declaration carried
	// by the shipped default profile. It replaces the previous backend
	// hardcode: every environment records a declaration, with no special
	// "undeclared" case.
	BuiltinBaseImage = "template:_images/ubuntu-lts"

	// StatusUnsupportedVersion marks a listing row for a record written by a
	// previous environment model. Only the record id, path, and version of
	// such rows are trusted; every other field is zeroed.
	StatusUnsupportedVersion = "unsupported-version"

	// StatusCreated marks a record whose guest has never booted. It is not a
	// stop target: there is no instance to stop yet.
	StatusCreated = "created"

	reservedName  = "default"
	maxNameLength = 64
)

// ErrUnsupportedVersion is returned when an operation touches an environment
// record written by a previous environment model. Records are never migrated;
// the operator cleans and recreates.
var ErrUnsupportedVersion = errors.New("environment model changed: this record predates the named-environment model; run 'hideout clean' for it and recreate the environment")

type Store struct {
	Root string
}

type Lock struct {
	file *os.File
}

type Spec struct {
	Name                 string
	AutoNamed            bool
	ImageRef             string
	Runtime              *RuntimeProvenance
	Profile              string
	Backend              string
	BackendConfigVersion string
	Workspace            string
	GuestWorkspace       string
	ProfileID            string
	IdentityID           string
	User                 string
	Hostname             string
	InstanceName         string
}

type Record struct {
	Version              string             `json:"version"`
	ID                   string             `json:"id"`
	Name                 string             `json:"name"`
	AutoNamed            bool               `json:"autoNamed,omitempty"`
	ImageRef             string             `json:"imageRef"`
	Runtime              *RuntimeProvenance `json:"runtime,omitempty"`
	Profile              string             `json:"profile"`
	Backend              string             `json:"backend"`
	BackendConfigVersion string             `json:"backendConfigVersion,omitempty"`
	Workspace            string             `json:"workspace"`
	GuestWorkspace       string             `json:"guestWorkspace"`
	ProfileID            string             `json:"profileId,omitempty"`
	IdentityID           string             `json:"identityId,omitempty"`
	User                 string             `json:"user,omitempty"`
	Hostname             string             `json:"hostname,omitempty"`
	InstanceName         string             `json:"instanceName,omitempty"`
	Status               string             `json:"status"`
	LastSessionID        string             `json:"lastSessionId,omitempty"`
	LastCommand          string             `json:"lastCommand,omitempty"`
	CreatedAt            time.Time          `json:"createdAt"`
	LastStartedAt        time.Time          `json:"lastStartedAt,omitempty"`
	LastEndedAt          time.Time          `json:"lastEndedAt,omitempty"`
}

type RuntimeProvenance struct {
	Family                 string `json:"family"`
	Revision               string `json:"revision"`
	CatalogRelease         string `json:"catalogRelease"`
	ContractID             string `json:"contractId"`
	ContractDigest         string `json:"contractDigest"`
	ArtifactLocation       string `json:"artifactLocation"`
	ArtifactSHA256         string `json:"artifactSHA256"`
	PackageInventoryDigest string `json:"packageInventoryDigest"`
	DownloadBytes          int64  `json:"downloadBytes"`
	VirtualBytes           int64  `json:"virtualBytes"`
	HostOS                 string `json:"hostOS"`
	HostArch               string `json:"hostArch"`
	GuestArch              string `json:"guestArch"`
	Maturity               string `json:"maturity"`
}

func (p RuntimeProvenance) ImageRef() string {
	return p.ArtifactLocation + "#sha256:" + p.ArtifactSHA256
}

func (p RuntimeProvenance) Validate() error {
	for label, value := range map[string]string{
		"family": p.Family, "revision": p.Revision, "catalogRelease": p.CatalogRelease,
		"contractId": p.ContractID,
	} {
		if strings.TrimSpace(value) == "" || len(value) > 64 || strings.TrimSpace(value) != value {
			return fmt.Errorf("runtime provenance %s is required and bounded", label)
		}
	}
	if !strings.HasPrefix(p.ContractDigest, "sha256:") || !isLowerHex(strings.TrimPrefix(p.ContractDigest, "sha256:"), 64) {
		return errors.New("runtime provenance contractDigest is invalid")
	}
	if !isLowerHex(p.ArtifactSHA256, 64) {
		return errors.New("runtime provenance artifactSHA256 is invalid")
	}
	if !strings.HasPrefix(p.PackageInventoryDigest, "sha256:") || !isLowerHex(strings.TrimPrefix(p.PackageInventoryDigest, "sha256:"), 64) {
		return errors.New("runtime provenance packageInventoryDigest is invalid")
	}
	if p.DownloadBytes <= 0 || p.DownloadBytes > 4<<30 || p.VirtualBytes <= 0 || p.VirtualBytes > 16<<30 {
		return errors.New("runtime provenance artifact sizes are invalid")
	}
	u, err := url.Parse(p.ArtifactLocation)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || filepath.Ext(u.Path) != ".qcow2" {
		return errors.New("runtime provenance artifactLocation must be a credential-free, query-free HTTPS qcow2 URL")
	}
	guestForHost := map[string]string{"arm64": "aarch64", "amd64": "x86_64"}
	if (p.HostOS != "darwin" && p.HostOS != "linux") || guestForHost[p.HostArch] != p.GuestArch {
		return fmt.Errorf("runtime provenance host/guest tuple %s/%s/%s is unsupported", p.HostOS, p.HostArch, p.GuestArch)
	}
	if p.Maturity != "preview" {
		return fmt.Errorf("runtime provenance maturity %q is unsupported", p.Maturity)
	}
	if _, err := ParseImageDeclaration(p.ImageRef()); err != nil {
		return fmt.Errorf("runtime provenance image: %w", err)
	}
	return nil
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// ValidateName checks an environment name: conservative charset, bounded
// length, and the reserved `default` name (any letter case) rejected.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("environment name is required")
	}
	if strings.EqualFold(name, reservedName) {
		return fmt.Errorf("environment name %q is reserved for the shared environment", name)
	}
	if len(name) > maxNameLength {
		return fmt.Errorf("environment name is too long (max %d characters)", maxNameLength)
	}
	first := rune(name[0])
	if !isNameAlnum(first) {
		return fmt.Errorf("environment name %q must start with a letter or digit", name)
	}
	for _, r := range name {
		if isNameAlnum(r) || r == '.' || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("environment name %q contains unsupported character %q", name, r)
	}
	return nil
}

func isNameAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// AutoName derives the deterministic environment name for a profile and
// workspace: a sanitized workspace basename plus a short stable hash of the
// (profile, cleaned path) pair. A moved workspace derives a different name
// instead of silently aliasing the old environment.
func AutoName(profileName, workspace string) string {
	cleaned := filepath.Clean(workspace)
	slug := autoNameSlug(filepath.Base(cleaned))
	sum := sha256.Sum256([]byte(profileName + "\x00" + cleaned))
	return slug + "-" + hex.EncodeToString(sum[:])[:8]
}

func autoNameSlug(base string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(base) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() >= 20 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "ws"
	}
	return slug
}

// ImageForm distinguishes the two accepted base image declaration forms.
type ImageForm string

const (
	ImageFormTemplate ImageForm = "template"
	ImageFormURL      ImageForm = "url"
)

// ImageDeclaration is a parsed base image declaration string.
type ImageDeclaration struct {
	Ref      string
	Form     ImageForm
	Template string
	Location string
	Digest   string
}

// ParseImageDeclaration validates a base image declaration. Accepted forms
// are `template:<built-in-name>` and a https disk-image URL carrying an
// explicit `#sha256:<64 hex>` fragment. Validation is local: no network
// resolution is ever attempted.
func ParseImageDeclaration(ref string) (ImageDeclaration, error) {
	decl := ImageDeclaration{Ref: ref}
	if strings.TrimSpace(ref) == "" {
		return decl, errors.New("base image declaration is required")
	}
	if strings.TrimSpace(ref) != ref {
		return decl, fmt.Errorf("base image declaration %q has leading or trailing whitespace", ref)
	}
	if name, ok := strings.CutPrefix(ref, "template:"); ok {
		if name == "" {
			return decl, errors.New("template image declaration needs a template name")
		}
		for _, r := range name {
			if isNameAlnum(r) || r == '.' || r == '_' || r == '-' || r == '/' {
				continue
			}
			return decl, fmt.Errorf("template name %q contains unsupported character %q", name, r)
		}
		decl.Form = ImageFormTemplate
		decl.Template = name
		return decl, nil
	}
	if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") {
		return decl, errors.New("local image paths are not supported; declare a https disk-image URL with a sha256 digest or a built-in template")
	}
	if !strings.Contains(ref, "://") {
		return decl, errors.New("OCI registry references are not supported in this slice; declare a https disk-image URL with a sha256 digest or a built-in template")
	}
	u, err := url.Parse(ref)
	if err != nil {
		return decl, fmt.Errorf("invalid image URL: %w", err)
	}
	if u.Scheme != "https" {
		return decl, fmt.Errorf("image URLs must use https, got %q", u.Scheme)
	}
	if u.User != nil {
		return decl, errors.New("image URLs must not embed credentials")
	}
	lowerPath := strings.ToLower(u.Path)
	if !strings.HasSuffix(lowerPath, ".img") && !strings.HasSuffix(lowerPath, ".qcow2") {
		return decl, errors.New("image URLs must point at a .img or .qcow2 disk image")
	}
	digest, ok := strings.CutPrefix(u.Fragment, "sha256:")
	if !ok {
		return decl, errors.New("image URL is missing its #sha256:<digest> fragment; copy the digest from the distributor's published sha256 checksums")
	}
	if !isHex64(digest) {
		return decl, errors.New("image URL digest must be 64 hex characters after sha256:")
	}
	decl.Form = ImageFormURL
	base := *u
	base.Fragment = ""
	decl.Location = base.String()
	decl.Digest = digest
	return decl, nil
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
}

func (s Store) Create(spec Spec) (Record, error) {
	if err := ValidateName(spec.Name); err != nil {
		return Record{}, err
	}
	if _, err := ParseImageDeclaration(spec.ImageRef); err != nil {
		return Record{}, err
	}
	if spec.Runtime != nil {
		if err := spec.Runtime.Validate(); err != nil {
			return Record{}, err
		}
		if spec.ImageRef != spec.Runtime.ImageRef() {
			return Record{}, errors.New("runtime provenance does not match environment imageRef")
		}
	}
	if existing, err := s.LoadByName(spec.Name); err == nil {
		return Record{}, fmt.Errorf("environment named %q already exists (%s)", existing.Name, existing.ID)
	} else if !errors.Is(err, ErrNameNotFound) {
		return Record{}, err
	}
	id, err := newID()
	if err != nil {
		return Record{}, err
	}
	now := time.Now().UTC()
	rec := Record{
		Version:              version,
		ID:                   id,
		Name:                 spec.Name,
		AutoNamed:            spec.AutoNamed,
		ImageRef:             spec.ImageRef,
		Runtime:              cloneRuntimeProvenance(spec.Runtime),
		Profile:              spec.Profile,
		Backend:              spec.Backend,
		BackendConfigVersion: spec.BackendConfigVersion,
		Workspace:            filepath.Clean(spec.Workspace),
		GuestWorkspace:       filepath.Clean(spec.GuestWorkspace),
		ProfileID:            spec.ProfileID,
		IdentityID:           spec.IdentityID,
		User:                 spec.User,
		Hostname:             spec.Hostname,
		InstanceName:         spec.InstanceName,
		Status:               StatusCreated,
		CreatedAt:            now,
	}
	if err := s.Save(rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// ErrNameNotFound reports that no supported-version environment record
// carries the requested name.
var ErrNameNotFound = errors.New("environment name not found")

// LoadByName resolves a supported-version environment record by its
// case-insensitive unique name.
func (s Store) LoadByName(name string) (Record, error) {
	if strings.TrimSpace(name) == "" {
		return Record{}, errors.New("environment name is required")
	}
	records, err := s.List()
	if err != nil {
		return Record{}, err
	}
	for _, rec := range records {
		if rec.Status == StatusUnsupportedVersion {
			continue
		}
		if strings.EqualFold(rec.Name, name) {
			return rec, nil
		}
	}
	return Record{}, fmt.Errorf("environment named %q not found (see 'hideout env list'): %w", name, ErrNameNotFound)
}

func (s Store) Save(rec Record) error {
	if !ValidID(rec.ID) {
		return fmt.Errorf("invalid environment id %q", rec.ID)
	}
	if rec.Version == "" {
		rec.Version = version
	}
	if rec.Status == "" {
		rec.Status = "ready"
	}
	if rec.Runtime != nil {
		if err := rec.Runtime.Validate(); err != nil {
			return err
		}
		if rec.ImageRef != rec.Runtime.ImageRef() {
			return errors.New("runtime provenance does not match environment imageRef")
		}
	}
	path := s.recordPath(rec.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func cloneRuntimeProvenance(in *RuntimeProvenance) *RuntimeProvenance {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func (s Store) Load(id string) (Record, error) {
	resolved, err := s.ResolveID(id)
	if err != nil {
		return Record{}, err
	}
	return s.loadExact(resolved)
}

func (s Store) ResolveID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("environment id is required")
	}
	if ValidID(id) {
		if _, err := os.Stat(s.recordPath(id)); err == nil {
			return id, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	records, err := s.List()
	if err != nil {
		return "", err
	}
	var matches []string
	for _, rec := range records {
		if strings.HasPrefix(rec.ID, id) || strings.HasPrefix(strings.TrimPrefix(rec.ID, "env_"), id) {
			matches = append(matches, rec.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("environment %q not found", id)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("environment %q is ambiguous", id)
	}
}

func (s Store) List() ([]Record, error) {
	dir := s.environmentsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !ValidID(entry.Name()) {
			continue
		}
		rec, err := s.loadExact(entry.Name())
		if err != nil {
			if errors.Is(err, ErrUnsupportedVersion) {
				// Only the id, path, and version of a foreign-version record
				// are trusted; every other field is zeroed for display.
				records = append(records, Record{
					ID:      entry.Name(),
					Version: foreignRecordVersion(s.recordPath(entry.Name())),
					Status:  StatusUnsupportedVersion,
				})
				continue
			}
			return nil, err
		}
		records = append(records, rec)
	}
	slices.SortFunc(records, func(a, b Record) int {
		at := sortTime(a)
		bt := sortTime(b)
		if at.Equal(bt) {
			return strings.Compare(a.ID, b.ID)
		}
		if at.After(bt) {
			return -1
		}
		return 1
	})
	return records, nil
}

func (s Store) Remove(id string) error {
	resolved, err := s.ResolveID(id)
	if err != nil {
		return err
	}
	return os.RemoveAll(s.dir(resolved))
}

func (s Store) Lock(id string) (*Lock, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("invalid environment id %q", id)
	}
	dir := s.dir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("environment %s is already in use", id)
		}
		return nil, err
	}
	return &Lock{file: file}, nil
}

func (l *Lock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	return errors.Join(unix.Flock(int(file.Fd()), unix.LOCK_UN), file.Close())
}

func (s Store) RuntimeDir(id string) string {
	return filepath.Join(s.dir(id), "runtime")
}

func (s Store) ShimDir(id string) string {
	return filepath.Join(s.RuntimeDir(id), "shims")
}

func (s Store) PrepareRuntime(id string) error {
	if !ValidID(id) {
		return fmt.Errorf("invalid environment id %q", id)
	}
	return s.ClearRuntime(id)
}

func (s Store) ClearRuntime(id string) error {
	if !ValidID(id) {
		return fmt.Errorf("invalid environment id %q", id)
	}
	runtimeDir := s.RuntimeDir(id)
	allowedDirs := map[string]bool{
		"tmp":       true,
		"shims":     true,
		"network":   true,
		"bootstrap": true,
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(runtimeDir, entry.Name())
		if allowedDirs[entry.Name()] && entry.IsDir() {
			if err := clearDir(path); err != nil {
				return err
			}
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	for name := range allowedDirs {
		if err := os.MkdirAll(filepath.Join(runtimeDir, name), 0o700); err != nil {
			return err
		}
	}
	return nil
}

func (s Store) loadExact(id string) (Record, error) {
	if !ValidID(id) {
		return Record{}, fmt.Errorf("invalid environment id %q", id)
	}
	data, err := os.ReadFile(s.recordPath(id))
	if err != nil {
		return Record{}, err
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		// A corrupt or partially written record is treated like a foreign
		// version: never read through, guided to clean and recreate.
		return Record{}, fmt.Errorf("environment %s: unreadable record: %w", id, ErrUnsupportedVersion)
	}
	if rec.Version != version {
		return Record{}, fmt.Errorf("environment %s has record version %q: %w", id, rec.Version, ErrUnsupportedVersion)
	}
	if rec.ID == "" {
		rec.ID = id
	}
	return rec, nil
}

// foreignRecordVersion best-effort extracts the version string from a record
// that failed supported-version loading. It trusts nothing else in the file.
func foreignRecordVersion(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var probe struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return ""
	}
	return probe.Version
}

func (s Store) recordPath(id string) string {
	return filepath.Join(s.dir(id), recordFile)
}

func (s Store) dir(id string) string {
	return filepath.Join(s.environmentsDir(), id)
}

func (s Store) environmentsDir() string {
	return filepath.Join(s.Root, "environments")
}

func clearDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func sortTime(rec Record) time.Time {
	if !rec.LastStartedAt.IsZero() {
		return rec.LastStartedAt
	}
	return rec.CreatedAt
}

func ValidID(id string) bool {
	if strings.TrimSpace(id) != id || !strings.HasPrefix(id, "env_") {
		return false
	}
	if len(id) <= len("env_") || strings.ContainsAny(id, `/\`) {
		return false
	}
	for _, r := range strings.TrimPrefix(id, "env_") {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func newID() (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "env_" + time.Now().UTC().Format("20060102t150405z") + hex.EncodeToString(b[:]), nil
}
