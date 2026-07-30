package operatorhelp

import (
	"errors"
	"fmt"
	"strings"
)

const CatalogSchema = "hideout.operator-help.v1"

type Audience string

const (
	AudienceNewUser   Audience = "new-user"
	AudienceOperator  Audience = "operator"
	AudienceDeveloper Audience = "developer"
)

type Stability string

const (
	StabilityStable   Stability = "stable"
	StabilityAdvanced Stability = "advanced"
	StabilityLab      Stability = "lab"
	StabilityInternal Stability = "internal"
)

type Flag struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
	Help  string `json:"help"`
}

// Command is the render-neutral, non-authoritative help projection shared by
// the CLI, terminal HUD, and loopback browser console. It intentionally carries
// no dispatch function or Manager authority.
type Command struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Aliases       []string  `json:"aliases"`
	SearchTerms   []string  `json:"searchTerms"`
	TaskGroup     string    `json:"taskGroup"`
	Purpose       string    `json:"purpose"`
	Syntax        []string  `json:"syntax"`
	Flags         []Flag    `json:"flags"`
	Examples      []string  `json:"examples"`
	Prerequisites []string  `json:"prerequisites"`
	Effects       []string  `json:"effects"`
	Safety        []string  `json:"safety"`
	Recovery      []string  `json:"recovery"`
	Next          []string  `json:"next"`
	Audience      Audience  `json:"audience"`
	Stability     Stability `json:"stability"`
	Hidden        bool      `json:"hidden,omitempty"`
}

type Catalog struct {
	Schema   string    `json:"schema"`
	Commands []Command `json:"commands"`
}

func (catalog Catalog) Validate() error {
	if catalog.Schema != CatalogSchema {
		return fmt.Errorf("unsupported operator help schema %q", catalog.Schema)
	}
	if catalog.Commands == nil {
		return errors.New("operator help commands must be a non-nil list")
	}
	ids := make(map[string]struct{}, len(catalog.Commands))
	tokens := make(map[string]string, len(catalog.Commands))
	for index, command := range catalog.Commands {
		label := fmt.Sprintf("operator help command %d", index)
		if strings.TrimSpace(command.ID) == "" ||
			strings.TrimSpace(command.Name) == "" {
			return fmt.Errorf("%s has no identity", label)
		}
		if _, exists := ids[command.ID]; exists {
			return fmt.Errorf("%s duplicates id %q", label, command.ID)
		}
		ids[command.ID] = struct{}{}
		for _, token := range append(
			[]string{command.Name},
			command.Aliases...,
		) {
			if strings.TrimSpace(token) == "" {
				return fmt.Errorf("%s has a blank route token", label)
			}
			if prior, exists := tokens[token]; exists {
				return fmt.Errorf(
					"%s token %q is already owned by %q",
					label,
					token,
					prior,
				)
			}
			tokens[token] = command.Name
		}
		switch command.Audience {
		case AudienceNewUser, AudienceOperator, AudienceDeveloper:
		default:
			return fmt.Errorf(
				"%s has invalid audience %q",
				label,
				command.Audience,
			)
		}
		switch command.Stability {
		case StabilityStable, StabilityAdvanced, StabilityLab,
			StabilityInternal:
		default:
			return fmt.Errorf(
				"%s has invalid stability %q",
				label,
				command.Stability,
			)
		}
		if command.Hidden {
			if command.Stability != StabilityInternal {
				return fmt.Errorf("%s hidden command is not internal", label)
			}
			continue
		}
		if strings.TrimSpace(command.TaskGroup) == "" ||
			strings.TrimSpace(command.Purpose) == "" {
			return fmt.Errorf("%s has incomplete task metadata", label)
		}
		for name, values := range map[string][]string{
			"syntax":        command.Syntax,
			"examples":      command.Examples,
			"prerequisites": command.Prerequisites,
			"effects":       command.Effects,
			"safety":        command.Safety,
			"recovery":      command.Recovery,
			"next":          command.Next,
		} {
			if len(values) == 0 {
				return fmt.Errorf("%s has no %s", label, name)
			}
			for _, value := range values {
				if strings.TrimSpace(value) == "" {
					return fmt.Errorf("%s has a blank %s item", label, name)
				}
			}
		}
	}
	return nil
}

func (catalog Catalog) Lookup(idOrName string) (Command, bool) {
	for _, command := range catalog.Commands {
		if command.ID == idOrName || command.Name == idOrName {
			return cloneCommand(command), true
		}
		for _, alias := range command.Aliases {
			if alias == idOrName {
				return cloneCommand(command), true
			}
		}
	}
	return Command{}, false
}

func (catalog Catalog) Clone() Catalog {
	clone := Catalog{
		Schema:   catalog.Schema,
		Commands: make([]Command, len(catalog.Commands)),
	}
	for index, command := range catalog.Commands {
		clone.Commands[index] = cloneCommand(command)
	}
	return clone
}

func cloneCommand(command Command) Command {
	command.Aliases = append([]string(nil), command.Aliases...)
	command.SearchTerms = append([]string(nil), command.SearchTerms...)
	command.Syntax = append([]string(nil), command.Syntax...)
	command.Flags = append([]Flag(nil), command.Flags...)
	command.Examples = append([]string(nil), command.Examples...)
	command.Prerequisites = append([]string(nil), command.Prerequisites...)
	command.Effects = append([]string(nil), command.Effects...)
	command.Safety = append([]string(nil), command.Safety...)
	command.Recovery = append([]string(nil), command.Recovery...)
	command.Next = append([]string(nil), command.Next...)
	return command
}
