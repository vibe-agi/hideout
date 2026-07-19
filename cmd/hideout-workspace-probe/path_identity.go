package main

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"

	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func runPathIdentityProbe(args []string) error {
	fs := flag.NewFlagSet("hideout-workspace-probe path-identity", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	inputPath := fs.String("input", "", "absolute observation input path")
	outputPath := fs.String("output", "", "absolute report output path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !filepath.IsAbs(*inputPath) || !filepath.IsAbs(*outputPath) {
		return errors.New("path-identity requires absolute --input and --output")
	}
	input, err := workspaceattach.LoadPathIdentityProbeInput(*inputPath)
	if err != nil {
		return err
	}
	report, err := workspaceattach.EvaluatePathIdentityProbe(input, lima.WorkspacePathMechanism)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return writeProbeJSONAtomically(*outputPath, append(data, '\n'))
}

func writeProbeJSONAtomically(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filepath.Clean(path)); err != nil {
		return err
	}
	keepTemp = false
	return nil
}
