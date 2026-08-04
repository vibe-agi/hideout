package components

import (
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

type Widgets struct {
	Table    table.Model
	Viewport viewport.Model
	Filter   textinput.Model
}

func NewWidgets(width, height int) Widgets {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	filter := textinput.New()
	filter.Prompt = "/ "
	filter.Placeholder = "filter current view"
	filter.CharLimit = 256
	filter.SetWidth(width)
	return Widgets{
		Table: table.New(
			table.WithColumns([]table.Column{
				{Title: "State", Width: min(14, width)},
				{Title: "Item", Width: max(1, width-17)},
			}),
			table.WithRows([]table.Row{}),
			table.WithWidth(width),
			table.WithHeight(max(1, height/3)),
		),
		Viewport: viewport.New(
			viewport.WithWidth(width),
			viewport.WithHeight(max(1, height/3)),
		),
		Filter: filter,
	}
}

func (widgets *Widgets) Resize(width, height int) {
	if widgets == nil {
		return
	}
	width = max(1, width)
	height = max(1, height)
	widgets.Table.SetWidth(width)
	widgets.Table.SetHeight(max(1, height/3))
	widgets.Viewport.SetWidth(width)
	widgets.Viewport.SetHeight(max(1, height/3))
	widgets.Filter.SetWidth(width)
}

func (widgets *Widgets) Update(message tea.Msg, focus string) tea.Cmd {
	if widgets == nil {
		return nil
	}
	var commands []tea.Cmd
	switch focus {
	case "primary":
		updated, command := widgets.Table.Update(message)
		widgets.Table = updated
		commands = append(commands, command)
	case "details":
		updated, command := widgets.Viewport.Update(message)
		widgets.Viewport = updated
		commands = append(commands, command)
	case "filter":
		updated, command := widgets.Filter.Update(message)
		widgets.Filter = updated
		commands = append(commands, command)
	}
	return tea.Batch(commands...)
}

func JoinFields(unicode bool, values ...string) string {
	separator := " | "
	if unicode {
		separator = " · "
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return strings.Join(out, separator)
}

func Tabs(unicode bool, limit int) string {
	first := "[1] Overview [2] Activity [3] Config [4] Operations [5] Migration [6] Help"
	if unicode {
		first = "[1] Overview [2] Activity [3] Config [4] Operations [5] Migration [6] Help"
	}
	if limit > 0 && limit < 88 {
		return "[1] Overview [2] Activity [3] Config\n[4] Operations [5] Migration [6] Help"
	}
	return first
}

func Footer(unicode bool, compact bool) string {
	separator := " | "
	if unicode {
		separator = " · "
	}
	keys := []string{
		"j/k select",
		"Enter inspect",
		"e environments",
	}
	if compact {
		keys[2] = "e env"
	}
	keys = append(keys, "? keys", "q quit")
	return strings.Join(keys, separator)
}

func EmptyState(summary, recovery string) string {
	if recovery == "" {
		return summary
	}
	return summary + "\n\n" + recovery
}
