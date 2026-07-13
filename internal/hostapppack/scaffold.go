package hostapppack

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type ScaffoldRequest struct {
	Directory              string
	PackID                 string
	AppID                  string
	Command                string
	BundleName             string
	ExecutableRelativePath string
}

func Scaffold(request ScaffoldRequest) error {
	if request.Directory == "" || !filepath.IsAbs(request.Directory) {
		return errors.New("host-app scaffold destination must be absolute")
	}
	manifest := Manifest{
		SchemaVersion: ManifestVersion,
		ID:            request.PackID,
		Version:       "0.1.0",
		Description:   "Local host application recipe for " + request.Command,
		Apps: []AppSpec{{
			ID: request.AppID, Platforms: []string{PlatformDarwin},
			BundleNames: []string{request.BundleName}, ExecutableRelativePath: request.ExecutableRelativePath,
			Launch: LaunchSpec{},
		}},
		Bindings: []BindingSpec{{
			ID: "open-resource", Commands: []string{request.Command}, AppID: request.AppID,
			CapabilityID: CapabilityOpenResource, ResourceKinds: []string{ResourceWorkspace, ResourceHostFSPortal},
			ResultPolicy: ResultNone, RequestedAccess: AccessAskEachRun,
			Grammar: GrammarSpec{
				Kind: GrammarOpenResourceV1, ResourceCount: 1,
				GotoFlags: []string{"-g", "--goto"}, NewWindowFlags: []string{"-n", "--new-window"},
				ReuseWindowFlags: []string{"-r", "--reuse-window"}, UnknownFlags: UnknownFlagsDeny,
			},
		}},
		Tests: []TestVector{{
			ID: "open-current-directory", BindingID: "open-resource", Argv: []string{request.Command, "."},
			Expected: TestExpectation{Resource: "/workspace", WindowMode: "reuse"},
		}},
	}
	if err := ValidateManifest(manifest); err != nil {
		return fmt.Errorf("host-app scaffold request: %w", err)
	}
	info, err := os.Lstat(request.Directory)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.Mkdir(request.Directory, 0o700); err != nil {
			return err
		}
	case err != nil:
		return err
	case !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
		return errors.New("host-app scaffold destination must be a real directory")
	default:
		entries, err := os.ReadDir(request.Directory)
		if err != nil {
			return err
		}
		if len(entries) != 0 {
			return errors.New("host-app scaffold destination is not empty")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return errors.New("host-app scaffold destination must be private")
		}
	}
	raw, err := json.MarshalIndent(NormalizeManifest(manifest), "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(request.Directory, ManifestFileName), raw, 0o600); err != nil {
		return err
	}
	readme := []byte("# " + request.PackID + "\n\nUntrusted local host-app recipe. Review with `hideout app inspect` before enablement.\n")
	if err := os.WriteFile(filepath.Join(request.Directory, "README.md"), readme, 0o600); err != nil {
		_ = os.Remove(filepath.Join(request.Directory, ManifestFileName))
		return err
	}
	return nil
}
