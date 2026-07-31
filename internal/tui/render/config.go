package render

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/tui/components"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

type ConfigInput struct {
	State    liveconsole.State
	Selected int
}

type ConfigRow struct {
	CapabilityID string
	EditorID     string
	Label        string
	Desired      string
	Effective    string
	Transition   string
	Scope        string
	Editable     bool
	Reason       string
}

type configEditorDefinition struct {
	capabilityID string
	editorID     string
	label        string
	scope        string
}

var configEditorDefinitions = []configEditorDefinition{
	{
		capabilityID: manager.CapabilityConfigNetworkPosture,
		editorID:     manager.ChangeNetworkPosture,
		label:        "Connection mode",
		scope:        "live · new connections",
	},
	{
		capabilityID: manager.CapabilityConfigNetworkProxyRef,
		editorID:     manager.ChangeNetworkProxyRef,
		label:        "Proxy secret reference",
		scope:        "live · new connections",
	},
	{
		capabilityID: manager.CapabilityConfigNetworkDNS,
		editorID:     manager.ChangeNetworkDNS,
		label:        "DNS mediation",
		scope:        "live · new connections",
	},
	{
		capabilityID: manager.CapabilityConfigEnvironment,
		editorID:     manager.ChangeProfileEnvironment,
		label:        "Environment policy",
		scope:        "new sessions",
	},
	{
		capabilityID: manager.CapabilityConfigHostFS,
		editorID:     manager.ChangeProfileHostFS,
		label:        "Host file access",
		scope:        "new sessions",
	},
	{
		capabilityID: manager.CapabilityConfigCommandProxy,
		editorID:     manager.ChangeProfileCommandProxy,
		label:        "Command proxy",
		scope:        "new sessions",
	},
	{
		capabilityID: manager.CapabilityConfigCommandAdapter,
		editorID:     manager.ChangeProfileCommandAdapter,
		label:        "Command adapters",
		scope:        "new sessions",
	},
	{
		capabilityID: manager.CapabilityConfigActivityRetention,
		editorID:     manager.ChangeActivityRetention,
		label:        "Activity retention",
		scope:        "future owners",
	},
	{
		capabilityID: manager.CapabilitySecretManage,
		editorID:     "secret.manage",
		label:        "Managed secrets",
		scope:        "live · new connections",
	},
}

func Config(input ConfigInput, options Options) string {
	if options.Width <= 0 {
		options.Width = 80
	}
	if options.Height <= 0 {
		options.Height = 24
	}
	projection, ok := configProjection(input.State)
	rows := ConfigRows(input.State)
	if !ok {
		output := fmt.Sprintf(
			"Config · no verified profile state\n"+
				"READ-ONLY · refresh Hideout state before editing\n\n"+
				"%s\n%s\n",
			components.Tabs(options.Unicode, options.Width),
			configFooter(options.Unicode, true),
		)
		return styleConfigOutput(output, options)
	}
	health := strings.ToUpper(
		sanitizeInline(input.State.StreamHealth.State),
	)
	if health == "" {
		health = "UNKNOWN"
	}
	access := "EDITABLE"
	if !input.State.CanMutate() {
		access = "READ-ONLY"
	}
	lines := []string{
		fmt.Sprintf(
			"Config · %s · revision %d · %s · %s",
			sanitizeInline(projection.Profile),
			projection.Revision,
			health,
			access,
		),
	}
	if !input.State.CanMutate() {
		reason := sanitizeInline(input.State.StreamHealth.Reason)
		if reason == "" {
			reason = "verified change state is unavailable"
		}
		lines = append(
			lines,
			"Changes disabled: "+reason+" · refresh before planning or applying",
		)
	}
	lines = append(lines, "", "FIELD | DESIRED | SCOPE | EFFECTIVE | TRANSITION")
	if len(rows) == 0 {
		lines = append(
			lines,
			"No editable settings are available for this profile.",
		)
	} else if options.Width < 80 {
		lines = append(lines, narrowConfigRows(rows, input.Selected)...)
	} else {
		lines = append(lines, normalConfigRows(rows, input.Selected)...)
	}
	if len(rows) != 0 {
		selected := clampConfigSelection(input.Selected, len(rows))
		row := rows[selected]
		lines = append(
			lines,
			"",
			"Selected · "+row.Label,
			"  DESIRED    "+row.Desired,
			"  EFFECTIVE  "+row.Effective,
			"  TRANSITION "+row.Transition,
			"  SCOPE      "+row.Scope,
		)
		if row.Editable {
			lines = append(
				lines,
				"  Enter opens a client-local draft; review and confirmation follow.",
			)
		} else {
			reason := row.Reason
			if reason == "" {
				reason = "setting is read-only"
			}
			lines = append(lines, "  DISABLED    "+reason)
		}
	}
	lines = append(
		lines,
		"",
		components.Tabs(options.Unicode, options.Width),
		configFooter(options.Unicode, options.Width < 72),
	)
	return styleConfigOutput(strings.Join(lines, "\n")+"\n", options)
}

