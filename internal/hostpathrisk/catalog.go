// Package hostpathrisk owns the categorized host roots shared by workspace
// safety and broad HostFS visibility policy.
package hostpathrisk

import (
	"os"
	"path/filepath"
	"strings"
)

type Category string

const (
	CategoryHomeBoundary Category = "home-boundary"
	CategoryControlPlane Category = "control-plane"
	CategoryCredential   Category = "credential"
	CategoryBrowser      Category = "browser"
	CategorySystemKey    Category = "system-key"
)

type Root struct {
	Path                 string
	Reason               string
	Category             Category
	WorkspaceRestricted  bool
	BroadDiscoveryHidden bool
}

func Catalog(storeRoot string) []Root {
	home, _ := os.UserHomeDir()
	return catalogFor(home, storeRoot)
}

func BroadDiscoveryHiddenRoots(storeRoot string) []Root {
	home, _ := os.UserHomeDir()
	return BroadDiscoveryHiddenRootsFor(home, storeRoot)
}

func BroadDiscoveryHiddenRootsFor(home, storeRoot string) []Root {
	roots := catalogFor(home, storeRoot)
	out := make([]Root, 0, len(roots))
	for _, root := range roots {
		if root.BroadDiscoveryHidden {
			out = append(out, root)
		}
	}
	return out
}

func catalogFor(home, storeRoot string) []Root {
	var roots []Root
	if strings.TrimSpace(storeRoot) != "" {
		roots = append(roots, Root{
			Path:                 filepath.Clean(storeRoot),
			Reason:               "Hideout control-plane store",
			Category:             CategoryControlPlane,
			WorkspaceRestricted:  true,
			BroadDiscoveryHidden: true,
		})
	}
	home = strings.TrimSpace(home)
	if home != "" {
		home = filepath.Clean(home)
		roots = append(roots, Root{
			Path:                home,
			Reason:              "host home contains credentials and Hideout control-plane state",
			Category:            CategoryHomeBoundary,
			WorkspaceRestricted: true,
		})
		for _, rel := range []string{
			".ssh",
			".gnupg",
			".aws",
			".azure",
			".kube",
			".docker",
			".android",
			".config/gcloud",
			".config/gh",
		} {
			roots = append(roots, hiddenRoot(home, rel, CategoryCredential, "credential root"))
		}
		for _, rel := range []string{
			".config/google-chrome",
			".config/BraveSoftware",
			".config/microsoft-edge",
			".mozilla",
			"Library/Application Support/Google/Chrome",
			"Library/Application Support/BraveSoftware",
			"Library/Application Support/Firefox",
			"Library/Application Support/Microsoft Edge",
		} {
			roots = append(roots, hiddenRoot(home, rel, CategoryBrowser, "browser profile root"))
		}
		roots = append(roots, hiddenRoot(home, "Library/Keychains", CategorySystemKey, "host keychain root"))
	}
	roots = append(roots, Root{
		Path:                 "/Library/Keychains",
		Reason:               "system credential root",
		Category:             CategorySystemKey,
		WorkspaceRestricted:  true,
		BroadDiscoveryHidden: true,
	})
	return deduplicate(roots)
}

func hiddenRoot(home, relative string, category Category, reason string) Root {
	return Root{
		Path:                 filepath.Join(home, filepath.FromSlash(relative)),
		Reason:               reason,
		Category:             category,
		WorkspaceRestricted:  true,
		BroadDiscoveryHidden: true,
	}
}

func deduplicate(roots []Root) []Root {
	seen := make(map[string]struct{}, len(roots))
	out := make([]Root, 0, len(roots))
	for _, root := range roots {
		root.Path = filepath.Clean(root.Path)
		if _, exists := seen[root.Path]; exists {
			continue
		}
		seen[root.Path] = struct{}{}
		out = append(out, root)
	}
	return out
}
