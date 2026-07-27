package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/packsnapshot"
	"github.com/vibe-agi/hideout/internal/recovery"
	"github.com/vibe-agi/hideout/internal/session"
)

const HostAppPackPlanVersion = "hideout.host-app-pack-plan/v1"

type HostAppPackOptions struct {
	Operation      string
	SourceKind     string
	SourcePath     string
	SourceURL      string
	SourceCommit   string
	ProfileName    string
	PackID         string
	RevisionID     string
	BindingIDs     []string
	Access         string
	Replacements   map[string]string
	ExpectedDigest string
	InstallOnly    bool
	Reason         string
}

type HostAppPackReview struct {
	PackID                 string                     `json:"packId"`
	Version                string                     `json:"version"`
	Description            string                     `json:"description"`
	InstallHint            string                     `json:"installHint,omitempty"`
	InstallHintURL         string                     `json:"installHintUrl,omitempty"`
	Commands               []string                   `json:"commands"`
	Applications           []string                   `json:"applications"`
	ApplicationsDeclared   []HostAppApplicationReview `json:"applicationsDeclared"`
	ApplicationsObserved   []HostAppIdentityReview    `json:"applicationsObserved,omitempty"`
	Capabilities           []string                   `json:"capabilities"`
	ResultPolicies         []string                   `json:"resultPolicies"`
	ResourceKinds          []string                   `json:"resourceKinds"`
	RequestedAccess        []string                   `json:"requestedAccess"`
	SourceDigest           string                     `json:"sourceDigest"`
	PermissionFingerprint  string                     `json:"permissionFingerprint"`
	UntrustedPackageFields bool                       `json:"untrustedPackageFields"`
}

type HostAppApplicationReview struct {
	AppID                  string   `json:"appId"`
	BundleNames            []string `json:"bundleNames"`
	ExecutableRelativePath string   `json:"executableRelativePath"`
	ExpectedBundleID       string   `json:"expectedBundleId,omitempty"`
	ExpectedTeamID         string   `json:"expectedTeamId,omitempty"`
	RequestedSafetyProfile string   `json:"requestedSafetyProfile,omitempty"`
}

type HostAppIdentityReview struct {
	AppID           string `json:"appId"`
	QualifiedAppRef string `json:"qualifiedAppRef"`
	Verification    string `json:"verification"`
	RootClass       string `json:"rootClass"`
	OwnerClass      string `json:"ownerClass"`
	BundleID        string `json:"bundleId,omitempty"`
	TeamID          string `json:"teamId,omitempty"`
	CodeIdentity    string `json:"codeIdentity,omitempty"`
	ContentDigest   string `json:"contentDigest,omitempty"`
	IdentityDigest  string `json:"identityDigest,omitempty"`
}

type HostAppUnverifiedTrustReview struct {
	QualifiedAppRef     string `json:"qualifiedAppRef"`
	RootClass           string `json:"rootClass"`
	CanonicalPathDigest string `json:"canonicalPathDigest"`
	ContentDigest       string `json:"contentDigest"`
	IdentityDigest      string `json:"identityDigest"`
}

type HostAppSourceReview struct {
	Kind     string `json:"kind"`
	Location string `json:"location,omitempty"`
	Commit   string `json:"commit,omitempty"`
}

type HostAppPackPlan struct {
	Version                           string                         `json:"version"`
	Operation                         string                         `json:"operation"`
	Source                            hostapppack.SourceSpec         `json:"-"`
	SourceReview                      HostAppSourceReview            `json:"source,omitempty"`
	Profile                           string                         `json:"profile,omitempty"`
	PackID                            string                         `json:"packId,omitempty"`
	RevisionID                        string                         `json:"revisionId,omitempty"`
	PreviousRevisionID                string                         `json:"previousRevisionId,omitempty"`
	BindingIDs                        []string                       `json:"bindingIds,omitempty"`
	Access                            string                         `json:"access,omitempty"`
	Replacements                      map[string]string              `json:"replacements,omitempty"`
	CommandPlan                       cmdproxy.HostAppCommandPlan    `json:"commandPlan,omitempty"`
	InstallOnly                       bool                           `json:"installOnly,omitempty"`
	ExpectedSourceDigest              string                         `json:"expectedSourceDigest,omitempty"`
	ExpectedBasePermissionFingerprint string                         `json:"expectedBasePermissionFingerprint,omitempty"`
	ExpectedPermissionFingerprint     string                         `json:"expectedPermissionFingerprint,omitempty"`
	ExpectedIdentityDigest            string                         `json:"expectedIdentityDigest,omitempty"`
	UnverifiedAppTrust                []HostAppUnverifiedTrustReview `json:"unverifiedAppTrust,omitempty"`
	SafetyProfileID                   string                         `json:"safetyProfileId,omitempty"`
	SafetyProfileVersion              string                         `json:"safetyProfileVersion,omitempty"`
	QualityTestStatus                 string                         `json:"qualityTestStatus,omitempty"`
	PermissionDiff                    hostapppack.PermissionDiff     `json:"permissionDiff,omitempty"`
	PermissionChanged                 bool                           `json:"permissionChanged,omitempty"`
	ExpectedPackState                 string                         `json:"expectedPackState,omitempty"`
	ExpectedEnablementState           string                         `json:"expectedEnablementState,omitempty"`
	Reason                            string                         `json:"reason,omitempty"`
	RecoveryCodes                     []string                       `json:"recoveryCodes,omitempty"`
	Review                            HostAppPackReview              `json:"review"`
	Status                            string                         `json:"status"`
	Message                           string                         `json:"message"`
}

type HostAppPackResult struct {
	Version    string                     `json:"version"`
	Plan       HostAppPackPlan            `json:"plan"`
	Applied    bool                       `json:"applied"`
	Entry      *hostapppack.RegistryEntry `json:"entry,omitempty"`
	Revision   *hostapppack.Revision      `json:"revision,omitempty"`
	Test       *hostapppack.TestResult    `json:"test,omitempty"`
	Enablement *hostapppack.Enablement    `json:"enablement,omitempty"`
}

type HostAppPackSummary struct {
	PackID           string `json:"packId"`
	State            string `json:"state"`
	ActiveRevisionID string `json:"activeRevisionId,omitempty"`
	RevisionCount    int    `json:"revisionCount"`
	BuiltIn          bool   `json:"builtIn,omitempty"`
}

type HostAppPackInspection struct {
	Summary     HostAppPackSummary       `json:"summary"`
	Manifest    hostapppack.Manifest     `json:"-"`
	Revision    hostapppack.Revision     `json:"-"`
	Test        *hostapppack.TestResult  `json:"-"`
	Enablements []hostapppack.Enablement `json:"-"`
	Status      hostapppack.Inspection   `json:"status"`
	Recovery    []string                 `json:"recoveryCodes,omitempty"`
}

// HostAppLifecycleError carries a stable recovery code without requiring a
// CLI, Manager client, or doctor surface to parse provider prose.
type HostAppLifecycleError struct {
	Code string
	Err  error
}

func (e *HostAppLifecycleError) Error() string {
	if e == nil || e.Err == nil {
		return "host-app lifecycle failed"
	}
	if e.Code == "" {
		return e.Err.Error()
	}
	return e.Code + ": " + e.Err.Error()
}

func (e *HostAppLifecycleError) Unwrap() error { return e.Err }

func HostAppRecoveryCode(err error) string {
	var lifecycleErr *HostAppLifecycleError
	if errors.As(err, &lifecycleErr) {
		return lifecycleErr.Code
	}
	return ""
}

func hostAppLifecycleError(code string, err error) error {
	if err == nil {
		return nil
	}
	return &HostAppLifecycleError{Code: code, Err: err}
}

func (c Core) hostAppPackStore() hostapppack.Store { return hostapppack.NewStore(c.Store.Root) }

func (c Core) PlanHostAppPack(opts HostAppPackOptions) (HostAppPackPlan, error) {
	op := strings.TrimSpace(opts.Operation)
	plan := HostAppPackPlan{Version: HostAppPackPlanVersion, Operation: op, Status: "pending", Replacements: cloneStringMap(opts.Replacements)}
	switch op {
	case "add":
		source := hostapppack.SourceSpec{Kind: opts.SourceKind, Path: opts.SourcePath, URL: opts.SourceURL, Commit: opts.SourceCommit}
		if source.Kind == "" {
			source.Kind = hostapppack.SourceLocal
		}
		manifest, snapshot, fingerprint, err := inspectHostAppSource(source)
		if err != nil {
			return HostAppPackPlan{}, classifyHostAppSourceError(err, source, c.Store.Root)
		}
		if opts.ExpectedDigest != "" && opts.ExpectedDigest != snapshot.Digest {
			return HostAppPackPlan{}, hostAppLifecycleError(recovery.CodeHostAppDigestMismatch, fmt.Errorf("host-app source digest mismatch: expected %s got %s", opts.ExpectedDigest, snapshot.Digest))
		}
		if err := c.requireNewHostAppPack(manifest.ID); err != nil {
			return HostAppPackPlan{}, err
		}
		plan.Source, plan.SourceReview, plan.PackID = source, hostAppSourceReview(source), manifest.ID
		plan.RevisionID = packsnapshot.RevisionID(snapshot.Digest)
		plan.InstallOnly = opts.InstallOnly
		plan.ExpectedSourceDigest = snapshot.Digest
		plan.ExpectedBasePermissionFingerprint = fingerprint
		plan.ExpectedPermissionFingerprint = fingerprint
		plan.Review = hostAppReview(manifest, snapshot.Digest, fingerprint)
		plan.Review.ApplicationsObserved = c.observeHostAppReviewIdentities(manifest, plan.RevisionID, defaultHostAppInspectionProfile(opts.ProfileName), source)
		if opts.InstallOnly {
			plan.Message = "install an inert immutable host-app pack revision"
			break
		}
		quality, err := hostapppack.RunQualityTests(manifest, plan.RevisionID, time.Now().UTC())
		if err != nil {
			return HostAppPackPlan{}, err
		}
		plan.QualityTestStatus = quality.Status
		candidate := hostapppack.Revision{
			RevisionID: plan.RevisionID, PackID: manifest.ID, SourceDigest: snapshot.Digest,
			BasePermissionFingerprint: fingerprint,
		}
		plan, err = c.populateHostAppEnablePlan(opts, plan, manifest, candidate)
		if err != nil {
			return HostAppPackPlan{}, err
		}
		plan.Operation, plan.Source, plan.InstallOnly = "add", source, false
		plan.Message = "test, install, and enable exact host-app bindings for future runs only"
	case "validate", "test":
		if hostAppSourceOptionsPresent(opts) {
			source := hostapppack.SourceSpec{Kind: opts.SourceKind, Path: opts.SourcePath, URL: opts.SourceURL, Commit: opts.SourceCommit}
			if source.Kind == "" {
				source.Kind = hostapppack.SourceLocal
			}
			manifest, snapshot, fingerprint, err := inspectHostAppSource(source)
			if err != nil {
				return HostAppPackPlan{}, classifyHostAppSourceError(err, source, c.Store.Root)
			}
			if opts.ExpectedDigest != "" && opts.ExpectedDigest != snapshot.Digest {
				return HostAppPackPlan{}, hostAppLifecycleError(recovery.CodeHostAppDigestMismatch, fmt.Errorf("host-app source digest mismatch: expected %s got %s", opts.ExpectedDigest, snapshot.Digest))
			}
			plan.Source, plan.SourceReview = source, hostAppSourceReview(source)
			plan.PackID, plan.RevisionID = manifest.ID, packsnapshot.RevisionID(snapshot.Digest)
			plan.ExpectedSourceDigest = snapshot.Digest
			plan.ExpectedBasePermissionFingerprint = fingerprint
			plan.ExpectedPermissionFingerprint = fingerprint
			plan.Review = hostAppReview(manifest, snapshot.Digest, fingerprint)
			plan.Review.ApplicationsObserved = c.observeHostAppReviewIdentities(manifest, plan.RevisionID, defaultHostAppInspectionProfile(opts.ProfileName), source)
			plan.Message = op + " immutable candidate snapshot without installing or enabling it"
			break
		}
		entry, revision, manifest, err := c.resolveHostAppRevision(opts.PackID, opts.RevisionID)
		if err != nil {
			return HostAppPackPlan{}, err
		}
		plan.PackID, plan.RevisionID = entry.ID, revision.RevisionID
		plan.ExpectedSourceDigest = revision.SourceDigest
		plan.ExpectedBasePermissionFingerprint = revision.BasePermissionFingerprint
		plan.ExpectedPermissionFingerprint = revision.BasePermissionFingerprint
		plan.Review = hostAppReview(manifest, revision.SourceDigest, revision.BasePermissionFingerprint)
		plan.Review.ApplicationsObserved = c.observeHostAppReviewIdentities(manifest, revision.RevisionID, defaultHostAppInspectionProfile(opts.ProfileName), hostapppack.SourceSpec{
			Kind: revision.Source.Kind, Path: revision.Source.LocalPath, URL: revision.Source.URL, Commit: revision.Source.Commit,
		})
		plan.Message = op + " exact installed host-app revision"
	case "enable":
		return c.planHostAppEnable(opts, plan)
	case "update":
		return c.planHostAppUpdate(opts, plan)
	case "disable", "revoke", "remove":
		return c.planHostAppStateChange(opts, plan)
	default:
		return HostAppPackPlan{}, fmt.Errorf("unsupported host-app operation %q", op)
	}
	return plan, nil
}

