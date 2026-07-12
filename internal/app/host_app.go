package app

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
)

func (a app) hostAppCommand(args []string) error {
	if len(args) == 0 || containsHelpToken(args) {
		fmt.Fprintln(a.stdout, "Usage: hideout app init|add|list|inspect|validate|test|enable|update|disable|remove|revoke")
		return nil
	}
	if args[0] == "init" {
		return a.hostAppInit(args[1:])
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	return a.hostAppCommandWithCore(manager.New(store), args)
}

func (a app) hostAppCommandWithCore(core manager.Core, args []string) error {
	switch args[0] {
	case "add":
		opts, yes, jsonOutput, err := parseHostAppAdd(args[1:])
		if err != nil {
			return err
		}
		plan, err := core.PlanHostAppPack(opts)
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := writeIndentedJSON(a.stdout, plan); err != nil {
				return err
			}
		} else {
			writeHostAppPlanReview(a.stdout, plan)
		}
		prompt := "Test, install, and enable this host-app recipe for future runs?"
		if plan.InstallOnly {
			prompt = "Install this inert host-app pack revision?"
		}
		if ok, err := a.confirmHostAppApply(yes, prompt); err != nil {
			return err
		} else if !ok {
			return errors.New("host-app add was not confirmed")
		}
		result, err := core.ApplyHostAppPack(plan)
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSONLine(a.stdout, result)
		}
		writeHostAppApplySummary(a.stdout, result)
		return nil
	case "list":
		fs := flag.NewFlagSet("app list", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		jsonOutput := fs.Bool("json", false, "emit JSON")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
			return errors.New("usage: hideout app list [--json]")
		}
		packs, err := core.ListHostAppPacks()
		if err != nil {
			return err
		}
		if *jsonOutput {
			return writeJSONLine(a.stdout, map[string]any{"hostAppPacks": packs})
		}
		writeHostAppPackList(a.stdout, packs)
		return nil
	case "inspect":
		fs := flag.NewFlagSet("app inspect", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		profileName := fs.String("profile", "", "profile enablement scope")
		jsonOutput := fs.Bool("json", false, "emit JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("usage: hideout app inspect [--profile <name>] <pack-id>")
		}
		inspection, err := core.InspectHostAppPack(fs.Arg(0), *profileName)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return writeIndentedJSON(a.stdout, inspection.Status)
		}
		writeHostAppInspection(a.stdout, inspection.Status)
		return nil
	case "validate", "test":
		opts, err := parseHostAppRevisionOperation(args[0], args[1:])
		if err != nil {
			return err
		}
		plan, err := core.PlanHostAppPack(opts)
		if err != nil {
			return err
		}
		result, err := core.ApplyHostAppPack(plan)
		if err != nil {
			return err
		}
		return writeJSONLine(a.stdout, result)
	case "enable":
		opts, yes, jsonOutput, err := parseHostAppEnable(args[1:])
		if err != nil {
			return err
		}
		plan, err := core.PlanHostAppPack(opts)
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := writeIndentedJSON(a.stdout, plan); err != nil {
				return err
			}
		} else {
			writeHostAppPlanReview(a.stdout, plan)
		}
		if ok, err := a.confirmHostAppApply(yes, "Enable these exact bindings for future runs?"); err != nil {
			return err
		} else if !ok {
			return errors.New("host-app enable was not confirmed")
		}
		result, err := core.ApplyHostAppPack(plan)
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSONLine(a.stdout, result)
		}
		writeHostAppApplySummary(a.stdout, result)
		return nil
	case "update":
		opts, yes, jsonOutput, err := parseHostAppUpdate(args[1:])
		if err != nil {
			return err
		}
		plan, err := core.PlanHostAppPack(opts)
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := writeIndentedJSON(a.stdout, plan); err != nil {
				return err
			}
		} else {
			writeHostAppPlanReview(a.stdout, plan)
		}
		if ok, err := a.confirmHostAppApply(yes, "Install and select this exact reviewed update for future runs?"); err != nil {
			return err
		} else if !ok {
			return errors.New("host-app update was not confirmed")
		}
		result, err := core.ApplyHostAppPack(plan)
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSONLine(a.stdout, result)
		}
		writeHostAppApplySummary(a.stdout, result)
		return nil
	case "disable", "remove", "revoke":
		opts, yes, jsonOutput, err := parseHostAppStateChange(args[0], args[1:])
		if err != nil {
			return err
		}
		plan, err := core.PlanHostAppPack(opts)
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := writeIndentedJSON(a.stdout, plan); err != nil {
				return err
			}
		} else {
			writeHostAppPlanReview(a.stdout, plan)
		}
		if ok, err := a.confirmHostAppApply(yes, "Apply this reviewed host-app lifecycle change?"); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("host-app %s was not confirmed", args[0])
		}
		result, err := core.ApplyHostAppPack(plan)
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSONLine(a.stdout, result)
		}
		writeHostAppApplySummary(a.stdout, result)
		return nil
	default:
		return fmt.Errorf("unknown app command %q", args[0])
	}
}

