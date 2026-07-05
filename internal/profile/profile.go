package profile

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	slashpath "path"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/secrets"
)

const (
	SchemaVersion       = "hideout.profile/v1"
	MaxPolicyScriptRefs = 16
)

type Profile struct {
	SchemaVersion    string            `json:"schemaVersion"`
	Name             string            `json:"name"`
	Identity         Identity          `json:"identity"`
	Workspace        Workspace         `json:"workspace"`
	Env              Env               `json:"env"`
	Git              Git               `json:"git"`
	Network          Network           `json:"network"`
	Tools            Tools             `json:"tools"`
	HostCapabilities HostCapabilities  `json:"hostCapabilities"`
	EndpointExposure EndpointExposure  `json:"endpointExposure,omitempty"`
	HostFS           hostfs.Config     `json:"hostfs,omitempty"`
	CommandProxy     CommandProxy      `json:"commandProxy"`
	Policy           Policy            `json:"policy"`
	Audit            Audit             `json:"audit"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

type Identity struct {
	User     string `json:"user"`
	Hostname string `json:"hostname"`
	Timezone string `json:"timezone"`
	Locale   string `json:"locale"`
}

type Workspace struct {
	Mode     string `json:"mode"`
	PathMode string `json:"pathMode"`
}

type Env struct {
	Public  map[string]string `json:"public"`
	Deny    []string          `json:"deny"`
	Inherit []string          `json:"inherit"`
}

type Git struct {
	UserName  string `json:"userName"`
	UserEmail string `json:"userEmail"`
}

type Network struct {
	Mode            string `json:"mode"`
	ProxyEnvVisible bool   `json:"proxyEnvVisible"`
	ProxySecretRef  string `json:"proxySecretRef,omitempty"`
}

type Tools struct {
	Presets    []string           `json:"presets"`
	NPMGlobals []NPMGlobalPackage `json:"npmGlobals,omitempty"`
}

type NPMGlobalPackage struct {
	Package  string   `json:"package"`
	Commands []string `json:"commands"`
}

type HostCapabilities struct {
	Open OpenCapability `json:"open"`
}

type EndpointExposure struct {
	HostToGuest []EndpointCandidate `json:"hostToGuest,omitempty"`
}

type EndpointCandidate struct {
	ID            string `json:"id"`
	Owner         string `json:"owner"`
	Proto         string `json:"proto,omitempty"`
	TargetAddress string `json:"targetAddress"`
}

type OpenCapability struct {
	Mode                    string `json:"mode"`
	AllowURLs               bool   `json:"allowUrls"`
	AllowLocalURLs          bool   `json:"allowLocalUrls"`
	AllowPrivateNetworkURLs bool   `json:"allowPrivateNetworkUrls"`
	AllowWorkspaceFiles     bool   `json:"allowWorkspaceFiles"`
	BrowserProfile          string `json:"browserProfile"`
}

type CommandProxy struct {
	Commands map[string]CommandProxyCommand `json:"commands"`
}

type CommandProxyCommand struct {
	Route      string `json:"route"`
	Action     string `json:"action"`
	ArgvSchema string `json:"argvSchema,omitempty"`
}

type Policy struct {
	Engine          string      `json:"engine"`
	MaxCapabilities []string    `json:"maxCapabilities"`
	ScriptRefs      []ScriptRef `json:"scriptRefs,omitempty"`
}

type ScriptRef struct {
	ID          string   `json:"id"`
	Path        string   `json:"path"`
	Entrypoints []string `json:"entrypoints"`
}

type Audit struct {
	Enabled bool `json:"enabled"`
}

type Store struct {
	Root string
}

var identityStateDirs = []string{"home", "cache", "config", "data", "browser", "machine"}

func DefaultStore() (Store, error) {
	if root := strings.TrimSpace(os.Getenv("HIDEOUT_STORE_ROOT")); root != "" {
		if !filepath.IsAbs(root) {
			return Store{}, fmt.Errorf("HIDEOUT_STORE_ROOT must be an absolute path")
		}
		return Store{Root: filepath.Clean(root)}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Store{}, err
	}
	return Store{Root: filepath.Join(home, ".hideout")}, nil
}

func ValidateName(name string) error {
	return validateProfileName(name)
}

func normalizeProfileName(name string) (string, error) {
	if name == "" {
		name = "default"
	}
	if err := validateProfileName(name); err != nil {
		return "", err
	}
	return name, nil
}

func validateProfileName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("profile name is required")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid profile name %q", name)
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '.' || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("invalid profile name %q", name)
	}
	return nil
}

func Default(name string) Profile {
	if name == "" {
		name = "default"
	}
	return Profile{
		SchemaVersion: SchemaVersion,
		Name:          name,
		Identity: Identity{
			User:     "developer",
			Hostname: "devbox",
			Timezone: "UTC",
			Locale:   "en_US.UTF-8",
		},
		Workspace: Workspace{
			Mode:     "read-write",
			PathMode: "preserve",
		},
		Env: Env{
			Public:  map[string]string{},
			Deny:    []string{},
			Inherit: []string{"TERM", "COLORTERM"},
		},
		Git: Git{
			UserName:  "Developer",
			UserEmail: "developer@example.com",
		},
		Network: Network{
			Mode:            "direct",
			ProxyEnvVisible: false,
		},
		Tools: Tools{
			Presets: []string{"base-dev"},
		},
		HostCapabilities: HostCapabilities{
			Open: OpenCapability{
				Mode:                "brokered",
				AllowURLs:           true,
				AllowWorkspaceFiles: true,
				BrowserProfile:      "isolated",
			},
		},
		CommandProxy: CommandProxy{
			Commands: map[string]CommandProxyCommand{
				"open": {
					Route:      "host-broker",
					Action:     "host.open",
					ArgvSchema: "open-target-v1",
				},
				"xdg-open": {
					Route:      "host-broker",
					Action:     "host.open",
					ArgvSchema: "open-target-v1",
				},
			},
		},
		Policy: Policy{
			Engine: "builtin+goja",
			MaxCapabilities: []string{
				"host.open",
				"guest.exec",
				"network.connect",
				"portbridge.host-to-guest",
				"endpoint.expose.host-to-guest",
			},
		},
		Audit: Audit{Enabled: true},
	}
}

func (s Store) ProfileDir(name string) string {
	return filepath.Join(s.Root, "profiles", name)
}

func (s Store) ProfilePath(name string) string {
	return filepath.Join(s.ProfileDir(name), "profile.json")
}

func (s Store) Load(name string) (Profile, error) {
	name, err := normalizeProfileName(name)
	if err != nil {
		return Profile{}, err
	}
	data, err := os.ReadFile(s.ProfilePath(name))
	if err != nil {
		return Profile{}, err
	}
	var p Profile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Profile{}, err
	}
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

func (s Store) LoadOrInit(name string) (Profile, error) {
	name, err := normalizeProfileName(name)
	if err != nil {
		return Profile{}, err
	}
	p, err := s.Load(name)
	if err == nil {
		if profileNeedsMetadata(p) {
			if err := ensureMetadata(&p, "create", ""); err != nil {
				return Profile{}, err
			}
			if err := s.Save(p); err != nil {
				return Profile{}, err
			}
			return p, nil
		}
		if err := MaterializeIdentityState(s.ProfileDir(p.Name), p); err != nil {
			return Profile{}, err
		}
		return p, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Profile{}, err
	}
	p = Default(name)
	if err := ensureMetadata(&p, "create", ""); err != nil {
		return Profile{}, err
	}
	return p, s.Save(p)
}

func profileNeedsMetadata(p Profile) bool {
	return p.Metadata == nil ||
		p.Metadata["profileId"] == "" ||
		p.Metadata["identityId"] == "" ||
		p.Metadata["machineId"] == "" ||
		p.Metadata["createdAt"] == "" ||
		p.Metadata["lineageMode"] == ""
}

func (s Store) Save(p Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if err := ensureMetadata(&p, "create", ""); err != nil {
		return err
	}
	dir := s.ProfileDir(p.Name)
	if err := os.MkdirAll(filepath.Join(dir, "policy"), 0o700); err != nil {
		return err
	}
	if err := MaterializeIdentityState(dir, p); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(s.ProfilePath(p.Name), data, 0o600)
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	keepTemp = false
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}

func (s Store) Create(p Profile) error {
	name, err := normalizeProfileName(p.Name)
	if err != nil {
		return err
	}
	p.Name = name
	dir := s.ProfileDir(name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("profile %q already exists", name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.Save(p)
}

func (s Store) RotateIdentity(name string) (Profile, error) {
	p, err := s.Load(name)
	if err != nil {
		return Profile{}, err
	}
	if err := ensureMetadata(&p, "create", ""); err != nil {
		return Profile{}, err
	}
	oldIdentityID := p.Metadata["identityId"]
	archiveID := oldIdentityID
	if archiveID == "" {
		archiveID, err = newID("archive")
		if err != nil {
			return Profile{}, err
		}
	}
	archiveDir := filepath.Join(s.ProfileDir(p.Name), "identity-archive", archiveID)
	if err := archiveIdentityState(s.ProfileDir(p.Name), archiveDir); err != nil {
		return Profile{}, err
	}
	if err := assignNewIdentity(&p, oldIdentityID, "rotate"); err != nil {
		return Profile{}, err
	}
	delete(p.Metadata, "identityArchive")
	p.Metadata["identityArchiveId"] = archiveID
	p.Metadata["identityRotatedAt"] = p.Metadata["identityChangedAt"]
	return p, s.Save(p)
}

func (s Store) ResetIdentity(name string) (Profile, error) {
	p, err := s.Load(name)
	if err != nil {
		return Profile{}, err
	}
	if err := ensureMetadata(&p, "create", ""); err != nil {
		return Profile{}, err
	}
	oldIdentityID := p.Metadata["identityId"]
	if err := removeIdentityState(s.ProfileDir(p.Name)); err != nil {
		return Profile{}, err
	}
	if err := assignNewIdentity(&p, oldIdentityID, "reset"); err != nil {
		return Profile{}, err
	}
	delete(p.Metadata, "identityArchive")
	delete(p.Metadata, "identityArchiveId")
	delete(p.Metadata, "identityRotatedAt")
	p.Metadata["identityResetAt"] = p.Metadata["identityChangedAt"]
	return p, s.Save(p)
}

func (s Store) ClonePolicy(sourceName, targetName string) (Profile, error) {
	source, err := s.Load(sourceName)
	if err != nil {
		return Profile{}, err
	}
	clone := cloneProfile(source)
	clone.Name = targetName
	clone.Metadata = nil
	if err := ensureMetadata(&clone, "policy-clone", source.Name); err != nil {
		return Profile{}, err
	}
	if source.Metadata != nil {
		if source.Metadata["profileId"] != "" {
			clone.Metadata["sourceProfileId"] = source.Metadata["profileId"]
		}
		if source.Metadata["identityId"] != "" {
			clone.Metadata["sourceIdentityId"] = source.Metadata["identityId"]
		}
	}
	if err := clone.Validate(); err != nil {
		return Profile{}, err
	}
	if err := s.Create(clone); err != nil {
		return Profile{}, err
	}
	if err := copyPolicyDir(filepath.Join(s.ProfileDir(source.Name), "policy"), filepath.Join(s.ProfileDir(clone.Name), "policy")); err != nil {
		_ = os.RemoveAll(s.ProfileDir(clone.Name))
		return Profile{}, err
	}
	return clone, nil
}

func EphemeralIdentityProfile(p Profile) (Profile, error) {
	ephemeral := cloneProfile(p)
	if ephemeral.Metadata == nil {
		ephemeral.Metadata = map[string]string{}
	}
	sourceIdentityID := p.Metadata["identityId"]
	id, err := newID("id")
	if err != nil {
		return Profile{}, err
	}
	machineID, err := machineIDFromIdentityID(id)
	if err != nil {
		return Profile{}, err
	}
	ephemeral.Metadata["identityId"] = id
	ephemeral.Metadata["machineId"] = machineID
	ephemeral.Metadata["lineageMode"] = "session-fork"
	ephemeral.Metadata["createdFrom"] = p.Name
	ephemeral.Metadata["identityChangedAt"] = time.Now().UTC().Format(time.RFC3339)
	if sourceIdentityID != "" {
		ephemeral.Metadata["sourceIdentityId"] = sourceIdentityID
	}
	if err := ephemeral.Validate(); err != nil {
		return Profile{}, err
	}
	return ephemeral, nil
}

func copyPolicyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		info, err := os.Lstat(srcPath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("policy path %q must not be a symlink", srcPath)
		}
		if info.IsDir() {
			if err := copyPolicyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("policy path %q must be a regular file or directory", srcPath)
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o600
		}
		if err := os.WriteFile(dstPath, data, mode); err != nil {
			return err
		}
	}
	return nil
}

func (p Profile) Validate() error {
	if p.SchemaVersion == "" {
		return errors.New("profile schemaVersion is required")
	}
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported profile schema %q", p.SchemaVersion)
	}
	if err := validateProfileName(p.Name); err != nil {
		return err
	}
	if p.Identity.User == "" || p.Identity.Hostname == "" {
		return errors.New("profile identity user and hostname are required")
	}
	if !validLinuxUserName(p.Identity.User) {
		return fmt.Errorf("profile identity user %q must be a Linux username", p.Identity.User)
	}
	if p.Identity.Timezone == "" || p.Identity.Locale == "" {
		return errors.New("profile identity timezone and locale are required")
	}
	if err := validateHostname(p.Identity.Hostname); err != nil {
		return err
	}
	if p.Workspace.Mode != "read-write" {
		return fmt.Errorf("unsupported workspace mode %q", p.Workspace.Mode)
	}
	if p.Workspace.PathMode != "preserve" && p.Workspace.PathMode != "alias" {
		return fmt.Errorf("unsupported workspace pathMode %q", p.Workspace.PathMode)
	}
	if p.Env.Public == nil || p.Env.Deny == nil || p.Env.Inherit == nil {
		return errors.New("env public, deny, and inherit are required")
	}
	if err := validateUniqueStrings("env.deny", p.Env.Deny); err != nil {
		return err
	}
	if err := validateUniqueStrings("env.inherit", p.Env.Inherit); err != nil {
		return err
	}
	if err := validateEnvPolicy(p.Env); err != nil {
		return err
	}
	if p.Git.UserName == "" || p.Git.UserEmail == "" {
		return errors.New("git userName and userEmail are required")
	}
	if p.Network.Mode == "" {
		return errors.New("network mode is required")
	}
	if p.Network.Mode != "direct" && p.Network.Mode != "tun2socks" {
		return fmt.Errorf("unsupported network mode %q", p.Network.Mode)
	}
	if p.Network.ProxyEnvVisible {
		return errors.New("network.proxyEnvVisible must be false")
	}
	if p.Network.Mode == "tun2socks" && strings.TrimSpace(p.Network.ProxySecretRef) == "" {
		return errors.New("tun2socks requires network.proxySecretRef")
	}
	if strings.TrimSpace(p.Network.ProxySecretRef) != "" {
		if err := secrets.ValidateRef(p.Network.ProxySecretRef); err != nil {
			return fmt.Errorf("network.proxySecretRef: %w", err)
		}
	}
	if p.Tools.Presets == nil {
		return errors.New("tools.presets is required")
	}
	if err := validateUniqueStrings("tools.presets", p.Tools.Presets); err != nil {
		return err
	}
	for _, preset := range p.Tools.Presets {
		if strings.TrimSpace(preset) == "" {
			return errors.New("tool preset cannot be empty")
		}
		if preset != "base-dev" && preset != "node-dev" {
			return fmt.Errorf("unsupported tool preset %q", preset)
		}
	}
	if err := validateNPMGlobals(p.Tools.NPMGlobals); err != nil {
		return err
	}
	if err := p.validateHostCapabilities(); err != nil {
		return err
	}
	if err := validateEndpointExposure(p.EndpointExposure); err != nil {
		return err
	}
	if err := hostfs.ValidateConfig(p.HostFS, hostfs.SourceProfile); err != nil {
		return err
	}
	if err := validateHostFSRuleIDs(p.HostFS); err != nil {
		return err
	}
	if p.Policy.Engine == "" {
		return errors.New("policy engine is required")
	}
	if p.Policy.Engine != "builtin+goja" {
		return fmt.Errorf("unsupported policy engine %q", p.Policy.Engine)
	}
	if len(p.Policy.MaxCapabilities) == 0 {
		return errors.New("policy.maxCapabilities is required")
	}
	if err := validateUniqueStrings("policy.maxCapabilities", p.Policy.MaxCapabilities); err != nil {
		return err
	}
	for _, capability := range p.Policy.MaxCapabilities {
		switch capability {
		case "host.open", "guest.exec", "network.connect", "portbridge.host-to-guest", "endpoint.expose.host-to-guest":
		default:
			return fmt.Errorf("unsupported policy max capability %q", capability)
		}
	}
	if len(p.Policy.ScriptRefs) > MaxPolicyScriptRefs {
		return fmt.Errorf("policy.scriptRefs must not exceed %d entries", MaxPolicyScriptRefs)
	}
	for _, ref := range p.Policy.ScriptRefs {
		id := strings.TrimSpace(ref.ID)
		if id == "" {
			return errors.New("policy script refs require id and path")
		}
		if err := validateScriptRefPath(id, ref.Path); err != nil {
			return err
		}
		if len(ref.Entrypoints) == 0 {
			return fmt.Errorf("policy script %s requires at least one entrypoint", id)
		}
		if err := validateUniqueStrings("policy script "+id+" entrypoints", ref.Entrypoints); err != nil {
			return err
		}
		for _, entrypoint := range ref.Entrypoints {
			switch entrypoint {
			case "decideCommand", "redactAudit":
			default:
				return fmt.Errorf("unsupported policy script entrypoint %q", entrypoint)
			}
		}
	}
	if err := validateUniqueScriptRefIDs(p.Policy.ScriptRefs); err != nil {
		return err
	}
	if err := p.validateCommandProxy(); err != nil {
		return err
	}
	if err := validateMetadata(p.Metadata); err != nil {
		return err
	}
	return nil
}

func validateUniqueScriptRefIDs(refs []ScriptRef) error {
	seen := map[string]struct{}{}
	for _, ref := range refs {
		id := strings.TrimSpace(ref.ID)
		if _, ok := seen[id]; ok {
			return fmt.Errorf("policy.scriptRefs must not contain duplicate id %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validateUniqueStrings(label string, values []string) error {
	seen := map[string]struct{}{}
	for _, value := range values {
		if _, ok := seen[value]; ok {
			return fmt.Errorf("%s must not contain duplicate value %q", label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateEnvPolicy(env Env) error {
	for name := range env.Public {
		if !validEnvName(name) {
			return fmt.Errorf("env.public contains invalid env name %q", name)
		}
		if isHideoutReservedEnvName(name) {
			return errors.New("env.public must not expose hideout runtime env")
		}
		if isBlockedProxyEnvName(name) {
			return fmt.Errorf("env.public must not expose proxy env %q", name)
		}
		if isSyntheticIdentityEnvName(name) {
			return fmt.Errorf("env.public must not override synthetic identity env %q", name)
		}
	}
	for _, name := range env.Inherit {
		if !validEnvName(name) {
			return fmt.Errorf("env.inherit contains invalid env name %q", name)
		}
		if isHideoutReservedEnvName(name) {
			return errors.New("env.inherit must not expose hideout runtime env")
		}
		if isBlockedProxyEnvName(name) {
			return fmt.Errorf("env.inherit must not expose proxy env %q", name)
		}
		if isSyntheticIdentityEnvName(name) {
			return fmt.Errorf("env.inherit must not inherit synthetic identity env %q", name)
		}
	}
	return nil
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func validLinuxUserName(name string) bool {
	if len(name) == 0 || len(name) > 32 {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if (r >= 'a' && r <= 'z') || r == '_' {
				continue
			}
			return false
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func isBlockedProxyEnvName(name string) bool {
	switch name {
	case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "FTP_PROXY",
		"http_proxy", "https_proxy", "no_proxy", "all_proxy", "ftp_proxy":
		return true
	default:
		return false
	}
}

func isHideoutReservedEnvName(name string) bool {
	return strings.HasPrefix(name, "HIDEOUT_")
}

func isSyntheticIdentityEnvName(name string) bool {
	switch name {
	case "HOME", "USER", "LOGNAME", "HOSTNAME", "TMPDIR",
		"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
		"GIT_CONFIG_GLOBAL",
		"TZ", "LANG", "LC_ALL", "PATH":
		return true
	default:
		return false
	}
}

func validateScriptRefPath(id, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("policy script refs require id and path")
	}
	if filepath.IsAbs(value) || strings.HasPrefix(value, "/") {
		return fmt.Errorf("policy script %s path must be relative under policy/", id)
	}
	if strings.Contains(value, `\`) {
		return fmt.Errorf("policy script %s path must use slash-separated policy paths", id)
	}
	clean := slashpath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return fmt.Errorf("policy script %s path must not escape policy/", id)
	}
	if clean != value {
		return fmt.Errorf("policy script %s path must be normalized under policy/", id)
	}
	if clean == "policy" || !strings.HasPrefix(clean, "policy/") {
		return fmt.Errorf("policy script %s path must be under policy/", id)
	}
	return nil
}

func validateHostFSRuleIDs(config hostfs.Config) error {
	seen := map[string]string{}
	for i, rule := range config.Grants {
		if strings.TrimSpace(rule.ID) == "" {
			return fmt.Errorf("hostfs.grants[%d].id is required", i)
		}
		if prev := seen[rule.ID]; prev != "" {
			return fmt.Errorf("hostfs.grants[%d].id duplicates %s", i, prev)
		}
		seen[rule.ID] = fmt.Sprintf("hostfs.grants[%d].id", i)
	}
	for i, rule := range config.Deny {
		if strings.TrimSpace(rule.ID) == "" {
			return fmt.Errorf("hostfs.deny[%d].id is required", i)
		}
		if prev := seen[rule.ID]; prev != "" {
			return fmt.Errorf("hostfs.deny[%d].id duplicates %s", i, prev)
		}
		seen[rule.ID] = fmt.Sprintf("hostfs.deny[%d].id", i)
	}
	return nil
}

func validateNPMGlobals(packages []NPMGlobalPackage) error {
	seenPackages := map[string]bool{}
	seenCommands := map[string]bool{}
	for i, pkg := range packages {
		name := strings.TrimSpace(pkg.Package)
		if name == "" {
			return fmt.Errorf("tools.npmGlobals[%d].package is required", i)
		}
		if strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\x00\r\n\t ") {
			return fmt.Errorf("tools.npmGlobals[%d].package is not a valid npm package spec", i)
		}
		if seenPackages[name] {
			return fmt.Errorf("tools.npmGlobals[%d].package duplicates %s", i, name)
		}
		seenPackages[name] = true
		if len(pkg.Commands) == 0 {
			return fmt.Errorf("tools.npmGlobals[%d].commands is required", i)
		}
		if err := validateUniqueStrings(fmt.Sprintf("tools.npmGlobals[%d].commands", i), pkg.Commands); err != nil {
			return err
		}
		for j, command := range pkg.Commands {
			command = strings.TrimSpace(command)
			if command == "" {
				return fmt.Errorf("tools.npmGlobals[%d].commands[%d] is required", i, j)
			}
			if strings.ContainsAny(command, "\x00\r\n\t /\\") || strings.HasPrefix(command, "-") {
				return fmt.Errorf("tools.npmGlobals[%d].commands[%d] is not a command name", i, j)
			}
			if seenCommands[command] {
				return fmt.Errorf("tools.npmGlobals[%d].commands[%d] duplicates command %s", i, j, command)
			}
			seenCommands[command] = true
		}
	}
	return nil
}

func validateEndpointExposure(config EndpointExposure) error {
	seen := map[string]bool{}
	for i, candidate := range config.HostToGuest {
		label := fmt.Sprintf("endpointExposure.hostToGuest[%d]", i)
		id := strings.TrimSpace(candidate.ID)
		if id == "" {
			return fmt.Errorf("%s.id is required", label)
		}
		if !validEndpointLabel(id) {
			return fmt.Errorf("%s.id contains unsupported characters", label)
		}
		if seen[id] {
			return fmt.Errorf("%s.id duplicates %q", label, id)
		}
		seen[id] = true
		owner := strings.TrimSpace(candidate.Owner)
		if owner == "" {
			return fmt.Errorf("%s.owner is required", label)
		}
		if !validEndpointLabel(owner) {
			return fmt.Errorf("%s.owner contains unsupported characters", label)
		}
		proto := strings.TrimSpace(candidate.Proto)
		if proto == "" {
			proto = "tcp"
		}
		if proto != "tcp" {
			return fmt.Errorf("%s.proto %q is unsupported", label, proto)
		}
		if err := validateLoopbackEndpoint(candidate.TargetAddress); err != nil {
			return fmt.Errorf("%s.targetAddress: %w", label, err)
		}
	}
	return nil
}

func validEndpointLabel(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == ':':
		default:
			return false
		}
	}
	return true
}

func validateLoopbackEndpoint(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("target address is required")
	}
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		return fmt.Errorf("must be host:port: %w", err)
	}
	if host == "localhost" {
		return nil
	}
	parsed := net.ParseIP(host)
	if parsed == nil || !parsed.IsLoopback() {
		return errors.New("target host must be localhost or a loopback IP")
	}
	return nil
}

func (p Profile) validateHostCapabilities() error {
	open := p.HostCapabilities.Open
	if open.Mode != "brokered" {
		return fmt.Errorf("hostCapabilities.open.mode must be brokered")
	}
	if open.BrowserProfile != "isolated" {
		return fmt.Errorf("hostCapabilities.open.browserProfile must be isolated")
	}
	return nil
}

func (p Profile) validateCommandProxy() error {
	if len(p.CommandProxy.Commands) == 0 {
		return errors.New("commandProxy.commands is required")
	}
	if _, ok := p.CommandProxy.Commands["open"]; !ok {
		return errors.New("commandProxy.commands.open is required")
	}
	for name, command := range p.CommandProxy.Commands {
		if err := validateCommandProxyName(name); err != nil {
			return err
		}
		if command.Route != "host-broker" {
			return fmt.Errorf("commandProxy.commands.%s.route must be host-broker", name)
		}
		if command.Action != "host.open" {
			return fmt.Errorf("commandProxy.commands.%s.action must be host.open", name)
		}
		switch command.ArgvSchema {
		case "", "open-target-v1":
		default:
			return fmt.Errorf("commandProxy.commands.%s.argvSchema must be open-target-v1", name)
		}
	}
	return nil
}

func validateCommandProxyName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("command proxy name is required")
	}
	if strings.TrimSpace(name) != name {
		return fmt.Errorf("command proxy %q must not contain surrounding whitespace", name)
	}
	if name == "." || name == ".." || name == "hideout-shim" || strings.ContainsAny(name, `/\`) || strings.ContainsFunc(name, unicode.IsSpace) {
		return fmt.Errorf("command proxy %q must be a simple command name", name)
	}
	return nil
}

func validateMetadata(metadata map[string]string) error {
	if len(metadata) == 0 {
		return nil
	}
	if value := metadata["profileId"]; value != "" && !validPrefixedHexID(value, "prf") {
		return fmt.Errorf("metadata.profileId %q is invalid", value)
	}
	if value := metadata["identityId"]; value != "" && !validPrefixedHexID(value, "id") {
		return fmt.Errorf("metadata.identityId %q is invalid", value)
	}
	if value := metadata["previousIdentityId"]; value != "" && !validPrefixedHexID(value, "id") {
		return fmt.Errorf("metadata.previousIdentityId %q is invalid", value)
	}
	if value := metadata["identityArchiveId"]; value != "" && !validPrefixedHexID(value, "id") {
		return fmt.Errorf("metadata.identityArchiveId %q is invalid", value)
	}
	if value := metadata["sourceProfileId"]; value != "" && !validPrefixedHexID(value, "prf") {
		return fmt.Errorf("metadata.sourceProfileId %q is invalid", value)
	}
	if value := metadata["sourceIdentityId"]; value != "" && !validPrefixedHexID(value, "id") {
		return fmt.Errorf("metadata.sourceIdentityId %q is invalid", value)
	}
	if value := metadata["machineId"]; value != "" && (len(value) != 32 || !isLowerHexString(value)) {
		return fmt.Errorf("metadata.machineId %q is invalid", value)
	}
	switch value := metadata["lineageMode"]; value {
	case "", "create", "policy-clone", "session-fork", "migration", "exact-copy":
	default:
		return fmt.Errorf("metadata.lineageMode %q is invalid", value)
	}
	switch value := metadata["lastIdentityOperation"]; value {
	case "", "rotate", "reset":
	default:
		return fmt.Errorf("metadata.lastIdentityOperation %q is invalid", value)
	}
	return nil
}

func validPrefixedHexID(value, prefix string) bool {
	prefix += "_"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(value, prefix)
	return suffix != "" && isLowerHexString(suffix)
}

func isLowerHexString(value string) bool {
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}

func validateHostname(hostname string) error {
	if hostname != strings.TrimSpace(hostname) || hostname == "" {
		return errors.New("profile identity hostname is required")
	}
	if len(hostname) > 253 {
		return errors.New("profile identity hostname is too long")
	}
	for _, label := range strings.Split(hostname, ".") {
		if label == "" || len(label) > 63 {
			return fmt.Errorf("invalid profile identity hostname %q", hostname)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid profile identity hostname %q", hostname)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
				continue
			}
			return fmt.Errorf("invalid profile identity hostname %q", hostname)
		}
	}
	return nil
}

func MaterializeIdentityState(dir string, p Profile) error {
	for _, sub := range identityStateDirs {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return err
		}
	}
	if err := materializeMachineID(filepath.Join(dir, "machine", "machine-id"), p.Metadata["machineId"]); err != nil {
		return err
	}
	if err := materializeGitConfig(filepath.Join(dir, "home", ".gitconfig"), p.Git); err != nil {
		return err
	}
	if err := materializeHomeXDGLinks(filepath.Join(dir, "home")); err != nil {
		return err
	}
	return writeIdentityMetadata(filepath.Join(dir, "identity.json"), p.Metadata)
}

func materializeHomeXDGLinks(home string) error {
	if err := ensureRelativeSymlink(filepath.Join(home, ".config"), "../config"); err != nil {
		return err
	}
	if err := ensureRelativeSymlink(filepath.Join(home, ".cache"), "../cache"); err != nil {
		return err
	}
	local := filepath.Join(home, ".local")
	if err := os.MkdirAll(local, 0o700); err != nil {
		return err
	}
	return ensureRelativeSymlink(filepath.Join(local, "share"), "../../data")
}

func ensureRelativeSymlink(linkPath, target string) error {
	st, err := os.Lstat(linkPath)
	if err == nil {
		if st.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s must be a symlink to %s", linkPath, target)
		}
		got, err := os.Readlink(linkPath)
		if err != nil {
			return err
		}
		if got != target {
			return fmt.Errorf("%s points to %s, want %s", linkPath, got, target)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Symlink(target, linkPath)
}

func materializeGitConfig(path string, git Git) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	content := fmt.Sprintf("[user]\n  name = %s\n  email = %s\n", git.UserName, git.UserEmail)
	return os.WriteFile(path, []byte(content), 0o600)
}

func materializeMachineID(path, machineID string) error {
	if machineID == "" {
		return errors.New("profile machineId is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(machineID+"\n"), 0o600)
}

func ensureMetadata(p *Profile, mode, source string) error {
	if p.Metadata == nil {
		p.Metadata = map[string]string{}
	}
	if p.Metadata["profileId"] == "" {
		id, err := newID("prf")
		if err != nil {
			return err
		}
		p.Metadata["profileId"] = id
	}
	if p.Metadata["identityId"] == "" {
		id, err := newID("id")
		if err != nil {
			return err
		}
		p.Metadata["identityId"] = id
	}
	if p.Metadata["machineId"] == "" {
		machineID, err := machineIDFromIdentityID(p.Metadata["identityId"])
		if err != nil {
			return err
		}
		p.Metadata["machineId"] = machineID
	}
	if p.Metadata["createdAt"] == "" {
		p.Metadata["createdAt"] = time.Now().UTC().Format(time.RFC3339)
	}
	if mode != "" && p.Metadata["lineageMode"] == "" {
		p.Metadata["lineageMode"] = mode
	}
	if source != "" {
		p.Metadata["createdFrom"] = source
	}
	return nil
}

func newID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(raw[:]), nil
}

func assignNewIdentity(p *Profile, oldIdentityID, operation string) error {
	id, err := newID("id")
	if err != nil {
		return err
	}
	if p.Metadata == nil {
		p.Metadata = map[string]string{}
	}
	p.Metadata["identityId"] = id
	machineID, err := machineIDFromIdentityID(id)
	if err != nil {
		return err
	}
	p.Metadata["machineId"] = machineID
	if oldIdentityID != "" {
		p.Metadata["previousIdentityId"] = oldIdentityID
	}
	p.Metadata["lastIdentityOperation"] = operation
	p.Metadata["identityChangedAt"] = time.Now().UTC().Format(time.RFC3339)
	return nil
}

func machineIDFromIdentityID(identityID string) (string, error) {
	body := strings.TrimPrefix(strings.ToLower(identityID), "id_")
	if len(body) == 32 && isLowerHex(body) {
		return body, nil
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func isLowerHex(value string) bool {
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}

func archiveIdentityState(profileDir, archiveDir string) error {
	if err := os.MkdirAll(archiveDir, 0o700); err != nil {
		return err
	}
	for _, name := range identityStateDirs {
		if err := moveIfExists(filepath.Join(profileDir, name), filepath.Join(archiveDir, name)); err != nil {
			return err
		}
	}
	return moveIfExists(filepath.Join(profileDir, "identity.json"), filepath.Join(archiveDir, "identity.json"))
}

func removeIdentityState(profileDir string) error {
	for _, name := range identityStateDirs {
		if err := os.RemoveAll(filepath.Join(profileDir, name)); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(profileDir, "identity.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func moveIfExists(src, dst string) error {
	if _, err := os.Lstat(src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

func writeIdentityMetadata(path string, metadata map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func cloneProfile(p Profile) Profile {
	p.Env.Public = copyStringMap(p.Env.Public)
	p.Env.Deny = copyStringSlice(p.Env.Deny)
	p.Env.Inherit = copyStringSlice(p.Env.Inherit)
	p.Tools.Presets = copyStringSlice(p.Tools.Presets)
	p.Tools.NPMGlobals = copyNPMGlobals(p.Tools.NPMGlobals)
	p.Policy.MaxCapabilities = copyStringSlice(p.Policy.MaxCapabilities)
	p.Policy.ScriptRefs = append([]ScriptRef(nil), p.Policy.ScriptRefs...)
	p.Metadata = copyStringMap(p.Metadata)
	return p
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func copyNPMGlobals(in []NPMGlobalPackage) []NPMGlobalPackage {
	if in == nil {
		return nil
	}
	out := make([]NPMGlobalPackage, len(in))
	for i, pkg := range in {
		out[i] = pkg
		out[i].Commands = copyStringSlice(pkg.Commands)
	}
	return out
}