func (c Core) ApplyHostAppPack(plan HostAppPackPlan) (HostAppPackResult, error) {
	if plan.Version != HostAppPackPlanVersion {
		return HostAppPackResult{}, errors.New("invalid host-app plan version")
	}
	result := HostAppPackResult{Version: HostAppPackPlanVersion, Plan: plan}
	auditReadOnlyFailure := func(err error) (HostAppPackResult, error) {
		_ = c.recordHostAppPackAudit(plan.Operation+"-failed", plan, err)
		return HostAppPackResult{}, err
	}
	switch plan.Operation {
	case "add":
		return c.applyHostAppAdd(plan)
	case "validate":
		if plan.Source.Kind != "" {
			manifest, err := c.revalidateHostAppSourcePlan(plan)
			if err != nil {
				return auditReadOnlyFailure(err)
			}
			if manifest.ID != plan.PackID {
				return auditReadOnlyFailure(hostAppLifecycleError(recovery.CodeHostAppDigestMismatch, errors.New("host-app validation plan is stale")))
			}
			result.Applied = true
			break
		}
		_, revision, _, err := c.resolveHostAppRevision(plan.PackID, plan.RevisionID)
		if err != nil || revision.SourceDigest != plan.ExpectedSourceDigest ||
			revision.BasePermissionFingerprint != plan.ExpectedBasePermissionFingerprint ||
			revision.BasePermissionFingerprint != plan.ExpectedPermissionFingerprint {
			if err == nil {
				err = errors.New("host-app validation plan is stale")
			}
			return auditReadOnlyFailure(err)
		}
		revision = publicHostAppRevision(revision)
		result.Applied, result.Revision = true, &revision
	case "test":
		if plan.Source.Kind != "" {
			manifest, err := c.revalidateHostAppSourcePlan(plan)
			if err != nil {
				return auditReadOnlyFailure(err)
			}
			testResult, err := hostapppack.RunQualityTests(manifest, plan.RevisionID, time.Now().UTC())
			if err != nil {
				return auditReadOnlyFailure(err)
			}
			result.Applied, result.Test = true, &testResult
			break
		}
		entry, revision, manifest, err := c.resolveHostAppRevision(plan.PackID, plan.RevisionID)
		if err != nil {
			return auditReadOnlyFailure(err)
		}
		if revision.SourceDigest != plan.ExpectedSourceDigest ||
			revision.BasePermissionFingerprint != plan.ExpectedBasePermissionFingerprint ||
			revision.BasePermissionFingerprint != plan.ExpectedPermissionFingerprint {
			return auditReadOnlyFailure(errors.New("host-app test plan is stale"))
		}
		testResult, err := hostapppack.RunQualityTests(manifest, revision.RevisionID, time.Now().UTC())
		if err == nil {
			err = c.hostAppPackStore().SaveTestResult(testResult)
		}
		if err != nil {
			return auditReadOnlyFailure(err)
		}
		entry = publicHostAppRegistryEntry(entry)
		revision = publicHostAppRevision(revision)
		result.Applied, result.Entry, result.Revision, result.Test = true, &entry, &revision, &testResult
	case "enable":
		return c.applyHostAppEnable(plan)
	case "update":
		return c.applyHostAppUpdate(plan)
	case "disable":
		return c.applyHostAppDisable(plan)
	case "revoke":
		return c.applyHostAppRevoke(plan)
	case "remove":
		return c.applyHostAppRemove(plan)
	default:
		return HostAppPackResult{}, fmt.Errorf("unsupported host-app operation %q", plan.Operation)
	}
	_ = c.recordHostAppPackAudit(plan.Operation, plan, nil)
	return result, nil
}

func hostAppSourceOptionsPresent(opts HostAppPackOptions) bool {
	return strings.TrimSpace(opts.SourceKind) != "" || strings.TrimSpace(opts.SourcePath) != "" ||
		strings.TrimSpace(opts.SourceURL) != "" || strings.TrimSpace(opts.SourceCommit) != ""
}

func (c Core) revalidateHostAppSourcePlan(plan HostAppPackPlan) (hostapppack.Manifest, error) {
	manifest, snapshot, fingerprint, err := inspectHostAppSource(plan.Source)
	if err != nil {
		return hostapppack.Manifest{}, classifyHostAppSourceError(err, plan.Source, c.Store.Root)
	}
	if manifest.ID != plan.PackID || packsnapshot.RevisionID(snapshot.Digest) != plan.RevisionID ||
		snapshot.Digest != plan.ExpectedSourceDigest ||
		fingerprint != plan.ExpectedBasePermissionFingerprint ||
		fingerprint != plan.ExpectedPermissionFingerprint {
		return hostapppack.Manifest{}, hostAppLifecycleError(recovery.CodeHostAppDigestMismatch, errors.New("host-app source plan is stale"))
	}
	return manifest, nil
}

func (c Core) planHostAppUpdate(opts HostAppPackOptions, plan HostAppPackPlan) (HostAppPackPlan, error) {
	source := hostapppack.SourceSpec{Kind: opts.SourceKind, Path: opts.SourcePath, URL: opts.SourceURL, Commit: opts.SourceCommit}
	if source.Kind == "" {
		source.Kind = hostapppack.SourceLocal
	}
	manifest, snapshot, baseFingerprint, err := inspectHostAppSource(source)
	if err != nil {
		return HostAppPackPlan{}, classifyHostAppSourceError(err, source, c.Store.Root)
	}
	packID := strings.TrimSpace(opts.PackID)
	if packID == "" {
		packID = manifest.ID
	}
	if manifest.ID != packID {
		return HostAppPackPlan{}, hostAppLifecycleError(recovery.CodeHostAppSourceInvalid, fmt.Errorf("host-app update source contains pack %q, expected %q", manifest.ID, packID))
	}
	if opts.ExpectedDigest != "" && opts.ExpectedDigest != snapshot.Digest {
		return HostAppPackPlan{}, hostAppLifecycleError(recovery.CodeHostAppDigestMismatch, fmt.Errorf("host-app source digest mismatch: expected %s got %s", opts.ExpectedDigest, snapshot.Digest))
	}
	profileName, err := normalizeManagerProfileName(opts.ProfileName)
	if err != nil {
		return HostAppPackPlan{}, err
	}
	priorEnablement, err := c.hostAppPackStore().LoadEnablement(profileName, packID)
	if err != nil {
		return HostAppPackPlan{}, hostAppLifecycleError(recovery.CodeHostAppPermissionReviewRequired, fmt.Errorf("host-app update requires an exact profile enablement: %w", err))
	}
	if priorEnablement.State != hostapppack.EnablementEnabled && priorEnablement.State != hostapppack.EnablementSuspended {
		return HostAppPackPlan{}, hostAppLifecycleError(recovery.CodeHostAppBindingDisabled, fmt.Errorf("host-app update cannot inherit %s enablement", priorEnablement.State))
	}
	_, priorRevision, priorManifest, err := c.resolveHostAppRevision(packID, priorEnablement.RevisionID)
	if err != nil {
		return HostAppPackPlan{}, hostAppLifecycleError(recovery.CodeHostAppBindingRevoked, err)
	}
	priorContext, err := effectivePermissionContextForEnablement(priorManifest, priorEnablement)
	if err != nil {
		return HostAppPackPlan{}, hostAppLifecycleError(recovery.CodeHostAppPermissionReviewRequired, err)
	}
	quality, err := hostapppack.RunQualityTests(manifest, packsnapshot.RevisionID(snapshot.Digest), time.Now().UTC())
	if err != nil {
		return HostAppPackPlan{}, err
	}
	updateOpts := opts
	updateOpts.ProfileName = profileName
	if len(updateOpts.BindingIDs) == 0 {
		updateOpts.BindingIDs = append([]string(nil), priorEnablement.BindingIDs...)
	}
	if strings.TrimSpace(updateOpts.Access) == "" {
		updateOpts.Access = priorEnablement.Access
	}
	if len(updateOpts.Replacements) == 0 {
		updateOpts.Replacements = cloneStringMap(priorEnablement.ConflictReplacements)
	}
	candidate := hostapppack.Revision{
		RevisionID: packsnapshot.RevisionID(snapshot.Digest), PackID: manifest.ID,
		SourceDigest: snapshot.Digest, BasePermissionFingerprint: baseFingerprint,
	}
	plan.QualityTestStatus = quality.Status
	plan.Source = source
	plan, err = c.populateHostAppEnablePlan(updateOpts, plan, manifest, candidate)
	if err != nil {
		return HostAppPackPlan{}, err
	}
	afterContext := hostAppEffectivePermissionContext(
		plan.Access, plan.SafetyProfileID, plan.SafetyProfileVersion, plan.BindingIDs, plan.Replacements,
	)
	diff, err := hostapppack.DiffEffectivePermissions(priorManifest, priorContext, manifest, afterContext)
	if err != nil {
		return HostAppPackPlan{}, err
	}
	plan.Operation = "update"
	plan.Source = source
	plan.SourceReview = hostAppSourceReview(source)
	plan.PreviousRevisionID = priorRevision.RevisionID
	plan.ExpectedSourceDigest = snapshot.Digest
	plan.ExpectedBasePermissionFingerprint = baseFingerprint
	plan.PermissionDiff = diff
	plan.PermissionChanged = diff.TotalChanges > 0 || priorEnablement.PermissionFingerprint != plan.ExpectedPermissionFingerprint
	plan.ExpectedPackState = hostapppack.PackInstalled
	plan.ExpectedEnablementState = priorEnablement.State
	plan.Reason = boundedHostAppReason(opts.Reason, "operator accepted exact host-app update for future runs")
	plan.RecoveryCodes = []string{recovery.CodeHostAppNewRunRequired}
	if plan.PermissionChanged {
		plan.RecoveryCodes = append(plan.RecoveryCodes, recovery.CodeHostAppPermissionReviewRequired)
	}
	plan.Message = "install and select one exact reviewed host-app revision for future runs only"
	return plan, nil
}

