package render

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/tui/components"
)

type MigrationInput struct {
	State        liveconsole.State
	Selected     int
	DetailOpen   bool
	RefreshError string
}

type MigrationRow struct {
	ID        string
	Kind      manager.MigrationOperationKind
	State     manager.MigrationPhase
	BundleID  string
	UpdatedAt time.Time
	Operation manager.MigrationOperationProjection
}

func MigrationRows(input MigrationInput) []MigrationRow {
	rows := make([]MigrationRow, 0, len(input.State.Migrations))
	for _, operation := range input.State.Migrations {
		if operation.Validate() != nil {
			continue
		}
		updatedAt := operation.Progress.CheckpointAt
		if updatedAt.IsZero() {
			updatedAt = operation.Progress.PhaseStartedAt
		}
		rows = append(rows, MigrationRow{
			ID: operation.OperationID, Kind: operation.Kind,
			State: operation.State, BundleID: string(operation.BundleID),
			UpdatedAt: updatedAt, Operation: operation,
		})
	}
	sort.SliceStable(rows, func(left, right int) bool {
		if rows[left].UpdatedAt.Equal(rows[right].UpdatedAt) {
			return rows[left].ID < rows[right].ID
		}
		return rows[left].UpdatedAt.After(rows[right].UpdatedAt)
	})
	return rows
}

func Migration(input MigrationInput, options Options) string {
	if options.Width <= 0 {
		options.Width = 80
	}
	if options.Height <= 0 {
		options.Height = 24
	}
	rows := MigrationRows(input)
	selected := clampMigrationRow(input.Selected, len(rows))
	active, recovery := migrationRowCounts(rows)
	capability, capabilityReason := migrationCapability(input.State)
	lines := []string{
		fmt.Sprintf(
			"Migration · %s · %d active · %d need action · %d retained",
			capability, active, recovery, len(rows),
		),
	}
	if capabilityReason != "" {
		lines = append(lines, "SUPPORT "+capabilityReason)
	}
	if input.RefreshError != "" {
		lines = append(lines, "REFRESH "+sanitizeInline(input.RefreshError)+" · last verified state retained")
	}
	if options.Width < 48 {
		lines = append(lines, "")
		lines = append(lines, migrationCompactSummary(rows, selected)...)
		lines = append(lines, "", "[5] Migration [6] Help", "x export · i import · Enter inspect · r refresh · q quit")
		return migrationColor(fitOutput(strings.Join(lines, "\n")+"\n", options.Width), options)
	}

	lines = append(lines, "", "ID | KIND | PHASE | PROGRESS | UPDATED")
	if len(rows) == 0 {
		lines = append(
			lines,
			"No migration has been started on this computer.",
			"Start with Enter, then choose Export or Import in the guided dialog.",
		)
	} else {
		lines = append(lines, renderMigrationRows(rows, selected, options.Width)...)
		if input.DetailOpen {
			lines = append(lines, "")
			lines = append(lines, migrationDetail(rows[selected].Operation, options.Width)...)
		} else {
			lines = append(
				lines,
				"",
				"Enter opens the selected plan, progress, recovery, and available actions.",
			)
		}
	}
	lines = append(
		lines,
		"",
		components.Tabs(options.Unicode, options.Width),
		migrationFooter(options.Unicode),
	)
	return migrationColor(fitOutput(strings.Join(lines, "\n")+"\n", options.Width), options)
}

func migrationCompactSummary(rows []MigrationRow, selected int) []string {
	if len(rows) == 0 {
		return []string{"No migrations yet.", "Enter starts a guided export or import."}
	}
	operation := rows[selected].Operation
	return []string{
		"> " + sanitizeInline(operation.OperationID),
		strings.ToUpper(sanitizeInline(string(operation.Kind))) + " · " +
			sanitizeInline(operation.PhaseLabel),
		migrationLogicalProgress(operation.Progress),
		"ETA " + migrationETA(operation.Progress),
		"NEXT " + migrationNextAction(operation),
	}
}

func renderMigrationRows(rows []MigrationRow, selected, width int) []string {
	lines := make([]string, 0, len(rows))
	for index, row := range rows {
		marker := " "
		if index == selected {
			marker = ">"
		}
		updated := "unknown"
		if !row.UpdatedAt.IsZero() {
			updated = row.UpdatedAt.UTC().Format("15:04:05")
		}
		progress := migrationLogicalProgress(row.Operation.Progress)
		if width < 88 {
			lines = append(lines, fmt.Sprintf(
				"%s %s · %s · %s\n  %s · %s",
				marker, sanitizeInline(row.ID), strings.ToUpper(sanitizeInline(string(row.Kind))),
				sanitizeInline(row.Operation.PhaseLabel), progress, updated,
			))
			continue
		}
		lines = append(lines, fmt.Sprintf(
			"%s %s | %s | %s | %s | %s",
			marker, sanitizeInline(row.ID), strings.ToUpper(sanitizeInline(string(row.Kind))),
			sanitizeInline(row.Operation.PhaseLabel), progress, updated,
		))
	}
	return lines
}

