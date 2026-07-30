package main

import (
	"fmt"
	"os"
)

func main() {
	if err := runSupervisorCommand(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "hideout-session-supervisor:", err)
		os.Exit(1)
	}
}