func ConfigRows(state liveconsole.State) []ConfigRow {
	projection, ok := configProjection(state)
	if !ok {
		return nil
	}
	capabilities := make(
		map[string]liveconsole.CapabilityProjection,
		len(state.Capabilities),
	)
	for _, capability := range state.Capabilities {
		capabilities[capability.ID] = capability
	}
	rows := make([]ConfigRow, 0, len(configEditorDefinitions))
	for _, definition := range configEditorDefinitions {
		capability, advertised := capabilities[definition.capabilityID]
		if !advertised {
			continue
		}
		row := ConfigRow{
			CapabilityID: definition.capabilityID,
			EditorID:     definition.editorID,
			Label:        definition.label,
			Desired: configDesiredValue(
				projection,
				definition.editorID,
			),
			Effective: configEffectiveValue(
				state,
				projection,
				definition.editorID,
			),
			Transition: configTransitionValue(
				projection,
				definition.editorID,
			),
			Scope: definition.scope,
			Editable: state.CanMutate() &&
				capability.Mutable &&
				capability.Status == workloadtypes.CoverageAvailable,
			Reason: sanitizeInline(capability.Reason),
		}
		if row.Reason == "" && !capability.Mutable {
			row.Reason = "Hideout marked this setting read-only"
		}
		if row.Reason == "" &&
			capability.Status != workloadtypes.CoverageAvailable {
			row.Reason = "This setting is " +
				strings.ToLower(sanitizeInline(capability.Status))
		}
		if row.Reason == "" && !state.CanMutate() {
			row.Reason = "console is read-only until current state is refreshed"
		}
		rows = append(rows, row)
	}
	return rows
}

func configProjection(
	state liveconsole.State,
) (manager.ProfileProjection, bool) {
	profileName := state.ProfileScope
	if profileName == "" && len(state.Overview.Sessions) != 0 {
		profileName = state.Overview.Sessions[0].Profile
	}
	if profileName != "" {
		for _, projection := range state.Profiles {
			if projection.Profile == profileName {
				return projection, true
			}
		}
	}
	if len(state.Profiles) == 0 {
		return manager.ProfileProjection{}, false
	}
	return state.Profiles[0], true
}

func configDesiredValue(
	projection manager.ProfileProjection,
	editorID string,
) string {
	desired := projection.Desired
	switch editorID {
	case manager.ChangeNetworkPosture:
		return configNetworkMode(desired.Network.Mode)
	case manager.ChangeNetworkProxyRef:
		if desired.Network.ProxySecretRef == "" {
			return "not configured"
		}
		return sanitizeInline(desired.Network.ProxySecretRef)
	case manager.ChangeNetworkDNS:
		if desired.Network.MediatedResolver == "" {
			return "system"
		}
		return "doh " + sanitizeInline(
			desired.Network.MediatedResolver,
		)
	case manager.ChangeProfileEnvironment:
		return fmt.Sprintf(
			"%d set · %d inherit · %d deny",
			len(desired.Env.Public),
			len(desired.Env.Inherit),
			len(desired.Env.Deny),
		)
	case manager.ChangeProfileHostFS:
		return fmt.Sprintf(
			"%d allow · %d deny",
			len(desired.HostFS.Grants),
			len(desired.HostFS.Deny),
		)
	case manager.ChangeProfileCommandProxy:
		names := make(
			[]string,
			0,
			len(desired.CommandProxy.Commands),
		)
		for name := range desired.CommandProxy.Commands {
			names = append(names, sanitizeInline(name))
		}
		sort.Strings(names)
		if len(names) == 0 {
			return "none"
		}
		return strings.Join(names, ", ")
	case manager.ChangeProfileCommandAdapter:
		enabled := 0
		for _, adapter := range desired.CommandAdapters.Adapters {
			if adapter.Enabled {
				enabled++
			}
		}
		return fmt.Sprintf(
			"%d configured · %d enabled",
			len(desired.CommandAdapters.Adapters),
			enabled,
		)
	case manager.ChangeActivityRetention:
		retention := workloadtypes.DefaultActivityRetentionPolicy()
		suffix := " (default)"
		if desired.Activity != nil {
			retention = desired.Activity.Retention
			suffix = ""
		}
		return formatActivityRetentionPolicy(retention) + suffix
	case "secret.manage":
		return "references only · values hidden"
	default:
		return "unsupported"
	}
}