func migrationDetail(
	operation manager.MigrationOperationProjection,
	width int,
) []string {
	inventory := []string{
		"Bundle " + sanitizeInline(string(operation.BundleID)),
		fmt.Sprintf(
			"Identity safe-clone=%d exact-restore=%d",
			operation.IdentityPolicies.SafeClone,
			operation.IdentityPolicies.ExactGuestRestore,
		),
		fmt.Sprintf(
			"Fresh control=%d backend=%d",
			operation.IdentityPolicies.FreshControl,
			operation.IdentityPolicies.FreshBackend,
		),
		migrationComponentProgress(operation.Progress),
	}
	progress := []string{
		"Phase " + sanitizeInline(operation.PhaseLabel),
		"Current " + migrationCurrentItem(operation.Progress),
		"Logical " + migrationLogicalProgress(operation.Progress),
		"Encoded " + migrationEncodedProgress(operation.Progress),
		"Elapsed " + formatMigrationSeconds(operation.Progress.ElapsedSeconds),
		"ETA " + migrationETA(operation.Progress),
	}
	action := []string{
		"Blockers " + migrationBlockers(operation),
		"Next " + migrationNextAction(operation),
		"Effects " + migrationEffectSummary(operation.Effects),
	}
	if operation.Progress.CancelPending {
		action = append(action, "Cancellation pending at the next safe boundary")
	}
	if operation.TerminalReceipt != nil {
		action = append(
			action,
			"Receipt "+sanitizeInline(operation.TerminalReceipt.ResultCode),
			fmt.Sprintf(
				"Completed %d/%d components · claims released=%t",
				operation.TerminalReceipt.CompletedComponents,
				operation.TerminalReceipt.TotalComponents,
				operation.TerminalReceipt.ClaimsReleased,
			),
		)
	}
	if width >= 108 {
		return migrationThreePanes(inventory, progress, action, width)
	}
	lines := []string{"Inventory"}
	lines = appendIndentedMigration(lines, inventory)
	lines = append(lines, "Progress")
	lines = appendIndentedMigration(lines, progress)
	lines = append(lines, "Action")
	return appendIndentedMigration(lines, action)
}

func migrationThreePanes(inventory, progress, action []string, width int) []string {
	gap := " │ "
	columnWidth := max(20, (width-len(gap)*2)/3)
	headings := []string{"INVENTORY", "PROGRESS", "ACTION"}
	rows := max(len(inventory), max(len(progress), len(action)))
	lines := []string{migrationColumnLine(headings, columnWidth, gap)}
	for index := 0; index < rows; index++ {
		values := []string{
			migrationLineAt(inventory, index),
			migrationLineAt(progress, index),
			migrationLineAt(action, index),
		}
		lines = append(lines, migrationColumnLine(values, columnWidth, gap))
	}
	return lines
}

func migrationColumnLine(values []string, width int, gap string) string {
	columns := make([]string, len(values))
	for index, value := range values {
		runes := []rune(sanitizeInline(value))
		if len(runes) > width {
			runes = runes[:max(1, width-1)]
			runes = append(runes, '…')
		}
		columns[index] = fmt.Sprintf("%-*s", width, string(runes))
	}
	return strings.TrimRight(strings.Join(columns, gap), " ")
}

func migrationLineAt(values []string, index int) string {
	if index < 0 || index >= len(values) {
		return ""
	}
	return values[index]
}

func appendIndentedMigration(lines, values []string) []string {
	for _, value := range values {
		lines = append(lines, "  "+value)
	}
	return lines
}

func migrationLogicalProgress(progress manager.MigrationProgressProjection) string {
	completed := formatMigrationBytes(progress.CompletedLogicalBytes)
	if !progress.LogicalTotalKnown {
		return completed + " / total unknown"
	}
	return completed + " / " + formatMigrationBytes(progress.TotalLogicalBytes)
}

