package adapterpack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/cmdadapter"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/profile"
)

func (s Store) RunTests(ctx context.Context, packID, revisionID string) (TestResult, error) {
	entry, err := s.Inspect(packID)
	if err != nil {
		return TestResult{}, err
	}
	rev, ok := entry.Revisions[revisionID]
	if !ok {
		return TestResult{}, fmt.Errorf("adapter pack %q revision %q is not installed", packID, revisionID)
	}
	root := s.SourceDir(packID, revisionID)
	manifest, _, err := LoadManifest(filepath.Join(root, ManifestFileName))
	if err != nil {
		return TestResult{}, err
	}
	result := TestResult{
		Version:    TestVersion,
		ID:         TestResultID(packID, revisionID),
		PackID:     packID,
		RevisionID: revisionID,
		Status:     TestPassed,
		RecordedAt: time.Now().UTC(),
	}
	if len(manifest.Tests) == 0 {
		result.Status = TestBlocked
		result.Failures = []string{"adapter pack has no tests"}
		return result, s.SaveTestResult(packID, revisionID, result)
	}
	p := profile.Default("adapter-pack-test")
	evaluator := policy.NewEvaluator(p)
	for _, vector := range manifest.Tests {
		outcome := TestVectorOutcome{ID: vector.ID, Status: TestPassed}
		if err := runOneTest(ctx, evaluator, root, manifest, rev, vector); err != nil {
			outcome.Status = TestFailed
			outcome.Reason = err.Error()
			result.Failures = append(result.Failures, vector.ID+": "+err.Error())
			result.Failed++
		} else {
			result.Passed++
		}
		result.Results = append(result.Results, outcome)
	}
	if result.Failed > 0 {
		result.Status = TestFailed
	}
	return result, s.SaveTestResult(packID, revisionID, result)
}

func runOneTest(ctx context.Context, evaluator policy.Evaluator, root string, manifest Manifest, rev Revision, vector TestVector) error {
	adapterSpec, ok := FindAdapter(manifest, vector.AdapterID)
	if !ok {
		return fmt.Errorf("adapter %q is not present", vector.AdapterID)
	}
	sourcePath := filepath.Join(root, filepath.FromSlash(adapterSpec.Script))
	source, err := filepath.Abs(sourcePath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	adapter := cmdadapter.RuntimeAdapter{
		ID:                          adapterSpec.ID,
		Path:                        source,
		Digest:                      DigestBytes(data),
		Entrypoint:                  firstNonEmpty(adapterSpec.Entrypoint, cmdadapter.DefaultEntrypoint),
		Commands:                    append([]string(nil), adapterSpec.Commands...),
		AllowedProposalCapabilities: append([]string(nil), adapterSpec.AllowedProposalCapabilities...),
		Description:                 adapterSpec.Description,
		Source:                      string(data),
	}
	if expected := rev.AdapterDigests[adapterSpec.ID]; expected != "" && expected != adapter.Digest {
		return fmt.Errorf("adapter digest mismatch: expected %s got %s", expected, adapter.Digest)
	}
	testCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := cmdadapter.Evaluate(testCtx, evaluator, adapter, cmdadapter.EvalContext{
		Version: "command-adapter/v1",
		Profile: map[string]string{
			"name": "adapter-pack-test",
		},
		Session: map[string]any{"id": "adapter-pack-test"},
		Adapter: map[string]any{"id": adapter.ID, "digest": adapter.Digest},
		Command: map[string]any{
			"name": vector.Context.Command.Name,
			"argv": append([]string(nil), vector.Context.Command.Argv...),
			"cwd":  firstNonEmpty(vector.Context.Command.CWD, "/workspace"),
		},
		Env:       map[string]any{"keys": []string{}, "classes": []string{}, "count": 0},
		Workspace: map[string]any{"guestRoot": "/workspace", "mode": "read-write"},
		Network:   map[string]any{"mode": "direct"},
	})
	if err != nil {
		return err
	}
	if out.Outcome != vector.Expect.Outcome {
		return fmt.Errorf("outcome=%q want %q", out.Outcome, vector.Expect.Outcome)
	}
	if vector.Expect.Capability != "" && out.Capability != vector.Expect.Capability {
		return fmt.Errorf("capability=%q want %q", out.Capability, vector.Expect.Capability)
	}
	if vector.Expect.ReasonContains != "" && !strings.Contains(out.Reason, vector.Expect.ReasonContains) {
		return fmt.Errorf("reason %q does not contain %q", out.Reason, vector.Expect.ReasonContains)
	}
	return nil
}
