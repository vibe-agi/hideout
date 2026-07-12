package tui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/productevidence"
	webui "github.com/vibe-agi/hideout/test/e2e/webui"
)

type Result struct {
	ConsoleVisible        bool              `json:"consoleVisible"`
	InitialRenderCount    int               `json:"initialRenderCount"`
	RenderCountAfterEvent int               `json:"renderCountAfterEvent"`
	LiveUpdateObserved    bool              `json:"liveUpdateObserved"`
	FallbackObserved      bool              `json:"fallbackObserved"`
	Artifacts             map[string]string `json:"artifacts"`
}

func runTUIProof(t *testing.T, prereq Prerequisites, fixture webui.Fixture, outDir string) Result {
	t.Helper()
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		t.Fatal(err)
	}
	root := repoRoot(t)
	binary := buildHideoutBinary(t, prereq, root, outDir)
	rawTranscript := filepath.Join(outDir, "terminal.raw.txt")
	ctx, cancel := context.WithCancel(context.Background())
	cmd, stderr := tuiCommand(ctx, prereq, root, rawTranscript, fixture.StoreRoot, binary, 10*time.Millisecond)
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start TUI proof command: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	processDone := false
	defer func() {
		if processDone {
			return
		}
		cancel()
		select {
		case <-done:
			processDone = true
		case <-time.After(5 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			select {
			case <-done:
				processDone = true
			case <-time.After(3 * time.Second):
				t.Fatalf("TUI proof process did not exit after cancellation; stderr=%s", stderr.String())
			}
		}
	}()

	waitForTranscript(t, rawTranscript, func(s string) bool {
		return strings.Contains(s, "Operator Console") && strings.Contains(s, "Stream: live")
	}, 20*time.Second)
	time.Sleep(300 * time.Millisecond)
	idle := readTranscript(t, rawTranscript)
	initialRenders := strings.Count(idle, "Operator Console")
	if initialRenders != 1 {
		t.Fatalf("TUI rendered %d times while stream was healthy and idle; want exactly 1", initialRenders)
	}

	ackNotice(t, fixture)
	waitForTranscript(t, rawTranscript, func(s string) bool {
		return strings.Contains(s, "acknowledged=true")
	}, 10*time.Second)
	afterEvent := readTranscript(t, rawTranscript)
	eventRenders := strings.Count(afterEvent, "Operator Console")
	if eventRenders <= initialRenders {
		t.Fatalf("TUI did not render after live event; before=%d after=%d", initialRenders, eventRenders)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := fixture.StopDaemon(stopCtx); err != nil {
		stopCancel()
		t.Fatalf("stop daemon for TUI fallback proof: %v", err)
	}
	stopCancel()
	waitForTranscript(t, rawTranscript, func(s string) bool {
		return strings.Contains(s, "Stream: disconnected") || strings.Contains(s, "event stream closed")
	}, 10*time.Second)

	cancel()
	select {
	case err := <-done:
		processDone = true
		if err != nil && !errors.Is(ctx.Err(), context.Canceled) {
			// script(1) commonly returns a signal-related status after context
			// cancellation; the transcript assertions above are the proof.
		}
	case <-time.After(5 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
			processDone = true
		case <-time.After(3 * time.Second):
			t.Fatalf("TUI proof process did not exit after cancellation; stderr=%s", stderr.String())
		}
	}

	sanitized := audit.RedactString(readTranscript(t, rawTranscript))
	transcriptPath := filepath.Join(outDir, "terminal-capture.txt")
	if err := os.WriteFile(transcriptPath, []byte(sanitized), 0o600); err != nil {
		t.Fatal(err)
	}
	result := Result{
		ConsoleVisible:        true,
		InitialRenderCount:    initialRenders,
		RenderCountAfterEvent: eventRenders,
		LiveUpdateObserved:    true,
		FallbackObserved:      true,
		Artifacts: map[string]string{
			"terminal-capture": "terminal-capture.txt",
			"event-summary":    "tui-result.json",
		},
	}
	resultPath := filepath.Join(outDir, "tui-result.json")
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(resultPath, []byte(audit.RedactString(string(data))), 0o600); err != nil {
		t.Fatal(err)
	}
	return result
}

func buildHideoutBinary(t *testing.T, prereq Prerequisites, root, outDir string) string {
	t.Helper()
	binary := filepath.Join(outDir, "hideout-e2e")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, prereq.GoPath, "build", "-o", binary, "./cmd/hideout")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build hideout binary for TUI proof: %v\n%s", err, output)
	}
	return binary
}