func configEffectiveValue(
	state liveconsole.State,
	projection manager.ProfileProjection,
	editorID string,
) string {
	switch editorID {
	case manager.ChangeNetworkPosture:
		if projection.Effective.Network == nil {
			return configEffectiveStatus(projection)
		}
		return configNetworkMode(projection.Effective.Network.Mode)
	case manager.ChangeNetworkProxyRef:
		if projection.Effective.Network == nil {
			return configEffectiveStatus(projection)
		}
		if projection.Effective.Network.ProxySecretRef == "" {
			return "not configured"
		}
		value := sanitizeInline(
			projection.Effective.Network.ProxySecretRef,
		)
		if projection.Effective.Network.SecretGeneration != 0 {
			value += fmt.Sprintf(
				" · version %d",
				projection.Effective.Network.SecretGeneration,
			)
		}
		return value
	case manager.ChangeNetworkDNS:
		if projection.Effective.Network == nil {
			return configEffectiveStatus(projection)
		}
		if projection.Effective.Network.DNS == "" {
			return "system"
		}
		return sanitizeInline(projection.Effective.Network.DNS)
	case "secret.manage":
		return "availability and version from Hideout"
	case manager.ChangeActivityRetention:
		return configActivityRetentionEffective(state, projection)
	default:
		current, older := 0, 0
		for _, session := range projection.Effective.Sessions {
			if session.Current {
				current++
			} else {
				older++
			}
		}
		if current == 0 && older == 0 {
			return "future session snapshot"
		}
		return fmt.Sprintf(
			"future session snapshot · %d current · %d older",
			current,
			older,
		)
	}
}

func configActivityRetentionEffective(
	state liveconsole.State,
	projection manager.ProfileProjection,
) string {
	matching := make([]manager.OperatorActivityRetentionProjection, 0)
	for _, retention := range state.ActivityRetention {
		if activityOwnerMatchesProfile(
			retention.Owner,
			projection.Profile,
			state,
		) {
			matching = append(matching, retention)
		}
	}
	parts := make([]string, 0, 2)
	switch len(matching) {
	case 0:
		parts = append(parts, "no current owner yet")
	case 1:
		parts = append(
			parts,
			fmt.Sprintf(
				"current owner %s / %s · %s",
				activityByteSize(matching[0].UsedBytes),
				activityByteSize(matching[0].LimitBytes),
				formatActivityRetentionAge(
					matching[0].MaxAgeSeconds,
				),
			),
		)
	default:
		parts = append(
			parts,
			fmt.Sprintf(
				"%d current owners · selected %s / %s · %s",
				len(matching),
				activityByteSize(matching[0].UsedBytes),
				activityByteSize(matching[0].LimitBytes),
				formatActivityRetentionAge(
					matching[0].MaxAgeSeconds,
				),
			),
		)
	}
	if global := state.ActivityStoreRetention; global != nil {
		parts = append(
			parts,
			fmt.Sprintf(
				"store (read-only) %s / %s",
				activityByteSize(global.UsedBytes),
				activityByteSize(global.LimitBytes),
			),
		)
	}
	return strings.Join(parts, " · ")
}

func activityOwnerMatchesProfile(
	owner workloadtypes.ActivityOwner,
	profileName string,
	state liveconsole.State,
) bool {
	for _, session := range state.Overview.Sessions {
		if session.Profile != profileName {
			continue
		}
		switch owner.Kind {
		case workloadtypes.OwnerDisposableSession:
			if owner.SessionID == session.ID {
				return true
			}
		case workloadtypes.OwnerReusableEnvironment:
			if owner.EnvironmentID == session.EnvironmentID {
				return true
			}
		}
	}
	if owner.Kind != workloadtypes.OwnerReusableEnvironment {
		return false
	}
	for _, environment := range state.Overview.Environments {
		if environment.Profile == profileName &&
			owner.EnvironmentID == environment.ID {
			return true
		}
	}
	return false
}

func formatActivityRetentionPolicy(
	policy workloadtypes.ActivityRetentionPolicy,
) string {
	return activityByteSize(uint64(policy.MaxBytes)) +
		" · " +
		formatActivityRetentionAge(policy.MaxAgeSeconds)
}

