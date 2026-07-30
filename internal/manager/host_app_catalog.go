package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/cmdgrammar"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/hostcap/appopen"
	"github.com/vibe-agi/hideout/internal/packsnapshot"
)

type hostAppCatalogSource struct {
	manifest   hostapppack.Manifest
	revision   hostapppack.Revision
	enablement hostapppack.Enablement
	builtIn    bool
}

// CompileHostAppCatalog emits immutable, path-free command bindings. Safe
// bindings defer host application observation until their command is actually
// invoked; ask-each-run bindings retain eager observation because the operator
// decision must be bound to the exact observed identity.
func (c Core) CompileHostAppCatalog(profileName, runID string, forbiddenRoots []string) (hostcap.BindingCatalog, []cmdproxy.Registration, error) {
	profileName, err := normalizeManagerProfileName(profileName)
	if err != nil {
		return hostcap.BindingCatalog{}, nil, err
	}
	sources, err := c.hostAppCatalogSources(profileName)
	if err != nil {
		return hostcap.BindingCatalog{}, nil, err
	}
	forbiddenRoots = append(append([]string(nil), forbiddenRoots...), c.Store.Root)
	var bindings []hostcap.OpenResourceBinding
	var registrations []cmdproxy.Registration
	effectiveOwners, err := planHostAppCatalogCommandOwners(sources)
	if err != nil {
		return hostcap.BindingCatalog{}, nil, err
	}
	effectiveOwnerByCommand := make(map[string]string, len(effectiveOwners))
	for _, owner := range effectiveOwners {
		effectiveOwnerByCommand[owner.Command] = owner.Owner
	}
	for _, source := range sources {
		selected, err := selectHostAppBindings(source.manifest, source.enablement.BindingIDs)
		if err != nil {
			return hostcap.BindingCatalog{}, nil, err
		}
		identityDeferred := source.enablement.Access == hostapppack.AccessSafe
		identities := map[string]hostcap.ObservedApplicationIdentity{}
		safetyID, safetyVersion := "", ""
		if identityDeferred {
			safetyID, safetyVersion, err = manifestSafetyProfile(source.manifest, selected)
		} else {
			identities, safetyID, safetyVersion, err = c.resolveManifestIdentities(source.manifest, source.revision.RevisionID, selected, source.enablement.Access, forbiddenRoots)
		}
		if err != nil {
			// An eager built-in ask-each-run recipe remains optional. A missing or
			// unclassifiable local app must not break unrelated guest commands.
			if source.builtIn && !identityDeferred {
				continue
			}
			return hostcap.BindingCatalog{}, nil, err
		}
		if !source.builtIn {
			permissionContext := hostAppEffectivePermissionContext(
				source.enablement.Access, safetyID, safetyVersion,
				source.enablement.BindingIDs, source.enablement.ConflictReplacements,
			)
			if err := hostapppack.ValidateEnablement(source.enablement, source.revision, source.manifest, permissionContext); err != nil {
				return hostcap.BindingCatalog{}, nil, fmt.Errorf("host-app pack %q enablement drifted: %w", source.manifest.ID, err)
			}
			if !identityDeferred {
				if err := validateUnverifiedAppTrust(source.enablement, identities); err != nil {
					return hostcap.BindingCatalog{}, nil, err
				}
				if observed := combinedIdentityDigest(identities); observed != source.enablement.ObservedIdentityDigest {
					return hostcap.BindingCatalog{}, nil, &hostcap.Error{Code: hostcap.CodeAppIdentityDrift, Reason: fmt.Sprintf("host application identity changed for pack %q", source.manifest.ID)}
				}
			}
		}
		apps := map[string]hostapppack.AppSpec{}
		for _, app := range source.manifest.Apps {
			apps[app.ID] = app
		}
		for _, spec := range selected {
			owner := hostAppBindingOwner(source.manifest.ID, source.revision.RevisionID, spec.ID)
			commands := make([]string, 0, len(spec.Commands))
			for _, command := range spec.Commands {
				if effectiveOwnerByCommand[command] == owner {
					commands = append(commands, command)
				}
			}
			if len(commands) == 0 {
				continue
			}
			app := apps[spec.AppID]
			identity := identities[spec.AppID]
			qualified := source.manifest.ID + "/" + source.revision.RevisionID + "/" + app.ID
			expectation := applicationExpectation(source.manifest.ID, source.revision.RevisionID, app)
			identity.QualifiedAppRef = qualified
			binding := hostcap.OpenResourceBinding{
				PackID: source.manifest.ID, RevisionID: source.revision.RevisionID, BindingID: spec.ID,
				QualifiedAppRef: qualified, Commands: commands,
				CapabilityID: spec.CapabilityID, ResourceKinds: hostAppResourceKinds(spec.ResourceKinds),
				ResultPolicy: hostcap.ResultPolicy(spec.ResultPolicy), Access: source.enablement.Access,
				Grammar:     hostcap.BindingGrammar{Kind: spec.Grammar.Kind, ResourceCount: spec.Grammar.ResourceCount, GotoFlags: append([]string(nil), spec.Grammar.GotoFlags...), NewWindowFlags: append([]string(nil), spec.Grammar.NewWindowFlags...), ReuseWindowFlags: append([]string(nil), spec.Grammar.ReuseWindowFlags...), UnknownFlags: spec.Grammar.UnknownFlags},
				Application: expectation, Launch: hostAppLaunch(app.Launch), SafetyProfileID: safetyID, SafetyProfileVersion: safetyVersion,
				SourceDigest: source.revision.SourceDigest, PermissionFingerprint: source.enablement.PermissionFingerprint,
				IdentityDeferred:       identityDeferred,
				ObservedIdentityDigest: identity.IdentityDigest(), ObservedIdentity: identity,
			}
			if identityDeferred && !source.builtIn {
				binding.ExpectedIdentitySetDigest = source.enablement.ObservedIdentityDigest
			}
			binding, err = hostcap.FinalizeBindingDigest(binding)
			if err != nil {
				return hostcap.BindingCatalog{}, nil, err
			}
			for _, command := range commands {
				grammar := cmdgrammar.OpenResourceGrammar{Kind: spec.Grammar.Kind, ResourceCount: spec.Grammar.ResourceCount, GotoFlags: append([]string(nil), spec.Grammar.GotoFlags...), NewWindowFlags: append([]string(nil), spec.Grammar.NewWindowFlags...), ReuseWindowFlags: append([]string(nil), spec.Grammar.ReuseWindowFlags...), UnknownFlags: spec.Grammar.UnknownFlags}
				registrations = append(registrations, cmdproxy.Registration{
					Name: command, Action: cmdproxy.ActionHostAppOpenResource, ArgvSchema: cmdproxy.ArgvSchemaOpenResourceV1,
					StreamPolicy: cmdproxy.StreamMetadataOnly, DefaultMode: cmdproxy.DefaultModeAllow,
					AllowedTargets: []string{"workspace-file", "workspace-dir", "hostfs-portal"}, OwnerType: cmdproxy.OwnerHostAppProjection,
					BindingDigest: binding.BindingDigest, OpenResourceGrammar: &grammar,
				})
			}
			bindings = append(bindings, binding)
		}
	}
	catalog, err := hostcap.NewBindingCatalog(bindings)
	if err != nil {
		return hostcap.BindingCatalog{}, nil, err
	}
	sort.Slice(registrations, func(i, j int) bool { return registrations[i].Name < registrations[j].Name })
	_ = runID // run identity is bound by ProjectionConfig and safe-state paths.
	return catalog, registrations, nil
}

