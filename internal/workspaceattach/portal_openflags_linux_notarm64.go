//go:build linux && !arm64

package workspaceattach

func portalIgnoredKernelOpenFlags() int { return 0 }
