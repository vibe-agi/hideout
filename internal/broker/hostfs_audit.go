package broker

import (
	"sort"
	"sync"

	"github.com/vibe-agi/hideout/internal/audit"
)

const hostFSDiscoveryAggregateAction = "host.fs.discovery.aggregate"

type hostFSDiscoveryAuditKey struct {
	root    string
	op      string
	outcome string
}

type hostFSDiscoveryAudit struct {
	mu     sync.Mutex
	counts map[hostFSDiscoveryAuditKey]int64
}

func (a *hostFSDiscoveryAudit) add(key hostFSDiscoveryAuditKey) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.counts == nil {
		a.counts = map[hostFSDiscoveryAuditKey]int64{}
	}
	a.counts[key]++
}

func (a *hostFSDiscoveryAudit) drain() []struct {
	key   hostFSDiscoveryAuditKey
	count int64
} {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rows := make([]struct {
		key   hostFSDiscoveryAuditKey
		count int64
	}, 0, len(a.counts))
	for key, count := range a.counts {
		rows = append(rows, struct {
			key   hostFSDiscoveryAuditKey
			count int64
		}{key: key, count: count})
	}
	a.counts = nil
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].key.root != rows[j].key.root {
			return rows[i].key.root < rows[j].key.root
		}
		if rows[i].key.op != rows[j].key.op {
			return rows[i].key.op < rows[j].key.op
		}
		return rows[i].key.outcome < rows[j].key.outcome
	})
	return rows
}

func (s *Server) recordHostFSDiscoveryAudit(req Request, resp Response, policyAudit hostFSPolicyAudit) bool {
	if s == nil || s.Audit == nil || (req.Action != "host.fs.stat" && req.Action != "host.fs.list") || !policyAudit.Visibility.ExplicitDomain {
		return false
	}
	root := policyAudit.Visibility.Root
	if root == "" {
		root = policyAudit.Visibility.DiscoverDecision.RuleID
	}
	outcome := "ok"
	if resp.Error != nil {
		outcome = resp.Error.Code
	} else if resp.Status != "ok" {
		outcome = resp.Status
	}
	s.hostFSAuditMu.Lock()
	if s.hostFSAudit == nil {
		s.hostFSAudit = &hostFSDiscoveryAudit{}
	}
	aggregate := s.hostFSAudit
	s.hostFSAuditMu.Unlock()
	aggregate.add(hostFSDiscoveryAuditKey{root: root, op: req.Action, outcome: outcome})
	return true
}

func (s *Server) flushHostFSDiscoveryAudit() error {
	if s == nil || s.Audit == nil {
		return nil
	}
	s.hostFSAuditMu.Lock()
	aggregate := s.hostFSAudit
	s.hostFSAuditMu.Unlock()
	for _, row := range aggregate.drain() {
		if err := s.Audit.Emit(audit.Event{
			Session:  s.SessionID,
			Profile:  s.Profile,
			Backend:  s.Backend,
			Action:   hostFSDiscoveryAggregateAction,
			Decision: "audit-only",
			Details: map[string]any{
				"root":    row.key.root,
				"op":      row.key.op,
				"outcome": row.key.outcome,
				"count":   row.count,
			},
		}); err != nil {
			return err
		}
	}
	return nil
}
