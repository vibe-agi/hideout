package cmdadapter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vibe-agi/hideout/internal/profile"
)

type testResolver struct{}

func (testResolver) ResolveCommandAdapter(profileDir, id string, adapter profile.CommandAdapter) (ResolvedSource, error) {
	return ResolvedSource{
		Source: "function decideCommandAdapter(){ return {outcome:'deny', reason:'blocked'} }",
		Digest: "sha256:5c56876531b552127e352981ae54947e43e6269d8544f11cc99bb2edc882688c",
		Path:   filepath.Join(profileDir, "pack.js"),
	}, nil
}

func TestProfileRejectsDuplicateCommandOwnership(t *testing.T) {
	p := profile.Default("adapter-test")
	p.CommandAdapters.Adapters = map[string]profile.CommandAdapter{
		"adapter": {
			Enabled:    true,
			Path:       "adapter.js",
			Digest:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Entrypoint: DefaultEntrypoint,
			Commands:   []string{"open"},
		},
	}
	if err := p.Validate(); err == nil {
		t.Fatal("expected duplicate command ownership error")
	}
}

func TestCompileAdapterVerifiesDigest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "adapter.js"), []byte("function decideCommandAdapter() { return {outcome: 'deny', reason: 'x'} }"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := profile.CommandAdapter{
		Enabled:    true,
		Path:       "adapter.js",
		Digest:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Entrypoint: DefaultEntrypoint,
		Commands:   []string{"tool-x"},
	}
	if _, err := CompileAdapter(dir, "adapter", adapter); err == nil {
		t.Fatal("expected digest mismatch")
	}
	source, digest, err := ResolveSource(dir, RuntimeAdapter{ID: "adapter", Path: "adapter.js"})
	if err != nil {
		t.Fatal(err)
	}
	if source == "" || digest == "" {
		t.Fatalf("expected source and digest, got source=%q digest=%q", source, digest)
	}
	adapter.Digest = digest
	if _, err := CompileAdapter(dir, "adapter", adapter); err != nil {
		t.Fatalf("compile with matching digest: %v", err)
	}
}

func TestCompilePackBackedAdapterRequiresResolver(t *testing.T) {
	p := profile.Default("pack-test")
	p.CommandAdapters.Adapters = map[string]profile.CommandAdapter{
		"tool": {
			Enabled:        true,
			PackID:         "example.pack",
			PackRevisionID: "rev_abc",
			PackAdapterID:  "tool",
			PackLockDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Commands:       []string{"tool-x"},
		},
	}
	if _, err := Compile(p, t.TempDir()); err == nil {
		t.Fatal("expected missing pack resolver to fail closed")
	}
}

func TestCompilePackBackedAdapterUsesResolver(t *testing.T) {
	p := profile.Default("pack-test")
	p.CommandAdapters.Adapters = map[string]profile.CommandAdapter{
		"tool": {
			Enabled:        true,
			Digest:         "sha256:5c56876531b552127e352981ae54947e43e6269d8544f11cc99bb2edc882688c",
			PackID:         "example.pack",
			PackRevisionID: "rev_abc",
			PackAdapterID:  "tool",
			PackLockDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Commands:       []string{"tool-x"},
		},
	}
	rt, err := CompileWithResolver(p, t.TempDir(), testResolver{})
	if err != nil {
		t.Fatalf("compile pack-backed adapter: %v", err)
	}
	if _, ok := AdapterForCommand(rt, "tool-x"); !ok {
		t.Fatalf("pack-backed adapter did not own command: %+v", rt)
	}
}
