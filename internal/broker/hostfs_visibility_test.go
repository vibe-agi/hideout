package broker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/audit"
	hostfspkg "github.com/vibe-agi/hideout/internal/hostfs"
)

func TestBrokerHostFSDiscoveryOmitsFullMetadataAndUsesTypedDenial(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "locked.txt")
	if err := os.WriteFile(path, []byte("not-in-discovery-response"), 0o640); err != nil {
		t.Fatal(err)
	}
	service := testHostFSService(t, hostfspkg.Config{Grants: []hostfspkg.Rule{{
		ID: "hfs_see", HostPath: root, Ops: []hostfspkg.Op{hostfspkg.OpDiscover}, Scope: hostfspkg.ScopeDir, Reason: "navigate",
	}}})
	server := Server{SessionID: "ses_1", Token: "cap_good", HostFS: service}

	stat := server.Handle(context.Background(), hostFSRequest("req_stat_discover", "host.fs.stat", path, nil))
	if stat.Status != "ok" || stat.Data["kind"] != "file" || stat.Data["locked"] != true {
		t.Fatalf("unexpected discover stat: %+v", stat)
	}
	for _, forbidden := range []string{"size", "mode", "modTime"} {
		if _, exists := stat.Data[forbidden]; exists {
			t.Fatalf("discover stat exposed %s: %+v", forbidden, stat.Data)
		}
	}

	list := server.Handle(context.Background(), hostFSRequest("req_list_discover", "host.fs.list", root, nil))
	entries, ok := list.Data["entries"].([]map[string]any)
	if list.Status != "ok" || !ok || len(entries) != 1 || entries[0]["locked"] != true {
		t.Fatalf("unexpected discover list: %+v", list)
	}
	for _, forbidden := range []string{"size", "mode"} {
		if _, exists := entries[0][forbidden]; exists {
			t.Fatalf("discover list exposed %s: %+v", forbidden, entries[0])
		}
	}

	read := server.Handle(context.Background(), hostFSRequest("req_read_discover", "host.fs.read", path, nil))
	if read.Status != "denied" || read.Error == nil || read.Error.Code != HostFSErrorReadDenied || read.Error.Errno != ErrnoEACCES || read.Error.DecisionRef != "" {
		t.Fatalf("locked read did not return honest typed denial without a provider: %+v", read)
	}
}

func TestBrokerHostFSReadProposalPublishesReferencesOnlyForRealDecisions(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "locked.txt")
	if err := os.WriteFile(path, []byte("locked"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := testHostFSService(t, hostfspkg.Config{Grants: []hostfspkg.Rule{{
		ID: "hfs_see", HostPath: root, Ops: []hostfspkg.Op{hostfspkg.OpDiscover}, Scope: hostfspkg.ScopeRecursiveDir, Reason: "navigate",
	}}})
	tests := []struct {
		name       string
		provider   HostFSReadDecisionProvider
		wantCode   string
		wantRef    string
		wantRetry  bool
		wantStatus string
	}{
		{"pending", stubHostFSReadProvider{result: HostFSReadProposalResult{Code: HostFSErrorReadApprovalRequired, DecisionRef: "dec_hfr_real"}}, HostFSErrorReadApprovalRequired, "dec_hfr_real", false, "denied"},
		{"denied", stubHostFSReadProvider{result: HostFSReadProposalResult{Code: HostFSErrorReadDenied}}, HostFSErrorReadDenied, "", false, "denied"},
		{"limited", stubHostFSReadProvider{result: HostFSReadProposalResult{Code: HostFSErrorReadRequestLimited, RetryAfterMS: int64Ptr(250)}}, HostFSErrorReadRequestLimited, "", true, "denied"},
		{"provider failure", stubHostFSReadProvider{err: errors.New("unavailable")}, HostFSErrorBrokerUnavailable, "", false, "denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := Server{SessionID: "ses_1", Profile: "default", Backend: "lima", Token: "cap_good", HostFS: service, HostFSReadDecisions: tt.provider}
			resp := server.Handle(context.Background(), hostFSRequest("req_read_proposal", "host.fs.read", path, nil))
			if resp.Status != tt.wantStatus || resp.Error == nil || resp.Error.Code != tt.wantCode || resp.Error.DecisionRef != tt.wantRef || (resp.Error.RetryAfterMS != nil) != tt.wantRetry {
				t.Fatalf("response=%+v", resp)
			}
			if tt.wantRef == "" && resp.Error.DecisionRef != "" {
				t.Fatalf("non-decision outcome leaked reference: %+v", resp.Error)
			}
		})
	}
}

