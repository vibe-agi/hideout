package runtimeverify

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/environment"
)

const (
	Schema = "hideout.runtime-verification/v1"

	StatusPreviewReady     = "preview-ready"
	StatusPreviewFailed    = "preview-failed"
	StatusCustomUnverified = "custom/unverified"
	StatusUnknown          = "unknown"
	StatusNotRunning       = "not-running"

	maxResults     = 64
	maxOutputBytes = 512
	maxReasonBytes = 256
)

type Receipt struct {
	Schema          string                        `json:"schema"`
	EnvironmentID   string                        `json:"environmentId"`
	ImageRef        string                        `json:"imageRef"`
	Provenance      environment.RuntimeProvenance `json:"provenance"`
	ContractDigest  string                        `json:"contractDigest"`
	ObservedAt      time.Time                     `json:"observedAt"`
	SessionID       string                        `json:"sessionId"`
	Backend         string                        `json:"backend"`
	BackendReal     bool                          `json:"backendReal"`
	Running         bool                          `json:"running"`
	Instance        Instance                      `json:"instance"`
	PrivilegeStatus string                        `json:"privilegeStatus"`
	Status          string                        `json:"status"`
	Results         []Result                      `json:"results"`
	FailedIDs       []string                      `json:"failedIds"`
	RecoveryCode    string                        `json:"recoveryCode,omitempty"`
}

type Instance struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	VMType        string `json:"vmType"`
	HostOS        string `json:"hostOS"`
	HostArch      string `json:"hostArch"`
	GuestArch     string `json:"guestArch"`
	ImageLocation string `json:"imageLocation"`
	ImageSHA256   string `json:"imageSHA256"`
	// ActiveBuildIdentity is only the SHA-256 of the image-owned package inventory file.
	ActiveBuildIdentity string `json:"activeBuildIdentity"`
	BootID              string `json:"bootId"`
}

type Result struct {
	ID            string `json:"id"`
	Class         string `json:"class"`
	Command       string `json:"command"`
	Present       bool   `json:"present"`
	VersionOutput string `json:"versionOutput,omitempty"`
	Matched       bool   `json:"matched"`
	Reason        string `json:"reason"`
}

func (r *Receipt) Normalize() {
	if r == nil {
		return
	}
	for i := range r.Results {
		r.Results[i].VersionOutput = boundedPrintable(audit.RedactString(r.Results[i].VersionOutput), maxOutputBytes)
		r.Results[i].Reason = boundedPrintable(audit.RedactString(r.Results[i].Reason), maxReasonBytes)
	}
	r.RecoveryCode = boundedPrintable(audit.RedactString(r.RecoveryCode), 96)
}

func (r Receipt) Validate() error {
	if r.Schema != Schema {
		return fmt.Errorf("unsupported runtime verification schema %q", r.Schema)
	}
	if !environment.ValidID(r.EnvironmentID) {
		return fmt.Errorf("invalid runtime verification environment id %q", r.EnvironmentID)
	}
	if _, err := environment.ParseImageDeclaration(r.ImageRef); err != nil {
		return fmt.Errorf("runtime verification imageRef: %w", err)
	}
	if err := r.Provenance.Validate(); err != nil {
		return fmt.Errorf("runtime verification provenance: %w", err)
	}
	if r.ImageRef != r.Provenance.ImageRef() {
		return errors.New("runtime verification imageRef does not match provenance")
	}
	if r.ContractDigest != r.Provenance.ContractDigest {
		return errors.New("runtime verification contract digest does not match provenance")
	}
	if r.ObservedAt.IsZero() || r.ObservedAt.Location() != time.UTC {
		return errors.New("runtime verification observedAt must be non-zero UTC")
	}
	if strings.TrimSpace(r.SessionID) == "" || len(r.SessionID) > 96 || containsControl(r.SessionID) {
		return errors.New("runtime verification sessionId is required and bounded")
	}
	if strings.TrimSpace(r.Backend) == "" || len(r.Backend) > 32 {
		return errors.New("runtime verification backend is required and bounded")
	}
	if err := r.Instance.Validate(); err != nil {
		return fmt.Errorf("runtime verification instance: %w", err)
	}
	if r.Backend != "lima" || !r.BackendReal || !r.Running {
		return errors.New("runtime verification requires a real running Lima instance")
	}
	if r.Instance.ImageLocation != r.Provenance.ArtifactLocation ||
		r.Instance.ImageSHA256 != r.Provenance.ArtifactSHA256 ||
		r.Instance.ActiveBuildIdentity != r.Provenance.PackageInventoryDigest ||
		r.Instance.HostOS != r.Provenance.HostOS ||
		r.Instance.HostArch != r.Provenance.HostArch ||
		r.Instance.GuestArch != r.Provenance.GuestArch {
		return errors.New("runtime verification instance does not match catalog provenance")
	}
	if r.PrivilegeStatus != "enforced" && r.PrivilegeStatus != "degraded" && r.PrivilegeStatus != "unknown" {
		return fmt.Errorf("unsupported runtime privilege status %q", r.PrivilegeStatus)
	}
	if r.Status != StatusPreviewReady && r.Status != StatusPreviewFailed && r.Status != StatusCustomUnverified && r.Status != StatusUnknown {
		return fmt.Errorf("unsupported runtime verification status %q", r.Status)
	}
	if len(r.Results) == 0 || len(r.Results) > maxResults {
		return fmt.Errorf("runtime verification requires 1-%d results", maxResults)
	}
	seen := map[string]struct{}{}
	var computedFailed []string
	for i, result := range r.Results {
		if err := result.Validate(); err != nil {
			return fmt.Errorf("results[%d]: %w", i, err)
		}
		if _, exists := seen[result.ID]; exists {
			return fmt.Errorf("duplicate runtime result id %q", result.ID)
		}
		seen[result.ID] = struct{}{}
		if !result.Present || !result.Matched {
			computedFailed = append(computedFailed, result.ID)
		}
	}
	slices.Sort(computedFailed)
	if !slices.IsSorted(r.FailedIDs) || !slices.Equal(r.FailedIDs, computedFailed) {
		return fmt.Errorf("runtime failedIds do not match failed results: got %v want %v", r.FailedIDs, computedFailed)
	}
	canBeReady := r.PrivilegeStatus == "enforced" && len(computedFailed) == 0
	if r.Status == StatusPreviewReady && !canBeReady {
		return errors.New("runtime verification cannot claim preview-ready from failed, weak, stopped, or degraded facts")
	}
	if r.Status == StatusPreviewFailed && canBeReady {
		return errors.New("runtime verification reports preview-failed with no failed fact")
	}
	if r.Status == StatusPreviewReady && r.RecoveryCode != "" {
		return errors.New("preview-ready runtime verification must not carry recovery")
	}
	if r.Status != StatusPreviewReady && strings.TrimSpace(r.RecoveryCode) == "" {
		return errors.New("non-ready runtime verification requires a recovery code")
	}
	if len(r.RecoveryCode) > 96 || containsControl(r.RecoveryCode) {
		return errors.New("runtime verification recovery code is invalid")
	}
	return nil
}

