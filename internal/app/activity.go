package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	workloadrisk "github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	activityMaxFilterValues = 128
	activityMaxCursorLength = 4096
	activityMaxPathSearch   = 4096
	activityMaxDomainSearch = 253
)

type activityOwnerOptions struct {
	session     string
	environment string
	incarnation string
}

type activityTimeOptions struct {
	fromRaw string
	toRaw   string
	from    time.Time
	to      time.Time
}

type activityCommandScope struct {
	provider manager.ActivityProvider
	owner    workloadtypes.ActivityOwner
	session  string
}

func (a app) activityCommand(args []string) error {
	if len(args) == 0 || containsHelpToken(args) {
		a.activityUsage()
		return nil
	}
	switch args[0] {
	case "summary":
		return a.activitySummary(args[1:])
	case "events":
		return a.activityEvents(args[1:])
	case "executions":
		return a.activityExecutions(args[1:])
	case "coverage":
		return a.activityCoverage(args[1:])
	case "risks":
		return a.activityRisks(args[1:])
	default:
		return fmt.Errorf(
			"unknown activity subcommand %q (expected summary, events, executions, coverage, or risks)",
			args[0],
		)
	}
}

func (a app) activityUsage() {
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout activity summary [OWNER] [--from RFC3339] [--to RFC3339] [--json]")
	fmt.Fprintln(a.stdout, "  hideout activity events [OWNER] [--kind KIND] [--operation OP] [--execution ID] [--risk ID] [--path TEXT] [--domain NAME] [--ip ADDRESS] [--cursor CURSOR] [--limit 1..500] [--json]")
	fmt.Fprintln(a.stdout, "  hideout activity executions [OWNER] [--id EXECUTION] [--roots] [--json]")
	fmt.Fprintln(a.stdout, "  hideout activity coverage [OWNER] [--subsystem NAME] [--from RFC3339] [--to RFC3339] [--json]")
	fmt.Fprintln(a.stdout, "  hideout activity risks [OWNER] [--severity LEVEL] [--rule ID] [--execution ID] [--from RFC3339] [--to RFC3339] [--json]")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "OWNER:")
	fmt.Fprintln(a.stdout, "  --session ID                         select a run (reusable or disposable)")
	fmt.Fprintln(a.stdout, "  --environment ID --incarnation ID   select one exact reusable VM instance")
	fmt.Fprintln(a.stdout, "  omit OWNER                           use the newest active workload")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Repeat filter flags to match any listed value. Queries are read-only and require the running daemon.")
}

func (a app) activitySummary(args []string) error {
	fs := flag.NewFlagSet("activity summary", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var owner activityOwnerOptions
	var window activityTimeOptions
	registerActivityOwnerFlags(fs, &owner)
	registerActivityTimeFlags(fs, &window)
	jsonOut := fs.Bool("json", false, "write canonical JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout activity summary [OWNER] [--from RFC3339] [--to RFC3339] [--json]")
	}
	if err := window.parse(); err != nil {
		return err
	}
	scope, err := a.resolveActivityScope(context.Background(), owner)
	if err != nil {
		return err
	}
	result, err := scope.provider.ActivitySummary(
		context.Background(),
		manager.ActivitySummaryQuery{
			Owner: scope.owner, SessionID: scope.session,
			From: window.from, To: window.to,
		},
	)
	if err != nil {
		return activityCommandError(err)
	}
	if *jsonOut {
		return writeIndentedJSON(a.stdout, result)
	}
	writeActivitySummaryHuman(a.stdout, scope, result)
	return nil
}

