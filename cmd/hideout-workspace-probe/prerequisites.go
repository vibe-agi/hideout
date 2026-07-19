package main

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"

	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func runPrerequisiteProbe(args []string) error {
	fs := flag.NewFlagSet("hideout-workspace-probe prerequisites", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", "", "absolute host workspace root")
	output := fs.String("output", "", "absolute JSON output path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !filepath.IsAbs(*root) || !filepath.IsAbs(*output) {
		return errors.New("prerequisites probe requires absolute --root and --output")
	}
	report := workspaceattach.ProbeHostRootPrerequisite(*root)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeProbeJSONAtomically(*output, data)
}
