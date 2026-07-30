package app

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

const defaultHelpWidth = 80

var helpTaskGroupOrder = []string{
	commandGroupGetStarted,
	commandGroupRunSafely,
	commandGroupObserve,
	commandGroupConfigure,
	commandGroupEnvironments,
	commandGroupDiagnose,
	commandGroupInstall,
	commandGroupDeveloper,
	commandGroupLab,
}

func (a app) helpCommand(args []string) error {
	if len(args) == 0 || (len(args) == 1 && isHelpToken(args[0])) {
		a.primaryUsage()
		return nil
	}

	switch args[0] {
	case "all", "--all":
		a.catalogUsage(strings.Join(args[1:], " "), false)
		return nil
	case "search":
		query := strings.TrimSpace(strings.Join(args[1:], " "))
		if query == "" {
			return fmt.Errorf("usage: hideout help search <word>")
		}
		if a.catalogUsage(query, true) == 0 {
			return fmt.Errorf(
				"no help results for %q; try a shorter word or use: hideout help all",
				query,
			)
		}
		return nil
	}

	if len(args) != 1 {
		return fmt.Errorf(
			"usage: hideout help [<command>|all [query]|search <query>]",
		)
	}
	topic := canonicalHelpTopic(strings.TrimSpace(args[0]))
	entry, ok := defaultCommandCatalog().lookup(topic)
	if !ok || entry.spec.Hidden {
		return fmt.Errorf(
			"unknown help topic %q; search with: hideout help search <word> or browse: hideout help --all",
			args[0],
		)
	}
	a.contextualUsage(entry.spec)
	return nil
}

func canonicalHelpTopic(topic string) string {
	switch topic {
	case "readiness":
		return "doctor"
	case "privacy", "connection", "proxy":
		return "connect"
	case "secrets":
		return "secret"
	case "update", "uninstall":
		return "package"
	case "report":
		return "support"
	default:
		return topic
	}
}

func (a app) primaryUsage() {
	fmt.Fprintln(a.stdout, "Hideout — run unfamiliar developer tools inside an inspectable local VM.")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Start here:")
	fmt.Fprintln(a.stdout, "  1. hideout setup")
	fmt.Fprintln(a.stdout, "  2. hideout doctor")
	fmt.Fprintln(a.stdout, "  3. cd /path/to/project")
	fmt.Fprintln(a.stdout, "     hideout run -- git status --short")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "While it runs:")
	fmt.Fprintln(a.stdout, "  hideout tui")
	fmt.Fprintln(a.stdout, "  hideout activity summary")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Common tasks:")
	fmt.Fprintln(a.stdout, "  Proxy or DNS:      hideout help connect")
	fmt.Fprintln(a.stdout, "  Stop or delete VM: hideout help stop")
	fmt.Fprintln(a.stdout, "  Browser console:   hideout ui")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Find a command:")
	fmt.Fprintln(a.stdout, "  hideout help <command>")
	fmt.Fprintln(a.stdout, "  hideout help search <word>")
	fmt.Fprintln(a.stdout, "  hideout help all  (compatibility alias: hideout help --all)")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Boundary: macOS arm64 prerelease; the selected project is writable; direct networking does not hide your network origin.")
}

