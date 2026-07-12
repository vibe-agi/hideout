package runtimecatalog

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
)

const (
	CatalogSchema     = "hideout.runtime-catalog/v1"
	MaturityPreview   = "preview"
	RevisionPreview   = "preview"
	RevisionWithdrawn = "withdrawn"
	UnpromotedRelease = "development-unpromoted"
	maxDownloadBytes  = 4 << 30
	maxVirtualBytes   = 16 << 30
)

type RequiredObservation struct {
	ID      string
	Class   string
	Command string
}

var v1RequiredObservations = []RequiredObservation{
	{ID: "boundary.getent", Class: ObservationBoundary, Command: "getent"},
	{ID: "baseline.git", Class: ObservationBaseline, Command: "git"},
	{ID: "baseline.curl", Class: ObservationBaseline, Command: "curl"},
	{ID: "baseline.jq", Class: ObservationBaseline, Command: "jq"},
	{ID: "baseline.tar", Class: ObservationBaseline, Command: "tar"},
	{ID: "baseline.unzip", Class: ObservationBaseline, Command: "unzip"},
	{ID: "baseline.find", Class: ObservationBaseline, Command: "find"},
	{ID: "baseline.grep", Class: ObservationBaseline, Command: "grep"},
	{ID: "baseline.node", Class: ObservationBaseline, Command: "node"},
	{ID: "baseline.npm", Class: ObservationBaseline, Command: "npm"},
	{ID: "baseline.python", Class: ObservationBaseline, Command: "python3"},
	{ID: "baseline.pip", Class: ObservationBaseline, Command: "pip3"},
	{ID: "baseline.go", Class: ObservationBaseline, Command: "go"},
	{ID: "baseline.cc", Class: ObservationBaseline, Command: "cc"},
	{ID: "baseline.make", Class: ObservationBaseline, Command: "make"},
}

func V1RequiredObservations() []RequiredObservation {
	return slices.Clone(v1RequiredObservations)
}

var (
	//go:embed catalog.json
	embeddedCatalog []byte
	//go:embed contract.json
	embeddedContract []byte
	idPattern        = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._/-]*[a-z0-9])?$`)
	hex64Pattern     = regexp.MustCompile(`^[a-f0-9]{64}$`)
	hex128Pattern    = regexp.MustCompile(`^[a-f0-9]{128}$`)
)

type Catalog struct {
	Schema         string   `json:"schema"`
	CatalogRelease string   `json:"catalogRelease"`
	GeneratedAt    string   `json:"generatedAt"`
	Families       []Family `json:"families"`
	Contract       Contract `json:"-"`
}

type Family struct {
	ID              string     `json:"id"`
	DisplayName     string     `json:"displayName"`
	Maturity        string     `json:"maturity"`
	CurrentRevision string     `json:"currentRevision"`
	Revisions       []Revision `json:"revisions"`
}

type Revision struct {
	ID             string     `json:"id"`
	Status         string     `json:"status"`
	ContractID     string     `json:"contractId"`
	ContractDigest string     `json:"contractDigest"`
	ReviewedAt     string     `json:"reviewedAt"`
	Artifacts      []Artifact `json:"artifacts"`
}

type Artifact struct {
	HostOS                 string         `json:"hostOS"`
	HostArch               string         `json:"hostArch"`
	GuestArch              string         `json:"guestArch"`
	Format                 string         `json:"format"`
	Location               string         `json:"location"`
	SHA256                 string         `json:"sha256"`
	DownloadBytes          int64          `json:"downloadBytes"`
	VirtualBytes           int64          `json:"virtualBytes"`
	SupplyMode             string         `json:"supplyMode"`
	Source                 ArtifactSource `json:"source"`
	PackageInventoryDigest string         `json:"packageInventoryDigest"`
	SBOM                   SBOM           `json:"sbom"`
}

type ArtifactSource struct {
	BaseLocation     string `json:"baseLocation"`
	BaseSHA512       string `json:"baseSHA512"`
	BaseSHA256       string `json:"baseSHA256"`
	BuildCommit      string `json:"buildCommit"`
	SourceLockSHA256 string `json:"sourceLockSHA256"`
	LicenseReview    string `json:"licenseReview"`
}

type SBOM struct {
	Available bool   `json:"available"`
	Status    string `json:"status"`
	Format    string `json:"format,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Reference string `json:"reference,omitempty"`
}

type Selection struct {
	Family   string
	Revision string
	HostOS   string
	HostArch string
	ImageRef string
}

type Provenance = environment.RuntimeProvenance

type Resolution struct {
	Family     Family
	Revision   Revision
	Artifact   Artifact
	Contract   Contract
	ImageRef   string
	Provenance Provenance
}

