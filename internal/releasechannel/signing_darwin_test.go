package releasechannel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestObserveDeclaredDarwinEntitlements(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "hideout-migration-vz-adopt-darwin-arm64")
	contents, err := os.ReadFile("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	entitlements := filepath.Join(
		"..",
		"..",
		"packaging",
		"macos",
		"hideout-migration-vz-adopt.entitlements.plist",
	)
	command := exec.Command(
		"/usr/bin/codesign",
		"--force",
		"--timestamp=none",
		"--options",
		"runtime",
		"--entitlements",
		entitlements,
		"--sign",
		"-",
		binary,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("sign entitlement fixture: %v: %s", err, output)
	}
	verified, err := observeDeclaredDarwinEntitlements(
		context.Background(),
		darwinVirtualizationHelperPath,
		binary,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !verified {
		t.Fatal("required entitlement was not reported as verified")
	}
	if _, err := observeDeclaredDarwinEntitlements(
		context.Background(),
		"bin/hideout",
		binary,
	); err == nil {
		t.Fatal("undeclared entitlement on another binary was accepted")
	}
}
