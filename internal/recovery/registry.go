package recovery

import (
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
)

const Schema = "hideout.recovery-codes/v1"

const (
	CodePackageObsoleteLeftover     = "package.obsolete-leftover"
	CodePackagePrerequisiteMissing  = "package.prerequisite.missing"
	CodePackageMigrationUnsupported = "package.migration.unsupported"
	CodePackagePlatformUnsupported  = "package.platform.unsupported"
	CodeInitProxySecretMissing      = "init.proxy-secret.missing"
	CodeInitMediatedResolverMissing = "init.mediated-resolver.missing"
	CodePrivilegeStatusDegraded     = "privilege.status.degraded"
	CodeReleaseGateEvidenceMissing  = "release.gate-evidence.missing"
	CodeReleaseEvidenceStale        = "release.evidence.stale"
	CodeReleasePackageIdentity      = "release.package.identity-invalid"
	CodeReleaseSigningRequired      = "release.signing.required"
	CodeReleaseNotarizationRequired = "release.notarization.required"
	CodeReleaseRepositoryPrereq     = "release.repository.prerequisite"
	CodeHostFSReservedRootDenied    = "hostfs.reserved-root.denied"
	CodeDecisionClaimExpired        = "decision.claim.expired"
	CodeRuntimeSelectionUnsupported = "runtime.selection.unsupported"
	CodeRuntimeCatalogInvalid       = "runtime.catalog.invalid"
	CodeRuntimeArtifactUnavailable  = "runtime.artifact.unavailable"
	CodeRuntimeArtifactDigest       = "runtime.artifact.digest-mismatch"
	CodeRuntimeDiskInsufficient     = "runtime.disk.insufficient"
	CodeRuntimeBoundaryMissing      = "runtime.boundary.missing"
	CodeRuntimeBaselineMissing      = "runtime.baseline.missing"
	CodeRuntimeCommandMissing       = "runtime.command.missing"
	CodeRuntimeNetworkDenied        = "runtime.network.denied"
	CodeRuntimeDNSFailed            = "runtime.dns.failed"
	CodeRuntimeRegistryFailed       = "runtime.registry.failed"
	CodeRuntimePrefixUnwritable     = "runtime.prefix.unwritable"
	CodeSessionOwnerUnprovable      = "session.owner.unprovable"
	CodeSessionIsolationUnsupported = "session.isolation.unsupported"
	CodeSessionServiceConflict      = "session.service.conflict"
	CodeSessionCleanupFailed        = "session.cleanup.failed"
	CodeEnvironmentActiveSessions   = "environment.active-sessions"

	// Community host-app recipe lifecycle (032).
	CodeHostAppSourceInvalid            = "host-app.source.invalid"
	CodeHostAppGitUnavailable           = "host-app.source.git-unavailable"
	CodeHostAppDigestMismatch           = "host-app.source.digest-mismatch"
	CodeHostAppCommandConflict          = "host-app.command.conflict"
	CodeHostAppAbsent                   = "host-app.identity.absent"
	CodeHostAppIdentityInvalid          = "host-app.identity.invalid"
	CodeHostAppIdentityDrift            = "host-app.identity.drift"
	CodeHostAppSafetyUnavailable        = "host-app.safety.unavailable"
	CodeHostAppPermissionReviewRequired = "host-app.permission.review-required"
	CodeHostAppPortalUnavailable        = "host-app.portal.unavailable"
	CodeHostAppBindingDisabled          = "host-app.binding.disabled"
	CodeHostAppBindingRevoked           = "host-app.binding.revoked"
	CodeHostAppNewRunRequired           = "host-app.run.new-required"

	// Host capability projection (030).
	CodeProjectionCommandUnbound        = "projection.command.unbound"
	CodeProjectionProviderUnavailable   = "projection.provider.unavailable"
	CodeProjectionPathNoHostMapping     = "projection.path.no-host-mapping"
	CodeProjectionAppAbsent             = "projection.app.absent"
	CodeProjectionAppIdentityDrift      = "projection.app.identity-drift"
	CodeProjectionModeTrustedDenied     = "projection.mode.trusted-denied"
	CodeProjectionFlagUnrecognized      = "projection.flag.unrecognized"
	CodeProjectionIntentInvalid         = "projection.intent.invalid"
	CodeProjectionCapabilityDesignReady = "projection.capability.design-ready"
)

