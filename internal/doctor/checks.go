package doctor

import (
	"os"
	"path/filepath"
)

func CheckStoreWritable(storeRoot string, b *Builder) {
	if b == nil {
		return
	}
	if storeRoot == "" {
		b.Add("store", "store", StatusError, "store root is empty")
		return
	}
	if err := os.MkdirAll(storeRoot, 0o700); err != nil {
		b.Add("store", "store", StatusError, err.Error())
		return
	}
	probe := filepath.Join(storeRoot, ".doctor-write-test")
	if err := os.WriteFile(probe, []byte("ok\n"), 0o600); err != nil {
		b.Add("store", "store", StatusError, err.Error())
		return
	}
	_ = os.Remove(probe)
	b.Add("store", "store", StatusPass, "writable")
}

func AddSelectedFeaturePlaceholders(req Request, b *Builder) {
	if b == nil {
		return
	}
	for _, feature := range NormalizeFeatures(req.Features) {
		b.Add("feature-"+feature, feature, StatusSkipped, "feature scope selected; local doctor did not run a real backend or network probe", WithRequired(false))
	}
}
