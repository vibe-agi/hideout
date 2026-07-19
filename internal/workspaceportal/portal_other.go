//go:build !linux

package workspaceportal

import (
	"errors"
)

func Run([]string) error {
	return errors.New("hideout-workspace-portal requires Linux with FUSE")
}