func EmbeddedBytes() ([]byte, []byte) {
	return slices.Clone(embeddedCatalog), slices.Clone(embeddedContract)
}

func LoadEmbedded() (Catalog, error) {
	return Parse(embeddedCatalog, embeddedContract)
}

func ResolveEmbedded(selection Selection) (Resolution, error) {
	catalog, err := LoadEmbedded()
	if err != nil {
		return Resolution{}, err
	}
	return catalog.Resolve(selection)
}

func Parse(catalogData, contractData []byte) (Catalog, error) {
	contract, err := ParseContract(contractData)
	if err != nil {
		return Catalog{}, err
	}
	var catalog Catalog
	if err := decodeStrict(catalogData, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode runtime catalog: %w", err)
	}
	catalog.Contract = contract
	if err := catalog.Validate(contractData); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func (c Catalog) Validate(contractData []byte) error {
	if c.Schema != CatalogSchema {
		return fmt.Errorf("unsupported runtime catalog schema %q", c.Schema)
	}
	if strings.TrimSpace(c.CatalogRelease) == "" || len(c.CatalogRelease) > 64 || containsControl(c.CatalogRelease) {
		return errors.New("catalog release is required and must be bounded")
	}
	if _, err := time.Parse(time.RFC3339, c.GeneratedAt); err != nil {
		return fmt.Errorf("catalog generatedAt: %w", err)
	}
	if len(c.Families) > 32 {
		return errors.New("runtime catalog allows at most 32 families")
	}
	if len(c.Families) == 0 {
		if c.CatalogRelease != UnpromotedRelease || len(c.Contract.Observations) != 0 {
			return errors.New("an empty runtime catalog must be the explicit development-unpromoted shell with an empty contract")
		}
		return nil
	}
	if len(c.Contract.Observations) == 0 {
		return errors.New("a populated runtime catalog requires a non-empty contract")
	}
	contractSum := sha256.Sum256(contractData)
	contractDigest := "sha256:" + hex.EncodeToString(contractSum[:])
	seenFamilies := map[string]struct{}{}
	for i := range c.Families {
		family := &c.Families[i]
		if err := family.Validate(c.Contract, contractDigest); err != nil {
			return fmt.Errorf("families[%d]: %w", i, err)
		}
		if _, exists := seenFamilies[family.ID]; exists {
			return fmt.Errorf("duplicate family id %q", family.ID)
		}
		seenFamilies[family.ID] = struct{}{}
	}
	return nil
}

// ValidateV1Promotable is the product acceptance layer. Generic parsing stays
// multi-platform for future schema evolution, but only this exact v1 shape can
// be selected or produce runtime provenance.
func (c Catalog) ValidateV1Promotable() error {
	if c.CatalogRelease == UnpromotedRelease {
		return errors.New("runtime catalog is explicitly unpromoted")
	}
	if len(c.Families) != 1 {
		return fmt.Errorf("runtime v1 promotion requires exactly one family, got %d", len(c.Families))
	}
	family := c.Families[0]
	if family.ID != "developer-standard" || family.Maturity != MaturityPreview || len(family.Revisions) != 1 {
		return errors.New("runtime v1 promotion requires one preview developer-standard revision")
	}
	revision := family.Revisions[0]
	if revision.ID != family.CurrentRevision || revision.Status != RevisionPreview || len(revision.Artifacts) != 1 {
		return errors.New("runtime v1 promotion requires one current non-withdrawn artifact")
	}
	artifact := revision.Artifacts[0]
	if artifact.HostOS != "darwin" || artifact.HostArch != "arm64" || artifact.GuestArch != "aarch64" {
		return fmt.Errorf("runtime v1 promotion requires darwin/arm64 -> aarch64, got %s/%s -> %s", artifact.HostOS, artifact.HostArch, artifact.GuestArch)
	}
	observed := make(map[string]Observation, len(c.Contract.Observations))
	for _, item := range c.Contract.Observations {
		observed[item.ID] = item
	}
	for _, required := range v1RequiredObservations {
		item, ok := observed[required.ID]
		if !ok || item.Class != required.Class || item.Command != required.Command {
			return fmt.Errorf("runtime v1 contract is missing required observation %s (%s/%s)", required.ID, required.Class, required.Command)
		}
	}
	return nil
}

func (f Family) Validate(contract Contract, contractDigest string) error {
	if err := validateID("family id", f.ID); err != nil {
		return err
	}
	if strings.TrimSpace(f.DisplayName) == "" || len(f.DisplayName) > 96 || containsControl(f.DisplayName) {
		return fmt.Errorf("family %q display name is required and bounded", f.ID)
	}
	if f.Maturity != MaturityPreview {
		return fmt.Errorf("family %q has unsupported maturity %q", f.ID, f.Maturity)
	}
	if len(f.Revisions) == 0 || len(f.Revisions) > 32 {
		return fmt.Errorf("family %q requires 1-32 revisions", f.ID)
	}
	seen := map[string]struct{}{}
	currentFound := false
	for i := range f.Revisions {
		revision := f.Revisions[i]
		if err := revision.Validate(contract, contractDigest); err != nil {
			return fmt.Errorf("revisions[%d]: %w", i, err)
		}
		if _, exists := seen[revision.ID]; exists {
			return fmt.Errorf("duplicate revision id %q", revision.ID)
		}
		seen[revision.ID] = struct{}{}
		if revision.ID == f.CurrentRevision {
			currentFound = true
			if revision.Status == RevisionWithdrawn {
				return fmt.Errorf("current revision %q is withdrawn", revision.ID)
			}
		}
	}
	if !currentFound {
		return fmt.Errorf("current revision %q does not exist", f.CurrentRevision)
	}
	return nil
}

func (r Revision) Validate(contract Contract, contractDigest string) error {
	if err := validateID("revision id", r.ID); err != nil {
		return err
	}
	if r.Status != RevisionPreview && r.Status != RevisionWithdrawn {
		return fmt.Errorf("revision %q has unsupported status %q", r.ID, r.Status)
	}
	if r.ContractID != contract.ID {
		return fmt.Errorf("revision %q contract id %q does not match %q", r.ID, r.ContractID, contract.ID)
	}
	if r.ContractDigest != contractDigest {
		return fmt.Errorf("revision %q contract digest mismatch", r.ID)
	}
	if _, err := time.Parse(time.RFC3339, r.ReviewedAt); err != nil {
		return fmt.Errorf("revision %q reviewedAt: %w", r.ID, err)
	}
	if len(r.Artifacts) == 0 || len(r.Artifacts) > 16 {
		return fmt.Errorf("revision %q requires 1-16 artifacts", r.ID)
	}
	seen := map[string]struct{}{}
	for i, artifact := range r.Artifacts {
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("artifacts[%d]: %w", i, err)
		}
		key := artifact.HostOS + "/" + artifact.HostArch
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate artifact for %s", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (a Artifact) Validate() error {
	if !slices.Contains([]string{"darwin", "linux"}, a.HostOS) || !slices.Contains([]string{"arm64", "amd64"}, a.HostArch) {
		return fmt.Errorf("unsupported host tuple %s/%s", a.HostOS, a.HostArch)
	}
	wantGuest := map[string]string{"arm64": "aarch64", "amd64": "x86_64"}[a.HostArch]
	if a.GuestArch != wantGuest {
		return fmt.Errorf("guest arch %q does not match host arch %q", a.GuestArch, a.HostArch)
	}
	if a.Format != "qcow2" || a.SupplyMode != "hideout-built" {
		return fmt.Errorf("unsupported artifact format/supply %q/%q", a.Format, a.SupplyMode)
	}
	if err := validateVersionedHTTPSQCOW2("artifact location", a.Location); err != nil {
		return err
	}
	if !hex64Pattern.MatchString(a.SHA256) {
		return errors.New("artifact sha256 must be 64 lowercase hex characters")
	}
	if a.DownloadBytes <= 0 || a.DownloadBytes > maxDownloadBytes || a.VirtualBytes <= 0 || a.VirtualBytes > maxVirtualBytes {
		return errors.New("artifact sizes exceed the runtime budget")
	}
	if err := a.Source.Validate(); err != nil {
		return err
	}
	if !strings.HasPrefix(a.PackageInventoryDigest, "sha256:") || !hex64Pattern.MatchString(strings.TrimPrefix(a.PackageInventoryDigest, "sha256:")) {
		return errors.New("package inventory digest is invalid")
	}
	return a.SBOM.Validate()
}

func (s ArtifactSource) Validate() error {
	if err := validateVersionedHTTPSQCOW2("base location", s.BaseLocation); err != nil {
		return err
	}
	if !hex128Pattern.MatchString(s.BaseSHA512) || !hex64Pattern.MatchString(s.BaseSHA256) || !hex64Pattern.MatchString(s.SourceLockSHA256) {
		return errors.New("artifact source digest is invalid")
	}
	if !regexp.MustCompile(`^[a-f0-9]{12,40}$`).MatchString(s.BuildCommit) {
		return errors.New("artifact build commit is invalid")
	}
	if s.LicenseReview != "reviewed" && s.LicenseReview != "pending" {
		return fmt.Errorf("unsupported license review %q", s.LicenseReview)
	}
	return nil
}

func (s SBOM) Validate() error {
	if s.Available {
		if s.Status != "available" || !slices.Contains([]string{"spdx-json", "cyclonedx-json"}, s.Format) || !hex64Pattern.MatchString(s.SHA256) {
			return errors.New("available SBOM requires status, format, and sha256")
		}
		u, err := url.Parse(s.Reference)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return errors.New("available SBOM reference must be credential-free HTTPS")
		}
		return nil
	}
	if s.Status != "unavailable-preview" || s.Format != "" || s.SHA256 != "" || s.Reference != "" {
		return errors.New("unavailable SBOM must use unavailable-preview without artifact fields")
	}
	return nil
}

