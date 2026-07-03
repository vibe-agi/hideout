package profile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/hostfs"
)

func TestDefaultStoreCanUseExplicitHideoutStoreRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	t.Setenv("HIDEOUT_STORE_ROOT", root)
	store, err := DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if store.Root != root {
		t.Fatalf("store root=%q want %q", store.Root, root)
	}
}

func TestDefaultStoreRejectsRelativeHideoutStoreRoot(t *testing.T) {
	t.Setenv("HIDEOUT_STORE_ROOT", "relative-store")
	_, err := DefaultStore()
	if err == nil || !strings.Contains(err.Error(), "HIDEOUT_STORE_ROOT must be an absolute path") {
		t.Fatalf("expected relative HIDEOUT_STORE_ROOT failure, got %v", err)
	}
}

func TestStoreSaveMaterializesProfile(t *testing.T) {
	store := Store{Root: t.TempDir()}
	p := Default("test")
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.ProfilePath("test")); err != nil {
		t.Fatalf("profile.json missing: %v", err)
	}
	gitConfig := filepath.Join(store.ProfileDir("test"), "home", ".gitconfig")
	data, err := os.ReadFile(gitConfig)
	if err != nil {
		t.Fatalf(".gitconfig missing: %v", err)
	}
	if got := string(data); got == "" || !strings.Contains(got, "developer@example.com") {
		t.Fatalf("git config did not contain profile email: %q", got)
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("test"), "identity.json")); err != nil {
		t.Fatalf("identity metadata missing: %v", err)
	}
	for link, target := range map[string]string{
		filepath.Join(store.ProfileDir("test"), "home", ".config"):         filepath.Join(store.ProfileDir("test"), "config"),
		filepath.Join(store.ProfileDir("test"), "home", ".cache"):          filepath.Join(store.ProfileDir("test"), "cache"),
		filepath.Join(store.ProfileDir("test"), "home", ".local", "share"): filepath.Join(store.ProfileDir("test"), "data"),
	} {
		resolved, err := filepath.EvalSymlinks(link)
		if err != nil {
			t.Fatalf("home XDG link %s missing or invalid: %v", link, err)
		}
		resolvedTarget, err := filepath.EvalSymlinks(target)
		if err != nil {
			t.Fatalf("home XDG target %s missing or invalid: %v", target, err)
		}
		if resolved != resolvedTarget {
			t.Fatalf("home XDG link %s resolved to %s want %s", link, resolved, resolvedTarget)
		}
	}
	reloaded, err := store.Load("test")
	if err != nil {
		t.Fatalf("reload profile: %v", err)
	}
	if reloaded.Metadata["profileId"] == "" || reloaded.Metadata["identityId"] == "" {
		t.Fatalf("profile metadata missing IDs: %+v", reloaded.Metadata)
	}
	if reloaded.Metadata["machineId"] == "" || len(reloaded.Metadata["machineId"]) != 32 {
		t.Fatalf("profile metadata missing machineId: %+v", reloaded.Metadata)
	}
	machineID, err := os.ReadFile(filepath.Join(store.ProfileDir("test"), "machine", "machine-id"))
	if err != nil {
		t.Fatalf("machine-id missing: %v", err)
	}
	if strings.TrimSpace(string(machineID)) != reloaded.Metadata["machineId"] {
		t.Fatalf("machine-id file mismatch: %q metadata=%+v", machineID, reloaded.Metadata)
	}
}

func TestMaterializeIdentityStateRejectsInvalidHomeXDGMappings(t *testing.T) {
	for _, tt := range []struct {
		name      string
		setupPath string
		setup     func(t *testing.T, path string)
		want      string
	}{
		{
			name:      "config directory",
			setupPath: filepath.Join("home", ".config"),
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			want: "must be a symlink",
		},
		{
			name:      "cache wrong symlink",
			setupPath: filepath.Join("home", ".cache"),
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../../host-cache", path); err != nil {
					t.Fatal(err)
				}
			},
			want: "points to",
		},
		{
			name:      "data wrong symlink",
			setupPath: filepath.Join("home", ".local", "share"),
			setup: func(t *testing.T, path string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../../../host-data", path); err != nil {
					t.Fatal(err)
				}
			},
			want: "points to",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p := Default("test")
			if err := ensureMetadata(&p, "create", ""); err != nil {
				t.Fatal(err)
			}
			tt.setup(t, filepath.Join(dir, tt.setupPath))

			err := MaterializeIdentityState(dir, p)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q failure, got %v", tt.want, err)
			}
		})
	}
}

func TestStoreCreateRejectsExistingProfileDirectory(t *testing.T) {
	store := Store{Root: t.TempDir()}
	if err := store.Create(Default("test")); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	err := store.Create(Default("test"))
	if err == nil || !strings.Contains(err.Error(), `profile "test" already exists`) {
		t.Fatalf("expected existing profile failure, got %v", err)
	}
	if err := os.MkdirAll(store.ProfileDir("partial"), 0o700); err != nil {
		t.Fatal(err)
	}
	err = store.Create(Default("partial"))
	if err == nil || !strings.Contains(err.Error(), `profile "partial" already exists`) {
		t.Fatalf("expected partial profile directory failure, got %v", err)
	}
}

