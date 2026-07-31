package hostfs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/hostfs/overlay"
)

const (
	DefaultMaxReadBytes              int64 = 1 << 20
	DefaultMaxListEntries                  = 4096
	DefaultMaxConcurrentEnumerations       = 4
)

var (
	ErrNotFound                    = errors.New("hostfs path not found")
	ErrUnsupported                 = errors.New("hostfs operation unsupported")
	ErrReadApprovalRequired        = errors.New("hostfs read requires operator approval")
	ErrReadDenied                  = errors.New("hostfs read denied")
	ErrDirectoryNotEnumerable      = errors.New("hostfs directory is not enumerable")
	ErrDirectoryIncomplete         = errors.New("hostfs directory listing is incomplete")
	ErrWriteUnauthorized           = errors.New("hostfs write is unauthorized")
	ErrHostPrerequisite            = errors.New("hostfs host prerequisite failed")
	ErrHostAppResourceUnauthorized = errors.New("hostfs resource lacks active content authority")
	ErrHostAppResourceOwner        = errors.New("hostfs portal owner is not live")
	ErrHostAppResourceChanged      = errors.New("hostfs resource changed before launch")
)

type AccessError struct {
	Kind          error
	RequestedPath string
	CanonicalPath string
	Visibility    VisibilityResult
	Cause         error
}

func (e *AccessError) Error() string {
	if e == nil || e.Kind == nil {
		return "hostfs access failed"
	}
	return e.Kind.Error()
}

func (e *AccessError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Kind
}

type Service struct {
	Policy         EffectivePolicy
	MaxReadBytes   int64
	MaxListEntries int
	Overlay        *overlay.Store
	Context        OverlayContext
	ReadAuthority  ReadAuthority
	// BeginStage records retained lifecycle metadata before Overlay.Stage can
	// create durable state. The completion callback is called exactly once.
	BeginStage func() (complete func(staged bool) error, err error)
	enumSem    chan struct{}
	readDir    func(string) ([]os.DirEntry, error)
	lstat      func(string) (os.FileInfo, error)
	now        func() time.Time
}

type ReadGrantCheck struct {
	RequestedPath string
	CanonicalPath string
	Visibility    VisibilityResult
}

type ReadAuthority interface {
	AllowsRead(ReadGrantCheck) (bool, error)
}

const (
	HostAppResourceFile = "file"
	HostAppResourceTree = "tree"
)

// HostAppResourceOwner is supplied by the authenticated run broker. The
// authority implementation must independently prove that this exact session
// still owns the HostFS portal.
type HostAppResourceOwner struct {
	SessionID string `json:"-"`
	Profile   string `json:"-"`
}

// HostAppResourceCheck is an internal handoff to Manager's live HostFS
// authority provider. Path and policy facts are deliberately non-serializable.
type HostAppResourceCheck struct {
	Owner             HostAppResourceOwner `json:"-"`
	RequestedPath     string               `json:"-"`
	CanonicalPath     string               `json:"-"`
	ResourceType      string               `json:"-"`
	Operation         Op                   `json:"-"`
	RequestedDecision Decision             `json:"-"`
	CanonicalDecision Decision             `json:"-"`
	DynamicRead       bool                 `json:"-"`
	Revalidation      bool                 `json:"-"`
}

// HostAppResourceValidator proves live portal ownership and checks any
// mutable Manager-owned policy state. It is intentionally stronger than the
// ordinary read callback because host-app launch consumes authority without
// creating a HostFS grant.
type HostAppResourceValidator interface {
	ValidateHostAppResource(HostAppResourceCheck) error
}

type HostAppResourceReadAuthority interface {
	AllowsHostAppResourceRead(ReadGrantCheck) (bool, error)
}

// HostAppResourceLossReporter lets Manager stale already-approved app
// decisions when authority disappears during final launch revalidation.
type HostAppResourceLossReporter interface {
	HostAppResourceAuthorityLost(HostAppResourceOwner, string)
}

// HostAppResource is an opaque Core-only authorization handle. All fields are
// private so accidental JSON encoding cannot expose a lower host path, policy
// token, or canonical target.
type HostAppResource struct {
	requestedPath  string
	canonicalPath  string
	relativeTarget string
	resourceType   string
	operation      Op
	info           os.FileInfo
}