func parseHostAppUpdate(args []string) (manager.HostAppPackOptions, bool, bool, error) {
	fs := flag.NewFlagSet("app update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("path", "", "local pack directory")
	gitURL := fs.String("git", "", "Git source")
	commit := fs.String("commit", "", "exact 40-hex Git commit")
	profileName := fs.String("profile", "default", "profile name")
	packID := fs.String("pack", "", "installed pack id")
	access := fs.String("access", "", "safe or ask-each-run")
	expectedDigest := fs.String("expected-digest", "", "expected exact source digest")
	reason := fs.String("reason", "", "operator lifecycle reason")
	yes := fs.Bool("yes", false, "confirm after review")
	jsonOutput := fs.Bool("json", false, "emit JSON")
	var bindings stringListFlag
	var replacements stringListFlag
	fs.Var(&bindings, "binding", "binding id; repeatable")
	fs.Var(&replacements, "replace", "exact command=old-owner replacement; repeatable")
	if err := fs.Parse(args); err != nil {
		return manager.HostAppPackOptions{}, false, false, err
	}
	if fs.NArg() != 0 || *packID == "" || (*path == "") == (*gitURL == "") {
		return manager.HostAppPackOptions{}, false, false, errors.New("usage: hideout app update (--path <dir> | --git <url> --commit <sha>) --pack <id> [--profile <name>] [--yes] [--json]")
	}
	replacementMap, err := parseHostAppReplacements(replacements)
	if err != nil {
		return manager.HostAppPackOptions{}, false, false, err
	}
	opts := manager.HostAppPackOptions{
		Operation: "update", ProfileName: *profileName, PackID: *packID, BindingIDs: []string(bindings),
		Access: *access, Replacements: replacementMap, ExpectedDigest: *expectedDigest, Reason: *reason,
	}
	if *gitURL != "" {
		opts.SourceKind, opts.SourceURL, opts.SourceCommit = hostapppack.SourceGit, *gitURL, *commit
	} else {
		opts.SourceKind, opts.SourcePath = hostapppack.SourceLocal, *path
	}
	return opts, *yes, *jsonOutput, nil
}

func parseHostAppStateChange(operation string, args []string) (manager.HostAppPackOptions, bool, bool, error) {
	fs := flag.NewFlagSet("app "+operation, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profileName := fs.String("profile", "default", "profile name")
	packID := fs.String("pack", "", "pack id")
	revisionID := fs.String("revision", "", "exact revision id")
	reason := fs.String("reason", "", "operator lifecycle reason")
	yes := fs.Bool("yes", false, "confirm after review")
	jsonOutput := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return manager.HostAppPackOptions{}, false, false, err
	}
	if fs.NArg() != 0 || *packID == "" {
		return manager.HostAppPackOptions{}, false, false, fmt.Errorf("usage: hideout app %s --pack <id> [--profile <name>] [--revision <id>] [--reason <text>] [--yes] [--json]", operation)
	}
	opts := manager.HostAppPackOptions{Operation: operation, PackID: *packID, RevisionID: *revisionID, Reason: *reason}
	if operation == "disable" {
		opts.ProfileName = *profileName
	}
	return opts, *yes, *jsonOutput, nil
}

func (a app) hostAppInit(args []string) error {
	fs := flag.NewFlagSet("app init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", "", "new private recipe directory")
	packID := fs.String("id", "", "pack id")
	appID := fs.String("app-id", "", "application id")
	command := fs.String("command", "", "projected command")
	bundle := fs.String("bundle", "", "application bundle basename")
	executable := fs.String("executable", "", "executable path relative to bundle")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected app init argument %q", fs.Arg(0))
	}
	directory := *dir
	if directory != "" && !filepath.IsAbs(directory) {
		absolute, err := filepath.Abs(directory)
		if err != nil {
			return fmt.Errorf("resolve app scaffold directory: %w", err)
		}
		directory = absolute
	}
	if err := hostapppack.Scaffold(hostapppack.ScaffoldRequest{Directory: directory, PackID: *packID, AppID: *appID, Command: *command, BundleName: *bundle, ExecutableRelativePath: *executable}); err != nil {
		return err
	}
	return writeJSONLine(a.stdout, map[string]any{"created": true, "directory": directory, "packId": *packID})
}

