package cmdproxy

import (
	"reflect"
	"testing"

	"github.com/vibe-agi/hideout/internal/profile"
)

func TestDefaultRegistryRegistersOpenAndAlias(t *testing.T) {
	registry := DefaultRegistry()
	names := registry.ShimNames()
	want := []string{"open", "xdg-open"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("ShimNames=%v want %v", names, want)
	}
	open, ok := registry.Lookup("open")
	if !ok || open.Action != ActionHostOpen {
		t.Fatalf("open registration missing: %+v ok=%t", open, ok)
	}
	alias, ok := registry.Lookup("xdg-open")
	if !ok || alias.Name != "open" || alias.Action != ActionHostOpen {
		t.Fatalf("alias registration mismatch: %+v ok=%t", alias, ok)
	}
}

func TestNewRegistryRejectsAmbiguousRegistrations(t *testing.T) {
	base := Registration{
		Name:           "open",
		Action:         ActionHostOpen,
		ArgvSchema:     ArgvSchemaOpenV1,
		StreamPolicy:   StreamMetadataOnly,
		DefaultMode:    DefaultModeAllow,
		AllowedTargets: []string{"url:https"},
	}
	for name, registrations := range map[string][]Registration{
		"duplicate command": {
			base,
			base,
		},
		"duplicate alias": {
			func() Registration { r := base; r.Aliases = []string{"xdg-open"}; return r }(),
			{Name: "open-url", Aliases: []string{"xdg-open"}, Action: ActionHostOpen, ArgvSchema: ArgvSchemaOpenV1},
		},
		"alias conflicts with command": {
			func() Registration { r := base; r.Aliases = []string{"open-url"}; return r }(),
			{Name: "open-url", Action: ActionHostOpen, ArgvSchema: ArgvSchemaOpenV1},
		},
		"path command": {
			func() Registration { r := base; r.Name = "/tmp/open"; return r }(),
		},
		"path alias": {
			func() Registration { r := base; r.Aliases = []string{"/tmp/xdg-open"}; return r }(),
		},
		"backslash command": {
			func() Registration { r := base; r.Name = `tmp\open`; return r }(),
		},
		"backslash alias": {
			func() Registration { r := base; r.Aliases = []string{`tmp\xdg-open`}; return r }(),
		},
		"parent command": {
			func() Registration { r := base; r.Name = ".."; return r }(),
		},
		"dot alias": {
			func() Registration { r := base; r.Aliases = []string{"."}; return r }(),
		},
		"spaced command": {
			func() Registration { r := base; r.Name = " open"; return r }(),
		},
		"spaced alias": {
			func() Registration { r := base; r.Aliases = []string{"xdg-open "}; return r }(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewRegistry(registrations); err == nil {
				t.Fatal("expected ambiguous registry to fail")
			}
		})
	}
}

func TestResolveInvocationSupportsWrapperAndShimName(t *testing.T) {
	registry := DefaultRegistry()
	command, args, err := registry.ResolveInvocation("hideout-shim", []string{"open", "https://example.com"})
	if err != nil {
		t.Fatalf("ResolveInvocation wrapper: %v", err)
	}
	if command != "open" || !reflect.DeepEqual(args, []string{"https://example.com"}) {
		t.Fatalf("wrapper command=%s args=%v", command, args)
	}
	command, args, err = registry.ResolveInvocation("hideout-shim", []string{"/hideout/session/shims/xdg-open", "https://example.com"})
	if err != nil {
		t.Fatalf("ResolveInvocation wrapper path: %v", err)
	}
	if command != "xdg-open" || !reflect.DeepEqual(args, []string{"https://example.com"}) {
		t.Fatalf("wrapper path command=%s args=%v", command, args)
	}
	command, args, err = registry.ResolveInvocation("/hideout/session/shims/xdg-open", []string{"file.txt"})
	if err != nil {
		t.Fatalf("ResolveInvocation shim: %v", err)
	}
	if command != "xdg-open" || !reflect.DeepEqual(args, []string{"file.txt"}) {
		t.Fatalf("shim command=%s args=%v", command, args)
	}
}

func TestNormalizeOpenRequest(t *testing.T) {
	registry := DefaultRegistry()
	req, err := registry.Normalize("/hideout/session/shims/xdg-open", []string{"https://example.com"}, "/workspace/./src/..")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if req.Subject != "command:xdg-open" || req.Action != ActionHostOpen || req.Route != RouteHostBroker {
		t.Fatalf("unexpected normalized request: %+v", req)
	}
	if req.CWD != "/workspace" || req.Payload["target"] != "https://example.com" || req.Payload["cwd"] != "/workspace" {
		t.Fatalf("payload mismatch: %+v", req.Payload)
	}
}

func TestNormalizeRejectsInvalidCWD(t *testing.T) {
	for name, cwd := range map[string]string{
		"empty":        "",
		"relative":     "workspace",
		"url":          "https://example.com",
		"network path": "//host/share",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DefaultRegistry().Normalize("open", []string{"https://example.com"}, cwd)
			if err == nil {
				t.Fatal("expected invalid cwd to fail")
			}
		})
	}
}

func TestNormalizeRejectsMissingTarget(t *testing.T) {
	_, err := DefaultRegistry().Normalize("open", nil, "/workspace")
	if err == nil {
		t.Fatal("expected missing target to fail")
	}
}

func TestNormalizeRejectsBlankTarget(t *testing.T) {
	_, err := DefaultRegistry().Normalize("open", []string{"   "}, "/workspace")
	if err == nil {
		t.Fatal("expected blank target to fail")
	}
}

func TestNormalizeRejectsExtraOpenArgs(t *testing.T) {
	_, err := DefaultRegistry().Normalize("open", []string{"https://example.com", "--token", "secret"}, "/workspace")
	if err == nil {
		t.Fatal("expected extra open args to fail")
	}
}

func TestRegistryFromProfileUsesCommandProxyConfig(t *testing.T) {
	p := profile.Default("test")
	delete(p.CommandProxy.Commands, "xdg-open")
	registry, err := RegistryFromProfile(p)
	if err != nil {
		t.Fatalf("RegistryFromProfile: %v", err)
	}
	if got := registry.ShimNames(); !reflect.DeepEqual(got, []string{"open"}) {
		t.Fatalf("ShimNames=%v", got)
	}
	if _, ok := registry.Lookup("xdg-open"); ok {
		t.Fatal("xdg-open should not be registered when profile omits it")
	}
}

func TestRegistryFromProfileRejectsUnsupportedHostEscape(t *testing.T) {
	p := profile.Default("test")
	p.CommandProxy.Commands["shell"] = profile.CommandProxyCommand{
		Route:  "host-broker",
		Action: "host.exec",
	}
	if _, err := RegistryFromProfile(p); err == nil {
		t.Fatal("expected unsupported command proxy to fail")
	}
}
