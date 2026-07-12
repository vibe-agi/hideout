package decision

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
)

const defaultLease = time.Minute

type Store struct {
	root string
	now  func() time.Time
	mu   sync.Mutex
}

func NewStore(storeRoot string) *Store {
	return &Store{root: filepath.Join(storeRoot, "operator-center"), now: time.Now}
}

func NewStoreAt(root string) *Store {
	return &Store{root: root, now: time.Now}
}

func (s *Store) SetNow(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	s.now = now
}

func (s *Store) CreateOrUpdateDecision(d Decision) (Decision, error) {
	unlock, err := s.lockFile()
	if err != nil {
		return Decision{}, err
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	if existing, err := s.decisionLocked(d.ID); err == nil {
		d.CreatedAt = existing.CreatedAt
		if d.Revision <= existing.Revision {
			d.Revision = existing.Revision + 1
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Decision{}, err
	}
	d.Normalize(now)
	d.Preview = RedactPreview(d.Preview)
	d.Risk = redactMap(d.Risk)
	d.ProposedAction = redactMap(d.ProposedAction)
	if err := ValidateDecision(d); err != nil {
		return Decision{}, err
	}
	if err := s.writeJSONAtomic(s.decisionPath(d.ID), d); err != nil {
		return Decision{}, err
	}
	return RedactDecision(d), nil
}

func (s *Store) Decision(id string) (Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.decisionLocked(id)
	if err != nil {
		return Decision{}, err
	}
	return RedactDecision(d), nil
}

func (s *Store) RawDecision(id string) (Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.decisionLocked(id)
}

func (s *Store) decisionLocked(id string) (Decision, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Decision{}, errors.New("decision id is required")
	}
	var d Decision
	if err := readJSON(s.decisionPath(id), &d); err != nil {
		return Decision{}, err
	}
	return d, nil
}

func (s *Store) Decisions(filter ListFilter) ([]Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.decisionsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Decision{}, nil
		}
		return nil, err
	}
	out := make([]Decision, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var d Decision
		if err := readJSON(filepath.Join(s.decisionsDir(), entry.Name()), &d); err != nil {
			return nil, err
		}
		if !decisionMatches(d, filter) {
			continue
		}
		out = append(out, RedactDecision(d))
	}
	slices.SortFunc(out, func(a, b Decision) int {
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *Store) ClaimDecision(id, surface string, lease time.Duration) (ClaimResponse, Decision, error) {
	unlock, err := s.lockFile()
	if err != nil {
		return ClaimResponse{}, Decision{}, err
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	if lease <= 0 {
		lease = defaultLease
	}
	d, err := s.decisionLocked(id)
	if err != nil {
		return ClaimResponse{}, Decision{}, err
	}
	if expiredDecision(d, now) {
		if err := s.timeoutDecisionLocked(&d, now); err != nil {
			return ClaimResponse{}, Decision{}, err
		}
		return ClaimResponse{}, RedactDecision(d), fmt.Errorf("decision %s timed out", d.ID)
	}
	if d.State != StatePending {
		if d.State == StateClaimed && d.Claim != nil && now.After(d.Claim.ExpiresAt) {
			// Stale claim can be replaced while the decision is otherwise unresolved.
		} else {
			return ClaimResponse{}, RedactDecision(d), fmt.Errorf("decision %s is %s", d.ID, d.State)
		}
	}
	token, err := newClaimToken()
	if err != nil {
		return ClaimResponse{}, Decision{}, err
	}
	if strings.TrimSpace(surface) == "" {
		surface = "manager-client"
	}
	d.State = StateClaimed
	d.Claim = &Claim{
		Surface:   surface,
		ClaimedAt: now,
		ExpiresAt: now.Add(lease),
		TokenHash: HashClaimToken(token),
	}
	d.Revision++
	d.UpdatedAt = now
	if err := s.writeJSONAtomic(s.decisionPath(d.ID), d); err != nil {
		return ClaimResponse{}, Decision{}, err
	}
	return ClaimResponse{
		Version:        DecisionClaimVersion,
		DecisionID:     d.ID,
		State:          d.State,
		ClaimToken:     token,
		ClaimExpiresAt: d.Claim.ExpiresAt,
		Revision:       d.Revision,
	}, RedactDecision(d), nil
}

func (s *Store) ResolveDecision(id, token, state, decision, reason string, providerResult map[string]any) (Resolution, Decision, error) {
	unlock, err := s.lockFile()
	if err != nil {
		return Resolution{}, Decision{}, err
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	d, err := s.decisionLocked(id)
	if err != nil {
		return Resolution{}, Decision{}, err
	}
	if expiredDecision(d, now) {
		if err := s.timeoutDecisionLocked(&d, now); err != nil {
			return Resolution{}, Decision{}, err
		}
		return Resolution{}, RedactDecision(d), fmt.Errorf("decision %s timed out", d.ID)
	}
	if err := validateClaim(d, token, now); err != nil {
		return Resolution{}, RedactDecision(d), err
	}
	if !DecisionTerminal(state) {
		return Resolution{}, RedactDecision(d), fmt.Errorf("resolution state %q is not terminal", state)
	}
	d.State = state
	d.Claim = nil
	d.Revision++
	d.UpdatedAt = now
	if err := s.writeJSONAtomic(s.decisionPath(d.ID), d); err != nil {
		return Resolution{}, Decision{}, err
	}
	res := Resolution{
		Version:        DecisionResultVersion,
		DecisionID:     d.ID,
		Kind:           d.Kind,
		Status:         state,
		Decision:       decision,
		Reason:         reason,
		ProviderResult: providerResult,
		AuditRef:       d.AuditRef,
		Revision:       d.Revision,
	}
	return RedactResolution(res), RedactDecision(d), nil
}

// RevokeGrantedDecision revokes an active terminal grant. It is deliberately
// separate from ResolveDecision: revocation does not reuse a stale claim token
// and is restricted to the expected provider kind and approved state.
func (s *Store) RevokeGrantedDecision(id, expectedKind, reason string) (Resolution, Decision, error) {
	unlock, err := s.lockFile()
	if err != nil {
		return Resolution{}, Decision{}, err
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.decisionLocked(id)
	if err != nil {
		return Resolution{}, Decision{}, err
	}
	if d.Kind != expectedKind || d.State != StateApproved {
		return Resolution{}, RedactDecision(d), fmt.Errorf("decision %s is not an approved %s grant", d.ID, expectedKind)
	}
	d.State = StateDenied
	d.Claim = nil
	d.Revision++
	d.UpdatedAt = s.now().UTC()
	if err := s.writeJSONAtomic(s.decisionPath(d.ID), d); err != nil {
		return Resolution{}, Decision{}, err
	}
	res := Resolution{
		Version:    DecisionResultVersion,
		DecisionID: d.ID,
		Kind:       d.Kind,
		Status:     d.State,
		Decision:   ActionRevoke,
		Reason:     reason,
		AuditRef:   d.AuditRef,
		Revision:   d.Revision,
	}
	return RedactResolution(res), RedactDecision(d), nil
}

// InvalidateProviderDecision removes authority when its owning lifecycle can
// no longer be proven. It can invalidate pending, claimed, or approved records;
// already non-authoritative terminal outcomes are left unchanged.
func (s *Store) InvalidateProviderDecision(id, expectedKind, reason string) (Resolution, Decision, error) {
	unlock, err := s.lockFile()
	if err != nil {
		return Resolution{}, Decision{}, err
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.decisionLocked(id)
	if err != nil {
		return Resolution{}, Decision{}, err
	}
	if d.Kind != expectedKind {
		return Resolution{}, RedactDecision(d), fmt.Errorf("decision %s kind %q cannot be invalidated by %q provider", d.ID, d.Kind, expectedKind)
	}
	if d.State != StatePending && d.State != StateClaimed && d.State != StateApproved {
		return RedactResolution(Resolution{Version: DecisionResultVersion, DecisionID: d.ID, Kind: d.Kind, Status: d.State, Decision: "unchanged", Reason: reason, AuditRef: d.AuditRef, Revision: d.Revision}), RedactDecision(d), nil
	}
	d.State = StateStale
	d.Claim = nil
	d.Revision++
	d.UpdatedAt = s.now().UTC()
	if err := s.writeJSONAtomic(s.decisionPath(d.ID), d); err != nil {
		return Resolution{}, Decision{}, err
	}
	res := Resolution{Version: DecisionResultVersion, DecisionID: d.ID, Kind: d.Kind, Status: d.State, Decision: "invalidate", Reason: reason, AuditRef: d.AuditRef, Revision: d.Revision}
	return RedactResolution(res), RedactDecision(d), nil
}

func (s *Store) ReopenProviderDecision(id, expectedKind string, timeout time.Duration, reason string) (Resolution, Decision, error) {
	unlock, err := s.lockFile()
	if err != nil {
		return Resolution{}, Decision{}, err
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if timeout <= 0 {
		return Resolution{}, Decision{}, errors.New("provider reopen timeout must be positive")
	}
	d, err := s.decisionLocked(id)
	if err != nil {
		return Resolution{}, Decision{}, err
	}
	if d.Kind != expectedKind {
		return Resolution{}, RedactDecision(d), fmt.Errorf("decision %s kind %q cannot be reopened by %q provider", d.ID, d.Kind, expectedKind)
	}
	if d.State != StateDenied && d.State != StateTimedOut {
		return Resolution{}, RedactDecision(d), fmt.Errorf("decision %s state %q is not reopenable", d.ID, d.State)
	}
	now := s.now().UTC()
	d.State = StatePending
	d.Claim = nil
	d.TimeoutAt = now.Add(timeout)
	d.Revision++
	d.UpdatedAt = now
	if err := ValidateDecision(d); err != nil {
		return Resolution{}, RedactDecision(d), err
	}
	if err := s.writeJSONAtomic(s.decisionPath(d.ID), d); err != nil {
		return Resolution{}, Decision{}, err
	}
	res := Resolution{
		Version:    DecisionResultVersion,
		DecisionID: d.ID,
		Kind:       d.Kind,
		Status:     StatePending,
		Decision:   ActionReopen,
		Reason:     reason,
		AuditRef:   d.AuditRef,
		Revision:   d.Revision,
	}
	return RedactResolution(res), RedactDecision(d), nil
}

func (s *Store) FailAppliedProviderDecision(id, expectedKind string) (Decision, error) {
	unlock, err := s.lockFile()
	if err != nil {
		return Decision{}, err
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.decisionLocked(id)
	if err != nil {
		return Decision{}, err
	}
	if d.Kind != expectedKind || d.State != StateApplied {
		return RedactDecision(d), fmt.Errorf("decision %s is not an applied %s provider decision", d.ID, expectedKind)
	}
	d.State = StateFailed
	d.Claim = nil
	d.Revision++
	d.UpdatedAt = s.now().UTC()
	if d.Preview.Facts == nil {
		d.Preview.Facts = map[string]any{}
	}
	d.Preview.Facts["activation"] = "failed"
	if err := ValidateDecision(d); err != nil {
		return RedactDecision(d), err
	}
	if err := s.writeJSONAtomic(s.decisionPath(d.ID), d); err != nil {
		return Decision{}, err
	}
	return RedactDecision(d), nil
}

func (s *Store) ValidateDecisionClaim(id, token string) (Decision, error) {
	unlock, err := s.lockFile()
	if err != nil {
		return Decision{}, err
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	d, err := s.decisionLocked(id)
	if err != nil {
		return Decision{}, err
	}
	if expiredDecision(d, now) {
		if err := s.timeoutDecisionLocked(&d, now); err != nil {
			return Decision{}, err
		}
		return RedactDecision(d), fmt.Errorf("decision %s timed out", d.ID)
	}
	if err := validateClaim(d, token, now); err != nil {
		return RedactDecision(d), err
	}
	return RedactDecision(d), nil
}

func (s *Store) TimeoutExpired(now time.Time) (int, error) {
	timedOut, err := s.TimeoutExpiredDecisions(now)
	if err != nil {
		return 0, err
	}
	return len(timedOut), nil
}

func (s *Store) TimeoutExpiredDecisions(now time.Time) ([]Decision, error) {
	unlock, err := s.lockFile()
	if err != nil {
		return nil, err
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.IsZero() {
		now = s.now().UTC()
	}
	entries, err := os.ReadDir(s.decisionsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Decision{}, nil
		}
		return nil, err
	}
	var out []Decision
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var d Decision
		path := filepath.Join(s.decisionsDir(), entry.Name())
		if err := readJSON(path, &d); err != nil {
			return out, err
		}
		if !expiredDecision(d, now) {
			continue
		}
		if err := s.timeoutDecisionLocked(&d, now); err != nil {
			return out, err
		}
		out = append(out, RedactDecision(d))
	}
	return out, nil
}

func (s *Store) timeoutDecisionLocked(d *Decision, now time.Time) error {
	d.State = StateTimedOut
	d.Claim = nil
	d.Revision++
	d.UpdatedAt = now
	return s.writeJSONAtomic(s.decisionPath(d.ID), *d)
}

func (s *Store) CreateOrUpdateNotice(n Notice) (Notice, error) {
	unlock, err := s.lockFile()
	if err != nil {
		return Notice{}, err
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	if existing, err := s.noticeLocked(n.ID); err == nil {
		n.CreatedAt = existing.CreatedAt
		if n.Revision <= existing.Revision {
			n.Revision = existing.Revision + 1
		}
		if !n.Acknowledged && existing.Acknowledged {
			n.Acknowledged = true
			n.AcknowledgedBy = existing.AcknowledgedBy
			n.AcknowledgedAt = existing.AcknowledgedAt
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Notice{}, err
	}
	n.Normalize(now)
	n.Preview = RedactPreview(n.Preview)
	n.Payload = redactMap(n.Payload)
	if err := ValidateNotice(n); err != nil {
		return Notice{}, err
	}
	if err := s.writeJSONAtomic(s.noticePath(n.ID), n); err != nil {
		return Notice{}, err
	}
	return RedactNotice(n), nil
}

func (s *Store) Notice(id string) (Notice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.noticeLocked(id)
	if err != nil {
		return Notice{}, err
	}
	return RedactNotice(n), nil
}

func (s *Store) noticeLocked(id string) (Notice, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Notice{}, errors.New("notice id is required")
	}
	var n Notice
	if err := readJSON(s.noticePath(id), &n); err != nil {
		return Notice{}, err
	}
	return n, nil
}

func (s *Store) Notices(filter ListFilter) ([]Notice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.noticesDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Notice{}, nil
		}
		return nil, err
	}
	out := make([]Notice, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var n Notice
		if err := readJSON(filepath.Join(s.noticesDir(), entry.Name()), &n); err != nil {
			return nil, err
		}
		if !noticeMatches(n, filter) {
			continue
		}
		out = append(out, RedactNotice(n))
	}
	slices.SortFunc(out, func(a, b Notice) int {
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *Store) AckNotice(id, surface string) (Acknowledgement, Notice, error) {
	unlock, err := s.lockFile()
	if err != nil {
		return Acknowledgement{}, Notice{}, err
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	n, err := s.noticeLocked(id)
	if err != nil {
		return Acknowledgement{}, Notice{}, err
	}
	if strings.TrimSpace(surface) == "" {
		surface = "manager-client"
	}
	n.Acknowledged = true
	n.AcknowledgedBy = surface
	n.AcknowledgedAt = now
	n.Revision++
	n.UpdatedAt = now
	if err := s.writeJSONAtomic(s.noticePath(n.ID), n); err != nil {
		return Acknowledgement{}, Notice{}, err
	}
	ack := Acknowledgement{
		NoticeID:       n.ID,
		Surface:        surface,
		AcknowledgedAt: now,
		AuditRef:       n.AuditRef,
		Revision:       n.Revision,
	}
	return ack, RedactNotice(n), nil
}

func (s *Store) Status() (Status, error) {
	now := s.now().UTC()
	decisions, err := s.Decisions(ListFilter{IncludeTerminal: true})
	if err != nil {
		return Status{}, err
	}
	notices, err := s.Notices(ListFilter{})
	if err != nil {
		return Status{}, err
	}
	status := Status{Version: StatusVersion, GeneratedAt: now}
	var oldest time.Time
	for _, d := range decisions {
		if DecisionTerminal(d.State) {
			status.TerminalDecisions++
		} else if d.State == StateClaimed {
			status.ClaimedDecisions++
		} else {
			status.PendingDecisions++
		}
		if !DecisionTerminal(d.State) {
			if oldest.IsZero() || d.CreatedAt.Before(oldest) {
				oldest = d.CreatedAt
			}
			if !d.TimeoutAt.IsZero() && d.TimeoutAt.Sub(now) <= time.Minute {
				status.TimeoutRisk++
			}
			status.DecisionIDs = append(status.DecisionIDs, d.ID)
		}
	}
	if !oldest.IsZero() {
		status.OldestPendingAge = now.Sub(oldest).Round(time.Second).String()
	}
	for _, n := range notices {
		if !n.Acknowledged {
			status.UnackedNotices++
			status.NoticeIDs = append(status.NoticeIDs, n.ID)
		}
	}
	return status, nil
}

func (s *Store) decisionsDir() string {
	return filepath.Join(s.root, "decisions")
}

func (s *Store) noticesDir() string {
	return filepath.Join(s.root, "notices")
}

func (s *Store) decisionPath(id string) string {
	return filepath.Join(s.decisionsDir(), id+".json")
}

func (s *Store) noticePath(id string) string {
	return filepath.Join(s.noticesDir(), id+".json")
}

func (s *Store) lockFile() (func() error, error) {
	if s == nil {
		return nil, errors.New("decision store is nil")
	}
	if s.root == "" {
		return nil, errors.New("decision store root is required")
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(s.root, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() error {
		unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		closeErr := file.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}

func decisionMatches(d Decision, f ListFilter) bool {
	if f.Kind != "" && d.Kind != f.Kind {
		return false
	}
	if f.State != "" && d.State != f.State {
		return false
	}
	if f.Profile != "" && d.Source.Profile != f.Profile {
		return false
	}
	if f.Session != "" && d.Source.Session != f.Session {
		return false
	}
	if !f.IncludeTerminal && DecisionTerminal(d.State) {
		return false
	}
	return true
}

func noticeMatches(n Notice, f ListFilter) bool {
	if f.Kind != "" && n.Kind != f.Kind {
		return false
	}
	if f.Profile != "" && n.Source.Profile != f.Profile {
		return false
	}
	if f.Session != "" && n.Source.Session != f.Session {
		return false
	}
	if f.Severity != "" && n.Severity != f.Severity {
		return false
	}
	return true
}

func expiredDecision(d Decision, now time.Time) bool {
	return !DecisionTerminal(d.State) && !d.TimeoutAt.IsZero() && !now.Before(d.TimeoutAt)
}

func validateClaim(d Decision, token string, now time.Time) error {
	if d.State != StateClaimed || d.Claim == nil {
		return errors.New("decision is not claimed")
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("claimToken is required")
	}
	if now.After(d.Claim.ExpiresAt) {
		return errors.New("claimToken expired")
	}
	if HashClaimToken(token) != d.Claim.TokenHash {
		return errors.New("claimToken is invalid")
	}
	return nil
}

func redactMap(in map[string]any) map[string]any {
	return audit.RedactDetails(in)
}

func newClaimToken() (string, error) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "claim_" + hex.EncodeToString(raw[:]), nil
}

func HashClaimToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Store) writeJSONAtomic(path string, value any) error {
	if s == nil {
		return errors.New("decision store is nil")
	}
	if s.root == "" {
		return errors.New("decision store root is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
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
	keepTemp = false
	return nil
}

func readJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