func manifestSafetyProfile(manifest hostapppack.Manifest, bindings []hostapppack.BindingSpec) (string, string, error) {
	apps := make(map[string]hostapppack.AppSpec, len(manifest.Apps))
	for _, app := range manifest.Apps {
		apps[app.ID] = app
	}
	safetyID, safetyVersion := "", ""
	for _, binding := range bindings {
		app, ok := apps[binding.AppID]
		if !ok || app.RequestedSafetyProfile == "" {
			return "", "", errors.New("safe host-app binding has no requested Core safety profile")
		}
		profile, err := hostcap.CoreSafetyProfile(app.RequestedSafetyProfile)
		if err != nil {
			return "", "", err
		}
		if safetyID != "" && (safetyID != profile.ID || safetyVersion != profile.Version) {
			return "", "", errors.New("selected bindings require different Core safety profiles")
		}
		safetyID, safetyVersion = profile.ID, profile.Version
	}
	return safetyID, safetyVersion, nil
}

func (c Core) resolveDeferredHostAppBinding(profileName string, binding hostcap.OpenResourceBinding, forbiddenRoots []string) (hostcap.OpenResourceBinding, error) {
	return c.resolveDeferredHostAppBindingContext(context.Background(), profileName, binding, forbiddenRoots)
}

func (c Core) resolveDeferredHostAppBindingContext(ctx context.Context, profileName string, binding hostcap.OpenResourceBinding, forbiddenRoots []string) (hostcap.OpenResourceBinding, error) {
	if !binding.IdentityDeferred {
		return binding, nil
	}
	sources, err := c.hostAppCatalogSources(profileName)
	if err != nil {
		return binding, err
	}
	var source *hostAppCatalogSource
	for i := range sources {
		if sources[i].manifest.ID == binding.PackID && sources[i].revision.RevisionID == binding.RevisionID {
			source = &sources[i]
			break
		}
	}
	if source == nil {
		return binding, &hostcap.Error{Code: hostcap.CodeCommandUnbound, Reason: "host-app binding source is no longer enabled"}
	}
	selected, err := selectHostAppBindings(source.manifest, source.enablement.BindingIDs)
	if err != nil {
		return binding, err
	}
	identities, safetyID, safetyVersion, err := c.resolveManifestIdentitiesContext(ctx, source.manifest, source.revision.RevisionID, selected, binding.Access, forbiddenRoots)
	if err != nil {
		return binding, err
	}
	if safetyID != binding.SafetyProfileID || safetyVersion != binding.SafetyProfileVersion {
		return binding, &hostcap.Error{Code: hostcap.CodeAppIdentityDrift, Reason: "Core safety profile changed before command execution"}
	}
	if !source.builtIn {
		if err := validateUnverifiedAppTrust(source.enablement, identities); err != nil {
			return binding, err
		}
		if observed := combinedIdentityDigest(identities); observed != binding.ExpectedIdentitySetDigest || observed != source.enablement.ObservedIdentityDigest {
			return binding, &hostcap.Error{Code: hostcap.CodeAppIdentityDrift, Reason: "host application identity changed after enablement"}
		}
	}
	for appID, identity := range identities {
		qualified := source.manifest.ID + "/" + source.revision.RevisionID + "/" + appID
		if qualified != binding.QualifiedAppRef {
			continue
		}
		identity.QualifiedAppRef = qualified
		binding.ObservedIdentity = identity
		binding.ObservedIdentityDigest = identity.IdentityDigest()
		if err := hostcap.ValidateOpenResourceBinding(binding); err != nil {
			return binding, err
		}
		return binding, nil
	}
	return binding, &hostcap.Error{Code: hostcap.CodeAppIdentityDrift, Reason: "host application identity is absent from the enabled binding"}
}

