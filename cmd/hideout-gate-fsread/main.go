package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	readPath := flag.String("read", "", "path that must be readable")
	denyPath := flag.String("deny", "", "path that must not be readable")
	flag.Parse()
	if *readPath == "" || *denyPath == "" {
		fmt.Fprintln(os.Stderr, "usage: hideout-gate-fsread --read <path> --deny <path>")
		os.Exit(2)
	}
	data, err := os.ReadFile(*readPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read granted path: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("hostfs_go=%s\n", strings.TrimSpace(string(data)))
	if _, err := os.ReadFile(*denyPath); err == nil {
		fmt.Fprintln(os.Stderr, "denied path unexpectedly readable")
		os.Exit(1)
	}
	fmt.Println("hostfs_go_denied=yes")
}
