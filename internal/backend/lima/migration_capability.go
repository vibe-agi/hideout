package lima

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/migration/vzexecutor"
)

const (
	migrationCapabilityProtocol = "hideout.lima-migration-capability/v1"
	migrationAdoptionVersion    = "1.0.0"
	migrationCapabilityTimeout  = 5 * time.Second
	migrationVersionOutputLimit = 256
	migrationExecutorProbeLimit = 4096
)

var supportedMigrationLimaVersion = regexp.MustCompile(
	`^limactl version (2\.(?:1|2)\.[0-9]+)\n?$`,
)

type MigrationOptions struct {
	LimaHome             string
	HelperPath           string
	AdoptionExecutorPath string
	HostOS               string
	HostArch             string
	GuestArch            string
	FileCloner           func(source, destination string) error

	// adoptionIsolationProber is intentionally package-private. Production
	// cannot opt into full import by configuration; only a compiled provider
	// integration that proves its no-network adoption executor may set it.
	adoptionIsolationProber func(context.Context, string, string) (string, error)

	// sourceIdentityObserver is a package-private deterministic test seam. The
	// production path always boots a disposable COW root through the packaged
	// zero-network executor and never accepts a configured identity value.
	sourceIdentityObserver func(
		context.Context,
		string,
		migrationSnapshotOwner,
		migration.Digest,
	) ([]backend.MigrationSourceIdentity, []migration.Digest, error)
}

type migrationCapabilityFacts struct {
	Protocol       string `json:"protocol"`
	Provider       string `json:"provider"`
	LimaVersion    string `json:"limaVersion"`
	Layout         string `json:"layout"`
	Host           string `json:"host"`
	Guest          string `json:"guest"`
	LimaHomeDigest string `json:"limaHomeDigest"`
	HelperDigest   string `json:"helperDigest"`
	ExecutorDigest string `json:"executorDigest"`
	IsolationProof string `json:"isolationProof"`
	FullExport     bool   `json:"fullExport"`
	FullImport     bool   `json:"fullImport"`
	BlockerCode    string `json:"blockerCode"`
}