func (i Instance) Validate() error {
	for label, value := range map[string]string{
		"name": i.Name, "status": i.Status, "vmType": i.VMType,
		"hostOS": i.HostOS, "hostArch": i.HostArch, "guestArch": i.GuestArch,
	} {
		if strings.TrimSpace(value) == "" || len(value) > 128 || containsControl(value) {
			return fmt.Errorf("%s is required and bounded", label)
		}
	}
	if i.Status != "Running" || i.VMType != "vz" {
		return fmt.Errorf("instance must be a running VZ VM, got %s/%s", i.Status, i.VMType)
	}
	if i.HostOS != "darwin" || i.HostArch != "arm64" || i.GuestArch != "aarch64" {
		return fmt.Errorf("unsupported observed runtime tuple %s/%s/%s", i.HostOS, i.HostArch, i.GuestArch)
	}
	if _, err := environment.ParseImageDeclaration(i.ImageLocation + "#sha256:" + i.ImageSHA256); err != nil {
		return fmt.Errorf("image identity: %w", err)
	}
	if !validSHA256Digest(i.ActiveBuildIdentity) {
		return errors.New("activeBuildIdentity must be a sha256 digest of the package inventory")
	}
	if !validBootID(i.BootID) {
		return errors.New("bootId must be a canonical UUID")
	}
	return nil
}

func validSHA256Digest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	for _, r := range value[len(prefix):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validBootID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, r := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func (r Result) Validate() error {
	if strings.TrimSpace(r.ID) == "" || len(r.ID) > 64 || containsControl(r.ID) {
		return errors.New("runtime result id is required and bounded")
	}
	if r.Class != "boundary" && r.Class != "baseline" {
		return fmt.Errorf("unsupported runtime result class %q", r.Class)
	}
	if strings.TrimSpace(r.Command) == "" || len(r.Command) > 128 || strings.ContainsAny(r.Command, "/\\\\ \t\r\n") {
		return fmt.Errorf("runtime result command %q is invalid", r.Command)
	}
	if len(r.VersionOutput) > maxOutputBytes || !utf8.ValidString(r.VersionOutput) || containsControl(r.VersionOutput) {
		return errors.New("runtime result version output must be printable UTF-8 and bounded")
	}
	if strings.TrimSpace(r.Reason) == "" || len(r.Reason) > maxReasonBytes || containsControl(r.Reason) {
		return errors.New("runtime result reason is required, printable, and bounded")
	}
	return nil
}

func boundedPrintable(value string, maxBytes int) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) || !unicode.IsPrint(r) {
			continue
		}
		width := utf8.RuneLen(r)
		if width < 0 || b.Len()+width > maxBytes {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

func containsControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || !unicode.IsPrint(r) {
			return true
		}
	}
	return false
}
