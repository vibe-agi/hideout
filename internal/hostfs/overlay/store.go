package overlay

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	ErrDenied      = errors.New("hostfs overlay staging denied")
	ErrConflict    = errors.New("hostfs overlay conflict")
	ErrUnsafePath  = errors.New("hostfs overlay unsafe path")
	ErrUnsupported = errors.New("hostfs overlay operation unsupported")
)

type Store struct {
	Root string
	Now  func() time.Time
	mu   sync.Mutex
}

type StageRequest struct {
	SessionID       string
	Profile         string
	Backend         string
	Operation       string
	Path            string
	DestinationPath string
	GrantID         string
	GrantSource     string
	Data            []byte
	Offset          int64
	Size            int64
	Mode            string
	Owner           string
	Group           string
	Privilege       Privilege
}

type StageResult struct {
	Operation Operation `json:"operation"`
	Decision  Decision  `json:"decision"`
}

type View struct {
	Exists  bool
	Deleted bool
	Kind    string
	Size    int64
	Mode    string
	Data    []byte
}

func NewStore(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("hostfs overlay root is required")
	}
	store := &Store{Root: filepath.Clean(root)}
	if err := store.ensure(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Stage(req StageRequest) (StageResult, error) {
	if s == nil {
		return StageResult{}, ErrUnsupported
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
		return StageResult{}, err
	}
	req.Path = filepath.Clean(req.Path)
	if !filepath.IsAbs(req.Path) {
		return StageResult{}, ErrUnsafePath
	}
	if req.DestinationPath != "" {
		req.DestinationPath = filepath.Clean(req.DestinationPath)
		if !filepath.IsAbs(req.DestinationPath) {
			return StageResult{}, ErrUnsafePath
		}
	}
	now := s.now()
	base, err := snapshotPath(req.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return StageResult{}, err
	}
	if base.Kind == "symlink" {
		return StageResult{}, fmt.Errorf("%w: symlink target requires operator apply review", ErrUnsafePath)
	}
	opID, err := newID("hfwop")
	if err != nil {
		return StageResult{}, err
	}
	decID, err := newID("hfwdec")
	if err != nil {
		return StageResult{}, err
	}
	nextSnapshot := Snapshot{}
	contentObject := ""
	preview := Preview{Kind: "summary", Summary: req.Operation}
	switch req.Operation {
	case "create":
		if base.Exists {
			return StageResult{}, fmt.Errorf("%w: create target already exists", ErrConflict)
		}
		contentObject, nextSnapshot, preview, err = s.stageContent(req, nil)
	case "replace":
		content := append([]byte(nil), req.Data...)
		if req.Offset > 0 {
			prior, readErr := s.readCurrentContent(req.Path)
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				return StageResult{}, readErr
			}
			content = patchBytes(prior, req.Offset, req.Data)
		}
		contentObject, nextSnapshot, preview, err = s.writeContentObject(content, "replace")
	case "append":
		prior, readErr := s.readCurrentContent(req.Path)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return StageResult{}, readErr
		}
		content := append(append([]byte(nil), prior...), req.Data...)
		contentObject, nextSnapshot, preview, err = s.writeContentObject(content, "append")
	case "truncate":
		prior, readErr := s.readCurrentContent(req.Path)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return StageResult{}, readErr
		}
		content := resizeBytes(prior, req.Size)
		contentObject, nextSnapshot, preview, err = s.writeContentObject(content, "truncate")
	case "mkdir":
		if base.Exists {
			return StageResult{}, fmt.Errorf("%w: mkdir target already exists", ErrConflict)
		}
		nextSnapshot = Snapshot{Exists: true, Kind: "dir", Mode: modeOrDefault(req.Mode, "drwxr-xr-x"), MTime: now}
		preview = Preview{Kind: "metadata", Summary: "create directory"}
	case "delete":
		if !base.Exists {
			return StageResult{}, fmt.Errorf("%w: delete target missing", ErrConflict)
		}
		nextSnapshot = Snapshot{Exists: false, Kind: "missing", MTime: now}
		preview = Preview{Kind: "metadata", Summary: "delete path"}
	case "rename":
		if !base.Exists {
			return StageResult{}, fmt.Errorf("%w: rename source missing", ErrConflict)
		}
		if req.DestinationPath == "" {
			return StageResult{}, errors.New("destinationPath is required")
		}
		dest, destErr := snapshotPath(req.DestinationPath)
		if destErr != nil && !errors.Is(destErr, os.ErrNotExist) {
			return StageResult{}, destErr
		}
		if dest.Exists {
			return StageResult{}, fmt.Errorf("%w: rename destination exists", ErrConflict)
		}
		nextSnapshot = base
		preview = Preview{Kind: "metadata", Summary: "rename path"}
	case "chmod":
		if !base.Exists {
			return StageResult{}, fmt.Errorf("%w: chmod target missing", ErrConflict)
		}
		nextSnapshot = base
		nextSnapshot.Mode = req.Mode
		preview = Preview{Kind: "metadata", Summary: "change mode"}
	case "chown":
		if !base.Exists {
			return StageResult{}, fmt.Errorf("%w: chown target missing", ErrConflict)
		}
		nextSnapshot = base
		nextSnapshot.UID = parseIntOrZero(req.Owner)
		nextSnapshot.GID = parseIntOrZero(req.Group)
		preview = Preview{Kind: "metadata", Summary: "change owner"}
	default:
		return StageResult{}, ErrUnsupported
	}
	if err != nil {
		return StageResult{}, err
	}
	if req.Privilege.Status == "" {
		req.Privilege.Status = "unknown"
	}
	op := Operation{
		Version:         "hideout.hostfs-write-operation/v1",
		ID:              opID,
		SessionID:       req.SessionID,
		Profile:         req.Profile,
		Backend:         req.Backend,
		Operation:       req.Operation,
		RequestedPath:   req.Path,
		CanonicalPath:   req.Path,
		DestinationPath: req.DestinationPath,
		GrantID:         req.GrantID,
		GrantSource:     req.GrantSource,
		BaseSnapshot:    base,
		NewSnapshot:     nextSnapshot,
		ContentObject:   contentObject,
		Preview:         preview,
		RequestedMode:   req.Mode,
		RequestedOwner:  req.Owner,
		RequestedGroup:  req.Group,
		Privilege:       req.Privilege,
		DecisionID:      decID,
		Status:          StateStaged,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	decision := Decision{
		Version:         PlanVersion,
		DecisionID:      decID,
		OperationID:     opID,
		State:           StatePending,
		Operation:       req.Operation,
		Path:            req.Path,
		DestinationPath: req.DestinationPath,
		Preview:         preview,
		Policy:          PolicyRef{GrantID: req.GrantID, Source: req.GrantSource},
		Privilege:       req.Privilege,
		TimeoutAt:       now.Add(5 * time.Minute),
		UpdatedAt:       now,
	}
	if err := s.writeJSONAtomic(s.operationPath(op.ID), op); err != nil {
		return StageResult{}, err
	}
	if err := s.writeJSONAtomic(s.decisionPath(decision.DecisionID), decision); err != nil {
		return StageResult{}, err
	}
	return StageResult{Operation: op, Decision: decision}, nil
}

