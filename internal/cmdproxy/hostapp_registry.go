package cmdproxy

import (
	"fmt"
	"sort"
	"strings"
)

const (
	ReservedCommandClassControl = "control"
	ReservedCommandClassHelper  = "helper"
	ReservedCommandClassShell   = "shell"

	MaxProjectedHostAppCommands = 64
)

// ReservedHostAppCommand is a Core-owned command name that a host-app pack
// cannot claim or replace. The catalog protects Hideout's control plane and
// the guest command-dispatch primitives that must remain independently usable.
type ReservedHostAppCommand struct {
	Name   string `json:"name"`
	Class  string `json:"class"`
	Reason string `json:"reason"`
}

// HostAppCommandOwner is the deterministic owner of one projected command
// symbol. Owner is a stable qualified binding identity, not display text.
type HostAppCommandOwner struct {
	Command string `json:"command"`
	Owner   string `json:"owner"`
}

// HostAppOwnerReplacement records the operator's exact conflict decision.
// Both owner identities are required so stale plans fail instead of replacing
// whichever owner happens to be current at apply time.
type HostAppOwnerReplacement struct {
	Command   string `json:"command"`
	FromOwner string `json:"fromOwner"`
	ToOwner   string `json:"toOwner"`
}

// HostAppCommandPlan is sorted by command and independent of installation or
// map iteration order.
type HostAppCommandPlan struct {
	Owners       []HostAppCommandOwner     `json:"owners"`
	Replacements []HostAppOwnerReplacement `json:"replacements,omitempty"`
}

var reservedHostAppCommandCatalog = []ReservedHostAppCommand{
	{Name: "bash", Class: ReservedCommandClassShell, Reason: "login and command execution shell"},
	{Name: "dash", Class: ReservedCommandClassShell, Reason: "system command execution shell"},
	{Name: "env", Class: ReservedCommandClassShell, Reason: "environment dispatch primitive"},
	{Name: "fish", Class: ReservedCommandClassShell, Reason: "login and command execution shell"},
	{Name: "hideout", Class: ReservedCommandClassControl, Reason: "Hideout operator control command"},
	{Name: "hideout-dns-stub", Class: ReservedCommandClassHelper, Reason: "Hideout DNS mediation helper"},
	{Name: "hideout-hostfsd", Class: ReservedCommandClassHelper, Reason: "Hideout HostFS broker helper"},
	{Name: "hideout-schema-validate", Class: ReservedCommandClassHelper, Reason: "Hideout contract validation helper"},
	{Name: "hideout-shim", Class: ReservedCommandClassHelper, Reason: "Hideout projected-command dispatcher"},
	{Name: "open", Class: ReservedCommandClassControl, Reason: "Core-owned host.open projection"},
	{Name: "sh", Class: ReservedCommandClassShell, Reason: "POSIX command execution shell"},
	{Name: "su", Class: ReservedCommandClassShell, Reason: "guest identity transition command"},
	{Name: "sudo", Class: ReservedCommandClassShell, Reason: "guest privilege transition command"},
	{Name: "xdg-open", Class: ReservedCommandClassControl, Reason: "Core-owned host.open compatibility projection"},
	{Name: "zsh", Class: ReservedCommandClassShell, Reason: "login and command execution shell"},
}

