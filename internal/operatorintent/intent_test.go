package operatorintent

import (
	"reflect"
	"testing"
)

func TestParseNaturalOperatorIntents(t *testing.T) {
	tests := []struct {
		args []string
		want Intent
	}{
		{[]string{"setup"}, Setup{}},
		{[]string{"run", "git", "status", "--short"}, Run{Argv: []string{"git", "status", "--short"}}},
		{[]string{"show", "status"}, Show{Topic: ShowStatus}},
		{[]string{"show", "connection", "for", "profile", "work"}, Show{Topic: ShowConnection, ProfileName: "work"}},
		{[]string{"open", "web"}, Open{Surface: OpenWeb}},
		{[]string{"connect", "directly"}, Connect{Connection: ConnectionDirect}},
		{[]string{"connect", "through", "charles"}, Connect{Connection: ConnectionProxy, ProxyName: "charles"}},
		{[]string{"connect", "through", "charles", "using", "1.1.1.1"}, Connect{Connection: ConnectionProxy, ProxyName: "charles", Resolver: "1.1.1.1"}},
		{[]string{"connect", "through", "charles", "using", "1.1.1.1", "for", "profile", "work"}, Connect{Connection: ConnectionProxy, ProxyName: "charles", Resolver: "1.1.1.1", ProfileName: "work"}},
		{[]string{"allow", "read", "/Users/alice/spec.md"}, Access{Effect: AccessAllow, Operation: AccessRead, Path: "/Users/alice/spec.md", Scope: ScopeProfile}},
		{[]string{"allow", "read", "/Users/alice/spec.md", "--for-this-project"}, Access{Effect: AccessAllow, Operation: AccessRead, Path: "/Users/alice/spec.md", Scope: ScopeProject}},
		{[]string{"deny", "all", "/Users/alice/.ssh", "--for-profile", "work"}, Access{Effect: AccessDeny, Operation: AccessAll, Path: "/Users/alice/.ssh", Scope: ScopeProfile, ProfileName: "work"}},
		{[]string{"approve", "request", "dec_123"}, Request{Action: RequestApprove, ID: "dec_123"}},
		{[]string{"deny", "request", "dec_123"}, Request{Action: RequestDeny, ID: "dec_123"}},
		{[]string{"stop", "when", "idle"}, Stop{WhenIdle: true}},
		{[]string{"remove", "vm", "work"}, Remove{Object: "vm", Name: "work"}},
	}
	for _, tt := range tests {
		t.Run(joinName(tt.args), func(t *testing.T) {
			got, err := Parse(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("intent=%#v want %#v", got, tt.want)
			}
		})
	}
}

func TestParseRejectsAmbiguousOrAuthorityBearingFallbacks(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"deny"},
		{"connect", "charles"},
		{"connect", "through", "../../evil"},
		{"connect", "through", "charles", "using"},
		{"connect", "through", "charles", "for", "profile", "../../evil"},
		{"show", "connection", "for", "work"},
		{"allow", "execute", "/tmp/tool"},
		{"allow", "read", "--for-this-project"},
		{"allow", "read", "/tmp/a", "--once", "--for-this-project"},
		{"approve", "dec_123"},
		{"deny", "request", "../../bad"},
		{"host", "exec", "osascript"},
		{"remove", "everything", "now"},
	} {
		t.Run(joinName(args), func(t *testing.T) {
			if got, err := Parse(args); err == nil {
				t.Fatalf("Parse(%q)=%#v, want rejection", args, got)
			}
		})
	}
}

func joinName(args []string) string {
	if len(args) == 0 {
		return "empty"
	}
	out := args[0]
	for _, value := range args[1:] {
		out += "_" + value
	}
	return out
}

func TestParseHostAppTrustGrammar(t *testing.T) {
	cases := []struct {
		args []string
		want Intent
		err  bool
	}{
		{[]string{"allow", "host-app", "code"}, HostAppTrust{Effect: AccessAllow, Command: "code"}, false},
		{[]string{"deny", "host-app", "code"}, HostAppTrust{Effect: AccessDeny, Command: "code"}, false},
		{[]string{"allow", "host-app", "code", "--for-profile", "work"}, HostAppTrust{Effect: AccessAllow, Command: "code", ProfileName: "work"}, false},
		{[]string{"deny", "host-app", "code", "--for-profile", "work"}, HostAppTrust{Effect: AccessDeny, Command: "code", ProfileName: "work"}, false},
		{[]string{"allow", "host-app"}, nil, true},
		{[]string{"allow", "host-app", "code", "extra"}, nil, true},
		{[]string{"allow", "host-app", "code", "--for-profile"}, nil, true},
		{[]string{"allow", "host-app", "/some/path"}, nil, true},
	}
	for _, tc := range cases {
		got, err := Parse(tc.args)
		if tc.err {
			if err == nil {
				t.Fatalf("Parse(%v) = %v, want error", tc.args, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("Parse(%v): %v", tc.args, err)
		}
		if got != tc.want {
			t.Fatalf("Parse(%v) = %+v, want %+v", tc.args, got, tc.want)
		}
	}
}