func (s *Store) Apply(decisionID string) (Result, Operation, Decision, error) {
	if s == nil {
		return Result{}, Operation{}, Decision{}, ErrUnsupported
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensure(); err != nil {
		return Result{}, Operation{}, Decision{}, err
	}
	decision, err := s.Decision(decisionID)
	if err != nil {
		return Result{}, Operation{}, Decision{}, err
	}
	op, err := s.Operation(decision.OperationID)
	if err != nil {
		return Result{}, Operation{}, Decision{}, err
	}
	result := Result{
		Version:                  ResultVersion,
		DecisionID:               decision.DecisionID,
		OperationID:              op.ID,
		Decision:                 DecisionAllow,
		Status:                   StateApplied,
		ChangedPaths:             changedPaths(op),
		PartialMutationPrevented: true,
		Privilege:                decision.Privilege,
		AuditRef:                 "audit:hostfs-overlay:apply",
	}
	if decision.State != StateClaimed {
		return result, op, decision, fmt.Errorf("HostFS write decision %s is %s", decision.DecisionID, decision.State)
	}
	if reason, err := s.applyConflictReason(op); err != nil {
		return result, op, decision, err
	} else if reason != "" {
		decision.State = StateConflict
		decision.Claim = nil
		op.Status = StateConflict
		result.Decision = DecisionDeny
		result.Status = StateConflict
		result.ChangedPaths = nil
		result.ConflictReason = reason
		if saveErr := s.saveApplyState(op, decision); saveErr != nil {
			return result, op, decision, saveErr
		}
		_ = s.CleanupArtifacts(op)
		return result, op, decision, ErrConflict
	}
	if err := s.applyOperation(op); err != nil {
		decision.State = StateFailed
		decision.Claim = nil
		op.Status = StateFailed
		result.Status = StateFailed
		result.ChangedPaths = nil
		result.ConflictReason = err.Error()
		if saveErr := s.saveApplyState(op, decision); saveErr != nil {
			return result, op, decision, saveErr
		}
		_ = s.CleanupArtifacts(op)
		return result, op, decision, err
	}
	decision.State = StateApplied
	decision.Claim = nil
	op.Status = StateApplied
	if err := s.saveApplyState(op, decision); err != nil {
		return result, op, decision, err
	}
	_ = s.CleanupArtifacts(op)
	return result, op, decision, nil
}

func (s *Store) View(path string) (View, bool, error) {
	op, ok, err := s.latestOperation(path)
	if err != nil || !ok {
		return View{}, ok, err
	}
	return s.viewFromOperation(op, path)
}

func (s *Store) Entries(dir string) ([]ViewEntry, error) {
	ops, err := s.operations()
	if err != nil {
		return nil, err
	}
	dir = filepath.Clean(dir)
	seen := map[string]ViewEntry{}
	for _, op := range ops {
		if !operationLive(op.Status) {
			continue
		}
		if filepath.Dir(op.RequestedPath) == dir {
			name := filepath.Base(op.RequestedPath)
			view, _, err := s.viewFromOperation(op, op.RequestedPath)
			if err != nil {
				return nil, err
			}
			if view.Deleted {
				seen[name] = ViewEntry{Name: name, Deleted: true}
				continue
			}
			seen[name] = ViewEntry{Name: name, Kind: view.Kind, Size: view.Size, Mode: view.Mode}
		}
		if op.Operation == "rename" && op.DestinationPath != "" {
			if filepath.Dir(op.DestinationPath) == dir {
				name := filepath.Base(op.DestinationPath)
				seen[name] = ViewEntry{Name: name, Kind: op.NewSnapshot.Kind, Size: op.NewSnapshot.Size, Mode: op.NewSnapshot.Mode}
			}
			if filepath.Dir(op.RequestedPath) == dir {
				seen[filepath.Base(op.RequestedPath)] = ViewEntry{Name: filepath.Base(op.RequestedPath), Deleted: true}
			}
		}
	}
	out := make([]ViewEntry, 0, len(seen))
	for _, entry := range seen {
		out = append(out, entry)
	}
	slices.SortFunc(out, func(a, b ViewEntry) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

type ViewEntry struct {
	Name    string
	Kind    string
	Size    int64
	Mode    string
	Deleted bool
}

func (s *Store) Operations() ([]Operation, error) {
	return s.operations()
}

func (s *Store) Operation(id string) (Operation, error) {
	if strings.TrimSpace(id) == "" {
		return Operation{}, errors.New("operation id is required")
	}
	var op Operation
	if err := readJSON(s.operationPath(id), &op); err != nil {
		return Operation{}, err
	}
	return op, nil
}

func (s *Store) Decision(id string) (Decision, error) {
	if strings.TrimSpace(id) == "" {
		return Decision{}, errors.New("decision id is required")
	}
	var decision Decision
	if err := readJSON(s.decisionPath(id), &decision); err != nil {
		return Decision{}, err
	}
	return decision, nil
}

func (s *Store) DecisionForOperation(operationID string) (Decision, error) {
	decisions, err := s.Decisions()
	if err != nil {
		return Decision{}, err
	}
	for _, decision := range decisions {
		if decision.OperationID == operationID {
			return decision, nil
		}
	}
	return Decision{}, os.ErrNotExist
}

func (s *Store) SaveDecision(decision Decision) error {
	if strings.TrimSpace(decision.DecisionID) == "" {
		return errors.New("decision id is required")
	}
	decision.UpdatedAt = s.now()
	return s.writeJSONAtomic(s.decisionPath(decision.DecisionID), decision)
}

func (s *Store) SaveOperation(op Operation) error {
	if strings.TrimSpace(op.ID) == "" {
		return errors.New("operation id is required")
	}
	op.UpdatedAt = s.now()
	return s.writeJSONAtomic(s.operationPath(op.ID), op)
}

func (s *Store) Decisions() ([]Decision, error) {
	entries, err := os.ReadDir(s.decisionsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Decision, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var decision Decision
		if err := readJSON(filepath.Join(s.decisionsDir(), entry.Name()), &decision); err != nil {
			return nil, err
		}
		out = append(out, decision)
	}
	slices.SortFunc(out, func(a, b Decision) int { return a.UpdatedAt.Compare(b.UpdatedAt) })
	return out, nil
}

func (s *Store) latestOperation(path string) (Operation, bool, error) {
	ops, err := s.operations()
	if err != nil {
		return Operation{}, false, err
	}
	path = filepath.Clean(path)
	for i := len(ops) - 1; i >= 0; i-- {
		op := ops[i]
		if !operationLive(op.Status) {
			continue
		}
		if op.RequestedPath == path || op.DestinationPath == path {
			return op, true, nil
		}
	}
	return Operation{}, false, nil
}

func (s *Store) viewFromOperation(op Operation, requested string) (View, bool, error) {
	switch op.Operation {
	case "create", "replace", "append", "truncate":
		data, err := os.ReadFile(s.objectPath(op.ContentObject))
		if err != nil {
			return View{}, true, err
		}
		return View{Exists: true, Kind: "file", Size: int64(len(data)), Mode: modeOrDefault(op.NewSnapshot.Mode, "-rw-r--r--"), Data: data}, true, nil
	case "mkdir":
		return View{Exists: true, Kind: "dir", Mode: modeOrDefault(op.NewSnapshot.Mode, "drwxr-xr-x")}, true, nil
	case "delete":
		return View{Deleted: true, Kind: "missing"}, true, nil
	case "rename":
		if requested == op.RequestedPath {
			return View{Deleted: true, Kind: "missing"}, true, nil
		}
		return View{Exists: true, Kind: op.NewSnapshot.Kind, Size: op.NewSnapshot.Size, Mode: op.NewSnapshot.Mode}, true, nil
	case "chmod", "chown":
		return View{Exists: true, Kind: op.NewSnapshot.Kind, Size: op.NewSnapshot.Size, Mode: op.NewSnapshot.Mode}, true, nil
	default:
		return View{}, false, ErrUnsupported
	}
}

func (s *Store) readCurrentContent(path string) ([]byte, error) {
	if view, ok, err := s.View(path); err != nil || ok {
		if err != nil {
			return nil, err
		}
		if view.Deleted {
			return nil, os.ErrNotExist
		}
		return append([]byte(nil), view.Data...), nil
	}
	return os.ReadFile(path)
}

func (s *Store) stageContent(req StageRequest, prior []byte) (string, Snapshot, Preview, error) {
	content := append([]byte(nil), prior...)
	content = append(content, req.Data...)
	return s.writeContentObject(content, req.Operation)
}

func (s *Store) writeContentObject(content []byte, operation string) (string, Snapshot, Preview, error) {
	id, err := newID("hfwobj")
	if err != nil {
		return "", Snapshot{}, Preview{}, err
	}
	if err := writeFileAtomic(s.objectPath(id), content, 0o600); err != nil {
		return "", Snapshot{}, Preview{}, err
	}
	sum := sha256.Sum256(content)
	snapshot := Snapshot{
		Exists:      true,
		Kind:        "file",
		Size:        int64(len(content)),
		Mode:        "-rw-r--r--",
		MTime:       s.now(),
		ContentHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
	preview := Preview{Kind: "content", Summary: fmt.Sprintf("%s %d bytes", operation, len(content)), Bytes: int64(len(content))}
	return id, snapshot, preview, nil
}

func (s *Store) operations() ([]Operation, error) {
	entries, err := os.ReadDir(s.operationsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Operation, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var op Operation
		if err := readJSON(filepath.Join(s.operationsDir(), entry.Name()), &op); err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	slices.SortFunc(out, func(a, b Operation) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

func (s *Store) saveApplyState(op Operation, decision Decision) error {
	if err := s.writeJSONAtomic(s.operationPath(op.ID), op); err != nil {
		return err
	}
	return s.writeJSONAtomic(s.decisionPath(decision.DecisionID), decision)
}

func (s *Store) applyConflictReason(op Operation) (string, error) {
	current, err := snapshotPath(op.RequestedPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if !sameBaseSnapshot(op.BaseSnapshot, current) {
		return "base path changed since staging", nil
	}
	if op.Operation == "rename" && op.DestinationPath != "" {
		dest, destErr := snapshotPath(op.DestinationPath)
		if destErr != nil && !errors.Is(destErr, os.ErrNotExist) {
			return "", destErr
		}
		if dest.Exists {
			return "rename destination exists", nil
		}
	}
	return "", nil
}

func (s *Store) applyOperation(op Operation) error {
	switch op.Operation {
	case "create":
		return s.applyFileObject(op.RequestedPath, op.ContentObject, op.RequestedMode, true)
	case "replace", "append", "truncate":
		return s.applyFileObject(op.RequestedPath, op.ContentObject, op.RequestedMode, false)
	case "mkdir":
		mode, err := parseFileModeOrDefault(op.RequestedMode, 0o755)
		if err != nil {
			return err
		}
		return os.Mkdir(op.RequestedPath, mode)
	case "delete":
		return os.Remove(op.RequestedPath)
	case "rename":
		return os.Rename(op.RequestedPath, op.DestinationPath)
	case "chmod":
		mode, err := parseFileModeOrDefault(op.RequestedMode, 0)
		if err != nil {
			return err
		}
		return os.Chmod(op.RequestedPath, mode)
	case "chown":
		return applyConstrainedChown(op)
	default:
		return ErrUnsupported
	}
}

func (s *Store) applyFileObject(path, objectID, requestedMode string, createOnly bool) error {
	data, err := os.ReadFile(s.objectPath(objectID))
	if err != nil {
		return err
	}
	mode, err := parseFileModeOrDefault(requestedMode, 0o644)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, ".hideout-"+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := tmpFile.Chmod(mode); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if createOnly {
		err = os.Link(tmp, path)
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Store) CleanupArtifacts(op Operation) error {
	if strings.TrimSpace(op.ContentObject) == "" {
		return nil
	}
	err := os.Remove(s.objectPath(op.ContentObject))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func applyConstrainedChown(op Operation) error {
	current, err := snapshotPath(op.RequestedPath)
	if err != nil {
		return err
	}
	uid := current.UID
	gid := current.GID
	if strings.TrimSpace(op.RequestedOwner) != "" {
		uid, err = strconv.Atoi(strings.TrimSpace(op.RequestedOwner))
		if err != nil {
			return err
		}
	}
	if strings.TrimSpace(op.RequestedGroup) != "" {
		gid, err = strconv.Atoi(strings.TrimSpace(op.RequestedGroup))
		if err != nil {
			return err
		}
	}
	if uid != current.UID || gid != current.GID {
		return fmt.Errorf("%w: chown requires host privilege not held by HostFS overlay apply", ErrUnsupported)
	}
	return nil
}

func changedPaths(op Operation) []string {
	if op.Operation == "rename" && op.DestinationPath != "" {
		return []string{op.RequestedPath, op.DestinationPath}
	}
	return []string{op.RequestedPath}
}

func sameBaseSnapshot(a, b Snapshot) bool {
	if a.Exists != b.Exists || a.Kind != b.Kind {
		return false
	}
	if !a.Exists {
		return true
	}
	if a.Kind == "file" {
		return a.Size == b.Size && a.ContentHash == b.ContentHash
	}
	if a.Kind == "symlink" {
		return a.LinkTarget == b.LinkTarget
	}
	return true
}

func operationLive(status string) bool {
	switch status {
	case StateStaged, StatePending, StateClaimed, "":
		return true
	default:
		return false
	}
}

func snapshotPath(path string) (Snapshot, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{Exists: false, Kind: "missing"}, os.ErrNotExist
		}
		return Snapshot{}, err
	}
	snapshot := Snapshot{
		Exists: true,
		Kind:   fileKind(info),
		Size:   info.Size(),
		Mode:   info.Mode().String(),
		MTime:  info.ModTime().UTC(),
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		snapshot.UID = int(stat.Uid)
		snapshot.GID = int(stat.Gid)
	}
	if info.Mode().IsRegular() {
		if hash, err := hashFile(path); err == nil {
			snapshot.ContentHash = hash
		}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if target, err := os.Readlink(path); err == nil {
			snapshot.LinkTarget = target
		}
	}
	return snapshot, nil
}

func fileKind(info os.FileInfo) string {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return "symlink"
	case info.IsDir():
		return "dir"
	case info.Mode().IsRegular():
		return "file"
	default:
		return "unsupported"
	}
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func patchBytes(prior []byte, offset int64, data []byte) []byte {
	if offset < 0 {
		offset = 0
	}
	if int64(len(prior)) < offset {
		pad := bytes.Repeat([]byte{0}, int(offset)-len(prior))
		prior = append(append([]byte(nil), prior...), pad...)
	} else {
		prior = append([]byte(nil), prior...)
	}
	end := int(offset) + len(data)
	if len(prior) < end {
		next := make([]byte, end)
		copy(next, prior)
		prior = next
	}
	copy(prior[int(offset):], data)
	return prior
}

func resizeBytes(prior []byte, size int64) []byte {
	if size < 0 {
		size = 0
	}
	out := make([]byte, size)
	copy(out, prior)
	return out
}

func parseIntOrZero(value string) int {
	var out int
	_, _ = fmt.Sscanf(value, "%d", &out)
	return out
}

func modeOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func parseFileModeOrDefault(value string, fallback os.FileMode) (os.FileMode, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if fallback == 0 {
			return 0, errors.New("file mode is required")
		}
		return fallback, nil
	}
	parsed, err := strconv.ParseUint(value, 8, 32)
	if err != nil {
		return 0, err
	}
	return os.FileMode(parsed), nil
}

func (s *Store) ensure() error {
	for _, dir := range []string{s.objectsDir(), s.operationsDir(), s.decisionsDir(), filepath.Join(s.Root, "locks")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Store) objectsDir() string    { return filepath.Join(s.Root, "objects") }
func (s *Store) operationsDir() string { return filepath.Join(s.Root, "operations") }
func (s *Store) decisionsDir() string  { return filepath.Join(s.Root, "decisions") }
func (s *Store) objectPath(id string) string {
	return filepath.Join(s.objectsDir(), filepath.Base(id))
}
func (s *Store) operationPath(id string) string {
	return filepath.Join(s.operationsDir(), filepath.Base(id)+".json")
}
func (s *Store) decisionPath(id string) string {
	return filepath.Join(s.decisionsDir(), filepath.Base(id)+".json")
}

func (s *Store) writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, 0o600)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if f, err := os.Open(tmp); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func newID(prefix string) (string, error) {
	var raw [10]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(raw[:]), nil
}
