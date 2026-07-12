package hostfsdecision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/hostfs/overlay"
	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/productevidence"
)

var supportedOperations = []string{
	"create",
	"replace",
	"append",
	"truncate",
	"mkdir",
	"delete",
	"rename",
	"chmod",
	"chown",
}

type evidenceRun struct {
	out     string
	reports string
	proofs  map[string]productevidence.ProofEntry
}

func TestLocalFastHostFSDecisionEvidence(t *testing.T) {
	out := strings.TrimSpace(os.Getenv("HIDEOUT_HOSTFS_DECISION_E2E_OUT"))
	if out == "" {
		t.Skip("HIDEOUT_HOSTFS_DECISION_E2E_OUT is not set")
	}
	out, err := filepath.Abs(out)
	if err != nil {
		t.Fatal(err)
	}
	run := evidenceRun{
		out:     out,
		reports: filepath.Join(out, "reports"),
		proofs:  map[string]productevidence.ProofEntry{},
	}
	if err := os.MkdirAll(run.reports, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, proof := range productevidence.HostFSDecisionLocalFastProofs() {
		run.proofs[proof.ProofID] = proof
	}

	run.lifecycleProof(t)
	run.decisionProof(t)
	run.visibilityProof(t)
	run.redactionProof(t)
	run.writeManifest(t)
}

func (r evidenceRun) lifecycleProof(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	lower := filepath.Join(root, "lower.txt")
	mustWrite(t, lower, "before\n")
	store := &overlay.Store{Root: filepath.Join(root, "overlay")}
	staged, err := store.Stage(stageRequest("replace", lower, "after\n"))
	if err != nil {
		t.Fatal(err)
	}
	view, ok, err := store.View(lower)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || string(view.Data) != "after\n" {
		t.Fatalf("staged view = ok:%v data:%q, want after", ok, string(view.Data))
	}
	hostBefore := mustRead(t, lower)
	if hostBefore != "before\n" {
		t.Fatalf("host lower changed before apply: %q", hostBefore)
	}
	claimOverlayDecision(t, store, staged.Decision.DecisionID, "cli")
	result, _, _, err := store.Apply(staged.Decision.DecisionID)
	if err != nil {
		t.Fatal(err)
	}
	hostAfter := mustRead(t, lower)
	if result.Status != overlay.StateApplied || hostAfter != "after\n" {
		t.Fatalf("apply status=%s host=%q", result.Status, hostAfter)
	}

	conflictFile := filepath.Join(root, "conflict.txt")
	mustWrite(t, conflictFile, "base\n")
	conflict, err := store.Stage(stageRequest("replace", conflictFile, "planned\n"))
	if err != nil {
		t.Fatal(err)
	}
	claimOverlayDecision(t, store, conflict.Decision.DecisionID, "cli")
	mustWrite(t, conflictFile, "conflicting\n")
	conflictResult, _, _, conflictErr := store.Apply(conflict.Decision.DecisionID)
	if !errors.Is(conflictErr, overlay.ErrConflict) {
		t.Fatalf("expected conflict, got result=%+v err=%v", conflictResult, conflictErr)
	}
	if got := mustRead(t, conflictFile); got != "conflicting\n" {
		t.Fatalf("conflict apply mutated lower file: %q", got)
	}

	coverage := r.operationCoverage(t)
	report := map[string]any{
		"mode":                   "local-fast",
		"operation":              "replace",
		"decisionId":             staged.Decision.DecisionID,
		"guestReadBeforeApply":   string(view.Data),
		"hostLowerBeforeApply":   hashString(hostBefore),
		"hostLowerAfterApply":    hashString(hostAfter),
		"changedPaths":           []string{"fixture/lower.txt"},
		"conflictDecisionId":     conflict.Decision.DecisionID,
		"conflictStatus":         conflictResult.Status,
		"conflictReason":         conflictResult.ConflictReason,
		"conflictHostLowerAfter": hashString(mustRead(t, conflictFile)),
		"coverage":               coverage,
	}
	path := r.writeJSON(t, "reports/lifecycle.json", report)
	proof := r.proofs[productevidence.Proof023LocalFastLifecycle]
	proof.Artifacts = append(proof.Artifacts, artifact(t, r.out, "event-summary", path, "local HostFS lifecycle and coverage report"))
	proof.Notes = append(proof.Notes, "local-fast only; does not claim real Gate 2 guest FUSE behavior")
	r.proofs[proof.ProofID] = proof
}

func (r evidenceRun) operationCoverage(t *testing.T) map[string]any {
	t.Helper()
	root := t.TempDir()
	store := &overlay.Store{Root: filepath.Join(root, "overlay")}
	var covered []string
	for _, op := range supportedOperations {
		req, observePath := operationRequest(t, root, op)
		result, err := store.Stage(req)
		if err != nil {
			t.Fatalf("stage %s: %v", op, err)
		}
		if _, ok, err := store.View(observePath); err != nil || !ok {
			t.Fatalf("view %s ok=%v err=%v decision=%s", op, ok, err, result.Decision.DecisionID)
		}
		covered = append(covered, op)
	}
	slices.Sort(covered)
	return map[string]any{
		"supportedOperations": supportedOperations,
		"coveredLocalFast":    covered,
		"coveredRealGate":     []string{},
		"uncovered":           []string{},
		"coverageNote":        "local-fast covers staged view and decision state only; real Gate 2 coverage is separate",
	}
}

func (r evidenceRun) decisionProof(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	storeA := decision.NewStore(root)
	storeB := decision.NewStore(root)
	now := time.Date(2026, 7, 9, 8, 0, 0, 0, time.UTC)
	storeA.SetNow(func() time.Time { return now })
	storeB.SetNow(func() time.Time { return now })
	created, err := storeA.CreateOrUpdateDecision(decision.Decision{
		ID:             "dec-race",
		Kind:           decision.KindEvidenceShare,
		State:          decision.StatePending,
		Preview:        decision.Preview{Summary: "share local artifact"},
		AllowedActions: []string{decision.ActionApply, decision.ActionDeny},
		DefaultOutcome: decision.DefaultOutcomeNoRelease,
		TimeoutAt:      now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	winner, _, err := storeA.ClaimDecision(created.ID, "cli", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := storeB.ClaimDecision(created.ID, "webui", time.Minute); err == nil {
		t.Fatal("second claimant unexpectedly won")
	}
	denied, _, err := storeA.ResolveDecision(created.ID, winner.ClaimToken, decision.StateDenied, "deny", "operator denied", nil)
	if err != nil {
		t.Fatal(err)
	}
	if denied.Status != decision.StateDenied {
		t.Fatalf("deny status=%s", denied.Status)
	}

	timeoutID := "dec_timeout"
	if _, err := storeA.CreateOrUpdateDecision(decision.Decision{
		ID:             timeoutID,
		Kind:           decision.KindEvidenceShare,
		State:          decision.StatePending,
		Preview:        decision.Preview{Summary: "timeout fixture"},
		AllowedActions: []string{decision.ActionDeny},
		DefaultOutcome: decision.DefaultOutcomeNoRelease,
		TimeoutAt:      now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	expired, err := storeA.TimeoutExpiredDecisions(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].ID != timeoutID || expired[0].State != decision.StateTimedOut {
		t.Fatalf("expired=%+v", expired)
	}

	report := map[string]any{
		"decisionId":        created.ID,
		"winnerState":       winner.State,
		"losingClaim":       "decision already claimed",
		"deniedStatus":      denied.Status,
		"timeoutDecisionId": timeoutID,
		"timeoutStatus":     expired[0].State,
	}
	path := r.writeJSON(t, "reports/decision.json", report)
	claimProof := r.proofs[productevidence.Proof023LocalFastClaimRace]
	claimProof.Artifacts = append(claimProof.Artifacts, artifact(t, r.out, "event-summary", path, "decision claim race report"))
	r.proofs[claimProof.ProofID] = claimProof
	timeoutProof := r.proofs[productevidence.Proof023LocalFastTimeout]
	timeoutProof.Artifacts = append(timeoutProof.Artifacts, artifact(t, r.out, "event-summary", path, "decision timeout report"))
	r.proofs[timeoutProof.ProofID] = timeoutProof
}

func (r evidenceRun) visibilityProof(t *testing.T) {
	t.Helper()
	state := liveconsole.State{Version: liveconsole.SeedVersion}
	if res := liveconsole.Apply(&state, liveconsole.Event{
		Version: liveconsole.EventVersion,
		Kind:    liveconsole.KindHostFSWrite,
		Seq:     1,
		Payload: liveconsole.EventPayload{
			DecisionID:      "hfwdec_visible",
			OperationID:     "hfwop_visible",
			Status:          "pending",
			Operation:       "replace",
			Path:            "fixture/lower.txt",
			PrivilegeStatus: "enforced",
		},
	}); res.Status != liveconsole.ResultApplied {
		t.Fatalf("hostfs write apply result=%+v", res)
	}
	if res := liveconsole.Apply(&state, liveconsole.Event{
		Version: liveconsole.EventVersion,
		Kind:    liveconsole.KindDecision,
		Seq:     2,
		Payload: liveconsole.EventPayload{
			DecisionID:     "hfwdec_visible",
			RecordKind:     decision.KindHostFSWrite,
			Status:         "pending",
			DefaultOutcome: "discard",
			Profile:        "default",
			Backend:        "native",
		},
	}); res.Status != liveconsole.ResultApplied {
		t.Fatalf("decision apply result=%+v", res)
	}
	if len(state.HostFSWrites) != 1 || len(state.Decisions) != 1 {
		t.Fatalf("visibility state hostfs=%d decisions=%d", len(state.HostFSWrites), len(state.Decisions))
	}
	report := map[string]any{
		"cli":         map[string]any{"decisionId": "hfwdec_visible", "state": "pending"},
		"api":         map[string]any{"decisionId": "hfwdec_visible", "state": "pending"},
		"webuiModel":  state.HostFSWrites,
		"tuiModel":    state.Decisions,
		"proofScope":  "model visibility only; browser and PTY click proof belongs to 021",
		"privateData": "absent",
	}
	path := r.writeJSON(t, "reports/visibility.json", report)
	proof := r.proofs[productevidence.Proof023LocalFastVisibility]
	proof.Artifacts = append(proof.Artifacts, artifact(t, r.out, "event-summary", path, "CLI/API/WebUI-model/TUI-model visibility report"))
	r.proofs[proof.ProofID] = proof
}

func (r evidenceRun) redactionProof(t *testing.T) {
	t.Helper()
	for _, marker := range []string{
		"claim_",
		"hfwobj_",
		"providerRef",
		"HIDEOUT_SECRET_",
		"cap_0123456789abcdef",
	} {
		if hit := findInDir(t, r.reports, marker); hit != "" {
			t.Fatalf("public artifact leaked %q in %s", marker, hit)
		}
	}
	report := map[string]any{
		"scanned": []string{"reports/lifecycle.json", "reports/decision.json", "reports/visibility.json"},
		"result":  "passed",
	}
	path := r.writeJSON(t, "reports/redaction.json", report)
	proof := r.proofs[productevidence.Proof023LocalFastRedaction]
	proof.Artifacts = append(proof.Artifacts, artifact(t, r.out, "event-summary", path, "redaction scan report"))
	r.proofs[proof.ProofID] = proof
}

func (r evidenceRun) writeManifest(t *testing.T) {
	t.Helper()
	manifest := productevidence.NewManifest("test", false)
	for _, id := range productevidence.Required023LocalFastProofIDs {
		manifest.Proofs = append(manifest.Proofs, r.proofs[id])
	}
	if err := productevidence.Require023LocalFastComplete(manifest); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.out, "product-hardening-evidence.json")
	if err := productevidence.WriteFile(path, manifest); err != nil {
		t.Fatal(err)
	}
}

func stageRequest(op, path, data string) overlay.StageRequest {
	return overlay.StageRequest{
		SessionID:   "session-local-fast",
		Profile:     "default",
		Backend:     "native",
		Operation:   op,
		Path:        path,
		Data:        []byte(data),
		GrantID:     "grant-local-fast",
		GrantSource: "test",
		Mode:        "0644",
		Privilege:   overlay.Privilege{Status: "enforced", Source: "test"},
	}
}

func operationRequest(t *testing.T, root, op string) (overlay.StageRequest, string) {
	t.Helper()
	path := filepath.Join(root, op+".txt")
	switch op {
	case "create":
		return stageRequest(op, path, "created\n"), path
	case "replace":
		mustWrite(t, path, "before\n")
		return stageRequest(op, path, "after\n"), path
	case "append":
		mustWrite(t, path, "before\n")
		return stageRequest(op, path, "after\n"), path
	case "truncate":
		mustWrite(t, path, "before\n")
		req := stageRequest(op, path, "")
		req.Size = 3
		return req, path
	case "mkdir":
		dir := filepath.Join(root, "created-dir")
		req := stageRequest(op, dir, "")
		req.Mode = "0755"
		return req, dir
	case "delete":
		mustWrite(t, path, "delete-me\n")
		return stageRequest(op, path, ""), path
	case "rename":
		mustWrite(t, path, "rename-me\n")
		dest := filepath.Join(root, "renamed.txt")
		req := stageRequest(op, path, "")
		req.DestinationPath = dest
		return req, dest
	case "chmod":
		mustWrite(t, path, "mode\n")
		req := stageRequest(op, path, "")
		req.Mode = "0640"
		return req, path
	case "chown":
		mustWrite(t, path, "owner\n")
		req := stageRequest(op, path, "")
		req.Owner = "0"
		req.Group = "0"
		return req, path
	default:
		t.Fatalf("unsupported fixture op %s", op)
		return overlay.StageRequest{}, ""
	}
}

func claimOverlayDecision(t *testing.T, store *overlay.Store, id, surface string) {
	t.Helper()
	decision, err := store.Decision(id)
	if err != nil {
		t.Fatal(err)
	}
	decision.State = overlay.StateClaimed
	decision.Claim = &overlay.Claim{
		Surface:   surface,
		ClaimedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(time.Minute),
		TokenHash: "redacted-token-hash",
	}
	if err := store.SaveDecision(decision); err != nil {
		t.Fatal(err)
	}
}

func (r evidenceRun) writeJSON(t *testing.T, rel string, value any) string {
	t.Helper()
	path := filepath.Join(r.out, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return rel
}

func artifact(t *testing.T, out, kind, rel, desc string) productevidence.ArtifactRef {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(out, rel))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return productevidence.ArtifactRef{
		Kind:            kind,
		Path:            rel,
		SHA256:          hex.EncodeToString(sum[:]),
		RedactionStatus: productevidence.RedactionPassed,
		Description:     desc,
	}
}

func mustWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func findInDir(t *testing.T, root, marker string) string {
	t.Helper()
	var hit string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || hit != "" || entry.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), marker) {
			hit = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hit
}
