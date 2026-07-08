package cmdadapter

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	DefaultEntrypoint         = "decideCommandAdapter"
	BuiltinRootSensitiveID    = "root-sensitive"
	BuiltinRootSensitiveKey   = "root-sensitive"
	CapabilityGuestPrivPlan   = "guest.privilege.plan"
	CapabilityHostFSWritePlan = "host.fs.write.plan"
)

type Runtime struct {
	Adapters  map[string]RuntimeAdapter
	ByCommand map[string]string
}

type SourceResolver interface {
	ResolveCommandAdapter(profileDir, id string, adapter profile.CommandAdapter) (ResolvedSource, error)
}

type ResolvedSource struct {
	Source string
	Digest string
	Path   string
}

type RuntimeAdapter struct {
	ID                          string
	Path                        string
	Digest                      string
	Entrypoint                  string
	Commands                    []string
	AllowedProposalCapabilities []string
	Description                 string
	Builtin                     string
	RootSensitive               bool
	Source                      string
	PackID                      string
	PackRevisionID              string
	PackAdapterID               string
	PackLockDigest              string
}

func Compile(p profile.Profile, profileDir string) (Runtime, error) {
	return CompileWithResolver(p, profileDir, nil)
}

func CompileWithResolver(p profile.Profile, profileDir string, resolver SourceResolver) (Runtime, error) {
	rt := Runtime{
		Adapters:  map[string]RuntimeAdapter{},
		ByCommand: map[string]string{},
	}
	for id, adapter := range p.CommandAdapters.Adapters {
		if !adapter.Enabled {
			continue
		}
		compiled, err := CompileAdapterWithResolver(profileDir, id, adapter, resolver)
		if err != nil {
			return Runtime{}, err
		}
		rt.Adapters[id] = compiled
		for _, command := range compiled.Commands {
			if existing := rt.ByCommand[command]; existing != "" {
				return Runtime{}, fmt.Errorf("command adapter command %q is already owned by adapter %q", command, existing)
			}
			rt.ByCommand[command] = id
		}
	}
	return rt, nil
}

func CompileAdapter(profileDir, id string, adapter profile.CommandAdapter) (RuntimeAdapter, error) {
	return CompileAdapterWithResolver(profileDir, id, adapter, nil)
}

func CompileAdapterWithResolver(profileDir, id string, adapter profile.CommandAdapter, resolver SourceResolver) (RuntimeAdapter, error) {
	entrypoint := adapter.Entrypoint
	if entrypoint == "" {
		entrypoint = DefaultEntrypoint
	}
	out := RuntimeAdapter{
		ID:                          id,
		Path:                        adapter.Path,
		Digest:                      adapter.Digest,
		Entrypoint:                  entrypoint,
		Commands:                    sortedCopy(adapter.Commands),
		AllowedProposalCapabilities: sortedCopy(adapter.AllowedProposalCapabilities),
		Description:                 adapter.Description,
		Builtin:                     adapter.Builtin,
		RootSensitive:               adapter.Builtin == BuiltinRootSensitiveKey || id == BuiltinRootSensitiveID,
		PackID:                      adapter.PackID,
		PackRevisionID:              adapter.PackRevisionID,
		PackAdapterID:               adapter.PackAdapterID,
		PackLockDigest:              adapter.PackLockDigest,
	}
	source, digest, path, err := ResolveSourceWithResolver(profileDir, out, adapter, resolver)
	if err != nil {
		return RuntimeAdapter{}, err
	}
	out.Source = source
	if path != "" {
		out.Path = path
	}
	if out.Digest == "" {
		out.Digest = digest
	}
	if digest != out.Digest {
		return RuntimeAdapter{}, fmt.Errorf("command adapter %s digest mismatch: expected %s got %s", id, out.Digest, digest)
	}
	return out, nil
}

func ResolveSource(profileDir string, adapter RuntimeAdapter) (string, string, error) {
	source, digest, _, err := ResolveSourceWithResolver(profileDir, adapter, profile.CommandAdapter{}, nil)
	return source, digest, err
}