func formatActivityRetentionAge(seconds int64) string {
	if seconds == 0 {
		return "VM lifecycle"
	}
	const (
		hour = int64(time.Hour / time.Second)
		day  = 24 * hour
	)
	switch {
	case seconds%day == 0:
		days := seconds / day
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	case seconds%hour == 0:
		hours := seconds / hour
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	default:
		return (time.Duration(seconds) * time.Second).String()
	}
}

func configEffectiveStatus(
	projection manager.ProfileProjection,
) string {
	status := sanitizeInline(projection.Effective.Status)
	if status == "" {
		return "not observed"
	}
	return strings.ReplaceAll(status, "-", " ")
}

func configTransitionValue(
	projection manager.ProfileProjection,
	editorID string,
) string {
	transition := projection.Transition
	if transition == nil || !transitionMatchesEditor(
		transition.Kind,
		editorID,
	) {
		return "none"
	}
	value := sanitizeInline(transition.Phase)
	if value == "" {
		value = "unknown"
	}
	if len(transition.Blockers) != 0 {
		blockers := make([]string, 0, len(transition.Blockers))
		for _, blocker := range transition.Blockers {
			if safe := sanitizeInline(blocker); safe != "" {
				blockers = append(blockers, safe)
			}
		}
		if len(blockers) != 0 {
			value += " · blocked: " + strings.Join(blockers, ", ")
		}
	}
	return value
}

func transitionMatchesEditor(kind, editorID string) bool {
	kind = strings.ToLower(sanitizeInline(kind))
	switch {
	case strings.Contains(kind, "network"):
		return editorID == manager.ChangeNetworkPosture ||
			editorID == manager.ChangeNetworkProxyRef ||
			editorID == manager.ChangeNetworkDNS ||
			editorID == "secret.manage"
	case strings.Contains(kind, "environment"):
		return editorID == manager.ChangeProfileEnvironment
	case strings.Contains(kind, "hostfs"):
		return editorID == manager.ChangeProfileHostFS
	case strings.Contains(kind, "command"):
		return editorID == manager.ChangeProfileCommandProxy ||
			editorID == manager.ChangeProfileCommandAdapter
	case strings.Contains(kind, "retention"),
		strings.Contains(kind, "activity"):
		return editorID == manager.ChangeActivityRetention
	default:
		return true
	}
}

func configNetworkMode(value string) string {
	switch value {
	case profile.NetworkModeTun2Socks, "proxy":
		return "proxy"
	case profile.NetworkModeDirect:
		return "direct"
	default:
		value = sanitizeInline(value)
		if value == "" {
			return "unknown"
		}
		return value
	}
}

func normalConfigRows(
	rows []ConfigRow,
	selected int,
) []string {
	selected = clampConfigSelection(selected, len(rows))
	lines := make([]string, 0, len(rows)*2)
	for index, row := range rows {
		marker := " "
		if index == selected {
			marker = ">"
		}
		editable := ""
		if !row.Editable {
			editable = " [disabled]"
		}
		lines = append(
			lines,
			fmt.Sprintf(
				"%s %s%s | DESIRED %s",
				marker,
				row.Label,
				editable,
				row.Desired,
			),
			fmt.Sprintf(
				"  SCOPE %s | EFFECTIVE %s | TRANSITION %s",
				row.Scope,
				row.Effective,
				row.Transition,
			),
		)
	}
	return lines
}

func narrowConfigRows(
	rows []ConfigRow,
	selected int,
) []string {
	selected = clampConfigSelection(selected, len(rows))
	lines := make([]string, 0, len(rows)*2)
	for index, row := range rows {
		marker := " "
		if index == selected {
			marker = ">"
		}
		editable := ""
		if !row.Editable {
			editable = " · disabled"
		}
		lines = append(
			lines,
			marker+" "+row.Label+editable,
			"  "+row.Desired+" → "+row.Effective+
				" · "+row.Transition+" · "+row.Scope,
		)
	}
	return lines
}

func clampConfigSelection(selected, length int) int {
	if length == 0 || selected < 0 {
		return 0
	}
	if selected >= length {
		return length - 1
	}
	return selected
}

func configFooter(unicode, compact bool) string {
	separator := " | "
	if unicode {
		separator = " · "
	}
	keys := []string{"j/k select", "Enter edit", "? keys", "q quit"}
	if compact {
		keys = []string{"j/k select", "Enter edit", "? keys", "q quit"}
	}
	return strings.Join(keys, separator)
}

func styleConfigOutput(output string, options Options) string {
	output = fitOutput(output, options.Width)
	if !options.NoColor {
		output = "\x1b[36m" + output + "\x1b[0m"
	}
	return output
}