func TestBrokerExplicitReadDenyNeverCallsDecisionProvider(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "denied.txt")
	if err := os.WriteFile(path, []byte("denied"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := testHostFSService(t, hostfspkg.Config{
		Grants: []hostfspkg.Rule{{ID: "hfs_see", HostPath: root, Ops: []hostfspkg.Op{hostfspkg.OpDiscover}, Scope: hostfspkg.ScopeDir, Reason: "navigate"}},
		Deny:   []hostfspkg.Rule{{ID: "hfs_deny_read", HostPath: path, Ops: []hostfspkg.Op{hostfspkg.OpRead}, Scope: hostfspkg.ScopeExactFile, Reason: "operator deny"}},
	})
	provider := &countingHostFSReadProvider{}
	server := Server{SessionID: "ses_1", Profile: "default", Backend: "lima", Token: "cap_good", HostFS: service, HostFSReadDecisions: provider}
	resp := server.Handle(context.Background(), hostFSRequest("req_explicit_deny", "host.fs.read", path, nil))
	if resp.Status != "denied" || resp.Error == nil || resp.Error.Code != HostFSErrorReadDenied || resp.Error.Errno != ErrnoEACCES || resp.Error.DecisionRef != "" {
		t.Fatalf("explicit read deny response=%+v", resp)
	}
	if provider.calls != 0 {
		t.Fatalf("explicit read deny created/proposed a decision: calls=%d", provider.calls)
	}
}

type stubHostFSReadProvider struct {
	result HostFSReadProposalResult
	err    error
}

type countingHostFSReadProvider struct {
	calls int
}

func (p *countingHostFSReadProvider) ProposeHostFSRead(context.Context, HostFSReadProposal) (HostFSReadProposalResult, error) {
	p.calls++
	return HostFSReadProposalResult{Code: HostFSErrorReadApprovalRequired, DecisionRef: "dec_should_not_exist"}, nil
}

func (s stubHostFSReadProvider) ProposeHostFSRead(context.Context, HostFSReadProposal) (HostFSReadProposalResult, error) {
	return s.result, s.err
}

func int64Ptr(value int64) *int64 { return &value }

func TestBrokerAggregatesRoutineDiscoveryAuditWithoutPathInventory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "locked.txt")
	if err := os.WriteFile(path, []byte("content-must-not-enter-aggregate"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := testHostFSService(t, hostfspkg.Config{Grants: []hostfspkg.Rule{{
		ID: "hfs_see", HostPath: root, Ops: []hostfspkg.Op{hostfspkg.OpDiscover}, Scope: hostfspkg.ScopeDir, Reason: "navigate",
	}}})
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	writer, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	server := Server{SessionID: "ses_1", Profile: "default", Backend: "lima", Token: "cap_good", HostFS: service, Audit: writer}
	for i := 0; i < 3; i++ {
		_ = server.Handle(context.Background(), hostFSRequest("req_stat_aggregate", "host.fs.stat", path, nil))
	}
	for i := 0; i < 2; i++ {
		_ = server.Handle(context.Background(), hostFSRequest("req_list_aggregate", "host.fs.list", root, nil))
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("routine discovery emitted per-call audit: %s", data)
	}
	if err := server.flushHostFSDiscoveryAudit(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(strings.TrimSpace(text), "\n") != 1 || !strings.Contains(text, `"count":3`) || !strings.Contains(text, `"count":2`) {
		t.Fatalf("aggregate audit did not contain one row per op: %s", text)
	}
	for _, forbidden := range []string{"locked.txt", "content-must-not-enter-aggregate", "cap_good"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("aggregate audit leaked %q: %s", forbidden, text)
		}
	}
}

func TestBrokerHostFSExactDirectoryAndIncompleteErrorsAreTyped(t *testing.T) {
	root := t.TempDir()
	exact := testHostFSService(t, hostfspkg.Config{Grants: []hostfspkg.Rule{{
		ID: "hfs_exact", HostPath: root, Ops: []hostfspkg.Op{hostfspkg.OpDiscover}, Scope: hostfspkg.ScopeExactFile, Reason: "lookup",
	}}})
	server := Server{SessionID: "ses_1", Token: "cap_good", HostFS: exact}
	resp := server.Handle(context.Background(), hostFSRequest("req_exact_list", "host.fs.list", root, nil))
	if resp.Error == nil || resp.Error.Code != HostFSErrorDirectoryNotEnumerable || resp.Error.Errno != ErrnoEACCES {
		t.Fatalf("exact directory denial was not typed: %+v", resp)
	}

	tree := testHostFSService(t, hostfspkg.Config{Grants: []hostfspkg.Rule{{
		ID: "hfs_tree", HostPath: root, Ops: []hostfspkg.Op{hostfspkg.OpDiscover}, Scope: hostfspkg.ScopeRecursiveDir, Reason: "tree",
	}}})
	tree.MaxListEntries = 1
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	server.HostFS = tree
	resp = server.Handle(context.Background(), hostFSRequest("req_overflow", "host.fs.list", root, nil))
	if resp.Error == nil || resp.Error.Code != HostFSErrorDirectoryIncomplete || resp.Error.Errno != ErrnoEOVERFLOW {
		t.Fatalf("incomplete directory error was not typed: %+v", resp)
	}
}
