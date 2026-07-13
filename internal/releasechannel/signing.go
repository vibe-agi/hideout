package releasechannel

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	SigningObservationSchema      = "hideout.release-signing-observation/v1"
	NotarizationObservationSchema = "hideout.release-notarization-observation/v1"
)

type SigningObservation struct {
	Schema                string            `json:"schema"`
	Status                string            `json:"status"`
	TeamID                string            `json:"teamId,omitempty"`
	CommonName            string            `json:"commonName,omitempty"`
	ObservedAt            time.Time         `json:"observedAt"`
	HostOS                string            `json:"hostOS"`
	PackageManifestSHA256 string            `json:"packageManifestSHA256"`
	Binaries              []BinarySignature `json:"binaries"`
}

type BinarySignature struct {
	Path              string `json:"path"`
	Identifier        string `json:"identifier"`
	CDHash            string `json:"cdHash"`
	SecureTimestamp   bool   `json:"secureTimestamp"`
	HardenedRuntime   bool   `json:"hardenedRuntime"`
	StrictVerified    bool   `json:"strictVerified"`
	SystemPolicyValid bool   `json:"systemPolicyValid"`
}

type NotarizationObservation struct {
	Schema                string    `json:"schema"`
	Status                string    `json:"status"`
	SubmissionID          string    `json:"submissionId,omitempty"`
	SubmissionSHA256      string    `json:"submissionSHA256,omitempty"`
	PackageManifestSHA256 string    `json:"packageManifestSHA256,omitempty"`
	ObservedAt            time.Time `json:"observedAt"`
	TicketMode            string    `json:"ticketMode"`
	StapleStatus          string    `json:"stapleStatus"`
}

func (o SigningObservation) Validate(public bool) error {
	if o.Schema != SigningObservationSchema || o.ObservedAt.IsZero() || o.HostOS == "" {
		return errors.New("signing observation schema, observedAt, and hostOS are required")
	}
	if o.Status == "developer-preview-unsigned" && !public {
		if o.TeamID != "" || o.CommonName != "" || len(o.Binaries) != 0 {
			return errors.New("unsigned preview must not claim signing identity")
		}
		return nil
	}
	if o.Status != "developer-id-verified" {
		return fmt.Errorf("unsupported signing status %q", o.Status)
	}
	if o.HostOS != "darwin" || o.TeamID == "" || !strings.HasPrefix(o.CommonName, "Developer ID Application:") || !IsSHA256(o.PackageManifestSHA256) || len(o.Binaries) == 0 {
		return errors.New("Developer ID observation is incomplete")
	}
	seen := map[string]bool{}
	for _, binary := range o.Binaries {
		if err := ValidateRelativePath(binary.Path); err != nil {
			return err
		}
		if seen[binary.Path] || binary.Identifier == "" || binary.CDHash == "" || !binary.SecureTimestamp || !binary.HardenedRuntime || !binary.StrictVerified || !binary.SystemPolicyValid {
			return fmt.Errorf("binary signing observation for %q is incomplete", binary.Path)
		}
		seen[binary.Path] = true
	}
	return nil
}

func (s SigningSummary) ValidatePublic() error {
	if s.Status != "developer-id-verified" || s.TeamID == "" || !strings.HasPrefix(s.CommonName, "Developer ID Application:") || !IsSHA256(s.ObservationSHA256) {
		return errors.New("public release requires observed Developer ID signing")
	}
	return nil
}

func (o NotarizationObservation) Validate(public bool) error {
	if o.Schema != NotarizationObservationSchema || o.ObservedAt.IsZero() {
		return errors.New("notarization observation schema and observedAt are required")
	}
	if o.Status == "not-applicable-preview" && !public {
		if o.SubmissionID != "" || o.SubmissionSHA256 != "" {
			return errors.New("preview notarization must not claim a submission")
		}
		return nil
	}
	if o.Status != "accepted" || o.SubmissionID == "" || !IsSHA256(o.SubmissionSHA256) || !IsSHA256(o.PackageManifestSHA256) || o.TicketMode != "online" || o.StapleStatus != "not-applicable-tar-gz" {
		return errors.New("public release requires accepted online notarization observation")
	}
	return nil
}

func ValidateSigningObservationForPackage(root string, observation SigningObservation) error {
	if err := observation.Validate(true); err != nil {
		return err
	}
	manifestDigest, _, err := RootedFileSHA256(root, "package-manifest.json")
	if err != nil || manifestDigest != observation.PackageManifestSHA256 {
		return errors.New("signing observation package manifest does not match package")
	}
	macho, err := MachOPaths(root)
	if err != nil {
		return err
	}
	observed := make([]string, 0, len(observation.Binaries))
	for _, binary := range observation.Binaries {
		observed = append(observed, binary.Path)
	}
	sort.Strings(macho)
	sort.Strings(observed)
	if strings.Join(macho, "\x00") != strings.Join(observed, "\x00") {
		return fmt.Errorf("signing observation paths do not cover every package Mach-O: package=%v observed=%v", macho, observed)
	}
	return nil
}

func MachOPaths(root string) ([]string, error) {
	var macho []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		var magic [4]byte
		_, readErr := io.ReadFull(file, magic[:])
		closeErr := file.Close()
		if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if readErr == nil && isMachOMagic(magic[:]) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			macho = append(macho, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(macho)
	return macho, nil
}

func isMachOMagic(data []byte) bool {
	return bytes.Equal(data, []byte{0xfe, 0xed, 0xfa, 0xce}) ||
		bytes.Equal(data, []byte{0xce, 0xfa, 0xed, 0xfe}) ||
		bytes.Equal(data, []byte{0xfe, 0xed, 0xfa, 0xcf}) ||
		bytes.Equal(data, []byte{0xcf, 0xfa, 0xed, 0xfe}) ||
		bytes.Equal(data, []byte{0xca, 0xfe, 0xba, 0xbe}) ||
		bytes.Equal(data, []byte{0xbe, 0xba, 0xfe, 0xca})
}

func ValidateNotarizationObservationForPackage(root string, observation NotarizationObservation) error {
	if err := observation.Validate(true); err != nil {
		return err
	}
	digest, _, err := RootedFileSHA256(root, "package-manifest.json")
	if err != nil || digest != observation.PackageManifestSHA256 {
		return errors.New("notarization observation package manifest does not match package")
	}
	return nil
}

func (n NotarizationSummary) ValidatePublic() error {
	if n.Status != "accepted" || n.SubmissionID == "" || !IsSHA256(n.SubmissionSHA256) || n.TicketMode != "online" || n.StapleStatus != "not-applicable-tar-gz" {
		return errors.New("public release notarization summary is invalid")
	}
	return nil
}
