package daemon

import (
	"context"
	"errors"
	"sort"

	"github.com/vibe-agi/hideout/internal/manager"
	workloadquery "github.com/vibe-agi/hideout/internal/workloadobs/query"
	workloadstore "github.com/vibe-agi/hideout/internal/workloadobs/store"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

var errActivityOwnerLive = errors.New(
	"activity owner still has a live observation session",
)

// daemonActivityCleanupStore makes persistent and live daemon activity state
// one cleanup scope. Deleting only the durable owner would leave a closed
// session queryable from the daemon's in-memory coverage snapshots.
type daemonActivityCleanupStore struct {
	persistent manager.ActivityCleanupStore
	activity   *activityService
}

var _ manager.ActivityCleanupStore = (*daemonActivityCleanupStore)(nil)

func newDaemonActivityCleanupStore(
	persistent manager.ActivityCleanupStore,
	activity *activityService,
) *daemonActivityCleanupStore {
	return &daemonActivityCleanupStore{
		persistent: persistent,
		activity:   activity,
	}
}

func (store *daemonActivityCleanupStore) Owners(
	ctx context.Context,
) ([]workloadtypes.ActivityOwner, error) {
	if store == nil || store.persistent == nil || store.activity == nil ||
		ctx == nil {
		return nil, workloadquery.ErrInvalidQuery
	}
	persisted, err := store.persistent.Owners(ctx)
	if err != nil {
		return nil, err
	}
	live, err := store.activity.owners(ctx)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]workloadtypes.ActivityOwner, len(persisted)+len(live))
	for _, owner := range append(persisted, live...) {
		if err := owner.Validate(); err != nil {
			return nil, err
		}
		key := owner.Key()
		if existing, ok := byKey[key]; ok && !existing.Equal(owner) {
			return nil, errors.New("activity cleanup owner identity collision")
		}
		byKey[key] = owner
	}
	result := make([]workloadtypes.ActivityOwner, 0, len(byKey))
	for _, owner := range byKey {
		result = append(result, owner)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Key() < result[right].Key()
	})
	return result, nil
}

func (store *daemonActivityCleanupStore) DeleteOwner(
	ctx context.Context,
	owner workloadtypes.ActivityOwner,
) (workloadstore.DeletionProof, error) {
	if store == nil || store.persistent == nil || store.activity == nil ||
		ctx == nil {
		return workloadstore.DeletionProof{}, workloadquery.ErrInvalidQuery
	}
	if err := ctx.Err(); err != nil {
		return workloadstore.DeletionProof{}, err
	}
	if err := owner.Validate(); err != nil {
		return workloadstore.DeletionProof{}, err
	}
	// Quiesce and forget closed in-memory sessions first. That prevents a
	// previously resolved session from recreating durable records after the
	// store owner has been atomically removed.
	if err := store.activity.forgetOwner(ctx, owner); err != nil {
		return workloadstore.DeletionProof{}, err
	}
	return store.persistent.DeleteOwner(ctx, owner)
}

func (service *activityService) owners(
	ctx context.Context,
) ([]workloadtypes.ActivityOwner, error) {
	if service == nil {
		return nil, workloadquery.ErrOwnerNotFound
	}
	if ctx == nil {
		return nil, workloadquery.ErrInvalidQuery
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	service.mu.RLock()
	sessions := make([]*activitySession, 0, len(service.sessions))
	for _, session := range service.sessions {
		sessions = append(sessions, session)
	}
	service.mu.RUnlock()

	byKey := make(map[string]workloadtypes.ActivityOwner, len(sessions))
	for index, session := range sessions {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		session.mu.Lock()
		owner := session.preparation.Owner
		session.mu.Unlock()
		if err := owner.Validate(); err != nil {
			return nil, err
		}
		byKey[owner.Key()] = owner
	}
	result := make([]workloadtypes.ActivityOwner, 0, len(byKey))
	for _, owner := range byKey {
		result = append(result, owner)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Key() < result[right].Key()
	})
	return result, nil
}

func (service *activityService) forgetOwner(
	ctx context.Context,
	owner workloadtypes.ActivityOwner,
) error {
	if service == nil || ctx == nil || owner.Validate() != nil {
		return workloadquery.ErrInvalidQuery
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	matched := make([]string, 0)
	for sessionID, session := range service.sessions {
		session.mu.Lock()
		matches := session.preparation.Owner.Equal(owner)
		closed := session.sessionClosed
		session.mu.Unlock()
		if !matches {
			continue
		}
		if !closed {
			return errActivityOwnerLive
		}
		matched = append(matched, sessionID)
	}
	sort.Strings(matched)
	for _, sessionID := range matched {
		if err := ctx.Err(); err != nil {
			return err
		}
		session := service.sessions[sessionID]
		session.mu.Lock()
		session.clearRedactionSnapshotLocked()
		session.ready = nil
		session.tracker = nil
		session.timelines = nil
		session.heartbeatByCPU = nil
		session.supervisorDropped = nil
		session.accountedDropped = nil
		session.mu.Unlock()
		delete(service.sessions, sessionID)
	}
	return nil
}
