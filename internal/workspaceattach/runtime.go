package workspaceattach

import (
	"errors"
	"net"
	"path/filepath"
	"strings"
)

const PortalCredentialGuestPath = "/hideout/session/workspace/credential.bin"

// PortalRuntime is the non-secret binding produced by the daemon-owned view.
// The credential bytes remain in the private host file named here and are not
// represented in Manager plans, status, or backend configuration.
type PortalRuntime struct {
	Endpoint            string
	CredentialHostPath  string
	CredentialGuestPath string
}

func (runtime PortalRuntime) Validate(attachment Attachment) error {
	if err := attachment.Validate(); err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(runtime.Endpoint))
	if err != nil || host == "" || port == "" || port == "0" {
		return errors.New("workspace Portal runtime endpoint is invalid")
	}
	if !filepath.IsAbs(runtime.CredentialHostPath) || filepath.Clean(runtime.CredentialHostPath) != runtime.CredentialHostPath {
		return errors.New("workspace Portal host credential path is invalid")
	}
	if runtime.CredentialGuestPath != PortalCredentialGuestPath {
		return errors.New("workspace Portal guest credential path is invalid")
	}
	return nil
}
