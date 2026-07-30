package render

import (
	"fmt"
	"strings"

	"github.com/vibe-agi/hideout/internal/operatorhelp"
)

type HelpInput struct {
	Catalog    operatorhelp.Catalog
	Context    string
	CommandIDs []string
}

func Help(input HelpInput, options Options) string {
	if options.Width <= 0 {
		options.Width = 80
	}
	if options.Height <= 0 {
		options.Height = 24
	}
	contextName := sanitizeInline(input.Context)
	if contextName == "" {
		contextName = "Overview"
	}
	lines := []string{
		"Hideout · Help · " + contextName,
		"Documentation only · actions still use their normal review and confirmation.",
		"",
		"Keys",
		"  1-5 views · Tab focus · ? help · q quit",
		"  " + helpContextKeys(contextName),
		"",
		"CLI equivalents",
	}
	if err := input.Catalog.Validate(); err != nil {
		lines = append(
			lines,
			"  Command catalog unavailable in this client.",
		)
	} else {
		found := 0
		for _, id := range input.CommandIDs {
			command, ok := input.Catalog.Lookup(id)
			if !ok || command.Hidden {
				continue
			}
			found++
			lines = append(lines, helpCommandLines(command, options.Width)...)
		}
		if found == 0 {
			lines = append(
				lines,
				"  No command is mapped to this view.",
				"  Browse: hideout help all",
			)
		}
	}
	lines = append(lines, "", "Esc close")
	lines = wrapHelpLines(lines, options.Width)
	lines = fitHelpHeight(lines, options.Height)
	output := fitOutput(strings.Join(lines, "\n")+"\n", options.Width)
	if !options.NoColor {
		output = "\x1b[36m" + output + "\x1b[0m"
	}
	return output
}

func helpContextKeys(contextName string) string {
	switch strings.ToLower(contextName) {
	case "activity":
		return "/ filter · h/l tabs · r refresh · j/k select · Enter inspect"
	case "config", "configuration":
		return "j/k select · Enter review · changes open as drafts"
	case "operations":
		return "j/k select · Enter inspect exact operation"
	case "environments":
		return "s stop · c clean · Enter review exact target"
	case "overview":
		return "j/k session · Enter inspect · e environments"
	default:
		return "j/k select · Enter inspect"
	}
}

func helpCommandLines(
	command operatorhelp.Command,
	width int,
) []string {
	name := sanitizeInline(command.Name)
	stability := sanitizeInline(string(command.Stability))
	lines := []string{fmt.Sprintf("  %s  [%s]", name, stability)}
	if len(command.Syntax) != 0 {
		lines = append(
			lines,
			"    "+sanitizeInline(command.Syntax[0]),
		)
	}
	lines = append(
		lines,
		"    "+sanitizeInline(command.Purpose),
		"    Details: hideout help "+name,
	)
	return wrapHelpLines(lines, width)
}

func wrapHelpLines(lines []string, width int) []string {
	if width <= 0 {
		return nil
	}
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		line = sanitizeHelpLine(line)
		if line == "" || DisplayWidth(line) <= width {
			wrapped = append(wrapped, line)
			continue
		}
		prefixLength := len(line) - len(strings.TrimLeft(line, " "))
		prefix := strings.Repeat(" ", prefixLength)
		words := strings.Fields(strings.TrimSpace(line))
		current := prefix
		for _, word := range words {
			separator := ""
			if strings.TrimSpace(current) != "" {
				separator = " "
			}
			if DisplayWidth(current+separator+word) > width &&
				strings.TrimSpace(current) != "" {
				wrapped = append(wrapped, current)
				current = prefix + word
				continue
			}
			current += separator + word
		}
		if strings.TrimSpace(current) != "" {
			wrapped = append(wrapped, current)
		}
	}
	return wrapped
}

func sanitizeHelpLine(line string) string {
	prefixLength := len(line) - len(strings.TrimLeft(line, " "))
	return strings.Repeat(" ", prefixLength) +
		sanitizeInline(strings.TrimLeft(line, " "))
}

func fitHelpHeight(lines []string, height int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	if height == 1 {
		return []string{"Esc close"}
	}
	fitted := append([]string(nil), lines[:height-2]...)
	fitted = append(fitted, "…", "Esc close")
	return fitted
}