func (b Backend) MigrationCapabilities(
	ctx context.Context,
) (backend.MigrationCapabilities, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	hostOS, hostArch, guestArch := b.migrationArchitectures()
	capability := backend.MigrationCapabilities{
		Provider: "lima", ProviderVersion: "unknown",
		DiskRepresentations: []string{"raw"},
		ArchitecturePairs: []backend.MigrationArchitecturePair{{
			Host: hostOS + "/" + hostArch, Guest: "linux/" + guestArch,
		}},
		RootDiskKinds:     []string{"lima-root"},
		AttachedDiskKinds: []string{"lima-additional"},
		SparseExtents:     true,
		Limits:            migration.DefaultLimits(),
	}
	facts := migrationCapabilityFacts{
		Protocol: migrationCapabilityProtocol, Provider: "lima",
		LimaVersion: "unknown", Layout: "unavailable",
		Host: hostOS + "/" + hostArch, Guest: "linux/" + guestArch,
	}
	block := func(code, summary, remediation string) {
		if capability.Unavailable == nil {
			capability.Unavailable = &backend.MigrationProviderBlocker{
				Code: code, Summary: summary, Remediation: remediation,
			}
		}
		facts.BlockerCode = code
	}

	if hostOS != "darwin" || hostArch != "arm64" || guestArch != "arm64" {
		block(
			"migration.provider.platform_unsupported",
			"Full-state Lima migration is unavailable on this host architecture.",
			"Use config-only migration or a supported native-architecture macOS destination.",
		)
	}

	version, versionErr := b.probeMigrationLimaVersion(ctx)
	if versionErr != nil {
		block(
			"migration.provider.lima_version_unsupported",
			"The installed Lima version is unavailable or outside the proved migration range.",
			"Install a supported Lima 2.1.x or 2.2.x release and retry capability inspection.",
		)
	} else {
		capability.ProviderVersion = version
		facts.LimaVersion = version
		facts.Layout = "lima-v2-consolidated-disk-v1"
	}

	limaHome, homeErr := b.migrationLimaHome()
	if homeErr != nil {
		block(
			"migration.provider.lima_home_unsafe",
			"The Lima storage root is missing or cannot be safely resolved.",
			"Create a private Lima home directory or choose config-only migration.",
		)
	} else {
		facts.LimaHomeDigest = migrationCapabilityStringDigest(limaHome)
	}

	helper, helperErr := b.resolveMigrationAdoptionHelper(guestArch)
	if helperErr == nil && helper.Path != "" {
		digest := migration.Digest(helper.ExpectedDigest)
		capability.AdoptionHelper = &backend.MigrationHelperCapability{
			PackageID:         migration.AdoptionHelperPackage,
			Version:           migrationAdoptionVersion,
			GuestArchitecture: "linux/" + guestArch,
			Digest:            digest,
		}
		facts.HelperDigest = helper.ExpectedDigest
	}

	baseReady := capability.Unavailable == nil
	if baseReady && capability.AdoptionHelper == nil {
		block(
			"migration.provider.adoption_helper_unavailable",
			"The checksummed Linux migration identity helper is unavailable.",
			"Repair or reinstall the exact Hideout package before full-state migration.",
		)
	} else if baseReady {
		proof, isolationErr := b.probeMigrationAdoptionIsolation(
			ctx, capability.ProviderVersion, limaHome,
		)
		if isolationErr != nil || strings.TrimSpace(proof) == "" || len(proof) > 128 {
			block(
				"migration.provider.adoption_isolation_unproved",
				"Full-state migration cannot prove a zero-network identity/adoption boot.",
				"Install a Hideout package with the proved migration appliance; config-only migration remains available.",
			)
		} else {
			facts.IsolationProof = proof
			if b.Migration == nil || b.Migration.adoptionIsolationProber == nil {
				executor, resolveErr := b.resolveMigrationAdoptionExecutor(hostOS, hostArch)
				if resolveErr != nil || executor.Path == "" {
					return backend.MigrationCapabilities{}, errors.New(
						"proved migration adoption executor became unavailable",
					)
				}
				facts.ExecutorDigest = executor.ExpectedDigest
			}
			capability.FullExport = true
			capability.FullImport = true
		}
	}
	facts.FullExport = capability.FullExport
	facts.FullImport = capability.FullImport
	revision, err := migrationCapabilityDigest(facts)
	if err != nil {
		return backend.MigrationCapabilities{}, fmt.Errorf(
			"construct Lima migration capability revision: %w",
			err,
		)
	}
	capability.Revision = revision
	if err := capability.Validate(); err != nil {
		return backend.MigrationCapabilities{}, fmt.Errorf(
			"construct Lima migration capability: %w",
			err,
		)
	}
	return capability, nil
}