type Code struct {
	Code        string   `json:"code"`
	Subsystem   string   `json:"subsystem"`
	Severity    string   `json:"severity"`
	Reason      string   `json:"reason"`
	Hint        string   `json:"hint"`
	NextActions []string `json:"nextActions,omitempty"`
	DocsRefs    []string `json:"docsRefs,omitempty"`
}

type RegistryView struct {
	Schema string `json:"schema"`
	Codes  []Code `json:"codes"`
}

func All() []Code {
	out := append([]Code(nil), registry...)
	slices.SortFunc(out, func(a, b Code) int { return strings.Compare(a.Code, b.Code) })
	return out
}

func Lookup(code string) (Code, bool) {
	code = strings.TrimSpace(code)
	for _, entry := range registry {
		if entry.Code == code {
			return entry, true
		}
	}
	return Code{}, false
}

func View() RegistryView {
	return RegistryView{Schema: Schema, Codes: All()}
}

func WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(View())
}

func Validate(codes []Code) error {
	seen := map[string]bool{}
	for _, entry := range codes {
		if strings.TrimSpace(entry.Code) == "" {
			return errors.New("recovery code is required")
		}
		if seen[entry.Code] {
			return errors.New("duplicate recovery code " + entry.Code)
		}
		seen[entry.Code] = true
		if !strings.Contains(entry.Code, ".") || strings.ToLower(entry.Code) != entry.Code {
			return errors.New("recovery code must be lowercase and subsystem-namespaced: " + entry.Code)
		}
		if strings.TrimSpace(entry.Subsystem) == "" {
			return errors.New("recovery subsystem is required for " + entry.Code)
		}
		if strings.TrimSpace(entry.Severity) == "" {
			return errors.New("recovery severity is required for " + entry.Code)
		}
		if strings.TrimSpace(entry.Reason) == "" {
			return errors.New("recovery reason is required for " + entry.Code)
		}
		if strings.TrimSpace(entry.Hint) == "" {
			return errors.New("recovery hint is required for " + entry.Code)
		}
		for _, action := range entry.NextActions {
			if containsControlPlaneMaterial(action) {
				return errors.New("recovery next action contains control-plane material for " + entry.Code)
			}
		}
	}
	return nil
}

func containsControlPlaneMaterial(value string) bool {
	return strings.Contains(value, "HIDEOUT_SECRET_") ||
		strings.Contains(value, "cap_") ||
		strings.Contains(value, "claim_") ||
		strings.Contains(value, "ui_")
}

