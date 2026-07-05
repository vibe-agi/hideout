package hostfs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultMaxReadBytes int64 = 1 << 20

var (
	ErrNotFound    = errors.New("hostfs path not found")
	ErrUnsupported = errors.New("hostfs operation unsupported")
)

type Service struct {
	Policy       EffectivePolicy
	MaxReadBytes int64
}

type NodeInfo struct {
	Kind    string    `json:"kind"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"`
	ModTime time.Time `json:"modTime"`
}

type ReadResult struct {
	Info  NodeInfo `json:"info"`
	Data  []byte   `json:"data"`
	Bytes int      `json:"bytes"`
	EOF   bool     `json:"eof"`
}

type DirEntry struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Size int64  `json:"size"`
	Mode string `json:"mode"`
}

func NewService(policy EffectivePolicy) Service {
	return Service{Policy: canonicalizePolicy(policy), MaxReadBytes: DefaultMaxReadBytes}
}

func (s Service) Stat(path string) (NodeInfo, error) {
	if s.syntheticDir(path) {
		return syntheticDirInfo(), nil
	}
	resolved, err := s.authorizePath(OpStat, path)
	if err != nil {
		return NodeInfo{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return NodeInfo{}, hidePathErr(err)
	}
	return nodeInfo(info), nil
}

func (s Service) Read(path string, offset, size int64) (ReadResult, error) {
	if offset < 0 {
		return ReadResult{}, errors.New("hostfs read offset must be non-negative")
	}
	if size < 0 {
		return ReadResult{}, errors.New("hostfs read size must be non-negative")
	}
	maxRead := s.MaxReadBytes
	if maxRead <= 0 {
		maxRead = DefaultMaxReadBytes
	}
	if size == 0 || size > maxRead {
		size = maxRead
	}
	resolved, err := s.authorizePath(OpRead, path)
	if err != nil {
		return ReadResult{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return ReadResult{}, hidePathErr(err)
	}
	if !info.Mode().IsRegular() {
		return ReadResult{}, ErrNotFound
	}
	f, err := os.Open(resolved)
	if err != nil {
		return ReadResult{}, hidePathErr(err)
	}
	defer f.Close()
	buf := make([]byte, size)
	n, err := f.ReadAt(buf, offset)
	eof := errors.Is(err, io.EOF)
	if err != nil && !eof {
		return ReadResult{}, hidePathErr(err)
	}
	buf = buf[:n]
	if n == 0 {
		buf = nil
	}
	return ReadResult{
		Info:  nodeInfo(info),
		Data:  buf,
		Bytes: n,
		EOF:   eof || offset+int64(n) >= info.Size(),
	}, nil
}

func (s Service) List(path string) ([]DirEntry, error) {
	dir, err := s.authorizePath(OpList, path)
	if err != nil {
		clean, cleanErr := cleanHostPath(path)
		if cleanErr != nil || !s.syntheticDir(clean) {
			return nil, ErrNotFound
		}
		return s.syntheticEntries(clean), nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, hidePathErr(err)
	}
	out := make([]DirEntry, 0, len(entries))
	for _, entry := range entries {
		requestedChild := filepath.Join(dir, entry.Name())
		resolvedChild := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if !s.entryVisible(requestedChild, resolvedChild, info) {
			continue
		}
		out = append(out, DirEntry{
			Name: entry.Name(),
			Kind: nodeKind(info),
			Size: info.Size(),
			Mode: info.Mode().String(),
		})
	}
	out = appendMissingEntries(out, s.syntheticEntries(dir)...)
	return out, nil
}

func (s Service) WriteUnsupported() error {
	return ErrUnsupported
}

func (s Service) authorizePath(op Op, path string) (string, error) {
	clean, err := cleanHostPath(path)
	if err != nil {
		return "", ErrNotFound
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", hidePathErr(err)
	}
	cleanDecision := s.Policy.Decide(op, clean)
	resolvedDecision := s.Policy.Decide(op, resolved)
	if cleanDecision.Effect == "deny" || resolvedDecision.Effect == "deny" {
		return "", ErrNotFound
	}
	if resolved != clean {
		if !resolvedDecision.Allowed {
			return "", ErrNotFound
		}
		return resolved, nil
	}
	if !cleanDecision.Allowed {
		return "", ErrNotFound
	}
	return resolved, nil
}

func (s Service) entryVisible(requestedChild, resolvedChild string, info os.FileInfo) bool {
	if s.pathVisible(OpStat, requestedChild, resolvedChild) {
		return true
	}
	if !info.IsDir() && s.pathVisible(OpRead, requestedChild, resolvedChild) {
		return true
	}
	if info.IsDir() && s.pathVisible(OpList, requestedChild, resolvedChild) {
		return true
	}
	return false
}

func (s Service) pathVisible(op Op, requested, resolved string) bool {
	canonical, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return false
	}
	requestedDecision := s.Policy.Decide(op, requested)
	canonicalDecision := s.Policy.Decide(op, canonical)
	if requestedDecision.Effect == "deny" || canonicalDecision.Effect == "deny" {
		return false
	}
	if canonical != requested {
		if !canonicalDecision.Allowed {
			return false
		}
		return true
	}
	return requestedDecision.Allowed
}

func canonicalizePolicy(policy EffectivePolicy) EffectivePolicy {
	out := EffectivePolicy{
		Grants:        make([]Grant, len(policy.Grants)),
		Deny:          make([]Grant, len(policy.Deny)),
		Now:           policy.Now,
		ReservedRoots: append([]string(nil), policy.ReservedRoots...),
	}
	copy(out.Grants, policy.Grants)
	copy(out.Deny, policy.Deny)
	for i := range out.Grants {
		out.Grants[i].Rule = canonicalizeRule(out.Grants[i].Rule)
	}
	for i := range out.Deny {
		out.Deny[i].Rule = canonicalizeRule(out.Deny[i].Rule)
	}
	for i, root := range out.ReservedRoots {
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			out.ReservedRoots[i] = filepath.Clean(resolved)
		}
	}
	return out
}

