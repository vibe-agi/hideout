package manager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/hostfs/readgrant"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestHostFSReadProviderDeduplicatesAcrossInstancesWithoutExtendingDeadline(t *testing.T) {
	fixture := newHostFSReadFixture(t)
	defer fixture.owner.Close()
	second, err := newHostFSReadProvider(fixture.core, fixture.sessionID, fixture.readDir, fixture.ownerPath, fixture.policy)
	if err != nil {
		t.Fatal(err)
	}
	proposal := fixture.proposal(fixture.file)
	if _, _, err := fixture.provider.validateProposal(proposal); err != nil {
		t.Fatalf("fixture proposal is not eligible: %v", err)
	}

	const callers = 24
	results := make(chan broker.HostFSReadProposalResult, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		provider := fixture.provider
		if i%2 == 1 {
			provider = second
		}
		go func() {
			defer wg.Done()
			result, err := provider.ProposeHostFSRead(context.Background(), proposal)
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent proposal: %v", err)
		}
	}
	var decisionID string
	for result := range results {
		if result.Code != broker.HostFSErrorReadApprovalRequired || result.DecisionRef == "" {
			t.Fatalf("proposal result=%+v", result)
		}
		if decisionID == "" {
			decisionID = result.DecisionRef
		}
		if result.DecisionRef != decisionID {
			t.Fatalf("dedup drift: %q != %q", result.DecisionRef, decisionID)
		}
	}

	state := fixture.loadState(t)
	if len(state.Requests) != 1 || len(state.CreationTimes) != 1 {
		t.Fatalf("provider state=%+v", state)
	}
	for _, record := range state.Requests {
		if record.Revision != 1 || record.SuppressedCount != callers-1 || !record.TimeoutAt.Equal(record.CreatedAt.Add(hostFSReadDecisionTimeout)) {
			t.Fatalf("dedup changed authority lifecycle: %+v", record)
		}
	}
	decisions, err := fixture.core.ListDecisions(DecisionListRequest{Kind: decision.KindHostFSRead, IncludeTerminal: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].ID != decisionID || decisions[0].Revision != 1 {
		t.Fatalf("generic decisions=%+v", decisions)
	}
}