var registry = []Code{
	{Code: CodeEnvironmentActiveSessions, Subsystem: "environment", Severity: "warning", Reason: "the environment still has active run-session owners", Hint: "exit the active sessions and retry the explicit stop", NextActions: []string{"hideout env list"}, DocsRefs: []string{"docs/privacy-run-design.md"}},
	{Code: CodeDecisionClaimExpired, Subsystem: "decision", Severity: "warning", Reason: "decision claim expired or is no longer usable", Hint: "claim the decision again before applying it", NextActions: []string{"hideout decision claim <id>"}, DocsRefs: []string{"docs/first-run-alpha.md"}},
	{Code: CodeHostFSReservedRootDenied, Subsystem: "hostfs", Severity: "error", Reason: "HostFS request targets a reserved Hideout or credential root", Hint: "choose a workspace path outside reserved roots", NextActions: []string{"hideout doctor --feature hostfs"}, DocsRefs: []string{"docs/hostfs-overlay-design.md"}},
	{Code: CodeHostAppSourceInvalid, Subsystem: "host-app", Severity: "error", Reason: "the host-app recipe source is malformed, unavailable, or not exactly locked", Hint: "validate a bounded local snapshot or a git source pinned to an exact commit", NextActions: []string{"hideout app validate <source>"}, DocsRefs: []string{"docs/host-app-recipes.md"}},
	{Code: CodeHostAppGitUnavailable, Subsystem: "host-app", Severity: "error", Reason: "git is unavailable for exact-commit host-app source acquisition", Hint: "install git and retry the same exact source lock", NextActions: []string{"hideout doctor --feature packaging"}, DocsRefs: []string{"docs/host-app-recipes.md"}},
	{Code: CodeHostAppDigestMismatch, Subsystem: "host-app", Severity: "error", Reason: "the host-app recipe source no longer matches the reviewed digest", Hint: "inspect the changed source and create a fresh add or update plan", NextActions: []string{"hideout app inspect <pack-id>"}, DocsRefs: []string{"docs/host-app-recipes.md"}},
	{Code: CodeHostAppCommandConflict, Subsystem: "host-app", Severity: "warning", Reason: "a requested projected command is reserved or already has a different owner", Hint: "choose a non-reserved command or explicitly review the exact owner replacement", NextActions: []string{"hideout app inspect <pack-id>"}, DocsRefs: []string{"docs/host-app-recipes.md"}},
	{Code: CodeHostAppAbsent, Subsystem: "host-app", Severity: "error", Reason: "the selected host application is absent from every Core-owned application root", Hint: "install the application in a supported Applications directory and inspect again", NextActions: []string{"hideout app inspect <pack-id>"}, DocsRefs: []string{"docs/host-app-recipes.md", "docs/host-capability-projection.md"}},
	{Code: CodeHostAppIdentityInvalid, Subsystem: "host-app", Severity: "error", Reason: "the host application is absent, unsafe to launch, or does not match the Core-observed identity", Hint: "inspect the application location, ownership, signature, and current content identity", NextActions: []string{"hideout app inspect <pack-id>"}, DocsRefs: []string{"docs/host-app-recipes.md", "docs/host-capability-projection.md"}},
	{Code: CodeHostAppIdentityDrift, Subsystem: "host-app", Severity: "error", Reason: "the Core-observed host application identity changed after review", Hint: "inspect the current application and explicitly re-trust its exact observed identity", NextActions: []string{"hideout app inspect <pack-id>"}, DocsRefs: []string{"docs/host-app-recipes.md", "docs/host-capability-projection.md"}},
	{Code: CodeHostAppSafetyUnavailable, Subsystem: "host-app", Severity: "warning", Reason: "Core has no compatible reviewed safe posture for this application and recipe effect", Hint: "use ask-each-run after review or choose an app with a compatible Core safety profile", NextActions: []string{"hideout app inspect <pack-id>"}, DocsRefs: []string{"docs/host-app-recipes.md", "docs/host-capability-projection.md"}},
	{Code: CodeHostAppPermissionReviewRequired, Subsystem: "host-app", Severity: "warning", Reason: "the host-app recipe permission fingerprint changed or has not been accepted", Hint: "review the exact permission difference before enabling the revision", NextActions: []string{"hideout app enable <pack-id> --profile <name>"}, DocsRefs: []string{"docs/host-app-recipes.md"}},
	{Code: CodeHostAppPortalUnavailable, Subsystem: "host-app", Severity: "error", Reason: "the requested HostFS resource has no active same-session portal with sufficient content authority", Hint: "use an active authorized HostFS mapping; discover-only visibility is insufficient", NextActions: []string{"hideout doctor --feature hostfs"}, DocsRefs: []string{"docs/host-app-recipes.md", "docs/hostfs-overlay-design.md"}},
	{Code: CodeHostAppBindingDisabled, Subsystem: "host-app", Severity: "warning", Reason: "the exact host-app binding is disabled for this profile", Hint: "inspect and explicitly enable an exact revision before starting a new run", NextActions: []string{"hideout app inspect <pack-id>"}, DocsRefs: []string{"docs/host-app-recipes.md"}},
	{Code: CodeHostAppBindingRevoked, Subsystem: "host-app", Severity: "error", Reason: "the exact host-app binding or package revision is disabled or revoked", Hint: "inspect current package state; projected commands never fall back after revocation", NextActions: []string{"hideout app inspect <pack-id>"}, DocsRefs: []string{"docs/host-app-recipes.md"}},
	{Code: CodeHostAppNewRunRequired, Subsystem: "host-app", Severity: "warning", Reason: "host-app recipe changes apply only to future runs", Hint: "start a new run after reviewing the enabled exact revision", NextActions: []string{"hideout app list"}, DocsRefs: []string{"docs/host-app-recipes.md"}},
	{Code: CodeInitMediatedResolverMissing, Subsystem: "init", Severity: "error", Reason: "tun2socks privacy mode requires a mediated DNS resolver IP", Hint: "rerun init with --mediated-resolver <ip>", NextActions: []string{"hideout init --template privacy --backend lima --network tun2socks --proxy-secret <ref> --mediated-resolver <ip> --no-input"}, DocsRefs: []string{"docs/first-run-alpha.md"}},
	{Code: CodeInitProxySecretMissing, Subsystem: "init", Severity: "error", Reason: "tun2socks privacy mode requires a proxy secret ref", Hint: "create or reference a proxy secret and rerun init", NextActions: []string{"hideout init --template privacy --backend lima --network tun2socks --proxy-secret <ref> --mediated-resolver <ip> --no-input"}, DocsRefs: []string{"docs/first-run-alpha.md"}},
	{Code: CodePackageObsoleteLeftover, Subsystem: "package", Severity: "warning", Reason: "package-owned files from a previous install are still present", Hint: "inspect leftovers and run package repair if expected", NextActions: []string{"hideout package repair --prefix <dir> --dry-run"}, DocsRefs: []string{"docs/first-run-alpha.md"}},
	{Code: CodePackagePrerequisiteMissing, Subsystem: "package", Severity: "warning", Reason: "external package prerequisite is missing", Hint: "install the external prerequisite or expose it on PATH", NextActions: []string{"hideout package verify <install-prefix>"}, DocsRefs: []string{"docs/distribution-bootstrap.md"}},
	{Code: CodePackageMigrationUnsupported, Subsystem: "package", Severity: "error", Reason: "the installed package state is unpublished legacy, newer, or outside the supported migration floor", Hint: "verify the package identity and reinstall into a clean prefix before copying durable operator state", NextActions: []string{"hideout package verify <install-prefix>"}, DocsRefs: []string{"docs/distribution-bootstrap.md", "docs/support-matrix.md"}},
	{Code: CodePackagePlatformUnsupported, Subsystem: "package", Severity: "error", Reason: "the package target does not match the current host platform", Hint: "download the exact package declared for this host OS and architecture", NextActions: []string{"hideout support matrix --json"}, DocsRefs: []string{"docs/support-matrix.md"}},
	{Code: CodePrivilegeStatusDegraded, Subsystem: "privilege", Severity: "warning", Reason: "guest privilege separation is degraded or unproven", Hint: "recreate with an enforced-capable image or accept degraded risk explicitly", NextActions: []string{"hideout doctor --feature privilege --backend lima"}, DocsRefs: []string{"docs/threat-model.md"}},
	{Code: CodeReleaseEvidenceStale, Subsystem: "release", Severity: "error", Reason: "supporting evidence was produced for a different commit or package", Hint: "rerun the product-hardening or release gate on the candidate artifact", NextActions: []string{"hideout support readiness --mode release-candidate --gate2-evidence <manifest> --gate3-evidence <manifest>"}, DocsRefs: []string{"docs/support-matrix.md"}},
	{Code: CodeReleaseGateEvidenceMissing, Subsystem: "release", Severity: "error", Reason: "real Gate 2 or Gate 3 evidence is missing", Hint: "run the required real gate and pass its manifest to release readiness", NextActions: []string{"hideout support readiness --mode release-candidate --gate2-evidence <manifest> --gate3-evidence <manifest>"}, DocsRefs: []string{"docs/support-matrix.md"}},
	{Code: CodeReleasePackageIdentity, Subsystem: "release", Severity: "error", Reason: "the release package version, full source commit, archive digest, or target identity is invalid", Hint: "validate the exact archive rather than an extracted root or caller-supplied shorthand", NextActions: []string{"hideout support release validate --manifest <release.json> --asset-root <dir>"}, DocsRefs: []string{"docs/distribution-bootstrap.md", "docs/support-matrix.md"}},
	{Code: CodeReleaseSigningRequired, Subsystem: "release", Severity: "error", Reason: "the public-alpha package lacks an independently verified Developer ID signing observation", Hint: "sign the frozen host binaries and validate the observation against the exact package tree", NextActions: []string{"hideout support release validate-signing --package-root <dir> --observation <signing.json>"}, DocsRefs: []string{"docs/distribution-bootstrap.md"}},
	{Code: CodeReleaseNotarizationRequired, Subsystem: "release", Severity: "error", Reason: "the public-alpha candidate lacks an accepted online notarization observation", Hint: "submit the frozen signed tree in the private ZIP envelope and validate Apple's accepted result", NextActions: []string{"hideout support release validate-notarization --package-root <dir> --observation <notarization.json>"}, DocsRefs: []string{"docs/distribution-bootstrap.md"}},
	{Code: CodeReleaseRepositoryPrereq, Subsystem: "release", Severity: "error", Reason: "the repository release channel is missing immutable releases, private vulnerability reporting, or protected promotion review", Hint: "configure and independently observe every required repository control before promotion", NextActions: []string{"hideout support matrix --json"}, DocsRefs: []string{"SECURITY.md", "docs/distribution-bootstrap.md"}},
	{Code: CodeRuntimeArtifactDigest, Subsystem: "runtime", Severity: "error", Reason: "the downloaded runtime artifact does not match its catalog digest", Hint: "discard the failed download and retry the same immutable runtime selection", NextActions: []string{"hideout runtime inspect developer-standard"}, DocsRefs: []string{"docs/first-run-alpha.md", "docs/support-matrix.md"}},
	{Code: CodeRuntimeArtifactUnavailable, Subsystem: "runtime", Severity: "error", Reason: "the selected immutable runtime artifact could not be retrieved", Hint: "verify host connectivity and inspect the selected versioned artifact before retrying", NextActions: []string{"hideout runtime inspect developer-standard"}, DocsRefs: []string{"docs/first-run-alpha.md", "docs/support-matrix.md"}},
	{Code: CodeRuntimeBaselineMissing, Subsystem: "runtime", Severity: "warning", Reason: "the running guest is missing or mismatches a declared developer-baseline command", Hint: "inspect the failed observations and recreate from the selected immutable runtime when drift is unintended", NextActions: []string{"hideout runtime verify --env <name>"}, DocsRefs: []string{"docs/first-run-alpha.md", "docs/support-matrix.md"}},
	{Code: CodeRuntimeBoundaryMissing, Subsystem: "runtime", Severity: "error", Reason: "the running guest is missing a boundary prerequisite required before Hideout setup", Hint: "inspect the failed observations and recreate from the selected immutable runtime", NextActions: []string{"hideout runtime verify --env <name>"}, DocsRefs: []string{"docs/first-run-alpha.md", "docs/support-matrix.md"}},
	{Code: CodeRuntimeCatalogInvalid, Subsystem: "runtime", Severity: "error", Reason: "the package-owned runtime catalog or contract is invalid", Hint: "verify or reinstall the Hideout package; do not use an unverified catalog override", NextActions: []string{"hideout package verify <install-prefix>"}, DocsRefs: []string{"docs/distribution-bootstrap.md", "docs/support-matrix.md"}},
	{Code: CodeRuntimeCommandMissing, Subsystem: "runtime", Severity: "error", Reason: "the exact requested target command is absent from the running guest", Hint: "inspect the runtime contract or choose an existing command; Hideout will not guess or install a package", NextActions: []string{"hideout runtime inspect developer-standard"}, DocsRefs: []string{"docs/first-run-alpha.md", "docs/support-matrix.md"}},
	{Code: CodeRuntimeDiskInsufficient, Subsystem: "runtime", Severity: "error", Reason: "available host storage is insufficient or could not be proven before runtime download", Hint: "free storage and retry without changing the selected runtime revision", NextActions: []string{"hideout doctor --feature runtime"}, DocsRefs: []string{"docs/first-run-alpha.md", "docs/support-matrix.md"}},
	{Code: CodeRuntimeDNSFailed, Subsystem: "runtime", Severity: "error", Reason: "the target package install could not resolve its registry through the selected DNS path", Hint: "inspect mediated DNS configuration and retry the same target command", NextActions: []string{"hideout doctor --feature dns"}, DocsRefs: []string{"docs/first-run-alpha.md", "docs/threat-model.md"}},
	{Code: CodeRuntimeNetworkDenied, Subsystem: "runtime", Severity: "error", Reason: "the selected network policy denied the target package install", Hint: "review the profile network policy; Hideout will not silently broaden network access", NextActions: []string{"hideout doctor --feature dns"}, DocsRefs: []string{"docs/first-run-alpha.md", "docs/threat-model.md"}},
	{Code: CodeRuntimePrefixUnwritable, Subsystem: "runtime", Severity: "error", Reason: "the documented target-user install prefix is not writable", Hint: "restore target ownership of the durable user prefix and retry without sudo", NextActions: []string{"hideout runtime verify --env <name>"}, DocsRefs: []string{"docs/first-run-alpha.md"}},
	{Code: CodeRuntimeRegistryFailed, Subsystem: "runtime", Severity: "error", Reason: "the public package registry rejected or failed the pinned agent package request", Hint: "inspect the registry response and retry the exact pinned package; do not substitute an unreviewed package", NextActions: []string{"hideout runtime verify --env <name>"}, DocsRefs: []string{"docs/first-run-alpha.md"}},
	{Code: CodeRuntimeSelectionUnsupported, Subsystem: "runtime", Severity: "error", Reason: "the requested runtime family, revision, host, or architecture is unsupported", Hint: "list the package-owned runtime catalog and choose an explicitly supported tuple", NextActions: []string{"hideout runtime list"}, DocsRefs: []string{"docs/first-run-alpha.md", "docs/support-matrix.md"}},
	{Code: CodeSessionCleanupFailed, Subsystem: "session", Severity: "error", Reason: "a run session could not prove complete cleanup of its own authority", Hint: "inspect session status and run doctor before stopping or reusing the environment", NextActions: []string{"hideout doctor --level deep"}, DocsRefs: []string{"docs/privacy-run-design.md", "docs/threat-model.md"}},
	{Code: CodeSessionIsolationUnsupported, Subsystem: "session", Severity: "error", Reason: "the guest lacks a required concurrent-session isolation primitive", Hint: "recreate the environment from a supported runtime and retry; Hideout will not use the globally visible fallback", NextActions: []string{"hideout runtime verify --env <name>", "hideout env recreate <name>"}, DocsRefs: []string{"docs/support-matrix.md", "docs/threat-model.md"}},
	{Code: CodeSessionOwnerUnprovable, Subsystem: "session", Severity: "error", Reason: "run-session ownership could not be proven from its operating-system lease", Hint: "inspect deep doctor output and reconcile stale state before lifecycle mutation", NextActions: []string{"hideout doctor --level deep"}, DocsRefs: []string{"docs/privacy-run-design.md"}},
	{Code: CodeSessionServiceConflict, Subsystem: "session", Severity: "error", Reason: "the requested environment service configuration differs from the active service", Hint: "finish active sessions or use an environment with matching profile and network configuration", NextActions: []string{"hideout env list", "hideout doctor --feature dns"}, DocsRefs: []string{"docs/privacy-run-design.md", "docs/threat-model.md"}},
	{Code: CodeProjectionCommandUnbound, Subsystem: "projection", Severity: "error", Reason: "the projected command name is not bound to a host capability", Hint: "run the command inside the guest only if a projection binding exists", NextActions: []string{"hideout doctor --feature projection"}, DocsRefs: []string{"docs/host-capability-projection.md"}},
	{Code: CodeProjectionProviderUnavailable, Subsystem: "projection", Severity: "error", Reason: "the host capability provider is unavailable and the request fails closed", Hint: "the projected command does not fall back to host execution; retry after the provider is available", NextActions: []string{"hideout doctor --feature projection"}, DocsRefs: []string{"docs/host-capability-projection.md"}},
	{Code: CodeProjectionPathNoHostMapping, Subsystem: "projection", Severity: "warning", Reason: "the requested path does not map into the mounted workspace", Hint: "project only workspace-mapped paths; guest-only or out-of-workspace paths are refused", NextActions: []string{"hideout doctor --feature projection"}, DocsRefs: []string{"docs/host-capability-projection.md"}},
	{Code: CodeProjectionAppAbsent, Subsystem: "projection", Severity: "error", Reason: "the registered host application for this projection is not installed in a supported location", Hint: "install the officially signed application bundle in a supported Applications directory; ambient PATH entries are not application identity", NextActions: []string{"hideout doctor --feature projection"}, DocsRefs: []string{"docs/host-capability-projection.md"}},
	{Code: CodeProjectionAppIdentityDrift, Subsystem: "projection", Severity: "error", Reason: "the requested application id did not resolve to a registered host application", Hint: "projected applications are referenced by a stable id from the Core recipe registry", NextActions: []string{"hideout doctor --feature projection"}, DocsRefs: []string{"docs/host-capability-projection.md"}},
	{Code: CodeProjectionModeTrustedDenied, Subsystem: "projection", Severity: "warning", Reason: "the trusted-host-ide mode requires an explicit operator grant", Hint: "grant trusted-host-ide through the decision center, or use the default safe mode", NextActions: []string{"hideout decision list", "hideout doctor --feature projection"}, DocsRefs: []string{"docs/host-capability-projection.md"}},
	{Code: CodeProjectionFlagUnrecognized, Subsystem: "projection", Severity: "warning", Reason: "the projected command received a flag the recipe grammar does not accept", Hint: "the recipe accepts a bounded flag set; unrecognized flags are denied, not passed to the host", NextActions: []string{"hideout doctor --feature projection"}, DocsRefs: []string{"docs/host-capability-projection.md"}},
	{Code: CodeProjectionIntentInvalid, Subsystem: "projection", Severity: "warning", Reason: "the projected command produced an intent that failed Core validation", Hint: "report the command; Core re-validates every projection intent and fails closed", NextActions: []string{"hideout doctor --feature projection"}, DocsRefs: []string{"docs/host-capability-projection.md"}},
	{Code: CodeProjectionCapabilityDesignReady, Subsystem: "projection", Severity: "error", Reason: "the requested host capability is design-ready and cannot be dispatched in this version", Hint: "only implemented capabilities dispatch; design-ready capabilities fail closed", NextActions: []string{"hideout doctor --feature projection"}, DocsRefs: []string{"docs/host-capability-projection.md"}},
}