func (c Catalog) Resolve(selection Selection) (Resolution, error) {
	if err := c.ValidateV1Promotable(); err != nil {
		return Resolution{}, err
	}
	if strings.TrimSpace(selection.ImageRef) != "" {
		return Resolution{}, errors.New("runtime selection and custom image are mutually exclusive")
	}
	if selection.Family == "" || selection.HostOS == "" || selection.HostArch == "" {
		return Resolution{}, errors.New("runtime family and observed host tuple are required")
	}
	var family *Family
	for i := range c.Families {
		if c.Families[i].ID == selection.Family {
			family = &c.Families[i]
			break
		}
	}
	if family == nil {
		return Resolution{}, fmt.Errorf("runtime family %q is not in the package catalog", selection.Family)
	}
	revisionID := selection.Revision
	if revisionID == "" {
		revisionID = family.CurrentRevision
	}
	var revision *Revision
	for i := range family.Revisions {
		if family.Revisions[i].ID == revisionID {
			revision = &family.Revisions[i]
			break
		}
	}
	if revision == nil {
		return Resolution{}, fmt.Errorf("runtime revision %q is not in family %q", revisionID, family.ID)
	}
	if revision.Status == RevisionWithdrawn {
		return Resolution{}, fmt.Errorf("runtime revision %q is withdrawn", revision.ID)
	}
	var matches []Artifact
	for _, artifact := range revision.Artifacts {
		if artifact.HostOS == selection.HostOS && artifact.HostArch == selection.HostArch {
			matches = append(matches, artifact)
		}
	}
	if len(matches) != 1 {
		return Resolution{}, fmt.Errorf("runtime %s/%s has %d artifacts for host %s/%s; exactly one is required", family.ID, revision.ID, len(matches), selection.HostOS, selection.HostArch)
	}
	artifact := matches[0]
	if artifact.Source.LicenseReview != "reviewed" {
		return Resolution{}, errors.New("runtime artifact license/source review is not complete")
	}
	imageRef := artifact.Location + "#sha256:" + artifact.SHA256
	if _, err := environment.ParseImageDeclaration(imageRef); err != nil {
		return Resolution{}, fmt.Errorf("resolved runtime image: %w", err)
	}
	provenance := Provenance{
		Family: family.ID, Revision: revision.ID, CatalogRelease: c.CatalogRelease,
		ContractID: revision.ContractID, ContractDigest: revision.ContractDigest,
		ArtifactLocation: artifact.Location, ArtifactSHA256: artifact.SHA256,
		PackageInventoryDigest: artifact.PackageInventoryDigest,
		DownloadBytes:          artifact.DownloadBytes, VirtualBytes: artifact.VirtualBytes,
		HostOS: artifact.HostOS, HostArch: artifact.HostArch, GuestArch: artifact.GuestArch,
		Maturity: family.Maturity,
	}
	return Resolution{Family: *family, Revision: *revision, Artifact: artifact, Contract: c.Contract, ImageRef: imageRef, Provenance: provenance}, nil
}

func validateVersionedHTTPSQCOW2(label, value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%s must be an HTTPS URL", label)
	}
	if u.User != nil {
		return fmt.Errorf("%s must not contain userinfo", label)
	}
	if u.RawQuery != "" || u.Fragment != "" || path.Ext(u.Path) != ".qcow2" {
		return fmt.Errorf("%s must be a query-free versioned .qcow2 URL", label)
	}
	for _, part := range strings.Split(strings.ToLower(strings.Trim(u.Path, "/")), "/") {
		if slices.Contains([]string{"latest", "current", "daily"}, part) {
			return fmt.Errorf("%s contains moving path segment %q", label, part)
		}
	}
	return nil
}

func validateID(label, value string) error {
	if len(value) == 0 || len(value) > 64 || !idPattern.MatchString(value) {
		return fmt.Errorf("%s %q is invalid", label, value)
	}
	return nil
}
