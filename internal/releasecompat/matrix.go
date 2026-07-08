package releasecompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
)

const (
	MatrixSchema  = "hideout.support-matrix/v1"
	MatrixVersion = "2026-07-alpha"

	LevelFirstClass   = "first-class"
	LevelSupported    = "supported"
	LevelDegraded     = "degraded"
	LevelUnsupported  = "unsupported"
	LevelGateRequired = "gate-required"
)

var supportLevels = []string{
	LevelFirstClass,
	LevelSupported,
	LevelDegraded,
	LevelUnsupported,
	LevelGateRequired,
}

type Matrix struct {
	Schema      string     `json:"schema"`
	Version     string     `json:"version"`
	GeneratedBy string     `json:"generatedBy"`
	Entries     []Entry    `json:"entries"`
	NonClaims   []NonClaim `json:"nonClaims"`
}

type Entry struct {
	Area          string   `json:"area"`
	Subject       string   `json:"subject"`
	Level         string   `json:"level"`
	Reason        string   `json:"reason"`
	Guidance      string   `json:"guidance"`
	RequiredGates []string `json:"requiredGates"`
	Evidence      []string `json:"evidence"`
}

type NonClaim struct {
	ID        string   `json:"id"`
	Summary   string   `json:"summary"`
	AppliesTo []string `json:"appliesTo"`
	Guidance  string   `json:"guidance"`
}