func (a app) contextualUsage(spec commandSpec) {
	width := helpTerminalWidth()
	fmt.Fprintf(a.stdout, "%s  [%s · %s]\n", spec.Name, spec.Stability, spec.Audience)
	writeHelpSection(a, width, "Purpose", []string{spec.Purpose}, false)
	writeHelpSection(a, width, "Usage", spec.Syntax, true)
	if len(spec.Aliases) != 0 {
		writeHelpSection(a, width, "Aliases", spec.Aliases, true)
	}
	if len(spec.Flags) != 0 {
		fmt.Fprintln(a.stdout, "Flags:")
		for _, flag := range spec.Flags {
			label := strings.TrimSpace(flag.Name + " " + flag.Value)
			if width < 64 || helpTextWidth(label)+helpTextWidth(flag.Help)+4 > width {
				fmt.Fprintf(a.stdout, "  %s\n", label)
				writeWrappedHelpLine(a, width, "      ", flag.Help)
				continue
			}
			padding := 28 - helpTextWidth(label)
			if padding < 2 {
				padding = 2
			}
			prefix := "  " + label + strings.Repeat(" ", padding)
			writeWrappedHelpLine(a, width, prefix, flag.Help)
		}
		fmt.Fprintln(a.stdout)
	}
	writeHelpSection(a, width, "Examples", spec.Examples, true)
	writeHelpSection(a, width, "Before", spec.Prerequisites, false)
	writeHelpSection(a, width, "Effects", spec.Effects, false)
	writeHelpSection(a, width, "Safety", spec.Safety, false)
	writeHelpSection(a, width, "Recovery", spec.Recovery, false)
	writeFinalHelpSection(a, width, "Next", spec.Next, true)
}

func writeHelpSection(
	a app,
	width int,
	heading string,
	items []string,
	commands bool,
) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(a.stdout, "%s:\n", heading)
	for _, item := range items {
		if commands {
			fmt.Fprintf(a.stdout, "  %s\n", item)
			continue
		}
		writeWrappedHelpLine(a, width, "  ", item)
	}
	fmt.Fprintln(a.stdout)
}

func writeFinalHelpSection(
	a app,
	width int,
	heading string,
	items []string,
	commands bool,
) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(a.stdout, "%s:\n", heading)
	for _, item := range items {
		if commands {
			fmt.Fprintf(a.stdout, "  %s\n", item)
			continue
		}
		writeWrappedHelpLine(a, width, "  ", item)
	}
}

