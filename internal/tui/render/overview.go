package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/tui/components"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

type Options struct {
	Width   int
	Height  int
	Unicode bool
	NoColor bool
}

type OverviewInput struct {
	State liveconsole.State
	Now   time.Time
}

func Overview(input OverviewInput, options Options) string {
	if options.Width <= 0 {
		options.Width = 80
	}
	if options.Height <= 0 {
		options.Height = 24
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var output string
	switch {
	case options.Width < 48 || options.Height < 12:
		output = tooSmallOverview(input.State, options)
	case options.Width < 72:
		output = narrowOverview(input.State, options)
	case options.Unicode:
		output = unicodeOverview(input.State, now, options)
	default:
		output = plainOverview(input.State, now, options)
	}
	if options.Width >= 48 {
		output = insertOverviewOperatorSummary(output, input.State, options)
	}
	output = fitOutput(output, options.Width)
	if !options.NoColor {
		output = "\x1b[36m" + output + "\x1b[0m"
	}
	return output
}

func insertOverviewOperatorSummary(
	output string,
	state liveconsole.State,
	options Options,
) string {
	markers := []string{
		components.Tabs(options.Unicode, options.Width),
		components.Tabs(false, options.Width),
		components.Tabs(true, options.Width),
	}
	position := -1
	for _, marker := range markers {
		if index := strings.LastIndex(output, marker); index > position {
			position = index
		}
	}
	if position < 0 {
		return output
	}
	summary := overviewOperatorSummary(state)
	lineStart := strings.LastIndex(output[:position], "\n") + 1
	prefix := output[lineStart:position]
	separator := "\n\n"
	if prefix == "├ " {
		lines := strings.Split(summary, "\n")
		for index := range lines {
			if index == 0 {
				lines[index] = "├ " + lines[index]
			} else {
				lines[index] = "│ " + lines[index]
			}
		}
		summary = strings.Join(lines, "\n")
		separator = "\n"
	}
	return output[:lineStart] + summary + separator + output[lineStart:]
}

func overviewOperatorSummary(state liveconsole.State) string {
	actionRequired := liveconsole.ActionRequired(state)
	actionableDecisions := make([]liveconsole.DecisionRow, 0, len(state.Decisions))
	hostFSWrites := 0
	for _, row := range state.Decisions {
		if !overviewActionableStatus(row.Status) {
			continue
		}
		actionableDecisions = append(actionableDecisions, row)
		if row.Kind == decision.KindHostFSWrite {
			hostFSWrites++
		}
	}
	visibleNotices := make([]liveconsole.NoticeRow, 0, len(state.Notices))
	backgroundNotices := 0
	for _, row := range state.Notices {
		if row.Acknowledged {
			continue
		}
		visibleNotices = append(visibleNotices, row)
		if row.Kind == decision.KindBackgroundStatus {
			backgroundNotices++
		}
	}
	activeOperations := 0
	for _, operation := range state.Operations {
		switch operation.Phase {
		case "succeeded", "failed", "cancelled", "rolled-back",
			"rollback-unproved", "recovery-required":
		default:
			activeOperations++
		}
	}
	lines := []string{
		"Action Required",
		fmt.Sprintf(
			"total=%d decisions=%d notices=%d hostfs-writes=%d",
			actionRequired.Total,
			actionRequired.Decisions,
			actionRequired.Notices,
			hostFSWrites,
		),
	}
	if actionRequired.Total == 0 {
		lines = append(lines, "none")
	}
	for index, row := range actionableDecisions {
		if index >= 3 {
			lines = append(lines, fmt.Sprintf(
				"… %d more decision(s)",
				len(actionableDecisions)-index,
			))
			break
		}
		line := fmt.Sprintf(
			"Decision %s kind=%s status=%s",
			overviewDisplayValue(row.ID, "unknown"),
			overviewDisplayValue(row.Kind, "unknown"),
			overviewDisplayValue(row.Status, "unknown"),
		)
		if summary := sanitizeInline(row.Summary); summary != "" {
			line += " " + summary
		}
		lines = append(lines, line)
	}
	for index, row := range visibleNotices {
		if index >= 3 {
			lines = append(lines, fmt.Sprintf(
				"… %d more notice(s)",
				len(visibleNotices)-index,
			))
			break
		}
		line := fmt.Sprintf(
			"Notice %s kind=%s severity=%s status=%s",
			overviewDisplayValue(row.ID, "unknown"),
			overviewDisplayValue(row.Kind, "unknown"),
			overviewDisplayValue(row.Severity, "info"),
			overviewDisplayValue(row.Status, "unknown"),
		)
		if summary := sanitizeInline(row.Summary); summary != "" {
			line += " " + summary
		}
		lines = append(lines, line)
	}
	lines = append(lines,
		fmt.Sprintf("HostFS Writes pending=%d", hostFSWrites),
		fmt.Sprintf(
			"Background active=%d status-notices=%d",
			activeOperations,
			backgroundNotices,
		),
		"Doctor run explicitly: hideout doctor --level light",
		"Package/Support: hideout package verify <install-prefix> | hideout support matrix",
		fmt.Sprintf("Audit retained-records=%d", len(state.Activity.Recent)),
	)
	return strings.Join(lines, "\n")
}

func overviewActionableStatus(status string) bool {
	switch status {
	case "", "pending", "claimed", "ready", "requires-decision":
		return true
	default:
		return false
	}
}

func plainOverview(state liveconsole.State, now time.Time, options Options) string {
	facts := overviewFacts(state)
	if facts.stale {
		return fmt.Sprintf(
			"Hideout | %s | %s | read-only | %s\n"+
				"REASON %s\n"+
				"COMMAND %s\n"+
				"CONNECTION %s\n"+
				"STATE %s | mutations disabled\n"+
				"COVERAGE %s\n"+
				"RISK %s\n"+
				"NEXT %s\n\n"+
				"Activity\nlast verified data retained\n\n"+
				"Details\nrefresh before planning or applying changes\n\n"+
				"%s\n%s\n",
			facts.profile, facts.health, now.Format("15:04:05"), facts.reason,
			facts.command, facts.connection, facts.state, facts.coverage,
			facts.risk, facts.next,
			components.Tabs(false, options.Width), components.Footer(false, false),
		)
	}
	if facts.idle {
		return fmt.Sprintf(
			"Hideout | %s | %s | no active session | %s\n"+
				"COMMAND none\n"+
				"CONNECTION %s\n"+
				"STATE idle\n"+
				"COVERAGE unavailable | no active workload\n"+
				"RISK none\n"+
				"NEXT %s\n\n"+
				"Activity\nno activity yet\n\n"+
				"Details\nstart with: hideout run -- <command>\n\n"+
				"%s\n%s\n",
			facts.profile, facts.healthAndAccess(), now.Format("15:04:05"), facts.connection,
			facts.next, components.Tabs(false, options.Width), components.Footer(false, false),
		)
	}
	resources := overviewResourceSummary(state, false)
	return fmt.Sprintf(
		"Hideout | %s | %s | %s | %s\n"+
			"COMMAND %s\n"+
			"CONNECTION %s\n"+
			"STATE %s\n"+
			"COVERAGE %s\n"+
			"RISK %s\n"+
			"NEXT %s\n\n"+
			"Activity\n%s\n\n"+
			"Details\nsession %s | profile %s\n"+
			"%s\n\n"+
			"%s"+
			"%s\n%s\n",
		facts.profile, facts.healthAndAccess(), facts.session, now.Format("15:04:05"),
		facts.command, facts.connection, facts.state, facts.coverage,
		facts.risk, facts.next, facts.activity,
		facts.session, facts.profile, facts.activityCoverageLine, resources,
		components.Tabs(false, options.Width), components.Footer(false, false),
	)
}

func unicodeOverview(state liveconsole.State, now time.Time, options Options) string {
	facts := overviewFacts(state)
	if facts.stale || facts.idle {
		return plainOverview(state, now, Options{Width: options.Width, Height: options.Height})
	}
	resources := overviewResourceSummary(state, true)
	return fmt.Sprintf(
		"┌ Hideout · %s · %s · %s · %s\n"+
			"│ COMMAND %s\n"+
			"│ CONNECTION %s\n"+
			"│ STATE %s\n"+
			"│ COVERAGE %s\n"+
			"│ RISK %s\n"+
			"│ NEXT %s\n"+
			"├ Activity · Details\n"+
			"│ %s\n"+
			"│ session %s · profile %s\n"+
			"│ %s\n"+
			"%s"+
			"├ %s\n"+
			"│ %s\n"+
			"└\n",
		facts.profile, strings.ReplaceAll(facts.healthAndAccess(), " | ", " · "),
		facts.session, now.Format("15:04:05"),
		facts.command, strings.ReplaceAll(facts.connection, " | ", " · "),
		facts.state, strings.ReplaceAll(facts.coverage, " | ", " · "),
		strings.ReplaceAll(facts.risk, " | ", " · "), facts.next,
		strings.ReplaceAll(facts.activity, " | ", " · "),
		facts.session, facts.profile, facts.activityCoverageLine, resources,
		components.Tabs(true, options.Width),
		components.Footer(true, false),
	)
}

func narrowOverview(state liveconsole.State, options Options) string {
	facts := overviewFacts(state)
	if facts.idle || facts.stale {
		return plainOverview(state, time.Now().UTC(), Options{Width: options.Width, Height: options.Height})
	}
	available := 0
	var reduced []string
	for _, interval := range currentCoverageBySubsystem(state.Coverage) {
		if interval.State == workloadtypes.CoverageAvailable {
			available++
			continue
		}
		reduced = append(reduced, sanitizeInline(interval.Subsystem)+" "+sanitizeInline(interval.State))
	}
	coverage := fmt.Sprintf("coverage %d available", available)
	if len(reduced) > 0 {
		coverage += " · " + strings.Join(reduced, " · ")
	}
	return fmt.Sprintf(
		"Hideout · %s · %s\n"+
			"%s · %s · %s\n"+
			"%s\n"+
			"%s\n"+
			"risk %s\n"+
			"next %s\n\n"+
			"Activity (Enter for details)\n"+
			"> %s\n\n"+
			"%s\n%s\n",
		facts.profile, strings.ReplaceAll(facts.healthAndAccess(), " | ", " · "),
		facts.session, facts.command, facts.state,
		strings.ReplaceAll(facts.connection, " | ", " · "), coverage,
		strings.ReplaceAll(facts.risk, " | ", " · "), facts.next,
		facts.activityCompact,
		components.Tabs(true, options.Width), components.Footer(true, true),
	)
}

func tooSmallOverview(state liveconsole.State, _ Options) string {
	facts := overviewFacts(state)
	return fmt.Sprintf(
		"Hideout · %s\nterminal too small\n? help · q quit\n",
		strings.ReplaceAll(facts.healthAndAccess(), " | ", " · "),
	)
}

const (
	maxOverviewEnvironments  = 2
	maxOverviewWorkspaceRows = 3
)

func overviewResourceSummary(state liveconsole.State, unicode bool) string {
	environments := state.Overview.Environments
	workspaceRows := make([]manager.SessionSummary, 0, len(state.Overview.Sessions))
	for _, session := range state.Overview.Sessions {
		if session.WorkspaceID != "" || session.WorkspaceViewState != "" {
			workspaceRows = append(workspaceRows, session)
		}
	}
	if len(environments) == 0 && len(workspaceRows) == 0 {
		return ""
	}
	headingPrefix, rowPrefix := "", ""
	if unicode {
		headingPrefix, rowPrefix = "├ ", "│ "
	}
	lines := make([]string, 0, 12)
	if len(environments) != 0 {
		lines = append(lines, headingPrefix+"Environment")
		limit := min(len(environments), maxOverviewEnvironments)
		for _, environment := range environments[:limit] {
			name := overviewDisplayValue(environment.Name, environment.ID, "unnamed")
			status := overviewDisplayValue(environment.Status, "unknown")
			lines = append(lines, fmt.Sprintf(
				"%smachine %s status=%s sessions=%d active-views=%d",
				rowPrefix, name, status,
				environment.ActiveSessions, environment.ActiveWorkspaceViews,
			))
			if machineID := sanitizeInline(environment.MachineIdentityID); machineID != "" {
				detail := rowPrefix + "machine-id=" + machineID
				if provider := sanitizeInline(environment.WorkspaceProviderState); provider != "" {
					detail += " provider=" + provider
				}
				if owner := sanitizeInline(environment.OwnerHealth); owner != "" {
					detail += " owner=" + owner
				}
				lines = append(lines, detail)
			}
		}
		if remaining := len(environments) - limit; remaining > 0 {
			lines = append(lines, fmt.Sprintf("%s… %d more environment(s)", rowPrefix, remaining))
		}
	}
	if len(workspaceRows) != 0 {
		lines = append(lines, headingPrefix+"Workspace views")
		limit := min(len(workspaceRows), maxOverviewWorkspaceRows)
		for _, session := range workspaceRows[:limit] {
			viewState := overviewDisplayValue(string(session.WorkspaceViewState), "unknown")
			label := overviewDisplayValue(session.WorkspaceLabel, session.ID, "unnamed")
			workspaceID := overviewDisplayValue(session.WorkspaceID, "unknown")
			lines = append(lines, fmt.Sprintf(
				"%sworkspace-view=%s %s id=%s",
				rowPrefix, viewState, label, workspaceID,
			))
			detail := rowPrefix
			if guest := sanitizeInline(session.GuestWorkspace); guest != "" {
				detail += "guest=" + guest
			}
			if transport := sanitizeInline(session.WorkspaceTransport); transport != "" {
				if detail != rowPrefix {
					detail += " "
				}
				detail += "transport=" + transport
			}
			if len(session.WorkspaceRelations) != 0 {
				relation := session.WorkspaceRelations[0]
				if detail != rowPrefix {
					detail += " "
				}
				detail += "relation=" + sanitizeInline(string(relation.Relation)) +
					" position=" + sanitizeInline(relation.SelectedPosition) +
					" other=" + sanitizeInline(relation.OtherWorkspaceID)
			}
			if detail != rowPrefix {
				lines = append(lines, detail)
			}
		}
		if remaining := len(workspaceRows) - limit; remaining > 0 {
			lines = append(lines, fmt.Sprintf("%s… %d more workspace view(s)", rowPrefix, remaining))
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func overviewDisplayValue(values ...string) string {
	for _, value := range values {
		if cleaned := sanitizeInline(value); cleaned != "" {
			return cleaned
		}
	}
	return "unknown"
}

type facts struct {
	profile              string
	session              string
	command              string
	state                string
	health               string
	reason               string
	connection           string
	coverage             string
	risk                 string
	next                 string
	activity             string
	activityCompact      string
	activityCoverageLine string
	readOnly             bool
	idle                 bool
	stale                bool
}

func (value facts) healthAndAccess() string {
	if value.readOnly {
		return value.health + " | read-only"
	}
	return value.health
}

func overviewFacts(state liveconsole.State) facts {
	out := facts{
		profile: "default", command: "unknown", state: "unknown",
		health: healthLabel(state.StreamHealth), connection: "unknown",
		coverage: "unavailable", risk: "none", next: "open help",
	}
	if state.ProfileScope != "" {
		out.profile = sanitizeInline(state.ProfileScope)
	}
	if len(state.Profiles) > 0 {
		out.profile = sanitizeInline(state.Profiles[0].Profile)
		if out.profile == "" {
			out.profile = sanitizeInline(state.Profiles[0].Desired.Name)
		}
	}
	if len(state.Overview.Sessions) > 0 {
		session := state.Overview.Sessions[0]
		out.session = sanitizeInline(session.ID)
		if command := sanitizeInline(session.CommandClass); command != "" {
			out.command = command
		}
		if ownerState := sanitizeInline(string(session.State)); ownerState != "" {
			out.state = ownerState
		}
		if session.Profile != "" {
			out.profile = sanitizeInline(session.Profile)
		}
	}
	out.readOnly = healthReadOnly(state)
	out.idle = len(state.Overview.Sessions) == 0 ||
		state.StreamHealth.State == liveconsole.HealthIdleLive
	out.stale = state.StreamHealth.State == liveconsole.HealthStale ||
		state.StreamHealth.State == liveconsole.HealthDisconnected ||
		state.StreamHealth.State == liveconsole.HealthCredentialExpired ||
		state.StreamHealth.State == liveconsole.HealthSchemaMismatch
	out.reason = sanitizeInline(state.StreamHealth.Reason)
	if out.reason == "" {
		out.reason = "verified state stream unavailable"
	}
	out.connection = connectionSummary(state, out.profile)
	out.coverage = coverageSummary(state.Coverage)
	if len(state.Risks) > 0 {
		out.risk = strings.ToUpper(sanitizeInline(state.Risks[0].Severity)) +
			" | " + sanitizeInline(state.Risks[0].Title)
	}
	if len(state.NextActions) > 0 {
		out.next = sanitizeInline(state.NextActions[0].Label)
		if out.next == "" {
			out.next = sanitizeInline(state.NextActions[0].ID)
		}
	}
	activity := primaryActivity(state)
	out.activity = activityLine(activity, " | ")
	out.activityCompact = compactActivityLine(activity, " · ")
	out.activityCoverageLine = activityCoverageLine(activity)
	return out
}

func connectionSummary(state liveconsole.State, profileName string) string {
	for _, projection := range state.Profiles {
		if profileName != "" && projection.Profile != profileName {
			continue
		}
		if projection.Effective.Network != nil {
			mode := sanitizeInline(projection.Effective.Network.Mode)
			if projection.Effective.Network.SecretGeneration != 0 {
				mode += fmt.Sprintf(
					" | secret version %d",
					projection.Effective.Network.SecretGeneration,
				)
			}
			dns := sanitizeInline(projection.Effective.Network.DNS)
			if dns != "" {
				return mode + " | DNS " + dns
			}
			if mode != "" {
				return mode
			}
		}
		status := sanitizeInline(projection.Effective.Status)
		if status == "" ||
			status == manager.EffectiveNotObserved {
			status = "not observed"
		} else {
			status = strings.ReplaceAll(status, "-", " ")
		}
		mode := sanitizeInline(projection.Desired.Network.Mode)
		if mode == profile.NetworkModeTun2Socks {
			mode = "proxy"
		}
		if mode != "" {
			return status + " | desired " + mode
		}
		return status
	}
	return "unknown"
}

func coverageSummary(values []workloadtypes.CoverageInterval) string {
	current := currentCoverageBySubsystem(values)
	if len(current) == 0 {
		return "unavailable"
	}
	order := []string{
		workloadtypes.SubsystemProcess, workloadtypes.SubsystemFile,
		workloadtypes.SubsystemNetwork, workloadtypes.SubsystemDNS,
	}
	bySubsystem := make(map[string]workloadtypes.CoverageInterval, len(current))
	for _, interval := range current {
		bySubsystem[interval.Subsystem] = interval
	}
	var fields []string
	for _, subsystem := range order {
		if interval, ok := bySubsystem[subsystem]; ok {
			fields = append(fields, subsystem+" "+sanitizeInline(interval.State))
		}
	}
	return strings.Join(fields, " | ")
}

func currentCoverageBySubsystem(
	values []workloadtypes.CoverageInterval,
) []workloadtypes.CoverageInterval {
	bySubsystem := make(map[string]workloadtypes.CoverageInterval)
	for _, interval := range values {
		current, exists := bySubsystem[interval.Subsystem]
		if !exists || interval.StartedAt.After(current.StartedAt) {
			bySubsystem[interval.Subsystem] = interval
		}
	}
	out := make([]workloadtypes.CoverageInterval, 0, len(bySubsystem))
	for _, subsystem := range []string{
		workloadtypes.SubsystemProcess, workloadtypes.SubsystemFile,
		workloadtypes.SubsystemNetwork, workloadtypes.SubsystemDNS,
	} {
		if interval, ok := bySubsystem[subsystem]; ok {
			out = append(out, interval)
		}
	}
	return out
}
