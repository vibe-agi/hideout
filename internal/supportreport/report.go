package supportreport

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/doctor"
	"github.com/vibe-agi/hideout/internal/recovery"
)

const (
	Schema   = "hideout.support-report/v1"
	MaxBytes = 1 << 20
)

type Product struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
	HostOS    string `json:"hostOS"`
	HostArch  string `json:"hostArch"`
}

type SupportEntry struct {
	Subject string `json:"subject"`
	Level   string `json:"level"`
}

type Support struct {
	Schema   string       `json:"schema"`
	Version  string       `json:"version"`
	Platform SupportEntry `json:"platform"`
	Backend  SupportEntry `json:"backend"`
}

type Package struct {
	Applicability  string `json:"applicability"`
	Verification   string `json:"verification"`
	Schema         string `json:"schema,omitempty"`
	ProductVersion string `json:"productVersion,omitempty"`
	SourceCommit   string `json:"sourceCommit,omitempty"`
	Target         string `json:"target,omitempty"`
	FileCount      int    `json:"fileCount,omitempty"`
	Finding        string `json:"finding,omitempty"`
}

type RecoveryEntry struct {
	Code        string   `json:"code"`
	NextActions []string `json:"nextActions"`
}

type Collection struct {
	Product  string `json:"product"`
	Support  string `json:"support"`
	Package  string `json:"package"`
	Doctor   string `json:"doctor"`
	Recovery string `json:"recovery"`
}

type Redaction struct {
	Mode                string   `json:"mode"`
	ExcludedDataClasses []string `json:"excludedDataClasses"`
}

type Provenance struct {
	Command  string `json:"command"`
	Delivery string `json:"delivery"`
	Uploaded bool   `json:"uploaded"`
	MaxBytes int    `json:"maxBytes"`
}

type Report struct {
	Schema      string          `json:"schema"`
	GeneratedAt time.Time       `json:"generatedAt"`
	Product     Product         `json:"product"`
	Support     Support         `json:"support"`
	Package     Package         `json:"package"`
	Doctor      doctor.Report   `json:"doctor"`
	Recovery    []RecoveryEntry `json:"recovery"`
	Collection  Collection      `json:"collection"`
	Redaction   Redaction       `json:"redaction"`
	Provenance  Provenance      `json:"provenance"`
}

var (
	capabilityTokenPattern = regexp.MustCompile(`(?i)\b(?:cap|ui|claim)_[a-z0-9_-]{16,}\b`)
	proxyValuePattern      = regexp.MustCompile(`(?i)\b(?:socks5h?|https?)://[^\s"]+`)
	secretBackingPattern   = regexp.MustCompile(`\bHIDEOUT_SECRET_[A-Z0-9_]+\b`)
	machineIDPattern       = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	rawHostPathPattern     = regexp.MustCompile(`(?:/Users/|/home/|/var/folders/)[^\s"]+`)
)

func MarshalValidated(report Report, protected []string) ([]byte, error) {
	if err := validateModel(report); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("support report is %d bytes; maximum is %d", len(data), MaxBytes)
	}
	if err := validateRedaction(data, protected); err != nil {
		return nil, err
	}
	return data, nil
}

