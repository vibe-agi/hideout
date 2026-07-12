package hostpathrisk

import (
	"path/filepath"
	"testing"
)

func TestCatalogSeparatesHomeBoundaryFromBroadDiscoveryHiddenRoots(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	store := filepath.Join(home, ".hideout")
	roots := catalogFor(home, store)

	byPath := map[string]Root{}
	for _, root := range roots {
		if _, exists := byPath[root.Path]; exists {
			t.Fatalf("duplicate root %q", root.Path)
		}
		byPath[root.Path] = root
	}

	homeRoot := byPath[home]
	if homeRoot.Category != CategoryHomeBoundary || !homeRoot.WorkspaceRestricted || homeRoot.BroadDiscoveryHidden {
		t.Fatalf("home root category is wrong: %+v", homeRoot)
	}
	storeRoot := byPath[store]
	if storeRoot.Category != CategoryControlPlane || !storeRoot.WorkspaceRestricted || !storeRoot.BroadDiscoveryHidden {
		t.Fatalf("store root category is wrong: %+v", storeRoot)
	}
	sshRoot := byPath[filepath.Join(home, ".ssh")]
	if sshRoot.Category != CategoryCredential || !sshRoot.WorkspaceRestricted || !sshRoot.BroadDiscoveryHidden {
		t.Fatalf("credential root category is wrong: %+v", sshRoot)
	}
	browserRoot := byPath[filepath.Join(home, ".mozilla")]
	if browserRoot.Category != CategoryBrowser || !browserRoot.WorkspaceRestricted || !browserRoot.BroadDiscoveryHidden {
		t.Fatalf("browser root category is wrong: %+v", browserRoot)
	}
}

func TestCatalogWithoutHomeStillContainsStoreAndSystemKeyRoots(t *testing.T) {
	store := filepath.Join(t.TempDir(), "store")
	roots := catalogFor("", store)
	if len(roots) != 2 {
		t.Fatalf("expected store and system key roots, got %+v", roots)
	}
	if roots[0].Path != store || roots[0].Category != CategoryControlPlane {
		t.Fatalf("unexpected store root: %+v", roots[0])
	}
	if roots[1].Path != "/Library/Keychains" || roots[1].Category != CategorySystemKey {
		t.Fatalf("unexpected system key root: %+v", roots[1])
	}
}
