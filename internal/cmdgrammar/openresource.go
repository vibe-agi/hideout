package cmdgrammar

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vibe-agi/hideout/internal/hostcap"
)

const (
	GrammarOpenResourceV1        = "open-resource-v1"
	UnknownFlagsDeny             = "deny"
	MaxOpenResourceArguments     = 64
	MaxOpenResourceArgumentBytes = 4_096
	MaxOpenResourceIntentBytes   = 16_384
	MaxOpenResourceLocation      = 2_147_483_647
	maxOpenResourceFlagAliases   = 16
	maxOpenResourceFlagBytes     = 64
)

// OpenResourceGrammar is bounded package data compiled into an immutable
// command binding. It describes syntax only and carries no app or authority.
type OpenResourceGrammar struct {
	Kind             string
	ResourceCount    int
	GotoFlags        []string
	NewWindowFlags   []string
	ReuseWindowFlags []string
	UnknownFlags     string
}

type UnboundResourceRef struct {
	GuestPath string `json:"guestPath"`
}

// DecodeUnboundOpenResourceIntent is the boundary decoder for grammar output.
// It rejects every field not represented by the authority-free v2 model;
// relative/audit paths are derived later by the authoritative resolver.
func DecodeUnboundOpenResourceIntent(raw []byte) (UnboundOpenResourceIntent, error) {
	if len(raw) == 0 || len(raw) > MaxOpenResourceIntentBytes {
		return UnboundOpenResourceIntent{}, &hostcap.Error{Code: hostcap.CodeIntentInvalid, Reason: "unbound open-resource intent exceeds its bound"}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var intent UnboundOpenResourceIntent
	if err := decoder.Decode(&intent); err != nil {
		return UnboundOpenResourceIntent{}, &hostcap.Error{Code: hostcap.CodeIntentInvalid, Reason: "unbound open-resource intent is invalid"}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return UnboundOpenResourceIntent{}, &hostcap.Error{Code: hostcap.CodeIntentInvalid, Reason: "unbound open-resource intent must be one JSON document"}
	}
	if err := intent.Validate(); err != nil {
		return UnboundOpenResourceIntent{}, err
	}
	return intent, nil
}

// UnboundOpenResourceIntent is deliberately incapable of selecting an app,
// binding, capability, host path, launch mode, result channel, or raw argv.
// Core derives those facts from the immutable registration after strict decode.
type UnboundOpenResourceIntent struct {
	Resources  []UnboundResourceRef `json:"resources"`
	Location   *hostcap.Location    `json:"location,omitempty"`
	WindowMode hostcap.WindowMode   `json:"windowMode,omitempty"`
}

func ParseOpenResource(grammar OpenResourceGrammar, argv []string, guestCWD string) (UnboundOpenResourceIntent, error) {
	if err := ValidateOpenResourceGrammar(grammar); err != nil {
		return UnboundOpenResourceIntent{}, &hostcap.Error{Code: hostcap.CodeFlagUnrecognized, Reason: "open-resource grammar is invalid"}
	}
	if len(argv) == 0 || len(argv)-1 > MaxOpenResourceArguments {
		return UnboundOpenResourceIntent{}, flagError("open-resource argv exceeds its bound")
	}
	for _, arg := range argv {
		if len(arg) > MaxOpenResourceArgumentBytes || strings.ContainsRune(arg, '\x00') {
			return UnboundOpenResourceIntent{}, flagError("open-resource argument exceeds its bound")
		}
	}

	intent := UnboundOpenResourceIntent{WindowMode: hostcap.WindowReuse}
	args := argv[1:]
	var positionals []string
	var gotoTarget string
	var haveGoto bool
	var windowModeSet bool

	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--":
			positionals = append(positionals, args[index+1:]...)
			index = len(args)
		case slices.Contains(grammar.NewWindowFlags, arg):
			if windowModeSet && intent.WindowMode != hostcap.WindowNew {
				return UnboundOpenResourceIntent{}, flagError("new-window and reuse-window flags conflict")
			}
			intent.WindowMode = hostcap.WindowNew
			windowModeSet = true
		case slices.Contains(grammar.ReuseWindowFlags, arg):
			if windowModeSet && intent.WindowMode != hostcap.WindowReuse {
				return UnboundOpenResourceIntent{}, flagError("new-window and reuse-window flags conflict")
			}
			intent.WindowMode = hostcap.WindowReuse
			windowModeSet = true
		case slices.Contains(grammar.GotoFlags, arg):
			if haveGoto || index+1 >= len(args) {
				return UnboundOpenResourceIntent{}, flagError("goto requires exactly one file:line[:column] value")
			}
			gotoTarget = args[index+1]
			haveGoto = true
			index++
		case gotoAssignment(grammar.GotoFlags, arg) != "":
			if haveGoto {
				return UnboundOpenResourceIntent{}, flagError("only one goto target is supported")
			}
			gotoTarget = gotoAssignment(grammar.GotoFlags, arg)
			haveGoto = true
		case strings.HasPrefix(arg, "-"):
			return UnboundOpenResourceIntent{}, flagError("unrecognized open-resource flag")
		default:
			positionals = append(positionals, arg)
		}
	}

	var target string
	if haveGoto {
		if len(positionals) != 0 {
			return UnboundOpenResourceIntent{}, flagError("one resource cannot combine goto and a positional target")
		}
		path, location, err := parseGoto(gotoTarget)
		if err != nil {
			return UnboundOpenResourceIntent{}, err
		}
		target, intent.Location = path, location
	} else {
		if len(positionals) != grammar.ResourceCount {
			return UnboundOpenResourceIntent{}, flagError("open-resource-v1 requires exactly one resource")
		}
		target = positionals[0]
	}
	guestPath, err := absGuestPath(target, guestCWD)
	if err != nil {
		return UnboundOpenResourceIntent{}, err
	}
	intent.Resources = []UnboundResourceRef{{GuestPath: guestPath}}
	if err := intent.Validate(); err != nil {
		return UnboundOpenResourceIntent{}, err
	}
	return intent, nil
}

