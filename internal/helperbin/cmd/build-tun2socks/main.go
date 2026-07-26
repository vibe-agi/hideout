package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/vibe-agi/hideout/internal/helperbin"
)

func main() {
	var opts helperbin.BuildOptions
	flag.StringVar(&opts.Out, "out", "", "output path")
	flag.StringVar(&opts.GOARCH, "goarch", "", "Linux guest architecture")
	flag.StringVar(&opts.Source, "source", ".", "Hideout source root")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "build-tun2socks: positional arguments are not supported")
		os.Exit(2)
	}
	if err := helperbin.BuildLinuxTun2Socks(opts); err != nil {
		fmt.Fprintln(os.Stderr, "build-tun2socks:", err)
		os.Exit(1)
	}
}