func TestLoadOrInitBackfillsMissingMetadata(t *testing.T) {
	store := Store{Root: t.TempDir()}
	p := Default("manual")
	p.Metadata = nil
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	profilePath := store.ProfilePath("manual")
	if err := os.MkdirAll(filepath.Dir(profilePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.LoadOrInit("manual")
	if err != nil {
		t.Fatalf("LoadOrInit: %v", err)
	}
	for _, key := range []string{"profileId", "identityId", "machineId", "createdAt", "lineageMode"} {
		if loaded.Metadata[key] == "" {
			t.Fatalf("metadata %s was not backfilled: %+v", key, loaded.Metadata)
		}
	}
	machineID, err := os.ReadFile(filepath.Join(store.ProfileDir("manual"), "machine", "machine-id"))
	if err != nil {
		t.Fatalf("machine-id was not materialized: %v", err)
	}
	if strings.TrimSpace(string(machineID)) != loaded.Metadata["machineId"] {
		t.Fatalf("machine-id file mismatch: %q metadata=%+v", machineID, loaded.Metadata)
	}
}

func TestLoadOrInitRematerializesEditedProfileIdentityFiles(t *testing.T) {
	store := Store{Root: t.TempDir()}
	p := Default("manual")
	p.Git.UserEmail = "old@example.com"
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	edited, err := store.Load("manual")
	if err != nil {
		t.Fatal(err)
	}
	edited.Git.UserEmail = "new@example.com"
	data, err := json.MarshalIndent(edited, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.ProfilePath("manual"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	gitConfig := filepath.Join(store.ProfileDir("manual"), "home", ".gitconfig")
	before, err := os.ReadFile(gitConfig)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(before), "new@example.com") {
		t.Fatalf("test setup expected stale gitconfig, got %s", before)
	}

	loaded, err := store.LoadOrInit("manual")
	if err != nil {
		t.Fatalf("LoadOrInit: %v", err)
	}
	if loaded.Git.UserEmail != "new@example.com" {
		t.Fatalf("loaded profile email=%q", loaded.Git.UserEmail)
	}
	after, err := os.ReadFile(gitConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "new@example.com") {
		t.Fatalf("gitconfig was not rematerialized from edited profile: %s", after)
	}
}

func TestValidateRequiresSchemaVersion(t *testing.T) {
	p := Default("test")
	p.SchemaVersion = ""
	if err := p.Validate(); err == nil {
		t.Fatal("expected missing schemaVersion to fail")
	}
}

func TestDefaultProfileIncludesToolPreset(t *testing.T) {
	p := Default("test")
	if len(p.Tools.Presets) != 1 || p.Tools.Presets[0] != "base-dev" {
		t.Fatalf("unexpected tool presets: %+v", p.Tools.Presets)
	}
}

func TestDefaultProfileIncludesCommandProxyPolicy(t *testing.T) {
	p := Default("test")
	open := p.CommandProxy.Commands["open"]
	if open.Route != "host-broker" || open.Action != "host.open" {
		t.Fatalf("unexpected open command proxy: %+v", open)
	}
	xdgOpen := p.CommandProxy.Commands["xdg-open"]
	if xdgOpen.Route != "host-broker" || xdgOpen.Action != "host.open" {
		t.Fatalf("unexpected xdg-open command proxy: %+v", xdgOpen)
	}
}

func TestDefaultProfileHostFSIsHiddenByDefault(t *testing.T) {
	p := Default("test")
	if len(p.HostFS.Grants) != 0 || len(p.HostFS.Deny) != 0 {
		t.Fatalf("default HostFS policy should be empty: %+v", p.HostFS)
	}
}

func TestDefaultProfileDeniesProxyAndHideoutRuntimeEnvNames(t *testing.T) {
	p := Default("test")
	deny := map[string]bool{}
	for _, name := range p.Env.Deny {
		deny[name] = true
	}
	for _, name := range []string{
		"HIDEOUT_*",
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"NO_PROXY",
		"ALL_PROXY",
		"FTP_PROXY",
		"http_proxy",
		"https_proxy",
		"no_proxy",
		"all_proxy",
		"ftp_proxy",
	} {
		if !deny[name] {
			t.Fatalf("default profile deny list missing proxy env %s: %+v", name, p.Env.Deny)
		}
	}
}

func TestDefaultProfileMatchesJSONSchema(t *testing.T) {
	schema := compileProfileSchema(t)
	if err := validateProfileWithSchema(schema, Default("test")); err != nil {
		t.Fatalf("default profile should validate against schema: %v", err)
	}
}

func TestValidateRejectsEmptyToolPreset(t *testing.T) {
	p := Default("test")
	p.Tools.Presets = []string{""}
	if err := p.Validate(); err == nil {
		t.Fatal("expected empty tool preset to fail")
	}
}

func TestValidateRejectsUnsupportedWorkspaceMode(t *testing.T) {
	p := Default("test")
	p.Workspace.Mode = "read-only"
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "workspace mode") {
		t.Fatalf("expected workspace mode validation failure, got %v", err)
	}
}

func TestValidateRejectsInvalidIdentityUser(t *testing.T) {
	for _, user := range []string{"Developer", "dev user", "1dev", "dev.user", strings.Repeat("a", 33)} {
		t.Run(user, func(t *testing.T) {
			p := Default("test")
			p.Identity.User = user
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "Linux username") {
				t.Fatalf("expected identity user validation failure for %q, got %v", user, err)
			}
		})
	}
}

func TestValidateRejectsVisibleProxyEnv(t *testing.T) {
	p := Default("test")
	p.Network.ProxyEnvVisible = true
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "proxyEnvVisible") {
		t.Fatalf("expected proxyEnvVisible validation failure, got %v", err)
	}
}

func TestValidateRejectsProxyEnvExposureConfig(t *testing.T) {
	p := Default("test")
	p.Env.Public["HTTPS_PROXY"] = "http://proxy"
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "env.public") {
		t.Fatalf("expected public proxy env validation failure, got %v", err)
	}
	p = Default("test")
	p.Env.Inherit = append(p.Env.Inherit, "all_proxy")
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "env.inherit") {
		t.Fatalf("expected inherit proxy env validation failure, got %v", err)
	}
}

func TestValidateRejectsHideoutRuntimeEnvExposureConfig(t *testing.T) {
	for _, name := range []string{"HIDEOUT_SECRET_DEFAULT_PROXY", "HIDEOUT_CAPABILITY_TOKEN", "HIDEOUT_BROKER_ENDPOINT"} {
		t.Run("public/"+name, func(t *testing.T) {
			p := Default("test")
			p.Env.Public[name] = "value"
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "hideout runtime env") {
				t.Fatalf("expected public hideout runtime env validation failure for %s, got %v", name, err)
			} else if strings.Contains(err.Error(), "HIDEOUT_") {
				t.Fatalf("validation error leaked hideout runtime env name: %v", err)
			}
		})
		t.Run("inherit/"+name, func(t *testing.T) {
			p := Default("test")
			p.Env.Inherit = append(p.Env.Inherit, name)
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "hideout runtime env") {
				t.Fatalf("expected inherit hideout runtime env validation failure for %s, got %v", name, err)
			} else if strings.Contains(err.Error(), "HIDEOUT_") {
				t.Fatalf("validation error leaked hideout runtime env name: %v", err)
			}
		})
	}
}

