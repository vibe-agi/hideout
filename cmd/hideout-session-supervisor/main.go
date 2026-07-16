package main

import (
	"fmt"
	"os"
)

func main() {
	if err := runSupervisor(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "hideout-session-supervisor:", err)
		os.Exit(1)
	}
}
