package webui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vibe-agi/hideout/internal/audit"
)

func TestBrowserArtifactRedactionCanary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	raw := "HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:1 cap_0123456789abcdef0123456789abcdef"
	sanitized := audit.RedactString(raw)
	if err := os.WriteFile(path, []byte(sanitized), 0o600); err != nil {
		t.Fatal(err)
	}
	assertNoControlPlaneMaterial(t, dir)
}