func (c Core) planHostAppStateChange(opts HostAppPackOptions, plan HostAppPackPlan) (HostAppPackPlan, error) {
	packID := strings.TrimSpace(opts.PackID)
	if packID == "" {
		return HostAppPackPlan{}, errors.New("host-app pack id is required")
	}
	builtin, err := builtinHostAppManifest()
	if err != nil {
		return HostAppPackPlan{}, err
	}
	if packID == builtin.ID {
		return HostAppPackPlan{}, errors.New("the built-in host-app pack cannot be changed through the community lifecycle")
	}
	registry, err := c.hostAppPackStore().LoadRegistry()
	if err != nil {
		return HostAppPackPlan{}, err
	}
	entry, ok := findHostAppRegistryEntry(registry, packID)
	if !ok {
		return HostAppPackPlan{}, fmt.Errorf("host-app pack %q is not installed", packID)
	}
	plan.PackID = packID
	plan.ExpectedPackState = entry.State
	plan.Reason = boundedHostAppReason(opts.Reason, "operator changed host-app lifecycle state")
	plan.RecoveryCodes = []string{recovery.CodeHostAppNewRunRequired}
	switch plan.Operation {
	case "disable":
		profileName, err := normalizeManagerProfileName(opts.ProfileName)
		if err != nil {
			return HostAppPackPlan{}, err
		}
		enablement, err := c.hostAppPackStore().LoadEnablement(profileName, packID)
		if err != nil {
			return HostAppPackPlan{}, hostAppLifecycleError(recovery.CodeHostAppBindingDisabled, err)
		}
		if opts.RevisionID != "" && opts.RevisionID != enablement.RevisionID {
			return HostAppPackPlan{}, errors.New("host-app disable revision does not match the profile enablement")
		}
		if enablement.State == hostapppack.EnablementRevoked {
			return HostAppPackPlan{}, hostAppLifecycleError(recovery.CodeHostAppBindingRevoked, errors.New("revoked host-app enablement is terminal"))
		}
		_, revision, manifest, err := c.resolveHostAppRevision(packID, enablement.RevisionID)
		if err != nil {
			return HostAppPackPlan{}, hostAppLifecycleError(recovery.CodeHostAppBindingRevoked, err)
		}
		context, err := effectivePermissionContextForEnablement(manifest, enablement)
		if err != nil {
			return HostAppPackPlan{}, hostAppLifecycleError(recovery.CodeHostAppPermissionReviewRequired, err)
		}
		plan.Profile = profileName
		plan.RevisionID = revision.RevisionID
		plan.BindingIDs = append([]string(nil), enablement.BindingIDs...)
		plan.Access = enablement.Access
		plan.Replacements = cloneStringMap(enablement.ConflictReplacements)
		plan.ExpectedSourceDigest = enablement.SourceDigest
		plan.ExpectedBasePermissionFingerprint = enablement.BasePermissionFingerprint
		plan.ExpectedPermissionFingerprint = enablement.PermissionFingerprint
		plan.ExpectedIdentityDigest = enablement.ObservedIdentityDigest
		plan.ExpectedEnablementState = enablement.State
		plan.SafetyProfileID = context.SafetyProfileID
		plan.SafetyProfileVersion = context.SafetyProfileVersion
		plan.Review = hostAppReview(manifest, revision.SourceDigest, enablement.PermissionFingerprint)
		source := hostAppSourceSpecFromRevision(revision)
		plan.SourceReview = hostAppSourceReview(source)
		plan.Review.ApplicationsObserved = c.observeHostAppReviewIdentities(manifest, revision.RevisionID, profileName, source)
		plan.Message = "disable exact profile bindings for future runs; running sessions retain only their immutable shim inventory"
		plan.RecoveryCodes = append(plan.RecoveryCodes, recovery.CodeHostAppBindingDisabled)
	case "revoke":
		revisionID := strings.TrimSpace(opts.RevisionID)
		if revisionID == "" {
			revisionID = entry.ActiveRevisionID
		}
		revision, ok := findHostAppRevision(entry, revisionID)
		if !ok {
			return HostAppPackPlan{}, fmt.Errorf("host-app revision %q is not installed", revisionID)
		}
		if revision.State == hostapppack.RevisionRevoked {
			return HostAppPackPlan{}, hostAppLifecycleError(recovery.CodeHostAppBindingRevoked, errors.New("host-app revision is already revoked"))
		}
		plan.RevisionID = revision.RevisionID
		plan.ExpectedSourceDigest = revision.SourceDigest
		plan.ExpectedBasePermissionFingerprint = revision.BasePermissionFingerprint
		c.populateBestEffortHostAppStateReview(&plan, revision, defaultHostAppInspectionProfile(opts.ProfileName))
		plan.Message = "terminally revoke the exact revision across every profile and future launch"
		plan.RecoveryCodes = append(plan.RecoveryCodes, recovery.CodeHostAppBindingRevoked)
	case "remove":
		if entry.State == hostapppack.PackRemoved {
			return HostAppPackPlan{}, hostAppLifecycleError(recovery.CodeHostAppBindingRevoked, errors.New("host-app pack already has a removal tombstone"))
		}
		plan.RevisionID = entry.ActiveRevisionID
		if plan.RevisionID == "" && len(entry.Revisions) > 0 {
			plan.RevisionID = entry.Revisions[len(entry.Revisions)-1].RevisionID
		}
		if revision, found := findHostAppRevision(entry, plan.RevisionID); found {
			plan.ExpectedSourceDigest = revision.SourceDigest
			plan.ExpectedBasePermissionFingerprint = revision.BasePermissionFingerprint
			c.populateBestEffortHostAppStateReview(&plan, revision, defaultHostAppInspectionProfile(opts.ProfileName))
		}
		plan.Message = "disable every profile binding, delete only package-owned snapshots, and retain a tombstone and audit"
		plan.RecoveryCodes = append(plan.RecoveryCodes, recovery.CodeHostAppBindingRevoked)
	default:
		return HostAppPackPlan{}, fmt.Errorf("unsupported host-app state operation %q", plan.Operation)
	}
	return plan, nil
}

func (c Core) planHostAppEnable(opts HostAppPackOptions, plan HostAppPackPlan) (HostAppPackPlan, error) {
	entry, revision, manifest, err := c.resolveHostAppRevision(opts.PackID, opts.RevisionID)
	if err != nil {
		return HostAppPackPlan{}, err
	}
	qualityStatus := revision.TestStatus
	if qualityStatus == "" {
		qualityStatus = hostapppack.TestNotRun
	}
	if testResult, loadErr := c.hostAppPackStore().LoadTestResult(entry.ID, revision.RevisionID); loadErr == nil {
		qualityStatus = testResult.Status
	}
	if qualityStatus != hostapppack.TestPassed && qualityStatus != hostapppack.TestNotRun && qualityStatus != hostapppack.TestFailed {
		return HostAppPackPlan{}, fmt.Errorf("host-app pack %q revision %q has invalid quality status %q", entry.ID, revision.RevisionID, qualityStatus)
	}
	plan.QualityTestStatus = qualityStatus
	return c.populateHostAppEnablePlan(opts, plan, manifest, revision)
}

func (c Core) populateHostAppEnablePlan(opts HostAppPackOptions, plan HostAppPackPlan, manifest hostapppack.Manifest, revision hostapppack.Revision) (HostAppPackPlan, error) {
	profileName, err := normalizeManagerProfileName(opts.ProfileName)
	if err != nil {
		return HostAppPackPlan{}, err
	}
	bindingIDs := sortedUnique(opts.BindingIDs)
	if len(bindingIDs) == 0 {
		for _, binding := range manifest.Bindings {
			bindingIDs = append(bindingIDs, binding.ID)
		}
		sort.Strings(bindingIDs)
	}
	selected, err := selectHostAppBindings(manifest, bindingIDs)
	if err != nil {
		return HostAppPackPlan{}, err
	}
	access := strings.TrimSpace(opts.Access)
	if access == "" {
		access = selected[0].RequestedAccess
	}
	for _, binding := range selected {
		if binding.RequestedAccess != access {
			return HostAppPackPlan{}, errors.New("selected host-app bindings require one exact access posture")
		}
	}
	source := plan.Source
	if source.Kind == "" {
		source = hostapppack.SourceSpec{
			Kind: revision.Source.Kind, Path: revision.Source.LocalPath,
			URL: revision.Source.URL, Commit: revision.Source.Commit,
		}
	}
	forbidden, err := c.hostAppForbiddenOverlapRoots(profileName, source)
	if err != nil {
		return HostAppPackPlan{}, classifyHostAppIdentityError(err)
	}
	identities, safetyID, safetyVersion, err := c.resolveManifestIdentities(manifest, revision.RevisionID, selected, access, forbidden)
	if err != nil {
		if HostAppRecoveryCode(err) != "" {
			return HostAppPackPlan{}, err
		}
		return HostAppPackPlan{}, classifyHostAppIdentityError(err)
	}
	identityDigest := combinedIdentityDigest(identities)
	context := hostAppEffectivePermissionContext(access, safetyID, safetyVersion, bindingIDs, opts.Replacements)
	permission, err := hostapppack.EffectivePermissionFingerprint(manifest, context)
	if err != nil {
		return HostAppPackPlan{}, err
	}
	commandPlan, err := c.planHostAppCommandOwners(profileName, manifest, revision.RevisionID, selected, opts.Replacements)
	if err != nil {
		return HostAppPackPlan{}, err
	}
	plan.Profile, plan.PackID, plan.RevisionID = profileName, manifest.ID, revision.RevisionID
	plan.BindingIDs, plan.Access, plan.Replacements = bindingIDs, access, cloneStringMap(opts.Replacements)
	plan.CommandPlan = commandPlan
	plan.ExpectedSourceDigest = revision.SourceDigest
	plan.ExpectedBasePermissionFingerprint = revision.BasePermissionFingerprint
	plan.ExpectedPermissionFingerprint = permission
	plan.ExpectedIdentityDigest, plan.SafetyProfileID, plan.SafetyProfileVersion = identityDigest, safetyID, safetyVersion
	plan.UnverifiedAppTrust = unverifiedAppTrustReview(identities)
	plan.Review = hostAppReview(manifest, revision.SourceDigest, permission)
	plan.Review.ApplicationsObserved = hostAppIdentityReviews(manifest, revision.RevisionID, identities)
	plan.Message = "enable exact host-app bindings for future runs only"
	return plan, nil
}

