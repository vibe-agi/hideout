package releasecompat

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalFastReadinessIsNotReleaseEvidence(t *testing.T) {
	ready, err := BuildReadiness(ReadinessOptions{
		Mode:        "local-fast",
		Commit:      "abc123",
		LocalPassed: true,
		Now:         time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	if err := ValidateReadiness(ready); err != nil {
		t.Fatalf("validate readiness: %v", err)
	}
	if ready.ReleaseReady {
		t.Fatal("local-fast claimed releaseReady")
	}
	if ready.Status != "not-release" || ready.EvidenceClass != "local-fast" {
		t.Fatalf("unexpected local-fast status: %+v", ready)
	}
	for _, gate := range ready.Gates {
		if gate.Status != "not-run" {
			t.Fatalf("local-fast gate should be not-run: %+v", gate)
		}
	}
}

func TestReleaseCandidateMissingEvidenceFailsClosed(t *testing.T) {
	ready, err := BuildReadiness(ReadinessOptions{Mode: "release-candidate", LocalPassed: true})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	if ready.ReleaseReady {
		t.Fatal("candidate without evidence claimed releaseReady")
	}
	if ready.Status != "failed" {
		t.Fatalf("status=%s", ready.Status)
	}
	for _, gate := range ready.Gates {
		if gate.Status != "missing" {
			t.Fatalf("gate should be missing: %+v", gate)
		}
	}
}

func TestReleaseCandidateWithEvidenceCanPass(t *testing.T) {
	dir := t.TempDir()
	gate2 := filepath.Join(dir, "gate2.json")
	gate3 := filepath.Join(dir, "gate3.json")
	if err := os.WriteFile(gate2, []byte(`{"id":"gate2-lima","backend":"lima","result":"passed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate3, []byte(`{"id":"gate3-hidden-proxy","backend":"lima","result":"passed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ready, err := BuildReadiness(ReadinessOptions{Mode: "release-candidate", LocalPassed: true, Gate2Evidence: gate2, Gate3Evidence: gate3})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	if err := ValidateReadiness(ready); err != nil {
		t.Fatalf("validate readiness: %v", err)
	}
	if !ready.ReleaseReady || ready.Status != "passed" {
		t.Fatalf("candidate with evidence not ready: %+v", ready)
	}
}

func TestReleaseCandidateRejectsEmptyOrWrongGateEvidence(t *testing.T) {
	dir := t.TempDir()
	gate2 := filepath.Join(dir, "gate2.json")
	gate3 := filepath.Join(dir, "gate3.json")
	if err := os.WriteFile(gate2, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate3, []byte(`{"id":"gate2-lima","backend":"lima","result":"passed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ready, err := BuildReadiness(ReadinessOptions{Mode: "release-candidate", LocalPassed: true, Gate2Evidence: gate2, Gate3Evidence: gate3})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	if ready.ReleaseReady {
		t.Fatal("candidate with empty/wrong evidence claimed releaseReady")
	}
	if ready.Gates[0].Status != "failed" || ready.Gates[1].Status != "failed" {
		t.Fatalf("expected failed gates, got %+v", ready.Gates)
	}
}

func TestReleaseCandidateRejectsNativeGateEvidence(t *testing.T) {
	dir := t.TempDir()
	gate2 := filepath.Join(dir, "gate2.json")
	gate3 := filepath.Join(dir, "gate3.json")
	if err := os.WriteFile(gate2, []byte(`{"id":"gate2-lima","backend":"native","result":"passed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gate3, []byte(`{"id":"gate3-hidden-proxy","backend":"lima","result":"passed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ready, err := BuildReadiness(ReadinessOptions{Mode: "release-candidate", LocalPassed: true, Gate2Evidence: gate2, Gate3Evidence: gate3})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	if ready.ReleaseReady || ready.Gates[0].Status != "failed" {
		t.Fatalf("native evidence accepted: %+v", ready)
	}
}

func TestReleaseCandidateAcceptsReleaseDogfoodManifest(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "manifest.json")
	data := []byte(`{
  "schema": "hideout.release-dogfood.v1",
  "status": "passed",
  "isolationGates": [
    {"id":"gate2-lima","backend":"lima","result":"passed"},
    {"id":"gate3-hidden-proxy","backend":"lima","result":"passed"}
  ]
}`)
	if err := os.WriteFile(manifest, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ready, err := BuildReadiness(ReadinessOptions{Mode: "release-candidate", LocalPassed: true, Gate2Evidence: manifest, Gate3Evidence: manifest})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	if !ready.ReleaseReady {
		t.Fatalf("dogfood manifest was not accepted: %+v", ready)
	}
}

func TestReadinessRedactsControlPlaneMaterial(t *testing.T) {
	ready, err := BuildReadiness(ReadinessOptions{
		Mode:          "release-candidate",
		Commit:        `HIDEOUT_SECRET_DEFAULT_PROXY=socks5://user:pass@127.0.0.1:7890 cap_0123456789abcdef0123456789abcdef`,
		Gate2Evidence: "/tmp/HIDEOUT_SECRET_DEFAULT_PROXY=secret/gate2.json",
		Gate3Evidence: "/tmp/gate3.json",
		LocalPassed:   false,
	})
	if err != nil {
		t.Fatalf("build readiness: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteReadinessJSON(&buf, ready); err != nil {
		t.Fatalf("write readiness: %v", err)
	}
	text := buf.String()
	for _, forbidden := range []string{
		"HIDEOUT_SECRET_DEFAULT_PROXY=socks5://user:pass@127.0.0.1:7890",
		"cap_0123456789abcdef0123456789abcdef",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("readiness leaked %q:\n%s", forbidden, text)
		}
	}
	var decoded Readiness
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if decoded.Redaction.Mode != "control-plane" {
		t.Fatalf("redaction=%+v", decoded.Redaction)
	}
}