func migrationEncodedProgress(progress manager.MigrationProgressProjection) string {
	completed := formatMigrationBytes(progress.CompletedEncodedBytes)
	if !progress.EncodedTotalKnown {
		return completed + " / total unknown"
	}
	return completed + " / " + formatMigrationBytes(progress.TotalEncodedBytes)
}

func migrationComponentProgress(progress manager.MigrationProgressProjection) string {
	if progress.ComponentsTotal == 0 {
		return fmt.Sprintf("Components %d / total unknown", progress.ComponentsComplete)
	}
	return fmt.Sprintf("Components %d / %d", progress.ComponentsComplete, progress.ComponentsTotal)
}

func migrationCurrentItem(progress manager.MigrationProgressProjection) string {
	if value := sanitizeInline(progress.CurrentItem); value != "" {
		return value
	}
	return "waiting for the next verified checkpoint"
}

func migrationETA(progress manager.MigrationProgressProjection) string {
	if !progress.RemainingKnown {
		return "unknown"
	}
	return formatMigrationSeconds(progress.RemainingSeconds)
}

func migrationBlockers(operation manager.MigrationOperationProjection) string {
	values := make([]string, 0, len(operation.Warnings)+1)
	if operation.Recovery.Required {
		values = append(values, sanitizeInline(operation.Recovery.Code))
	}
	for _, warning := range operation.Warnings {
		values = append(values, sanitizeInline(warning.Code))
	}
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func migrationNextAction(operation manager.MigrationOperationProjection) string {
	if operation.Recovery.Required {
		if value := sanitizeInline(operation.Recovery.NextAction); value != "" {
			return value
		}
	}
	if operation.TerminalReceipt != nil {
		return "Review the terminal receipt; no migration action is pending."
	}
	if operation.Progress.CancelPending {
		return "Wait for the current safe boundary."
	}
	return "Wait for the next verified checkpoint, or open this operation to request cancellation."
}

func migrationEffectSummary(effects []manager.MigrationEffectProjection) string {
	if len(effects) == 0 {
		return "none reported"
	}
	counts := map[manager.MigrationEffectStatus]int{}
	for _, effect := range effects {
		counts[effect.Status]++
	}
	order := []manager.MigrationEffectStatus{
		manager.MigrationEffectRunning,
		manager.MigrationEffectFailed,
		manager.MigrationEffectPending,
		manager.MigrationEffectSucceeded,
		manager.MigrationEffectCompensating,
		manager.MigrationEffectCompensated,
		manager.MigrationEffectUnproved,
	}
	values := make([]string, 0, len(order))
	for _, status := range order {
		if counts[status] != 0 {
			values = append(values, fmt.Sprintf("%s=%d", status, counts[status]))
		}
	}
	return strings.Join(values, " ")
}

func migrationRowCounts(rows []MigrationRow) (int, int) {
	active, recovery := 0, 0
	for _, row := range rows {
		if row.Operation.Recovery.Required {
			recovery++
		}
		if row.Operation.TerminalReceipt == nil && !row.Operation.Recovery.Required {
			active++
		}
	}
	return active, recovery
}

func migrationCapability(state liveconsole.State) (string, string) {
	for _, capability := range state.Capabilities {
		if capability.ID != "migration.manage" {
			continue
		}
		status := strings.ToUpper(sanitizeInline(capability.Status))
		if status == "" {
			status = "UNKNOWN"
		}
		return status, sanitizeInline(capability.Reason)
	}
	return "UNAVAILABLE", "this snapshot does not advertise migration support"
}

func formatMigrationBytes(value uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	amount := float64(value)
	unit := 0
	for amount >= 1024 && unit < len(units)-1 {
		amount /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", amount, units[unit])
}

func formatMigrationSeconds(value uint64) string {
	duration := time.Duration(value) * time.Second
	if duration < time.Minute {
		return fmt.Sprintf("%ds", value)
	}
	if duration < time.Hour {
		return fmt.Sprintf("%dm%02ds", value/60, value%60)
	}
	return fmt.Sprintf("%dh%02dm", value/3600, value%3600/60)
}

func clampMigrationRow(selected, length int) int {
	if length == 0 || selected < 0 {
		return 0
	}
	if selected >= length {
		return length - 1
	}
	return selected
}

func migrationFooter(unicode bool) string {
	separator := " | "
	if unicode {
		separator = " · "
	}
	return strings.Join([]string{
		"j/k select", "x export", "i import", "Enter open", "r refresh", "? keys", "q quit",
	}, separator)
}

func migrationColor(output string, options Options) string {
	if options.NoColor {
		return output
	}
	return "\x1b[36m" + output + "\x1b[0m"
}