func (c Core) applyHostAppAdd(plan HostAppPackPlan) (HostAppPackResult, error) {
	replan := func() (HostAppPackPlan, error) {
		return c.PlanHostAppPack(HostAppPackOptions{
			Operation: "add", SourceKind: plan.Source.Kind, SourcePath: plan.Source.Path,
			SourceURL: plan.Source.URL, SourceCommit: plan.Source.Commit, ProfileName: plan.Profile,
			BindingIDs: append([]string(nil), plan.BindingIDs...), Access: plan.Access,
			Replacements: cloneStringMap(plan.Replacements), ExpectedDigest: plan.ExpectedSourceDigest,
			InstallOnly: plan.InstallOnly,
		})
	}
	checked, err := replan()
	if err != nil {
		err = sanitizeHostAppSourceError(err, plan.Source, c.Store.Root)
		_ = c.recordHostAppPackAudit("add-failed", plan, err)
		return HostAppPackResult{}, err
	}
	if !sameHostAppAddAuthority(plan, checked) {
		err := errors.New("host-app add plan became stale")
		_ = c.recordHostAppPackAudit("add-failed", plan, err)
		return HostAppPackResult{}, err
	}
	if checked.InstallOnly {
		entry, revision, err := c.hostAppPackStore().Install(hostapppack.InstallRequest{
			Source: checked.Source, ExpectedSourceDigest: checked.ExpectedSourceDigest,
			ExpectedBasePermissionFingerprint: checked.ExpectedBasePermissionFingerprint,
		})
		if err != nil {
			err = sanitizeHostAppSourceError(err, checked.Source, c.Store.Root)
			_ = c.recordHostAppPackAudit("add-install-only-failed", checked, err)
			return HostAppPackResult{}, err
		}
		entry = publicHostAppRegistryEntry(entry)
		revision = publicHostAppRevision(revision)
		result := HostAppPackResult{Version: HostAppPackPlanVersion, Plan: checked, Applied: true, Entry: &entry, Revision: &revision}
		_ = c.recordHostAppPackAudit("add-install-only", checked, nil)
		return result, nil
	}

	var result HostAppPackResult
	err = c.withProfileMutationLock(checked.Profile, func() error {
		revalidated, err := replan()
		if err != nil {
			return sanitizeHostAppSourceError(err, checked.Source, c.Store.Root)
		}
		if !sameHostAppAddAuthority(checked, revalidated) {
			return errors.New("host-app add plan became stale while acquiring profile lock")
		}
		now := time.Now().UTC()
		enablement := hostAppEnablementFromPlan(revalidated, now, "operator accepted guided add for future runs")
		context := hostAppEffectivePermissionContext(
			revalidated.Access, revalidated.SafetyProfileID, revalidated.SafetyProfileVersion, revalidated.BindingIDs, revalidated.Replacements,
		)
		entry, revision, testResult, err := c.hostAppPackStore().InstallTestEnable(hostapppack.InstallRequest{
			Source: revalidated.Source, ExpectedSourceDigest: revalidated.ExpectedSourceDigest,
			ExpectedBasePermissionFingerprint: revalidated.ExpectedBasePermissionFingerprint,
		}, enablement, context, now)
		if err != nil {
			return sanitizeHostAppSourceError(err, revalidated.Source, c.Store.Root)
		}
		entry = publicHostAppRegistryEntry(entry)
		revision = publicHostAppRevision(revision)
		result = HostAppPackResult{
			Version: HostAppPackPlanVersion, Plan: revalidated, Applied: true,
			Entry: &entry, Revision: &revision, Test: &testResult, Enablement: &enablement,
		}
		return nil
	})
	if err != nil {
		_ = c.recordHostAppPackAudit("add-failed", checked, err)
		return HostAppPackResult{}, err
	}
	_ = c.recordHostAppPackAudit("add", result.Plan, nil)
	return result, nil
}

func sameHostAppAddAuthority(a, b HostAppPackPlan) bool {
	return a.Operation == b.Operation && a.InstallOnly == b.InstallOnly && a.Profile == b.Profile &&
		a.PackID == b.PackID && a.RevisionID == b.RevisionID && a.Access == b.Access &&
		a.ExpectedSourceDigest == b.ExpectedSourceDigest &&
		a.ExpectedBasePermissionFingerprint == b.ExpectedBasePermissionFingerprint &&
		a.ExpectedPermissionFingerprint == b.ExpectedPermissionFingerprint &&
		a.ExpectedIdentityDigest == b.ExpectedIdentityDigest &&
		reflect.DeepEqual(a.UnverifiedAppTrust, b.UnverifiedAppTrust) &&
		reflect.DeepEqual(a.BindingIDs, b.BindingIDs) && reflect.DeepEqual(a.Replacements, b.Replacements) &&
		reflect.DeepEqual(a.CommandPlan, b.CommandPlan)
}

func hostAppEnablementFromPlan(plan HostAppPackPlan, enabledAt time.Time, reason string) hostapppack.Enablement {
	trust := make([]hostapppack.UnverifiedAppTrust, 0, len(plan.UnverifiedAppTrust))
	for _, reviewed := range plan.UnverifiedAppTrust {
		trust = append(trust, hostapppack.UnverifiedAppTrust{
			Schema: hostapppack.UnverifiedAppTrustVersion, QualifiedAppRef: reviewed.QualifiedAppRef,
			RootClass: reviewed.RootClass, CanonicalPathDigest: reviewed.CanonicalPathDigest,
			ContentDigest: reviewed.ContentDigest, IdentityDigest: reviewed.IdentityDigest,
			AcceptedAt: enabledAt.UTC(),
		})
	}
	hostapppack.SortUnverifiedAppTrust(trust)
	return hostapppack.Enablement{
		Schema: hostapppack.EnablementVersion, Profile: plan.Profile, PackID: plan.PackID, RevisionID: plan.RevisionID,
		BindingIDs: append([]string(nil), plan.BindingIDs...), SourceDigest: plan.ExpectedSourceDigest,
		BasePermissionFingerprint: plan.ExpectedBasePermissionFingerprint, PermissionFingerprint: plan.ExpectedPermissionFingerprint,
		Access: plan.Access, ObservedIdentityDigest: plan.ExpectedIdentityDigest, UnverifiedAppTrust: trust,
		ConflictReplacements: cloneStringMap(plan.Replacements), EnabledAt: enabledAt.UTC(),
		State: hostapppack.EnablementEnabled, Reason: reason,
	}
}

func (c Core) applyHostAppEnable(plan HostAppPackPlan) (HostAppPackResult, error) {
	var result HostAppPackResult
	err := c.withProfileMutationLock(plan.Profile, func() error {
		checked, err := c.PlanHostAppPack(HostAppPackOptions{Operation: "enable", ProfileName: plan.Profile, PackID: plan.PackID, RevisionID: plan.RevisionID, BindingIDs: plan.BindingIDs, Access: plan.Access, Replacements: plan.Replacements})
		if err != nil {
			return err
		}
		if !sameHostAppEnableAuthority(plan, checked) {
			return errors.New("host-app enable plan became stale")
		}
		enablement := hostAppEnablementFromPlan(checked, time.Now().UTC(), "operator enabled exact revision for future runs")
		context := hostAppEffectivePermissionContext(
			checked.Access, checked.SafetyProfileID, checked.SafetyProfileVersion, checked.BindingIDs, checked.Replacements,
		)
		revision, _, err := c.hostAppPackStore().SaveEnablementSnapshot(enablement, context)
		if err != nil {
			return err
		}
		revision = publicHostAppRevision(revision)
		result = HostAppPackResult{Version: HostAppPackPlanVersion, Plan: checked, Applied: true, Enablement: &enablement, Revision: &revision}
		return nil
	})
	if err != nil {
		_ = c.recordHostAppPackAudit("enable-failed", plan, err)
		return HostAppPackResult{}, err
	}
	_ = c.recordHostAppPackAudit("enable", result.Plan, nil)
	return result, nil
}

func (c Core) applyHostAppUpdate(plan HostAppPackPlan) (HostAppPackResult, error) {
	replan := func() (HostAppPackPlan, error) {
		return c.PlanHostAppPack(HostAppPackOptions{
			Operation: "update", SourceKind: plan.Source.Kind, SourcePath: plan.Source.Path,
			SourceURL: plan.Source.URL, SourceCommit: plan.Source.Commit, ProfileName: plan.Profile,
			PackID: plan.PackID, BindingIDs: append([]string(nil), plan.BindingIDs...), Access: plan.Access,
			Replacements: cloneStringMap(plan.Replacements), ExpectedDigest: plan.ExpectedSourceDigest, Reason: plan.Reason,
		})
	}
	checked, err := replan()
	if err != nil {
		err = classifyHostAppSourceError(err, plan.Source, c.Store.Root)
		_ = c.recordHostAppPackAudit("update-failed", plan, err)
		return HostAppPackResult{}, err
	}
	if !sameHostAppUpdateAuthority(plan, checked) {
		err := hostAppLifecycleError(recovery.CodeHostAppPermissionReviewRequired, errors.New("host-app update plan became stale"))
		_ = c.recordHostAppPackAudit("update-failed", plan, err)
		return HostAppPackResult{}, err
	}
	var result HostAppPackResult
	err = c.withProfileMutationLock(checked.Profile, func() error {
		revalidated, err := replan()
		if err != nil {
			return err
		}
		if !sameHostAppUpdateAuthority(checked, revalidated) {
			return hostAppLifecycleError(recovery.CodeHostAppPermissionReviewRequired, errors.New("host-app update plan became stale while acquiring profile lock"))
		}
		now := time.Now().UTC()
		enablement := hostAppEnablementFromPlan(revalidated, now, revalidated.Reason)
		context := hostAppEffectivePermissionContext(
			revalidated.Access, revalidated.SafetyProfileID, revalidated.SafetyProfileVersion, revalidated.BindingIDs, revalidated.Replacements,
		)
		entry, revision, testResult, err := c.hostAppPackStore().InstallTestEnable(hostapppack.InstallRequest{
			Source: revalidated.Source, ExpectedSourceDigest: revalidated.ExpectedSourceDigest,
			ExpectedBasePermissionFingerprint: revalidated.ExpectedBasePermissionFingerprint,
		}, enablement, context, now)
		if err != nil {
			return classifyHostAppSourceError(err, revalidated.Source, c.Store.Root)
		}
		entry = publicHostAppRegistryEntry(entry)
		revision = publicHostAppRevision(revision)
		result = HostAppPackResult{
			Version: HostAppPackPlanVersion, Plan: revalidated, Applied: true,
			Entry: &entry, Revision: &revision, Test: &testResult, Enablement: &enablement,
		}
		return nil
	})
	if err != nil {
		_ = c.recordHostAppPackAudit("update-failed", checked, err)
		return HostAppPackResult{}, err
	}
	if err := c.invalidateHostAppDecisions(checked.PackID, checked.PreviousRevisionID, "package-revision-updated"); err != nil {
		_ = c.recordHostAppPackAudit("update-failed", result.Plan, err)
		return result, err
	}
	if result.Plan.PermissionChanged {
		_ = c.recordHostAppPackAudit("permission-diff", result.Plan, nil)
	}
	_ = c.recordHostAppPackAudit("update", result.Plan, nil)
	return result, nil
}

func sameHostAppUpdateAuthority(a, b HostAppPackPlan) bool {
	return sameHostAppEnableAuthority(a, b) && a.Operation == b.Operation &&
		a.PreviousRevisionID == b.PreviousRevisionID && a.PermissionChanged == b.PermissionChanged &&
		reflect.DeepEqual(a.PermissionDiff, b.PermissionDiff)
}

