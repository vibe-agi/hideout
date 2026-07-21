package manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

// deriveWorkspaceIDFromRoot computes the stable workspace identity from an
// already-captured root, using the exact key + derivation a run uses. Sharing
// this one function between run attachment and the trusted-host-app grant command
// guarantees, by construction, that the grant keys on the same workspaceID a
// run will present (analyze U1 / research D4). Root capture stays at each call
// site so run keeps its mount-safety ordering unchanged.
func (c Core) deriveWorkspaceIDFromRoot(canonicalRoot string, rootIdentity workspaceattach.RootFileIdentity) (string, error) {
	stateExists, err := workspaceAttachmentStateExists(c.Store.Root)
	if err != nil {
		return "", err
	}
	key, err := workspaceattach.LoadOrCreateIdentityKey(c.Store.Root, stateExists)
	if err != nil {
		return "", err
	}
	return workspaceattach.DeriveWorkspaceID(key, canonicalRoot, rootIdentity)
}

// TrustedHostAppGrantsVersion is the schema version of the per-profile trusted-host-app
// grant manifest.
const TrustedHostAppGrantsVersion = "hideout.trusted-host-app-grants/v1"

// TrustedHostAppGrant is durable operator policy authorizing a trusted (native)
// host-app open for one workspace + app binding. It carries only Core-derived
// identifiers: no host path, username, token, machine id, or raw argv.
type TrustedHostAppGrant struct {
	WorkspaceID     string    `json:"workspaceId"`
	QualifiedAppRef string    `json:"qualifiedAppRef"`
	BindingDigest   string    `json:"bindingDigest"`
	GrantedAt       time.Time `json:"grantedAt"`
}

// trustedHostAppGrantManifest is the per-profile grant file. It lives under the
// reserved, guest-unreachable store beside host-app-mode.json, keyed by profile, so
// a guest writing the workspace can never mint, refresh, or read a grant.
type trustedHostAppGrantManifest struct {
	Version string                `json:"version"`
	Profile string                `json:"profile"`
	Grants  []TrustedHostAppGrant `json:"grants"`
}

func trustedHostAppGrantsPath(storeRoot, profileName string) string {
	return filepath.Join(storeRoot, "profiles", profileName, "host-app-trust-grants.json")
}

func validTrustedHostAppGrant(g TrustedHostAppGrant) bool {
	return g.WorkspaceID != "" && g.QualifiedAppRef != "" && g.BindingDigest != ""
}

