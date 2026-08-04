package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/vibe-agi/hideout/internal/helperbin"
)

func main() {
	var options helperbin.BuildOptions
	flag.StringVar(&options.Out, "out", "", "output path")
	flag.StringVar(&options.GOARCH, "goarch", "", "Darwin host architecture")
	flag.StringVar(&options.Source, "source", ".", "Hideout source root")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(
			os.Stderr,
			"build-migration-vz-adopt: positional arguments are not supported",
		)
		os.Exit(2)
	}
	if err := helperbin.BuildHostMigrationVZAdopt(options); err != nil {
		fmt.Fprintln(os.Stderr, "build-migration-vz-adopt:", err)
		os.Exit(1)
	}
}