func sameHostAppEnableAuthority(a, b HostAppPackPlan) bool {
	return a.Profile == b.Profile && a.PackID == b.PackID && a.RevisionID == b.RevisionID &&
		a.Access == b.Access && a.ExpectedSourceDigest == b.ExpectedSourceDigest &&
		a.ExpectedBasePermissionFingerprint == b.ExpectedBasePermissionFingerprint &&
		a.ExpectedPermissionFingerprint == b.ExpectedPermissionFingerprint &&
		a.ExpectedIdentityDigest == b.ExpectedIdentityDigest && a.SafetyProfileID == b.SafetyProfileID &&
		a.SafetyProfileVersion == b.SafetyProfileVersion && reflect.DeepEqual(a.BindingIDs, b.BindingIDs) &&
		reflect.DeepEqual(a.UnverifiedAppTrust, b.UnverifiedAppTrust) && reflect.DeepEqual(a.Replacements, b.Replacements) &&
		reflect.DeepEqual(a.CommandPlan, b.CommandPlan)
}

func (c Core) applyHostAppDisable(plan HostAppPackPlan) (HostAppPackResult, error) {
	var result HostAppPackResult
	err := c.withProfileMutationLock(plan.Profile, func() error {
		checked, err := c.PlanHostAppPack(HostAppPackOptions{
			Operation: "disable", ProfileName: plan.Profile, PackID: plan.PackID,
			RevisionID: plan.RevisionID, Reason: plan.Reason,
		})
		if err != nil {
			return err
		}
		if !sameHostAppStatePlan(plan, checked) {
			return errors.New("host-app disable plan became stale")
		}
		enablement, err := c.hostAppPackStore().LoadEnablement(checked.Profile, checked.PackID)
		if err != nil {
			return err
		}
		enablement.State = hostapppack.EnablementDisabled
		enablement.Reason = checked.Reason
		context := hostAppEffectivePermissionContext(
			checked.Access, checked.SafetyProfileID, checked.SafetyProfileVersion, checked.BindingIDs, checked.Replacements,
		)
		revision, _, err := c.hostAppPackStore().SaveEnablementSnapshot(enablement, context)
		if err != nil {
			return err
		}
		revision = publicHostAppRevision(revision)
		result = HostAppPackResult{
			Version: HostAppPackPlanVersion, Plan: checked, Applied: true,
			Revision: &revision, Enablement: &enablement,
		}
		return nil
	})
	if err != nil {
		_ = c.recordHostAppPackAudit("disable-failed", plan, err)
		return HostAppPackResult{}, err
	}
	if err := c.invalidateHostAppDecisions(plan.PackID, plan.RevisionID, "binding-disabled"); err != nil {
		_ = c.recordHostAppPackAudit("disable-failed", result.Plan, err)
		return result, err
	}
	_ = c.recordHostAppPackAudit("disable", result.Plan, nil)
	return result, nil
}

func (c Core) applyHostAppRevoke(plan HostAppPackPlan) (HostAppPackResult, error) {
	checked, err := c.PlanHostAppPack(HostAppPackOptions{
		Operation: "revoke", PackID: plan.PackID, RevisionID: plan.RevisionID, Reason: plan.Reason,
	})
	if err != nil {
		_ = c.recordHostAppPackAudit("revoke-failed", plan, err)
		return HostAppPackResult{}, err
	}
	if !sameHostAppStatePlan(plan, checked) {
		err := errors.New("host-app revoke plan became stale")
		_ = c.recordHostAppPackAudit("revoke-failed", plan, err)
		return HostAppPackResult{}, err
	}
	if err := c.hostAppPackStore().RevokeRevision(checked.PackID, checked.RevisionID, checked.Reason); err != nil {
		_ = c.recordHostAppPackAudit("revoke-failed", checked, err)
		return HostAppPackResult{}, err
	}
	if err := c.invalidateHostAppDecisions(checked.PackID, checked.RevisionID, "package-revision-revoked"); err != nil {
		_ = c.recordHostAppPackAudit("revoke-failed", checked, err)
		return HostAppPackResult{Version: HostAppPackPlanVersion, Plan: checked, Applied: true}, err
	}
	result := HostAppPackResult{Version: HostAppPackPlanVersion, Plan: checked, Applied: true}
	_ = c.recordHostAppPackAudit("revoke", checked, nil)
	return result, nil
}

func (c Core) applyHostAppRemove(plan HostAppPackPlan) (HostAppPackResult, error) {
	checked, err := c.PlanHostAppPack(HostAppPackOptions{Operation: "remove", PackID: plan.PackID, Reason: plan.Reason})
	if err != nil {
		_ = c.recordHostAppPackAudit("remove-failed", plan, err)
		return HostAppPackResult{}, err
	}
	if !sameHostAppStatePlan(plan, checked) {
		err := errors.New("host-app remove plan became stale")
		_ = c.recordHostAppPackAudit("remove-failed", plan, err)
		return HostAppPackResult{}, err
	}
	if err := c.hostAppPackStore().RemovePack(checked.PackID, checked.Reason); err != nil {
		_ = c.recordHostAppPackAudit("remove-failed", checked, err)
		return HostAppPackResult{}, err
	}
	if err := c.invalidateHostAppDecisions(checked.PackID, "", "package-removed"); err != nil {
		_ = c.recordHostAppPackAudit("remove-failed", checked, err)
		return HostAppPackResult{Version: HostAppPackPlanVersion, Plan: checked, Applied: true}, err
	}
	result := HostAppPackResult{Version: HostAppPackPlanVersion, Plan: checked, Applied: true}
	_ = c.recordHostAppPackAudit("remove", checked, nil)
	return result, nil
}

func sameHostAppStatePlan(a, b HostAppPackPlan) bool {
	return a.Version == b.Version && a.Operation == b.Operation && a.Profile == b.Profile &&
		a.PackID == b.PackID && a.RevisionID == b.RevisionID && a.ExpectedPackState == b.ExpectedPackState &&
		a.ExpectedEnablementState == b.ExpectedEnablementState && a.ExpectedSourceDigest == b.ExpectedSourceDigest &&
		a.ExpectedPermissionFingerprint == b.ExpectedPermissionFingerprint && a.ExpectedIdentityDigest == b.ExpectedIdentityDigest &&
		a.Reason == b.Reason
}