func TestValidateRejectsSyntheticIdentityEnvExposureConfig(t *testing.T) {
	for _, name := range []string{"HOME", "USER", "LOGNAME", "HOSTNAME", "TMPDIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "GIT_CONFIG_GLOBAL", "TZ", "LANG", "LC_ALL", "PATH"} {
		t.Run("public/"+name, func(t *testing.T) {
			p := Default("test")
			p.Env.Public[name] = "host-value"
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "env.public") {
				t.Fatalf("expected public synthetic env validation failure for %s, got %v", name, err)
			}
		})
		t.Run("inherit/"+name, func(t *testing.T) {
			p := Default("test")
			p.Env.Inherit = append(p.Env.Inherit, name)
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "env.inherit") {
				t.Fatalf("expected inherited synthetic env validation failure for %s, got %v", name, err)
			}
		})
	}
}

func TestValidateRejectsInvalidEnvNames(t *testing.T) {
	for _, name := range []string{"", "1BAD", "BAD-NAME", "BAD=NAME"} {
		t.Run("public/"+name, func(t *testing.T) {
			p := Default("test")
			p.Env.Public[name] = "value"
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "env.public") {
				t.Fatalf("expected invalid public env name failure for %q, got %v", name, err)
			}
		})
		t.Run("inherit/"+name, func(t *testing.T) {
			p := Default("test")
			p.Env.Inherit = append(p.Env.Inherit, name)
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "env.inherit") {
				t.Fatalf("expected invalid inherited env name failure for %q, got %v", name, err)
			}
		})
	}
}

func TestValidateRejectsTun2SocksWithoutProxySecretRef(t *testing.T) {
	p := Default("test")
	p.Network.Mode = "tun2socks"
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "proxySecretRef") {
		t.Fatalf("expected proxySecretRef validation failure, got %v", err)
	}
}

func TestValidateRejectsInvalidProxySecretRef(t *testing.T) {
	for _, ref := range []string{"Default-Proxy", "default_proxy", "default.proxy", "-default", "default-"} {
		t.Run(ref, func(t *testing.T) {
			p := Default("test")
			p.Network.ProxySecretRef = ref
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "proxySecretRef") {
				t.Fatalf("expected proxySecretRef validation failure for %q, got %v", ref, err)
			}
		})
	}
}

func TestValidateRejectsDuplicateProfileArrays(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Profile)
	}{
		{
			name: "env deny",
			edit: func(p *Profile) {
				p.Env.Deny = append(p.Env.Deny, "SSH_*")
			},
		},
		{
			name: "env inherit",
			edit: func(p *Profile) {
				p.Env.Inherit = append(p.Env.Inherit, "TERM")
			},
		},
		{
			name: "tool presets",
			edit: func(p *Profile) {
				p.Tools.Presets = []string{"base-dev", "base-dev"}
			},
		},
		{
			name: "max capabilities",
			edit: func(p *Profile) {
				p.Policy.MaxCapabilities = append(p.Policy.MaxCapabilities, "host.open")
			},
		},
		{
			name: "script entrypoints",
			edit: func(p *Profile) {
				p.Policy.ScriptRefs = []ScriptRef{{
					ID:          "script",
					Path:        "policy/script.js",
					Entrypoints: []string{"decideCommand", "decideCommand"},
				}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Default("test")
			tt.edit(&p)
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("expected duplicate validation failure, got %v", err)
			}
		})
	}
}

func TestSchemaRejectsDuplicateProfileArrays(t *testing.T) {
	schema := compileProfileSchema(t)
	tests := []struct {
		name string
		edit func(*Profile)
	}{
		{
			name: "env deny",
			edit: func(p *Profile) {
				p.Env.Deny = append(p.Env.Deny, "SSH_*")
			},
		},
		{
			name: "env inherit",
			edit: func(p *Profile) {
				p.Env.Inherit = append(p.Env.Inherit, "TERM")
			},
		},
		{
			name: "tool presets",
			edit: func(p *Profile) {
				p.Tools.Presets = []string{"base-dev", "base-dev"}
			},
		},
		{
			name: "max capabilities",
			edit: func(p *Profile) {
				p.Policy.MaxCapabilities = append(p.Policy.MaxCapabilities, "host.open")
			},
		},
		{
			name: "script entrypoints",
			edit: func(p *Profile) {
				p.Policy.ScriptRefs = []ScriptRef{{
					ID:          "script",
					Path:        "policy/script.js",
					Entrypoints: []string{"decideCommand", "decideCommand"},
				}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Default("test")
			tt.edit(&p)
			if err := validateProfileWithSchema(schema, p); err == nil {
				t.Fatal("expected schema to reject duplicate array values")
			}
		})
	}
}

func TestValidateRejectsInvalidMetadata(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
	}{
		{name: "profile id prefix", key: "profileId", val: "id_abcdef"},
		{name: "profile id hex", key: "profileId", val: "prf_ABCDEF"},
		{name: "identity id prefix", key: "identityId", val: "prf_abcdef"},
		{name: "previous identity id", key: "previousIdentityId", val: "id_not-hex"},
		{name: "identity archive id", key: "identityArchiveId", val: "archive_abcdef"},
		{name: "source profile id", key: "sourceProfileId", val: "id_abcdef"},
		{name: "source identity id", key: "sourceIdentityId", val: "prf_abcdef"},
		{name: "machine id length", key: "machineId", val: "abc123"},
		{name: "machine id newline", key: "machineId", val: "0123456789abcdef0123456789abcde\n"},
		{name: "lineage mode", key: "lineageMode", val: "copy"},
		{name: "last operation", key: "lastIdentityOperation", val: "clone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Default("test")
			p.Metadata = map[string]string{tt.key: tt.val}
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "metadata.") {
				t.Fatalf("expected metadata validation failure for %s=%q, got %v", tt.key, tt.val, err)
			}
		})
	}
}

func TestValidateRejectsUnsupportedHostOpenMode(t *testing.T) {
	p := Default("test")
	p.HostCapabilities.Open.Mode = "native"
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "hostCapabilities.open.mode") {
		t.Fatalf("expected host capability validation failure, got %v", err)
	}
}