func (a app) activityEvents(args []string) error {
	fs := flag.NewFlagSet("activity events", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var owner activityOwnerOptions
	var window activityTimeOptions
	var kinds, operations, executions, risks stringListFlag
	registerActivityOwnerFlags(fs, &owner)
	registerActivityTimeFlags(fs, &window)
	fs.Var(&kinds, "kind", "process, file, connection, dns, or risk; repeatable")
	fs.Var(&operations, "operation", "operation code; repeatable")
	fs.Var(&executions, "execution", "execution ID; repeatable")
	fs.Var(&risks, "risk", "risk or rule ID; repeatable")
	cursor := fs.String("cursor", "", "opaque next cursor")
	limit := fs.Int("limit", manager.DefaultOperatorActivityLimit, "maximum records (1..500)")
	pathSearch := fs.String("path", "", "case-sensitive path substring")
	domain := fs.String("domain", "", "domain substring")
	ip := fs.String("ip", "", "IP literal")
	jsonOut := fs.Bool("json", false, "write canonical JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout activity events [OWNER] [FILTERS] [--json]")
	}
	if err := window.parse(); err != nil {
		return err
	}
	if *limit < 1 || *limit > manager.MaxOperatorActivityLimit {
		return errors.New("--limit must be between 1 and 500")
	}
	normalizedKinds, err := normalizeActivityCLIValues(
		"--kind",
		kinds.Values(),
		validActivityCLIKind,
	)
	if err != nil {
		return err
	}
	normalizedOperations, err := normalizeActivityCLIValues(
		"--operation",
		operations.Values(),
		validActivityCLICode,
	)
	if err != nil {
		return err
	}
	normalizedExecutions, err := normalizeActivityCLIValues(
		"--execution",
		executions.Values(),
		validActivityCLIExecution,
	)
	if err != nil {
		return err
	}
	normalizedRisks, err := normalizeActivityCLIValues(
		"--risk",
		risks.Values(),
		validActivityCLIRisk,
	)
	if err != nil {
		return err
	}
	if !validActivityCLISearch(*cursor, activityMaxCursorLength) {
		return errors.New("--cursor must be at most 4096 bytes and contain no control characters")
	}
	if !validActivityCLISearch(*pathSearch, activityMaxPathSearch) {
		return errors.New("--path must be at most 4096 bytes and contain no control characters")
	}
	if !validActivityCLISearch(*domain, activityMaxDomainSearch) {
		return errors.New("--domain must be at most 253 bytes and contain no control characters")
	}
	canonicalDomain := strings.ToLower(strings.TrimSuffix(*domain, "."))
	if *domain != "" && canonicalDomain == "" {
		return errors.New("--domain must contain a searchable name")
	}
	canonicalIP := ""
	if *ip != "" {
		address, parseErr := netip.ParseAddr(*ip)
		if parseErr != nil || address.Zone() != "" {
			return errors.New("--ip must be an IP literal")
		}
		canonicalIP = address.Unmap().String()
	}
	scope, err := a.resolveActivityScope(context.Background(), owner)
	if err != nil {
		return err
	}
	result, err := scope.provider.ActivityEvents(
		context.Background(),
		manager.ActivityEventsQuery{
			Owner: scope.owner, SessionID: scope.session,
			From: window.from, To: window.to,
			Cursor: *cursor, Limit: *limit,
			Kinds: normalizedKinds, Operations: normalizedOperations,
			Executions: normalizedExecutions, Risks: normalizedRisks,
			Path: *pathSearch, Domain: canonicalDomain, IP: canonicalIP,
		},
	)
	if err != nil {
		return activityCommandError(err)
	}
	if *jsonOut {
		return writeIndentedJSON(a.stdout, result)
	}
	writeActivityEventsHuman(a.stdout, scope, result)
	return nil
}

