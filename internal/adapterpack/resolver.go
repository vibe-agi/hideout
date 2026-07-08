package adapterpack

import (
	"fmt"

	"github.com/vibe-agi/hideout/internal/cmdadapter"
	"github.com/vibe-agi/hideout/internal/profile"
)

type RuntimeResolver struct {
	Store Store
}

func (r RuntimeResolver) ResolveCommandAdapter(profileDir, id string, adapter profile.CommandAdapter) (cmdadapter.ResolvedSource, error) {
	_ = profileDir
	if adapter.PackID == "" || adapter.PackRevisionID == "" || adapter.PackAdapterID == "" {
		return cmdadapter.ResolvedSource{}, fmt.Errorf("command adapter %s pack id, revision id, and adapter id are required", id)
	}
	resolved, err := r.Store.ResolveRuntime(adapter.PackID, adapter.PackRevisionID, adapter.PackAdapterID, adapter.PackLockDigest)
	if err != nil {
		return cmdadapter.ResolvedSource{}, err
	}
	return cmdadapter.ResolvedSource{
		Source: resolved.Source,
		Digest: resolved.Digest,
		Path:   resolved.Path,
	}, nil
}