func (a app) catalogUsage(query string, search bool) int {
	query = strings.TrimSpace(query)
	width := helpTerminalWidth()
	if search {
		fmt.Fprintf(a.stdout, "Hideout help results for %q\n", query)
	} else if query != "" {
		fmt.Fprintf(a.stdout, "Hideout command catalog filtered by %q\n", query)
	} else {
		fmt.Fprintln(a.stdout, "Hideout command catalog")
	}
	fmt.Fprintln(a.stdout, "Usage:")
	fmt.Fprintln(a.stdout, "  hideout help <command>       open purpose, effects, safety, and recovery")
	fmt.Fprintln(a.stdout, "  hideout help search <word>   search by task or keyword")
	fmt.Fprintln(a.stdout, "  hideout help all [query]     browse or filter this catalog")
	fmt.Fprintln(a.stdout, "  Compatibility: hideout help --all is the same as hideout help all")
	fmt.Fprintln(a.stdout)

	entries := make([]commandCatalogEntry, 0)
	for _, entry := range defaultCommandCatalog().entries {
		if entry.spec.Hidden || !commandMatchesHelpQuery(entry.spec, query) {
			continue
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		fmt.Fprintln(a.stdout, "No matching commands.")
		return 0
	}

	wroteStability := false
	for _, stability := range []commandStability{
		commandStabilityStable,
		commandStabilityAdvanced,
		commandStabilityLab,
	} {
		stabilityEntries := filterCatalogByStability(entries, stability)
		if len(stabilityEntries) == 0 {
			continue
		}
		if wroteStability {
			fmt.Fprintln(a.stdout)
		}
		fmt.Fprintln(a.stdout, catalogStabilityHeading(stability))
		for _, group := range helpTaskGroupOrder {
			groupEntries := filterCatalogByTaskGroup(stabilityEntries, group)
			if len(groupEntries) == 0 {
				continue
			}
			fmt.Fprintf(a.stdout, "  %s\n", group)
			for _, entry := range groupEntries {
				writeCatalogIndexEntry(a, width, entry.spec)
			}
		}
		wroteStability = true
	}
	return len(entries)
}

func commandMatchesHelpQuery(spec commandSpec, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	values := []string{
		spec.ID,
		spec.Name,
		strings.Join(spec.Aliases, " "),
		strings.Join(spec.SearchTerms, " "),
		spec.TaskGroup,
		spec.Purpose,
		string(spec.Audience),
		string(spec.Stability),
		strings.Join(spec.Syntax, " "),
		strings.Join(spec.Prerequisites, " "),
		strings.Join(spec.Effects, " "),
		strings.Join(spec.Safety, " "),
		strings.Join(spec.Recovery, " "),
	}
	for _, flag := range spec.Flags {
		values = append(values, flag.Name, flag.Value, flag.Help)
	}
	return strings.Contains(strings.ToLower(strings.Join(values, "\n")), query)
}

func filterCatalogByStability(
	entries []commandCatalogEntry,
	stability commandStability,
) []commandCatalogEntry {
	filtered := make([]commandCatalogEntry, 0)
	for _, entry := range entries {
		if entry.spec.Stability == stability {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func filterCatalogByTaskGroup(
	entries []commandCatalogEntry,
	group string,
) []commandCatalogEntry {
	filtered := make([]commandCatalogEntry, 0)
	for _, entry := range entries {
		if entry.spec.TaskGroup == group {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func catalogStabilityHeading(stability commandStability) string {
	switch stability {
	case commandStabilityStable:
		return "Stable"
	case commandStabilityAdvanced:
		return "Advanced"
	case commandStabilityLab:
		return "Lab (unsupported; explicit opt-in)"
	default:
		return string(stability)
	}
}

func writeCatalogIndexEntry(a app, width int, spec commandSpec) {
	label := "    " + spec.Name
	if helpTextWidth(label) < 22 {
		label += strings.Repeat(" ", 22-helpTextWidth(label))
	} else {
		label += "  "
	}
	writeWrappedHelpLine(a, width, label, spec.Purpose)
}

func helpTerminalWidth() int {
	width, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS")))
	if err != nil || width == 0 {
		return defaultHelpWidth
	}
	if width < 40 {
		return 40
	}
	if width > 120 {
		return 120
	}
	return width
}

func writeWrappedHelpLine(a app, width int, prefix, text string) {
	words := helpWords(text)
	if len(words) == 0 {
		fmt.Fprintln(a.stdout, prefix)
		return
	}
	continuation := strings.Repeat(" ", helpTextWidth(prefix))
	line := prefix
	lineWidth := helpTextWidth(prefix)
	for _, word := range words {
		wordWidth := helpTextWidth(word)
		separator := 0
		if lineWidth > helpTextWidth(prefix) {
			separator = 1
		}
		if lineWidth+separator+wordWidth > width &&
			lineWidth > helpTextWidth(prefix) {
			fmt.Fprintln(a.stdout, line)
			line = continuation + word
			lineWidth = helpTextWidth(continuation) + wordWidth
			continue
		}
		if separator != 0 {
			line += " "
			lineWidth++
		}
		line += word
		lineWidth += wordWidth
	}
	fmt.Fprintln(a.stdout, line)
}

func helpWords(text string) []string {
	fields := strings.Fields(text)
	words := make([]string, 0, len(fields))
	for index := 0; index < len(fields); index++ {
		word := fields[index]
		if strings.Count(word, "`")%2 == 0 {
			words = append(words, word)
			continue
		}
		for index+1 < len(fields) {
			index++
			word += " " + fields[index]
			if strings.Count(word, "`")%2 == 0 {
				break
			}
		}
		words = append(words, word)
	}
	return words
}

func helpTextWidth(value string) int {
	return utf8.RuneCountInString(value)
}

func (a app) setupUsage() {
	a.commandUsage("setup")
}

func (a app) allUsage() {
	a.catalogUsage("", false)
}

func (a app) commandUsage(name string) {
	entry, ok := defaultCommandCatalog().lookup(name)
	if !ok || entry.spec.Hidden {
		fmt.Fprintf(a.stdout, "No help is available for %s.\n", name)
		return
	}
	a.contextualUsage(entry.spec)
}
