package main

import (
	"fmt"
	"os"
)

func main() {
	if err := runObserverCommand(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "hideout-observer:", err)
		os.Exit(1)
	}
}