func parseHostAppAdd(args []string) (manager.HostAppPackOptions, bool, bool, error) {
	fs := flag.NewFlagSet("app add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("path", "", "local pack directory")
	gitURL := fs.String("git", "", "HTTPS Git source")
	commit := fs.String("commit", "", "exact 40-hex Git commit")
	profileName := fs.String("profile", "default", "profile name")
	access := fs.String("access", "", "safe or ask-each-run")
	expectedDigest := fs.String("expected-digest", "", "expected exact source digest")
	installOnly := fs.Bool("install-only", false, "store the immutable revision without enabling commands")
	yes := fs.Bool("yes", false, "confirm after review")
	jsonOutput := fs.Bool("json", false, "emit the plan and result as JSON")
	var bindings stringListFlag
	var replacements stringListFlag
	fs.Var(&bindings, "binding", "binding id; repeatable")
	fs.Var(&replacements, "replace", "exact command=old-owner replacement; repeatable")
	if err := fs.Parse(args); err != nil {
		return manager.HostAppPackOptions{}, false, false, err
	}
	if fs.NArg() != 0 || (*path == "") == (*gitURL == "") {
		return manager.HostAppPackOptions{}, false, false, errors.New("usage: hideout app add (--path <dir> | --git <https-url> --commit <sha>) [--profile <name>] [--binding <id>] [--access safe|ask-each-run] [--install-only] [--yes] [--json]")
	}
	replacementMap, err := parseHostAppReplacements(replacements)
	if err != nil {
		return manager.HostAppPackOptions{}, false, false, err
	}
	opts := manager.HostAppPackOptions{
		Operation: "add", ProfileName: *profileName, BindingIDs: []string(bindings), Access: *access,
		Replacements: replacementMap, ExpectedDigest: *expectedDigest, InstallOnly: *installOnly,
	}
	if *gitURL != "" {
		opts.SourceKind, opts.SourceURL, opts.SourceCommit = hostapppack.SourceGit, *gitURL, *commit
		return opts, *yes, *jsonOutput, nil
	}
	opts.SourceKind, opts.SourcePath = hostapppack.SourceLocal, *path
	return opts, *yes, *jsonOutput, nil
}

func parseHostAppRevisionOperation(operation string, args []string) (manager.HostAppPackOptions, error) {
	fs := flag.NewFlagSet("app "+operation, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	revision := fs.String("revision", "", "exact revision id")
	path := fs.String("path", "", "local host-app pack directory")
	gitURL := fs.String("git", "", "host-app pack Git URL")
	commit := fs.String("commit", "", "exact 40-hex Git commit")
	expectedDigest := fs.String("expected-digest", "", "expected immutable source digest")
	if err := fs.Parse(args); err != nil {
		return manager.HostAppPackOptions{}, err
	}
	sourceSelected := *path != "" || *gitURL != "" || *commit != "" || *expectedDigest != ""
	if sourceSelected {
		if fs.NArg() != 0 || *revision != "" || (*path == "") == (*gitURL == "") || (*gitURL != "" && *commit == "") || (*path != "" && *commit != "") {
			return manager.HostAppPackOptions{}, fmt.Errorf("usage: hideout app %s (--path <dir> | --git <url> --commit <40-hex>) [--expected-digest <sha256>]", operation)
		}
		opts := manager.HostAppPackOptions{Operation: operation, ExpectedDigest: *expectedDigest}
		if *gitURL != "" {
			opts.SourceKind, opts.SourceURL, opts.SourceCommit = hostapppack.SourceGit, *gitURL, *commit
		} else {
			opts.SourceKind, opts.SourcePath = hostapppack.SourceLocal, *path
		}
		return opts, nil
	}
	if fs.NArg() != 1 {
		return manager.HostAppPackOptions{}, fmt.Errorf("usage: hideout app %s [--revision <id>] <pack-id>", operation)
	}
	return manager.HostAppPackOptions{Operation: operation, PackID: fs.Arg(0), RevisionID: *revision}, nil
}

func parseHostAppEnable(args []string) (manager.HostAppPackOptions, bool, bool, error) {
	fs := flag.NewFlagSet("app enable", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profileName := fs.String("profile", "default", "profile name")
	packID := fs.String("pack", "", "pack id")
	revision := fs.String("revision", "", "exact revision id")
	access := fs.String("access", "", "safe or ask-each-run")
	yes := fs.Bool("yes", false, "confirm after review")
	jsonOutput := fs.Bool("json", false, "emit the plan and result as JSON")
	var bindings stringListFlag
	var replacements stringListFlag
	fs.Var(&bindings, "binding", "binding id; repeatable")
	fs.Var(&replacements, "replace", "exact command=old-owner replacement; repeatable")
	if err := fs.Parse(args); err != nil {
		return manager.HostAppPackOptions{}, false, false, err
	}
	if fs.NArg() != 0 || *packID == "" {
		return manager.HostAppPackOptions{}, false, false, errors.New("usage: hideout app enable --profile <name> --pack <id> [--revision <id>] [--binding <id>] [--access safe|ask-each-run] [--yes] [--json]")
	}
	replacementMap, err := parseHostAppReplacements(replacements)
	if err != nil {
		return manager.HostAppPackOptions{}, false, false, err
	}
	return manager.HostAppPackOptions{Operation: "enable", ProfileName: *profileName, PackID: *packID, RevisionID: *revision, BindingIDs: []string(bindings), Access: *access, Replacements: replacementMap}, *yes, *jsonOutput, nil
}

func parseHostAppReplacements(values []string) (map[string]string, error) {
	replacementMap := map[string]string{}
	for _, raw := range values {
		command, owner, ok := strings.Cut(raw, "=")
		if !ok || strings.TrimSpace(command) == "" || strings.TrimSpace(owner) == "" {
			return nil, fmt.Errorf("invalid --replace %q; expected command=old-owner", raw)
		}
		replacementMap[strings.TrimSpace(command)] = strings.TrimSpace(owner)
	}
	return replacementMap, nil
}

func writeHostAppPlanReview(w io.Writer, plan manager.HostAppPackPlan) {
	clean := func(value string) string { return manager.SanitizeHostAppDisplayText(value, 2048) }
	source := clean(plan.SourceReview.Kind)
	if plan.SourceReview.Location != "" {
		source += " " + clean(plan.SourceReview.Location)
	}
	if plan.SourceReview.Commit != "" {
		source += " @ " + clean(plan.SourceReview.Commit)
	}
	formatList := func(values []string) string {
		if len(values) == 0 {
			return "none"
		}
		cleaned := make([]string, 0, len(values))
		for _, value := range values {
			cleaned = append(cleaned, clean(value))
		}
		return strings.Join(cleaned, ", ")
	}
	fmt.Fprintln(w, "Host application recipe review")
	fmt.Fprintf(w, "  Source: %s\n", source)
	fmt.Fprintf(w, "  Snapshot: %s\n", clean(plan.ExpectedSourceDigest))
	fmt.Fprintf(w, "  Package: %s %s\n", clean(plan.Review.PackID), clean(plan.Review.Version))
	fmt.Fprintf(w, "  Package description [untrusted]: %s\n", clean(plan.Review.Description))
	if plan.Review.InstallHint != "" || plan.Review.InstallHintURL != "" {
		fmt.Fprintf(w, "  Installation hint [untrusted, copy only]: %s %s\n", clean(plan.Review.InstallHint), clean(plan.Review.InstallHintURL))
	}
	fmt.Fprintf(w, "  Commands: %s\n", formatList(plan.Review.Commands))
	fmt.Fprintf(w, "  Host applications: %s\n", formatList(plan.Review.Applications))
	for _, app := range plan.Review.ApplicationsDeclared {
		fmt.Fprintf(w, "  Package app declaration [untrusted]: %s bundles=%s executable=%s expected-bundle=%s expected-team=%s requested-safety=%s\n",
			clean(app.AppID), formatList(app.BundleNames), clean(app.ExecutableRelativePath),
			clean(app.ExpectedBundleID), clean(app.ExpectedTeamID), clean(app.RequestedSafetyProfile))
	}
	fmt.Fprintf(w, "  Resource kinds: %s\n", formatList(plan.Review.ResourceKinds))
	fmt.Fprintf(w, "  Result policy: %s\n", formatList(plan.Review.ResultPolicies))
	fmt.Fprintf(w, "  Access: %s\n", clean(plan.Access))
	for _, identity := range plan.Review.ApplicationsObserved {
		fmt.Fprintf(w, "  Core-observed app trust: %s=%s root=%s owner=%s bundle=%s team=%s code=%s identity=%s\n",
			clean(identity.AppID), clean(identity.Verification), clean(identity.RootClass), clean(identity.OwnerClass),
			clean(identity.BundleID), clean(identity.TeamID), clean(identity.CodeIdentity), clean(identity.IdentityDigest))
		if identity.ContentDigest != "" {
			fmt.Fprintf(w, "  Core-observed app content: %s\n", clean(identity.ContentDigest))
		}
	}
	if plan.PreviousRevisionID != "" {
		fmt.Fprintf(w, "  Previous revision: %s\n", clean(plan.PreviousRevisionID))
		fmt.Fprintf(w, "  Candidate revision: %s\n", clean(plan.RevisionID))
		fmt.Fprintf(w, "  Permission changes: %d (fresh acceptance required: %t)\n", plan.PermissionDiff.TotalChanges, plan.PermissionChanged)
		for _, change := range plan.PermissionDiff.Changed {
			fmt.Fprintf(w, "    %s: %s -> %s\n", clean(change.Key), clean(change.Before), clean(change.After))
		}
		for _, item := range plan.PermissionDiff.Added {
			fmt.Fprintf(w, "    + %s=%s\n", clean(item.Key), clean(item.Value))
		}
		for _, item := range plan.PermissionDiff.Removed {
			fmt.Fprintf(w, "    - %s=%s\n", clean(item.Key), clean(item.Value))
		}
	}
	if plan.QualityTestStatus != "" {
		fmt.Fprintf(w, "  Package quality tests: %s (advisory; Core constraints remain authoritative)\n", clean(plan.QualityTestStatus))
	}
	safety := "none (approval required each run)"
	if plan.SafetyProfileID != "" {
		safety = clean(plan.SafetyProfileID) + "@" + clean(plan.SafetyProfileVersion)
	}
	fmt.Fprintf(w, "  Core safety profile: %s\n", safety)
	if plan.ExpectedIdentityDigest != "" {
		fmt.Fprintf(w, "  Core-observed app identity: %s\n", clean(plan.ExpectedIdentityDigest))
	}
	if len(plan.CommandPlan.Replacements) == 0 {
		fmt.Fprintln(w, "  Command conflicts: none")
	} else {
		for _, replacement := range plan.CommandPlan.Replacements {
			fmt.Fprintf(w, "  Command replacement: %s (%s -> %s)\n", clean(replacement.Command), clean(replacement.FromOwner), clean(replacement.ToOwner))
		}
	}
	if plan.Operation == "disable" || plan.Operation == "revoke" || plan.Operation == "remove" {
		fmt.Fprintf(w, "  Effect: %s\n", clean(plan.Message))
	} else if plan.InstallOnly {
		fmt.Fprintln(w, "  Effect: store immutable bytes only; no command is enabled")
	} else {
		fmt.Fprintf(w, "  Profile: %s\n", clean(plan.Profile))
		fmt.Fprintln(w, "  Effect: future runs only; existing sessions are unchanged")
	}
}

func writeHostAppApplySummary(w io.Writer, result manager.HostAppPackResult) {
	mode := "installed without enabling commands"
	if result.Enablement != nil {
		mode = "tested, installed, and enabled for future runs"
	}
	switch result.Plan.Operation {
	case "update":
		mode = "updated to an exact reviewed revision for future runs"
	case "disable":
		mode = "disabled for future runs"
	case "revoke":
		mode = "revoked across profiles"
	case "remove":
		mode = "removed with a retained tombstone"
	}
	fmt.Fprintf(w, "Host application recipe %s: %s (%s)\n", mode, manager.SanitizeHostAppDisplayText(result.Plan.PackID, 128), manager.SanitizeHostAppDisplayText(result.Plan.RevisionID, 128))
}

func writeHostAppPackList(w io.Writer, packs []manager.HostAppPackSummary) {
	fmt.Fprintln(w, "Host application recipe packs")
	for _, pack := range packs {
		fmt.Fprintf(w, "  %s  state=%s  revision=%s  revisions=%d\n",
			manager.SanitizeHostAppDisplayText(pack.PackID, 128), manager.SanitizeHostAppDisplayText(pack.State, 64),
			manager.SanitizeHostAppDisplayText(pack.ActiveRevisionID, 128), pack.RevisionCount)
	}
}

func writeHostAppInspection(w io.Writer, inspection hostapppack.Inspection) {
	fmt.Fprintln(w, "Host application recipes")
	if len(inspection.Entries) == 0 {
		fmt.Fprintln(w, "  none")
		return
	}
	for _, entry := range inspection.Entries {
		clean := func(value string) string { return manager.SanitizeHostAppDisplayText(value, 512) }
		list := func(values []string) string {
			if len(values) == 0 {
				return "none"
			}
			cleaned := make([]string, 0, len(values))
			for _, value := range values {
				cleaned = append(cleaned, clean(value))
			}
			return strings.Join(cleaned, ", ")
		}
		fmt.Fprintf(w, "  %s  app=%s  pack=%s@%s\n", clean(entry.Summary.Command), clean(entry.Summary.App), clean(entry.Package.ID), clean(entry.Package.RevisionID))
		fmt.Fprintf(w, "    readiness=%s access=%s identity=%s safety=%s permissions=%s grant=%s shadow=%s\n",
			clean(entry.Summary.Readiness), clean(entry.Summary.Access), clean(entry.AppIdentity.Verification), clean(entry.Safety.Posture),
			clean(entry.Permissions.Status), clean(entry.Runtime.GrantState), clean(entry.Binding.ShadowStatus))
		fmt.Fprintf(w, "    source=%s digest=%s quality-tests=%s\n",
			clean(entry.Package.SourceKind), clean(entry.Package.SourceDigest), clean(entry.Package.TestStatus))
		fmt.Fprintf(w, "    app-root=%s owner=%s bundle=%s team=%s code-identity=%s content=%s\n",
			clean(entry.AppIdentity.RootClass), clean(entry.AppIdentity.OwnerClass), clean(entry.AppIdentity.BundleID),
			clean(entry.AppIdentity.TeamID), clean(entry.AppIdentity.CodeIdentity), clean(entry.AppIdentity.ContentDigest))
		fmt.Fprintf(w, "    binding=%s commands=%s capability=%s grammar=%s result=%s resources=%s\n",
			clean(entry.Binding.ID), list(entry.Binding.Commands), clean(entry.Binding.CapabilityID), clean(entry.Binding.Grammar),
			clean(entry.Binding.ResultPolicy), list(entry.Binding.ResourceKinds))
		fmt.Fprintf(w, "    permission-fingerprint=%s\n", clean(entry.Permissions.Fingerprint))
		for _, diff := range entry.Permissions.Diff {
			fmt.Fprintf(w, "      permission-diff=%s\n", clean(diff))
		}
		fmt.Fprintf(w, "    safety-requested=%s safety-compatible=%s\n",
			clean(entry.Safety.RequestedProfile), clean(entry.Safety.CompatibleProfile))
		fmt.Fprintf(w, "    active-current-run=%t last-outcome=%s audit=%s\n",
			entry.Runtime.ActiveInCurrentRun, clean(entry.Runtime.LastOutcome), clean(entry.Runtime.AuditRef))
		if entry.Summary.NextAction != "" {
			fmt.Fprintf(w, "    next=%s\n", clean(entry.Summary.NextAction))
		}
		if entry.Hint != nil {
			fmt.Fprintf(w, "    installation hint [untrusted, copy only]=%s %s\n", clean(entry.Hint.Text), clean(entry.Hint.URL))
		}
	}
}

func (a app) confirmHostAppApply(yes bool, prompt string) (bool, error) {
	return a.confirmHostAppApplyWithTerminal(yes, prompt, stdinIsTerminal)
}

func (a app) confirmHostAppApplyWithTerminal(yes bool, prompt string, isTerminal func() bool) (bool, error) {
	if yes {
		return true, nil
	}
	if isTerminal == nil || !isTerminal() {
		return false, nil
	}
	fmt.Fprint(a.stdout, prompt+" [y/N]: ")
	answer, err := bufio.NewReader(a.stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}
