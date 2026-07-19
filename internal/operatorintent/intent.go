package operatorintent

import (
	"errors"
	"fmt"
	"strings"
)

type Kind string

const (
	KindSetup   Kind = "setup"
	KindRun     Kind = "run"
	KindShow    Kind = "show"
	KindOpen    Kind = "open"
	KindConnect Kind = "connect"
	KindAccess  Kind = "access"
	KindRequest Kind = "request"
	KindStop    Kind = "stop"
	KindRemove  Kind = "remove"
)

type Intent interface {
	Kind() Kind
}

type Setup struct{}

func (Setup) Kind() Kind { return KindSetup }

type Run struct {
	Argv []string
}

func (Run) Kind() Kind { return KindRun }

type ShowTopic string

const (
	ShowStatus     ShowTopic = "status"
	ShowActivity   ShowTopic = "activity"
	ShowRequests   ShowTopic = "requests"
	ShowAccess     ShowTopic = "access"
	ShowConnection ShowTopic = "connection"
)

type Show struct {
	Topic       ShowTopic
	ProfileName string
}

func (Show) Kind() Kind { return KindShow }

type OpenSurface string

const (
	OpenConsole OpenSurface = "console"
	OpenWeb     OpenSurface = "web"
)

type Open struct {
	Surface OpenSurface
}

func (Open) Kind() Kind { return KindOpen }

type ConnectionKind string

const (
	ConnectionDirect ConnectionKind = "direct"
	ConnectionProxy  ConnectionKind = "proxy"
)

type Connect struct {
	Connection  ConnectionKind
	ProxyName   string
	Resolver    string
	ProfileName string
}

func (Connect) Kind() Kind { return KindConnect }

type AccessEffect string

const (
	AccessAllow AccessEffect = "allow"
	AccessDeny  AccessEffect = "deny"
)

type AccessOperation string

const (
	AccessRead  AccessOperation = "read"
	AccessWrite AccessOperation = "write"
	AccessAll   AccessOperation = "all"
)

type AccessScope string

const (
	ScopeAsk     AccessScope = "ask"
	ScopeOnce    AccessScope = "once"
	ScopeProject AccessScope = "project"
	ScopeProfile AccessScope = "profile"
)

type Access struct {
	Effect      AccessEffect
	Operation   AccessOperation
	Path        string
	Scope       AccessScope
	ProfileName string
}

func (Access) Kind() Kind { return KindAccess }

type RequestAction string

const (
	RequestApprove RequestAction = "approve"
	RequestDeny    RequestAction = "deny"
	RequestReopen  RequestAction = "reopen"
)

type Request struct {
	Action RequestAction
	ID     string
}

func (Request) Kind() Kind { return KindRequest }

type Stop struct {
	WhenIdle bool
}

func (Stop) Kind() Kind { return KindStop }

type Remove struct {
	Object string
	Name   string
}

func (Remove) Kind() Kind { return KindRemove }

// Parse accepts a deliberately small, deterministic language. It only creates
// typed intent; Manager remains responsible for planning, authorization, and
// application. Unknown words never fall back to a generic command or host
// effect.
func Parse(args []string) (Intent, error) {
	if len(args) == 0 {
		return nil, errors.New("an operator command is required")
	}
	switch args[0] {
	case "setup":
		if len(args) != 1 {
			return nil, errors.New("usage: hideout setup")
		}
		return Setup{}, nil
	case "run":
		if len(args) < 2 {
			return nil, errors.New("usage: hideout run <command> [args...]")
		}
		return Run{Argv: append([]string(nil), args[1:]...)}, nil
	case "show":
		return parseShow(args[1:])
	case "open":
		return parseOpen(args[1:])
	case "connect":
		return parseConnect(args[1:])
	case "allow":
		return parseAccess(AccessAllow, args[1:])
	case "deny":
		if len(args) > 1 && args[1] == "request" {
			return parseRequest(RequestDeny, args[2:])
		}
		return parseAccess(AccessDeny, args[1:])
	case "approve":
		return parseRequestPhrase(RequestApprove, args[1:])
	case "reopen":
		return parseRequestPhrase(RequestReopen, args[1:])
	case "stop":
		if len(args) == 1 {
			return Stop{}, nil
		}
		if len(args) == 3 && args[1] == "when" && args[2] == "idle" {
			return Stop{WhenIdle: true}, nil
		}
		return nil, errors.New("usage: hideout stop [when idle]")
	case "remove":
		if len(args) != 3 || args[1] != "vm" || !validName(args[2]) {
			return nil, errors.New("usage: hideout remove vm <name>")
		}
		return Remove{Object: "vm", Name: args[2]}, nil
	default:
		return nil, fmt.Errorf("unknown operator verb %q", args[0])
	}
}

