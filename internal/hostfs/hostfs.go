package hostfs

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Op string
type Scope string
type Subject string
type TTL string
type Source string

const (
	OpStat   Op = "stat"
	OpRead   Op = "read"
	OpList   Op = "list"
	OpWrite  Op = "write"
	OpCreate Op = "create"
	OpDelete Op = "delete"
	OpRename Op = "rename"

	ScopeExactFile    Scope = "exact-file"
	ScopeGlob         Scope = "glob"
	ScopeDir          Scope = "dir"
	ScopeRecursiveDir Scope = "recursive-dir"

	SubjectProfile     Subject = "profile"
	SubjectEnvironment Subject = "environment"
	SubjectSession     Subject = "session"
	SubjectCommand     Subject = "command"

	TTLRun         TTL = "run"
	TTLSession     TTL = "session"
	TTLEnvironment TTL = "environment"
	TTLProfile     TTL = "profile"

	SourceProfile     Source = "profile"
	SourceEnvironment Source = "environment"
	SourceRun         Source = "run"
)

var writeOps = []Op{OpWrite, OpCreate, OpDelete, OpRename}

type Config struct {
	Grants []Rule `json:"grants,omitempty"`
	Deny   []Rule `json:"deny,omitempty"`
}

type Rule struct {
	ID        string     `json:"id,omitempty"`
	HostPath  string     `json:"hostPath"`
	GuestPath string     `json:"guestPath,omitempty"`
	Ops       []Op       `json:"ops,omitempty"`
	Scope     Scope      `json:"scope"`
	Subject   Subject    `json:"subject,omitempty"`
	TTL       TTL        `json:"ttl,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Reason    string     `json:"reason,omitempty"`
}

type BuildInput struct {
	Profile     Config
	Environment Config
	Run         Config
	Now         time.Time
}

type EffectivePolicy struct {
	Grants []Grant
	Deny   []Grant
	Now    time.Time
}

type Grant struct {
	Rule
	Source Source
	Index  int
}

type Decision struct {
	Allowed bool
	Effect  string
	Reason  string
	RuleID  string
	GrantID string
	Source  Source
}

func ValidateConfig(c Config, source Source) error {
	_, err := buildSourcePolicy(c, source, nowOrDefault(time.Time{}))
	return err
}

func Build(input BuildInput) (EffectivePolicy, error) {
	now := nowOrDefault(input.Now)
	out := EffectivePolicy{Now: now}
	for _, sourceInput := range []struct {
		source Source
		config Config
	}{
		{SourceProfile, input.Profile},
		{SourceEnvironment, input.Environment},
		{SourceRun, input.Run},
	} {
		policy, err := buildSourcePolicy(sourceInput.config, sourceInput.source, now)
		if err != nil {
			return EffectivePolicy{}, err
		}
		out.Grants = append(out.Grants, policy.Grants...)
		out.Deny = append(out.Deny, policy.Deny...)
	}
	return out, nil
}

func (p EffectivePolicy) Decide(op Op, hostPath string) Decision {
	if err := validateKnownOp(op); err != nil {
		return Decision{Reason: err.Error()}
	}
	clean, err := cleanHostPath(hostPath)
	if err != nil {
		return Decision{Reason: err.Error()}
	}
	for _, deny := range p.Deny {
		if ruleMatches(deny.Rule, op, clean, true) {
			return Decision{
				Effect:  "deny",
				Reason:  "denied by HostFS policy",
				RuleID:  deny.ID,
				GrantID: deny.ID,
				Source:  deny.Source,
			}
		}
	}
	for _, grant := range p.Grants {
		if ruleExpired(grant.Rule, p.Now) {
			continue
		}
		if ruleMatches(grant.Rule, op, clean, false) {
			return Decision{
				Allowed: true,
				Effect:  "allow",
				Reason:  grant.Reason,
				RuleID:  grant.ID,
				GrantID: grant.ID,
				Source:  grant.Source,
			}
		}
	}
	return Decision{Effect: "none", Reason: "no matching HostFS grant"}
}

func (p EffectivePolicy) Summary() map[string]any {
	return map[string]any{
		"profileRoots": []string{"/hideout/hostfs", "/Users", "/Volumes", "/private"},
		"grants":       len(p.Grants),
		"denyRules":    len(p.Deny),
		"default":      "hidden",
		"write":        "unsupported",
	}
}

func buildSourcePolicy(c Config, source Source, now time.Time) (EffectivePolicy, error) {
	if err := validateSource(source); err != nil {
		return EffectivePolicy{}, err
	}
	out := EffectivePolicy{Now: now}
	for i, rule := range c.Grants {
		grant, err := normalizeRule(rule, source, i, false, now)
		if err != nil {
			return EffectivePolicy{}, fmt.Errorf("hostfs.%s.grants[%d]: %w", source, i, err)
		}
		if !ruleExpired(grant.Rule, now) {
			out.Grants = append(out.Grants, grant)
		}
	}
	for i, rule := range c.Deny {
		deny, err := normalizeRule(rule, source, i, true, now)
		if err != nil {
			return EffectivePolicy{}, fmt.Errorf("hostfs.%s.deny[%d]: %w", source, i, err)
		}
		if !ruleExpired(deny.Rule, now) {
			out.Deny = append(out.Deny, deny)
		}
	}
	return out, nil
}

func normalizeRule(rule Rule, source Source, index int, deny bool, now time.Time) (Grant, error) {
	hostPath, err := cleanHostPath(rule.HostPath)
	if err != nil {
		return Grant{}, err
	}
	rule.HostPath = hostPath
	if rule.GuestPath != "" {
		guestPath, err := cleanGuestPath(rule.GuestPath)
		if err != nil {
			return Grant{}, err
		}
		rule.GuestPath = guestPath
	}
	if err := validateScope(rule.Scope); err != nil {
		return Grant{}, err
	}
	if rule.Scope == ScopeGlob {
		if err := validateGlobPattern(rule.HostPath); err != nil {
			return Grant{}, err
		}
	}
	if !deny && isBroadHostRoot(rule.HostPath, rule.Scope) {
		return Grant{}, fmt.Errorf("broad HostFS grant for %s with scope %s is forbidden", rule.HostPath, rule.Scope)
	}
	if !deny && strings.TrimSpace(rule.Reason) == "" {
		return Grant{}, errors.New("reason is required")
	}
	if deny && strings.TrimSpace(rule.Reason) == "" {
		return Grant{}, errors.New("reason is required")
	}
	rule.Subject, err = normalizeSubject(rule.Subject, source)
	if err != nil {
		return Grant{}, err
	}
	rule.TTL, err = normalizeTTL(rule.TTL, source)
	if err != nil {
		return Grant{}, err
	}
	if err := validateExpiry(rule); err != nil {
		return Grant{}, err
	}
	if deny {
		rule.Ops, err = normalizeDenyOps(rule.Ops)
	} else {
		rule.Ops, err = normalizeGrantOps(rule.Ops)
	}
	if err != nil {
		return Grant{}, err
	}
	if rule.ID == "" {
		rule.ID = fmt.Sprintf("%s-%d", source, index)
	}
	return Grant{Rule: rule, Source: source, Index: index}, nil
}

func normalizeGrantOps(ops []Op) ([]Op, error) {
	if len(ops) == 0 {
		return nil, errors.New("ops are required")
	}
	normalized, err := normalizeOps(ops)
	if err != nil {
		return nil, err
	}
	for _, op := range normalized {
		if slices.Contains(writeOps, op) {
			return nil, fmt.Errorf("HostFS op %q is write-class and unsupported in v1", op)
		}
	}
	if slices.Contains(normalized, OpRead) || slices.Contains(normalized, OpList) {
		normalized = appendMissingOp(normalized, OpStat)
	}
	sortOps(normalized)
	return normalized, nil
}

func normalizeDenyOps(ops []Op) ([]Op, error) {
	if len(ops) == 0 {
		return nil, nil
	}
	normalized, err := normalizeOps(ops)
	if err != nil {
		return nil, err
	}
	if slices.Contains(normalized, OpRead) || slices.Contains(normalized, OpList) {
		normalized = appendMissingOp(normalized, OpStat)
	}
	sortOps(normalized)
	return normalized, nil
}

func normalizeOps(ops []Op) ([]Op, error) {
	seen := map[Op]bool{}
	out := make([]Op, 0, len(ops))
	for _, op := range ops {
		if err := validateKnownOp(op); err != nil {
			return nil, err
		}
		if seen[op] {
			return nil, fmt.Errorf("duplicate op %q", op)
		}
		seen[op] = true
		out = append(out, op)
	}
	return out, nil
}

func validateKnownOp(op Op) error {
	switch op {
	case OpStat, OpRead, OpList, OpWrite, OpCreate, OpDelete, OpRename:
		return nil
	default:
		return fmt.Errorf("unsupported HostFS op %q", op)
	}
}

func appendMissingOp(ops []Op, op Op) []Op {
	if slices.Contains(ops, op) {
		return ops
	}
	return append(ops, op)
}

func sortOps(ops []Op) {
	slices.SortFunc(ops, func(a, b Op) int {
		return opRank(a) - opRank(b)
	})
}

func opRank(op Op) int {
	switch op {
	case OpStat:
		return 0
	case OpRead:
		return 1
	case OpList:
		return 2
	case OpWrite:
		return 3
	case OpCreate:
		return 4
	case OpDelete:
		return 5
	case OpRename:
		return 6
	default:
		return 100
	}
}

func ruleMatches(rule Rule, op Op, hostPath string, deny bool) bool {
	if !deny && !opAllowed(rule, op) {
		if !(op == OpList && ruleAllowsSyntheticList(rule, hostPath)) {
			return false
		}
	}
	if deny && len(rule.Ops) > 0 && !opAllowed(rule, op) {
		return false
	}
	switch rule.Scope {
	case ScopeExactFile:
		if hostPath == rule.HostPath {
			return true
		}
		return !deny && op == OpList && exactGrantAllowsParentList(rule, hostPath)
	case ScopeGlob:
		if op == OpList {
			return !deny && globAllowsParentList(rule, hostPath)
		}
		return globMatches(rule.HostPath, hostPath)
	case ScopeDir:
		if hostPath == rule.HostPath {
			return true
		}
		if op == OpList {
			return false
		}
		return filepath.Dir(hostPath) == rule.HostPath
	case ScopeRecursiveDir:
		return hostPath == rule.HostPath || strings.HasPrefix(hostPath, rule.HostPath+string(filepath.Separator))
	default:
		return false
	}
}

func ruleAllowsSyntheticList(rule Rule, hostPath string) bool {
	switch rule.Scope {
	case ScopeExactFile:
		return exactGrantAllowsParentList(rule, hostPath)
	case ScopeGlob:
		return globAllowsParentList(rule, hostPath)
	default:
		return false
	}
}

func exactGrantAllowsParentList(rule Rule, hostPath string) bool {
	if rule.Scope != ScopeExactFile {
		return false
	}
	return filepath.Dir(rule.HostPath) == hostPath && (slices.Contains(rule.Ops, OpRead) || slices.Contains(rule.Ops, OpStat))
}

func globAllowsParentList(rule Rule, hostPath string) bool {
	if rule.Scope != ScopeGlob || !(slices.Contains(rule.Ops, OpRead) || slices.Contains(rule.Ops, OpStat)) {
		return false
	}
	return globCouldMatchChildOf(rule.HostPath, hostPath)
}

func globMatches(pattern, hostPath string) bool {
	ok, err := filepath.Match(pattern, hostPath)
	return err == nil && ok
}

func globCouldMatchChildOf(pattern, dir string) bool {
	dirParts := pathParts(dir)
	patternParts := pathParts(pattern)
	if len(dirParts) >= len(patternParts) {
		return false
	}
	for i, part := range dirParts {
		ok, err := filepath.Match(patternParts[i], part)
		if err != nil || !ok {
			return false
		}
	}
	return true
}

func opAllowed(rule Rule, op Op) bool {
	return slices.Contains(rule.Ops, op)
}

func ruleExpired(rule Rule, now time.Time) bool {
	return rule.ExpiresAt != nil && !now.IsZero() && !rule.ExpiresAt.After(now)
}

func validateSource(source Source) error {
	switch source {
	case SourceProfile, SourceEnvironment, SourceRun:
		return nil
	default:
		return fmt.Errorf("unsupported HostFS source %q", source)
	}
}

func normalizeSubject(subject Subject, source Source) (Subject, error) {
	want := defaultSubject(source)
	if subject == "" {
		return want, nil
	}
	switch subject {
	case SubjectProfile, SubjectEnvironment, SubjectSession, SubjectCommand:
	default:
		return "", fmt.Errorf("unsupported HostFS subject %q", subject)
	}
	if subject != want {
		return "", fmt.Errorf("HostFS subject %q does not match %s source", subject, source)
	}
	return subject, nil
}

func defaultSubject(source Source) Subject {
	switch source {
	case SourceProfile:
		return SubjectProfile
	case SourceEnvironment:
		return SubjectEnvironment
	default:
		return SubjectSession
	}
}

func normalizeTTL(ttl TTL, source Source) (TTL, error) {
	want := defaultTTL(source)
	if ttl == "" {
		return want, nil
	}
	switch ttl {
	case TTLRun, TTLSession, TTLEnvironment, TTLProfile:
	default:
		return "", fmt.Errorf("unsupported HostFS ttl %q", ttl)
	}
	if ttl != want {
		return "", fmt.Errorf("HostFS ttl %q does not match %s source", ttl, source)
	}
	return ttl, nil
}

func defaultTTL(source Source) TTL {
	switch source {
	case SourceProfile:
		return TTLProfile
	case SourceEnvironment:
		return TTLEnvironment
	default:
		return TTLRun
	}
}

func validateScope(scope Scope) error {
	switch scope {
	case ScopeExactFile, ScopeGlob, ScopeDir, ScopeRecursiveDir:
		return nil
	default:
		return fmt.Errorf("unsupported HostFS scope %q", scope)
	}
}

func validateGlobPattern(pattern string) error {
	if !hasGlobMeta(pattern) {
		return errors.New("glob HostFS scope requires a pattern containing *, ?, or [")
	}
	if _, err := filepath.Match(pattern, pattern); err != nil {
		return fmt.Errorf("invalid HostFS glob pattern %q: %w", pattern, err)
	}
	return nil
}

func hasGlobMeta(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

func validateExpiry(rule Rule) error {
	if rule.CreatedAt != nil && rule.ExpiresAt != nil && rule.ExpiresAt.Before(*rule.CreatedAt) {
		return errors.New("expiresAt must not be before createdAt")
	}
	return nil
}

func cleanHostPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("hostPath is required")
	}
	if strings.ContainsRune(path, 0) {
		return "", errors.New("hostPath must not contain NUL")
	}
	if strings.Contains(path, `\`) {
		return "", errors.New("hostPath must use slash-separated paths")
	}
	if strings.Contains(path, "://") || strings.HasPrefix(path, "//") {
		return "", fmt.Errorf("hostPath %q must be an absolute local path", path)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("hostPath %q must be absolute", path)
	}
	clean := filepath.Clean(path)
	if clean == string(filepath.Separator) {
		return "", errors.New("hostPath must not be host root")
	}
	return clean, nil
}

func cleanGuestPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("guestPath is required when present")
	}
	if strings.ContainsRune(path, 0) {
		return "", errors.New("guestPath must not contain NUL")
	}
	if strings.Contains(path, `\`) {
		return "", errors.New("guestPath must use slash-separated paths")
	}
	if strings.Contains(path, "://") || strings.HasPrefix(path, "//") {
		return "", fmt.Errorf("guestPath %q must be an absolute guest path", path)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("guestPath %q must be absolute", path)
	}
	clean := filepath.Clean(path)
	if clean == string(filepath.Separator) {
		return "", errors.New("guestPath must not be guest root")
	}
	return clean, nil
}

func isBroadHostRoot(path string, scope Scope) bool {
	if scope == ScopeExactFile {
		return false
	}
	if path == "/Users" || path == "/Volumes" || path == "/private" {
		return true
	}
	if scope != ScopeRecursiveDir {
		return false
	}
	parts := pathParts(path)
	if len(parts) <= 1 {
		return true
	}
	if len(parts) == 2 && parts[0] == "Users" {
		return true
	}
	return false
}

func pathParts(path string) []string {
	clean := strings.Trim(filepath.Clean(path), string(filepath.Separator))
	if clean == "" {
		return nil
	}
	return strings.Split(clean, string(filepath.Separator))
}

func nowOrDefault(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now
}
