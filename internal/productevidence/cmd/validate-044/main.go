package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/vibe-agi/hideout/internal/productevidence"
)

func main() {
	target := flag.String("target", "targeted-completion", "targeted-completion or release-candidate")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: validate-044 [--target targeted-completion|release-candidate] <manifest>")
		os.Exit(2)
	}
	manifest, err := productevidence.ReadFile(flag.Arg(0))
	if err == nil {
		switch *target {
		case "targeted-completion":
			err = productevidence.Require044TargetedComplete(manifest)
		case "release-candidate":
			err = productevidence.Require044ReleaseComplete(manifest)
		default:
			err = fmt.Errorf("unsupported target %q", *target)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "validate-044:", err)
		os.Exit(1)
	}
	fmt.Printf("validate-044: %s passed\n", *target)
}