func TestValidateHostFSProfilePolicy(t *testing.T) {
	valid := Default("test")
	valid.HostFS.Grants = []hostfs.Rule{{
		ID:       "hfs_public",
		HostPath: "/Users/alice/Downloads/public",
		Ops:      []hostfs.Op{hostfs.OpRead, hostfs.OpList},
		Scope:    hostfs.ScopeDir,
		Reason:   "shared public downloads",
	}}
	valid.HostFS.Deny = []hostfs.Rule{{
		ID:       "hfs_private",
		HostPath: "/Users/alice/Downloads/private.txt",
		Scope:    hostfs.ScopeExactFile,
		Reason:   "private file",
	}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid HostFS policy should pass: %v", err)
	}

	tests := []struct {
		name string
		edit func(*Profile)
		want string
	}{
		{
			name: "relative path",
			edit: func(p *Profile) {
				p.HostFS.Grants = []hostfs.Rule{{
					ID:       "hfs_relative",
					HostPath: "Downloads/file.txt",
					Ops:      []hostfs.Op{hostfs.OpRead},
					Scope:    hostfs.ScopeExactFile,
					Reason:   "relative",
				}}
			},
			want: "absolute",
		},
		{
			name: "hard deny",
			edit: func(p *Profile) {
				p.HostFS.Grants = []hostfs.Rule{{
					ID:       "hfs_ssh",
					HostPath: "/Users/alice/.ssh/id_ed25519",
					Ops:      []hostfs.Op{hostfs.OpRead},
					Scope:    hostfs.ScopeExactFile,
					Reason:   "ssh",
				}}
			},
			want: "hard-deny",
		},
		{
			name: "write op",
			edit: func(p *Profile) {
				p.HostFS.Grants = []hostfs.Rule{{
					ID:       "hfs_write",
					HostPath: "/Users/alice/Downloads/file.txt",
					Ops:      []hostfs.Op{hostfs.OpWrite},
					Scope:    hostfs.ScopeExactFile,
					Reason:   "write",
				}}
			},
			want: "write-class",
		},
		{
			name: "broad recursive",
			edit: func(p *Profile) {
				p.HostFS.Grants = []hostfs.Rule{{
					ID:       "hfs_broad",
					HostPath: "/Users/alice",
					Ops:      []hostfs.Op{hostfs.OpRead},
					Scope:    hostfs.ScopeRecursiveDir,
					Reason:   "home",
				}}
			},
			want: "broad HostFS grant",
		},
		{
			name: "missing id",
			edit: func(p *Profile) {
				p.HostFS.Grants = []hostfs.Rule{{
					HostPath: "/Users/alice/Downloads/file.txt",
					Ops:      []hostfs.Op{hostfs.OpRead},
					Scope:    hostfs.ScopeExactFile,
					Reason:   "missing id",
				}}
			},
			want: "id is required",
		},
		{
			name: "duplicate id across grant and deny",
			edit: func(p *Profile) {
				p.HostFS.Grants = []hostfs.Rule{{
					ID:       "hfs_duplicate",
					HostPath: "/Users/alice/Downloads/file.txt",
					Ops:      []hostfs.Op{hostfs.OpRead},
					Scope:    hostfs.ScopeExactFile,
					Reason:   "grant",
				}}
				p.HostFS.Deny = []hostfs.Rule{{
					ID:       "hfs_duplicate",
					HostPath: "/Users/alice/Downloads/private.txt",
					Ops:      []hostfs.Op{hostfs.OpRead},
					Scope:    hostfs.ScopeExactFile,
					Reason:   "deny",
				}}
			},
			want: "duplicates",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Default("test")
			tt.edit(&p)
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected HostFS validation failure containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestValidateRejectsUnsupportedPolicyEntrypoint(t *testing.T) {
	p := Default("test")
	p.Policy.ScriptRefs = []ScriptRef{{
		ID:          "bad",
		Path:        "policy/bad.js",
		Entrypoints: []string{"brokerDecide"},
	}}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "entrypoint") {
		t.Fatalf("expected script entrypoint validation failure, got %v", err)
	}
}

func TestValidateRejectsDuplicatePolicyScriptIDs(t *testing.T) {
	p := Default("test")
	p.Policy.ScriptRefs = []ScriptRef{
		{
			ID:          "command-policy",
			Path:        "policy/command.js",
			Entrypoints: []string{"decideCommand"},
		},
		{
			ID:          "command-policy",
			Path:        "policy/redact.js",
			Entrypoints: []string{"redactAudit"},
		},
	}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("expected duplicate script id validation failure, got %v", err)
	}
}

func TestValidateRejectsTooManyPolicyScriptRefs(t *testing.T) {
	p := Default("test")
	for i := 0; i < MaxPolicyScriptRefs+1; i++ {
		p.Policy.ScriptRefs = append(p.Policy.ScriptRefs, ScriptRef{
			ID:          fmt.Sprintf("script-%02d", i),
			Path:        fmt.Sprintf("policy/script-%02d.js", i),
			Entrypoints: []string{"decideCommand"},
		})
	}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "policy.scriptRefs") {
		t.Fatalf("expected script ref count validation failure, got %v", err)
	}
}

func TestValidateRejectsPolicyScriptPathsOutsidePolicyDir(t *testing.T) {
	for _, path := range []string{
		"/tmp/policy.js",
		"../policy.js",
		"policy/../policy.js",
		"policy/./bad.js",
		"policy//bad.js",
		"policy/a//bad.js",
		"workspace/policy.js",
		`policy\bad.js`,
	} {
		t.Run(path, func(t *testing.T) {
			p := Default("test")
			p.Policy.ScriptRefs = []ScriptRef{{
				ID:          "bad",
				Path:        path,
				Entrypoints: []string{"decideCommand"},
			}}
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "policy script bad path") {
				t.Fatalf("expected script path validation failure for %q, got %v", path, err)
			}
		})
	}
}