// readTrustedHostAppGrants returns the profile's grant manifest, or an empty
// (no-grants) manifest when the file is absent, unreadable, malformed, or
// contains any invalid entry. It fails closed to no grants and never to an
// implicit allow.
func readTrustedHostAppGrants(storeRoot, profileName string) trustedHostAppGrantManifest {
	empty := trustedHostAppGrantManifest{Version: TrustedHostAppGrantsVersion, Profile: profileName}
	raw, err := os.ReadFile(trustedHostAppGrantsPath(storeRoot, profileName))
	if err != nil {
		return empty
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest trustedHostAppGrantManifest
	if err := decoder.Decode(&manifest); err != nil {
		return empty
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		return empty // trailing JSON: malformed
	}
	if manifest.Version != TrustedHostAppGrantsVersion || manifest.Profile != profileName {
		return empty
	}
	for _, g := range manifest.Grants {
		if !validTrustedHostAppGrant(g) {
			return empty
		}
	}
	return manifest
}

// trustedHostAppGrantMatches reports whether a persistent grant authorizes this
// scope. It requires trusted host-app mode AND an exact match on the Core-derived
// workspace identity, app reference, and binding digest. Any inequality, safe
// mode, or missing/malformed manifest yields false (fail closed).
func trustedHostAppGrantMatches(storeRoot string, scope hostcap.GrantScope) bool {
	if scope.WorkspaceID == "" || scope.QualifiedAppRef == "" || scope.BindingDigest == "" {
		return false
	}
	if ReadProjectionHostAppMode(storeRoot, scope.Profile) != ProjectionHostAppModeTrusted {
		return false
	}
	for _, g := range readTrustedHostAppGrants(storeRoot, scope.Profile).Grants {
		if g.WorkspaceID == scope.WorkspaceID &&
			g.QualifiedAppRef == scope.QualifiedAppRef &&
			g.BindingDigest == scope.BindingDigest {
			return true
		}
	}
	return false
}

func writeTrustedHostAppGrants(storeRoot string, manifest trustedHostAppGrantManifest) error {
	if manifest.Profile == "" {
		return errors.New("trusted-host-app grant manifest requires a profile")
	}
	manifest.Version = TrustedHostAppGrantsVersion
	for _, g := range manifest.Grants {
		if !validTrustedHostAppGrant(g) {
			return fmt.Errorf("invalid trusted-host-app grant for workspace %q", g.WorkspaceID)
		}
	}
	dir := filepath.Join(storeRoot, "profiles", manifest.Profile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := trustedHostAppGrantsPath(storeRoot, manifest.Profile)
	tmp, err := os.CreateTemp(dir, ".host-app-trust-grants.json.tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keep := true
	defer func() {
		if keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	keep = false
	return nil
}

// addTrustedHostAppGrant records a grant for the profile. It is idempotent: an
// identical (workspace, app ref, digest) grant is a no-op success.
func addTrustedHostAppGrant(storeRoot, profileName string, grant TrustedHostAppGrant, now time.Time) error {
	if !validTrustedHostAppGrant(grant) {
		return errors.New("trusted-host-app grant requires workspace, app ref, and binding digest")
	}
	manifest := readTrustedHostAppGrants(storeRoot, profileName)
	manifest.Profile = profileName
	for _, existing := range manifest.Grants {
		if existing.WorkspaceID == grant.WorkspaceID &&
			existing.QualifiedAppRef == grant.QualifiedAppRef &&
			existing.BindingDigest == grant.BindingDigest {
			return nil
		}
	}
	grant.GrantedAt = now.UTC()
	manifest.Grants = append(manifest.Grants, grant)
	return writeTrustedHostAppGrants(storeRoot, manifest)
}

// removeTrustedHostAppGrantsForWorkspace drops every grant for one workspace under
// the profile. Removing an absent grant is a no-op success.
func removeTrustedHostAppGrantsForWorkspace(storeRoot, profileName, workspaceID string) error {
	manifest := readTrustedHostAppGrants(storeRoot, profileName)
	kept := make([]TrustedHostAppGrant, 0, len(manifest.Grants))
	changed := false
	for _, g := range manifest.Grants {
		if g.WorkspaceID == workspaceID {
			changed = true
			continue
		}
		kept = append(kept, g)
	}
	if !changed {
		return nil
	}
	manifest.Profile = profileName
	manifest.Grants = kept
	return writeTrustedHostAppGrants(storeRoot, manifest)
}

// removeAllTrustedHostAppGrants drops every trusted-host-app grant for the profile, used
// when the profile switches to safe mode.
func removeAllTrustedHostAppGrants(storeRoot, profileName string) error {
	err := os.Remove(trustedHostAppGrantsPath(storeRoot, profileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// hasTrustedHostAppGrants reports whether the profile has any host-app trust grant,
// for visibility surfaces (e.g. profile host-app-mode output).
func hasTrustedHostAppGrants(storeRoot, profileName string) bool {
	return len(readTrustedHostAppGrants(storeRoot, profileName).Grants) > 0
}

// HasHostAppTrustGrants reports whether the profile has any host-app trust
// grant, so the host-app-mode surface can show that a standing grant exists.
func (c Core) HasHostAppTrustGrants(profileName string) bool {
	name, err := normalizeManagerProfileName(profileName)
	if err != nil {
		return false
	}
	return hasTrustedHostAppGrants(c.Store.Root, name)
}

// trustedHostAppRequest is a run-written hint recording the exact Core-derived
// identity a trusted-mode open needed but lacked a grant for. It is NOT
// authority: it exists only so `allow host-app` can promote the run-observed
// app ref + binding digest (the digest depends on run-time editor observation,
// so it cannot be recomputed reliably by the command).
type trustedHostAppRequest struct {
	Version         string    `json:"version"`
	Profile         string    `json:"profile"`
	Command         string    `json:"command"`
	WorkspaceID     string    `json:"workspaceId"`
	QualifiedAppRef string    `json:"qualifiedAppRef"`
	BindingDigest   string    `json:"bindingDigest"`
	RecordedAt      time.Time `json:"recordedAt"`
}

func trustedHostAppRequestPath(storeRoot, profileName string) string {
	return filepath.Join(storeRoot, "profiles", profileName, "host-app-trust-request.json")
}

// maybeRecordTrustedHostAppRequest records a request when a trusted-mode open finds
// no grant. Best-effort: a failure to write a hint must never affect the run.
func maybeRecordTrustedHostAppRequest(storeRoot string, scope hostcap.GrantScope) {
	if scope.Profile == "" || scope.WorkspaceID == "" || scope.QualifiedAppRef == "" || scope.BindingDigest == "" {
		return
	}
	if ReadProjectionHostAppMode(storeRoot, scope.Profile) != ProjectionHostAppModeTrusted {
		return
	}
	dir := filepath.Join(storeRoot, "profiles", scope.Profile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	if scope.Command == "" {
		return
	}
	data, err := json.MarshalIndent(trustedHostAppRequest{
		Version: TrustedHostAppGrantsVersion, Profile: scope.Profile, Command: scope.Command,
		WorkspaceID: scope.WorkspaceID, QualifiedAppRef: scope.QualifiedAppRef,
		BindingDigest: scope.BindingDigest, RecordedAt: time.Now().UTC(),
	}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(trustedHostAppRequestPath(storeRoot, scope.Profile), append(data, '\n'), 0o600)
}

func readTrustedHostAppRequest(storeRoot, profileName string) (trustedHostAppRequest, bool) {
	raw, err := os.ReadFile(trustedHostAppRequestPath(storeRoot, profileName))
	if err != nil {
		return trustedHostAppRequest{}, false
	}
	var req trustedHostAppRequest
	if json.Unmarshal(raw, &req) != nil {
		return trustedHostAppRequest{}, false
	}
	if req.Command == "" || req.WorkspaceID == "" || req.QualifiedAppRef == "" || req.BindingDigest == "" {
		return trustedHostAppRequest{}, false
	}
	return req, true
}

// HostAppTrustResult reports the outcome of a host-app trust grant command.
type HostAppTrustResult struct {
	Profile     string `json:"profile"`
	WorkspaceID string `json:"workspaceId"`
	Command     string `json:"command"`
	Granted     bool   `json:"granted"`
}

// GrantHostAppTrust grants trusted (native) opening for the named projected
// host-app command in the workspace at workspacePath under the profile. It
// derives the workspaceID (proven equal to a run's), then promotes the
// run-written request's app ref + binding digest so the grant keys on exactly
// what a run will present. It requires trusted host-app mode and a request
// recorded for this exact project and command.
func (c Core) GrantHostAppTrust(profileName, workspacePath, command string) (HostAppTrustResult, error) {
	profileName, err := normalizeManagerProfileName(profileName)
	if err != nil {
		return HostAppTrustResult{}, err
	}
	if strings.TrimSpace(command) == "" {
		return HostAppTrustResult{}, errors.New("host-app trust requires a command")
	}
	if ReadProjectionHostAppMode(c.Store.Root, profileName) != ProjectionHostAppModeTrusted {
		return HostAppTrustResult{}, fmt.Errorf("profile %q is not in trusted host-app mode; run: hideout profile host-app-mode %s trusted", profileName, profileName)
	}
	workspaceID, err := c.deriveWorkspaceIDForCommand(workspacePath)
	if err != nil {
		return HostAppTrustResult{}, err
	}
	req, ok := readTrustedHostAppRequest(c.Store.Root, profileName)
	if !ok || req.WorkspaceID != workspaceID || req.Command != command {
		return HostAppTrustResult{}, fmt.Errorf("no host-app trust request for %q in this project yet; run `hideout run -- %s .` here once, then rerun `hideout allow host-app %s`", command, command, command)
	}
	grant := TrustedHostAppGrant{WorkspaceID: workspaceID, QualifiedAppRef: req.QualifiedAppRef, BindingDigest: req.BindingDigest}
	result := HostAppTrustResult{Profile: profileName, WorkspaceID: workspaceID, Command: command}
	err = c.withProfileMutationLock(profileName, func() error {
		existing := readTrustedHostAppGrants(c.Store.Root, profileName)
		already := false
		for _, g := range existing.Grants {
			if g.WorkspaceID == grant.WorkspaceID && g.QualifiedAppRef == grant.QualifiedAppRef && g.BindingDigest == grant.BindingDigest {
				already = true
				break
			}
		}
		if err := addTrustedHostAppGrant(c.Store.Root, profileName, grant, time.Now()); err != nil {
			return err
		}
		result.Granted = !already
		return nil
	})
	if err != nil {
		return HostAppTrustResult{}, err
	}
	_ = c.emitOperatorCenterAudit(audit.Event{
		Profile: profileName, Backend: "native", Action: "host-app.trust", Decision: "grant",
		Details: map[string]any{"profile": profileName, "command": command, "workspaceId": workspaceID, "qualifiedAppRef": req.QualifiedAppRef, "bindingDigest": req.BindingDigest},
	})
	return result, nil
}

// RevokeHostAppTrust removes the trusted host-app grants for the workspace at
// workspacePath under the profile. Removing an absent grant is a no-op success.
// The command name is recorded in audit; MVP removes the workspace's grants.
func (c Core) RevokeHostAppTrust(profileName, workspacePath, command string) error {
	profileName, err := normalizeManagerProfileName(profileName)
	if err != nil {
		return err
	}
	workspaceID, err := c.deriveWorkspaceIDForCommand(workspacePath)
	if err != nil {
		return err
	}
	err = c.withProfileMutationLock(profileName, func() error {
		return removeTrustedHostAppGrantsForWorkspace(c.Store.Root, profileName, workspaceID)
	})
	if err != nil {
		return err
	}
	_ = c.emitOperatorCenterAudit(audit.Event{
		Profile: profileName, Backend: "native", Action: "host-app.trust", Decision: "revoke",
		Details: map[string]any{"profile": profileName, "command": command, "workspaceId": workspaceID},
	})
	return nil
}

// deriveWorkspaceIDForCommand captures the root at workspacePath, checks mount
// safety, and derives the workspaceID with the same path a run uses.
func (c Core) deriveWorkspaceIDForCommand(workspacePath string) (string, error) {
	canonicalRoot, rootIdentity, err := workspaceattach.CaptureRootIdentity(workspacePath)
	if err != nil {
		return "", fmt.Errorf("capture workspace root identity: %w", err)
	}
	if err := ValidateWorkspaceMountSafety(canonicalRoot, c.Store.Root); err != nil {
		return "", err
	}
	return c.deriveWorkspaceIDFromRoot(canonicalRoot, rootIdentity)
}
