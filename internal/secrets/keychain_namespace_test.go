package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeychainServiceForStoreRootPreservesDefaultAndIsolatesCustomStores(
	t *testing.T,
) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	defaultService, err := keychainServiceForStoreRoot(
		filepath.Join(home, ".hideout"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if defaultService != KeychainServiceName {
		t.Fatalf("default service=%q", defaultService)
	}

	firstRoot := t.TempDir()
	first, err := keychainServiceForStoreRoot(firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	firstAgain, err := keychainServiceForStoreRoot(
		filepath.Join(firstRoot, "."),
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := keychainServiceForStoreRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if first == KeychainServiceName || first != firstAgain || first == second {
		t.Fatalf(
			"custom services first=%q again=%q second=%q",
			first,
			firstAgain,
			second,
		)
	}
	if !strings.HasPrefix(
		first,
		KeychainServiceName+".store.",
	) || len(first) != len(KeychainServiceName+".store.")+24 {
		t.Fatalf("custom service has unstable shape: %q", first)
	}

	alias := filepath.Join(t.TempDir(), "store-alias")
	if err := os.Symlink(firstRoot, alias); err != nil {
		t.Fatal(err)
	}
	aliased, err := keychainServiceForStoreRoot(alias)
	if err != nil {
		t.Fatal(err)
	}
	if aliased != first {
		t.Fatalf("physical alias service=%q want %q", aliased, first)
	}
	if _, err := keychainServiceForStoreRoot("relative"); err == nil {
		t.Fatal("relative store root was accepted")
	}
}
