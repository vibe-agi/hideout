package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/vibe-agi/hideout/internal/lifecycle"
)

func main() {
	commit := flag.String("commit", "", "canonical candidate commit")
	dirty := flag.Bool("dirty", false, "candidate worktree is dirty")
	flag.Parse()
	if len(*commit) != 40 {
		fmt.Fprintln(os.Stderr, "lifecycle model: --commit must be a canonical 40-character commit")
		os.Exit(2)
	}
	report, err := lifecycle.RunBoundedModelCheck()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lifecycle model: %v\n", err)
		os.Exit(1)
	}
	output := struct {
		lifecycle.ModelCheckReport
		GeneratedAt string `json:"generatedAt"`
		Commit      string `json:"commit"`
		Dirty       bool   `json:"dirty"`
	}{
		ModelCheckReport: report,
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
		Commit:           *commit,
		Dirty:            *dirty,
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "lifecycle model: encode report: %v\n", err)
		os.Exit(1)
	}
}