func (c Core) ListHostAppPacks() ([]HostAppPackSummary, error) {
	builtin, err := builtinHostAppManifest()
	if err != nil {
		return nil, err
	}
	registry, err := c.hostAppPackStore().LoadRegistry()
	if err != nil {
		return nil, err
	}
	out := []HostAppPackSummary{{PackID: builtin.ID, State: hostapppack.PackInstalled, ActiveRevisionID: builtinHostAppRevision().RevisionID, RevisionCount: 1, BuiltIn: true}}
	for _, entry := range registry.Packs {
		out = append(out, HostAppPackSummary{PackID: entry.ID, State: entry.State, ActiveRevisionID: entry.ActiveRevisionID, RevisionCount: len(entry.Revisions)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PackID < out[j].PackID })
	return out, nil
}

func (c Core) InspectHostAppPack(packID, profileName string) (HostAppPackInspection, error) {
	manifest, err := builtinHostAppManifest()
	if err != nil {
		return HostAppPackInspection{}, err
	}
	if packID == manifest.ID {
		revision := builtinHostAppRevision()
		status, recoveryCodes, err := c.hostAppInspectionWithRecovery(defaultHostAppInspectionProfile(profileName), packID)
		if err != nil {
			return HostAppPackInspection{}, err
		}
		return HostAppPackInspection{Summary: HostAppPackSummary{PackID: packID, State: hostapppack.PackInstalled, ActiveRevisionID: revision.RevisionID, RevisionCount: 1, BuiltIn: true}, Manifest: manifest, Revision: revision, Enablements: []hostapppack.Enablement{}, Status: status, Recovery: recoveryCodes}, nil
	}
	registry, err := c.hostAppPackStore().LoadRegistry()
	if err != nil {
		return HostAppPackInspection{}, err
	}
	registryEntry, found := findHostAppRegistryEntry(registry, strings.TrimSpace(packID))
	if !found {
		return HostAppPackInspection{}, fmt.Errorf("host-app pack %q is not installed", packID)
	}
	if registryEntry.State == hostapppack.PackRemoved {
		status := hostapppack.Inspection{
			Schema: hostapppack.InspectionVersion, GeneratedAt: time.Now().UTC(), Entries: []hostapppack.InspectionEntry{},
		}
		return HostAppPackInspection{
			Summary: HostAppPackSummary{PackID: registryEntry.ID, State: registryEntry.State, RevisionCount: len(registryEntry.Revisions)},
			Status:  status, Recovery: []string{recovery.CodeHostAppBindingRevoked},
		}, nil
	}
	if registryEntry.State == hostapppack.PackRevoked {
		revisionID := registryEntry.ActiveRevisionID
		if revisionID == "" && len(registryEntry.Revisions) > 0 {
			revisionID = registryEntry.Revisions[len(registryEntry.Revisions)-1].RevisionID
		}
		revision, _ := findHostAppRevision(registryEntry, revisionID)
		manifest, _, _ := hostapppack.LoadManifest(filepath.Join(c.hostAppPackStore().SourceDir(registryEntry.ID, revisionID), hostapppack.ManifestFileName))
		status, recoveryCodes, statusErr := c.hostAppInspectionWithRecovery(defaultHostAppInspectionProfile(profileName), packID)
		if statusErr != nil {
			return HostAppPackInspection{}, statusErr
		}
		return HostAppPackInspection{
			Summary:  HostAppPackSummary{PackID: registryEntry.ID, State: registryEntry.State, RevisionCount: len(registryEntry.Revisions)},
			Manifest: manifest, Revision: publicHostAppRevision(revision), Status: status,
			Recovery: recoveryCodes,
		}, nil
	}
	entry, revision, manifest, err := c.resolveHostAppRevision(packID, "")
	if err != nil {
		return HostAppPackInspection{}, err
	}
	inspection := HostAppPackInspection{Summary: HostAppPackSummary{PackID: entry.ID, State: entry.State, ActiveRevisionID: entry.ActiveRevisionID, RevisionCount: len(entry.Revisions)}, Manifest: manifest, Revision: publicHostAppRevision(revision), Enablements: []hostapppack.Enablement{}}
	if testResult, err := c.hostAppPackStore().LoadTestResult(entry.ID, revision.RevisionID); err == nil {
		inspection.Test = &testResult
	}
	if strings.TrimSpace(profileName) != "" {
		if enablement, err := c.hostAppPackStore().LoadEnablement(profileName, entry.ID); err == nil {
			inspection.Enablements = append(inspection.Enablements, enablement)
		}
	}
	inspection.Status, inspection.Recovery, err = c.hostAppInspectionWithRecovery(defaultHostAppInspectionProfile(profileName), packID)
	if err != nil {
		return HostAppPackInspection{}, err
	}
	return inspection, nil
}

func inspectHostAppSource(source hostapppack.SourceSpec) (hostapppack.Manifest, packsnapshot.Snapshot, string, error) {
	root, err := os.MkdirTemp("", "hideout-host-app-plan-*")
	if err != nil {
		return hostapppack.Manifest{}, packsnapshot.Snapshot{}, "", err
	}
	defer os.RemoveAll(root)
	snapshot, err := packsnapshot.Acquire(packsnapshot.SourceSpec{Kind: source.Kind, Path: source.Path, URL: source.URL, Commit: source.Commit}, filepath.Join(root, "source"), packsnapshot.Options{Limits: packsnapshot.DefaultLimits(), DigestStyle: packsnapshot.DigestCanonicalV1, WorkRoot: root})
	if err != nil {
		return hostapppack.Manifest{}, packsnapshot.Snapshot{}, "", err
	}
	manifest, _, err := hostapppack.LoadManifest(filepath.Join(root, "source", hostapppack.ManifestFileName))
	if err != nil {
		return hostapppack.Manifest{}, packsnapshot.Snapshot{}, "", err
	}
	fingerprint, err := hostapppack.BasePermissionFingerprint(manifest)
	return manifest, snapshot, fingerprint, err
}

func (c Core) resolveHostAppRevision(packID, revisionID string) (hostapppack.RegistryEntry, hostapppack.Revision, hostapppack.Manifest, error) {
	store := c.hostAppPackStore()
	registry, err := store.LoadRegistry()
	if err != nil {
		return hostapppack.RegistryEntry{}, hostapppack.Revision{}, hostapppack.Manifest{}, err
	}
	for _, entry := range registry.Packs {
		if entry.ID != strings.TrimSpace(packID) || entry.State != hostapppack.PackInstalled {
			continue
		}
		if revisionID == "" {
			revisionID = entry.ActiveRevisionID
		}
		revision, manifest, err := store.ResolveRevisionManifest(entry.ID, revisionID)
		if err != nil {
			return hostapppack.RegistryEntry{}, hostapppack.Revision{}, hostapppack.Manifest{}, err
		}
		return entry, revision, manifest, nil
	}
	return hostapppack.RegistryEntry{}, hostapppack.Revision{}, hostapppack.Manifest{}, fmt.Errorf("host-app pack %q is not installed", packID)
}

func selectHostAppBindings(manifest hostapppack.Manifest, ids []string) ([]hostapppack.BindingSpec, error) {
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	var out []hostapppack.BindingSpec
	for _, binding := range manifest.Bindings {
		if wanted[binding.ID] {
			out = append(out, binding)
			delete(wanted, binding.ID)
		}
	}
	if len(wanted) != 0 {
		return nil, errors.New("host-app enablement references an unknown binding")
	}
	return out, nil
}

func (c Core) planHostAppCommandOwners(profileName string, manifest hostapppack.Manifest, revisionID string, selected []hostapppack.BindingSpec, replacements map[string]string) (cmdproxy.HostAppCommandPlan, error) {
	sources, err := c.hostAppCatalogSources(profileName)
	if err != nil {
		return cmdproxy.HostAppCommandPlan{}, err
	}
	// A package update replaces that package's prior exact revision ownership.
	// Rebuild the catalog without that package so a prior explicit replacement
	// is still checked against the displaced owner, not silently inherited.
	filteredSources := sources[:0]
	for _, source := range sources {
		if source.manifest.ID != manifest.ID {
			filteredSources = append(filteredSources, source)
		}
	}
	current, err := planHostAppCatalogCommandOwners(filteredSources)
	if err != nil {
		return cmdproxy.HostAppCommandPlan{}, err
	}
	requested := hostAppCommandOwners(manifest.ID, revisionID, selected)
	requestedByCommand := make(map[string]string, len(requested))
	for _, owner := range requested {
		requestedByCommand[owner.Command] = owner.Owner
	}
	structured := make([]cmdproxy.HostAppOwnerReplacement, 0, len(replacements))
	for command, fromOwner := range replacements {
		toOwner, ok := requestedByCommand[command]
		if !ok {
			return cmdproxy.HostAppCommandPlan{}, fmt.Errorf("host-app command %q replacement does not name a requested binding", command)
		}
		structured = append(structured, cmdproxy.HostAppOwnerReplacement{Command: command, FromOwner: fromOwner, ToOwner: toOwner})
	}
	sort.Slice(structured, func(i, j int) bool { return structured[i].Command < structured[j].Command })
	commandPlan, err := cmdproxy.PlanHostAppCommandOwners(current, requested, structured)
	if err != nil {
		return cmdproxy.HostAppCommandPlan{}, hostAppLifecycleError(recovery.CodeHostAppCommandConflict, err)
	}
	return commandPlan, nil
}

func (c Core) currentHostAppCommandOwners(profileName string) ([]cmdproxy.HostAppCommandOwner, error) {
	sources, err := c.hostAppCatalogSources(profileName)
	if err != nil {
		return nil, err
	}
	return planHostAppCatalogCommandOwners(sources)
}

func planHostAppCatalogCommandOwners(sources []hostAppCatalogSource) ([]cmdproxy.HostAppCommandOwner, error) {
	var owners []cmdproxy.HostAppCommandOwner
	for _, source := range sources {
		selected, err := selectHostAppBindings(source.manifest, source.enablement.BindingIDs)
		if err != nil {
			return nil, err
		}
		requested := hostAppCommandOwners(source.manifest.ID, source.revision.RevisionID, selected)
		requestedByCommand := make(map[string]string, len(requested))
		for _, owner := range requested {
			requestedByCommand[owner.Command] = owner.Owner
		}
		var replacements []cmdproxy.HostAppOwnerReplacement
		for command, fromOwner := range source.enablement.ConflictReplacements {
			toOwner, ok := requestedByCommand[command]
			if !ok {
				return nil, fmt.Errorf("host-app pack %q replacement for %q does not name an enabled binding", source.manifest.ID, command)
			}
			replacements = append(replacements, cmdproxy.HostAppOwnerReplacement{Command: command, FromOwner: fromOwner, ToOwner: toOwner})
		}
		ownerPlan, err := cmdproxy.PlanHostAppCommandOwners(owners, requested, replacements)
		if err != nil {
			return nil, err
		}
		owners = ownerPlan.Owners
	}
	return owners, nil
}

func hostAppCommandOwners(packID, revisionID string, bindings []hostapppack.BindingSpec) []cmdproxy.HostAppCommandOwner {
	var owners []cmdproxy.HostAppCommandOwner
	for _, binding := range bindings {
		owner := hostAppBindingOwner(packID, revisionID, binding.ID)
		for _, command := range binding.Commands {
			owners = append(owners, cmdproxy.HostAppCommandOwner{Command: command, Owner: owner})
		}
	}
	sort.Slice(owners, func(i, j int) bool {
		if owners[i].Command != owners[j].Command {
			return owners[i].Command < owners[j].Command
		}
		return owners[i].Owner < owners[j].Owner
	})
	return owners
}

func hostAppBindingOwner(packID, revisionID, bindingID string) string {
	return packID + "/" + revisionID + "/" + bindingID
}

func (c Core) resolveManifestIdentities(manifest hostapppack.Manifest, revisionID string, bindings []hostapppack.BindingSpec, access string, forbidden []string) (map[string]hostcap.ObservedApplicationIdentity, string, string, error) {
	return c.resolveManifestIdentitiesContext(context.Background(), manifest, revisionID, bindings, access, forbidden)
}

func (c Core) resolveManifestIdentitiesContext(ctx context.Context, manifest hostapppack.Manifest, revisionID string, bindings []hostapppack.BindingSpec, access string, forbidden []string) (map[string]hostcap.ObservedApplicationIdentity, string, string, error) {
	apps := map[string]hostapppack.AppSpec{}
	for _, app := range manifest.Apps {
		apps[app.ID] = app
	}
	identities := map[string]hostcap.ObservedApplicationIdentity{}
	safetyID, safetyVersion := "", ""
	for _, binding := range bindings {
		app := apps[binding.AppID]
		if _, ok := identities[app.ID]; !ok {
			expectation := applicationExpectation(manifest.ID, revisionID, app)
			identity, err := c.resolveHostAppIdentityContext(ctx, expectation, forbidden)
			if err != nil {
				return nil, "", "", err
			}
			identities[app.ID] = identity
		}
		if identities[app.ID].Verification == hostcap.AppVerificationUnverified && access != hostapppack.AccessAskEachRun {
			return nil, "", "", errors.New("an unverified host application requires ask-each-run access")
		}
		if access == hostapppack.AccessSafe {
			if app.RequestedSafetyProfile == "" {
				return nil, "", "", errors.New("safe host-app binding has no requested Core safety profile")
			}
			identity := identities[app.ID]
			profile, err := hostcap.SelectCoreSafetyProfile(app.RequestedSafetyProfile, identity.SafetyIdentity(c.hostAppPlatform()))
			if err != nil {
				return nil, "", "", hostAppLifecycleError(recovery.CodeHostAppSafetyUnavailable, err)
			}
			if safetyID != "" && (safetyID != profile.ID || safetyVersion != profile.Version) {
				return nil, "", "", errors.New("selected bindings require different Core safety profiles")
			}
			safetyID, safetyVersion = profile.ID, profile.Version
		}
	}
	return identities, safetyID, safetyVersion, nil
}

func unverifiedAppTrustReview(identities map[string]hostcap.ObservedApplicationIdentity) []HostAppUnverifiedTrustReview {
	keys := make([]string, 0, len(identities))
	for key := range identities {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]HostAppUnverifiedTrustReview, 0, len(keys))
	for _, key := range keys {
		identity := identities[key]
		if identity.Verification != hostcap.AppVerificationUnverified {
			continue
		}
		out = append(out, HostAppUnverifiedTrustReview{
			QualifiedAppRef: identity.QualifiedAppRef, RootClass: string(identity.RootClass),
			CanonicalPathDigest: identity.CanonicalPathDigest, ContentDigest: identity.ContentDigest,
			IdentityDigest: identity.IdentityDigest(),
		})
	}
	return out
}

func validateUnverifiedAppTrust(enablement hostapppack.Enablement, identities map[string]hostcap.ObservedApplicationIdentity) error {
	required := unverifiedAppTrustReview(identities)
	if len(required) != len(enablement.UnverifiedAppTrust) {
		return &hostcap.Error{Code: hostcap.CodeAppIdentityDrift, Reason: "unverified application trust is missing or stale"}
	}
	byRef := make(map[string]hostapppack.UnverifiedAppTrust, len(enablement.UnverifiedAppTrust))
	for _, record := range enablement.UnverifiedAppTrust {
		if err := hostapppack.ValidateUnverifiedAppTrust(record); err != nil {
			return &hostcap.Error{Code: hostcap.CodeAppIdentityDrift, Reason: "unverified application trust record is invalid"}
		}
		byRef[record.QualifiedAppRef] = record
	}
	for _, want := range required {
		record, ok := byRef[want.QualifiedAppRef]
		if !ok || !hostapppack.MatchesUnverifiedAppTrust(record, want.QualifiedAppRef, want.RootClass, want.CanonicalPathDigest, want.ContentDigest, want.IdentityDigest) {
			return &hostcap.Error{Code: hostcap.CodeAppIdentityDrift, Reason: "unverified application content changed after explicit trust"}
		}
	}
	return nil
}

