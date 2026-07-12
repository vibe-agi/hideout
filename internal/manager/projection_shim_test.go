package manager

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/cmdgrammar"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
)

func TestGeneratedHostAppShimPinsProjectionActionGrammarAndBinding(t *testing.T) {
	grammar := cmdgrammar.OpenResourceGrammar{Kind: cmdgrammar.GrammarOpenResourceV1, ResourceCount: 1, GotoFlags: []string{"-g"}, UnknownFlags: cmdgrammar.UnknownFlagsDeny}
	reg := cmdproxy.Registration{Name: "editor", Action: cmdproxy.ActionHostAppOpenResource, ArgvSchema: cmdproxy.ArgvSchemaOpenResourceV1, BindingDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", OpenResourceGrammar: &grammar}
	for _, tc := range []struct {
		name   string
		script func(string, cmdproxy.Registration) string
		lima   bool
	}{
		{name: "native", script: func(shim string, reg cmdproxy.Registration) string { return nativeShimScript(shim, reg) }},
		{name: "lima", script: func(_ string, reg cmdproxy.Registration) string { return limaShimScript(reg) }, lima: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "marker")
			fakeShim := filepath.Join(dir, "fake-hideout-shim")
			if tc.lima {
				fakeShim = filepath.Join(dir, "hideout-shim")
			}
			fake := "#!/bin/sh\nprintf '%s|%s|%s|%s\\n' \"$HIDEOUT_COMMAND_PROXY_ACTION\" \"$HIDEOUT_COMMAND_PROXY_BINDING_DIGEST\" \"$HIDEOUT_COMMAND_PROXY_GRAMMAR_B64\" \"$*\" > \"$PROJECTION_MARKER\"\n"
			if err := os.WriteFile(fakeShim, []byte(fake), 0o700); err != nil {
				t.Fatal(err)
			}
			commandShim := filepath.Join(dir, "editor")
			if err := os.WriteFile(commandShim, []byte(tc.script(fakeShim, reg)), 0o700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(commandShim, "-g", "src/main.go:12:3")
			cmd.Env = append(os.Environ(), "PROJECTION_MARKER="+marker)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("run generated shim: %v: %s", err, out)
			}
			data, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.TrimSpace(string(data))
			parts := strings.Split(got, "|")
			if len(parts) != 4 || parts[0] != cmdproxy.ActionHostAppOpenResource || parts[1] != reg.BindingDigest || parts[2] == "" || parts[3] != "--action "+cmdproxy.ActionHostAppOpenResource+" editor -g src/main.go:12:3" {
				t.Fatalf("generated shim route = %q", got)
			}
		})
	}
}