func canonicalizeRule(rule Rule) Rule {
	if rule.Scope == ScopeGlob {
		rule.HostPath = canonicalizeGlobPattern(rule.HostPath)
		return rule
	}
	if resolved, err := filepath.EvalSymlinks(rule.HostPath); err == nil {
		rule.HostPath = resolved
	}
	return rule
}

func canonicalizeGlobPattern(pattern string) string {
	index := firstUnescapedGlobMeta(pattern)
	if index < 0 {
		return pattern
	}
	prefix := pattern[:index]
	lastSeparator := strings.LastIndex(prefix, string(filepath.Separator))
	if lastSeparator <= 0 {
		return pattern
	}
	base := prefix[:lastSeparator]
	rest := pattern[lastSeparator:]
	resolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		return pattern
	}
	return filepath.Clean(resolved) + rest
}

func hidePathErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		return ErrNotFound
	}
	return ErrNotFound
}

func nodeInfo(info os.FileInfo) NodeInfo {
	return NodeInfo{
		Kind:    nodeKind(info),
		Size:    info.Size(),
		Mode:    info.Mode().String(),
		ModTime: info.ModTime().UTC(),
	}
}

func syntheticDirInfo() NodeInfo {
	return NodeInfo{
		Kind:    "dir",
		Mode:    "dr-xr-xr-x",
		ModTime: time.Time{},
	}
}

func (s Service) syntheticDir(path string) bool {
	clean, err := cleanHostPath(path)
	if err != nil {
		return false
	}
	candidates := []string{clean}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil && resolved != clean {
		candidates = append(candidates, resolved)
	}
	for _, grant := range s.Policy.Grants {
		if ruleExpired(grant.Rule, s.Policy.Now) {
			continue
		}
		for _, candidate := range candidates {
			if grant.Scope == ScopeGlob && globCouldMatchChildOf(grant.HostPath, candidate) {
				return true
			}
			if isAncestorPath(candidate, grant.HostPath) {
				return true
			}
		}
	}
	return false
}

func (s Service) syntheticEntries(path string) []DirEntry {
	clean, err := cleanHostPath(path)
	if err != nil {
		return nil
	}
	candidates := []string{clean}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil && resolved != clean {
		candidates = append(candidates, resolved)
	}
	seen := map[string]bool{}
	var entries []DirEntry
	for _, grant := range s.Policy.Grants {
		if ruleExpired(grant.Rule, s.Policy.Now) {
			continue
		}
		for _, candidate := range candidates {
			if grant.Scope == ScopeGlob {
				continue
			}
			name, ok := nextPathComponent(candidate, grant.HostPath)
			if !ok || seen[name] {
				continue
			}
			seen[name] = true
			kind := "dir"
			if filepath.Join(candidate, name) == grant.HostPath && grant.Scope == ScopeExactFile {
				kind = "file"
			}
			entries = append(entries, DirEntry{
				Name: name,
				Kind: kind,
				Mode: syntheticMode(kind),
			})
		}
	}
	return entries
}

func syntheticMode(kind string) string {
	if kind == "file" {
		return "-r--r--r--"
	}
	return "dr-xr-xr-x"
}

func appendMissingEntries(entries []DirEntry, synthetic ...DirEntry) []DirEntry {
	seen := map[string]bool{}
	for _, entry := range entries {
		seen[entry.Name] = true
	}
	for _, entry := range synthetic {
		if seen[entry.Name] {
			continue
		}
		entries = append(entries, entry)
		seen[entry.Name] = true
	}
	return entries
}

func isAncestorPath(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && !pathEscapes(rel)
}

func nextPathComponent(parent, child string) (string, bool) {
	if !isAncestorPath(parent, child) {
		return "", false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil || pathEscapes(rel) {
		return "", false
	}
	first, _, _ := strings.Cut(rel, string(filepath.Separator))
	return first, first != "" && first != "."
}

func pathEscapes(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func nodeKind(info os.FileInfo) string {
	switch {
	case info.IsDir():
		return "dir"
	case info.Mode().IsRegular():
		return "file"
	default:
		return "other"
	}
}

func ReadResultDataString(result ReadResult) string {
	return string(bytes.ToValidUTF8(result.Data, []byte{}))
}

func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrUnsupported) {
		return ErrUnsupported
	}
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return fmt.Errorf("hostfs operation failed")
}
