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

type OperationsInput struct {
	State      liveconsole.State
	Selected   int
	DetailOpen bool
	LookupID   string
}

type OperationRow struct {
	ID        string
	Kind      string
	Owner     string
	Phase     string
	UpdatedAt time.Time
	Operation manager.Operation
}

func OperationRows(input OperationsInput) []OperationRow {
	rows := make([]OperationRow, 0, len(input.State.Operations))
	for _, operation := range input.State.Operations {
		if operation.Validate() != nil {
			continue
		}
		rows = append(rows, OperationRow{
			ID:        operation.ID,
			Kind:      operation.Kind,
			Owner:     operation.Owner.Kind + "/" + operation.Owner.ID,
			Phase:     operation.Phase,
			UpdatedAt: operation.UpdatedAt,
			Operation: operation,
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

func Operations(input OperationsInput, options Options) string {
	if options.Width <= 0 {
		options.Width = 80
	}
	if options.Height <= 0 {
		options.Height = 24
	}
	rows := OperationRows(input)
	selected, lookupFound := operationSelection(
		rows,
		input.Selected,
		input.LookupID,
	)
	health := strings.ToUpper(
		sanitizeInline(input.State.StreamHealth.State),
	)
	if health == "" {
		health = "UNKNOWN"
	}
	lines := []string{
		fmt.Sprintf(
			"Operations · %d retained · %s",
			len(rows),
			health,
		),
	}
	if input.LookupID != "" {
		switch {
		case lookupFound:
			lines = append(
				lines,
				"Resumed exact operation "+
					sanitizeInline(input.LookupID)+
					"; no replacement request was created.",
			)
		default:
			lines = append(
				lines,
				"Operation "+sanitizeInline(input.LookupID)+
					" is not present in this snapshot.",
				"Next: refresh the authenticated snapshot; never create a replacement operation.",
			)
		}
	}
	lines = append(lines, "", "ID | KIND | OWNER | PHASE | UPDATED")
	if len(rows) == 0 {
		lines = append(
			lines,
			"No durable operations are present in this scope.",
			"Accepted work appears here by stable operation ID.",
		)
	} else {
		lines = append(
			lines,
			renderOperationRows(rows, selected, options.Width)...,
		)
		if input.DetailOpen || input.LookupID != "" && lookupFound {
			lines = append(lines, "")
			lines = append(
				lines,
				operationDetail(rows[selected].Operation)...,
			)
		} else {
			lines = append(
				lines,
				"",
				"Enter opens effects, evidence, result, and recovery for the selected exact ID.",
			)
		}
	}
	lines = append(
		lines,
		"",
		components.Tabs(options.Unicode, options.Width),
		operationsFooter(options.Unicode),
	)
	output := fitOutput(strings.Join(lines, "\n")+"\n", options.Width)
	if !options.NoColor {
		output = "\x1b[36m" + output + "\x1b[0m"
	}
	return output
}

func operationSelection(
	rows []OperationRow,
	selected int,
	lookupID string,
) (int, bool) {
	if lookupID != "" {
		for index, row := range rows {
			if row.ID == lookupID {
				return index, true
			}
		}
	}
	if len(rows) == 0 || selected < 0 {
		return 0, false
	}
	if selected >= len(rows) {
		selected = len(rows) - 1
	}
	return selected, false
}

func renderOperationRows(
	rows []OperationRow,
	selected int,
	width int,
) []string {
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
		line := fmt.Sprintf(
			"%s %s | %s | %s | %s | %s",
			marker,
			sanitizeInline(row.ID),
			sanitizeInline(row.Kind),
			sanitizeInline(row.Owner),
			strings.ToUpper(sanitizeInline(row.Phase)),
			updated,
		)
		if width < 80 {
			line = fmt.Sprintf(
				"%s %s · %s · %s\n  %s · %s",
				marker,
				sanitizeInline(row.ID),
				sanitizeInline(row.Kind),
				sanitizeInline(row.Owner),
				strings.ToUpper(sanitizeInline(row.Phase)),
				updated,
			)
		}
		lines = append(lines, line)
	}
	return lines
}

func operationDetail(operation manager.Operation) []string {
	lines := []string{
		"Operation " + sanitizeInline(operation.ID),
		"Kind " + sanitizeInline(operation.Kind) +
			" · Owner " + sanitizeInline(operation.Owner.Kind) +
			"/" + sanitizeInline(operation.Owner.ID) +
			" · Phase " +
			strings.ToUpper(sanitizeInline(operation.Phase)),
		fmt.Sprintf(
			"Base revision %d · Plan %s",
			operation.BaseRevision,
			sanitizeInline(operation.PlanDigest),
		),
	}
	lines = append(lines, operationStateGuidance(operation)...)
	lines = append(lines, "Effects")
	if len(operation.Effects) == 0 {
		lines = append(lines, "  none reported")
	}
	for _, effect := range operation.Effects {
		lines = append(
			lines,
			"  Effect "+sanitizeInline(effect.ID)+
				" · "+sanitizeInline(effect.Kind)+
				" · "+sanitizeInline(effect.Provider)+
				" · "+strings.ToUpper(
				sanitizeInline(effect.Status),
			),
		)
		if len(effect.Evidence) == 0 {
			lines = append(lines, "    Evidence pending")
			continue
		}
		for _, evidence := range effect.Evidence {
			value := sanitizeInline(evidence.Code)
			if evidence.Ref != "" {
				value += "=" + sanitizeInline(evidence.Ref)
			}
			if !evidence.ObservedAt.IsZero() {
				value += " · " +
					evidence.ObservedAt.UTC().Format(
						time.RFC3339,
					)
			}
			lines = append(lines, "    Evidence "+value)
		}
	}
	if operation.Result == nil {
		lines = append(
			lines,
			"Result pending · terminal evidence has not been proved",
		)
	} else {
		lines = append(
			lines,
			"Result "+sanitizeInline(operation.Result.Code)+
				" · "+sanitizeInline(operation.Result.Status),
			"  "+sanitizeInline(operation.Result.Summary),
		)
	}
	lines = append(
		lines,
		"Recovery "+sanitizeInline(operation.Recovery.Code)+
			" · "+sanitizeInline(operation.Recovery.Summary),
	)
	if operation.Recovery.NextAction != "" {
		lines = append(
			lines,
			"  Next "+sanitizeInline(
				operation.Recovery.NextAction,
			),
		)
	}
	return lines
}

func operationStateGuidance(operation manager.Operation) []string {
	switch operation.Phase {
	case manager.OperationRecoveryRequired:
		return []string{
			"State ACTION REQUIRED · completion is not proved.",
			"Retry only this exact operation ID after following its stored recovery action.",
		}
	case manager.OperationRollingBack:
		return []string{
			"State ROLLING BACK · neither success nor restoration is terminal yet.",
		}
	case manager.OperationRollbackUnproved:
		return []string{
			"State UNPROVED · rollback finished without sufficient restoration evidence.",
			"Do not repeat the original mutation; follow the stored recovery action.",
		}
	}
	if strings.Contains(operation.Kind, ".stop") &&
		!operation.Terminal() {
		return []string{
			"State STOPPING · backend absence and cleanup evidence are still pending.",
		}
	}
	return nil
}

func operationsFooter(unicode bool) string {
	separator := " | "
	if unicode {
		separator = " · "
	}
	return strings.Join(
		[]string{
			"j/k select",
			"Enter details",
			"? keys",
			"q quit",
		},
		separator,
	)
}