func ValidateOpenResourceGrammar(grammar OpenResourceGrammar) error {
	if grammar.Kind != GrammarOpenResourceV1 || grammar.ResourceCount != 1 || grammar.UnknownFlags != UnknownFlagsDeny {
		return errors.New("cmdgrammar: unsupported open-resource grammar contract")
	}
	all := [][]string{grammar.GotoFlags, grammar.NewWindowFlags, grammar.ReuseWindowFlags}
	seen := map[string]bool{}
	for _, aliases := range all {
		if len(aliases) > maxOpenResourceFlagAliases {
			return errors.New("cmdgrammar: too many flag aliases")
		}
		for _, alias := range aliases {
			if alias == "" || alias == "-" || alias == "--" || len(alias) > maxOpenResourceFlagBytes || !strings.HasPrefix(alias, "-") || strings.ContainsAny(alias, "=\x00 \t\r\n") || seen[alias] {
				return errors.New("cmdgrammar: invalid or duplicate flag alias")
			}
			seen[alias] = true
		}
	}
	return nil
}

func (intent UnboundOpenResourceIntent) Validate() error {
	if len(intent.Resources) != 1 {
		return &hostcap.Error{Code: hostcap.CodeIntentInvalid, Reason: "open-resource-v1 requires exactly one resource"}
	}
	resource := intent.Resources[0]
	if !strings.HasPrefix(resource.GuestPath, "/") || filepath.Clean(resource.GuestPath) != resource.GuestPath || strings.Contains(resource.GuestPath, "://") || strings.ContainsRune(resource.GuestPath, '\x00') || len(resource.GuestPath) > MaxOpenResourceArgumentBytes {
		return &hostcap.Error{Code: hostcap.CodeIntentInvalid, Reason: "unbound resource guest path is invalid"}
	}
	if intent.Location != nil && (intent.Location.Line < 1 || intent.Location.Line > MaxOpenResourceLocation || intent.Location.Column < 1 || intent.Location.Column > MaxOpenResourceLocation) {
		return &hostcap.Error{Code: hostcap.CodeIntentInvalid, Reason: "location must be positive"}
	}
	if intent.WindowMode != "" && intent.WindowMode != hostcap.WindowReuse && intent.WindowMode != hostcap.WindowNew {
		return &hostcap.Error{Code: hostcap.CodeIntentInvalid, Reason: "window mode is invalid"}
	}
	return nil
}

func gotoAssignment(flags []string, arg string) string {
	for _, flag := range flags {
		prefix := flag + "="
		if strings.HasPrefix(arg, prefix) && len(arg) > len(prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
	}
	return ""
}

func flagError(reason string) error {
	return &hostcap.Error{Code: hostcap.CodeFlagUnrecognized, Reason: reason}
}

func (grammar OpenResourceGrammar) String() string {
	return fmt.Sprintf("%s/%d", grammar.Kind, grammar.ResourceCount)
}