func validateModel(report Report) error {
	if report.Schema != Schema {
		return fmt.Errorf("unsupported support report schema %q", report.Schema)
	}
	if report.GeneratedAt.IsZero() {
		return errors.New("support report generatedAt is required")
	}
	for label, value := range map[string]string{
		"product.version":          report.Product.Version,
		"product.commit":           report.Product.Commit,
		"product.buildTime":        report.Product.BuildTime,
		"product.hostOS":           report.Product.HostOS,
		"product.hostArch":         report.Product.HostArch,
		"support.schema":           report.Support.Schema,
		"support.version":          report.Support.Version,
		"support.platform.subject": report.Support.Platform.Subject,
		"support.platform.level":   report.Support.Platform.Level,
		"support.backend.subject":  report.Support.Backend.Subject,
		"support.backend.level":    report.Support.Backend.Level,
	} {
		if strings.TrimSpace(value) == "" {
			return errors.New(label + " is required")
		}
	}
	if report.Doctor.Schema != doctor.Schema {
		return errors.New("required doctor report is missing or invalid")
	}
	if report.Redaction.Mode != "shareable-support" || len(report.Redaction.ExcludedDataClasses) == 0 {
		return errors.New("shareable support redaction declaration is required")
	}
	if report.Provenance.Command != "hideout support report --out <path>" ||
		report.Provenance.Delivery != "local-file-only" ||
		report.Provenance.Uploaded ||
		report.Provenance.MaxBytes != MaxBytes {
		return errors.New("support report provenance contract is invalid")
	}
	if err := validateCollection(report.Collection); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, entry := range report.Recovery {
		if strings.TrimSpace(entry.Code) == "" || seen[entry.Code] {
			return fmt.Errorf("support recovery code is empty or duplicated: %q", entry.Code)
		}
		seen[entry.Code] = true
	}
	return nil
}

func validateCollection(collection Collection) error {
	for section, status := range map[string]string{
		"product":  collection.Product,
		"support":  collection.Support,
		"package":  collection.Package,
		"doctor":   collection.Doctor,
		"recovery": collection.Recovery,
	} {
		switch status {
		case "collected", "not-applicable", "failed":
		default:
			return fmt.Errorf("support collection %s has invalid status %q", section, status)
		}
	}
	if collection.Doctor != "collected" {
		return errors.New("required doctor collection did not complete")
	}
	return nil
}

func validateRedaction(data []byte, protected []string) error {
	text := string(data)
	for label, pattern := range map[string]*regexp.Regexp{
		"control-plane token": capabilityTokenPattern,
		"proxy value":         proxyValuePattern,
		"secret backing name": secretBackingPattern,
		"machine id":          machineIDPattern,
		"raw host-user path":  rawHostPathPattern,
	} {
		if pattern.MatchString(text) {
			return errors.New("support report contains protected " + label)
		}
	}
	for _, value := range protected {
		value = strings.TrimSpace(value)
		if len(value) >= 4 && strings.Contains(text, value) {
			return errors.New("support report contains caller-protected material")
		}
	}
	return nil
}

func Sanitize(value string, protected []string) string {
	out := value
	for _, secret := range protected {
		if strings.TrimSpace(secret) != "" {
			out = strings.ReplaceAll(out, secret, "[redacted]")
		}
	}
	for _, pattern := range []*regexp.Regexp{
		capabilityTokenPattern,
		proxyValuePattern,
		secretBackingPattern,
		machineIDPattern,
		rawHostPathPattern,
	} {
		out = pattern.ReplaceAllString(out, "[redacted]")
	}
	return out
}

func RecoveryEntries() []RecoveryEntry {
	var out []RecoveryEntry
	for _, entry := range recovery.All() {
		actions := make([]string, 0, len(entry.NextActions))
		for _, action := range entry.NextActions {
			if isShareableAction(action) {
				actions = append(actions, action)
			}
		}
		out = append(out, RecoveryEntry{Code: entry.Code, NextActions: actions})
	}
	slices.SortFunc(out, func(a, b RecoveryEntry) int {
		return strings.Compare(a.Code, b.Code)
	})
	return out
}

func isShareableAction(action string) bool {
	action = strings.TrimSpace(strings.ToLower(action))
	return !strings.Contains(action, "scripts/test-") &&
		!strings.HasPrefix(action, "scripts/") &&
		!strings.HasPrefix(action, "./scripts/") &&
		!strings.HasPrefix(action, "go test ") &&
		!strings.HasPrefix(action, "make ")
}

func DefaultProduct(version, commit, buildTime string) Product {
	return Product{
		Version: version, Commit: commit, BuildTime: buildTime,
		HostOS: runtime.GOOS, HostArch: runtime.GOARCH,
	}
}
