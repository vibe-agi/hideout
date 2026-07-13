package hostcap

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestExecDarwinIdentityCommandBoundsDelegatedHelpers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := execDarwinIdentityCommand(ctx, "/bin/sh", "-c", "sleep 30 & wait"); err == nil {
		t.Fatal("timed-out identity command unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("identity command retained a delegated output pipe for %s", elapsed)
	}
}

func TestExecDarwinIdentityCommandBoundsOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := execDarwinIdentityCommand(ctx, "/bin/sh", "-c", "dd if=/dev/zero bs=1024 count=300 2>/dev/null")
	if err == nil || !strings.Contains(err.Error(), "output exceeded") {
		t.Fatalf("oversized identity output error=%v", err)
	}
	if len(output) != maxDarwinIdentityCommandOutput {
		t.Fatalf("bounded identity output bytes=%d, want %d", len(output), maxDarwinIdentityCommandOutput)
	}
}

func TestDarwinIdentityProductionBudgetAllowsGatekeeperSerialization(t *testing.T) {
	if darwinIdentityCommandTimeout < 30*time.Second {
		t.Fatalf("production identity timeout=%s, want at least 30s for serialized Gatekeeper assessment", darwinIdentityCommandTimeout)
	}
}

func TestObserveDarwinSigningIdentityRequiresSystemTrustAssessment(t *testing.T) {
	type call struct {
		executable string
		args       []string
	}
	var calls []call
	runner := func(_ context.Context, executable string, args ...string) ([]byte, error) {
		calls = append(calls, call{executable: executable, args: append([]string(nil), args...)})
		switch {
		case executable == "/usr/bin/codesign" && len(args) > 0 && args[0] == "--verify":
			return nil, nil
		case executable == "/usr/bin/codesign" && len(args) > 0 && args[0] == "--display":
			return []byte("Identifier=com.example.Test\nTeamIdentifier=TEAM123456\nCDHash=00112233445566778899aabbccddeeff00112233\n"), nil
		case executable == "/usr/sbin/spctl":
			return []byte("accepted"), nil
		default:
			return nil, errors.New("unexpected command")
		}
	}
	facts, err := observeDarwinSigningIdentity("/Applications/Test.app", runner)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Signed || !facts.Trusted || facts.TrustAnchor != "macos-system-policy" || facts.TeamID != "TEAM123456" || facts.BundleID != "com.example.Test" || facts.CodeIdentity == "" {
		t.Fatalf("trusted observed facts are incomplete: %+v", facts)
	}
	want := []call{
		{executable: "/usr/bin/codesign", args: []string{"--verify", "--strict", "--all-architectures", "/Applications/Test.app"}},
		{executable: "/usr/bin/codesign", args: []string{"--display", "--verbose=4", "/Applications/Test.app"}},
		{executable: "/usr/sbin/spctl", args: []string{"--assess", "--type", "execute", "--verbose=4", "/Applications/Test.app"}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("identity commands=%+v want %+v", calls, want)
	}
}

func TestObserveDarwinSigningIdentityUsesIndependentPerStepBudgets(t *testing.T) {
	runner := func(ctx context.Context, executable string, args ...string) ([]byte, error) {
		select {
		case <-time.After(45 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		switch {
		case executable == "/usr/bin/codesign" && args[0] == "--verify":
			return nil, nil
		case executable == "/usr/bin/codesign" && args[0] == "--display":
			return []byte("Identifier=com.example.Test\nTeamIdentifier=TEAM123456\nCDHash=00112233445566778899aabbccddeeff00112233\n"), nil
		case executable == "/usr/sbin/spctl":
			return []byte("accepted"), nil
		default:
			return nil, errors.New("unexpected command")
		}
	}
	facts, err := observeDarwinSigningIdentityWithTimeout("/Applications/Test.app", runner, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("independent command budgets were consumed cumulatively: %v", err)
	}
	if !facts.Trusted {
		t.Fatalf("trust observation did not complete: %+v", facts)
	}
}

func TestObserveDarwinSigningIdentityReportsTrustTimeout(t *testing.T) {
	runner := func(ctx context.Context, executable string, args ...string) ([]byte, error) {
		if executable == "/usr/bin/codesign" && args[0] == "--verify" {
			return nil, nil
		}
		if executable == "/usr/bin/codesign" && args[0] == "--display" {
			return []byte("Identifier=com.example.Test\nTeamIdentifier=TEAM123456\nCDHash=00112233445566778899aabbccddeeff00112233\n"), nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	_, err := observeDarwinSigningIdentityWithTimeout("/Applications/Test.app", runner, 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "trust assessment timed out") {
		t.Fatalf("trust timeout was not surfaced: %v", err)
	}
}

func TestObserveDarwinSigningIdentityClassifiesUntrustedChainAsUnverifiedFacts(t *testing.T) {
	runner := func(_ context.Context, executable string, args ...string) ([]byte, error) {
		if executable == "/usr/bin/codesign" && args[0] == "--verify" {
			return nil, nil
		}
		if executable == "/usr/bin/codesign" && args[0] == "--display" {
			return []byte("Identifier=com.example.Local\nTeamIdentifier=LOCALTEAM1\nCDHash=abcdef\n"), nil
		}
		return []byte("rejected"), errors.New("system policy rejected")
	}
	facts, err := observeDarwinSigningIdentity("/Applications/Local.app", runner)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Signed || facts.Trusted || facts.TrustAnchor != "" || facts.TeamID != "LOCALTEAM1" {
		t.Fatalf("untrusted chain was promoted or lost observed facts: %+v", facts)
	}
}

func TestIdentityExpectationsOnlyNarrowObservedSigningFacts(t *testing.T) {
	observed := SigningObservation{
		Signed:       true,
		BundleID:     "com.example.Real",
		TeamID:       "REALTEAM01",
		CodeIdentity: "real-code-identity",
	}
	if err := ValidateSigningExpectations(observed, IdentityExpectations{BundleID: observed.BundleID, TeamID: observed.TeamID}); err != nil {
		t.Fatal(err)
	}
	for _, expectation := range []IdentityExpectations{
		{BundleID: "com.package.Forged"},
		{TeamID: "FORGEDTEAM"},
	} {
		if err := ValidateSigningExpectations(observed, expectation); err == nil {
			t.Fatalf("package expectation must not override Core observation: %+v", expectation)
		}
	}
}

func TestUnsignedObservationCannotBeAuthenticatedByPackageExpectation(t *testing.T) {
	unsigned := SigningObservation{}
	if err := ValidateSigningExpectations(unsigned, IdentityExpectations{BundleID: "com.package.ClaimsSigned", TeamID: "PACKAGE123"}); err == nil {
		t.Fatal("package signing fields must not authenticate an unsigned app")
	}
}

func TestParseDarwinSigningFactsUsesObservedOutputOnly(t *testing.T) {
	facts, err := parseDarwinSigningFacts(`Executable=/Applications/Test.app/Contents/MacOS/Test
Identifier=com.example.Test
TeamIdentifier=TEAM123456
CDHash=00112233445566778899aabbccddeeff00112233
`)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Signed || facts.BundleID != "com.example.Test" || facts.TeamID != "TEAM123456" || facts.CodeIdentity != "00112233445566778899aabbccddeeff00112233" {
		t.Fatalf("unexpected observed facts: %+v", facts)
	}
}

func TestParseDarwinAdHocSigningFactsRemainUnverified(t *testing.T) {
	facts, err := parseDarwinSigningFacts(`Identifier=com.example.Local
TeamIdentifier=not set
CDHash=00112233445566778899aabbccddeeff00112233
`)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Signed || facts.TeamID != "" || facts.BundleID != "com.example.Local" {
		t.Fatalf("ad-hoc signing facts should remain observed but unverifiable: %+v", facts)
	}
}

func TestResolveDarwinAdHocAppUsesExactBundleDigest(t *testing.T) {
	root := t.TempDir()
	makeTestBundle(t, root, "Test App.app", "adhoc")
	opts := testIdentityOptions(root)
	opts.ObserveSigning = func(string) (SigningObservation, error) {
		return SigningObservation{Signed: true, BundleID: "com.example.TestApp", CodeIdentity: "adhoc-cdhash"}, nil
	}
	expectation := testIdentityExpectation()
	expectation.ExpectedTeamID = ""
	got, err := ResolveApplicationIdentity(expectation, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got.Verification != AppVerificationUnverified || got.ContentDigest == "" {
		t.Fatalf("ad-hoc app should be unverified and digest-bound: %+v", got)
	}
}
