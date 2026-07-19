package manager

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/hostcap"
)

// projectionDeduper suppresses identical projection opens within a short window
// so an agent cannot flood the host with windows. It is safe for concurrent use.
type projectionDeduper struct {
	mu     sync.Mutex
	seen   map[string]projectionDedupEntry
	window time.Duration
	now    func() time.Time
}

type projectionDedupEntry struct {
	at        time.Time
	committed bool
}

func newProjectionDeduper() *projectionDeduper {
	return &projectionDeduper{
		seen:   map[string]projectionDedupEntry{},
		window: 2 * time.Second,
		now:    time.Now,
	}
}

// ProjectionIdeModeSafe / ...Trusted are the persisted per-profile IDE modes.
const (
	ProjectionIdeModeSafe    = "safe"
	ProjectionIdeModeTrusted = "trusted-host-ide"
)

type projectionIdeModeState struct {
	Mode  string    `json:"mode"`
	SetAt time.Time `json:"setAt"`
}

// projectionIdeModePath is the per-profile IDE-mode file. It lives under the
// reserved, guest-unreachable store, keyed by profile — so the guest cannot flip
// itself into trusted mode by writing to the workspace.
func projectionIdeModePath(storeRoot, profileName string) string {
	return filepath.Join(storeRoot, "profiles", profileName, "ide-mode.json")
}

// ReadProjectionIdeMode returns the persisted mode for a profile, defaulting to
// safe when unset or unreadable (fail closed to the least-authority mode).
func ReadProjectionIdeMode(storeRoot, profileName string) string {
	raw, err := os.ReadFile(projectionIdeModePath(storeRoot, profileName))
	if err != nil {
		return ProjectionIdeModeSafe
	}
	var st projectionIdeModeState
	if err := json.Unmarshal(raw, &st); err != nil {
		return ProjectionIdeModeSafe
	}
	if st.Mode == ProjectionIdeModeTrusted {
		return ProjectionIdeModeTrusted
	}
	return ProjectionIdeModeSafe
}