func (c Core) hostAppCatalogSources(profileName string) ([]hostAppCatalogSource, error) {
	builtinManifest, err := builtinHostAppManifest()
	if err != nil {
		return nil, err
	}
	builtinRevision := builtinHostAppRevision()
	access := hostapppack.AccessSafe
	if ReadProjectionHostAppMode(c.Store.Root, profileName) == ProjectionHostAppModeTrusted {
		access = hostapppack.AccessAskEachRun
	}
	builtinEnablement, err := builtinHostAppEnablement(profileName, builtinManifest, builtinRevision, access)
	if err != nil {
		return nil, err
	}
	sources := []hostAppCatalogSource{{manifest: builtinManifest, revision: builtinRevision, enablement: builtinEnablement, builtIn: true}}
	enablements, err := c.hostAppPackStore().ListEnablements(profileName)
	if err != nil {
		return nil, err
	}
	for _, enablement := range enablements {
		if enablement.State != hostapppack.EnablementEnabled {
			continue
		}
		_, revision, manifest, err := c.resolveHostAppRevision(enablement.PackID, enablement.RevisionID)
		if err != nil {
			return nil, err
		}
		sources = append(sources, hostAppCatalogSource{manifest: manifest, revision: revision, enablement: enablement})
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].builtIn != sources[j].builtIn {
			return sources[i].builtIn
		}
		if !sources[i].enablement.EnabledAt.Equal(sources[j].enablement.EnabledAt) {
			return sources[i].enablement.EnabledAt.Before(sources[j].enablement.EnabledAt)
		}
		return sources[i].manifest.ID < sources[j].manifest.ID
	})
	return sources, nil
}