func classifyHostAppIdentityError(err error) error {
	if err == nil || HostAppRecoveryCode(err) != "" {
		return err
	}
	switch hostcap.CodeOf(err) {
	case hostcap.CodeAppAbsent:
		return hostAppLifecycleError(recovery.CodeHostAppAbsent, err)
	case hostcap.CodeAppIdentityDrift:
		return hostAppLifecycleError(recovery.CodeHostAppIdentityInvalid, err)
	default:
		return hostAppLifecycleError(recovery.CodeHostAppIdentityInvalid, err)
	}
}

func (c Core) resolveHostAppIdentity(expectation hostcap.ApplicationExpectation, forbidden []string) (hostcap.ObservedApplicationIdentity, error) {
	return c.resolveHostAppIdentityContext(context.Background(), expectation, forbidden)
}

func (c Core) resolveHostAppIdentityContext(ctx context.Context, expectation hostcap.ApplicationExpectation, forbidden []string) (hostcap.ObservedApplicationIdentity, error) {
	if c.HostAppIdentityResolver != nil {
		return c.HostAppIdentityResolver(expectation, append([]string(nil), forbidden...))
	}
	if c.hostAppPlatform() != hostcap.PlatformDarwin {
		return hostcap.ObservedApplicationIdentity{}, &hostcap.Error{Code: hostcap.CodeAppAbsent, Reason: "host-app projection is available on macOS hosts in v1"}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return hostcap.ObservedApplicationIdentity{}, err
	}
	return hostcap.ResolveApplicationIdentityContext(ctx, expectation, hostcap.ApplicationIdentityOptions{
		Roots: hostcap.CoreApplicationRoots(home), ForbiddenRoots: forbidden, OperatorUID: uint32(os.Getuid()),
		ObserveSigningContext: hostcap.ObserveDarwinSigningIdentityContext,
	})
}

func (c Core) hostAppPlatform() hostcap.Platform {
	if c.HostAppPlatform != "" {
		return c.HostAppPlatform
	}
	return hostcap.CurrentPlatform()
}

func (c Core) hostAppRevalidator(forbidden []string) hostcap.IdentityRevalidator {
	if c.HostAppIdentityRevalidator != nil {
		return c.HostAppIdentityRevalidator
	}
	return func(expectation hostcap.ApplicationExpectation, previous hostcap.ObservedApplicationIdentity) (hostcap.ObservedApplicationIdentity, error) {
		current, err := c.resolveHostAppIdentity(expectation, forbidden)
		if err != nil {
			return hostcap.ObservedApplicationIdentity{}, err
		}
		if current.IdentityDigest() != previous.IdentityDigest() || current.QualifiedAppRef != previous.QualifiedAppRef {
			return hostcap.ObservedApplicationIdentity{}, &hostcap.Error{Code: hostcap.CodeAppIdentityDrift, Reason: "host application identity changed before launch"}
		}
		return current, nil
	}
}

func (c Core) hostAppRevalidatorContext(forbidden []string) hostcap.ContextIdentityRevalidator {
	if c.HostAppIdentityRevalidator != nil {
		return func(_ context.Context, expectation hostcap.ApplicationExpectation, previous hostcap.ObservedApplicationIdentity) (hostcap.ObservedApplicationIdentity, error) {
			return c.HostAppIdentityRevalidator(expectation, previous)
		}
	}
	return func(ctx context.Context, expectation hostcap.ApplicationExpectation, previous hostcap.ObservedApplicationIdentity) (hostcap.ObservedApplicationIdentity, error) {
		current, err := c.resolveHostAppIdentityContext(ctx, expectation, forbidden)
		if err != nil {
			return hostcap.ObservedApplicationIdentity{}, err
		}
		if current.IdentityDigest() != previous.IdentityDigest() || current.QualifiedAppRef != previous.QualifiedAppRef {
			return hostcap.ObservedApplicationIdentity{}, &hostcap.Error{Code: hostcap.CodeAppIdentityDrift, Reason: "host application identity changed before launch"}
		}
		return current, nil
	}
}

func applicationExpectation(packID, revisionID string, app hostapppack.AppSpec) hostcap.ApplicationExpectation {
	qualified := packID + "/" + revisionID + "/" + app.ID
	return hostcap.ApplicationExpectation{QualifiedAppRef: qualified, BundleNames: append([]string(nil), app.BundleNames...), ExecutableRelativePath: app.ExecutableRelativePath, ExpectedBundleID: app.ExpectedBundleID, ExpectedTeamID: app.ExpectedTeamID}
}

