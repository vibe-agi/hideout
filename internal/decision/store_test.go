package decision

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStoreAtomicClaimTimeoutAndNoPartialFile(t *testing.T) {
	root := t.TempDir()
	store := NewStoreAt(root)
	now := time.Date(2026, 7, 8, 2, 0, 0, 0, time.UTC)
	store.SetNow(func() time.Time { return now })
	d := sampleDecision("dec-1", now.Add(time.Minute))
	if _, err := store.CreateOrUpdateDecision(d); err != nil {
		t.Fatalf("CreateOrUpdateDecision: %v", err)
	}
	claim, claimed, err := store.ClaimDecision("dec-1", "cli", time.Minute)
	if err != nil {
		t.Fatalf("ClaimDecision: %v", err)
	}
	if claim.ClaimToken == "" || claimed.State != StateClaimed {
		t.Fatalf("bad claim response: %#v %#v", claim, claimed)
	}
	if _, _, err := store.ResolveDecision("dec-1", "wrong-token", StateApplied, "allow", "", nil); err == nil {
		t.Fatalf("expected wrong claim token failure")
	}
	now = now.Add(2 * time.Minute)
	if _, _, err := store.ResolveDecision("dec-1", claim.ClaimToken, StateApplied, "allow", "", nil); err == nil {
		t.Fatalf("expected timeout before provider apply")
	}
	got, err := store.Decision("dec-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateTimedOut {
		t.Fatalf("state=%s, want %s", got.State, StateTimedOut)
	}

	failRoot := filepath.Join(root, "readonly")
	if err := os.MkdirAll(failRoot, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(failRoot, 0o700) })
	failStore := NewStoreAt(failRoot)
	if _, err := failStore.CreateOrUpdateDecision(sampleDecision("dec-fail", now.Add(time.Minute))); err == nil {
		t.Fatalf("expected write failure")
	}
	if entries, _ := filepath.Glob(filepath.Join(failRoot, "**", "*.tmp-*")); len(entries) != 0 {
		t.Fatalf("left partial temp files: %v", entries)
	}
}