func (c Core) hostAppBindingLifecycleValidator(profileName string) (func(hostcap.OpenResourceBinding) error, error) {
	profileName, err := normalizeManagerProfileName(profileName)
	if err != nil {
		return nil, err
	}
	builtinManifest, err := builtinHostAppManifest()
	if err != nil {
		return nil, err
	}
	builtinRevision := builtinHostAppRevision()
	return func(binding hostcap.OpenResourceBinding) error {
		if binding.PackID == builtinManifest.ID {
			enablement, enableErr := builtinHostAppEnablement(profileName, builtinManifest, builtinRevision, binding.Access)
			if enableErr != nil || binding.RevisionID != builtinRevision.RevisionID ||
				binding.SourceDigest != builtinRevision.SourceDigest ||
				binding.PermissionFingerprint != enablement.PermissionFingerprint ||
				!slices.Contains(enablement.BindingIDs, binding.BindingID) {
				return &hostcap.Error{Code: hostcap.CodeCommandUnbound, Reason: "built-in host-app binding identity is stale"}
			}
			return nil
		}

		store := c.hostAppPackStore()
		enablement, loadErr := store.LoadEnablement(profileName, binding.PackID)
		if loadErr != nil || enablement.State != hostapppack.EnablementEnabled ||
			enablement.RevisionID != binding.RevisionID || !slices.Contains(enablement.BindingIDs, binding.BindingID) ||
			enablement.SourceDigest != binding.SourceDigest ||
			enablement.PermissionFingerprint != binding.PermissionFingerprint {
			return &hostcap.Error{Code: hostcap.CodeCommandUnbound, Reason: "host-app binding was disabled, replaced, or revoked"}
		}
		revision, manifest, resolveErr := store.ResolveRevisionManifest(binding.PackID, binding.RevisionID)
		if resolveErr != nil {
			return &hostcap.Error{Code: hostcap.CodeCommandUnbound, Reason: "host-app binding revision is unavailable"}
		}
		context, contextErr := effectivePermissionContextForEnablement(manifest, enablement)
		if contextErr != nil || hostapppack.ValidateEnablement(enablement, revision, manifest, context) != nil {
			return &hostcap.Error{Code: hostcap.CodeCommandUnbound, Reason: "host-app binding trust no longer validates"}
		}
		return nil
	}, nil
}

func builtinHostAppManifest() (hostapppack.Manifest, error) {
	raw := hostcap.BuiltinHostAppPackJSON()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest hostapppack.Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return hostapppack.Manifest{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return hostapppack.Manifest{}, errors.New("built-in host-app pack contains trailing data")
	}
	trusted := trustedBuiltinFingerprintManifest(manifest)
	if err := hostapppack.ValidateManifest(trusted); err != nil {
		return hostapppack.Manifest{}, fmt.Errorf("invalid built-in host-app pack: %w", err)
	}
	return hostapppack.NormalizeManifest(manifest), nil
}