func combinedIdentityDigest(identities map[string]hostcap.ObservedApplicationIdentity) string {
	keys := make([]string, 0, len(identities))
	for key := range identities {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		_, _ = hash.Write([]byte(key + "\x00" + identities[key].IdentityDigest() + "\x00"))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func hostAppReview(manifest hostapppack.Manifest, sourceDigest, fingerprint string) HostAppPackReview {
	review := HostAppPackReview{
		PackID: manifest.ID, Version: manifest.Version,
		Description:  SanitizeHostAppDisplayText(manifest.Description, hostapppack.MaxDescriptionBytes),
		SourceDigest: sourceDigest, PermissionFingerprint: fingerprint, UntrustedPackageFields: true,
	}
	if manifest.InstallHint != nil {
		review.InstallHint = SanitizeHostAppDisplayText(manifest.InstallHint.Text, hostapppack.MaxHintBytes)
		review.InstallHintURL = SanitizeHostAppDisplayText(manifest.InstallHint.URL, hostapppack.MaxURLBytes)
	}
	for _, app := range manifest.Apps {
		review.Applications = append(review.Applications, app.ID)
		review.ApplicationsDeclared = append(review.ApplicationsDeclared, HostAppApplicationReview{
			AppID: app.ID, BundleNames: append([]string(nil), app.BundleNames...),
			ExecutableRelativePath: app.ExecutableRelativePath,
			ExpectedBundleID:       app.ExpectedBundleID, ExpectedTeamID: app.ExpectedTeamID,
			RequestedSafetyProfile: app.RequestedSafetyProfile,
		})
	}
	for _, binding := range manifest.Bindings {
		review.Commands = append(review.Commands, binding.Commands...)
		review.Capabilities = append(review.Capabilities, binding.CapabilityID)
		review.ResultPolicies = append(review.ResultPolicies, binding.ResultPolicy)
		review.ResourceKinds = append(review.ResourceKinds, binding.ResourceKinds...)
		review.RequestedAccess = append(review.RequestedAccess, binding.RequestedAccess)
	}
	review.Commands, review.Applications = sortedUnique(review.Commands), sortedUnique(review.Applications)
	review.Capabilities, review.ResourceKinds = sortedUnique(review.Capabilities), sortedUnique(review.ResourceKinds)
	review.ResultPolicies = sortedUnique(review.ResultPolicies)
	review.RequestedAccess = sortedUnique(review.RequestedAccess)
	return review
}

func hostAppSourceSpecFromRevision(revision hostapppack.Revision) hostapppack.SourceSpec {
	return hostapppack.SourceSpec{
		Kind: revision.Source.Kind, Path: revision.Source.LocalPath,
		URL: revision.Source.URL, Commit: revision.Source.Commit,
	}
}

// populateBestEffortHostAppStateReview must never prevent authority removal.
// Revocation and removal remain available even when package bytes or app
// observation are unavailable; a successful read only enriches the review.
func (c Core) populateBestEffortHostAppStateReview(plan *HostAppPackPlan, revision hostapppack.Revision, profileName string) {
	if plan == nil {
		return
	}
	source := hostAppSourceSpecFromRevision(revision)
	plan.SourceReview = hostAppSourceReview(source)
	manifest, _, err := hostapppack.LoadManifest(filepath.Join(c.hostAppPackStore().SourceDir(revision.PackID, revision.RevisionID), hostapppack.ManifestFileName))
	if err != nil {
		return
	}
	plan.Review = hostAppReview(manifest, revision.SourceDigest, revision.BasePermissionFingerprint)
	plan.Review.ApplicationsObserved = c.observeHostAppReviewIdentities(manifest, revision.RevisionID, profileName, source)
}

func hostAppIdentityReviews(manifest hostapppack.Manifest, revisionID string, identities map[string]hostcap.ObservedApplicationIdentity) []HostAppIdentityReview {
	out := make([]HostAppIdentityReview, 0, len(identities))
	for _, app := range manifest.Apps {
		identity, ok := identities[app.ID]
		if !ok {
			continue
		}
		out = append(out, HostAppIdentityReview{
			AppID: app.ID, QualifiedAppRef: manifest.ID + "/" + revisionID + "/" + app.ID,
			Verification: string(identity.Verification), RootClass: string(identity.RootClass), OwnerClass: string(identity.OwnerClass),
			BundleID:      SanitizeHostAppDisplayText(identity.BundleID, hostapppack.MaxDescriptionBytes),
			TeamID:        SanitizeHostAppDisplayText(identity.TeamID, hostapppack.MaxDescriptionBytes),
			CodeIdentity:  SanitizeHostAppDisplayText(identity.CodeIdentity, hostapppack.MaxDescriptionBytes),
			ContentDigest: identity.ContentDigest, IdentityDigest: identity.IdentityDigest(),
		})
	}
	return out
}

// observeHostAppReviewIdentities adds Core-observed trust facts to read-only
// reviews. Observation failures remain visible classifications here; authority
// checks are performed separately when an enablement is planned.
func (c Core) observeHostAppReviewIdentities(manifest hostapppack.Manifest, revisionID, profileName string, source hostapppack.SourceSpec) []HostAppIdentityReview {
	forbidden, forbiddenErr := c.hostAppForbiddenOverlapRoots(profileName, source)
	out := make([]HostAppIdentityReview, 0, len(manifest.Apps))
	for _, app := range manifest.Apps {
		review := HostAppIdentityReview{
			AppID: app.ID, QualifiedAppRef: manifest.ID + "/" + revisionID + "/" + app.ID,
			Verification: "unsupported", RootClass: "none", OwnerClass: "unknown",
		}
		identityErr := forbiddenErr
		var identity hostcap.ObservedApplicationIdentity
		if identityErr == nil {
			identity, identityErr = c.resolveHostAppIdentity(applicationExpectation(manifest.ID, revisionID, app), forbidden)
		}
		if identityErr != nil {
			switch hostcap.CodeOf(identityErr) {
			case hostcap.CodeAppAbsent:
				review.Verification = "absent"
			case hostcap.CodeAppIdentityDrift:
				review.Verification = "drifted"
			}
			out = append(out, review)
			continue
		}
		review.Verification = string(identity.Verification)
		review.RootClass = string(identity.RootClass)
		review.OwnerClass = string(identity.OwnerClass)
		review.BundleID = SanitizeHostAppDisplayText(identity.BundleID, hostapppack.MaxDescriptionBytes)
		review.TeamID = SanitizeHostAppDisplayText(identity.TeamID, hostapppack.MaxDescriptionBytes)
		review.CodeIdentity = SanitizeHostAppDisplayText(identity.CodeIdentity, hostapppack.MaxDescriptionBytes)
		review.ContentDigest = identity.ContentDigest
		review.IdentityDigest = identity.IdentityDigest()
		out = append(out, review)
	}
	return out
}

func (c Core) hostAppForbiddenOverlapRoots(profileName string, source hostapppack.SourceSpec) ([]string, error) {
	roots := []string{c.Store.Root, os.TempDir()}
	if source.Kind == hostapppack.SourceLocal {
		roots = append(roots, source.Path)
	}
	if source.Kind == hostapppack.SourceGit && filepath.IsAbs(source.URL) {
		roots = append(roots, source.URL)
	}
	if profileRecord, err := c.Store.Load(profileName); err == nil {
		rules := append([]hostfs.Rule(nil), profileRecord.HostFS.Grants...)
		rules = append(rules, profileRecord.HostFS.Deny...)
		for _, rule := range rules {
			roots = append(roots, rule.HostPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if records, err := (environment.Store{Root: c.Store.Root}).List(); err == nil {
		for _, record := range records {
			if record.Profile == profileName {
				if binding, ok := pinnedEnvironmentWorkspace(record); ok {
					roots = append(roots, binding.HostRoot)
				}
			}
		}
	} else {
		return nil, err
	}
	if c.HostAppForbiddenRoots != nil {
		active, err := c.HostAppForbiddenRoots(profileName)
		if err != nil {
			return nil, err
		}
		roots = append(roots, active...)
	}
	cleaned := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		cleaned = append(cleaned, filepath.Clean(root))
	}
	return sortedUnique(cleaned), nil
}

// SanitizeHostAppDisplayText makes untrusted package/source prose safe for a
// terminal review. It strips ANSI CSI, OSC, C0/C1 controls, collapses layout
// whitespace, and bounds the result. It does not make the text authoritative;
// callers must still label package-provided prose as untrusted.
func SanitizeHostAppDisplayText(value string, maxBytes int) string {
	var out strings.Builder
	for i := 0; i < len(value); {
		if value[i] == 0x1b {
			i++
			if i >= len(value) {
				break
			}
			switch value[i] {
			case '[':
				i++
				for i < len(value) {
					b := value[i]
					i++
					if b >= 0x40 && b <= 0x7e {
						break
					}
				}
			case ']':
				i++
				for i < len(value) {
					if value[i] == 0x07 {
						i++
						break
					}
					if value[i] == 0x1b && i+1 < len(value) && value[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
			default:
				i++
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		if size == 0 {
			break
		}
		i += size
		if unicode.IsControl(r) || (r >= 0x80 && r <= 0x9f) {
			continue
		}
		if unicode.IsSpace(r) {
			r = ' '
		}
		out.WriteRune(r)
	}
	clean := strings.Join(strings.Fields(out.String()), " ")
	if maxBytes > 0 && len(clean) > maxBytes {
		clean = clean[:maxBytes]
		for !utf8.ValidString(clean) {
			clean = clean[:len(clean)-1]
		}
	}
	return clean
}

func hostAppSourceReview(source hostapppack.SourceSpec) HostAppSourceReview {
	review := HostAppSourceReview{Kind: source.Kind, Commit: source.Commit}
	switch source.Kind {
	case hostapppack.SourceLocal:
		base := filepath.Base(filepath.Clean(source.Path))
		if base == "." || base == string(filepath.Separator) || base == "" {
			review.Location = "<local-directory>"
		} else {
			review.Location = "<local-directory>/" + SanitizeHostAppDisplayText(base, hostapppack.MaxStorageIDBytes)
		}
	case hostapppack.SourceGit:
		review.Location = sanitizeHostAppSourceURL(source.URL)
	}
	return review
}

func sanitizeHostAppSourceURL(raw string) string {
	if filepath.IsAbs(raw) {
		base := filepath.Base(filepath.Clean(raw))
		if base == "." || base == string(filepath.Separator) || base == "" {
			return "<local-git>"
		}
		return "<local-git>/" + SanitizeHostAppDisplayText(base, hostapppack.MaxStorageIDBytes)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "file" {
		return "<git-source>"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	clean := SanitizeHostAppDisplayText(parsed.String(), hostapppack.MaxURLBytes)
	if clean == "" {
		return "<git-source>"
	}
	return clean
}

func sanitizeHostAppSourceError(err error, source hostapppack.SourceSpec, storeRoot string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if source.Path != "" {
		message = strings.ReplaceAll(message, source.Path, "<local-source>")
	}
	if source.URL != "" {
		message = strings.ReplaceAll(message, source.URL, sanitizeHostAppSourceURL(source.URL))
	}
	if storeRoot != "" {
		message = strings.ReplaceAll(message, storeRoot, "<hideout-store>")
	}
	if tempRoot := os.TempDir(); tempRoot != "" {
		message = strings.ReplaceAll(message, tempRoot, "<temporary-directory>")
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil && home != "" {
		message = strings.ReplaceAll(message, home, "$HOME")
	}
	message = SanitizeHostAppDisplayText(message, 4096)
	if message == "" {
		message = "host-app source operation failed"
	}
	return errors.New(message)
}

func classifyHostAppSourceError(err error, source hostapppack.SourceSpec, storeRoot string) error {
	if err == nil {
		return nil
	}
	if HostAppRecoveryCode(err) != "" {
		return err
	}
	code := recovery.CodeHostAppSourceInvalid
	var execErr *exec.Error
	if errors.As(err, &execErr) && execErr.Name == "git" {
		code = recovery.CodeHostAppGitUnavailable
	}
	return hostAppLifecycleError(code, sanitizeHostAppSourceError(err, source, storeRoot))
}

func publicHostAppRevision(revision hostapppack.Revision) hostapppack.Revision {
	review := hostAppSourceReview(hostapppack.SourceSpec{
		Kind: revision.Source.Kind, Path: revision.Source.LocalPath,
		URL: revision.Source.URL, Commit: revision.Source.Commit,
	})
	revision.Source.LocalPath = ""
	revision.Source.URL = ""
	switch revision.Source.Kind {
	case hostapppack.SourceLocal:
		revision.Source.LocalPath = review.Location
	case hostapppack.SourceGit:
		revision.Source.URL = review.Location
	}
	return revision
}

func publicHostAppRegistryEntry(entry hostapppack.RegistryEntry) hostapppack.RegistryEntry {
	entry.Revisions = append([]hostapppack.Revision(nil), entry.Revisions...)
	for i := range entry.Revisions {
		entry.Revisions[i] = publicHostAppRevision(entry.Revisions[i])
	}
	return entry
}

func sortedUnique(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			set[value] = true
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func hostAppEffectivePermissionContext(access, safetyID, safetyVersion string, bindingIDs []string, replacements map[string]string) hostapppack.EffectivePermissionContext {
	return hostapppack.EffectivePermissionContext{
		Access: access, SafetyProfileID: safetyID, SafetyProfileVersion: safetyVersion,
		BindingIDs: append([]string(nil), bindingIDs...), ConflictReplacements: cloneStringMap(replacements),
	}
}

func boundedHostAppReason(reason, fallback string) string {
	reason = SanitizeHostAppDisplayText(reason, hostapppack.MaxDescriptionBytes)
	if reason == "" {
		reason = fallback
	}
	return reason
}

func effectivePermissionContextForEnablement(manifest hostapppack.Manifest, enablement hostapppack.Enablement) (hostapppack.EffectivePermissionContext, error) {
	context := hostAppEffectivePermissionContext(
		enablement.Access, "", "", enablement.BindingIDs, enablement.ConflictReplacements,
	)
	if enablement.Access == hostapppack.AccessAskEachRun {
		return context, nil
	}
	if enablement.Access != hostapppack.AccessSafe {
		return hostapppack.EffectivePermissionContext{}, fmt.Errorf("unsupported host-app access %q", enablement.Access)
	}
	selected, err := selectHostAppBindings(manifest, enablement.BindingIDs)
	if err != nil {
		return hostapppack.EffectivePermissionContext{}, err
	}
	apps := make(map[string]hostapppack.AppSpec, len(manifest.Apps))
	for _, app := range manifest.Apps {
		apps[app.ID] = app
	}
	requested := ""
	for _, binding := range selected {
		profileID := apps[binding.AppID].RequestedSafetyProfile
		if profileID == "" {
			return hostapppack.EffectivePermissionContext{}, errors.New("safe enablement has no requested Core safety profile")
		}
		if requested != "" && requested != profileID {
			return hostapppack.EffectivePermissionContext{}, errors.New("safe enablement spans different Core safety profiles")
		}
		requested = profileID
	}
	for _, safety := range hostcap.CoreSafetyProfiles() {
		if safety.ID != requested {
			continue
		}
		candidate := hostAppEffectivePermissionContext(
			enablement.Access, safety.ID, safety.Version, enablement.BindingIDs, enablement.ConflictReplacements,
		)
		fingerprint, fingerprintErr := hostapppack.EffectivePermissionFingerprint(manifest, candidate)
		if fingerprintErr == nil && fingerprint == enablement.PermissionFingerprint {
			return candidate, nil
		}
	}
	return hostapppack.EffectivePermissionContext{}, errors.New("accepted Core safety profile version is no longer available")
}

func findHostAppRegistryEntry(registry hostapppack.Registry, packID string) (hostapppack.RegistryEntry, bool) {
	for _, entry := range registry.Packs {
		if entry.ID == packID {
			return entry, true
		}
	}
	return hostapppack.RegistryEntry{}, false
}

func (c Core) requireNewHostAppPack(packID string) error {
	registry, err := c.hostAppPackStore().LoadRegistry()
	if err != nil {
		return err
	}
	if _, exists := findHostAppRegistryEntry(registry, packID); exists {
		return hostAppLifecycleError(recovery.CodeHostAppPermissionReviewRequired,
			fmt.Errorf("host-app pack %q is already installed; use app update to review a new revision", packID))
	}
	return nil
}

func findHostAppRevision(entry hostapppack.RegistryEntry, revisionID string) (hostapppack.Revision, bool) {
	for _, revision := range entry.Revisions {
		if revision.RevisionID == revisionID {
			return revision, true
		}
	}
	return hostapppack.Revision{}, false
}

func defaultHostAppInspectionProfile(profileName string) string {
	if profileName = strings.TrimSpace(profileName); profileName != "" {
		return profileName
	}
	return "default"
}

func (c Core) recordHostAppPackAudit(operation string, plan HostAppPackPlan, opErr error) error {
	decisionValue, reason, phase := "allow", "", "complete"
	if opErr != nil {
		decisionValue, phase = "deny", "failed"
		reason = sanitizeHostAppSourceError(opErr, plan.Source, c.Store.Root).Error()
	}
	event := hostapppack.Evidence(operation, decisionValue, plan.Profile, plan.PackID, plan.RevisionID, plan.ExpectedSourceDigest, plan.ExpectedPermissionFingerprint, plan.ExpectedIdentityDigest, reason)
	details := cloneAnyMap(event.Details)
	details["action"] = event.Action
	details["decision"] = event.Decision
	details["operation"] = plan.Operation
	details["profile"] = plan.Profile
	details["status"] = phase
	if code := HostAppRecoveryCode(opErr); code != "" {
		details["recoveryCode"] = code
		event.Details["recoveryCode"] = code
	}
	c.emitOperation("host-app", phase, details)
	if c.Store.Root == "" {
		return nil
	}
	layout, err := session.New(c.Store.Root)
	if err != nil {
		return err
	}
	writer, err := audit.NewFile(layout.AuditPath)
	if err != nil {
		return err
	}
	event.Session = layout.ID
	emitErr := writer.Emit(event)
	closeErr := writer.Close()
	_, _ = session.CleanupEphemeral(c.Store.Root, layout.ID, false)
	if emitErr != nil {
		return emitErr
	}
	return closeErr
}

func cloneAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+4)
	for key, value := range in {
		out[key] = value
	}
	return out
}
