package decision

import (
	"os"
	"path/filepath"
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