func parseShow(args []string) (Intent, error) {
	if len(args) != 1 && len(args) != 4 {
		return nil, errors.New("usage: hideout show status|activity|requests|access|connection [for profile <name>]")
	}
	topic := ShowTopic(args[0])
	switch topic {
	case ShowStatus, ShowActivity, ShowRequests, ShowAccess, ShowConnection:
	default:
		return nil, fmt.Errorf("unknown show topic %q", args[0])
	}
	profileName, err := parseProfileScope(args[1:])
	if err != nil {
		return nil, err
	}
	return Show{Topic: topic, ProfileName: profileName}, nil
}

func parseOpen(args []string) (Intent, error) {
	if len(args) != 1 {
		return nil, errors.New("usage: hideout open console|web")
	}
	surface := OpenSurface(args[0])
	switch surface {
	case OpenConsole, OpenWeb:
		return Open{Surface: surface}, nil
	default:
		return nil, fmt.Errorf("unknown open surface %q", args[0])
	}
}

func parseConnect(args []string) (Intent, error) {
	if len(args) >= 1 && args[0] == "directly" {
		profileName, err := parseProfileScope(args[1:])
		if err != nil {
			return nil, err
		}
		return Connect{Connection: ConnectionDirect, ProfileName: profileName}, nil
	}
	if len(args) >= 2 && args[0] == "through" && validName(args[1]) {
		intent := Connect{Connection: ConnectionProxy, ProxyName: args[1]}
		tail := args[2:]
		if len(tail) >= 2 && tail[0] == "using" {
			intent.Resolver = strings.TrimSpace(tail[1])
			if intent.Resolver == "" || strings.HasPrefix(intent.Resolver, "-") {
				return nil, errors.New("resolver is required after using")
			}
			tail = tail[2:]
		}
		profileName, err := parseProfileScope(tail)
		if err != nil {
			return nil, err
		}
		intent.ProfileName = profileName
		return intent, nil
	}
	return nil, errors.New("usage: hideout connect directly|through <proxy-secret> [using <resolver>] [for profile <name>]")
}

func parseProfileScope(args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	if len(args) == 3 && args[0] == "for" && args[1] == "profile" && validName(args[2]) {
		return args[2], nil
	}
	return "", errors.New("profile scope must be 'for profile <name>'")
}

func parseAccess(effect AccessEffect, args []string) (Intent, error) {
	if len(args) < 2 {
		return nil, errors.New("usage: hideout allow|deny read|write|all <path> [--once|--for-this-project|--for-profile <name>]")
	}
	operation := AccessOperation(args[0])
	switch operation {
	case AccessRead, AccessWrite, AccessAll:
	default:
		return nil, fmt.Errorf("unknown access operation %q", args[0])
	}
	path := strings.TrimSpace(args[1])
	if path == "" || strings.HasPrefix(path, "-") {
		return nil, errors.New("access path is required before scope options")
	}
	intent := Access{Effect: effect, Operation: operation, Path: path, Scope: ScopeAsk}
	switch len(args) {
	case 2:
		return intent, nil
	case 3:
		switch args[2] {
		case "--once":
			intent.Scope = ScopeOnce
		case "--for-this-project":
			intent.Scope = ScopeProject
		default:
			return nil, fmt.Errorf("unknown access scope %q", args[2])
		}
		return intent, nil
	case 4:
		if args[2] != "--for-profile" || !validName(args[3]) {
			return nil, errors.New("--for-profile requires a profile name")
		}
		intent.Scope = ScopeProfile
		intent.ProfileName = args[3]
		return intent, nil
	default:
		return nil, errors.New("only one access scope may be selected")
	}
}

func parseRequestPhrase(action RequestAction, args []string) (Intent, error) {
	if len(args) != 2 || args[0] != "request" {
		return nil, fmt.Errorf("usage: hideout %s request <id>", action)
	}
	return parseRequest(action, args[1:])
}

func parseRequest(action RequestAction, args []string) (Intent, error) {
	if len(args) != 1 || !validName(args[0]) {
		return nil, fmt.Errorf("usage: hideout %s request <id>", action)
	}
	return Request{Action: action, ID: args[0]}, nil
}

func validName(value string) bool {
	if value == "" || len(value) > 128 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}
