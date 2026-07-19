package main

import (
	"fmt"
	"os"

	"github.com/vibe-agi/hideout/internal/workspaceportal"
)

func main() {
	if err := workspaceportal.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "hideout-workspace-portal: %v\n", err)
		os.Exit(1)
	}
}
