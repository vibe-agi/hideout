//go:build !linux

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "hideout-hostfsd: HostFS FUSE daemon is only supported on Linux guests")
	os.Exit(1)
}