// WriteProjectionIdeMode persists the requested IDE mode for a profile. This
// file is preference only; it is never sufficient authority for trusted mode.
func WriteProjectionIdeMode(storeRoot, profileName, mode string, now time.Time) error {
	mode = strings.TrimSpace(mode)
	if mode != ProjectionIdeModeSafe && mode != ProjectionIdeModeTrusted {
		return errors.New("ide mode must be safe or trusted-host-ide")
	}
	dir := filepath.Join(storeRoot, "profiles", profileName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(projectionIdeModeState{Mode: mode, SetAt: now.UTC()}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(projectionIdeModePath(storeRoot, profileName), append(data, '\n'), 0o600)
}

// ProjectionIdeMode returns the persisted IDE mode for a profile.
func (c Core) ProjectionIdeMode(profileName string) (string, error) {
	profileName, err := normalizeProfileNameForProjection(profileName)
	if err != nil {
		return "", err
	}
	return ReadProjectionIdeMode(c.Store.Root, profileName), nil
}

// SetProjectionIdeMode persists the requested mode. Trusted mode creates no
// authority by itself: each run creates a separately claimable decision bound
// to that run. Selecting safe invalidates all active grants for the profile.
func (c Core) SetProjectionIdeMode(profileName, mode string) error {
	profileName, err := normalizeProfileNameForProjection(profileName)
	if err != nil {
		return err
	}
	if _, err := c.Store.Load(profileName); err != nil {
		return err
	}
	if err := WriteProjectionIdeMode(c.Store.Root, profileName, mode, time.Now()); err != nil {
		return err
	}
	mode = strings.TrimSpace(mode)
	auditDecision := "request"
	if mode == ProjectionIdeModeSafe {
		auditDecision = "revoke"
		if err := c.invalidateProjectionGrantsForProfile(profileName, "profile-mode-set-safe"); err != nil {
			return err
		}
	}
	_ = c.emitOperatorCenterAudit(audit.Event{
		Profile:  profileName,
		Backend:  "native",
		Action:   "host-app.ide-mode",
		Decision: auditDecision,
		Details:  map[string]any{"profile": profileName, "mode": mode},
	})
	return nil
}

func normalizeProfileNameForProjection(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("profile name is required")
	}
	return name, nil
}

type projectionGrantBinding struct {
	SessionID       string
	Profile         string
	Backend         string
	RunID           string
	WorkspaceID     string
	EnvironmentID   string
	ProfileID       string
	IdentityID      string
	PackID          string
	RevisionID      string
	BindingID       string
	QualifiedApp    string
	BindingDigest   string
	Command         string
	Subject         string
	ResourceClasses string
	HostFSAuthority string
	HostFSOwner     string
}

func projectionGrantBindingForRun(runSession RunSession, authority runSessionWorkspaceAuthority, appBinding hostcap.OpenResourceBinding, command string) projectionGrantBinding {
	environmentID := strings.TrimSpace(runSession.Environment.Record.ID)
	if environmentID == "" {
		environmentID = "recordless:" + runSession.Layout.ID
	}
	resourceClasses := projectionDecisionResourceClasses(appBinding.ResourceKinds)
	hostFSAuthority, hostFSOwner := "", ""
	if strings.Contains(","+resourceClasses+",", ",hostfs-portal,") {
		hostFSAuthority = "existing-content-only"
		hostFSOwner = "same-session"
	}
	return projectionGrantBinding{
		SessionID:       runSession.Layout.ID,
		Profile:         runSession.Plan.ProfileName,
		Backend:         runSession.Plan.Backend,
		RunID:           runSession.Layout.ID,
		WorkspaceID:     authority.WorkspaceID,
		EnvironmentID:   environmentID,
		ProfileID:       runSession.Plan.RuntimeProfile.Metadata["profileId"],
		IdentityID:      runSession.Plan.RuntimeProfile.Metadata["identityId"],
		PackID:          appBinding.PackID,
		RevisionID:      appBinding.RevisionID,
		BindingID:       appBinding.BindingID,
		QualifiedApp:    appBinding.QualifiedAppRef,
		BindingDigest:   appBinding.BindingDigest,
		Command:         command,
		Subject:         "command:" + command,
		ResourceClasses: resourceClasses,
		HostFSAuthority: hostFSAuthority,
		HostFSOwner:     hostFSOwner,
	}
}

func projectionDecisionResourceClasses(kinds []hostcap.ResourceKind) string {
	classes := make([]string, 0, len(kinds))
	seen := map[string]bool{}
	for _, kind := range kinds {
		class := hostcap.PublicResourceClass(kind)
		if class != "" && !seen[class] {
			seen[class] = true
			classes = append(classes, class)
		}
	}
	sort.Strings(classes)
	return strings.Join(classes, ",")
}

func projectionGrantScopeBase(runSession RunSession, authority runSessionWorkspaceAuthority) hostcap.GrantScope {
	return projectionGrantBindingForRun(runSession, authority, hostcap.OpenResourceBinding{}, "").scope()
}

func (b projectionGrantBinding) decisionID() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		b.SessionID, b.Profile, b.Backend, b.RunID, b.WorkspaceID, b.EnvironmentID,
		b.ProfileID, b.IdentityID, b.PackID, b.RevisionID, b.BindingID,
		b.QualifiedApp, b.BindingDigest, b.Command, b.Subject,
		b.ResourceClasses, b.HostFSAuthority, b.HostFSOwner,
	}, "\x00")))
	return fmt.Sprintf("dec_ide_%x", sum[:12])
}

