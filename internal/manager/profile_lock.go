package manager

import (
	"path/filepath"
	"sync"
)

var profileMutationLocks sync.Map

func (c Core) withProfileMutationLock(profileName string, fn func() error) error {
	profileName, err := normalizeManagerProfileName(profileName)
	if err != nil {
		return err
	}
	key := filepath.Join(c.Store.Root, "profiles", profileName)
	value, _ := profileMutationLocks.LoadOrStore(key, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	return fn()
}
