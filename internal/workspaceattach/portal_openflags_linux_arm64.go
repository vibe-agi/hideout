//go:build linux && arm64

package workspaceattach

// Linux arm64 passes the kernel ABI O_LARGEFILE bit through FUSE even though
// Go correctly exposes O_LARGEFILE as zero for 64-bit user-space open calls.
const portalLinuxArm64KernelLargeFile = 0x20000

func portalIgnoredKernelOpenFlags() int { return portalLinuxArm64KernelLargeFile }