func (r HostAppResource) HostPath() string       { return r.canonicalPath }
func (r HostAppResource) RelativeTarget() string { return r.relativeTarget }
func (r HostAppResource) ResourceType() string   { return r.resourceType }

type OverlayContext struct {
	SessionID string
	Profile   string
	Backend   string
	Privilege overlay.Privilege
}

type NodeInfo struct {
	Kind    string    `json:"kind"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"`
	ModTime time.Time `json:"modTime"`
	Locked  bool      `json:"locked,omitempty"`
	Caps    []string  `json:"caps,omitempty"`
	Coarse  bool      `json:"-"`
}

type ReadResult struct {
	Info  NodeInfo `json:"info"`
	Data  []byte   `json:"data"`
	Bytes int      `json:"bytes"`
	EOF   bool     `json:"eof"`
}

type DirEntry struct {
	Name   string   `json:"name"`
	Kind   string   `json:"kind"`
	Size   int64    `json:"size"`
	Mode   string   `json:"mode"`
	Locked bool     `json:"locked,omitempty"`
	Caps   []string `json:"caps,omitempty"`
	Coarse bool     `json:"-"`
}

type WriteRequest struct {
	Op              Op
	Path            string
	DestinationPath string
	Data            []byte
	Offset          int64
	Size            int64
	Mode            string
	UID             *int64
	GID             *int64
}

type WriteResult struct {
	OperationID string `json:"operationId"`
	DecisionID  string `json:"decisionId"`
	Staged      bool   `json:"staged"`
	HostChanged bool   `json:"hostChanged"`
}

func NewService(policy EffectivePolicy) Service {
	return Service{
		Policy:         canonicalizePolicy(policy),
		MaxReadBytes:   DefaultMaxReadBytes,
		MaxListEntries: DefaultMaxListEntries,
		enumSem:        make(chan struct{}, DefaultMaxConcurrentEnumerations),
		now:            time.Now,
	}
}