func (b projectionGrantBinding) data() map[string]any {
	return map[string]any{
		"workspaceId":     b.WorkspaceID,
		"environmentId":   b.EnvironmentID,
		"profileId":       b.ProfileID,
		"identityId":      b.IdentityID,
		"runId":           b.RunID,
		"packId":          b.PackID,
		"revisionId":      b.RevisionID,
		"bindingId":       b.BindingID,
		"qualifiedAppRef": b.QualifiedApp,
		"bindingDigest":   b.BindingDigest,
		"command":         b.Command,
		"subject":         b.Subject,
		"resourceClasses": b.ResourceClasses,
		"hostfsAuthority": b.HostFSAuthority,
		"hostfsOwner":     b.HostFSOwner,
	}
}

func (b projectionGrantBinding) scope() hostcap.GrantScope {
	return hostcap.GrantScope{
		SessionID: b.SessionID, Profile: b.Profile, RunID: b.RunID, WorkspaceID: b.WorkspaceID,
		EnvironmentID: b.EnvironmentID, PackID: b.PackID, RevisionID: b.RevisionID,
		BindingID: b.BindingID, QualifiedAppRef: b.QualifiedApp, BindingDigest: b.BindingDigest, Command: b.Command,
	}
}

func (c Core) ensureProjectionTrustedDecision(binding projectionGrantBinding) (decision.Decision, error) {
	if ReadProjectionIdeMode(c.Store.Root, binding.Profile) != ProjectionIdeModeTrusted {
		return decision.Decision{}, errors.New("trusted-host-ide is not requested for profile")
	}
	store, err := c.decisionStore()
	if err != nil {
		return decision.Decision{}, err
	}
	id := binding.decisionID()
	if existing, err := store.RawDecision(id); err == nil {
		if !projectionGrantMatches(existing, binding) {
			return decision.Decision{}, errors.New("host-app open-resource decision binding mismatch")
		}
		return decision.RedactDecision(existing), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return decision.Decision{}, err
	}
	now := time.Now().UTC()
	workspaceWritable := strings.Contains(","+binding.ResourceClasses+",", ",workspace,")
	d := decision.Decision{
		ID:   id,
		Kind: decision.KindHostAppOpenResource,
		Source: decision.Source{
			Profile: binding.Profile,
			Session: binding.SessionID,
			Backend: binding.Backend,
			Surface: "projection",
		},
		State: decision.StatePending,
		Risk:  map[string]any{"riskClass": "high", "workspaceWritable": workspaceWritable},
		ProposedAction: map[string]any{
			"capability": "host.app.open-resource",
			"mode":       ProjectionIdeModeTrusted,
			"binding":    binding.data(),
		},
		Preview: decision.Preview{
			Summary: "Allow this exact host-app binding to open its declared resource classes for this run",
			Facts: map[string]any{
				"mode":            ProjectionIdeModeTrusted,
				"workspaceId":     binding.WorkspaceID,
				"environmentId":   binding.EnvironmentID,
				"subject":         binding.Subject,
				"resourceClasses": binding.ResourceClasses,
				"hostfsAuthority": binding.HostFSAuthority,
				"hostfsOwner":     binding.HostFSOwner,
			},
		},
		AllowedActions: []string{decision.ActionApprove, decision.ActionDeny, decision.ActionRevoke},
		DefaultOutcome: decision.DefaultOutcomeDeny,
		TimeoutAt:      now.Add(15 * time.Minute),
		ProviderRef: decision.ProviderRef{
			Provider:  decision.KindHostAppOpenResource,
			SessionID: binding.SessionID,
			Data:      binding.data(),
		},
		AuditRef:  "audit:host-app-open-resource:" + id,
		CreatedAt: now,
	}
	return c.CreateDecision(d)
}

func projectionGrantMatches(d decision.Decision, binding projectionGrantBinding) bool {
	if d.ID != binding.decisionID() || d.Kind != decision.KindHostAppOpenResource || d.Source.Session != binding.SessionID || d.Source.Profile != binding.Profile || d.Source.Backend != binding.Backend || d.ProviderRef.Provider != decision.KindHostAppOpenResource || d.ProviderRef.SessionID != binding.SessionID {
		return false
	}
	for key, expected := range binding.data() {
		if fmt.Sprint(d.ProviderRef.Data[key]) != fmt.Sprint(expected) {
			return false
		}
	}
	return true
}