func TestStoreListFilterAndScale(t *testing.T) {
	root := t.TempDir()
	store := NewStoreAt(root)
	now := time.Date(2026, 7, 8, 2, 30, 0, 0, time.UTC)
	store.SetNow(func() time.Time { return now })
	for i := 0; i < 100; i++ {
		d := sampleDecision("dec-scale-"+itoa(i), now.Add(time.Hour))
		d.Source.Profile = "p"
		if _, err := store.CreateOrUpdateDecision(d); err != nil {
			t.Fatalf("decision %d: %v", i, err)
		}
		n := sampleNotice("not-scale-" + itoa(i))
		if _, err := store.CreateOrUpdateNotice(n); err != nil {
			t.Fatalf("notice %d: %v", i, err)
		}
	}
	decisions, err := store.Decisions(ListFilter{Kind: KindHostFSWrite, Profile: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 100 {
		t.Fatalf("decisions=%d, want 100", len(decisions))
	}
	notices, err := store.Notices(ListFilter{Kind: KindPrivilegeStatus})
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 100 {
		t.Fatalf("notices=%d, want 100", len(notices))
	}
	status, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingDecisions != 100 || status.UnackedNotices != 100 {
		t.Fatalf("status=%#v", status)
	}
}

func TestStoreClaimDecisionUsesFileLockAcrossStoreInstances(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 8, 4, 0, 0, 0, time.UTC)
	store := NewStoreAt(root)
	store.SetNow(func() time.Time { return now })
	if _, err := store.CreateOrUpdateDecision(sampleDecision("dec-race", now.Add(time.Hour))); err != nil {
		t.Fatalf("CreateOrUpdateDecision: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := NewStoreAt(root)
			s.SetNow(func() time.Time { return now })
			<-start
			_, _, err := s.ClaimDecision("dec-race", "webui", time.Minute)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var successes int
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful claims=%d, want exactly 1", successes)
	}
	got, err := store.Decision("dec-race")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateClaimed || got.Claim == nil {
		t.Fatalf("decision was not claimed once: %+v", got)
	}
}

func TestStoreHostFSReadProviderReopenAndActivationFailureAreNarrow(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 10, 4, 0, 0, 0, time.UTC)
	store := NewStoreAt(root)
	store.SetNow(func() time.Time { return now })
	readDecision := sampleDecision("dec_hfr_test", now.Add(time.Minute))
	readDecision.Kind = KindHostFSRead
	readDecision.DefaultOutcome = DefaultOutcomeDeny
	readDecision.AllowedActions = []string{ActionApprove, ActionDeny}
	readDecision.State = StateDenied
	if _, err := store.CreateOrUpdateDecision(readDecision); err != nil {
		t.Fatal(err)
	}
	result, reopened, err := store.ReopenProviderDecision(readDecision.ID, KindHostFSRead, 5*time.Minute, "reconsider")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatePending || result.Decision != ActionReopen || reopened.Revision != 2 || !reopened.TimeoutAt.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("reopen result=%+v decision=%+v", result, reopened)
	}
	if _, _, err := store.ReopenProviderDecision(readDecision.ID, KindHostFSRead, 5*time.Minute, "again"); err == nil {
		t.Fatal("pending decision should not reopen")
	}

	writeDecision := sampleDecision("dec_write", now.Add(time.Minute))
	writeDecision.State = StateDenied
	if _, err := store.CreateOrUpdateDecision(writeDecision); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReopenProviderDecision(writeDecision.ID, KindHostFSRead, 5*time.Minute, "wrong provider"); err == nil {
		t.Fatal("provider reopen must not weaken another decision kind")
	}

	applied := sampleDecision("dec_hfr_applied", now.Add(time.Minute))
	applied.Kind = KindHostFSRead
	applied.DefaultOutcome = DefaultOutcomeDeny
	applied.AllowedActions = []string{ActionApprove, ActionDeny}
	applied.State = StateApplied
	if _, err := store.CreateOrUpdateDecision(applied); err != nil {
		t.Fatal(err)
	}
	failed, err := store.FailAppliedProviderDecision(applied.ID, KindHostFSRead)
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != StateFailed || failed.Preview.Facts["activation"] != "failed" {
		t.Fatalf("failed activation=%+v", failed)
	}
	if _, err := store.FailAppliedProviderDecision(writeDecision.ID, KindHostFSRead); err == nil {
		t.Fatal("activation failure transition must remain provider- and state-bound")
	}
}

func TestNoticeAckDoesNotCreateProviderAuthority(t *testing.T) {
	store := NewStoreAt(t.TempDir())
	if _, err := store.CreateOrUpdateNotice(sampleNotice("not-1")); err != nil {
		t.Fatal(err)
	}
	ack, n, err := store.AckNotice("not-1", "webui")
	if err != nil {
		t.Fatalf("AckNotice: %v", err)
	}
	if !n.Acknowledged || ack.Surface != "webui" {
		t.Fatalf("ack=%#v notice=%#v", ack, n)
	}
	if _, _, err := store.ResolveDecision("not-1", "claim_x", StateApplied, "allow", "", nil); err == nil {
		t.Fatalf("notice should not resolve as decision")
	}
}

func sampleDecision(id string, timeout time.Time) Decision {
	return Decision{
		ID:             id,
		Kind:           KindHostFSWrite,
		State:          StatePending,
		Source:         Source{Profile: "p", Session: "s", Backend: "native"},
		Preview:        Preview{Summary: "write /workspace/file"},
		DefaultOutcome: DefaultOutcomeDiscard,
		TimeoutAt:      timeout,
		AuditRef:       "audit:" + id,
		AllowedActions: []string{ActionApply, ActionDiscard},
	}
}

func sampleNotice(id string) Notice {
	return Notice{
		ID:       id,
		Kind:     KindPrivilegeStatus,
		Severity: NoticeSeverityWarning,
		Status:   "degraded",
		Preview:  Preview{Summary: "target sudo degraded"},
		AuditRef: "audit:" + id,
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
