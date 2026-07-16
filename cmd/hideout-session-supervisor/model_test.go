package main

import (
	"strings"
	"testing"
)

const testProtocol = "hideout.guest-supervisor/v1"

func validStartSpec() startSpec {
	return startSpec{
		Protocol:       testProtocol,
		SessionID:      "ses_20260716T120000Z_0123456789abcdef",
		TargetUser:     "developer",
		GuestWork:      "/workspace",
		Argv:           []string{"sh", "-c", "printf ok"},
		Env:            []string{"HOME=/hideout/profile/home", "PATH=/usr/bin:/bin", "TERM=xterm-256color"},
		Terminal:       terminalSpec{Mode: "pty", Rows: 24, Columns: 80, Term: "xterm-256color"},
		ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef",
		SessionSource:  "/hideout/runtime/sessions/ses_20260716T120000Z_0123456789abcdef",
	}
}

func TestValidateStartAcceptsStrictPTYAndPipeSpecs(t *testing.T) {
	t.Parallel()
	ptySpec := validStartSpec()
	if err := validateStart(ptySpec, testProtocol); err != nil {
		t.Fatalf("PTY spec: %v", err)
	}
	pipeSpec := validStartSpec()
	pipeSpec.Terminal = terminalSpec{Mode: "none"}
	if err := validateStart(pipeSpec, testProtocol); err != nil {
		t.Fatalf("pipe spec: %v", err)
	}
}

func TestValidateStartRejectsAuthorityAndFramingInputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*startSpec)
	}{
		{"wrong protocol", func(s *startSpec) { s.Protocol = "hideout.session-supervisor/v2" }},
		{"traversing session", func(s *startSpec) { s.SessionID = "ses_../../root" }},
		{"root target", func(s *startSpec) { s.TargetUser = "root" }},
		{"relative workdir", func(s *startSpec) { s.GuestWork = "workspace" }},
		{"unclean workdir", func(s *startSpec) { s.GuestWork = "/workspace/../etc" }},
		{"control workdir", func(s *startSpec) { s.GuestWork = "/workspace/\x1bpayload" }},
		{"wrong source", func(s *startSpec) { s.SessionSource = "/hideout/runtime/sessions/sibling" }},
		{"bad boot", func(s *startSpec) { s.ExpectedBootID = "current" }},
		{"missing argv", func(s *startSpec) { s.Argv = nil }},
		{"argv NUL", func(s *startSpec) { s.Argv = []string{"sh", "bad\x00arg"} }},
		{"bad env", func(s *startSpec) { s.Env = []string{"A-B=value"} }},
		{"duplicate env", func(s *startSpec) { s.Env = []string{"A=1", "A=2"} }},
		{"zero rows", func(s *startSpec) { s.Terminal.Rows = 0 }},
		{"ambient term", func(s *startSpec) { s.Terminal.Term = "xterm;HOST=secret" }},
		{"pipe with PTY fields", func(s *startSpec) { s.Terminal.Mode = "none" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validStartSpec()
			tt.edit(&spec)
			if err := validateStart(spec, testProtocol); err == nil {
				t.Fatal("expected strict validation failure")
			}
		})
	}
}

func TestValidateStartEnforcesAggregateBounds(t *testing.T) {
	t.Parallel()
	spec := validStartSpec()
	spec.Argv = []string{"sh", strings.Repeat("x", maxStartTextBytes)}
	if err := validateStart(spec, testProtocol); err == nil {
		t.Fatal("expected argv byte bound failure")
	}
	spec = validStartSpec()
	spec.Env = []string{"VALUE=" + strings.Repeat("x", maxStartTextBytes)}
	if err := validateStart(spec, testProtocol); err == nil {
		t.Fatal("expected environment byte bound failure")
	}
}

func TestNormalizeSignalUsesClosedPortableCatalog(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{
		"SIGINT": "SIGINT",
		"term":   "SIGTERM",
		" TSTP ": "SIGTSTP",
	} {
		got, err := normalizeSignal(input)
		if err != nil || got != want {
			t.Fatalf("normalizeSignal(%q)=(%q,%v), want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "WINCH", "STOP", "USR1", "SEGV", "INT;touch /tmp/x"} {
		if _, err := normalizeSignal(input); err == nil {
			t.Fatalf("normalizeSignal(%q) unexpectedly succeeded", input)
		}
	}
}