func (s Service) Stat(path string) (NodeInfo, error) {
	if info, ok, err := s.overlayInfo(path); err != nil || ok {
		if err != nil {
			return NodeInfo{}, err
		}
		return info, nil
	}
	if s.syntheticDir(path) {
		return syntheticDirInfo(), nil
	}
	if resolved, err := s.authorizePath(OpStat, path); err == nil {
		info, statErr := os.Stat(resolved)
		if statErr != nil {
			return NodeInfo{}, hidePathErr(statErr)
		}
		return nodeInfo(info), nil
	}
	clean, err := cleanHostPath(path)
	if err != nil {
		return NodeInfo{}, ErrNotFound
	}
	visibility := s.Policy.Visibility(clean)
	if visibility.DepthExceeded {
		return NodeInfo{}, &AccessError{Kind: ErrDirectoryIncomplete, RequestedPath: clean, Visibility: visibility}
	}
	if visibility.State == VisibilityHidden {
		return NodeInfo{}, ErrNotFound
	}
	if resolved, info, allowed, grantErr := s.activeSessionRead(path); grantErr != nil {
		return NodeInfo{}, grantErr
	} else if allowed {
		_ = resolved
		return nodeInfo(info), nil
	}
	info, err := s.lstatPath(clean)
	if err != nil {
		return NodeInfo{}, s.discoveryHostError(clean, visibility, err)
	}
	return coarseNodeInfo(info), nil
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
	if result, ok, err := s.overlayRead(path, offset, size); err != nil || ok {
		return result, err
	}
	resolved, err := s.authorizePath(OpRead, path)
	var info os.FileInfo
	if err != nil {
		var allowed bool
		resolved, info, allowed, err = s.activeSessionRead(path)
		if err != nil {
			return ReadResult{}, err
		}
		if !allowed {
			return ReadResult{}, s.readDeniedError(path)
		}
	}
	if info == nil {
		info, err = os.Stat(resolved)
		if err != nil {
			return ReadResult{}, hidePathErr(err)
		}
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
	clean, cleanErr := cleanHostPath(path)
	if cleanErr != nil {
		return nil, ErrNotFound
	}
	visibility := s.Policy.Visibility(clean)
	if visibility.DepthExceeded {
		return nil, &AccessError{Kind: ErrDirectoryIncomplete, RequestedPath: clean, Visibility: visibility}
	}
	if visibility.State == VisibilityHidden && visibility.ExplicitDomain {
		return nil, ErrNotFound
	}
	if visibility.ExplicitDomain {
		if dir, ok := s.directlyAuthorizedPath(OpList, clean); ok {
			return s.listLegacy(dir)
		}
		if !visibility.Enumerable {
			return nil, &AccessError{Kind: ErrDirectoryNotEnumerable, RequestedPath: clean, Visibility: visibility}
		}
		return s.listDiscovered(clean, visibility)
	}
	dir, err := s.authorizePath(OpList, path)
	if err == nil {
		return s.listLegacy(dir)
	}
	if !s.syntheticDir(clean) {
		return nil, ErrNotFound
	}
	return s.syntheticEntries(clean), nil
}

func (s Service) directlyAuthorizedPath(op Op, path string) (string, bool) {
	resolved, err := s.authorizePath(op, path)
	if err != nil {
		return "", false
	}
	decision := s.Policy.Decide(op, path)
	if resolved != filepath.Clean(path) {
		decision = s.Policy.Decide(op, resolved)
	}
	for _, grant := range s.Policy.Grants {
		if grant.ID == decision.RuleID && grant.Source == decision.Source && opAllowed(grant.Rule, op) {
			return resolved, true
		}
	}
	return "", false
}

func (s Service) listLegacy(dir string) ([]DirEntry, error) {
	entries, err := s.readDirPath(dir)
	if err != nil {
		return nil, hidePathErr(err)
	}
	out := make([]DirEntry, 0, len(entries))
	for _, entry := range entries {
		requestedChild := filepath.Join(dir, entry.Name())
		resolvedChild := filepath.Join(dir, entry.Name())
		if s.Policy.Visibility(requestedChild).DiscoverDenied {
			continue
		}
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
	if s.Overlay != nil {
		overlayEntries, err := s.Overlay.Entries(dir)
		if err != nil {
			return nil, err
		}
		out = mergeOverlayEntries(out, overlayEntries)
	}
	return out, nil
}

func (s Service) listDiscovered(dir string, rootVisibility VisibilityResult) ([]DirEntry, error) {
	if !s.acquireEnumeration() {
		return nil, &AccessError{Kind: ErrDirectoryIncomplete, RequestedPath: dir, Visibility: rootVisibility}
	}
	defer s.releaseEnumeration()
	entries, err := s.readDirPath(dir)
	if err != nil {
		return nil, s.discoveryHostError(dir, rootVisibility, err)
	}
	limit := s.MaxListEntries
	if limit <= 0 {
		limit = DefaultMaxListEntries
	}
	if len(entries) > limit {
		return nil, &AccessError{Kind: ErrDirectoryIncomplete, RequestedPath: dir, Visibility: rootVisibility}
	}
	out := make([]DirEntry, 0, len(entries))
	for _, entry := range entries {
		child := filepath.Join(dir, entry.Name())
		info, err := s.lstatPath(child)
		if err != nil {
			return nil, s.discoveryHostError(child, rootVisibility, err)
		}
		visibility := s.Policy.Visibility(child)
		if visibility.DepthExceeded {
			return nil, &AccessError{Kind: ErrDirectoryIncomplete, RequestedPath: child, Visibility: visibility}
		}
		// Discover denial controls parent enumeration even when an independent
		// content grant keeps exact access to the child usable.
		if visibility.DiscoverDenied || visibility.State == VisibilityHidden {
			continue
		}
		if visibility.State == VisibilityContentGranted {
			out = append(out, DirEntry{Name: entry.Name(), Kind: nodeKind(info), Size: info.Size(), Mode: info.Mode().String()})
			continue
		}
		out = append(out, coarseDirEntry(entry.Name(), info))
	}
	return out, nil
}

func (s Service) WriteUnsupported() error {
	return ErrUnsupported
}

func (s Service) StageWrite(req WriteRequest) (WriteResult, error) {
	if s.Overlay == nil {
		if visibility := s.Policy.Visibility(req.Path); visibility.ExplicitDomain && visibility.State != VisibilityHidden {
			return WriteResult{}, &AccessError{Kind: ErrWriteUnauthorized, RequestedPath: req.Path, Visibility: visibility}
		}
		return WriteResult{}, ErrUnsupported
	}
	decision, err := s.authorizeWritePath(req.Op, req.Path)
	if err != nil {
		return WriteResult{}, err
	}
	if req.DestinationPath != "" {
		if _, err := s.authorizeWritePath(req.Op, req.DestinationPath); err != nil {
			return WriteResult{}, err
		}
	}
	complete := func(bool) error { return nil }
	if s.BeginStage != nil {
		complete, err = s.BeginStage()
		if err != nil {
			return WriteResult{}, err
		}
	}
	stageReq := overlay.StageRequest{
		SessionID:       s.Context.SessionID,
		Profile:         s.Context.Profile,
		Backend:         s.Context.Backend,
		Operation:       opOperation(req.Op),
		Path:            req.Path,
		DestinationPath: req.DestinationPath,
		GrantID:         decision.RuleID,
		GrantSource:     string(decision.Source),
		Data:            req.Data,
		Offset:          req.Offset,
		Size:            req.Size,
		Mode:            req.Mode,
		Privilege:       s.Context.Privilege,
	}
	if req.UID != nil {
		stageReq.Owner = fmt.Sprintf("%d", *req.UID)
	}
	if req.GID != nil {
		stageReq.Group = fmt.Sprintf("%d", *req.GID)
	}
	result, err := s.Overlay.Stage(stageReq)
	if err != nil {
		return WriteResult{}, errors.Join(err, complete(false))
	}
	if err := complete(true); err != nil {
		return WriteResult{}, err
	}
	return WriteResult{
		OperationID: result.Operation.ID,
		DecisionID:  result.Decision.DecisionID,
		Staged:      true,
		HostChanged: false,
	}, nil
}

func (s Service) overlayInfo(path string) (NodeInfo, bool, error) {
	if s.Overlay == nil {
		return NodeInfo{}, false, nil
	}
	view, ok, err := s.Overlay.View(path)
	if err != nil || !ok {
		return NodeInfo{}, ok, err
	}
	if view.Deleted {
		return NodeInfo{}, true, ErrNotFound
	}
	return NodeInfo{Kind: view.Kind, Size: view.Size, Mode: view.Mode}, true, nil
}

func (s Service) overlayRead(path string, offset, size int64) (ReadResult, bool, error) {
	if s.Overlay == nil {
		return ReadResult{}, false, nil
	}
	view, ok, err := s.Overlay.View(path)
	if err != nil || !ok {
		return ReadResult{}, ok, err
	}
	if view.Deleted || view.Kind != "file" {
		return ReadResult{}, true, ErrNotFound
	}
	if offset > int64(len(view.Data)) {
		return ReadResult{Info: NodeInfo{Kind: "file", Size: int64(len(view.Data)), Mode: view.Mode}, EOF: true}, true, nil
	}
	end := offset + size
	if end > int64(len(view.Data)) {
		end = int64(len(view.Data))
	}
	data := append([]byte(nil), view.Data[offset:end]...)
	return ReadResult{
		Info:  NodeInfo{Kind: "file", Size: int64(len(view.Data)), Mode: view.Mode},
		Data:  data,
		Bytes: len(data),
		EOF:   end >= int64(len(view.Data)),
	}, true, nil
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

func (s Service) authorizeWritePath(op Op, path string) (Decision, error) {
	clean, err := cleanHostPath(path)
	if err != nil {
		return Decision{}, ErrNotFound
	}
	if pathInReservedRoots(clean, s.Policy.ReservedRoots) {
		return Decision{}, ErrNotFound
	}
	visibility := s.Policy.Visibility(clean)
	unauthorized := func(decision Decision) (Decision, error) {
		if visibility.ExplicitDomain && visibility.State != VisibilityHidden {
			return decision, &AccessError{Kind: ErrWriteUnauthorized, RequestedPath: clean, Visibility: visibility}
		}
		return decision, ErrNotFound
	}
	cleanDecision := s.Policy.Decide(op, clean)
	if cleanDecision.Effect == "deny" {
		return unauthorized(cleanDecision)
	}
	if info, err := os.Lstat(clean); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return Decision{}, ErrNotFound
		}
		if resolved, err := filepath.EvalSymlinks(clean); err == nil && resolved != clean {
			resolvedDecision := s.Policy.Decide(op, resolved)
			if resolvedDecision.Effect == "deny" || !resolvedDecision.Allowed {
				resolvedVisibility := s.Policy.Visibility(resolved)
				if visibility.ExplicitDomain && visibility.State != VisibilityHidden && resolvedVisibility.ExplicitDomain && resolvedVisibility.State != VisibilityHidden {
					return cleanDecision, &AccessError{Kind: ErrWriteUnauthorized, RequestedPath: clean, Visibility: visibility}
				}
				return resolvedDecision, ErrNotFound
			}
			return resolvedDecision, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Decision{}, ErrNotFound
	}
	if !cleanDecision.Allowed {
		return unauthorized(cleanDecision)
	}
	return cleanDecision, nil
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
		return canonicalDecision.Allowed
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
	if slices.Contains(rule.Ops, OpDiscover) {
		rule.HostPath = visibilityPolicyPath(rule.HostPath)
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

// ResolveHostAppResource consumes existing HostFS file/tree authority for one
// host-app launch. It never creates a grant. The returned handle is opaque and
// must be revalidated immediately before the launcher side effect.
func (s Service) ResolveHostAppResource(owner HostAppResourceOwner, path string) (HostAppResource, error) {
	return s.resolveHostAppResource(owner, path, false)
}

// RevalidateHostAppResource proves that owner, policy, canonical target, and
// file identity are unchanged. Manager is notified when a previously valid
// handle loses authority so an approved app decision cannot restore it.
func (s Service) RevalidateHostAppResource(owner HostAppResourceOwner, previous HostAppResource) error {
	current, err := s.resolveHostAppResource(owner, previous.requestedPath, true)
	if err == nil {
		if current.canonicalPath != previous.canonicalPath || current.resourceType != previous.resourceType || current.operation != previous.operation || current.info == nil || previous.info == nil || !os.SameFile(current.info, previous.info) {
			err = ErrHostAppResourceChanged
		}
	}
	if err != nil {
		s.reportHostAppResourceLoss(owner, "hostfs-resource-authority-lost")
		if errors.Is(err, ErrHostAppResourceOwner) || errors.Is(err, ErrHostAppResourceUnauthorized) {
			return err
		}
		return ErrHostAppResourceChanged
	}
	return nil
}

func (s Service) resolveHostAppResource(owner HostAppResourceOwner, path string, revalidation bool) (HostAppResource, error) {
	validator, ok := s.ReadAuthority.(HostAppResourceValidator)
	if !ok || strings.TrimSpace(owner.SessionID) == "" || strings.TrimSpace(owner.Profile) == "" {
		return HostAppResource{}, ErrHostAppResourceOwner
	}
	clean, err := cleanHostPath(path)
	if err != nil {
		return HostAppResource{}, ErrHostAppResourceUnauthorized
	}
	policy := s.Policy
	if s.now != nil {
		policy.Now = s.now().UTC()
	} else {
		policy.Now = time.Now().UTC()
	}
	if pathInReservedRoots(clean, policy.ReservedRoots) {
		return HostAppResource{}, ErrHostAppResourceUnauthorized
	}
	canonical, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return HostAppResource{}, ErrHostAppResourceUnauthorized
	}
	canonical = filepath.Clean(canonical)
	if pathInReservedRoots(canonical, policy.ReservedRoots) {
		return HostAppResource{}, ErrHostAppResourceUnauthorized
	}
	info, err := os.Stat(canonical)
	if err != nil || (!info.Mode().IsRegular() && !info.IsDir()) {
		return HostAppResource{}, ErrHostAppResourceUnauthorized
	}
	operation := OpRead
	resourceType := HostAppResourceFile
	if info.IsDir() {
		operation = OpList
		resourceType = HostAppResourceTree
	}
	requestedDecision := policy.Decide(operation, clean)
	canonicalDecision := policy.Decide(operation, canonical)
	if requestedDecision.Effect == "deny" || canonicalDecision.Effect == "deny" {
		return HostAppResource{}, ErrHostAppResourceUnauthorized
	}
	if resourceType == HostAppResourceTree && !policy.AuthorizesHostAppTree(canonical) {
		return HostAppResource{}, ErrHostAppResourceUnauthorized
	}
	direct := requestedDecision.Allowed
	if canonical != clean {
		direct = canonicalDecision.Allowed
	}
	dynamicRead := false
	if !direct {
		if operation != OpRead {
			return HostAppResource{}, ErrHostAppResourceUnauthorized
		}
		visibility := policy.Visibility(clean)
		canonicalVisibility := policy.Visibility(canonical)
		if !visibility.ExplicitDomain || visibility.State == VisibilityHidden || !canonicalVisibility.ExplicitDomain || canonicalVisibility.State == VisibilityHidden {
			return HostAppResource{}, ErrHostAppResourceUnauthorized
		}
		readCheck := ReadGrantCheck{RequestedPath: clean, CanonicalPath: canonical, Visibility: visibility}
		var allowed bool
		var grantErr error
		if authority, ok := s.ReadAuthority.(HostAppResourceReadAuthority); ok {
			allowed, grantErr = authority.AllowsHostAppResourceRead(readCheck)
		} else {
			allowed, grantErr = s.ReadAuthority.AllowsRead(readCheck)
		}
		if grantErr != nil {
			if errors.Is(grantErr, ErrHostAppResourceUnauthorized) {
				return HostAppResource{}, ErrHostAppResourceUnauthorized
			}
			return HostAppResource{}, ErrHostAppResourceOwner
		}
		if !allowed {
			return HostAppResource{}, ErrHostAppResourceUnauthorized
		}
		dynamicRead = true
	}
	check := HostAppResourceCheck{
		Owner: owner, RequestedPath: clean, CanonicalPath: canonical, ResourceType: resourceType, Operation: operation,
		RequestedDecision: requestedDecision, CanonicalDecision: canonicalDecision,
		DynamicRead: dynamicRead, Revalidation: revalidation,
	}
	if err := validator.ValidateHostAppResource(check); err != nil {
		if errors.Is(err, ErrHostAppResourceUnauthorized) {
			return HostAppResource{}, ErrHostAppResourceUnauthorized
		}
		return HostAppResource{}, ErrHostAppResourceOwner
	}
	relative := boundedHostAppRelative(filepath.Base(clean))
	return HostAppResource{
		requestedPath: clean, canonicalPath: canonical, relativeTarget: relative,
		resourceType: resourceType, operation: operation, info: info,
	}, nil
}

// AuthorizesHostAppTree proves that exposing one lower directory cannot widen
// narrower HostFS policy. A host app receives the whole tree directly, so a
// root-only list grant or an intersecting child deny is insufficient.
func (p EffectivePolicy) AuthorizesHostAppTree(path string) bool {
	root, err := cleanHostPath(path)
	if err != nil {
		return false
	}
	for _, reserved := range p.ReservedRoots {
		if pathInRoot(root, reserved) || pathInRoot(reserved, root) {
			return false
		}
	}
	for _, op := range []Op{OpRead, OpList} {
		allowed := false
		for _, grant := range p.Grants {
			if grant.Overlay || grant.Scope != ScopeRecursiveDir || ruleExpired(grant.Rule, p.Now) || !opAllowed(grant.Rule, op) {
				continue
			}
			if ruleMatches(grant.Rule, op, root, false) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	for _, deny := range p.Deny {
		if ruleExpired(deny.Rule, p.Now) || !hostAppTreeDenyRelevant(deny.Rule) {
			continue
		}
		if hostAppTreeSelectorIntersects(deny.Rule, root) {
			return false
		}
	}
	return true
}

func hostAppTreeDenyRelevant(rule Rule) bool {
	if len(rule.Ops) == 0 {
		return true
	}
	for _, op := range []Op{OpDiscover, OpStat, OpRead, OpList} {
		if opAllowed(rule, op) {
			return true
		}
	}
	return false
}

func hostAppTreeSelectorIntersects(rule Rule, root string) bool {
	switch rule.Scope {
	case ScopeExactFile:
		return pathInRoot(rule.HostPath, root)
	case ScopeDir:
		return pathInRoot(rule.HostPath, root) || filepath.Dir(root) == rule.HostPath
	case ScopeRecursiveDir:
		return pathInRoot(rule.HostPath, root) || pathInRoot(root, rule.HostPath)
	case ScopeGlob:
		base := globStaticBase(rule.HostPath)
		return pathInRoot(base, root) || globMatches(rule.HostPath, root) || globCouldMatchChildOf(rule.HostPath, root)
	default:
		return true
	}
}

func (s Service) reportHostAppResourceLoss(owner HostAppResourceOwner, reason string) {
	reporter, ok := s.ReadAuthority.(HostAppResourceLossReporter)
	if ok {
		reporter.HostAppResourceAuthorityLost(owner, reason)
	}
}

func boundedHostAppRelative(value string) string {
	value = strings.TrimSpace(filepath.ToSlash(value))
	if value == "" || strings.HasPrefix(value, "/") || value == ".." || strings.HasPrefix(value, "../") || strings.ContainsRune(value, '\x00') {
		return ""
	}
	for _, r := range value {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return ""
		}
	}
	if len([]byte(value)) > 512 {
		return ""
	}
	return value
}

func (s Service) readDeniedError(path string) error {
	check, _, err := s.readGrantCandidate(path)
	if err != nil {
		return err
	}
	return &AccessError{Kind: ErrReadApprovalRequired, RequestedPath: check.RequestedPath, CanonicalPath: check.CanonicalPath, Visibility: check.Visibility}
}

func (s Service) activeSessionRead(path string) (string, os.FileInfo, bool, error) {
	if s.ReadAuthority == nil {
		return "", nil, false, nil
	}
	check, info, err := s.readGrantCandidate(path)
	if err != nil {
		if errors.Is(err, ErrReadDenied) || errors.Is(err, ErrNotFound) {
			return "", nil, false, nil
		}
		return "", nil, false, err
	}
	allowed, err := s.ReadAuthority.AllowsRead(check)
	if err != nil {
		return "", nil, false, &AccessError{Kind: ErrHostPrerequisite, RequestedPath: check.RequestedPath, CanonicalPath: check.CanonicalPath, Visibility: check.Visibility, Cause: err}
	}
	return check.CanonicalPath, info, allowed, nil
}

func (s Service) readGrantCandidate(path string) (ReadGrantCheck, os.FileInfo, error) {
	clean, err := cleanHostPath(path)
	if err != nil {
		return ReadGrantCheck{}, nil, ErrNotFound
	}
	visibility := s.Policy.Visibility(clean)
	if visibility.State == VisibilityHidden || !visibility.ExplicitDomain {
		return ReadGrantCheck{}, nil, ErrNotFound
	}
	_, err = s.lstatPath(clean)
	if err != nil {
		return ReadGrantCheck{}, nil, s.discoveryHostError(clean, visibility, err)
	}
	canonical, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return ReadGrantCheck{}, nil, s.discoveryHostError(clean, visibility, err)
	}
	if canonical != clean {
		canonicalVisibility := s.Policy.Visibility(canonical)
		if canonicalVisibility.State == VisibilityHidden || !canonicalVisibility.ExplicitDomain {
			return ReadGrantCheck{}, nil, ErrNotFound
		}
	}
	cleanDecision := s.Policy.Decide(OpRead, clean)
	canonicalDecision := s.Policy.Decide(OpRead, canonical)
	if cleanDecision.Effect == "deny" || canonicalDecision.Effect == "deny" {
		return ReadGrantCheck{}, nil, &AccessError{Kind: ErrReadDenied, RequestedPath: clean, CanonicalPath: canonical, Visibility: visibility}
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return ReadGrantCheck{}, nil, s.discoveryHostError(clean, visibility, err)
	}
	if !info.Mode().IsRegular() {
		return ReadGrantCheck{}, nil, &AccessError{Kind: ErrReadDenied, RequestedPath: clean, CanonicalPath: canonical, Visibility: visibility}
	}
	return ReadGrantCheck{RequestedPath: clean, CanonicalPath: canonical, Visibility: visibility}, info, nil
}

func (s Service) discoveryHostError(path string, visibility VisibilityResult, err error) error {
	kind := ErrDirectoryIncomplete
	if errors.Is(err, os.ErrPermission) {
		kind = ErrHostPrerequisite
	} else if errors.Is(err, os.ErrNotExist) {
		kind = ErrNotFound
	}
	return &AccessError{Kind: kind, RequestedPath: path, Visibility: visibility, Cause: err}
}

func (s Service) readDirPath(path string) ([]os.DirEntry, error) {
	if s.readDir != nil {
		return s.readDir(path)
	}
	return os.ReadDir(path)
}

func (s Service) lstatPath(path string) (os.FileInfo, error) {
	if s.lstat != nil {
		return s.lstat(path)
	}
	return os.Lstat(path)
}

func (s Service) acquireEnumeration() bool {
	if s.enumSem == nil {
		return true
	}
	select {
	case s.enumSem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s Service) releaseEnumeration() {
	if s.enumSem == nil {
		return
	}
	<-s.enumSem
}

func nodeInfo(info os.FileInfo) NodeInfo {
	return NodeInfo{
		Kind:    nodeKind(info),
		Size:    info.Size(),
		Mode:    info.Mode().String(),
		ModTime: info.ModTime().UTC(),
	}
}

func coarseNodeInfo(info os.FileInfo) NodeInfo {
	return NodeInfo{Kind: nodeKind(info), Locked: true, Caps: []string{"discover"}, Coarse: true}
}

func coarseDirEntry(name string, info os.FileInfo) DirEntry {
	return DirEntry{Name: name, Kind: nodeKind(info), Locked: true, Caps: []string{"discover"}, Coarse: true}
}

func syntheticDirInfo() NodeInfo {
	return NodeInfo{
		Kind:    "dir",
		Locked:  true,
		Caps:    []string{"discover"},
		Coarse:  true,
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
			if visibility := s.Policy.Visibility(candidate); opAllowed(grant.Rule, OpDiscover) && visibility.DiscoverDenied && visibility.State == VisibilityHidden {
				continue
			}
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
			child := filepath.Join(candidate, name)
			if visibility := s.Policy.Visibility(child); visibility.DiscoverDenied || (opAllowed(grant.Rule, OpDiscover) && visibility.State == VisibilityHidden) {
				continue
			}
			seen[name] = true
			kind := "dir"
			if filepath.Join(candidate, name) == grant.HostPath && grant.Scope == ScopeExactFile {
				kind = "file"
			}
			entries = append(entries, DirEntry{
				Name:   name,
				Kind:   kind,
				Locked: true,
				Caps:   []string{"discover"},
				Coarse: true,
			})
		}
	}
	return entries
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

func mergeOverlayEntries(entries []DirEntry, overlayEntries []overlay.ViewEntry) []DirEntry {
	byName := map[string]DirEntry{}
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	for _, entry := range overlayEntries {
		if entry.Deleted {
			delete(byName, entry.Name)
			continue
		}
		byName[entry.Name] = DirEntry{Name: entry.Name, Kind: entry.Kind, Size: entry.Size, Mode: entry.Mode}
	}
	out := make([]DirEntry, 0, len(byName))
	for _, entry := range byName {
		out = append(out, entry)
	}
	slices.SortFunc(out, func(a, b DirEntry) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

func opOperation(op Op) string {
	switch op {
	case OpCreate:
		return "create"
	case OpWrite:
		return "replace"
	case OpAppend:
		return "append"
	case OpTruncate:
		return "truncate"
	case OpMkdir:
		return "mkdir"
	case OpDelete:
		return "delete"
	case OpRename:
		return "rename"
	case OpChmod:
		return "chmod"
	case OpChown:
		return "chown"
	default:
		return string(op)
	}
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
	if info.Mode()&os.ModeSymlink != 0 {
		return "symlink"
	}
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
	for _, known := range []error{
		ErrReadApprovalRequired,
		ErrReadDenied,
		ErrDirectoryNotEnumerable,
		ErrDirectoryIncomplete,
		ErrWriteUnauthorized,
		ErrHostPrerequisite,
	} {
		if errors.Is(err, known) {
			return known
		}
	}
	return fmt.Errorf("hostfs operation failed")
}