// ReservedHostAppCommands returns a sorted copy of the Core-owned catalog.
func ReservedHostAppCommands() []ReservedHostAppCommand {
	out := append([]ReservedHostAppCommand(nil), reservedHostAppCommandCatalog...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LookupReservedHostAppCommand performs an exact, case-sensitive lookup.
func LookupReservedHostAppCommand(name string) (ReservedHostAppCommand, bool) {
	if name == "" || strings.TrimSpace(name) != name {
		return ReservedHostAppCommand{}, false
	}
	commands := ReservedHostAppCommands()
	i := sort.Search(len(commands), func(i int) bool { return commands[i].Name >= name })
	if i == len(commands) || commands[i].Name != name {
		return ReservedHostAppCommand{}, false
	}
	return commands[i], true
}

// PlanHostAppCommandOwners merges requested binding ownership into the current
// profile command catalog. Reserved commands always fail. A non-reserved
// collision succeeds only with an exact, otherwise-unused replacement record;
// installation order never selects the winner.
func PlanHostAppCommandOwners(current, requested []HostAppCommandOwner, replacements []HostAppOwnerReplacement) (HostAppCommandPlan, error) {
	currentByCommand, err := normalizeHostAppOwners("existing", current)
	if err != nil {
		return HostAppCommandPlan{}, err
	}
	requestedByCommand, err := normalizeHostAppOwners("requested", requested)
	if err != nil {
		return HostAppCommandPlan{}, err
	}

	replacementByCommand := make(map[string]HostAppOwnerReplacement, len(replacements))
	for _, replacement := range replacements {
		replacement, err = normalizeHostAppReplacement(replacement)
		if err != nil {
			return HostAppCommandPlan{}, err
		}
		if _, exists := replacementByCommand[replacement.Command]; exists {
			return HostAppCommandPlan{}, fmt.Errorf("host-app command %q has duplicate owner replacements", replacement.Command)
		}
		replacementByCommand[replacement.Command] = replacement
	}

	result := make(map[string]string, len(currentByCommand)+len(requestedByCommand))
	for command, owner := range currentByCommand {
		result[command] = owner
	}
	usedReplacements := map[string]bool{}
	for _, command := range sortedHostAppOwnerCommands(requestedByCommand) {
		requestedOwner := requestedByCommand[command]
		if reserved, ok := LookupReservedHostAppCommand(command); ok {
			return HostAppCommandPlan{}, fmt.Errorf("host-app command %q is reserved by Core: %s", command, reserved.Reason)
		}
		currentOwner, exists := currentByCommand[command]
		if !exists || currentOwner == requestedOwner {
			result[command] = requestedOwner
			continue
		}
		replacement, ok := replacementByCommand[command]
		if !ok {
			return HostAppCommandPlan{}, fmt.Errorf("host-app command %q is owned by %q; explicit owner replacement to %q is required", command, currentOwner, requestedOwner)
		}
		if replacement.FromOwner != currentOwner || replacement.ToOwner != requestedOwner {
			return HostAppCommandPlan{}, fmt.Errorf("host-app command %q replacement is stale: current=%q requested=%q replacement=%q->%q", command, currentOwner, requestedOwner, replacement.FromOwner, replacement.ToOwner)
		}
		usedReplacements[command] = true
		result[command] = requestedOwner
	}
	for _, command := range sortedHostAppReplacementCommands(replacementByCommand) {
		if !usedReplacements[command] {
			return HostAppCommandPlan{}, fmt.Errorf("host-app command %q owner replacement does not match an active conflict", command)
		}
	}
	if len(result) > MaxProjectedHostAppCommands {
		return HostAppCommandPlan{}, fmt.Errorf("host-app profile projects %d commands, exceeding limit %d", len(result), MaxProjectedHostAppCommands)
	}

	plan := HostAppCommandPlan{}
	for _, command := range sortedHostAppOwnerCommands(result) {
		plan.Owners = append(plan.Owners, HostAppCommandOwner{Command: command, Owner: result[command]})
		if usedReplacements[command] {
			plan.Replacements = append(plan.Replacements, replacementByCommand[command])
		}
	}
	return plan, nil
}

func normalizeHostAppOwners(kind string, owners []HostAppCommandOwner) (map[string]string, error) {
	out := make(map[string]string, len(owners))
	for _, item := range owners {
		command, err := normalizeRegistryName("host-app "+kind+" command", item.Command)
		if err != nil {
			return nil, err
		}
		owner, err := normalizeHostAppOwner(item.Owner)
		if err != nil {
			return nil, fmt.Errorf("host-app command %q: %w", command, err)
		}
		if existing, ok := out[command]; ok && existing != owner {
			owners := []string{existing, owner}
			sort.Strings(owners)
			return nil, fmt.Errorf("host-app command %q has multiple %s owners %q and %q", command, kind, owners[0], owners[1])
		}
		out[command] = owner
	}
	return out, nil
}

func normalizeHostAppReplacement(replacement HostAppOwnerReplacement) (HostAppOwnerReplacement, error) {
	command, err := normalizeRegistryName("host-app replacement command", replacement.Command)
	if err != nil {
		return HostAppOwnerReplacement{}, err
	}
	from, err := normalizeHostAppOwner(replacement.FromOwner)
	if err != nil {
		return HostAppOwnerReplacement{}, fmt.Errorf("host-app command %q replacement from owner: %w", command, err)
	}
	to, err := normalizeHostAppOwner(replacement.ToOwner)
	if err != nil {
		return HostAppOwnerReplacement{}, fmt.Errorf("host-app command %q replacement to owner: %w", command, err)
	}
	if from == to {
		return HostAppOwnerReplacement{}, fmt.Errorf("host-app command %q replacement owners must differ", command)
	}
	return HostAppOwnerReplacement{Command: command, FromOwner: from, ToOwner: to}, nil
}

func normalizeHostAppOwner(owner string) (string, error) {
	trimmed := strings.TrimSpace(owner)
	if trimmed == "" {
		return "", fmt.Errorf("owner is required")
	}
	if trimmed != owner {
		return "", fmt.Errorf("owner %q must not contain surrounding whitespace", owner)
	}
	if strings.ContainsAny(owner, "\r\n\x00") {
		return "", fmt.Errorf("owner contains control characters")
	}
	return owner, nil
}

func sortedHostAppOwnerCommands(owners map[string]string) []string {
	commands := make([]string, 0, len(owners))
	for command := range owners {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	return commands
}

func sortedHostAppReplacementCommands(replacements map[string]HostAppOwnerReplacement) []string {
	commands := make([]string, 0, len(replacements))
	for command := range replacements {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	return commands
}
