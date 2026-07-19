// Command hideout-workspace-probe runs bounded 035 research probes.
//
// It is intentionally not dispatched by hideout and carries no product
// authority. Phase R may use it to produce evidence; production must remove
// losing candidate code and package the selected implementation separately.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

const probeVersion = "hideout.workspace-probe/v1"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "hideout-workspace-probe:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "portal-serve" {
		return runPortalServe(args[1:])
	}
	if len(args) > 0 && args[0] == "portal-mount" {
		return runPortalMount(args[1:])
	}
	if len(args) > 0 && args[0] == "prerequisites" {
		return runPrerequisiteProbe(args[1:])
	}
	if len(args) > 0 && args[0] == "path-identity" {
		return runPathIdentityProbe(args[1:])
	}
	if len(args) > 0 && args[0] == "decision-validate" {
		return runDecisionValidate(args[1:])
	}
	fs := flag.NewFlagSet("hideout-workspace-probe", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	showVersion := fs.Bool("version", false, "print the research probe contract")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*showVersion || fs.NArg() != 0 {
		return fmt.Errorf("research-only probe requires --version until a Phase R candidate is selected")
	}
	fmt.Fprintln(os.Stdout, probeVersion)
	return nil
}

func runDecisionValidate(args []string) error {
	fs := flag.NewFlagSet("hideout-workspace-probe decision-validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	artifactRoot := fs.String("artifact-root", "", "absolute Phase R artifact root")
	decisionPath := fs.String("decision", "", "absolute decision artifact path")
	expectedCommit := fs.String("expected-commit", "", "exact 40-hex source commit")
	allowDirty := fs.Bool("allow-dirty", false, "allow dirty research evidence; never valid for promotion")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !filepath.IsAbs(*artifactRoot) || !filepath.IsAbs(*decisionPath) || *expectedCommit == "" {
		return fmt.Errorf("decision-validate requires absolute --artifact-root, absolute --decision, and --expected-commit")
	}
	decision, err := workspaceattach.LoadResearchDecision(*decisionPath, workspaceattach.ResearchEvaluationOptions{
		ArtifactRoot:   *artifactRoot,
		ExpectedCommit: *expectedCommit,
		AllowDirty:     *allowDirty,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "%s\t%s\t%s\n", decision.Result, decision.SelectedCandidate, *decisionPath)
	return nil
}