func BuiltinMatrix() Matrix {
	return Matrix{
		Schema:      MatrixSchema,
		Version:     MatrixVersion,
		GeneratedBy: "hideout",
		Entries: []Entry{
			{Area: "platform", Subject: "platform/darwin/arm64", Level: LevelFirstClass, Reason: "primary alpha dogfood host", Guidance: "use Lima for isolation claims", RequiredGates: []string{"gate0", "gate2-lima", "gate3-hidden-proxy"}, Evidence: []string{"release-dogfood"}},
			{Area: "platform", Subject: "platform/linux/amd64", Level: LevelSupported, Reason: "supported source/package path with narrower smoke coverage", Guidance: "run local-fast checks and Linux-specific smoke before relying on it", RequiredGates: []string{"gate0"}, Evidence: []string{"package-smoke"}},
			{Area: "platform", Subject: "platform/linux/arm64", Level: LevelSupported, Reason: "supported source/package path with narrower smoke coverage", Guidance: "run local-fast checks and Linux-specific smoke before relying on it", RequiredGates: []string{"gate0"}, Evidence: []string{"package-smoke"}},
			{Area: "platform", Subject: "platform/windows", Level: LevelUnsupported, Reason: "no Windows backend or package path is productized", Guidance: "use macOS arm64 or Linux amd64/aarch64 for alpha testing"},
			{Area: "backend", Subject: "backend/lima", Level: LevelFirstClass, Reason: "primary VM isolation backend", Guidance: "required for release isolation evidence", RequiredGates: []string{"gate2-lima", "gate3-hidden-proxy"}, Evidence: []string{"phase1-release-candidate"}},
			{Area: "backend", Subject: "backend/native", Level: LevelDegraded, Reason: "shares the host user context and is not isolation evidence", Guidance: "use only for development harness or explicit weak-isolation checks", RequiredGates: []string{"gate0"}, Evidence: []string{"native-smoke"}},
			{Area: "helper", Subject: "helper/linux-shim", Level: LevelSupported, Reason: "packaged Linux guest helper", Guidance: "verify package manifest checksums before install", RequiredGates: []string{"gate0"}, Evidence: []string{"package-smoke"}},
			{Area: "helper", Subject: "helper/linux-hostfsd", Level: LevelSupported, Reason: "packaged Linux HostFS daemon", Guidance: "verify package manifest checksums before install", RequiredGates: []string{"gate0"}, Evidence: []string{"package-smoke"}},
			{Area: "helper", Subject: "helper/linux-dns-stub", Level: LevelSupported, Reason: "packaged guest-local DoH stub", Guidance: "required by privacy template DNS mediation", RequiredGates: []string{"gate3-hidden-proxy"}, Evidence: []string{"gate3-hidden-proxy"}},
			{Area: "feature", Subject: "feature/package-install", Level: LevelSupported, Reason: "package install/verify/uninstall lifecycle is implemented", Guidance: "run package smoke on release candidates", RequiredGates: []string{"gate0"}, Evidence: []string{"package-smoke"}},
			{Area: "feature", Subject: "feature/dns-mediation", Level: LevelGateRequired, Reason: "claim depends on real Lima DNS closure proof", Guidance: "require Gate 3 evidence for release candidates", RequiredGates: []string{"gate3-hidden-proxy"}, Evidence: []string{"real-lima"}},
			{Area: "feature", Subject: "feature/hostfs-write-overlay", Level: LevelGateRequired, Reason: "host mutation claim depends on HostFS real data-plane proof", Guidance: "require Gate 2 evidence for release candidates", RequiredGates: []string{"gate2-lima"}, Evidence: []string{"real-lima"}},
			{Area: "feature", Subject: "feature/guest-privilege-separation", Level: LevelGateRequired, Reason: "claim depends on non-root target and separate setup identity proof", Guidance: "require privilege status evidence before claiming enforced separation", RequiredGates: []string{"gate3-hidden-proxy"}, Evidence: []string{"privilege-status"}},
			{Area: "feature", Subject: "feature/adapter-pack-lifecycle", Level: LevelSupported, Reason: "local digest-pinned pack lifecycle is implemented", Guidance: "public marketplace trust remains a non-claim", RequiredGates: []string{"gate0"}, Evidence: []string{"adapter-pack-smoke"}},
			{Area: "feature", Subject: "feature/decision-center", Level: LevelSupported, Reason: "local actionable decision and notice surfaces are implemented", Guidance: "use typed decision/notice commands or Manager API", RequiredGates: []string{"gate0"}, Evidence: []string{"decision-center-smoke"}},
			{Area: "feature", Subject: "feature/doctor", Level: LevelSupported, Reason: "local diagnostics and safe InitTask recovery are implemented", Guidance: "run doctor before release packaging and include redacted evidence when needed", RequiredGates: []string{"gate0"}, Evidence: []string{"doctor-smoke"}},
			{Area: "abi", Subject: "abi/command-adapter/v1", Level: LevelSupported, Reason: "strict command adapter outcomes are Go-validated", Guidance: "enable only tested digest-pinned adapter revisions", RequiredGates: []string{"gate0"}, Evidence: []string{"adapter-pack-smoke"}},
			{Area: "abi", Subject: "abi/adapter-pack/v1", Level: LevelSupported, Reason: "adapter pack manifest and registry schemas are productized", Guidance: "reject unknown ABI versions before enablement", RequiredGates: []string{"gate0"}, Evidence: []string{"adapter-pack-smoke"}},
			{Area: "schema", Subject: "schema/profile/v1", Level: LevelSupported, Reason: "profile schema is locally validated", Guidance: "unknown profile schema versions require recreate or migration", RequiredGates: []string{"gate0"}, Evidence: []string{"schema-smoke"}},
			{Area: "schema", Subject: "schema/doctor-report/v1", Level: LevelSupported, Reason: "doctor report schema is productized", Guidance: "validate doctor evidence before sharing", RequiredGates: []string{"gate0"}, Evidence: []string{"doctor-smoke"}},
			{Area: "schema", Subject: "schema/export-artifact/v1", Level: LevelSupported, Reason: "export/share artifact schema is productized", Guidance: "share only schema-valid export artifacts", RequiredGates: []string{"gate0"}, Evidence: []string{"export-smoke"}},
			{Area: "gate", Subject: "gate/release-candidate", Level: LevelGateRequired, Reason: "release readiness requires real evidence beyond local-fast checks", Guidance: "run release dogfood or provide Gate 2 and Gate 3 evidence", RequiredGates: []string{"gate2-lima", "gate3-hidden-proxy"}, Evidence: []string{"release-dogfood"}},
		},
		NonClaims: []NonClaim{
			{ID: "guest-root-containment", Summary: "Hideout does not claim containment after the target obtains guest root.", AppliesTo: []string{"feature/guest-privilege-separation", "backend/lima"}, Guidance: "treat guest-root as out of scope unless a later real boundary lands"},
			{ID: "workspace-write-blocking", Summary: "Hideout does not block or DLP-scan writes inside the mounted workspace.", AppliesTo: []string{"feature/hostfs-write-overlay"}, Guidance: "use a sanitized workspace and audit workspace-sensitive workflows separately"},
			{ID: "native-isolation", Summary: "The native backend is a weak development harness, not isolation evidence.", AppliesTo: []string{"backend/native"}, Guidance: "use Lima for privacy and release isolation claims"},
			{ID: "marketplace-trust", Summary: "Local adapter packs are supported; public marketplace trust is not productized.", AppliesTo: []string{"feature/adapter-pack-lifecycle"}, Guidance: "pin local packs by digest and inspect permission diffs"},
			{ID: "browser-security", Summary: "Hideout does not claim general browser exploit containment beyond typed host-open boundaries.", AppliesTo: []string{"feature/host-open"}, Guidance: "use browser host-open evidence only for the declared URL boundary"},
			{ID: "unsupported-platforms", Summary: "Unsupported platforms have no alpha release support promise.", AppliesTo: []string{"platform/windows"}, Guidance: "run on macOS arm64 or supported Linux targets"},
		},
	}
}

