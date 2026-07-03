package main

import (
	"os"

	"github.com/vibe-agi/hideout/internal/app"
)

func main() {
	os.Exit(app.Main(os.Args[1:], os.Stdout, os.Stderr))
}
