package hostcap

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/vibe-agi/hideout/internal/hostcap/appopen"
)

// UnboundResource is the full resource shape an untrusted grammar may submit.
// Kind, relative path, application, and host path are deliberately absent.
type UnboundResource struct {
	GuestPath string
}

type BoundOpenRequest struct {
	Resources  []UnboundResource
	Location   *Location
	WindowMode WindowMode
}

func (r BoundOpenRequest) Validate() error {
	if len(r.Resources) != 1 || strings.TrimSpace(r.Resources[0].GuestPath) == "" || !strings.HasPrefix(r.Resources[0].GuestPath, "/") || strings.ContainsRune(r.Resources[0].GuestPath, '\x00') {
		return &Error{Code: CodeIntentInvalid, Reason: "open-resource request requires one absolute guest path"}
	}
	if r.Location != nil && (r.Location.Line < 1 || r.Location.Column < 1) {
		return &Error{Code: CodeIntentInvalid, Reason: "location line and column must be positive"}
	}
	if r.WindowMode != "" && r.WindowMode != WindowReuse && r.WindowMode != WindowNew {
		return &Error{Code: CodeIntentInvalid, Reason: "window mode must be reuse or new"}
	}
	return nil
}

type ResolvedResource struct {
	Ref      ResourceRef
	HostPath string
}

// ResourceResolver derives workspace vs HostFS portal from the live session
// mapping. Guest input cannot declare a kind or a host path.
type ResourceResolver interface {
	ResolveResource(guestPath string) (ResolvedResource, error)
	RevalidateResource(previous ResolvedResource) error
}

type IdentityRevalidator func(ApplicationExpectation, ObservedApplicationIdentity) (ObservedApplicationIdentity, error)

type BoundOpenContext struct {
	SessionID          string
	Profile            string
	RunID              string
	Command            string
	SafeStateBase      string
	Platform           Platform
	Resources          ResourceResolver
	GrantScopeBase     GrantScope
	Grants             GrantChecker
	Launcher           appopen.Launcher
	Deduper            Deduper
	RevalidateIdentity IdentityRevalidator
	ValidateLifecycle  func(OpenResourceBinding) error
}