func (b Backend) probeMigrationAdoptionIsolation(
	ctx context.Context,
	limaVersion,
	limaHome string,
) (string, error) {
	if b.Migration != nil && b.Migration.adoptionIsolationProber != nil {
		return b.Migration.adoptionIsolationProber(ctx, limaVersion, limaHome)
	}
	hostOS, hostArch, _ := b.migrationArchitectures()
	executor, err := b.resolveMigrationAdoptionExecutor(hostOS, hostArch)
	if err != nil || executor.Path == "" {
		if err == nil {
			err = errors.New("packaged migration VZ adoption executor is unavailable")
		}
		return "", err
	}
	probeCtx, cancel := context.WithTimeout(ctx, migrationCapabilityTimeout)
	defer cancel()
	capture := &boundedRuntimeCapture{limit: migrationExecutorProbeLimit}
	if err := b.runner().Run(
		probeCtx, executor.Path, []string{"--probe"},
		[]string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/bin"},
		nil, capture, io.Discard,
	); err != nil {
		return "", err
	}
	if probeCtx.Err() != nil {
		return "", probeCtx.Err()
	}
	if capture.truncated {
		return "", errors.New("migration VZ executor probe exceeded the limit")
	}
	var probe vzexecutor.Probe
	decoder := json.NewDecoder(strings.NewReader(capture.String()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&probe); err != nil {
		return "", errors.New("migration VZ executor probe is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", errors.New("migration VZ executor probe has trailing data")
	}
	if err := probe.Validate(); err != nil {
		return "", err
	}
	rechecked, err := b.resolveMigrationAdoptionExecutor(hostOS, hostArch)
	if err != nil || rechecked.ExpectedDigest != executor.ExpectedDigest {
		return "", errors.New("migration VZ executor changed while probing")
	}
	return probe.ProofIdentity(migration.Digest(executor.ExpectedDigest))
}

func (b Backend) resolveMigrationAdoptionExecutor(
	hostOS, hostArch string,
) (helperbin.HostMigrationVZAdoptResolution, error) {
	configured := ""
	if b.Migration != nil {
		configured = strings.TrimSpace(b.Migration.AdoptionExecutorPath)
	}
	return helperbin.ResolveHostMigrationVZAdopt(configured, hostOS, hostArch)
}

func (b Backend) migrationArchitectures() (string, string, string) {
	hostOS, hostArch, guestArch := runtime.GOOS, runtime.GOARCH, runtime.GOARCH
	if b.Migration == nil {
		return hostOS, hostArch, guestArch
	}
	if value := strings.TrimSpace(b.Migration.HostOS); value != "" {
		hostOS = value
	}
	if value := strings.TrimSpace(b.Migration.HostArch); value != "" {
		hostArch = value
	}
	if value := strings.TrimSpace(b.Migration.GuestArch); value != "" {
		guestArch = value
	}
	return hostOS, hostArch, guestArch
}

func (b Backend) probeMigrationLimaVersion(ctx context.Context) (string, error) {
	runner := b.runner()
	if _, err := runner.LookPath(b.limactl()); err != nil {
		return "", err
	}
	probeCtx, cancel := context.WithTimeout(ctx, migrationCapabilityTimeout)
	defer cancel()
	capture := &boundedRuntimeCapture{limit: migrationVersionOutputLimit}
	if err := runner.Run(
		probeCtx,
		b.limactl(),
		[]string{"--version"},
		HostCommandEnv(os.Environ()),
		nil,
		capture,
		io.Discard,
	); err != nil {
		return "", err
	}
	if probeCtx.Err() != nil {
		return "", probeCtx.Err()
	}
	if capture.truncated {
		return "", errors.New("Lima version output exceeded the limit")
	}
	match := supportedMigrationLimaVersion.FindStringSubmatch(capture.String())
	if len(match) != 2 {
		return "", errors.New("Lima version output is unsupported")
	}
	return match[1], nil
}

func (b Backend) migrationLimaHome() (string, error) {
	configured := ""
	if b.Migration != nil {
		configured = strings.TrimSpace(b.Migration.LimaHome)
	}
	if configured == "" {
		configured = strings.TrimSpace(os.Getenv("LIMA_HOME"))
	}
	if configured == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		configured = filepath.Join(home, ".lima")
	}
	absolute, err := filepath.Abs(configured)
	if err != nil || filepath.Clean(absolute) != absolute {
		return "", errors.New("Lima home is not a clean absolute path")
	}
	physical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(physical)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() ||
		info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("Lima home is not a protected directory")
	}
	return physical, nil
}

func (b Backend) resolveMigrationAdoptionHelper(
	guestArch string,
) (helperbin.LinuxMigrationAdoptResolution, error) {
	if b.Migration != nil && strings.TrimSpace(b.Migration.HelperPath) != "" {
		path := b.Migration.HelperPath
		manifest, ok := helperbin.LinuxMigrationAdoptHelperCurrent(path, guestArch)
		if !ok {
			return helperbin.LinuxMigrationAdoptResolution{},
				errors.New("configured migration adoption helper is invalid")
		}
		return helperbin.LinuxMigrationAdoptResolution{
			Path: path, ExpectedDigest: "sha256:" + manifest.SHA256,
			Manifest: manifest,
		}, nil
	}
	return helperbin.ResolveLinuxMigrationAdopt("", guestArch)
}

func migrationCapabilityDigest(facts migrationCapabilityFacts) (migration.Digest, error) {
	data, err := json.Marshal(facts)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return migration.Digest(fmt.Sprintf("sha256:%x", digest[:])), nil
}

func migrationCapabilityStringDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", digest[:])
}