func TestHostFSReadProviderEnforcesPendingAndRollingRateLimitsWithoutFalseReference(t *testing.T) {
	fixture := newHostFSReadFixture(t)
	defer fixture.owner.Close()
	for i := 0; i < hostFSReadPendingLimit; i++ {
		path := filepath.Join(fixture.hostRoot, "pending-"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := fixture.provider.ProposeHostFSRead(context.Background(), fixture.proposal(path))
		if err != nil || result.Code != broker.HostFSErrorReadApprovalRequired {
			t.Fatalf("proposal %d result=%+v err=%v", i, result, err)
		}
	}
	extra := filepath.Join(fixture.hostRoot, "pending-extra.txt")
	if err := os.WriteFile(extra, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	limited, err := fixture.provider.ProposeHostFSRead(context.Background(), fixture.proposal(extra))
	if err != nil {
		t.Fatal(err)
	}
	if limited.Code != broker.HostFSErrorReadRequestLimited || limited.DecisionRef != "" || limited.RetryAfterMS != nil {
		t.Fatalf("pending limit produced false authority: %+v", limited)
	}

	rateFixture := newHostFSReadFixture(t)
	defer rateFixture.owner.Close()
	now := time.Now().UTC()
	store := rateFixture.provider.store
	if err := store.WithExclusive(func() error {
		state, err := store.LoadState()
		if err != nil {
			return err
		}
		for i := 0; i < hostFSReadRateLimit; i++ {
			state.CreationTimes = append(state.CreationTimes, now.Add(-30*time.Second))
		}
		state.UpdatedAt = now
		return store.SaveState(state)
	}); err != nil {
		t.Fatal(err)
	}
	rateFixture.provider.now = func() time.Time { return now }
	rateLimited, err := rateFixture.provider.ProposeHostFSRead(context.Background(), rateFixture.proposal(rateFixture.file))
	if err != nil {
		t.Fatal(err)
	}
	if rateLimited.Code != broker.HostFSErrorReadRequestLimited || rateLimited.DecisionRef != "" || rateLimited.RetryAfterMS == nil || *rateLimited.RetryAfterMS < 29_000 || *rateLimited.RetryAfterMS > 30_000 {
		t.Fatalf("rate limit result=%+v", rateLimited)
	}
}

func TestSanitizeHostFSReadReasonStripsTerminalControlsAndPreservesUTF8Boundary(t *testing.T) {
	raw := "inspect \x1b[31mred\x1b[0m\nspoof\u202e " + strings.Repeat("界", 300)
	got := sanitizeHostFSReadReason(raw)
	if !utf8.ValidString(got) || len([]byte(got)) > hostFSReadReasonMaxBytes {
		t.Fatalf("sanitized reason has invalid boundary: bytes=%d valid=%t", len([]byte(got)), utf8.ValidString(got))
	}
	for _, r := range got {
		if !unicode.IsPrint(r) {
			t.Fatalf("sanitized reason retained non-printable rune %U in %q", r, got)
		}
	}
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\n") || strings.ContainsRune(got, '\u202e') {
		t.Fatalf("sanitized reason retained terminal/format controls: %q", got)
	}
}

func TestHostFSReadApprovalActivatesSameRunningServiceAndReopenRequiresLiveOwner(t *testing.T) {
	fixture := newHostFSReadFixture(t)
	service := hostfs.NewService(fixture.policy)
	service.ReadAuthority = fixture.provider
	if _, err := service.Read(fixture.file, 0, 64); !errors.Is(err, hostfs.ErrReadApprovalRequired) {
		t.Fatalf("pre-approval read=%v", err)
	}
	proposalResult, err := fixture.provider.ProposeHostFSRead(context.Background(), fixture.proposal(fixture.file))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := fixture.core.ClaimDecision(DecisionClaimRequest{DecisionID: proposalResult.DecisionRef, ExpectedVersion: decision.DecisionVersion, Surface: "test"})
	if err != nil {
		t.Fatal(err)
	}
	separateCore := Core{Store: profile.Store{Root: fixture.core.Store.Root}}
	resolution, err := separateCore.ApproveDecision(DecisionResolveRequest{DecisionID: proposalResult.DecisionRef, ExpectedVersion: decision.DecisionVersion, ClaimToken: claim.ClaimToken, Reason: "operator-approved"})
	if err != nil {
		t.Fatalf("separate-process approval: %v", err)
	}
	if resolution.Status != decision.StateApplied || resolution.ProviderResult["generation"] == nil {
		t.Fatalf("resolution=%+v", resolution)
	}
	canonical, err := filepath.EvalSymlinks(fixture.file)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := fixture.provider.AllowsRead(hostfs.ReadGrantCheck{RequestedPath: fixture.file, CanonicalPath: canonical, Visibility: fixture.policy.Visibility(fixture.file)})
	if err != nil || !allowed {
		var manifest readgrant.Manifest
		_ = fixture.provider.store.WithShared(func() error {
			manifest, _ = fixture.provider.store.LoadManifest()
			return nil
		})
		t.Fatalf("published grant is not active: allowed=%t err=%v manifest=%+v", allowed, err, manifest)
	}
	result, err := service.Read(fixture.file, 0, 64)
	if err != nil || string(result.Data) != "secret fixture" {
		t.Fatalf("same-process next retry result=%+v err=%v", result, err)
	}
	info, err := service.Stat(fixture.file)
	if err != nil || info.Coarse || info.Size != int64(len("secret fixture")) {
		t.Fatalf("approved stat=%+v err=%v", info, err)
	}

	deniedFixture := newHostFSReadFixture(t)
	proposalResult, err = deniedFixture.provider.ProposeHostFSRead(context.Background(), deniedFixture.proposal(deniedFixture.file))
	if err != nil {
		t.Fatal(err)
	}
	claim, err = deniedFixture.core.ClaimDecision(DecisionClaimRequest{DecisionID: proposalResult.DecisionRef, Surface: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deniedFixture.core.DenyDecision(DecisionResolveRequest{DecisionID: proposalResult.DecisionRef, ClaimToken: claim.ClaimToken}); err != nil {
		t.Fatal(err)
	}
	terminalRetry, err := deniedFixture.provider.ProposeHostFSRead(context.Background(), deniedFixture.proposal(deniedFixture.file))
	if err != nil {
		t.Fatal(err)
	}
	if terminalRetry.Code != broker.HostFSErrorReadDenied || terminalRetry.DecisionRef != "" {
		t.Fatalf("terminal decision was not remembered without false pending authority: %+v", terminalRetry)
	}
	if _, err := deniedFixture.core.ReopenDecision(HostFSReadReopenRequest{DecisionID: proposalResult.DecisionRef, ExpectedVersion: decision.DecisionVersion}); err != nil {
		t.Fatalf("live reopen: %v", err)
	}
	claim, err = deniedFixture.core.ClaimDecision(DecisionClaimRequest{DecisionID: proposalResult.DecisionRef, Surface: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deniedFixture.core.DenyDecision(DecisionResolveRequest{DecisionID: proposalResult.DecisionRef, ClaimToken: claim.ClaimToken}); err != nil {
		t.Fatal(err)
	}
	if err := deniedFixture.owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(deniedFixture.readDir); err != nil {
		t.Fatal(err)
	}
	if _, err := deniedFixture.core.ReopenDecision(HostFSReadReopenRequest{DecisionID: proposalResult.DecisionRef}); err == nil {
		t.Fatal("dead-session reopen should fail closed")
	}
	if _, err := os.Stat(deniedFixture.readDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dead-session reopen recreated provider authority: %v", err)
	}
}

func TestHostFSReadGrantRejectsSymlinkRetargetPolicyDenyExpiryAndMalformedState(t *testing.T) {
	fixture := newHostFSReadFixture(t)
	defer fixture.owner.Close()
	other := filepath.Join(fixture.hostRoot, "other.txt")
	if err := os.WriteFile(other, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(fixture.hostRoot, "link.txt")
	if err := os.Symlink(fixture.file, link); err != nil {
		t.Fatal(err)
	}
	activateHostFSReadFixture(t, fixture, link)
	service := hostfs.NewService(fixture.policy)
	service.ReadAuthority = fixture.provider
	if _, err := service.Read(link, 0, 64); err != nil {
		t.Fatalf("approved symlink read: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Read(link, 0, 64); !errors.Is(err, hostfs.ErrReadApprovalRequired) {
		t.Fatalf("retargeted symlink inherited authority: %v", err)
	}

	denyPolicy := fixture.policy
	denyPolicy.Deny = append(denyPolicy.Deny, hostfs.Grant{Rule: hostfs.Rule{ID: "hfs_deny", HostPath: fixture.file, Ops: []hostfs.Op{hostfs.OpRead}, Scope: hostfs.ScopeExactFile, Reason: "test deny"}, Source: hostfs.SourceRun})
	fixture.provider.policy = denyPolicy
	service.Policy = denyPolicy
	if _, err := service.Read(fixture.file, 0, 64); !errors.Is(err, hostfs.ErrReadDenied) {
		t.Fatalf("current read deny did not close grant: %v", err)
	}

	fixture.provider.policy = fixture.policy
	service.Policy = fixture.policy
	fixture.provider.now = func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }
	if _, err := service.Read(fixture.file, 0, 64); !errors.Is(err, hostfs.ErrReadApprovalRequired) {
		t.Fatalf("expired grant remained active: %v", err)
	}
	fixture.provider.now = time.Now
	if err := os.WriteFile(fixture.provider.store.GrantsPath(), []byte(`{"version":"broken"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Read(fixture.file, 0, 64); !errors.Is(err, hostfs.ErrHostPrerequisite) {
		t.Fatalf("malformed grant state did not fail closed: %v", err)
	}
}

func TestHostFSReadOperatorSurfacesOmitSecretsContentSymlinkTargetAndPrivateState(t *testing.T) {
	fixture := newHostFSReadFixture(t)
	defer fixture.owner.Close()
	content := "TOP-SECRET-FILE-CONTENT-029"
	if err := os.WriteFile(fixture.file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(fixture.hostRoot, "visible-link.txt")
	if err := os.Symlink(fixture.file, link); err != nil {
		t.Fatal(err)
	}
	machineID := "0123456789abcdef0123456789abcdef"
	brokerToken := "cap_0123456789abcdef0123456789abcdef"
	secretValue := "proxy-password-029"
	proposal := fixture.proposal(link)
	proposal.UntrustedReason = "inspect machineId=" + machineID + " token=" + brokerToken + " HIDEOUT_SECRET_PROXY=" + secretValue
	observer := &detailRecordingObserver{}
	fixture.provider.core.Observer = observer
	fixture.core.Observer = observer
	created, err := fixture.provider.ProposeHostFSRead(context.Background(), proposal)
	if err != nil {
		t.Fatal(err)
	}
	d, err := fixture.core.InspectDecision(created.DecisionRef)
	if err != nil {
		t.Fatal(err)
	}
	decisionBody, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decisionBody), link) {
		t.Fatalf("operator decision should retain the requested path: %s", decisionBody)
	}

	claim, err := fixture.core.ClaimDecision(DecisionClaimRequest{DecisionID: d.ID, Surface: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.core.ApproveDecision(DecisionResolveRequest{
		DecisionID: d.ID,
		ClaimToken: claim.ClaimToken,
		Reason:     "approved machineId=" + machineID + " token=" + brokerToken,
	}); err != nil {
		t.Fatal(err)
	}
	auditBody, err := os.ReadFile(filepath.Join(fixture.core.Store.Root, "operator-center", "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	eventBody, err := json.Marshal(observer.events)
	if err != nil {
		t.Fatal(err)
	}
	combined := string(decisionBody) + "\n" + string(auditBody) + "\n" + string(eventBody)
	for _, forbidden := range []string{
		content,
		fixture.file,
		fixture.readDir,
		claim.ClaimToken,
		machineID,
		brokerToken,
		secretValue,
	} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("operator evidence leaked %q:\n%s", forbidden, combined)
		}
	}
}

func TestHostFSReadPolicyRevalidationSupportsImmutableRunAndEnvironmentSources(t *testing.T) {
	fixture := newHostFSReadFixture(t)
	defer fixture.owner.Close()
	for _, source := range []hostfs.Source{hostfs.SourceRun, hostfs.SourceEnvironment} {
		record := readgrant.RequestRecord{
			VisibilityRule:   "hfs_session_visibility",
			VisibilitySource: string(source),
			VisibilityRoot:   fixture.hostRoot,
			VisibilityScope:  string(hostfs.ScopeRecursiveDir),
			Profile:          "default",
		}
		policy, err := fixture.core.hostFSReadPolicyForRecord(record)
		if err != nil {
			t.Fatalf("source %s: %v", source, err)
		}
		visibility := policy.Visibility(fixture.file)
		if !visibility.ExplicitDomain || visibility.RuleID != record.VisibilityRule || visibility.Source != source {
			t.Fatalf("source %s revalidation visibility=%+v", source, visibility)
		}
	}
}

type hostFSReadFixture struct {
	core      Core
	sessionID string
	readDir   string
	ownerPath string
	hostRoot  string
	file      string
	policy    hostfs.EffectivePolicy
	provider  *hostFSReadProvider
	owner     *readgrant.Owner
}

func newHostFSReadFixture(t *testing.T) *hostFSReadFixture {
	t.Helper()
	storeRoot := t.TempDir()
	hostRoot := t.TempDir()
	file := filepath.Join(hostRoot, "report.txt")
	if err := os.WriteFile(file, []byte("secret fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := profile.Default("default")
	p.HostFS.Grants = []hostfs.Rule{{ID: "hfs_see_fixture", HostPath: hostRoot, Ops: []hostfs.Op{hostfs.OpDiscover}, Scope: hostfs.ScopeRecursiveDir, Reason: "test visibility"}}
	store := profile.Store{Root: storeRoot}
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	policy, err := hostfs.Build(hostfs.BuildInput{Profile: p.HostFS, StoreRoot: storeRoot})
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "ses_test"
	readDir := filepath.Join(storeRoot, "sessions", sessionID, "hostfs-read")
	ownerPath := filepath.Join(readDir, "owner.lock")
	owner, err := readgrant.AcquireOwner(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	core := Core{Store: store}
	provider, err := newHostFSReadProvider(core, sessionID, readDir, ownerPath, policy)
	if err != nil {
		owner.Close()
		t.Fatal(err)
	}
	// Keep the flock owner strongly reachable for the full test. Otherwise the
	// os.File finalizer may close a logically live fixture after the compiler
	// proves its Owner field is no longer read.
	t.Cleanup(func() { _ = owner.Close() })
	return &hostFSReadFixture{core: core, sessionID: sessionID, readDir: readDir, ownerPath: ownerPath, hostRoot: hostRoot, file: file, policy: provider.policy, provider: provider, owner: owner}
}

func (f *hostFSReadFixture) proposal(path string) broker.HostFSReadProposal {
	canonical, _ := filepath.EvalSymlinks(path)
	return broker.HostFSReadProposal{
		SessionID:     f.sessionID,
		Profile:       "default",
		Backend:       "lima",
		RequestedPath: path,
		CanonicalPath: canonical,
		Visibility:    f.policy.Visibility(path),
	}
}

func (f *hostFSReadFixture) loadState(t *testing.T) readgrant.ProviderState {
	t.Helper()
	var state readgrant.ProviderState
	if err := f.provider.store.WithShared(func() error {
		var err error
		state, err = f.provider.store.LoadState()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return state
}

func activateHostFSReadFixture(t *testing.T, fixture *hostFSReadFixture, path string) string {
	t.Helper()
	proposal, err := fixture.provider.ProposeHostFSRead(context.Background(), fixture.proposal(path))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := fixture.core.ClaimDecision(DecisionClaimRequest{DecisionID: proposal.DecisionRef, Surface: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.core.ApproveDecision(DecisionResolveRequest{DecisionID: proposal.DecisionRef, ClaimToken: claim.ClaimToken}); err != nil {
		t.Fatal(err)
	}
	return proposal.DecisionRef
}
