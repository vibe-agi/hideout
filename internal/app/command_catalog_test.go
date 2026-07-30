package app

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/daemon"
)

func TestCommandCatalogCoversEveryTopLevelRoute(t *testing.T) {
	catalog := defaultCommandCatalog()
	if err := validateCommandCatalog(catalog); err != nil {
		t.Fatalf("default catalog is invalid: %v", err)
	}

	want := []string{
		daemon.InternalDaemonServeCommand,
		"activity",
		"adapter-pack",
		"allow",
		"app",
		"audit",
		"clean",
		"cleanup",
		"connect",
		"daemon",
		"decision",
		"deny",
		"doctor",
		"env",
		"explain",
		"help",
		"hostfs",
		"hostfsd",
		"init",
		"lab",
		"notice",
		"package",
		"profile",
		"run",
		"runtime",
		"secret",
		"session",
		"setup",
		"shim",
		"show",
		"stop",
		"support",
		"tui",
		"ui",
		"version",
	}
	got := make([]string, 0, len(catalog.entries))
	for _, entry := range catalog.entries {
		if entry.handler == nil {
			t.Errorf("route %q has no dispatch adapter", entry.spec.Name)
		}
		got = append(got, entry.spec.Name)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("top-level route coverage differs\n got: %v\nwant: %v", got, want)
	}
}

func TestCommandCatalogMetadataIsCompleteAndSearchable(t *testing.T) {
	catalog := defaultCommandCatalog()
	seenAudience := map[commandAudience]bool{}
	seenStability := map[commandStability]bool{}

	for _, entry := range catalog.entries {
		spec := entry.spec
		if spec.Hidden {
			continue
		}
		for field, value := range map[string]string{
			"id":            spec.ID,
			"name":          spec.Name,
			"task group":    spec.TaskGroup,
			"purpose":       spec.Purpose,
			"prerequisites": strings.Join(spec.Prerequisites, "\n"),
			"effects":       strings.Join(spec.Effects, "\n"),
			"safety":        strings.Join(spec.Safety, "\n"),
			"recovery":      strings.Join(spec.Recovery, "\n"),
			"next":          strings.Join(spec.Next, "\n"),
		} {
			if strings.TrimSpace(value) == "" {
				t.Errorf("%q has empty %s metadata", spec.Name, field)
			}
		}
		if len(spec.Syntax) == 0 || len(spec.Examples) == 0 {
			t.Errorf("%q must have syntax and examples", spec.Name)
		}
		seenAudience[spec.Audience] = true
		seenStability[spec.Stability] = true

		flagNames := map[string]struct{}{}
		for _, flag := range spec.Flags {
			if strings.TrimSpace(flag.Name) == "" || strings.TrimSpace(flag.Help) == "" {
				t.Errorf("%q has an incomplete flag entry: %#v", spec.Name, flag)
			}
			if _, exists := flagNames[flag.Name]; exists {
				t.Errorf("%q declares flag %q more than once", spec.Name, flag.Name)
			}
			flagNames[flag.Name] = struct{}{}
		}
	}

	for _, audience := range []commandAudience{
		commandAudienceNewUser,
		commandAudienceOperator,
		commandAudienceDeveloper,
	} {
		if !seenAudience[audience] {
			t.Errorf("catalog has no %q audience entry", audience)
		}
	}
	for _, stability := range []commandStability{
		commandStabilityStable,
		commandStabilityAdvanced,
		commandStabilityLab,
	} {
		if !seenStability[stability] {
			t.Errorf("catalog has no %q stability entry", stability)
		}
	}
}

func TestCommandCatalogAliasesResolveToCanonicalRoutes(t *testing.T) {
	catalog := defaultCommandCatalog()
	for token, want := range map[string]string{
		"-h":        "help",
		"--help":    "help",
		"-v":        "version",
		"--version": "version",
	} {
		entry, ok := catalog.lookup(token)
		if !ok {
			t.Fatalf("alias %q did not resolve", token)
		}
		if entry.spec.Name != want {
			t.Fatalf("alias %q resolved to %q, want %q", token, entry.spec.Name, want)
		}
	}
}

func TestOperatorHelpProjectionIsValidVisibleAndDetached(t *testing.T) {
	projection := defaultOperatorHelpCatalog()
	if err := projection.Validate(); err != nil {
		t.Fatalf("operator help projection is invalid: %v", err)
	}
	if len(projection.Commands) == 0 {
		t.Fatal("operator help projection is empty")
	}
	for _, command := range projection.Commands {
		if command.Hidden || command.Stability == commandStabilityInternal {
			t.Fatalf("public help projection exposed internal command: %+v", command)
		}
	}
	projection.Commands[0].Purpose = "mutated by consumer"
	fresh := defaultOperatorHelpCatalog()
	if fresh.Commands[0].Purpose == "mutated by consumer" {
		t.Fatal("operator help projection aliases catalog storage")
	}
}

func TestCommandCatalogValidationRejectsStaleOrAmbiguousEntries(t *testing.T) {
	tests := map[string]func(*commandCatalog){
		"missing handler": func(catalog *commandCatalog) {
			catalog.entries[0].handler = nil
		},
		"duplicate id": func(catalog *commandCatalog) {
			catalog.entries[1].spec.ID = catalog.entries[0].spec.ID
		},
		"duplicate route": func(catalog *commandCatalog) {
			catalog.entries[1].spec.Name = catalog.entries[0].spec.Name
		},
		"alias shadows route": func(catalog *commandCatalog) {
			catalog.entries[0].spec.Aliases = append(
				catalog.entries[0].spec.Aliases,
				catalog.entries[1].spec.Name,
			)
		},
		"unknown audience": func(catalog *commandCatalog) {
			catalog.entries[1].spec.Audience = commandAudience("mystery")
		},
		"unknown stability": func(catalog *commandCatalog) {
			catalog.entries[1].spec.Stability = commandStability("mystery")
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			catalog := defaultCommandCatalog()
			mutate(&catalog)
			if err := validateCommandCatalog(catalog); err == nil {
				t.Fatal("mutated catalog unexpectedly validated")
			}
		})
	}
}

func TestRunWithCatalogDispatchesCanonicalRouteAndAlias(t *testing.T) {
	var calls [][]string
	handler := func(_ app, args []string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	catalog := commandCatalog{entries: []commandCatalogEntry{{
		spec: commandSpec{
			ID: "probe", Name: "probe", Aliases: []string{"p"},
			TaskGroup: "Test", Purpose: "Exercise catalog dispatch.",
			Syntax: []string{"hideout probe"}, Examples: []string{"hideout probe"},
			Prerequisites: []string{"test"}, Effects: []string{"test"},
			Safety: []string{"test"}, Recovery: []string{"test"}, Next: []string{"hideout probe"},
			Audience: commandAudienceDeveloper, Stability: commandStabilityAdvanced,
		},
		handler: handler,
	}}}
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}

	if err := a.runWithCatalog([]string{"probe", "one"}, catalog); err != nil {
		t.Fatalf("canonical dispatch failed: %v", err)
	}
	if err := a.runWithCatalog([]string{"p", "two"}, catalog); err != nil {
		t.Fatalf("alias dispatch failed: %v", err)
	}
	if len(calls) != 2 ||
		!slices.Equal(calls[0], []string{"one"}) ||
		!slices.Equal(calls[1], []string{"two"}) {
		t.Fatalf("dispatch args = %#v", calls)
	}
}
