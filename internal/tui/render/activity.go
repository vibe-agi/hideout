package render

import (
	"fmt"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/tui/components"
	workloadrisk "github.com/vibe-agi/hideout/internal/workloadobs/risk"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	ActivityTabAll      = "all"
	ActivityTabCommands = "commands"
	ActivityTabFiles    = "files"
	ActivityTabNetwork  = "network"
	ActivityTabDNS      = "dns"
	ActivityTabRisks    = "risks"
)

var activityTabs = []string{
	ActivityTabAll,
	ActivityTabCommands,
	ActivityTabFiles,
	ActivityTabNetwork,
	ActivityTabDNS,
	ActivityTabRisks,
}

// ActivityData is one bounded, exact-owner set of Manager query responses.
// The selected session remains explicit because reusable owners may retain
// records for several runs.
type ActivityData struct {
	Owner      workloadtypes.ActivityOwner
	Summary    manager.ActivitySummaryResult
	Events     manager.ActivityEventsPage
	Executions manager.ActivityExecutionsResult
	Coverage   manager.ActivityCoverageResult
	Risks      manager.ActivityRisksResult
}

type ActivityInput struct {
	State      liveconsole.State
	SessionID  string
	Tab        string
	Filter     string
	Data       ActivityData
	Selected   int
	DetailOpen bool
	Loaded     bool
	Loading    bool
	Error      string
	Now        time.Time
}

type ActivityRow struct {
	ID          string
	Source      string
	At          time.Time
	Kind        string
	Actor       string
	Operation   string
	Subject     string
	Count       uint64
	Attribution string
	Depth       int
}

// Activity renders the investigation HUD. It only consumes already-redacted
// Manager projections and never infers that an empty list proves no activity.
func Activity(input ActivityInput, options Options) string {
	if options.Width <= 0 {
		options.Width = 80
	}
	if options.Height <= 0 {
		options.Height = 24
	}
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	}
	input.Tab = normalizeActivityTab(input.Tab)
	if options.Width < 48 || options.Height < 12 {
		return fitOutput(
			fmt.Sprintf(
				"Hideout · %s\nterminal too small\n? help · q quit\n",
				healthLabel(input.State.StreamHealth),
			),
			options.Width,
		)
	}

	rows := ActivityRows(input)
	selected := clampActivitySelection(input.Selected, len(rows))
	header := activityHeader(input, options)
	body := ""
	switch {
	case input.Error != "" && !input.Loaded:
		body = activityUnavailable(input, options)
	case options.Width < 80:
		body = activityNarrowBody(input, rows, selected, options)
	default:
		body = activityWideBody(input, rows, selected, options)
	}
	output := header + body +
		components.Tabs(options.Unicode, options.Width) + "\n" +
		activityFooter(options.Unicode, options.Width < 80) + "\n"
	output = fitOutput(output, options.Width)
	if !options.NoColor {
		output = "\x1b[36m" + output + "\x1b[0m"
	}
	return output
}