// decisionIdeGrantChecker admits only the exact approved decision for the
// current run binding. The requested profile mode is necessary but never
// sufficient, and a stale/denied/revoked record carries no authority.
type decisionIdeGrantChecker struct {
	storeRoot string
	bindings  map[string]projectionGrantBinding
}

func (g decisionIdeGrantChecker) TrustedGrantActive(scope hostcap.GrantScope) bool {
	binding, ok := g.bindings[scope.Command]
	if !ok || g.storeRoot == "" || scope != binding.scope() || ReadProjectionIdeMode(g.storeRoot, scope.Profile) != ProjectionIdeModeTrusted {
		return false
	}
	d, err := decision.NewStore(g.storeRoot).RawDecision(binding.decisionID())
	return err == nil && d.State == decision.StateApproved && projectionGrantMatches(d, binding)
}

func (g decisionIdeGrantChecker) TrustedGrantActiveForResource(scope hostcap.GrantScope, resource hostcap.ResourceRef) bool {
	binding, ok := g.bindings[scope.Command]
	class := hostcap.PublicResourceClass(resource.Kind)
	if !ok || class == "" || !strings.Contains(","+binding.ResourceClasses+",", ","+class+",") {
		return false
	}
	return g.TrustedGrantActive(scope)
}

func (c Core) invalidateProjectionGrant(decisionID, reason string) error {
	if strings.TrimSpace(decisionID) == "" {
		return nil
	}
	store, err := c.decisionStore()
	if err != nil {
		return err
	}
	res, updated, err := store.InvalidateProviderDecision(decisionID, decision.KindHostAppOpenResource, reason)
	if err != nil {
		return err
	}
	if res.Decision == "unchanged" {
		return nil
	}
	if err := c.emitDecisionAudit(decision.ActionDecisionRevoke, "deny", updated, map[string]any{"reason": reason}); err != nil {
		return err
	}
	c.emitDecision(updated, decision.StateStale, reason)
	return nil
}

func (c Core) invalidateProjectionGrantsForProfile(profileName, reason string) error {
	store, err := c.decisionStore()
	if err != nil {
		return err
	}
	decisions, err := store.Decisions(decision.ListFilter{Kind: decision.KindHostAppOpenResource, Profile: profileName, IncludeTerminal: true})
	if err != nil {
		return err
	}
	for _, d := range decisions {
		if err := c.invalidateProjectionGrant(d.ID, reason); err != nil {
			return err
		}
	}
	return nil
}

func (c Core) invalidateProjectionGrantsForSession(profileName, sessionID, reason string) error {
	store, err := c.decisionStore()
	if err != nil {
		return err
	}
	decisions, err := store.Decisions(decision.ListFilter{
		Kind: decision.KindHostAppOpenResource, Profile: profileName, Session: sessionID, IncludeTerminal: true,
	})
	if err != nil {
		return err
	}
	for _, d := range decisions {
		if err := c.invalidateProjectionGrant(d.ID, reason); err != nil {
			return err
		}
	}
	return nil
}

// Reserve claims a key until the launch commits or releases it.
func (d *projectionDeduper) Reserve(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	// Opportunistically evict stale entries.
	for k, entry := range d.seen {
		if now.Sub(entry.at) > d.window {
			delete(d.seen, k)
		}
	}
	if entry, ok := d.seen[key]; ok && now.Sub(entry.at) <= d.window {
		return false
	}
	d.seen[key] = projectionDedupEntry{at: now}
	return true
}

func (d *projectionDeduper) Commit(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[key]; ok {
		d.seen[key] = projectionDedupEntry{at: d.now(), committed: true}
	}
}

func (d *projectionDeduper) Release(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if entry, ok := d.seen[key]; ok && !entry.committed {
		delete(d.seen, key)
	}
}