func tuiCommand(ctx context.Context, prereq Prerequisites, root, transcript, storeRoot, binary string, interval time.Duration) (*exec.Cmd, *bytes.Buffer) {
	intervalArg := interval.String()
	var args []string
	switch runtime.GOOS {
	case "linux":
		command := fmt.Sprintf("%s tui --interval %s", shellQuote(binary), shellQuote(intervalArg))
		args = []string{"-q", "-f", "-e", "-c", command, transcript}
	default:
		args = []string{"-q", "-F", transcript, binary, "tui", "--interval", intervalArg}
	}
	cmd := exec.CommandContext(ctx, prereq.ScriptPath, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"HIDEOUT_STORE_ROOT="+storeRoot,
		"TERM=xterm-256color",
		"NO_COLOR=1",
	)
	var stderr bytes.Buffer
	cmd.Stdout = nil
	cmd.Stderr = &stderr
	return cmd, &stderr
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func ackNotice(t *testing.T, fixture webui.Fixture) {
	t.Helper()
	body := strings.NewReader(`{"noticeId":"` + fixture.NoticeID + `","surface":"tui-e2e"}`)
	req, err := http.NewRequest(http.MethodPost, fixture.BaseURL+"/api/v1/notice/ack", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+fixture.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ack notice: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ack notice status=%s", resp.Status)
	}
}

func waitForTranscript(t *testing.T, path string, pred func(string) bool, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(path)
		text := string(data)
		if pred(text) {
			return text
		}
		time.Sleep(50 * time.Millisecond)
	}
	text := readTranscript(t, path)
	t.Fatalf("timed out waiting for transcript condition; transcript:\n%s", text)
	return ""
}

func readTranscript(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ""
		}
		t.Fatal(err)
	}
	return string(data)
}

func writeTUIProofs(t *testing.T, outDir, artifactPrefix string, result Result) {
	t.Helper()
	artifacts := resultArtifacts(t, outDir, artifactPrefix, result)
	proofs := []productevidence.ProofEntry{
		tuiProof(productevidence.Proof021TUIPTYConsole, "021.FR-006", "TUI launches as a real terminal process and renders the operator console", artifacts),
		tuiProof(productevidence.Proof021TUIPTYLive, "021.FR-008", "TUI updates visible terminal output from a daemon event", artifacts),
		tuiProof(productevidence.Proof021TUIPTYNoPolling, "021.FR-009", "TUI does not interval-poll while the daemon stream is healthy", artifacts),
		tuiProof(productevidence.Proof021TUIPTYFallback, "021.FR-010", "TUI shows stream fallback when the daemon event stream closes", artifacts),
	}
	path := filepath.Join(outDir, "proofs.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	enc := json.NewEncoder(file)
	for _, proof := range proofs {
		proof = proof.Sanitized()
		if err := proof.Validate(); err != nil {
			t.Fatalf("invalid TUI proof %s: %v", proof.ProofID, err)
		}
		if err := enc.Encode(proof); err != nil {
			t.Fatal(err)
		}
	}
}

func tuiProof(proofID, claimID, description string, artifacts []productevidence.ArtifactRef) productevidence.ProofEntry {
	host := productevidence.CurrentHost()
	return productevidence.ProofEntry{
		ProofID:        proofID,
		FeatureID:      productevidence.Feature021,
		Mode:           "pty-e2e",
		EvidenceClass:  "local-ui-e2e",
		Status:         productevidence.StatusPassed,
		CommandSummary: "scripts/test-ui-e2e.sh --tui --out <evidence-dir>",
		CoveredClaims: []productevidence.CoveredClaim{{
			ClaimID:     claimID,
			Source:      "spec",
			Description: description,
			Scope:       "tui",
		}},
		Prerequisites: []productevidence.PrerequisiteStatus{
			{Name: "script", Status: "available"},
			{Name: "go", Status: "available"},
		},
		Artifacts:       artifacts,
		RedactionStatus: productevidence.RedactionPassed,
		Host:            &host,
	}
}

func resultArtifacts(t *testing.T, outDir, artifactPrefix string, result Result) []productevidence.ArtifactRef {
	t.Helper()
	refs := []productevidence.ArtifactRef{}
	for kind, name := range result.Artifacts {
		path := filepath.Join(outDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing TUI artifact %s: %v", path, err)
		}
		refs = append(refs, productevidence.ArtifactRef{
			Kind:            kind,
			Path:            filepath.ToSlash(filepath.Join(artifactPrefix, name)),
			SHA256:          sha256File(t, path),
			RedactionStatus: productevidence.RedactionPassed,
			Description:     "TUI PTY proof " + kind,
		})
	}
	return refs
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func assertNoControlPlaneMaterial(t *testing.T, outDir string) {
	t.Helper()
	err := filepath.WalkDir(outDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasSuffix(path, ".raw.txt") {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".txt" && ext != ".json" && ext != ".jsonl" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if productevidence.ContainsControlPlaneMaterial(string(data)) {
			return errors.New("unredacted control-plane material in " + path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate current file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			t.Fatal("cannot locate repository root")
		}
		dir = next
	}
}
