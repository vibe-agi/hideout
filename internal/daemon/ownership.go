package daemon

import "sync"

// LiveResource is a resource that can survive a daemon restart (for example a
// running environment). The daemon reports any it cannot prove it owns.
type LiveResource struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

// ownership tracks, in memory only, the resources the current daemon instance
// owns since its start. It is deliberately not persisted: after a restart it is
// empty, so any pre-existing live resource is unprovable and fails closed.
type ownership struct {
	mu    sync.Mutex
	owned map[string]string // resourceID -> sessionID
}

func newOwnership() *ownership {
	return &ownership{owned: map[string]string{}}
}

func (o *ownership) record(resourceID, sessionID string) {
	o.mu.Lock()
	o.owned[resourceID] = sessionID
	o.mu.Unlock()
}

func (o *ownership) owns(resourceID string) bool {
	o.mu.Lock()
	_, ok := o.owned[resourceID]
	o.mu.Unlock()
	return ok
}

// detectOrphans reports every live resource the current instance cannot prove it
// owns. Callers fail these closed: report and audit, never re-adopt or destroy.
func (o *ownership) detectOrphans(resources []LiveResource) []LiveResource {
	var orphans []LiveResource
	for _, res := range resources {
		if !o.owns(res.ID) {
			orphans = append(orphans, res)
		}
	}
	return orphans
}
