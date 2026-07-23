package main

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxSessionIDBytes = 128
	maxGuestPathBytes = 4096
	maxArgCount       = 4096
	maxEnvCount       = 4096
	maxStartTextBytes = 1 << 20
	maxTermBytes      = 64
)

var (
	sessionIDPattern  = regexp.MustCompile(`^ses_[A-Za-z0-9_]+$`)
	targetUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	envNamePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	bootIDPattern     = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
	termPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
)

type terminalSpec struct {
	Mode    string
	Rows    uint16
	Columns uint16
	Term    string
}

type startSpec struct {
	Protocol            string
	SessionID           string
	TargetUser          string
	GuestWork           string
	Argv                []string
	Env                 []string
	Terminal            terminalSpec
	ExpectedBootID      string
	SessionSource       string
	ProjectionReadiness *projectionReadinessSpec
}

type targetCompletion struct {
	Kind      string
	ExitCode  int
	Signal    string
	Completed bool
}

func validateStart(spec startSpec, expectedProtocol string) error {
	if expectedProtocol == "" || spec.Protocol != expectedProtocol {
		return fmt.Errorf("unsupported protocol %q", spec.Protocol)
	}
	if len(spec.SessionID) > maxSessionIDBytes || !sessionIDPattern.MatchString(spec.SessionID) {
		return fmt.Errorf("invalid session id %q", spec.SessionID)
	}
	if !targetUserPattern.MatchString(spec.TargetUser) || spec.TargetUser == "root" {
		return fmt.Errorf("invalid non-root target user %q", spec.TargetUser)
	}
	if len(spec.GuestWork) == 0 || len(spec.GuestWork) > maxGuestPathBytes || !path.IsAbs(spec.GuestWork) || path.Clean(spec.GuestWork) != spec.GuestWork || hasUnsafePathText(spec.GuestWork) {
		return fmt.Errorf("invalid guest workdir %q", spec.GuestWork)
	}
	wantSource := "/hideout/runtime/sessions/" + spec.SessionID
	if spec.SessionSource != wantSource {
		return errors.New("session source does not match the fixed session runtime child")
	}
	if spec.ProjectionReadiness != nil {
		if err := spec.ProjectionReadiness.validate(); err != nil {
			return err
		}
	}
	if !bootIDPattern.MatchString(spec.ExpectedBootID) {
		return errors.New("expected guest boot identity is invalid")
	}
	if err := validateArgv(spec.Argv); err != nil {
		return err
	}
	if err := validateEnv(spec.Env); err != nil {
		return err
	}
	return validateTerminal(spec.Terminal)
}

func hasUnsafePathText(value string) bool {
	if !utf8.ValidString(value) {
		return true
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '\u001b' {
			return true
		}
	}
	return false
}

func validateArgv(argv []string) error {
	if len(argv) == 0 || len(argv) > maxArgCount || argv[0] == "" {
		return errors.New("target argv is empty or exceeds the argument count bound")
	}
	total := 0
	for _, arg := range argv {
		if strings.ContainsRune(arg, 0) {
			return errors.New("target argv contains NUL")
		}
		total += len(arg)
		if total > maxStartTextBytes {
			return errors.New("target argv exceeds the byte bound")
		}
	}
	return nil
}

func validateEnv(env []string) error {
	if len(env) > maxEnvCount {
		return errors.New("target environment exceeds the assignment count bound")
	}
	seen := make(map[string]struct{}, len(env))
	total := 0
	for _, assignment := range env {
		if strings.ContainsRune(assignment, 0) {
			return errors.New("target environment contains NUL")
		}
		name, _, ok := strings.Cut(assignment, "=")
		if !ok || !envNamePattern.MatchString(name) {
			return errors.New("target environment contains an invalid assignment")
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("target environment contains duplicate %q", name)
		}
		seen[name] = struct{}{}
		total += len(assignment)
		if total > maxStartTextBytes {
			return errors.New("target environment exceeds the byte bound")
		}
	}
	return nil
}

func validateTerminal(terminal terminalSpec) error {
	switch terminal.Mode {
	case "none":
		if terminal.Rows != 0 || terminal.Columns != 0 || terminal.Term != "" {
			return errors.New("non-PTY terminal descriptor contains PTY fields")
		}
	case "pty":
		if terminal.Rows == 0 || terminal.Columns == 0 {
			return errors.New("PTY dimensions must be non-zero")
		}
		if len(terminal.Term) > maxTermBytes || !termPattern.MatchString(terminal.Term) {
			return errors.New("PTY TERM is invalid")
		}
	default:
		return fmt.Errorf("unsupported terminal mode %q", terminal.Mode)
	}
	return nil
}

func normalizeSignal(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "SIG")
	switch value {
	case "HUP", "INT", "QUIT", "TERM", "TSTP", "CONT", "KILL":
		return "SIG" + value, nil
	default:
		return "", fmt.Errorf("unsupported target signal %q", value)
	}
}
