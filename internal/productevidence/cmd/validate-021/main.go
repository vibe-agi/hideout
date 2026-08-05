package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vibe-agi/hideout/internal/productevidence"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: validate-021 <manifest>")
		os.Exit(2)
	}
	path, err := filepath.Abs(os.Args[1])
	if err == nil {
		var manifest productevidence.Manifest
		manifest, err = productevidence.ReadFile(path)
		if err == nil {
			err = productevidence.Require021Complete(manifest)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "validate-021:", err)
		os.Exit(1)
	}
	fmt.Println("validate-021: targeted-completion passed")
}