func TestSchemaRejectsUnsupportedProfileShape(t *testing.T) {
	schema := compileProfileSchema(t)
	p := Default("test")
	p.CommandProxy.Commands["shell"] = CommandProxyCommand{Route: "host-broker", Action: "host.open"}
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject unsupported command proxy")
	}
	p = Default("test")
	p.Network.Mode = "tun2socks"
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject tun2socks without proxySecretRef")
	}
	p = Default(".")
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject dot profile name")
	}
	p = Default("test")
	p.Identity.User = "Developer"
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject invalid identity user")
	}
	p = Default("test")
	p.Env.Public["HTTPS_PROXY"] = "http://proxy"
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject public proxy env")
	}
	p = Default("test")
	p.Env.Inherit = append(p.Env.Inherit, "all_proxy")
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject inherited proxy env")
	}
	p = Default("test")
	p.Env.Public["HIDEOUT_SECRET_DEFAULT_PROXY"] = "http://proxy"
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject public hideout runtime env")
	}
	p = Default("test")
	p.Env.Inherit = append(p.Env.Inherit, "HIDEOUT_CAPABILITY_TOKEN")
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject inherited hideout runtime env")
	}
	p = Default("test")
	p.Env.Public["HOME"] = "/real/home"
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject public synthetic identity env")
	}
	p = Default("test")
	p.Env.Inherit = append(p.Env.Inherit, "PATH")
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject inherited synthetic identity env")
	}
	p = Default("test")
	p.Env.Public["GIT_CONFIG_GLOBAL"] = "/real/home/.gitconfig"
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject public synthetic git config env")
	}
	p = Default("test")
	p.Env.Public["BAD=NAME"] = "value"
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject invalid public env name")
	}
	p = Default("test")
	p.Env.Inherit = append(p.Env.Inherit, "1BAD")
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject invalid inherited env name")
	}
	p = Default("test")
	p.Network.ProxySecretRef = "default_proxy"
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject invalid proxy secret ref")
	}
	p = Default("test")
	p.Metadata = map[string]string{"machineId": "bad\nmachine"}
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject invalid metadata machineId")
	}
	p = Default("test")
	p.Metadata = map[string]string{"sourceProfileId": "id_abcdef"}
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject invalid sourceProfileId")
	}
	p = Default("test")
	p.Metadata = map[string]string{"sourceIdentityId": "prf_abcdef"}
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject invalid sourceIdentityId")
	}
	p = Default("test")
	p.Policy.ScriptRefs = []ScriptRef{{
		ID:          "bad",
		Path:        "../policy.js",
		Entrypoints: []string{"decideCommand"},
	}}
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject policy script path escape")
	}
	p = Default("test")
	p.Policy.ScriptRefs = []ScriptRef{{
		ID:          "bad",
		Path:        "policy/",
		Entrypoints: []string{"decideCommand"},
	}}
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject policy script directory path")
	}
	p = Default("test")
	p.Policy.ScriptRefs = []ScriptRef{{
		ID:          "bad",
		Path:        "policy//bad.js",
		Entrypoints: []string{"decideCommand"},
	}}
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject non-normalized policy script path")
	}
	p = Default("test")
	p.HostFS.Grants = []hostfs.Rule{{
		HostPath: "Downloads/file.txt",
		Ops:      []hostfs.Op{hostfs.OpRead},
		Scope:    hostfs.ScopeExactFile,
		Reason:   "relative",
	}}
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject relative HostFS host path")
	}
	p = Default("test")
	p.HostFS.Grants = []hostfs.Rule{{
		HostPath: "/Users/alice/Downloads/file.txt",
		Ops:      []hostfs.Op{hostfs.OpWrite},
		Scope:    hostfs.ScopeExactFile,
		Reason:   "write",
	}}
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject write-class HostFS op")
	}
	p = Default("test")
	p.HostFS.Grants = []hostfs.Rule{{
		HostPath: "/Users/alice/Downloads/file.txt",
		Ops:      []hostfs.Op{hostfs.OpRead},
		Scope:    hostfs.ScopeExactFile,
		Subject:  hostfs.SubjectEnvironment,
		Reason:   "bad subject",
	}}
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject non-profile HostFS subject in profile")
	}
	p = Default("test")
	p.Policy.ScriptRefs = []ScriptRef{{
		ID:          "bad",
		Path:        "policy/./bad.js",
		Entrypoints: []string{"decideCommand"},
	}}
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject dot-segment policy script path")
	}
	p = Default("test")
	for i := 0; i < MaxPolicyScriptRefs+1; i++ {
		p.Policy.ScriptRefs = append(p.Policy.ScriptRefs, ScriptRef{
			ID:          fmt.Sprintf("script-%02d", i),
			Path:        fmt.Sprintf("policy/script-%02d.js", i),
			Entrypoints: []string{"decideCommand"},
		})
	}
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject too many policy script refs")
	}
	p = Default("test")
	duplicateRef := ScriptRef{
		ID:          "duplicate",
		Path:        "policy/duplicate.js",
		Entrypoints: []string{"decideCommand"},
	}
	p.Policy.ScriptRefs = []ScriptRef{duplicateRef, duplicateRef}
	if err := validateProfileWithSchema(schema, p); err == nil {
		t.Fatal("expected schema to reject duplicate policy script refs")
	}
	raw := map[string]any{}
	data, err := json.Marshal(Default("test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["storage"] = map[string]string{"home": "persistent"}
	if err := validateProfileWithSchema(schema, raw); err == nil {
		t.Fatal("expected schema to reject unsupported storage field")
	}
}

func TestLoadRejectsUnknownProfileFields(t *testing.T) {
	store := Store{Root: t.TempDir()}
	p := Default("test")
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.ProfilePath("test"))
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"audit": {`), []byte(`"unknownField": true, "audit": {`), 1)
	if err := os.WriteFile(store.ProfilePath("test"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("test"); err == nil || !strings.Contains(err.Error(), "unknownField") {
		t.Fatalf("expected unknown field load failure, got %v", err)
	}
}

func TestLoadRejectsInvalidProfileNameBeforePathUse(t *testing.T) {
	store := Store{Root: t.TempDir()}
	for _, name := range []string{"../outside", "bad/name", `bad\name`, "bad name"} {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Load(name); err == nil || !strings.Contains(err.Error(), "invalid profile name") {
				t.Fatalf("expected invalid profile name failure for %q, got %v", name, err)
			}
		})
	}
}

func TestValidateRejectsProfileNameOutsideSchemaPattern(t *testing.T) {
	for _, name := range []string{".", "..", "bad/name", `bad\name`, "bad name"} {
		t.Run(name, func(t *testing.T) {
			p := Default(name)
			if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "invalid profile name") {
				t.Fatalf("expected invalid profile name failure for %q, got %v", name, err)
			}
		})
	}
}

func TestValidateRejectsInvalidHostname(t *testing.T) {
	p := Default("test")
	p.Identity.Hostname = "alice laptop"
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "hostname") {
		t.Fatalf("expected hostname validation failure, got %v", err)
	}
}

func TestValidateRejectsUnsupportedCommandProxy(t *testing.T) {
	p := Default("test")
	p.CommandProxy.Commands["shell"] = CommandProxyCommand{
		Route:  "host-broker",
		Action: "host.exec",
	}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported command proxy") {
		t.Fatalf("expected unsupported command proxy failure, got %v", err)
	}
}

func TestValidateRejectsCommandProxyHostExec(t *testing.T) {
	p := Default("test")
	p.CommandProxy.Commands["open"] = CommandProxyCommand{
		Route:  "host-broker",
		Action: "host.exec",
	}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "action must be host.open") {
		t.Fatalf("expected host.open clamp failure, got %v", err)
	}
}

func TestClonePolicyRegeneratesIdentityAndDoesNotCopyGeneratedState(t *testing.T) {
	store := Store{Root: t.TempDir()}
	source := Default("source")
	source.Git.UserEmail = "source@example.com"
	source.Policy.ScriptRefs = []ScriptRef{{
		ID:          "command-policy",
		Path:        "policy/nested/command.js",
		Entrypoints: []string{"decideCommand"},
	}}
	if err := store.Save(source); err != nil {
		t.Fatal(err)
	}
	mustWriteProfileTest(t, filepath.Join(store.ProfileDir("source"), "policy", "nested", "command.js"), "function decideCommand() {}\n")
	sourceOnlyIdentityFiles := map[string]string{
		"home":    "token.txt",
		"config":  filepath.Join("app", "config.json"),
		"cache":   filepath.Join("sdk", "cache.db"),
		"data":    filepath.Join("app", "state.json"),
		"browser": "cookie",
		"machine": "source-only-machine-id",
	}
	for dir, rel := range sourceOnlyIdentityFiles {
		mustWriteProfileTest(t, filepath.Join(store.ProfileDir("source"), dir, rel), "source identity material\n")
	}
	cloned, err := store.ClonePolicy("source", "target")
	if err != nil {
		t.Fatalf("ClonePolicy: %v", err)
	}
	if cloned.Name != "target" {
		t.Fatalf("clone name=%q", cloned.Name)
	}
	loadedSource, err := store.Load("source")
	if err != nil {
		t.Fatalf("load source: %v", err)
	}
	loadedTarget, err := store.Load("target")
	if err != nil {
		t.Fatalf("load target: %v", err)
	}
	if loadedTarget.Git.UserEmail != "source@example.com" {
		t.Fatalf("policy field was not copied: %+v", loadedTarget.Git)
	}
	if len(loadedTarget.Policy.ScriptRefs) != 1 || loadedTarget.Policy.ScriptRefs[0].Path != "policy/nested/command.js" {
		t.Fatalf("policy script refs were not copied: %+v", loadedTarget.Policy.ScriptRefs)
	}
	scriptData, err := os.ReadFile(filepath.Join(store.ProfileDir("target"), "policy", "nested", "command.js"))
	if err != nil {
		t.Fatalf("clone should copy policy script file: %v", err)
	}
	if string(scriptData) != "function decideCommand() {}\n" {
		t.Fatalf("cloned policy script mismatch: %q", scriptData)
	}
	if loadedTarget.Metadata["lineageMode"] != "policy-clone" || loadedTarget.Metadata["createdFrom"] != "source" {
		t.Fatalf("clone lineage missing: %+v", loadedTarget.Metadata)
	}
	if loadedTarget.Metadata["profileId"] == loadedSource.Metadata["profileId"] {
		t.Fatalf("clone reused profileId: source=%+v target=%+v", loadedSource.Metadata, loadedTarget.Metadata)
	}
	if loadedTarget.Metadata["identityId"] == loadedSource.Metadata["identityId"] {
		t.Fatalf("clone reused identityId: source=%+v target=%+v", loadedSource.Metadata, loadedTarget.Metadata)
	}
	if loadedTarget.Metadata["machineId"] == "" || loadedTarget.Metadata["machineId"] == loadedSource.Metadata["machineId"] {
		t.Fatalf("clone reused or missed machineId: source=%+v target=%+v", loadedSource.Metadata, loadedTarget.Metadata)
	}
	for dir, rel := range sourceOnlyIdentityFiles {
		if _, err := os.Stat(filepath.Join(store.ProfileDir("target"), dir, rel)); !os.IsNotExist(err) {
			t.Fatalf("clone copied source %s state %s; err=%v", dir, rel, err)
		}
	}
	identityData, err := os.ReadFile(filepath.Join(store.ProfileDir("target"), "identity.json"))
	if err != nil {
		t.Fatalf("read target identity metadata: %v", err)
	}
	var identity map[string]string
	if err := json.Unmarshal(identityData, &identity); err != nil {
		t.Fatalf("decode target identity metadata: %v", err)
	}
	if identity["identityId"] != loadedTarget.Metadata["identityId"] || identity["machineId"] != loadedTarget.Metadata["machineId"] {
		t.Fatalf("identity.json mismatch: %+v profile=%+v", identity, loadedTarget.Metadata)
	}
}

func TestEphemeralIdentityProfileRegeneratesIdentityAndKeepsPolicy(t *testing.T) {
	store := Store{Root: t.TempDir()}
	source := Default("source")
	source.Git.UserName = "Source Dev"
	source.Git.UserEmail = "source@example.com"
	source.Env.Public["NODE_ENV"] = "test"
	source.Tools.Presets = []string{"base-dev"}
	source.Policy.ScriptRefs = []ScriptRef{{
		ID:          "command-policy",
		Path:        "policy/command.js",
		Entrypoints: []string{"decideCommand", "redactAudit"},
	}}
	if err := store.Save(source); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load("source")
	if err != nil {
		t.Fatalf("load source: %v", err)
	}
	sourceIdentityID := loaded.Metadata["identityId"]
	sourceMachineID := loaded.Metadata["machineId"]

	ephemeral, err := EphemeralIdentityProfile(loaded)
	if err != nil {
		t.Fatalf("EphemeralIdentityProfile: %v", err)
	}
	if ephemeral.Name != loaded.Name {
		t.Fatalf("ephemeral profile changed selected policy name: %q", ephemeral.Name)
	}
	if ephemeral.Metadata["identityId"] == "" || ephemeral.Metadata["identityId"] == sourceIdentityID {
		t.Fatalf("ephemeral identityId was not regenerated: source=%+v ephemeral=%+v", loaded.Metadata, ephemeral.Metadata)
	}
	if ephemeral.Metadata["machineId"] == "" || ephemeral.Metadata["machineId"] == sourceMachineID {
		t.Fatalf("ephemeral machineId was not regenerated: source=%+v ephemeral=%+v", loaded.Metadata, ephemeral.Metadata)
	}
	if ephemeral.Metadata["lineageMode"] != "session-fork" ||
		ephemeral.Metadata["createdFrom"] != "source" ||
		ephemeral.Metadata["sourceIdentityId"] != sourceIdentityID ||
		ephemeral.Metadata["identityChangedAt"] == "" {
		t.Fatalf("ephemeral lineage metadata missing: %+v", ephemeral.Metadata)
	}
	if ephemeral.Git != loaded.Git ||
		ephemeral.Identity != loaded.Identity ||
		ephemeral.Workspace != loaded.Workspace ||
		ephemeral.Network != loaded.Network {
		t.Fatalf("ephemeral profile should keep policy fields: source=%+v ephemeral=%+v", loaded, ephemeral)
	}
	if len(ephemeral.Tools.Presets) != len(loaded.Tools.Presets) ||
		ephemeral.Tools.Presets[0] != "base-dev" {
		t.Fatalf("ephemeral profile lost tool presets: %+v", ephemeral.Tools.Presets)
	}
	if len(ephemeral.Policy.ScriptRefs) != 1 || ephemeral.Policy.ScriptRefs[0].Path != "policy/command.js" {
		t.Fatalf("ephemeral profile lost script refs: %+v", ephemeral.Policy.ScriptRefs)
	}
	if ephemeral.CommandProxy.Commands["open"].Action != "host.open" ||
		ephemeral.CommandProxy.Commands["xdg-open"].Route != "host-broker" {
		t.Fatalf("ephemeral profile lost command proxy rules: %+v", ephemeral.CommandProxy.Commands)
	}

	ephemeral.Env.Public["NODE_ENV"] = "changed"
	if loaded.Env.Public["NODE_ENV"] != "test" {
		t.Fatalf("ephemeral profile mutated source env map: %+v", loaded.Env.Public)
	}
	if loaded.Metadata["identityId"] != sourceIdentityID || loaded.Metadata["machineId"] != sourceMachineID {
		t.Fatalf("ephemeral profile mutated source metadata: source=%+v", loaded.Metadata)
	}
	reloaded, err := store.Load("source")
	if err != nil {
		t.Fatalf("reload source: %v", err)
	}
	if reloaded.Metadata["identityId"] != sourceIdentityID || reloaded.Metadata["machineId"] != sourceMachineID {
		t.Fatalf("ephemeral profile wrote back to source profile: before=%s/%s after=%+v", sourceIdentityID, sourceMachineID, reloaded.Metadata)
	}
}

func TestClonePolicyRejectsExistingTargetDirectoryWithoutProfile(t *testing.T) {
	store := Store{Root: t.TempDir()}
	if err := store.Save(Default("source")); err != nil {
		t.Fatal(err)
	}
	staleIdentity := filepath.Join(store.ProfileDir("target"), "home", "token.txt")
	mustWriteProfileTest(t, staleIdentity, "stale")
	if _, err := store.ClonePolicy("source", "target"); err == nil {
		t.Fatal("expected clone to reject existing target profile directory")
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("target"), "profile.json")); !os.IsNotExist(err) {
		t.Fatalf("clone should not materialize profile into existing target dir; err=%v", err)
	}
	data, err := os.ReadFile(staleIdentity)
	if err != nil {
		t.Fatalf("stale identity marker should be untouched for user review: %v", err)
	}
	if string(data) != "stale" {
		t.Fatalf("stale identity marker changed: %q", data)
	}
}

func TestClonePolicyRollsBackTargetWhenPolicyCopyFails(t *testing.T) {
	store := Store{Root: t.TempDir()}
	if err := store.Save(Default("source")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp/outside-policy.js", filepath.Join(store.ProfileDir("source"), "policy", "bad.js")); err != nil {
		t.Fatal(err)
	}

	_, err := store.ClonePolicy("source", "target")
	if err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("expected policy symlink failure, got %v", err)
	}
	if _, err := os.Stat(store.ProfileDir("target")); !os.IsNotExist(err) {
		t.Fatalf("failed clone should roll back target profile dir; err=%v", err)
	}
	if _, err := os.Stat(store.ProfileDir("source")); err != nil {
		t.Fatalf("source profile should remain intact: %v", err)
	}
}

func TestRotateIdentityArchivesGeneratedStateAndKeepsPolicy(t *testing.T) {
	store := Store{Root: t.TempDir()}
	p := Default("test")
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	before, err := store.Load("test")
	if err != nil {
		t.Fatal(err)
	}
	oldIdentityID := before.Metadata["identityId"]
	mustWriteProfileTest(t, filepath.Join(store.ProfileDir("test"), "home", "token.txt"), "secret")
	mustWriteProfileTest(t, filepath.Join(store.ProfileDir("test"), "browser", "cookie"), "cookie")
	mustWriteProfileTest(t, filepath.Join(store.ProfileDir("test"), "policy", "command.js"), "policy")

	rotated, err := store.RotateIdentity("test")
	if err != nil {
		t.Fatalf("RotateIdentity: %v", err)
	}
	if rotated.Metadata["profileId"] != before.Metadata["profileId"] {
		t.Fatalf("rotate changed profileId: before=%+v after=%+v", before.Metadata, rotated.Metadata)
	}
	if rotated.Metadata["identityId"] == oldIdentityID || rotated.Metadata["previousIdentityId"] != oldIdentityID {
		t.Fatalf("rotate identity metadata mismatch: before=%+v after=%+v", before.Metadata, rotated.Metadata)
	}
	if rotated.Metadata["machineId"] == "" || rotated.Metadata["machineId"] == before.Metadata["machineId"] {
		t.Fatalf("rotate machineId mismatch: before=%+v after=%+v", before.Metadata, rotated.Metadata)
	}
	if rotated.Metadata["lastIdentityOperation"] != "rotate" {
		t.Fatalf("rotate operation missing: %+v", rotated.Metadata)
	}
	if rotated.Metadata["identityArchiveId"] != oldIdentityID {
		t.Fatalf("rotate should record archive id, not a path: %+v", rotated.Metadata)
	}
	if strings.Contains(rotated.Metadata["identityArchive"], store.ProfileDir("test")) ||
		strings.Contains(rotated.Metadata["identityArchiveId"], store.ProfileDir("test")) {
		t.Fatalf("rotate metadata leaked archive path: %+v", rotated.Metadata)
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("test"), "home", "token.txt")); !os.IsNotExist(err) {
		t.Fatalf("new home should not contain old token; err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("test"), "browser", "cookie")); !os.IsNotExist(err) {
		t.Fatalf("new browser should not contain old cookie; err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("test"), "policy", "command.js")); err != nil {
		t.Fatalf("policy file should be kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("test"), "identity-archive", oldIdentityID, "home", "token.txt")); err != nil {
		t.Fatalf("old home should be archived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("test"), "identity-archive", oldIdentityID, "browser", "cookie")); err != nil {
		t.Fatalf("old browser should be archived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("test"), "identity-archive", oldIdentityID, "machine", "machine-id")); err != nil {
		t.Fatalf("old machine-id should be archived: %v", err)
	}
}

func TestResetIdentityDeletesGeneratedStateAndKeepsPolicy(t *testing.T) {
	store := Store{Root: t.TempDir()}
	p := Default("test")
	if err := store.Save(p); err != nil {
		t.Fatal(err)
	}
	before, err := store.Load("test")
	if err != nil {
		t.Fatal(err)
	}
	oldIdentityID := before.Metadata["identityId"]
	mustWriteProfileTest(t, filepath.Join(store.ProfileDir("test"), "home", "token.txt"), "secret")
	mustWriteProfileTest(t, filepath.Join(store.ProfileDir("test"), "browser", "cookie"), "cookie")
	mustWriteProfileTest(t, filepath.Join(store.ProfileDir("test"), "policy", "command.js"), "policy")

	reset, err := store.ResetIdentity("test")
	if err != nil {
		t.Fatalf("ResetIdentity: %v", err)
	}
	if reset.Metadata["profileId"] != before.Metadata["profileId"] {
		t.Fatalf("reset changed profileId: before=%+v after=%+v", before.Metadata, reset.Metadata)
	}
	if reset.Metadata["identityId"] == oldIdentityID || reset.Metadata["previousIdentityId"] != oldIdentityID {
		t.Fatalf("reset identity metadata mismatch: before=%+v after=%+v", before.Metadata, reset.Metadata)
	}
	if reset.Metadata["machineId"] == "" || reset.Metadata["machineId"] == before.Metadata["machineId"] {
		t.Fatalf("reset machineId mismatch: before=%+v after=%+v", before.Metadata, reset.Metadata)
	}
	if reset.Metadata["lastIdentityOperation"] != "reset" {
		t.Fatalf("reset operation missing: %+v", reset.Metadata)
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("test"), "home", "token.txt")); !os.IsNotExist(err) {
		t.Fatalf("new home should not contain old token; err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("test"), "browser", "cookie")); !os.IsNotExist(err) {
		t.Fatalf("new browser should not contain old cookie; err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("test"), "policy", "command.js")); err != nil {
		t.Fatalf("policy file should be kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.ProfileDir("test"), "identity-archive", oldIdentityID)); !os.IsNotExist(err) {
		t.Fatalf("reset should not archive old identity state; err=%v", err)
	}
}

func TestResetIdentityClearsStaleRotateArchiveMetadata(t *testing.T) {
	store := Store{Root: t.TempDir()}
	if err := store.Save(Default("test")); err != nil {
		t.Fatal(err)
	}
	rotated, err := store.RotateIdentity("test")
	if err != nil {
		t.Fatalf("RotateIdentity: %v", err)
	}
	if rotated.Metadata["identityArchiveId"] == "" || rotated.Metadata["identityRotatedAt"] == "" {
		t.Fatalf("rotate should create archive metadata for test setup: %+v", rotated.Metadata)
	}

	reset, err := store.ResetIdentity("test")
	if err != nil {
		t.Fatalf("ResetIdentity: %v", err)
	}
	if reset.Metadata["lastIdentityOperation"] != "reset" ||
		reset.Metadata["previousIdentityId"] != rotated.Metadata["identityId"] ||
		reset.Metadata["identityResetAt"] == "" {
		t.Fatalf("reset metadata mismatch: rotated=%+v reset=%+v", rotated.Metadata, reset.Metadata)
	}
	for _, key := range []string{"identityArchive", "identityArchiveId", "identityRotatedAt"} {
		if reset.Metadata[key] != "" {
			t.Fatalf("reset should clear stale rotate metadata %s: %+v", key, reset.Metadata)
		}
	}
	reloaded, err := store.Load("test")
	if err != nil {
		t.Fatalf("reload reset profile: %v", err)
	}
	for _, key := range []string{"identityArchive", "identityArchiveId", "identityRotatedAt"} {
		if reloaded.Metadata[key] != "" {
			t.Fatalf("saved reset profile retained stale rotate metadata %s: %+v", key, reloaded.Metadata)
		}
	}
}

func mustWriteProfileTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func compileProfileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	path := filepath.Join("..", "..", "schemas", "profile.schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read profile schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode profile schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("profile.schema.json", doc); err != nil {
		t.Fatalf("add profile schema: %v", err)
	}
	schema, err := compiler.Compile("profile.schema.json")
	if err != nil {
		t.Fatalf("compile profile schema: %v", err)
	}
	return schema
}

func validateProfileWithSchema(schema *jsonschema.Schema, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return schema.Validate(doc)
}
