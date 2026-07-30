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
	flag.StringVar(&options.GOARCH, "goarch", "", "Linux guest architecture")
	flag.StringVar(&options.Source, "source", ".", "Hideout source root")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "build-observer: positional arguments are not supported")
		os.Exit(2)
	}
	if err := helperbin.BuildLinuxObserver(options); err != nil {
		fmt.Fprintln(os.Stderr, "build-observer:", err)
		os.Exit(1)
	}
}
