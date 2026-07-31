package app

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/secrets"
)

type errorGuidance struct {
	Code       string
	Reason     string
	Suggestion string
	Next       []string
	Notes      []string
}

type unknownCommandError struct {
	token string
}

func (err *unknownCommandError) Error() string {
	if err == nil {
		return "unknown command"
	}
	return fmt.Sprintf("unknown command %q", err.token)
}

type commandRouteError struct {
	command string
	cause   error
}

func (err *commandRouteError) Error() string {
	if err == nil || err.cause == nil {
		return "command failed"
	}
	return err.cause.Error()
}

func (err *commandRouteError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

var (
	missingSecretRefPattern = regexp.MustCompile(
		`(?i)secret (?:ref|reference)\s+["']?([A-Za-z0-9][A-Za-z0-9._-]{0,63})`,
	)
	safeHelpSearchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

func writeErrorGuidance(
	w io.Writer,
	err error,
	catalog commandCatalog,
) {
	fmt.Fprintln(w, "hideout:", sanitizeGuidanceText(err.Error()))
	guidance := guidanceForError(err, catalog)
	if guidance.Code == "" {
		return
	}
	fmt.Fprintf(w, "  code: %s\n", guidance.Code)
	if guidance.Reason != "" {
		fmt.Fprintf(w, "  %s\n", guidance.Reason)
	}
	if guidance.Suggestion != "" {
		fmt.Fprintln(w, "Did you mean:")
		fmt.Fprintf(w, "  %s\n", guidance.Suggestion)
	}
	if len(guidance.Next) != 0 {
		fmt.Fprintln(w, "Next:")
		for _, command := range guidance.Next {
			fmt.Fprintf(w, "  %s\n", command)
		}
	}
	for _, note := range guidance.Notes {
		fmt.Fprintf(w, "Note: %s\n", note)
	}
}

func guidanceForError(
	err error,
	catalog commandCatalog,
) errorGuidance {
	if err == nil {
		return errorGuidance{}
	}

	var configurationOutcome *configurationApplyOutcomeError
	if errors.As(err, &configurationOutcome) {
		return errorGuidance{
			Code: "operation.outcome-unknown",
			Reason: "Hideout has not yet returned verified final evidence " +
				"for operation " + configurationOutcome.operationID + ".",
			Next: []string{
				"hideout tui",
				"hideout daemon status --human",
			},
			Notes: []string{
				"Inspect this exact ID in Operations before attempting another mutation.",
				"Do not create or apply a replacement plan until this exact operation shows a verified final result.",
			},
		}
	}

	var secretOutcome *secretApplyOutcomeError
	if errors.As(err, &secretOutcome) {
		return errorGuidance{
			Code: "operation.outcome-unknown",
			Reason: "Hideout has not yet returned verified final evidence " +
				"for operation " + secretOutcome.operationID + ".",
			Next: []string{
				"hideout tui",
				"hideout daemon status --human",
			},
			Notes: []string{
				"Inspect this exact ID in Operations before attempting another mutation.",
				"If the operation is absent or terminally failed, review a fresh plan instead of assuming the prior apply succeeded.",
			},
		}
	}

	var confirmation *configurationConfirmationRequiredError
	if errors.As(err, &confirmation) {
		return errorGuidance{
			Code:   "configuration.confirmation-required",
			Reason: "The canonical plan was created, but no configuration state was changed.",
			Next: []string{
				"hideout connect apply " + confirmation.operationID + " --yes",
			},
			Notes: []string{
				"Use only this exact operation ID after reviewing the displayed diff, effects, blockers, and rollback.",
			},
		}
	}

	var configurationRecovery *configurationRecoveryRequiredError
	if errors.As(err, &configurationRecovery) {
		return errorGuidance{
			Code: "operation.recovery-required",
			Reason: "Configuration operation " +
				configurationRecovery.operationID +
				" crossed an effect boundary and needs exact reconciliation.",
			Next: []string{
				"hideout tui",
				"hideout daemon status --human",
			},
			Notes: []string{
				"Inspect this exact ID and follow its stored recovery action; do not create a replacement mutation.",
			},
		}
	}

	var unknown *unknownCommandError
	if errors.As(err, &unknown) {
		guidance := errorGuidance{
			Code:   "command.unknown",
			Reason: "No state was changed.",
		}
		if closest := closestCatalogCommand(catalog, unknown.token); closest != "" {
			guidance.Suggestion = "hideout " + closest
		}
		if safeHelpSearchPattern.MatchString(unknown.token) {
			guidance.Next = []string{"hideout help search " + unknown.token}
		} else {
			guidance.Next = []string{"hideout help all"}
		}
		return guidance
	}

	message := err.Error()
	if errors.Is(err, secrets.ErrSecretMissing) ||
		strings.Contains(strings.ToLower(message), "secret ref") &&
			(strings.Contains(strings.ToLower(message), "not set") ||
				strings.Contains(strings.ToLower(message), "missing")) ||
		strings.Contains(strings.ToLower(message), "secret reference is missing") {
		ref := missingSecretRef(message)
		next := []string{"hideout secret list", "hideout help secret"}
		if ref != "" {
			next = []string{
				"hideout secret set " + ref,
				"hideout secret status " + ref,
				"hideout help secret",
			}
		}
		return errorGuidance{
			Code:   "secret.missing",
			Reason: "The selected connection names a credential that the running daemon cannot resolve.",
			Next:   next,
			Notes: []string{
				"Enter the value in the hidden prompt; do not put it in argv or an environment export.",
				"The daemon can accept a healthy secret update online.",
			},
		}
	}

	if errors.Is(err, manager.ErrStaleConfigurationPlan) ||
		errors.Is(err, manager.ErrStaleProfileRevision) {
		return errorGuidance{
			Code:   "configuration.stale-client",
			Reason: "Another client changed the profile after review; no stale plan was applied.",
			Next: []string{
				"hideout tui",
				"hideout show connection",
			},
			Notes: []string{
				"Refresh, review the new diff, and confirm a newly issued plan.",
			},
		}
	}

	if errors.Is(err, manager.ErrConfigurationProviderUnavailable) {
		return errorGuidance{
			Code:   "capability.unsupported",
			Reason: "This daemon does not advertise a provider for the requested capability, so no fallback was attempted.",
			Next: []string{
				"hideout support matrix",
				"hideout version",
			},
			Notes: []string{
				"Update to a version that supports the capability or choose an advertised provider.",
			},
		}
	}

	if errors.Is(err, manager.ErrOperationTerminalUnproved) {
		return errorGuidance{
			Code:   "operation.proof-unproved",
			Reason: "The requested terminal state is not backed by the required durable effect evidence.",
			Next: []string{
				"hideout tui",
				"hideout daemon status --human",
			},
			Notes: []string{
				"Keep the operation unproved; inspect its exact ID, effects, and recovery action before retrying.",
			},
		}
	}

	if errors.Is(err, manager.ErrConfigurationRecoveryRequired) ||
		errors.Is(err, manager.ErrSecretRecoveryRequired) ||
		errors.Is(err, manager.ErrNetworkTransitionRecoveryRequired) {
		return errorGuidance{
			Code:   "operation.recovery-required",
			Reason: "The operation crossed an effect boundary and must be reconciled by its exact operation ID.",
			Next: []string{
				"hideout tui",
				"hideout daemon status --human",
			},
			Notes: []string{
				"Follow the stored recovery action and retry only the same operation ID; do not create a replacement mutation.",
			},
		}
	}

	var routed *commandRouteError
	if errors.As(err, &routed) &&
		(strings.HasPrefix(strings.ToLower(strings.TrimSpace(message)), "usage:") ||
			strings.Contains(strings.ToLower(message), "unknown") ||
			strings.Contains(strings.ToLower(message), "flag provided but not defined")) {
		if entry, ok := catalog.lookup(routed.command); ok && !entry.spec.Hidden {
			return errorGuidance{
				Code:   "input.invalid",
				Reason: "The command was rejected before its requested effect was applied.",
				Next:   []string{"hideout help " + entry.spec.Name},
			}
		}
	}

	return errorGuidance{}
}

func missingSecretRef(message string) string {
	match := missingSecretRefPattern.FindStringSubmatch(message)
	if len(match) != 2 || secrets.ValidateRef(match[1]) != nil {
		return ""
	}
	return match[1]
}

func closestCatalogCommand(
	catalog commandCatalog,
	token string,
) string {
	token = strings.ToLower(strings.TrimSpace(token))
	if !safeHelpSearchPattern.MatchString(token) {
		return ""
	}
	best := ""
	bestDistance := -1
	for _, entry := range catalog.entries {
		if entry.spec.Hidden {
			continue
		}
		candidate := strings.ToLower(entry.spec.Name)
		distance := editDistance(token, candidate)
		if bestDistance == -1 ||
			distance < bestDistance ||
			distance == bestDistance && candidate < best {
			best = entry.spec.Name
			bestDistance = distance
		}
	}
	if bestDistance > 3 &&
		(best == "" || !strings.HasPrefix(best, token) && !strings.HasPrefix(token, best)) {
		return ""
	}
	return best
}

func editDistance(left, right string) int {
	a := []rune(left)
	b := []rune(right)
	previous := make([]int, len(b)+1)
	for index := range previous {
		previous[index] = index
	}
	for row, leftRune := range a {
		current := make([]int, len(b)+1)
		current[0] = row + 1
		for column, rightRune := range b {
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}
			current[column+1] = minInt(
				current[column]+1,
				previous[column+1]+1,
				previous[column]+cost,
			)
		}
		previous = current
	}
	return previous[len(b)]
}

func minInt(values ...int) int {
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}

func sanitizeGuidanceText(value string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
			'\u2066', '\u2067', '\u2068', '\u2069':
			return -1
		}
		if r == '\n' || r == '\r' || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
}
