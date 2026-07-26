package packagekit

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/vibe-agi/hideout/internal/helperbin"
)

const (
	PrerequisiteAvailable      = "available"
	PrerequisiteMissing        = "missing"
	PrerequisiteUndiscoverable = "undiscoverable"
)

func ExternalPrerequisites(packageRoot ...string) []ExternalPrerequisiteStatus {
	status := ExternalPrerequisiteStatus{
		Name:         "tun2socks",
		Status:       PrerequisiteMissing,
		PackageOwned: true,
		Source:       "package-owned",
		Hint:         "verify or reinstall the package-owned tun2socks privacy helper",
	}
	if len(packageRoot) > 0 && packageRoot[0] != "" {
		path := filepath.Join(packageRoot[0], "bin", helperbin.LinuxTun2SocksCommand+"-linux-"+runtime.GOARCH)
		if _, ok := helperbin.Tun2SocksHelperCurrent(path, runtime.GOARCH, true); ok {
			status.Status = PrerequisiteAvailable
		}
		return []ExternalPrerequisiteStatus{status}
	}
	resolution, err := helperbin.ResolveLinuxTun2Socks(helperbin.Tun2SocksResolveOptions{
		GOARCH:   runtime.GOARCH,
		Override: os.Getenv(helperbin.LinuxTun2SocksPathEnvironment),
	})
	if err != nil {
		status.Status = PrerequisiteUndiscoverable
		status.Source = "invalid-explicit-override"
		status.Hint = "remove or repair the invalid explicit tun2socks development override"
	} else if resolution.Path != "" {
		status.Status = PrerequisiteAvailable
		status.Source = resolution.Source
		status.PackageOwned = resolution.Source == helperbin.Tun2SocksSourcePackage
		if !status.PackageOwned {
			status.Hint = "explicit development override; package claims require the package-owned helper"
		}
	}
	return []ExternalPrerequisiteStatus{status}
}