func WriteMatrixJSON(w io.Writer, matrix Matrix) error {
	matrix = NormalizeMatrix(matrix)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(matrix)
}

func NormalizeMatrix(matrix Matrix) Matrix {
	for i := range matrix.Entries {
		if matrix.Entries[i].RequiredGates == nil {
			matrix.Entries[i].RequiredGates = []string{}
		}
		if matrix.Entries[i].Evidence == nil {
			matrix.Entries[i].Evidence = []string{}
		}
	}
	for i := range matrix.NonClaims {
		if matrix.NonClaims[i].AppliesTo == nil {
			matrix.NonClaims[i].AppliesTo = []string{}
		}
	}
	return matrix
}

func ValidateMatrix(matrix Matrix) error {
	if matrix.Schema != MatrixSchema {
		return fmt.Errorf("unsupported matrix schema %q", matrix.Schema)
	}
	if strings.TrimSpace(matrix.Version) == "" {
		return errors.New("matrix version is required")
	}
	seen := map[string]bool{}
	for i, entry := range matrix.Entries {
		key := entry.Subject
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("matrix entry %d missing subject", i)
		}
		if seen[key] {
			return fmt.Errorf("duplicate matrix entry %q", key)
		}
		seen[key] = true
		if !slices.Contains(supportLevels, entry.Level) {
			return fmt.Errorf("matrix entry %q uses unsupported level %q", key, entry.Level)
		}
		if entry.Level != LevelFirstClass && (strings.TrimSpace(entry.Reason) == "" || strings.TrimSpace(entry.Guidance) == "") {
			return fmt.Errorf("matrix entry %q needs reason and guidance", key)
		}
	}
	for _, subject := range requiredSubjects() {
		if !seen[subject] {
			return fmt.Errorf("matrix missing required subject %q", subject)
		}
	}
	nonClaims := map[string]bool{}
	for _, claim := range matrix.NonClaims {
		if strings.TrimSpace(claim.ID) == "" {
			return errors.New("matrix non-claim missing id")
		}
		nonClaims[claim.ID] = true
		if strings.TrimSpace(claim.Summary) == "" || strings.TrimSpace(claim.Guidance) == "" {
			return fmt.Errorf("matrix non-claim %q needs summary and guidance", claim.ID)
		}
	}
	for _, id := range RequiredNonClaimIDs() {
		if !nonClaims[id] {
			return fmt.Errorf("matrix missing required non-claim %q", id)
		}
	}
	return nil
}

func requiredSubjects() []string {
	return []string{
		"platform/darwin/arm64",
		"platform/linux/amd64",
		"platform/linux/arm64",
		"backend/lima",
		"backend/native",
		"feature/dns-mediation",
		"feature/hostfs-write-overlay",
		"feature/guest-privilege-separation",
		"abi/command-adapter/v1",
		"abi/adapter-pack/v1",
		"schema/profile/v1",
		"schema/doctor-report/v1",
		"schema/export-artifact/v1",
		"gate/release-candidate",
	}
}

func RequiredNonClaimIDs() []string {
	return []string{
		"guest-root-containment",
		"workspace-write-blocking",
		"native-isolation",
		"marketplace-trust",
		"browser-security",
		"unsupported-platforms",
	}
}

func FindEntry(matrix Matrix, subject string) (Entry, bool) {
	for _, entry := range matrix.Entries {
		if entry.Subject == subject {
			return entry, true
		}
	}
	return Entry{}, false
}

func CurrentPlatformSubject() string {
	return PlatformSubject(runtime.GOOS, runtime.GOARCH)
}

func PlatformSubject(goos, goarch string) string {
	if goarch == "aarch64" {
		goarch = "arm64"
	}
	return "platform/" + goos + "/" + goarch
}

func BackendSubject(backend string) string {
	backend = strings.TrimSpace(backend)
	if backend == "" || backend == "auto" {
		backend = "lima"
	}
	return "backend/" + backend
}

func CurrentSupportSummary(backend string) string {
	matrix := BuiltinMatrix()
	platformEntry, ok := FindEntry(matrix, CurrentPlatformSubject())
	if !ok {
		platformEntry = Entry{Subject: CurrentPlatformSubject(), Level: LevelUnsupported, Reason: "platform is not in the alpha matrix", Guidance: "use macOS arm64 or supported Linux targets"}
	}
	backendEntry, ok := FindEntry(matrix, BackendSubject(backend))
	if !ok {
		backendEntry = Entry{Subject: BackendSubject(backend), Level: LevelUnsupported, Reason: "backend is not in the alpha matrix", Guidance: "choose lima for isolation claims"}
	}
	return fmt.Sprintf("platform=%s backend=%s", supportLabel(platformEntry), supportLabel(backendEntry))
}

func supportLabel(entry Entry) string {
	return fmt.Sprintf("%s:%s", entry.Subject, entry.Level)
}