func ResolveSourceWithResolver(profileDir string, adapter RuntimeAdapter, original profile.CommandAdapter, resolver SourceResolver) (string, string, string, error) {
	if adapter.PackID != "" || adapter.PackRevisionID != "" || adapter.PackAdapterID != "" {
		if resolver == nil {
			return "", "", "", fmt.Errorf("command adapter %s is pack-backed but no adapter pack resolver is configured", adapter.ID)
		}
		resolved, err := resolver.ResolveCommandAdapter(profileDir, adapter.ID, original)
		if err != nil {
			return "", "", "", err
		}
		return resolved.Source, resolved.Digest, resolved.Path, nil
	}
	var data []byte
	switch adapter.Builtin {
	case "":
		path := adapter.Path
		if path == "" {
			return "", "", "", fmt.Errorf("command adapter %s path is required", adapter.ID)
		}
		if !filepath.IsAbs(path) {
			if profileDir == "" {
				return "", "", "", fmt.Errorf("command adapter %s profile directory is required", adapter.ID)
			}
			path = filepath.Join(profileDir, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", "", "", fmt.Errorf("command adapter %s: %w", adapter.ID, err)
		}
		if !info.Mode().IsRegular() {
			return "", "", "", fmt.Errorf("command adapter %s path must be a regular file", adapter.ID)
		}
		data, err = os.ReadFile(path)
		if err != nil {
			return "", "", "", fmt.Errorf("command adapter %s: %w", adapter.ID, err)
		}
		adapter.Path = path
	case BuiltinRootSensitiveKey:
		data = []byte(BuiltinRootSensitiveSource)
	default:
		return "", "", "", fmt.Errorf("command adapter %s builtin %q is unsupported", adapter.ID, adapter.Builtin)
	}
	return string(data), DigestBytes(data), adapter.Path, nil
}

func DigestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func BuiltinRootSensitiveProfileAdapter() profile.CommandAdapter {
	source := []byte(BuiltinRootSensitiveSource)
	return profile.CommandAdapter{
		Enabled:    true,
		Digest:     DigestBytes(source),
		Builtin:    BuiltinRootSensitiveKey,
		Entrypoint: DefaultEntrypoint,
		Commands: []string{
			"sudo", "su", "doas",
			"apt", "apt-get", "dnf", "yum", "apk", "pacman", "brew",
			"mount", "umount",
			"iptables", "nft", "ip", "route", "pfctl",
			"resolvectl", "systemd-resolve",
			"systemctl", "service", "launchctl",
			"sysctl", "modprobe", "kmod",
		},
		AllowedProposalCapabilities: []string{CapabilityGuestPrivPlan},
		Description:                 "Built-in root-sensitive intent capture",
	}
}

func AdapterForCommand(rt Runtime, command string) (RuntimeAdapter, bool) {
	id := rt.ByCommand[filepath.Base(command)]
	if id == "" {
		return RuntimeAdapter{}, false
	}
	adapter, ok := rt.Adapters[id]
	return adapter, ok
}

func ValidateRuntime(rt Runtime) error {
	if rt.Adapters == nil || rt.ByCommand == nil {
		return errors.New("command adapter runtime is incomplete")
	}
	return nil
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func HasCapability(adapter RuntimeAdapter, capability string) bool {
	for _, allowed := range adapter.AllowedProposalCapabilities {
		if allowed == capability {
			return true
		}
	}
	return false
}

func AdapterIDs(rt Runtime) []string {
	ids := make([]string, 0, len(rt.Adapters))
	for id := range rt.Adapters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func Commands(rt Runtime) []string {
	commands := make([]string, 0, len(rt.ByCommand))
	for command := range rt.ByCommand {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	return commands
}

func NormalizeSeparationStatus(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "intent-only", "enforced-009", "degraded-009", "unknown":
		return value
	default:
		return "unknown"
	}
}