func trustedBuiltinFingerprintManifest(manifest hostapppack.Manifest) hostapppack.Manifest {
	manifest.ID = "core." + strings.TrimPrefix(manifest.ID, "builtin.")
	return manifest
}

func builtinHostAppRevision() hostapppack.Revision {
	raw := hostcap.BuiltinHostAppPackJSON()
	sum := sha256.Sum256(raw)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	manifest, _ := builtinHostAppManifest()
	fingerprint, _ := hostapppack.BasePermissionFingerprint(trustedBuiltinFingerprintManifest(manifest))
	return hostapppack.Revision{RevisionID: packsnapshot.RevisionID(digest), PackID: manifest.ID, Source: hostapppack.SourceLock{Kind: "builtin", AcquiredAt: time.Unix(0, 0).UTC()}, SourceDigest: digest, ManifestDigest: digest, BasePermissionFingerprint: fingerprint, ValidationStatus: hostapppack.ValidationPassed, TestStatus: hostapppack.TestPassed, InstalledAt: time.Unix(0, 0).UTC(), State: hostapppack.RevisionInstalled}
}

func builtinHostAppEnablement(profileName string, manifest hostapppack.Manifest, revision hostapppack.Revision, access string) (hostapppack.Enablement, error) {
	bindingIDs := make([]string, 0, len(manifest.Bindings))
	appByID := make(map[string]hostapppack.AppSpec, len(manifest.Apps))
	for _, app := range manifest.Apps {
		appByID[app.ID] = app
	}
	context := hostapppack.EffectivePermissionContext{Access: access}
	for _, binding := range manifest.Bindings {
		bindingIDs = append(bindingIDs, binding.ID)
		if access != hostapppack.AccessSafe {
			continue
		}
		profileID := appByID[binding.AppID].RequestedSafetyProfile
		if profileID == "" {
			return hostapppack.Enablement{}, fmt.Errorf("built-in host-app binding %q has no Core safety profile", binding.ID)
		}
		if context.SafetyProfileID != "" && context.SafetyProfileID != profileID {
			return hostapppack.Enablement{}, errors.New("built-in host-app pack requires multiple safety profiles but enablement supports one")
		}
		profile, err := hostcap.CoreSafetyProfile(profileID)
		if err != nil {
			return hostapppack.Enablement{}, err
		}
		context.SafetyProfileID, context.SafetyProfileVersion = profile.ID, profile.Version
	}
	sort.Strings(bindingIDs)
	context.BindingIDs = append([]string(nil), bindingIDs...)
	context.ConflictReplacements = map[string]string{}
	permission, err := hostapppack.EffectivePermissionFingerprint(trustedBuiltinFingerprintManifest(manifest), context)
	if err != nil {
		return hostapppack.Enablement{}, err
	}
	return hostapppack.Enablement{
		Schema: hostapppack.EnablementVersion, Profile: profileName, PackID: manifest.ID, RevisionID: revision.RevisionID,
		BindingIDs: bindingIDs, SourceDigest: revision.SourceDigest, BasePermissionFingerprint: revision.BasePermissionFingerprint,
		PermissionFingerprint: permission, Access: access, ConflictReplacements: map[string]string{}, State: hostapppack.EnablementEnabled,
	}, nil
}

func hostAppResourceKinds(values []string) []hostcap.ResourceKind {
	out := make([]hostcap.ResourceKind, 0, len(values))
	for _, value := range values {
		switch value {
		case hostapppack.ResourceWorkspace:
			out = append(out, hostcap.KindWorkspace)
		case hostapppack.ResourceHostFSPortal:
			out = append(out, hostcap.KindHostFS)
		}
	}
	return out
}

func hostAppLaunch(spec hostapppack.LaunchSpec) appopen.LaunchSpec {
	return appopen.LaunchSpec{GotoFlag: spec.GotoFlag, NewWindowFlag: spec.NewWindowFlag, ReuseWindowFlag: spec.ReuseWindowFlag, GotoSeparator: spec.GotoSeparator}
}
