package manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	defaultMaxOperations = 1024
	maxOperationBytes    = 512 << 10
)

var (
	operationLocks      sync.Map
	operationStoreLocks sync.Map
)

type OperationStore struct {
	Root       string
	Now        func() time.Time
	MaxRecords int
}

func (store OperationStore) OperationPath(id string) string {
	return filepath.Join(store.Root, "operations", id+".json")
}

func (store OperationStore) Reserve(binding OperationBinding, effects []EffectResult) (Operation, bool, error) {
	if err := binding.Validate(); err != nil {
		return Operation{}, false, err
	}
	var operation Operation
	var created bool
	err := store.withStoreLock(func() error {
		if err := store.ensureDirectory(); err != nil {
			return err
		}
		return store.withOperationLock(binding.ID, func() error {
			existing, err := store.loadUnlocked(binding.ID)
			if err == nil {
				if !existing.Matches(binding) {
					return ErrOperationMismatch
				}
				operation = existing
				return nil
			}
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := store.pruneUnlocked(store.limit() - 1); err != nil {
				return err
			}
			now := store.now()
			normalizedEffects := append([]EffectResult(nil), effects...)
			for index := range normalizedEffects {
				if normalizedEffects[index].Provider == "" {
					normalizedEffects[index].Provider = "manager"
				}
				if normalizedEffects[index].Status == "" {
					normalizedEffects[index].Status = EffectPending
				}
			}
			operation = Operation{
				Schema:       OperationSchema,
				ID:           binding.ID,
				Kind:         binding.Kind,
				Owner:        binding.Owner,
				PlanDigest:   binding.PlanDigest,
				BaseRevision: binding.BaseRevision,
				Phase:        OperationPlanned,
				Effects:      normalizedEffects,
				Recovery: Recovery{
					Code:       "retry-operation",
					Summary:    "Retry with the same operation identity to inspect or resume this operation.",
					NextAction: "refresh operation status",
				},
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := store.writeUnlocked(operation); err != nil {
				return err
			}
			created = true
			return nil
		})
	})
	return operation, created, err
}

func (store OperationStore) Load(id string) (Operation, error) {
	if !operationIDPattern.MatchString(id) {
		return Operation{}, ErrInvalidOperation
	}
	var operation Operation
	err := store.withOperationLock(id, func() error {
		var loadErr error
		operation, loadErr = store.loadUnlocked(id)
		return loadErr
	})
	return operation, err
}

// List returns the newest durable operations first. Records are individually
// atomically replaced, so a reader never observes a partially written
// operation even while an effect advances in another process.
func (store OperationStore) List(limit int) ([]Operation, error) {
	if strings.TrimSpace(store.Root) == "" || !filepath.IsAbs(store.Root) {
		return nil, errors.New("operation store root must be absolute")
	}
	if limit < 0 || limit > defaultMaxOperations {
		return nil, errors.New("operation list limit is invalid")
	}
	if limit == 0 {
		return []Operation{}, nil
	}
	entries, err := os.ReadDir(filepath.Join(store.Root, "operations"))
	if errors.Is(err, os.ErrNotExist) {
		return []Operation{}, nil
	}
	if err != nil {
		return nil, err
	}
	operations := make([]Operation, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !operationIDPattern.MatchString(id) {
			return nil, fmt.Errorf("unexpected operation store entry %q", entry.Name())
		}
		operation, err := store.loadUnlocked(id)
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	sort.Slice(operations, func(left, right int) bool {
		if operations[left].UpdatedAt.Equal(operations[right].UpdatedAt) {
			return operations[left].ID < operations[right].ID
		}
		return operations[left].UpdatedAt.After(operations[right].UpdatedAt)
	})
	if len(operations) > limit {
		operations = operations[:limit]
	}
	return operations, nil
}

func (store OperationStore) Write(operation Operation) error {
	if err := operation.Validate(); err != nil {
		return err
	}
	return store.withOperationLock(operation.ID, func() error {
		existing, err := store.loadUnlocked(operation.ID)
		if err == nil && !existing.Matches(operation.Binding()) {
			return ErrOperationMismatch
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && operation.CreatedAt != existing.CreatedAt {
			return errors.New("operation creation time cannot change")
		}
		return store.writeUnlocked(operation)
	})
}

func (store OperationStore) Transition(id, next string, result *OperationResult) (Operation, error) {
	operation, _, err := store.TransitionIfChanged(id, next, result)
	return operation, err
}

func (store OperationStore) TransitionIfChanged(
	id, next string,
	result *OperationResult,
) (Operation, bool, error) {
	var operation Operation
	var changed bool
	err := store.withOperationLock(id, func() error {
		current, err := store.loadUnlocked(id)
		if err != nil {
			return err
		}
		if current.Phase == next {
			operation = current
			return nil
		}
		if !operationTransitionAllowed(current.Phase, next) {
			return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Phase, next)
		}
		current.Phase = next
		current.Result = cloneOperationResult(result)
		current.UpdatedAt = store.nextTime(current.UpdatedAt)
		if current.Terminal() {
			if current.Result == nil {
				return errors.New("terminal operation requires a result")
			}
			current.Recovery = Recovery{
				Code:    "operation-terminal",
				Summary: "This operation is terminal; inspect its result and evidence.",
			}
		} else if current.Result != nil {
			return errors.New("nonterminal operation cannot have a result")
		}
		if err := store.writeUnlocked(current); err != nil {
			return err
		}
		operation = current
		changed = true
		return nil
	})
	return operation, changed, err
}

func (store OperationStore) SetRecovery(
	id string,
	recovery Recovery,
) (Operation, bool, error) {
	return store.updateRecovery(id, recovery, false)
}

func (store OperationStore) RequireRecovery(
	id string,
	recovery Recovery,
) (Operation, bool, error) {
	return store.updateRecovery(id, recovery, true)
}

func (store OperationStore) updateRecovery(
	id string,
	recovery Recovery,
	require bool,
) (Operation, bool, error) {
	if err := recovery.Validate(); err != nil {
		return Operation{}, false, err
	}
	var operation Operation
	var changed bool
	err := store.withOperationLock(id, func() error {
		current, err := store.loadUnlocked(id)
		if err != nil {
			return err
		}
		if current.Terminal() {
			operation = current
			return nil
		}
		nextPhase := current.Phase
		if require {
			nextPhase = OperationRecoveryRequired
			if !operationTransitionAllowed(current.Phase, nextPhase) {
				return fmt.Errorf(
					"%w: %s -> %s",
					ErrInvalidTransition,
					current.Phase,
					nextPhase,
				)
			}
		}
		if current.Phase == nextPhase && current.Recovery == recovery {
			operation = current
			return nil
		}
		current.Phase = nextPhase
		current.Recovery = recovery
		current.UpdatedAt = store.nextTime(current.UpdatedAt)
		if err := store.writeUnlocked(current); err != nil {
			return err
		}
		operation = current
		changed = true
		return nil
	})
	return operation, changed, err
}

func (store OperationStore) BeginEffect(
	id, effectID, provider string,
) (Operation, bool, error) {
	var operation Operation
	var execute bool
	err := store.withOperationLock(id, func() error {
		current, err := store.loadUnlocked(id)
		if err != nil {
			return err
		}
		index := effectIndex(current.Effects, effectID)
		if index < 0 {
			return fmt.Errorf("operation effect %q not found", effectID)
		}
		if current.Effects[index].Provider != provider {
			return fmt.Errorf(
				"%w: effect=%s expected=%s actual=%s",
				ErrOperationProviderMismatch,
				effectID,
				current.Effects[index].Provider,
				provider,
			)
		}
		if current.Terminal() {
			operation = current
			return nil
		}
		switch current.Effects[index].Status {
		case EffectPending:
			current.Effects[index].Status = EffectRunning
			current.UpdatedAt = store.nextTime(current.UpdatedAt)
			if err := store.writeUnlocked(current); err != nil {
				return err
			}
			execute = true
		case EffectRunning, EffectSucceeded, EffectFailed, EffectRolledBack, EffectUnproved:
			// A running effect after response loss or restart must be reconciled.
			// It is never blindly executed a second time.
		default:
			return ErrInvalidOperation
		}
		operation = current
		return nil
	})
	return operation, execute, err
}

func (store OperationStore) FinishEffect(
	id, effectID, provider, status string,
	evidence []EvidenceRef,
) (Operation, error) {
	var operation Operation
	err := store.withOperationLock(id, func() error {
		current, err := store.loadUnlocked(id)
		if err != nil {
			return err
		}
		index := effectIndex(current.Effects, effectID)
		if index < 0 {
			return fmt.Errorf("operation effect %q not found", effectID)
		}
		if current.Effects[index].Provider != provider {
			return fmt.Errorf(
				"%w: effect=%s expected=%s actual=%s",
				ErrOperationProviderMismatch,
				effectID,
				current.Effects[index].Provider,
				provider,
			)
		}
		switch status {
		case EffectSucceeded, EffectFailed, EffectRolledBack, EffectUnproved:
		default:
			return ErrInvalidOperation
		}
		if current.Effects[index].Status == status {
			switch {
			case slices.Equal(
				current.Effects[index].Evidence,
				evidence,
			):
				operation = current
				return nil
			case len(current.Effects[index].Evidence) == 0 &&
				len(evidence) != 0:
				current.Effects[index].Evidence = append(
					[]EvidenceRef(nil),
					evidence...,
				)
				current.UpdatedAt = store.nextTime(current.UpdatedAt)
				if err := store.writeUnlocked(current); err != nil {
					return err
				}
				operation = current
				return nil
			default:
				return fmt.Errorf(
					"%w: effect %s completion evidence differs",
					ErrOperationMismatch,
					effectID,
				)
			}
		}
		if !operationEffectTransitionAllowed(
			current.Effects[index].Status,
			status,
		) {
			return fmt.Errorf("%w: effect %s is %s", ErrInvalidTransition, effectID, current.Effects[index].Status)
		}
		current.Effects[index].Status = status
		current.Effects[index].Evidence = append([]EvidenceRef(nil), evidence...)
		current.UpdatedAt = store.nextTime(current.UpdatedAt)
		if err := store.writeUnlocked(current); err != nil {
			return err
		}
		operation = current
		return nil
	})
	return operation, err
}

// AppendEffectEvidence atomically enriches a completed effect with a new,
// independently observed proof. It never changes the effect outcome and is
// idempotent for an identical evidence reference.
func (store OperationStore) AppendEffectEvidence(
	id, effectID, provider string,
	evidence EvidenceRef,
) (Operation, error) {
	if err := evidence.Validate(); err != nil {
		return Operation{}, err
	}
	var operation Operation
	err := store.withOperationLock(id, func() error {
		current, err := store.loadUnlocked(id)
		if err != nil {
			return err
		}
		index := effectIndex(current.Effects, effectID)
		if index < 0 {
			return fmt.Errorf("operation effect %q not found", effectID)
		}
		effect := &current.Effects[index]
		if effect.Provider != provider {
			return fmt.Errorf(
				"%w: effect=%s expected=%s actual=%s",
				ErrOperationProviderMismatch,
				effectID,
				effect.Provider,
				provider,
			)
		}
		if current.Terminal() {
			return fmt.Errorf(
				"%w: operation %s is terminal",
				ErrInvalidTransition,
				id,
			)
		}
		switch effect.Status {
		case EffectSucceeded, EffectFailed, EffectRolledBack,
			EffectUnproved:
		default:
			return fmt.Errorf(
				"%w: effect %s is %s",
				ErrInvalidTransition,
				effectID,
				effect.Status,
			)
		}
		if slices.Contains(effect.Evidence, evidence) {
			operation = current
			return nil
		}
		if len(effect.Evidence) >= maxOperationEvidence {
			return ErrInvalidOperation
		}
		effect.Evidence = append(effect.Evidence, evidence)
		current.UpdatedAt = store.nextTime(current.UpdatedAt)
		if err := store.writeUnlocked(current); err != nil {
			return err
		}
		operation = current
		return nil
	})
	return operation, err
}

func operationEffectTransitionAllowed(from, to string) bool {
	if from == EffectRunning {
		return true
	}
	return from == EffectSucceeded &&
		(to == EffectRolledBack || to == EffectUnproved)
}

func (store OperationStore) Prune() error {
	return store.withStoreLock(func() error {
		return store.pruneUnlocked(store.limit())
	})
}

func (store OperationStore) pruneUnlocked(keep int) error {
	if keep < 0 {
		return errors.New("operation retention bound is invalid")
	}
	dir := filepath.Join(store.Root, "operations")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	type candidate struct {
		operation Operation
		path      string
	}
	var operations []candidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !operationIDPattern.MatchString(id) {
			return fmt.Errorf("unexpected operation store entry %q", entry.Name())
		}
		operation, err := store.loadUnlocked(id)
		if err != nil {
			return err
		}
		operations = append(operations, candidate{operation: operation, path: store.OperationPath(id)})
	}
	if len(operations) <= keep {
		return nil
	}
	sort.Slice(operations, func(left, right int) bool {
		if operations[left].operation.UpdatedAt.Equal(operations[right].operation.UpdatedAt) {
			return operations[left].operation.ID < operations[right].operation.ID
		}
		return operations[left].operation.UpdatedAt.Before(operations[right].operation.UpdatedAt)
	})
	removeCount := len(operations) - keep
	for _, candidate := range operations {
		if removeCount == 0 {
			break
		}
		if !candidate.operation.Terminal() {
			continue
		}
		if err := os.Remove(candidate.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		removeCount--
	}
	if removeCount > 0 {
		return errors.New("operation store is full of nonterminal operations")
	}
	return syncOperationDirectory(dir)
}

func (store OperationStore) loadUnlocked(id string) (Operation, error) {
	path := store.OperationPath(id)
	info, err := os.Lstat(path)
	if err != nil {
		return Operation{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() <= 0 || info.Size() > maxOperationBytes {
		return Operation{}, errors.New("operation record must be a bounded private regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Operation{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var operation Operation
	if err := decoder.Decode(&operation); err != nil {
		return Operation{}, fmt.Errorf("decode operation %s: %w", id, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Operation{}, errors.New("operation record contains trailing data")
		}
		return Operation{}, err
	}
	if operation.ID != id {
		return Operation{}, errors.New("operation record identity mismatch")
	}
	if err := operation.Validate(); err != nil {
		return Operation{}, err
	}
	return operation, nil
}

func (store OperationStore) writeUnlocked(operation Operation) error {
	if err := operation.Validate(); err != nil {
		return err
	}
	if err := store.ensureDirectory(); err != nil {
		return err
	}
	data, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxOperationBytes {
		return errors.New("operation record exceeds size bound")
	}
	path := store.OperationPath(operation.ID)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("operation record target must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".operation-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	keepTemp = false
	return syncOperationDirectory(dir)
}

func (store OperationStore) ensureDirectory() error {
	if strings.TrimSpace(store.Root) == "" || !filepath.IsAbs(store.Root) {
		return errors.New("operation store root must be absolute")
	}
	for _, dir := range []string{store.Root, filepath.Join(store.Root, "operations")} {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(dir)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("operation store directory %q must be private", dir)
		}
		if info.Mode().Perm() != 0o700 {
			if err := os.Chmod(dir, 0o700); err != nil {
				return err
			}
		}
	}
	return nil
}

func (store OperationStore) withOperationLock(id string, fn func() error) (err error) {
	if !operationIDPattern.MatchString(id) {
		return ErrInvalidOperation
	}
	key := filepath.Join(store.Root, ".locks", "operations", id+".lock")
	value, _ := operationLocks.LoadOrStore(key, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()

	lock, err := openOperationLock(store.Root, id+".lock")
	if err != nil {
		return err
	}
	fd := int(lock.Fd())
	defer func() {
		err = errors.Join(err, unix.Flock(fd, unix.LOCK_UN), lock.Close())
	}()
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock operation: %w", err)
	}
	return fn()
}

func (store OperationStore) withStoreLock(fn func() error) (err error) {
	key := filepath.Join(store.Root, ".locks", "operations", "store.lock")
	value, _ := operationStoreLocks.LoadOrStore(key, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()

	lock, err := openOperationLock(store.Root, "store.lock")
	if err != nil {
		return err
	}
	fd := int(lock.Fd())
	defer func() {
		err = errors.Join(err, unix.Flock(fd, unix.LOCK_UN), lock.Close())
	}()
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock operation store: %w", err)
	}
	return fn()
}

func openOperationLock(root, name string) (*os.File, error) {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return nil, errors.New("operation store root must be absolute")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open operation store for lock: %w", err)
	}
	defer unix.Close(rootFD)
	locksFD, err := openOrCreateLockDirectory(rootFD, ".locks")
	if err != nil {
		return nil, err
	}
	defer unix.Close(locksFD)
	operationsFD, err := openOrCreateLockDirectory(locksFD, "operations")
	if err != nil {
		return nil, err
	}
	defer unix.Close(operationsFD)
	fd, err := unix.Openat(operationsFD, name, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open operation lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), filepath.Join(root, ".locks", "operations", name))
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open operation lock: invalid file descriptor")
	}
	return file, nil
}

func (store OperationStore) limit() int {
	if store.MaxRecords > 0 {
		return store.MaxRecords
	}
	return defaultMaxOperations
}

func (store OperationStore) now() time.Time {
	if store.Now != nil {
		return store.Now().UTC()
	}
	return time.Now().UTC()
}

func (store OperationStore) nextTime(previous time.Time) time.Time {
	now := store.now()
	if now.Before(previous) {
		return previous
	}
	return now
}

func effectIndex(effects []EffectResult, id string) int {
	for index := range effects {
		if effects[index].ID == id {
			return index
		}
	}
	return -1
}

func cloneOperationResult(result *OperationResult) *OperationResult {
	if result == nil {
		return nil
	}
	cloned := *result
	return &cloned
}

func syncOperationDirectory(dir string) error {
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