// OpenBoundResource executes only authority already present in binding. There
// is no generic host-exec fallback and no guest field can select an app,
// capability, result channel, resource kind, or host path.
func OpenBoundResource(ctx context.Context, binding OpenResourceBinding, request BoundOpenRequest, oc BoundOpenContext) (OpenResult, error) {
	if err := ValidateOpenResourceBinding(binding); err != nil {
		return OpenResult{}, &Error{Code: CodeCommandUnbound, Reason: "immutable host-app binding is invalid"}
	}
	if err := request.Validate(); err != nil {
		return OpenResult{}, err
	}
	if oc.Resources == nil || oc.Launcher == nil {
		return OpenResult{}, &Error{Code: CodeProviderUnavailable, Reason: "host-app resource resolver or launcher is unavailable"}
	}
	resource, err := oc.Resources.ResolveResource(request.Resources[0].GuestPath)
	if err != nil {
		return OpenResult{}, preserveCode(err, CodePathNoHostMapping, "resource has no authorized host mapping")
	}
	if !bindingAllowsResource(binding, resource.Ref.Kind) {
		return OpenResult{}, &Error{Code: CodePathNoHostMapping, Reason: "derived resource kind is outside the immutable binding"}
	}
	if strings.TrimSpace(resource.HostPath) == "" {
		return OpenResult{}, &Error{Code: CodePathNoHostMapping, Reason: "resource resolved to an empty host path"}
	}

	mode := appopen.ModeSafe
	if binding.Access == BindingAccessAskEachRun {
		scope := oc.GrantScopeBase
		scope.SessionID, scope.Profile, scope.RunID = oc.SessionID, oc.Profile, oc.RunID
		scope.PackID, scope.RevisionID, scope.BindingID = binding.PackID, binding.RevisionID, binding.BindingID
		scope.QualifiedAppRef, scope.BindingDigest, scope.Command = binding.QualifiedAppRef, binding.BindingDigest, oc.Command
		if oc.Grants == nil || !TrustedGrantActiveForResource(oc.Grants, scope, resource.Ref) {
			return OpenResult{}, &Error{Code: CodeModeTrustedDenied, Reason: "host application access requires an operator grant"}
		}
		mode = appopen.ModeTrusted
	}
	identity := binding.ObservedIdentity
	if identity.QualifiedAppRef != binding.QualifiedAppRef || identity.ExecutablePath == "" {
		return OpenResult{}, &Error{Code: CodeAppIdentityDrift, Reason: "host application identity was not observed for this run"}
	}
	line, column := 0, 0
	if request.Location != nil {
		line, column = request.Location.Line, request.Location.Column
	}
	launchRequest := appopen.OpenRequest{
		BinaryPath: identity.ExecutablePath, Mode: mode, HostTarget: resource.HostPath,
		Line: line, Column: column, NewWindow: request.WindowMode == WindowNew,
		SafeUserDataDir: oc.SafeStateBase, QualifiedAppRef: binding.QualifiedAppRef, RunID: oc.RunID,
	}
	var argv []string
	var safetyProfile appopen.SafetyProfile
	var safetyIdentity appopen.SafetyIdentity
	var safetyEffect appopen.SafeEffect
	if mode == appopen.ModeSafe {
		safetyIdentity = identity.SafetyIdentity(oc.Platform)
		safetyProfile, err = SelectCoreSafetyProfile(binding.SafetyProfileID, safetyIdentity)
		if err != nil {
			return OpenResult{}, &Error{Code: CodeProviderUnavailable, Reason: "Core safety profile is not compatible with the observed app"}
		}
		if binding.SafetyProfileVersion != "" && binding.SafetyProfileVersion != safetyProfile.Version {
			return OpenResult{}, &Error{Code: CodeProviderUnavailable, Reason: "Core safety profile version drifted after enablement"}
		}
		safetyEffect, err = appopen.BuildSafeEffect(binding.Launch, launchRequest, safetyProfile, safetyIdentity)
		if err != nil {
			return OpenResult{}, &Error{Code: CodeProviderUnavailable, Reason: "Core could not build the reviewed safe launch effect: " + err.Error()}
		}
		argv = safetyEffect.Argv
	} else {
		argv, err = appopen.RenderArgv(binding.Launch, launchRequest)
		if err != nil {
			return OpenResult{}, &Error{Code: CodeProviderUnavailable, Reason: "host launch could not be built"}
		}
	}
	result := OpenResult{Outcome: outcomeLaunched, Mode: mode, Argv: append([]string(nil), argv...), HostTarget: resource.HostPath}

	dedupKey := strings.Join([]string{binding.QualifiedAppRef, string(mode), resource.HostPath, string(request.WindowMode), strconv.Itoa(line), strconv.Itoa(column)}, "\x00")
	if oc.Deduper != nil && !oc.Deduper.Reserve(dedupKey) {
		result.Suppressed = true
		return result, nil
	}
	release := func() {
		if oc.Deduper != nil {
			oc.Deduper.Release(dedupKey)
		}
	}
	if err := oc.Resources.RevalidateResource(resource); err != nil {
		release()
		return OpenResult{}, preserveCode(err, CodePathNoHostMapping, "resource mapping changed before launch")
	}
	if oc.RevalidateIdentity == nil {
		release()
		return OpenResult{}, &Error{Code: CodeAppIdentityDrift, Reason: "launch-time app identity revalidation is unavailable"}
	}
	current, err := oc.RevalidateIdentity(binding.Application, identity)
	if err != nil || current.IdentityDigest() != identity.IdentityDigest() {
		release()
		return OpenResult{}, &Error{Code: CodeAppIdentityDrift, Reason: "host application identity changed before launch"}
	}
	if mode == appopen.ModeSafe {
		if err := appopen.PrepareSafetyProfileState(safetyProfile, safetyIdentity, safetyEffect); err != nil {
			release()
			return OpenResult{}, &Error{Code: CodeProviderUnavailable, Reason: "safe app state could not be prepared"}
		}
	}
	guard := func() error {
		if oc.ValidateLifecycle != nil {
			if err := oc.ValidateLifecycle(binding); err != nil {
				return err
			}
		}
		if err := oc.Resources.RevalidateResource(resource); err != nil {
			return preserveCode(err, CodePathNoHostMapping, "resource mapping changed at host launch boundary")
		}
		latest, err := oc.RevalidateIdentity(binding.Application, identity)
		if err != nil || latest.IdentityDigest() != identity.IdentityDigest() || latest.ExecutablePath != argv[0] {
			return &Error{Code: CodeAppIdentityDrift, Reason: "host application identity changed at launch boundary"}
		}
		return nil
	}
	if err := oc.Launcher.Run(ctx, argv, guard); err != nil {
		release()
		if CodeOf(err) != "" {
			return OpenResult{}, err
		}
		return OpenResult{}, &Error{Code: CodeProviderUnavailable, Reason: "host application failed to launch"}
	}
	if oc.Deduper != nil {
		oc.Deduper.Commit(dedupKey)
	}
	return result, nil
}

func preserveCode(err error, fallback, reason string) error {
	if err == nil {
		return errors.New(reason)
	}
	if CodeOf(err) != "" {
		return err
	}
	return &Error{Code: fallback, Reason: reason}
}