func ActivityRows(input ActivityInput) []ActivityRow {
	tab := normalizeActivityTab(input.Tab)
	var rows []ActivityRow
	switch tab {
	case ActivityTabCommands:
		for _, root := range input.Data.Executions.Roots {
			appendExecutionRows(&rows, root, input.SessionID, 0)
		}
		if len(rows) == 0 {
			for _, record := range activityRecords(input) {
				if record.Kind == workloadtypes.ActivityProcess {
					rows = append(rows, activityRecordRow(record, input.Data.Executions))
				}
			}
		}
	case ActivityTabRisks:
		for _, finding := range input.Data.Risks.Findings {
			if activitySessionMatches(input.SessionID, finding.SessionID) {
				rows = append(rows, activityRiskRow(finding))
			}
		}
		if !input.Loaded && len(rows) == 0 && len(input.State.Overview.Sessions) <= 1 {
			for _, finding := range input.State.Risks {
				rows = append(rows, liveRiskRow(finding))
			}
		}
	default:
		for _, record := range activityRecords(input) {
			if activityRecordInTab(record, tab) {
				rows = append(rows, activityRecordRow(record, input.Data.Executions))
			}
		}
		if tab == ActivityTabAll {
			for _, finding := range input.Data.Risks.Findings {
				if activitySessionMatches(input.SessionID, finding.SessionID) {
					rows = append(rows, activityRiskRow(finding))
				}
			}
		}
	}
	if tab != ActivityTabCommands {
		sort.SliceStable(rows, func(left, right int) bool {
			if rows[left].At.Equal(rows[right].At) {
				if rows[left].Source != rows[right].Source {
					return rows[left].Source < rows[right].Source
				}
				return rows[left].ID < rows[right].ID
			}
			return rows[left].At.After(rows[right].At)
		})
	}
	filter := strings.ToLower(sanitizeInline(input.Filter))
	if filter == "" {
		return rows
	}
	filtered := make([]ActivityRow, 0, len(rows))
	for _, row := range rows {
		haystack := strings.ToLower(strings.Join([]string{
			row.ID, row.Source, row.Kind, row.Actor, row.Operation,
			row.Subject, row.Attribution,
		}, "\x00"))
		if strings.Contains(haystack, filter) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func activityHeader(input ActivityInput, options Options) string {
	sessionID := sanitizeInline(input.SessionID)
	if sessionID == "" {
		sessionID = "no session"
	}
	status := healthLabel(input.State.StreamHealth)
	if input.Loading {
		status += " (refreshing)"
	}
	lines := []string{
		components.JoinFields(
			options.Unicode,
			"Hideout",
			"Activity",
			status,
			"session "+sessionID,
			input.Now.Format("15:04:05"),
		),
		activityTabBar(input.Tab, options.Unicode),
	}
	coverageLines := activityCoverageHUDLines(input)
	context := "coverage unavailable"
	if len(coverageLines) > 0 {
		context = coverageLines[0]
	}
	if filter := sanitizeInline(input.Filter); filter != "" {
		context = components.JoinFields(options.Unicode, context, "filter "+filter)
	}
	if input.Error != "" && input.Loaded {
		context = components.JoinFields(
			options.Unicode,
			context,
			"refresh failed: "+sanitizeInline(input.Error),
		)
	}
	lines = append(lines, context)
	if len(coverageLines) > 1 {
		lines = append(lines, coverageLines[1:]...)
	}
	return strings.Join(lines, "\n") + "\n\n"
}

func activityUnavailable(input ActivityInput, options Options) string {
	reason := sanitizeInline(input.Error)
	if reason == "" {
		reason = "authoritative Manager activity query is unavailable"
	}
	return components.EmptyState(
		"Activity unavailable: "+reason,
		"Next: hideout doctor --feature activity",
	) + "\n\n"
}

func activityWideBody(
	input ActivityInput,
	rows []ActivityRow,
	selected int,
	options Options,
) string {
	leftWidth := options.Width * 58 / 100
	if leftWidth < 43 {
		leftWidth = 43
	}
	rightWidth := options.Width - leftWidth - 3
	bodyHeight := options.Height - 9 - activityCoverageHUDExtraLines(input)
	if bodyHeight < 6 {
		bodyHeight = 6
	}
	left := activityListLines(input, rows, selected, bodyHeight)
	right := activityDetailLines(input, rows, selected, bodyHeight)
	separator := " | "
	if options.Unicode {
		separator = " │ "
	}
	var output strings.Builder
	for index := 0; index < bodyHeight; index++ {
		var leftLine, rightLine string
		if index < len(left) {
			leftLine = left[index]
		}
		if index < len(right) {
			rightLine = right[index]
		}
		leftLine = truncateWidth(leftLine, leftWidth)
		rightLine = truncateWidth(rightLine, rightWidth)
		output.WriteString(padDisplayWidth(leftLine, leftWidth))
		output.WriteString(separator)
		output.WriteString(rightLine)
		output.WriteByte('\n')
	}
	output.WriteByte('\n')
	return output.String()
}

func activityNarrowBody(
	input ActivityInput,
	rows []ActivityRow,
	selected int,
	options Options,
) string {
	height := options.Height - 9 - activityCoverageHUDExtraLines(input)
	if height < 5 {
		height = 5
	}
	lines := activityListLines(input, rows, selected, height)
	if input.DetailOpen {
		lines = activityDetailLines(input, rows, selected, height)
	}
	return strings.Join(lines, "\n") + "\n\n"
}

func activityListLines(
	input ActivityInput,
	rows []ActivityRow,
	selected int,
	limit int,
) []string {
	lines := []string{"Activity rows"}
	if len(rows) == 0 {
		lines = append(lines, activityEmptyLines(input)...)
		return boundedLines(lines, limit)
	}
	start := 0
	available := limit - 1
	if available < 1 {
		available = 1
	}
	if selected >= start+available {
		start = selected - available + 1
	}
	for index := start; index < len(rows) && len(lines) < limit; index++ {
		marker := "  "
		if index == selected {
			marker = "> "
		}
		lines = append(lines, marker+activityRowLine(rows[index]))
	}
	if input.Data.Events.QueryTruncated && len(lines) < limit {
		lines = append(lines, "… more retained records; refine the filter")
	}
	return lines
}

func activityDetailLines(
	input ActivityInput,
	rows []ActivityRow,
	selected int,
	limit int,
) []string {
	lines := []string{"Details"}
	if len(rows) == 0 {
		lines = append(lines, activityEmptyLines(input)...)
		return boundedLines(lines, limit)
	}
	row := rows[selected]
	if !input.DetailOpen {
		lines = append(
			lines,
			strings.ToUpper(sanitizeInline(row.Kind))+" "+sanitizeInline(row.Operation),
			sanitizeInline(row.Subject),
			"actor "+sanitizeInline(row.Actor),
			"attribution "+sanitizeInline(row.Attribution),
			"",
			"Press Enter for ancestry, evidence,",
			"coverage, rule, and next action.",
		)
		return boundedLines(lines, limit)
	}
	switch row.Source {
	case "risk":
		lines = append(lines, activityRiskDetail(input, row)...)
	case "live-risk":
		lines = append(lines, liveRiskDetail(input, row)...)
	case "execution":
		lines = append(lines, activityExecutionDetail(input, row)...)
	default:
		lines = append(lines, activityEventDetail(input, row)...)
	}
	return boundedLines(lines, limit)
}

func activityEmptyLines(input ActivityInput) []string {
	if input.Loading {
		return []string{"Loading authoritative activity…"}
	}
	coverage := selectedActivityCoverage(input)
	for _, interval := range coverage {
		if interval.State != workloadtypes.CoverageAvailable ||
			interval.DroppedEventCount > 0 ||
			interval.RetentionGap {
			return []string{
				"No matching activity is not proof of zero activity.",
				activityCoverageIntervalLine(interval),
			}
		}
	}
	if input.Filter != "" {
		return []string{"No matching activity for this client-local filter."}
	}
	return []string{"No matching activity in the retained query."}
}

func activityEventDetail(input ActivityInput, row ActivityRow) []string {
	record, ok := activityRecordByID(input, row.ID)
	if !ok {
		return []string{"Evidence record is unavailable.", "ID " + sanitizeInline(row.ID)}
	}
	lines := []string{
		strings.ToUpper(sanitizeInline(record.Kind)) + " " + sanitizeInline(record.Operation),
		"subject " + activitySubject(record),
		"outcome " + sanitizeInline(record.Outcome.Status),
		fmt.Sprintf("count %d · bytes %d", record.Count, record.Bytes),
		"time " + activityTimeRange(record.FirstAt, record.LastAt),
		"attribution " + sanitizeInline(record.Attribution),
		"evidence " + sanitizeInline(record.ID),
	}
	if record.Actor != nil {
		lines = append(lines, fmt.Sprintf(
			"actor pid %d · uid %d · %s",
			record.Actor.PID,
			record.Actor.UID,
			sanitizeInline(record.Actor.ExecutionID),
		))
		if execution, found := activityExecutionByID(
			input.Data.Executions.Roots,
			record.Actor.ExecutionID,
		); found {
			lines = append(
				lines,
				"process "+activityExecutionLabel(execution.Execution),
				"ancestry "+activityExecutionAncestry(input.Data.Executions.Roots, execution.Execution),
			)
		}
	} else if record.Mediator != nil {
		lines = append(lines, "mediator "+sanitizeInline(record.Mediator.Kind)+" · "+sanitizeInline(record.Mediator.Attribution))
	} else {
		lines = append(lines, "actor unknown")
	}
	if interval, found := activityCoverageByID(input, record.CoverageID); found {
		lines = append(lines, activityCoverageDetail(interval)...)
	} else {
		lines = append(lines, "coverage unavailable · interval missing")
	}
	for _, finding := range input.Data.Risks.Findings {
		if finding.SessionID != input.SessionID ||
			!containsString(finding.EvidenceRefs, record.ID) {
			continue
		}
		lines = append(
			lines,
			"risk "+strings.ToUpper(sanitizeInline(finding.Severity))+
				" · "+sanitizeInline(finding.RuleID)+"/"+sanitizeInline(finding.RuleVersion),
			"policy "+sanitizeInline(finding.PolicyStatus)+" / "+
				sanitizeInline(finding.PolicyDisposition),
			"next "+sanitizeInline(finding.NextAction),
		)
	}
	return lines
}

func activityRiskDetail(input ActivityInput, row ActivityRow) []string {
	finding, ok := activityRiskByID(input.Data.Risks.Findings, row.ID)
	if !ok {
		return []string{"Risk finding is unavailable.", "ID " + sanitizeInline(row.ID)}
	}
	lines := []string{
		strings.ToUpper(sanitizeInline(finding.Severity)) + " · " + sanitizeInline(finding.Title),
		"rule " + sanitizeInline(finding.RuleID) + "/" + sanitizeInline(finding.RuleVersion),
		"confidence " + sanitizeInline(finding.Confidence),
		"policy " + sanitizeInline(finding.PolicyStatus) + " / " +
			sanitizeInline(finding.PolicyDisposition),
		"count " + strconv.FormatUint(finding.Count, 10) + " · " +
			activityTimeRange(finding.FirstAt, finding.LastAt),
		sanitizeInline(finding.Explanation),
		"evidence:",
	}
	for _, reference := range finding.EvidenceRefs {
		lines = append(lines, "  "+sanitizeInline(reference))
		if record, found := activityRecordByID(input, reference); found {
			lines = append(
				lines,
				"    "+sanitizeInline(record.Operation)+" · "+activitySubject(record),
			)
		}
	}
	lines = append(lines, "next "+sanitizeInline(finding.NextAction))
	if interval, found := activityCoverageByID(input, finding.CoverageID); found {
		lines = append(lines, activityCoverageDetail(interval)...)
	}
	return lines
}

func liveRiskDetail(input ActivityInput, row ActivityRow) []string {
	for _, finding := range input.State.Risks {
		if finding.ID != row.ID {
			continue
		}
		return []string{
			strings.ToUpper(sanitizeInline(finding.Severity)) + " · " + sanitizeInline(finding.Title),
			"rule " + sanitizeInline(finding.RuleID) + "/" + sanitizeInline(finding.RuleVersion),
			"confidence " + sanitizeInline(finding.Confidence),
			"policy " + sanitizeInline(finding.PolicyStatus),
			sanitizeInline(finding.Explanation),
			"next " + sanitizeInline(finding.NextAction),
		}
	}
	return []string{"Risk finding is unavailable."}
}

func activityExecutionDetail(input ActivityInput, row ActivityRow) []string {
	node, ok := activityExecutionByID(input.Data.Executions.Roots, row.ID)
	if !ok {
		return []string{"Execution is unavailable.", "ID " + sanitizeInline(row.ID)}
	}
	execution := node.Execution
	lines := []string{
		"COMMAND " + activityExecutionLabel(execution),
		"execution " + sanitizeInline(execution.ID),
		fmt.Sprintf("pid %d · uid %d · user %s", execution.PID, execution.Identity.UID, sanitizeInline(execution.Identity.User)),
		"started " + execution.StartedAt.UTC().Format(time.RFC3339),
		"cwd " + sanitizeInline(execution.Cwd),
		"argv " + sanitizeArguments(execution.Argv),
		"ancestry " + activityExecutionAncestry(input.Data.Executions.Roots, execution),
	}
	if execution.Exit != nil {
		exit := "exit unknown"
		switch {
		case execution.Exit.Code != nil:
			exit = "exit code " + strconv.Itoa(*execution.Exit.Code)
		case execution.Exit.Signal != 0:
			exit = "exit signal " + strconv.FormatUint(uint64(execution.Exit.Signal), 10)
		case execution.Exit.UnknownReason != "":
			exit = "exit " + sanitizeInline(execution.Exit.UnknownReason)
		}
		lines = append(lines, exit+" · "+execution.Exit.At.UTC().Format(time.RFC3339))
	}
	if len(execution.Limitations) > 0 {
		lines = append(lines, "limitations "+strings.Join(execution.Limitations, ", "))
	}
	for _, kind := range []string{
		workloadtypes.ActivityProcess,
		workloadtypes.ActivityFile,
		workloadtypes.ActivityConnection,
		workloadtypes.ActivityDNS,
	} {
		if count := node.ActivityCounts[kind]; count > 0 {
			lines = append(lines, fmt.Sprintf("%s activity %d", kind, count))
		}
	}
	for _, record := range input.Data.Events.Records {
		if record.SessionID != input.SessionID ||
			record.Actor == nil ||
			record.Actor.ExecutionID != execution.ID {
			continue
		}
		lines = append(lines, "evidence "+sanitizeInline(record.ID)+" · "+activitySubject(record))
	}
	return lines
}

func activityCoverageDetail(interval workloadtypes.CoverageInterval) []string {
	lines := []string{
		"coverage " + sanitizeInline(interval.Subsystem) + " " + sanitizeInline(interval.State),
		"reason " + sanitizeInline(interval.Reason),
		fmt.Sprintf(
			"collector generation %d · sequence %d",
			interval.CollectorGeneration,
			interval.StartSequence,
		),
		fmt.Sprintf("dropped %d · retention gap %t", interval.DroppedEventCount, interval.RetentionGap),
		"interval " + interval.StartedAt.UTC().Format(time.RFC3339),
	}
	if interval.EndSequence != nil {
		lines[2] += fmt.Sprintf(" → %d", *interval.EndSequence)
	}
	if interval.EndedAt != nil {
		lines[len(lines)-1] += " → " + interval.EndedAt.UTC().Format(time.RFC3339)
	}
	for _, evidence := range interval.Evidence {
		value := sanitizeInline(evidence.Code)
		if evidence.Value != "" {
			value += "=" + sanitizeInline(evidence.Value)
		}
		lines = append(lines, "coverage evidence "+value)
	}
	return lines
}

func activityCoverageSummary(input ActivityInput) string {
	coverage := selectedActivityCoverage(input)
	if len(coverage) == 0 {
		return "coverage unavailable"
	}
	current := make(map[string]workloadtypes.CoverageInterval)
	for _, interval := range coverage {
		existing, exists := current[interval.Subsystem]
		if !exists || interval.StartedAt.After(existing.StartedAt) {
			current[interval.Subsystem] = interval
		}
	}
	var fields []string
	for _, subsystem := range []string{
		workloadtypes.SubsystemProcess,
		workloadtypes.SubsystemFile,
		workloadtypes.SubsystemNetwork,
		workloadtypes.SubsystemDNS,
	} {
		interval, exists := current[subsystem]
		if !exists {
			continue
		}
		label := subsystem
		if subsystem == workloadtypes.SubsystemDNS {
			label = "DNS"
		}
		field := label + " " + sanitizeInline(interval.State)
		if interval.State != workloadtypes.CoverageAvailable {
			field += " (" + sanitizeInline(interval.Reason) + ")"
		}
		if interval.RetentionGap {
			field += " retention gap"
		}
		fields = append(fields, field)
	}
	if len(fields) == 0 {
		return "coverage unavailable"
	}
	return "coverage " + strings.Join(fields, " · ")
}

func selectedActivityCoverage(input ActivityInput) []workloadtypes.CoverageInterval {
	values := input.Data.Coverage.Intervals
	if len(values) == 0 {
		values = input.Data.Events.Coverage
	}
	if len(values) == 0 {
		values = input.State.Coverage
	}
	out := make([]workloadtypes.CoverageInterval, 0, len(values))
	for _, interval := range values {
		if activitySessionMatches(input.SessionID, interval.SessionID) {
			out = append(out, interval)
		}
	}
	return out
}

func activityCoverageIntervalLine(interval workloadtypes.CoverageInterval) string {
	label := interval.Subsystem
	if interval.Subsystem == workloadtypes.SubsystemDNS {
		label = "DNS"
	}
	line := "coverage " + label + " " + sanitizeInline(interval.State) +
		" · " + sanitizeInline(interval.Reason)
	if interval.DroppedEventCount > 0 {
		line += " · dropped " + strconv.FormatUint(interval.DroppedEventCount, 10)
	}
	if interval.RetentionGap {
		line += " · retention gap"
	}
	return line
}

func activityRecords(input ActivityInput) []workloadtypes.ActivityRecord {
	values := input.Data.Events.Records
	if !input.Loaded && len(values) == 0 {
		values = input.State.Activity.Recent
	}
	out := make([]workloadtypes.ActivityRecord, 0, len(values))
	for _, record := range values {
		if activitySessionMatches(input.SessionID, record.SessionID) {
			out = append(out, record)
		}
	}
	return out
}

func activityRecordInTab(record workloadtypes.ActivityRecord, tab string) bool {
	switch tab {
	case ActivityTabAll:
		return true
	case ActivityTabFiles:
		return record.Kind == workloadtypes.ActivityFile
	case ActivityTabNetwork:
		return record.Kind == workloadtypes.ActivityConnection
	case ActivityTabDNS:
		return record.Kind == workloadtypes.ActivityDNS
	default:
		return false
	}
}

func activityRecordRow(
	record workloadtypes.ActivityRecord,
	executions manager.ActivityExecutionsResult,
) ActivityRow {
	row := ActivityRow{
		ID: record.ID, Source: "event", At: record.LastAt,
		Kind: record.Kind, Operation: record.Operation,
		Subject: activitySubject(record), Count: record.Count,
		Attribution: record.Attribution, Actor: "unknown",
	}
	if record.Actor != nil {
		row.Actor = record.Actor.ExecutionID
		if node, found := activityExecutionByID(executions.Roots, record.Actor.ExecutionID); found {
			row.Actor = activityExecutionName(node.Execution)
		}
	} else if record.Mediator != nil {
		row.Actor = record.Mediator.Kind
	}
	return row
}

func activityRiskRow(finding workloadrisk.Finding) ActivityRow {
	return ActivityRow{
		ID: finding.ID, Source: "risk", At: finding.LastAt,
		Kind: "risk", Actor: "rule", Operation: strings.ToUpper(finding.Severity),
		Subject: finding.Title, Count: finding.Count, Attribution: finding.Confidence,
	}
}

func liveRiskRow(finding liveconsole.RiskFinding) ActivityRow {
	return ActivityRow{
		ID: finding.ID, Source: "live-risk", At: finding.LastAt,
		Kind: "risk", Actor: "rule", Operation: strings.ToUpper(finding.Severity),
		Subject: finding.Title, Count: finding.Count, Attribution: finding.Confidence,
	}
}

func appendExecutionRows(
	rows *[]ActivityRow,
	node manager.ActivityExecutionNode,
	sessionID string,
	depth int,
) {
	if !activitySessionMatches(sessionID, node.Execution.SessionID) {
		return
	}
	count := uint64(0)
	for _, value := range node.ActivityCounts {
		count += value
	}
	*rows = append(*rows, ActivityRow{
		ID: node.Execution.ID, Source: "execution", At: node.Execution.StartedAt,
		Kind: "command", Actor: activityExecutionName(node.Execution),
		Operation: "exec", Subject: sanitizeArguments(node.Execution.Argv),
		Count: count, Attribution: workloadtypes.AttributionExact, Depth: depth,
	})
	for _, child := range node.Children {
		appendExecutionRows(rows, child, sessionID, depth+1)
	}
}

func activityRowLine(row ActivityRow) string {
	indent := strings.Repeat("  ", row.Depth)
	count := ""
	if row.Count > 1 {
		count = " x" + strconv.FormatUint(row.Count, 10)
	}
	return components.JoinFields(
		false,
		row.At.UTC().Format("15:04:05"),
		indent+sanitizeInline(row.Kind)+" "+sanitizeInline(row.Operation),
		sanitizeInline(row.Actor),
		sanitizeInline(row.Subject)+count,
		sanitizeInline(row.Attribution),
	)
}

func activitySubject(record workloadtypes.ActivityRecord) string {
	switch subject := record.Subject.(type) {
	case workloadtypes.ProcessSubject:
		return activityCommand(subject.Executable, subject.Argv)
	case workloadtypes.FileSubject:
		value := sanitizeInline(subject.Path)
		if value == "" {
			value = "<" + sanitizeInline(subject.PathState) + " path>"
		}
		if subject.TargetPath != "" {
			value += " → " + sanitizeInline(subject.TargetPath)
		}
		return value
	case workloadtypes.NetworkSubject:
		endpoint := net.JoinHostPort(subject.IP, strconv.Itoa(int(subject.Port)))
		if subject.Domain != "" {
			endpoint = sanitizeInline(subject.Domain) + " → " + endpoint
		}
		return endpoint + " " + sanitizeInline(subject.Protocol) + " " + sanitizeInline(subject.Route)
	case workloadtypes.DNSSubject:
		value := sanitizeInline(subject.Query) + " " + sanitizeInline(subject.QueryType)
		if len(subject.Answers) > 0 {
			value += " → " + sanitizeInline(strings.Join(subject.Answers, ","))
		}
		return value
	case workloadtypes.GenericSubject:
		if subject.Summary != "" {
			return sanitizeInline(subject.Summary)
		}
		return sanitizeInline(subject.Code)
	default:
		return "<unsupported subject>"
	}
}

func activityExecutionName(execution workloadtypes.Execution) string {
	name := path.Base(sanitizeInline(execution.Executable))
	if name == "." || name == "/" || name == "" {
		return sanitizeInline(execution.Executable)
	}
	return name
}

func activityExecutionLabel(execution workloadtypes.Execution) string {
	return activityCommand(execution.Executable, execution.Argv)
}

func activityCommand(executable string, arguments []string) string {
	if len(arguments) > 0 {
		return sanitizeArguments(arguments)
	}
	return sanitizeInline(executable)
}

func sanitizeArguments(arguments []string) string {
	values := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		values = append(values, sanitizeInline(argument))
	}
	return strings.Join(values, " ")
}

func activityExecutionByID(
	roots []manager.ActivityExecutionNode,
	id string,
) (manager.ActivityExecutionNode, bool) {
	for _, root := range roots {
		if root.Execution.ID == id {
			return root, true
		}
		if found, ok := activityExecutionByID(root.Children, id); ok {
			return found, true
		}
	}
	return manager.ActivityExecutionNode{}, false
}

func activityExecutionAncestry(
	roots []manager.ActivityExecutionNode,
	execution workloadtypes.Execution,
) string {
	labels := []string{activityExecutionName(execution)}
	parentID := execution.ParentExecutionID
	seen := map[string]struct{}{execution.ID: {}}
	for parentID != "" {
		if _, exists := seen[parentID]; exists {
			labels = append(labels, "<cycle>")
			break
		}
		seen[parentID] = struct{}{}
		parent, ok := activityExecutionByID(roots, parentID)
		if !ok {
			labels = append(labels, "<parent unavailable>")
			break
		}
		labels = append(labels, activityExecutionName(parent.Execution))
		parentID = parent.Execution.ParentExecutionID
	}
	for left, right := 0, len(labels)-1; left < right; left, right = left+1, right-1 {
		labels[left], labels[right] = labels[right], labels[left]
	}
	return strings.Join(labels, " → ")
}

func activityRecordByID(
	input ActivityInput,
	id string,
) (workloadtypes.ActivityRecord, bool) {
	for _, record := range input.Data.Events.Records {
		if record.ID == id && activitySessionMatches(input.SessionID, record.SessionID) {
			return record, true
		}
	}
	for _, record := range input.State.Activity.Recent {
		if record.ID == id && activitySessionMatches(input.SessionID, record.SessionID) {
			return record, true
		}
	}
	return workloadtypes.ActivityRecord{}, false
}

func activityRiskByID(
	findings []workloadrisk.Finding,
	id string,
) (workloadrisk.Finding, bool) {
	for _, finding := range findings {
		if finding.ID == id {
			return finding, true
		}
	}
	return workloadrisk.Finding{}, false
}

func activityCoverageByID(
	input ActivityInput,
	id string,
) (workloadtypes.CoverageInterval, bool) {
	for _, interval := range selectedActivityCoverage(input) {
		if interval.ID == id {
			return interval, true
		}
	}
	return workloadtypes.CoverageInterval{}, false
}

func activityTimeRange(first, last time.Time) string {
	if first.Equal(last) {
		return first.UTC().Format(time.RFC3339)
	}
	return first.UTC().Format(time.RFC3339) + " → " + last.UTC().Format(time.RFC3339)
}

func activityTabBar(active string, unicode bool) string {
	active = normalizeActivityTab(active)
	labels := map[string]string{
		ActivityTabAll: "All", ActivityTabCommands: "Commands",
		ActivityTabFiles: "Files", ActivityTabNetwork: "Network",
		ActivityTabDNS: "DNS", ActivityTabRisks: "Risks",
	}
	fields := make([]string, 0, len(activityTabs))
	for _, tab := range activityTabs {
		label := labels[tab]
		if tab == active {
			label = "[" + label + "]"
		}
		fields = append(fields, label)
	}
	return "Activity / " + components.JoinFields(unicode, fields...)
}

func activityFooter(unicode, compact bool) string {
	separator := " | "
	if unicode {
		separator = " · "
	}
	keys := []string{"←/→ tab", "j/k select", "Enter evidence", "/ filter"}
	if !compact {
		keys = append(keys, "r refresh")
	}
	keys = append(keys, "? keys", "q quit")
	return strings.Join(keys, separator)
}

func normalizeActivityTab(tab string) string {
	for _, candidate := range activityTabs {
		if tab == candidate {
			return candidate
		}
	}
	return ActivityTabAll
}

func activitySessionMatches(selected, actual string) bool {
	return selected == "" || selected == actual
}

func clampActivitySelection(selected, count int) int {
	if count == 0 || selected < 0 {
		return 0
	}
	if selected >= count {
		return count - 1
	}
	return selected
}

func boundedLines(lines []string, limit int) []string {
	if limit <= 0 || len(lines) <= limit {
		return lines
	}
	return lines[:limit]
}

func padDisplayWidth(value string, width int) string {
	missing := width - DisplayWidth(value)
	if missing <= 0 {
		return value
	}
	return value + strings.Repeat(" ", missing)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type activityPreview struct {
	kind        string
	operation   string
	count       uint64
	attribution string
	coverage    string
	reason      string
}

func primaryActivity(state liveconsole.State) activityPreview {
	preview := activityPreview{operation: "observed", attribution: "exact"}
	if len(state.Activity.Counts) > 0 {
		count := state.Activity.Counts[0]
		preview.kind = sanitizeInline(count.Kind)
		preview.count = count.Count
		switch count.Kind {
		case workloadtypes.ActivityFile:
			preview.operation = "write"
		case workloadtypes.ActivityConnection:
			preview.operation = "connect"
		case workloadtypes.ActivityDNS:
			preview.operation = "query"
		case workloadtypes.ActivityProcess:
			preview.operation = "exec"
		}
	}
	for _, interval := range state.Coverage {
		if interval.Subsystem == preview.kind {
			preview.coverage = sanitizeInline(interval.State)
			preview.reason = sanitizeInline(interval.Reason)
			break
		}
	}
	if len(state.Risks) > 0 && state.Risks[0].Confidence != "" {
		preview.attribution = sanitizeInline(state.Risks[0].Confidence)
	}
	return preview
}

func activityLine(preview activityPreview, separator string) string {
	if preview.kind == "" || preview.count == 0 {
		return "no activity yet"
	}
	return strings.Join([]string{
		preview.kind + " " + preview.operation,
		fmt.Sprintf("%d events", preview.count),
		preview.attribution + " attribution",
	}, separator)
}

func compactActivityLine(preview activityPreview, separator string) string {
	if preview.kind == "" || preview.count == 0 {
		return "no activity yet"
	}
	return strings.Join([]string{
		preview.kind + " " + preview.operation,
		fmt.Sprintf("%d events", preview.count),
	}, separator)
}

func activityCoverageLine(preview activityPreview) string {
	if preview.kind == "" || preview.count == 0 {
		return "coverage unavailable (no activity observed)"
	}
	coverage := preview.coverage
	if coverage == "" {
		coverage = "unavailable"
	}
	reason := preview.reason
	if reason == "" {
		reason = "no coverage interval"
	}
	return fmt.Sprintf(
		"coverage %s %s (%s)",
		preview.kind,
		coverage,
		reason,
	)
}
