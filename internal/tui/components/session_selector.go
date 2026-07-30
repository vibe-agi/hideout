package components

import (
	"slices"

	"github.com/vibe-agi/hideout/internal/manager"
)

type SessionSelector struct {
	rows     []manager.SessionSummary
	selected int
}

// Replace discards every row from the previous authoritative snapshot before
// applying the new one. This prevents detail/activity rows from one session
// surviving a session switch or reseed.
func (selector *SessionSelector) Replace(
	sessions []manager.SessionSummary,
	preferred string,
) {
	selector.rows = slices.Clone(sessions)
	selector.selected = 0
	if preferred == "" {
		return
	}
	for index := range selector.rows {
		if selector.rows[index].ID == preferred {
			selector.selected = index
			return
		}
	}
}

func (selector *SessionSelector) Move(delta int) {
	if len(selector.rows) == 0 || delta == 0 {
		return
	}
	selector.selected = (selector.selected + delta) % len(selector.rows)
	if selector.selected < 0 {
		selector.selected += len(selector.rows)
	}
}

func (selector SessionSelector) Selected() string {
	if selector.selected < 0 || selector.selected >= len(selector.rows) {
		return ""
	}
	return selector.rows[selector.selected].ID
}

func (selector SessionSelector) Rows() []manager.SessionSummary {
	return slices.Clone(selector.rows)
}