func (a app) activityExecutions(args []string) error {
	fs := flag.NewFlagSet("activity executions", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var owner activityOwnerOptions
	registerActivityOwnerFlags(fs, &owner)
	id := fs.String("id", "", "select one execution subtree")
	roots := fs.Bool("roots", false, "show top-level session commands")
	jsonOut := fs.Bool("json", false, "write canonical JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || (*id != "" && *roots) {
		return errors.New("usage: hideout activity executions [OWNER] [--id EXECUTION | --roots] [--json]")
	}
	if *id != "" && !validActivityCLIExecution(*id) {
		return errors.New("--id must be an execution ID")
	}
	scope, err := a.resolveActivityScope(context.Background(), owner)
	if err != nil {
		return err
	}
	result, err := scope.provider.ActivityExecutions(
		context.Background(),
		manager.ActivityExecutionsQuery{
			Owner: scope.owner, SessionID: scope.session,
			ID: *id, RootsOnly: *roots,
		},
	)
	if err != nil {
		return activityCommandError(err)
	}
	if *jsonOut {
		return writeIndentedJSON(a.stdout, result)
	}
	writeActivityExecutionsHuman(a.stdout, scope, result)
	return nil
}

func (a app) activityCoverage(args []string) error {
	fs := flag.NewFlagSet("activity coverage", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var owner activityOwnerOptions
	var window activityTimeOptions
	var subsystems stringListFlag
	registerActivityOwnerFlags(fs, &owner)
	registerActivityTimeFlags(fs, &window)
	fs.Var(&subsystems, "subsystem", "process, file, network, or dns; repeatable")
	jsonOut := fs.Bool("json", false, "write canonical JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout activity coverage [OWNER] [--subsystem NAME] [--from RFC3339] [--to RFC3339] [--json]")
	}
	if err := window.parse(); err != nil {
		return err
	}
	normalizedSubsystems, err := normalizeActivityCLIValues(
		"--subsystem",
		subsystems.Values(),
		validActivityCLISubsystem,
	)
	if err != nil {
		return err
	}
	scope, err := a.resolveActivityScope(context.Background(), owner)
	if err != nil {
		return err
	}
	result, err := scope.provider.ActivityCoverage(
		context.Background(),
		manager.ActivityCoverageQuery{
			Owner: scope.owner, SessionID: scope.session,
			From: window.from, To: window.to,
			Subsystems: normalizedSubsystems,
		},
	)
	if err != nil {
		return activityCommandError(err)
	}
	if *jsonOut {
		return writeIndentedJSON(a.stdout, result)
	}
	writeActivityCoverageHuman(a.stdout, scope, result)
	return nil
}

func (a app) activityRisks(args []string) error {
	fs := flag.NewFlagSet("activity risks", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var owner activityOwnerOptions
	var window activityTimeOptions
	var severities, rules, executions stringListFlag
	registerActivityOwnerFlags(fs, &owner)
	registerActivityTimeFlags(fs, &window)
	fs.Var(&severities, "severity", "info, low, medium, high, or critical; repeatable")
	fs.Var(&rules, "rule", "risk rule ID; repeatable")
	fs.Var(&executions, "execution", "execution ID; repeatable")
	jsonOut := fs.Bool("json", false, "write canonical JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: hideout activity risks [OWNER] [FILTERS] [--json]")
	}
	if err := window.parse(); err != nil {
		return err
	}
	normalizedSeverities, err := normalizeActivityCLIValues(
		"--severity",
		severities.Values(),
		validActivityCLISeverity,
	)
	if err != nil {
		return err
	}
	normalizedRules, err := normalizeActivityCLIValues(
		"--rule",
		rules.Values(),
		validActivityCLIRule,
	)
	if err != nil {
		return err
	}
	normalizedExecutions, err := normalizeActivityCLIValues(
		"--execution",
		executions.Values(),
		validActivityCLIExecution,
	)
	if err != nil {
		return err
	}
	scope, err := a.resolveActivityScope(context.Background(), owner)
	if err != nil {
		return err
	}
	result, err := scope.provider.ActivityRisks(
		context.Background(),
		manager.ActivityRisksQuery{
			Owner: scope.owner, SessionID: scope.session,
			From: window.from, To: window.to,
			Severities: normalizedSeverities, Rules: normalizedRules,
			Executions: normalizedExecutions,
		},
	)
	if err != nil {
		return activityCommandError(err)
	}
	if *jsonOut {
		return writeIndentedJSON(a.stdout, result)
	}
	writeActivityRisksHuman(a.stdout, scope, result)
	return nil
}

func registerActivityOwnerFlags(
	fs *flag.FlagSet,
	options *activityOwnerOptions,
) {
	fs.StringVar(&options.session, "session", "", "session ID")
	fs.StringVar(&options.environment, "environment", "", "exact environment ID")
	fs.StringVar(&options.incarnation, "incarnation", "", "exact VM instance ID")
}

func registerActivityTimeFlags(
	fs *flag.FlagSet,
	options *activityTimeOptions,
) {
	fs.StringVar(&options.fromRaw, "from", "", "inclusive RFC3339 start")
	fs.StringVar(&options.toRaw, "to", "", "inclusive RFC3339 end")
}

func (options *activityTimeOptions) parse() error {
	if options == nil {
		return nil
	}
	var err error
	if options.fromRaw != "" {
		options.from, err = time.Parse(time.RFC3339Nano, options.fromRaw)
		if err != nil {
			return errors.New("--from must be RFC3339")
		}
		options.from = options.from.Round(0).UTC()
	}
	if options.toRaw != "" {
		options.to, err = time.Parse(time.RFC3339Nano, options.toRaw)
		if err != nil {
			return errors.New("--to must be RFC3339")
		}
		options.to = options.to.Round(0).UTC()
	}
	if !options.from.IsZero() && !options.to.IsZero() &&
		options.to.Before(options.from) {
		return errors.New("--to must not precede --from")
	}
	return nil
}

func (a app) resolveActivityScope(
	ctx context.Context,
	options activityOwnerOptions,
) (activityCommandScope, error) {
	if options.session != "" &&
		(options.environment != "" || options.incarnation != "") {
		return activityCommandScope{}, errors.New(
			"select either --session or --environment with --incarnation",
		)
	}
	if (options.environment == "") != (options.incarnation == "") {
		return activityCommandScope{}, errors.New(
			"--environment and --incarnation must be provided together",
		)
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return activityCommandScope{}, err
	}
	provider := manager.ActivityProvider(daemon.NewActivityClient(store.Root))
	if a.activityProvider != nil {
		provider = a.activityProvider(store.Root)
	}
	if provider == nil {
		return activityCommandScope{}, errors.New(
			"activity requires a running Hideout daemon",
		)
	}
	if options.environment != "" {
		selector := manager.ActivityOwnerSelector{
			EnvironmentID:        options.environment,
			BackendIncarnationID: options.incarnation,
		}
		if err := selector.Validate(); err != nil {
			return activityCommandScope{}, errors.New(
				"--environment or --incarnation is invalid",
			)
		}
		owner, err := provider.ResolveActivityOwner(ctx, selector)
		if err != nil {
			return activityCommandScope{}, activityCommandError(err)
		}
		return activityCommandScope{provider: provider, owner: owner}, nil
	}
	if options.session != "" {
		query := manager.OperatorSnapshotQuery{
			Session:       options.session,
			ActivityLimit: 1,
		}
		if err := query.Validate(); err != nil {
			return activityCommandScope{}, errors.New("--session is invalid")
		}
		snapshot, err := a.fetchActivitySnapshot(ctx, store.Root, query)
		if err == nil {
			if owner, found, ownerErr := activityOwnerFromSnapshot(
				snapshot,
				options.session,
			); ownerErr != nil {
				return activityCommandScope{}, ownerErr
			} else if found {
				return activityCommandScope{
					provider: provider, owner: owner, session: options.session,
				}, nil
			}
		}
		// Session-only resolution is authoritative for disposable owners and is
		// a useful fallback when a minimal snapshot has no coverage row yet.
		owner, resolveErr := provider.ResolveActivityOwner(
			ctx,
			manager.ActivityOwnerSelector{SessionID: options.session},
		)
		if resolveErr == nil {
			return activityCommandScope{
				provider: provider, owner: owner, session: options.session,
			}, nil
		}
		if err != nil {
			return activityCommandScope{}, fmt.Errorf(
				"activity requires a running Hideout daemon: %w",
				err,
			)
		}
		return activityCommandScope{}, errors.New(
			"no exact activity owner is available for this session; run hideout doctor --feature activity",
		)
	}

	snapshot, err := a.fetchActivitySnapshot(
		ctx,
		store.Root,
		manager.OperatorSnapshotQuery{
			ActivityLimit: 1,
		},
	)
	if err != nil {
		return activityCommandScope{}, fmt.Errorf(
			"activity requires a running Hideout daemon: %w",
			err,
		)
	}
	sessionID := newestActivitySession(snapshot.Sessions)
	if sessionID == "" {
		return activityCommandScope{}, errors.New(
			"no workload session is available; start one with hideout run -- <command>",
		)
	}
	owner, found, err := activityOwnerFromSnapshot(snapshot, sessionID)
	if err != nil {
		return activityCommandScope{}, err
	}
	if !found {
		return activityCommandScope{}, errors.New(
			"the newest workload has no exact activity owner yet; run hideout doctor --feature activity",
		)
	}
	return activityCommandScope{
		provider: provider, owner: owner, session: sessionID,
	}, nil
}

func (a app) fetchActivitySnapshot(
	ctx context.Context,
	storeRoot string,
	query manager.OperatorSnapshotQuery,
) (manager.OperatorSnapshot, error) {
	if a.activitySnapshot != nil {
		return a.activitySnapshot(ctx, storeRoot, query)
	}
	return daemon.FetchOperatorSnapshot(ctx, storeRoot, query)
}

func activityOwnerFromSnapshot(
	snapshot manager.OperatorSnapshot,
	sessionID string,
) (workloadtypes.ActivityOwner, bool, error) {
	var (
		owner workloadtypes.ActivityOwner
		found bool
	)
	observe := func(candidate workloadtypes.ActivityOwner, candidateSession string) error {
		if candidateSession != sessionID {
			return nil
		}
		if candidate.Validate() != nil {
			return errors.New("operator snapshot contains an invalid activity owner")
		}
		if !found {
			owner = candidate
			found = true
			return nil
		}
		if !owner.Equal(candidate) {
			return errors.New(
				"operator snapshot contains conflicting exact owners for one session",
			)
		}
		return nil
	}
	for _, interval := range snapshot.Coverage {
		if err := observe(interval.Owner, interval.SessionID); err != nil {
			return workloadtypes.ActivityOwner{}, false, err
		}
	}
	for _, record := range snapshot.Activity {
		if err := observe(record.Owner, record.SessionID); err != nil {
			return workloadtypes.ActivityOwner{}, false, err
		}
	}
	return owner, found, nil
}

func newestActivitySession(values []manager.OperatorSessionProjection) string {
	if len(values) == 0 {
		return ""
	}
	sessions := append([]manager.OperatorSessionProjection(nil), values...)
	sort.SliceStable(sessions, func(left, right int) bool {
		switch {
		case sessions[left].StartedAt.Equal(sessions[right].StartedAt):
			return sessions[left].ID > sessions[right].ID
		case sessions[left].StartedAt.IsZero():
			return false
		case sessions[right].StartedAt.IsZero():
			return true
		default:
			return sessions[left].StartedAt.After(sessions[right].StartedAt)
		}
	})
	return sessions[0].ID
}

func normalizeActivityCLIValues(
	name string,
	values []string,
	valid func(string) bool,
) ([]string, error) {
	if len(values) > activityMaxFilterValues {
		return nil, fmt.Errorf(
			"%s has too many values (maximum %d)",
			name,
			activityMaxFilterValues,
		)
	}
	result := append([]string(nil), values...)
	sort.Strings(result)
	previous := ""
	for _, value := range result {
		if !valid(value) || value == previous {
			return nil, fmt.Errorf("%s contains an invalid or duplicate value", name)
		}
		previous = value
	}
	return result, nil
}

func validActivityCLIKind(value string) bool {
	switch value {
	case workloadtypes.ActivityProcess, workloadtypes.ActivityFile,
		workloadtypes.ActivityConnection, workloadtypes.ActivityDNS,
		workloadtypes.ActivityRisk:
		return true
	default:
		return false
	}
}

func validActivityCLISubsystem(value string) bool {
	switch value {
	case workloadtypes.SubsystemProcess, workloadtypes.SubsystemFile,
		workloadtypes.SubsystemNetwork, workloadtypes.SubsystemDNS:
		return true
	default:
		return false
	}
}

func validActivityCLISeverity(value string) bool {
	switch value {
	case workloadrisk.SeverityInfo, workloadrisk.SeverityLow,
		workloadrisk.SeverityMedium, workloadrisk.SeverityHigh,
		workloadrisk.SeverityCritical:
		return true
	default:
		return false
	}
}

func validActivityCLIExecution(value string) bool {
	return validActivityCLIPrefixedID(value, "exec_", 8, 124)
}

func validActivityCLIRisk(value string) bool {
	return validActivityCLIPrefixedID(value, "risk_", 8, 124) ||
		validActivityCLIRule(value)
}

func validActivityCLIRule(value string) bool {
	if len(value) < 3 || len(value) > 128 ||
		value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validActivityCLIPrefixedID(
	value, prefix string,
	minimumSuffix, maximumSuffix int,
) bool {
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	suffix := value[len(prefix):]
	if len(suffix) < minimumSuffix || len(suffix) > maximumSuffix {
		return false
	}
	for index := 0; index < len(suffix); index++ {
		character := suffix[index]
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validActivityCLICode(value string) bool {
	if !validActivityCLIText(value, 128) || value == "" ||
		value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validActivityCLIText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum &&
		utf8.ValidString(value) &&
		strings.TrimSpace(value) == value &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

func validActivityCLISearch(value string, maximum int) bool {
	return len(value) <= maximum &&
		utf8.ValidString(value) &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

func activityCommandError(err error) error {
	switch {
	case errors.Is(err, manager.ErrActivityOwnerNotFound):
		return errors.New(
			"the exact activity owner is no longer retained; refresh sessions with hideout activity summary",
		)
	case errors.Is(err, manager.ErrActivityCursorInvalid),
		errors.Is(err, manager.ErrActivityCursorOwnerMismatch),
		errors.Is(err, manager.ErrActivityCursorFilterMismatch):
		return errors.New(
			"the activity cursor does not match this owner/query; remove --cursor and retry",
		)
	case errors.Is(err, manager.ErrActivityCursorStale):
		return errors.New(
			"the activity cursor is stale; remove --cursor and restart the query",
		)
	case errors.Is(err, manager.ErrActivityExecutionNotFound):
		return errors.New(
			"the execution is not retained for this owner; query hideout activity executions again",
		)
	default:
		return err
	}
}

func writeActivitySummaryHuman(
	w io.Writer,
	scope activityCommandScope,
	result manager.ActivitySummaryResult,
) {
	fmt.Fprintln(w, "Activity summary")
	writeActivityScopeHuman(w, scope)
	if len(result.Counts) == 0 {
		fmt.Fprintln(w, "counts: none")
	} else {
		fmt.Fprintln(w, "counts:")
		for _, kind := range sortedActivityCountKinds(result.Counts) {
			fmt.Fprintf(w, "  %s: %d\n", activityHuman(kind), result.Counts[kind])
		}
	}
	fmt.Fprintf(
		w,
		"retained: %s -> %s\n",
		activityTime(result.RetainedRange.From),
		activityTime(result.RetainedRange.To),
	)
	fmt.Fprintf(
		w,
		"quota: used=%d limit=%d\n",
		result.Quota.UsedBytes,
		result.Quota.LimitBytes,
	)
	fmt.Fprintf(w, "state: pruned=%t corrupt=%t\n", result.Pruned, result.Corrupt)
	fmt.Fprintf(w, "reasons: %s\n", activityStringList(result.Reasons))
	fmt.Fprintf(w, "latest-cursor: %s\n", activityDash(result.LatestCursor))
	writeActivityCoverageRows(w, result.CurrentCoverage)
	if len(result.HighestRisks) == 0 {
		fmt.Fprintln(w, "highest risks: none")
		return
	}
	fmt.Fprintln(w, "highest risks:")
	for _, finding := range result.HighestRisks {
		writeActivityRiskHuman(w, finding, "  ")
	}
}

func writeActivityEventsHuman(
	w io.Writer,
	scope activityCommandScope,
	result manager.ActivityEventsPage,
) {
	fmt.Fprintln(w, "Activity events")
	writeActivityScopeHuman(w, scope)
	if len(result.Records) == 0 {
		fmt.Fprintln(w, "events: none")
	} else {
		for _, record := range result.Records {
			fmt.Fprintf(w, "event %s\n", activityHuman(record.ID))
			fmt.Fprintf(
				w,
				"  session: %s\n  time: %s -> %s\n",
				activityHuman(record.SessionID),
				activityTime(record.FirstAt),
				activityTime(record.LastAt),
			)
			fmt.Fprintf(
				w,
				"  kind: %s operation=%s count=%d bytes=%d\n",
				activityHuman(record.Kind),
				activityHuman(record.Operation),
				record.Count,
				record.Bytes,
			)
			if record.Actor != nil {
				fmt.Fprintf(
					w,
					"  actor: %s pid=%d uid=%d gid=%d user=%s group=%s\n",
					activityHuman(record.Actor.ExecutionID),
					record.Actor.PID,
					record.Actor.UID,
					record.Actor.GID,
					activityDash(record.Actor.User),
					activityDash(record.Actor.Group),
				)
			} else {
				fmt.Fprintln(w, "  actor: unknown")
			}
			if record.Mediator != nil {
				fmt.Fprintf(
					w,
					"  mediator: %s id=%s execution=%s attribution=%s reason=%s\n",
					activityHuman(record.Mediator.Kind),
					activityHuman(record.Mediator.ID),
					activityDash(record.Mediator.ExecutionID),
					activityHuman(record.Mediator.Attribution),
					activityDash(record.Mediator.Reason),
				)
			}
			fmt.Fprintf(w, "  subject: %s\n", activityHumanJSON(record.Subject))
			fmt.Fprintf(w, "  outcome: %s\n", activityHumanJSON(record.Outcome))
			fmt.Fprintf(
				w,
				"  attribution: %s coverage=%s sequences=%d..%d\n",
				activityHuman(record.Attribution),
				activityHuman(record.CoverageID),
				record.FirstSequence,
				record.LastSequence,
			)
			fmt.Fprintf(w, "  truncation: %s\n", activityStringList(record.Truncation))
		}
	}
	writeActivityCoverageRows(w, result.Coverage)
	fmt.Fprintf(w, "query-truncated: %t\n", result.QueryTruncated)
	fmt.Fprintf(w, "next-cursor: %s\n", activityDash(result.NextCursor))
}

func writeActivityExecutionsHuman(
	w io.Writer,
	scope activityCommandScope,
	result manager.ActivityExecutionsResult,
) {
	fmt.Fprintln(w, "Activity executions")
	writeActivityScopeHuman(w, scope)
	if len(result.Roots) == 0 {
		fmt.Fprintln(w, "executions: none")
	} else {
		for _, root := range result.Roots {
			writeActivityExecutionHuman(w, root, 0)
		}
	}
	writeActivityCoverageRows(w, result.Coverage)
}

func writeActivityExecutionHuman(
	w io.Writer,
	node manager.ActivityExecutionNode,
	depth int,
) {
	indent := strings.Repeat("  ", depth)
	execution := node.Execution
	fmt.Fprintf(w, "%sexecution %s\n", indent, activityHuman(execution.ID))
	fmt.Fprintf(
		w,
		"%s  session: %s parent=%s parent-unavailable=%t\n",
		indent,
		activityHuman(execution.SessionID),
		activityDash(execution.ParentExecutionID),
		node.ParentUnavailable,
	)
	fmt.Fprintf(
		w,
		"%s  process: pid=%d tid=%d exec-sequence=%d observer-run=%d boot=%s\n",
		indent,
		execution.PID,
		execution.TID,
		execution.ExecSequence,
		execution.ObserverGeneration,
		activityHuman(execution.GuestBootID),
	)
	fmt.Fprintf(
		w,
		"%s  command: %s\n%s  cwd: %s\n",
		indent,
		activityHuman(strings.Join(execution.Argv, " ")),
		indent,
		activityDash(execution.Cwd),
	)
	fmt.Fprintf(
		w,
		"%s  identity: uid=%d gid=%d user=%s group=%s\n",
		indent,
		execution.Identity.UID,
		execution.Identity.GID,
		activityDash(execution.Identity.User),
		activityDash(execution.Identity.Group),
	)
	fmt.Fprintf(
		w,
		"%s  started: %s monotonic-ns=%d\n",
		indent,
		activityTime(execution.StartedAt),
		execution.StartedAtMonoNS,
	)
	if execution.Exit != nil {
		fmt.Fprintf(w, "%s  exit: %s\n", indent, activityHumanJSON(execution.Exit))
	} else {
		fmt.Fprintf(w, "%s  exit: unobserved\n", indent)
	}
	fmt.Fprintf(
		w,
		"%s  limitations: %s\n%s  activity: %s\n",
		indent,
		activityStringList(execution.Limitations),
		indent,
		activityCounts(node.ActivityCounts),
	)
	for _, child := range node.Children {
		writeActivityExecutionHuman(w, child, depth+1)
	}
}

func writeActivityCoverageHuman(
	w io.Writer,
	scope activityCommandScope,
	result manager.ActivityCoverageResult,
) {
	fmt.Fprintln(w, "Activity coverage")
	writeActivityScopeHuman(w, scope)
	writeActivityCoverageRows(w, result.Intervals)
	if len(result.Current) == 0 {
		fmt.Fprintln(w, "current coverage: none")
		return
	}
	fmt.Fprintln(w, "current coverage:")
	writeActivityCoverageRows(w, result.Current)
}

func writeActivityCoverageRows(
	w io.Writer,
	values []workloadtypes.CoverageInterval,
) {
	if len(values) == 0 {
		fmt.Fprintln(w, "coverage: none")
		return
	}
	for _, interval := range values {
		ended := "-"
		if interval.EndedAt != nil {
			ended = activityTime(*interval.EndedAt)
		}
		endSequence := "-"
		if interval.EndSequence != nil {
			endSequence = strconv.FormatUint(*interval.EndSequence, 10)
		}
		fmt.Fprintf(
			w,
			"coverage %s %s reason=%s started=%s ended=%s dropped=%d retention-gap=%t sequences=%d..%s collector-run=%d id=%s session=%s\n",
			activityHuman(interval.Subsystem),
			activityHuman(interval.State),
			activityHuman(interval.Reason),
			activityTime(interval.StartedAt),
			ended,
			interval.DroppedEventCount,
			interval.RetentionGap,
			interval.StartSequence,
			endSequence,
			interval.CollectorGeneration,
			activityHuman(interval.ID),
			activityHuman(interval.SessionID),
		)
		for _, evidence := range interval.Evidence {
			fmt.Fprintf(
				w,
				"  evidence: %s=%s\n",
				activityHuman(evidence.Code),
				activityDash(evidence.Value),
			)
		}
	}
}

func writeActivityRisksHuman(
	w io.Writer,
	scope activityCommandScope,
	result manager.ActivityRisksResult,
) {
	fmt.Fprintln(w, "Activity risks")
	writeActivityScopeHuman(w, scope)
	if len(result.Findings) == 0 {
		fmt.Fprintln(w, "risks: none")
		return
	}
	for _, finding := range result.Findings {
		writeActivityRiskHuman(w, finding, "")
	}
}

func writeActivityRiskHuman(
	w io.Writer,
	finding workloadrisk.Finding,
	indent string,
) {
	fmt.Fprintf(w, "%srisk %s\n", indent, activityHuman(finding.ID))
	fmt.Fprintf(
		w,
		"%s  severity: %s confidence=%s\n",
		indent,
		activityHuman(finding.Severity),
		activityHuman(finding.Confidence),
	)
	fmt.Fprintf(
		w,
		"%s  rule: %s/%s ruleset=%s\n",
		indent,
		activityHuman(finding.RuleID),
		activityHuman(finding.RuleVersion),
		activityHuman(finding.RuleSetVersion),
	)
	fmt.Fprintf(
		w,
		"%s  policy: %s/%s\n",
		indent,
		activityHuman(finding.PolicyStatus),
		activityHuman(finding.PolicyDisposition),
	)
	fmt.Fprintf(
		w,
		"%s  title: %s\n%s  explanation: %s\n",
		indent,
		activityHuman(finding.Title),
		indent,
		activityHuman(finding.Explanation),
	)
	fmt.Fprintf(
		w,
		"%s  session: %s coverage=%s count=%d truncated=%t\n",
		indent,
		activityHuman(finding.SessionID),
		activityHuman(finding.CoverageID),
		finding.Count,
		finding.CountTruncated,
	)
	fmt.Fprintf(
		w,
		"%s  time: %s -> %s\n%s  evidence: %s\n%s  next: %s\n",
		indent,
		activityTime(finding.FirstAt),
		activityTime(finding.LastAt),
		indent,
		activityStringList(finding.EvidenceRefs),
		indent,
		activityHuman(finding.NextAction),
	)
}

func writeActivityScopeHuman(w io.Writer, scope activityCommandScope) {
	if scope.session != "" {
		fmt.Fprintf(w, "scope: session %s\n", activityHuman(scope.session))
	}
	owner := scope.owner
	switch owner.Kind {
	case workloadtypes.OwnerReusableEnvironment:
		fmt.Fprintf(
			w,
			"owner: %s environment=%s backend=%s vm-instance=%s\n",
			activityHuman(owner.Kind),
			activityHuman(owner.EnvironmentID),
			activityHuman(owner.Backend),
			activityHuman(owner.BackendIncarnationID),
		)
	case workloadtypes.OwnerDisposableSession:
		fmt.Fprintf(
			w,
			"owner: %s session=%s backend=%s vm-instance=%s\n",
			activityHuman(owner.Kind),
			activityHuman(owner.SessionID),
			activityHuman(owner.Backend),
			activityHuman(owner.BackendIncarnationID),
		)
	}
}

func sortedActivityCountKinds(values map[string]uint64) []string {
	kinds := make([]string, 0, len(values))
	for kind := range values {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func activityCounts(values map[string]uint64) string {
	if len(values) == 0 {
		return "none"
	}
	fields := make([]string, 0, len(values))
	for _, kind := range sortedActivityCountKinds(values) {
		fields = append(
			fields,
			activityHuman(kind)+"="+strconv.FormatUint(values[kind], 10),
		)
	}
	return strings.Join(fields, ",")
}

func activityStringList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	safe := make([]string, 0, len(values))
	for _, value := range values {
		safe = append(safe, activityHuman(value))
	}
	return strings.Join(safe, ",")
}

func activityDash(value string) string {
	if value == "" {
		return "-"
	}
	return activityHuman(value)
}

func activityTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Round(0).UTC().Format(time.RFC3339Nano)
}

func activityHumanJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "<invalid>"
	}
	return activityHuman(string(data))
}

func activityHuman(value string) string {
	value = audit.RedactString(value)
	if strings.ContainsFunc(value, func(character rune) bool {
		return unicode.IsControl(character) ||
			character == '\u007f' ||
			activityBidiControl(character)
	}) {
		return strconv.QuoteToASCII(value)
	}
	return value
}

func activityBidiControl(character rune) bool {
	switch character {
	case '\u061c', '\u200e', '\u200f',
		'\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
		'\u2066', '\u2067', '\u2068', '\u2069':
		return true
	default:
		return false
	}
}
