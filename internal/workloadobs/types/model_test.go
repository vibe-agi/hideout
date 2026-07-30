package types

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestActivityOwnerUsesExactStableBackendIncarnation(t *testing.T) {
	reusable, err := NewReusableOwner("env_fixture", "lima", "machine-incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	same, err := NewReusableOwner("env_fixture", "lima", "machine-incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	otherIncarnation, err := NewReusableOwner("env_fixture", "lima", "machine-incarnation-b")
	if err != nil {
		t.Fatal(err)
	}
	disposable, err := NewDisposableOwner("ses_fixture", "lima", "machine-incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	if !reusable.Equal(same) || reusable.Key() != same.Key() {
		t.Fatalf("same owner was not equal: first=%+v second=%+v", reusable, same)
	}
	if reusable.Equal(otherIncarnation) || reusable.Key() == otherIncarnation.Key() {
		t.Fatal("recreated backend inherited the prior activity owner")
	}
	if reusable.Equal(disposable) || reusable.Key() == disposable.Key() {
		t.Fatal("reusable and disposable owners collapsed")
	}
	if reusable.GuestBootID != "" || disposable.GuestBootID != "" {
		t.Fatal("guest boot identity leaked into stable retention ownership")
	}
}

func TestExecutionIdentitySurvivesPIDReuseAndRequiresGuestIdentity(t *testing.T) {
	owner, err := NewReusableOwner("env_fixture", "lima", "machine-incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	base := ExecutionIdentityInput{
		Owner:              owner,
		SessionID:          "ses_fixture",
		GuestBootID:        "boot-fixture-a",
		ObserverGeneration: 3,
		PID:                4242,
		ExecSequence:       10,
		StartedAtMonoNS:    100,
	}
	first, err := NewExecutionID(base)
	if err != nil {
		t.Fatal(err)
	}
	reused := base
	reused.ExecSequence++
	reused.StartedAtMonoNS++
	second, err := NewExecutionID(reused)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("PID reuse produced the same execution identity")
	}
	rebooted := base
	rebooted.GuestBootID = "boot-fixture-b"
	third, err := NewExecutionID(rebooted)
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("guest reboot produced the same execution identity")
	}
	missingGuest := base
	missingGuest.GuestBootID = ""
	if _, err := NewExecutionID(missingGuest); !errors.Is(err, ErrInvalidExecutionIdentity) {
		t.Fatalf("missing guest identity error=%v want %v", err, ErrInvalidExecutionIdentity)
	}
}

func TestActivityAndCoverageRejectCrossOwnerUse(t *testing.T) {
	owner, err := NewReusableOwner("env_fixture", "lima", "machine-incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewReusableOwner("env_fixture", "lima", "machine-incarnation-b")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	record := ActivityRecord{
		Schema:    ActivityRecordSchema,
		ID:        "act_fixture0001",
		Owner:     owner,
		SessionID: "ses_fixture",
		Kind:      ActivityProcess,
		Operation: "exec",
		Subject: ProcessSubject{
			Kind:          ActivityProcess,
			ExecutionID:   "exec_fixture0001",
			Executable:    "/usr/bin/true",
			Argv:          []string{"true"},
			GuestIdentity: GuestIdentity{UID: 1000, GID: 1000},
		},
		Outcome:         Outcome{Status: OutcomeSucceeded},
		Count:           1,
		FirstAt:         now,
		LastAt:          now,
		FirstSequence:   1,
		LastSequence:    1,
		Attribution:     AttributionExact,
		CoverageID:      "cov_fixture0001",
		RedactionStatus: RedactionPassed,
	}
	if err := record.ValidateForOwner(owner); err != nil {
		t.Fatalf("valid activity rejected: %v", err)
	}
	if err := record.ValidateForOwner(other); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("cross-owner activity error=%v want %v", err, ErrOwnerMismatch)
	}

	coverage := CoverageInterval{
		Schema:              CoverageIntervalSchema,
		ID:                  "cov_fixture0001",
		Owner:               owner,
		SessionID:           "ses_fixture",
		Subsystem:           SubsystemProcess,
		State:               CoverageAvailable,
		Reason:              "observer-ready",
		CollectorGeneration: 1,
		DroppedEventCount:   0,
		RetentionGap:        false,
		StartedAt:           now,
	}
	if err := coverage.ValidateForOwner(owner); err != nil {
		t.Fatalf("valid coverage rejected: %v", err)
	}
	if err := coverage.ValidateForOwner(other); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("cross-owner coverage error=%v want %v", err, ErrOwnerMismatch)
	}
	coverage.DroppedEventCount = 1
	if err := coverage.Validate(); !errors.Is(err, ErrFalseAvailableCoverage) {
		t.Fatalf("lossy Available interval error=%v want %v", err, ErrFalseAvailableCoverage)
	}
}

func TestActivityRecordJSONRoundTripRestoresClosedSubjectUnion(t *testing.T) {
	owner, err := NewReusableOwner("env_fixture", "lima", "machine-incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	subjects := []struct {
		kind    string
		subject any
	}{
		{ActivityProcess, ProcessSubject{
			Kind: ActivityProcess, ExecutionID: "exec_fixture0001",
			Executable: "/usr/bin/true", Argv: []string{"true"},
			GuestIdentity: GuestIdentity{UID: 1000, GID: 1000},
		}},
		{ActivityFile, FileSubject{
			Kind: ActivityFile, Path: "/workspace/file", PathState: "resolved",
			PathClass: "workspace", FileType: "regular",
		}},
		{ActivityConnection, NetworkSubject{
			Kind: ActivityConnection, Protocol: "tcp", IP: "203.0.113.10", Port: 443,
			DomainAttribution: AttributionUnknown, CorrelationReason: "literal-ip",
			Route: "direct", Direction: "egress",
		}},
		{ActivityDNS, DNSSubject{
			Kind: ActivityDNS, Query: "example.test", QueryType: "A",
			Answers: []string{"203.0.113.10"}, ResponseCode: "noerror",
		}},
		{ActivityRisk, GenericSubject{
			Kind: ActivityRisk, Code: "risk-observed", Summary: "risk evidence",
		}},
		{ActivityCoverage, GenericSubject{
			Kind: ActivityCoverage, Code: "coverage-gap", Summary: "coverage evidence",
		}},
	}
	for index, testCase := range subjects {
		t.Run(testCase.kind, func(t *testing.T) {
			record := ActivityRecord{
				Schema: ActivityRecordSchema,
				ID:     "act_fixture000" + string(rune('1'+index)), Owner: owner,
				SessionID: "ses_fixture", Kind: testCase.kind, Operation: "observe",
				Subject: testCase.subject, Outcome: Outcome{Status: OutcomeSucceeded},
				Count: 1, FirstAt: now, LastAt: now,
				FirstSequence: 1, LastSequence: 1,
				Attribution: AttributionExact, CoverageID: "cov_fixture0001",
				RedactionStatus: RedactionPassed,
			}
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			var decoded ActivityRecord
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if reflect.TypeOf(decoded.Subject) != reflect.TypeOf(testCase.subject) {
				t.Fatalf(
					"decoded subject type=%T want=%T",
					decoded.Subject,
					testCase.subject,
				)
			}
			if err := decoded.ValidatePersistable(); err != nil {
				t.Fatalf("decoded record is not persistable: %v", err)
			}
		})
	}
}

func TestFileSubjectRejectsInvalidUTF8Paths(t *testing.T) {
	base := FileSubject{
		Kind: ActivityFile, Path: "/workspace/file",
		PathState: "resolved", PathClass: "workspace", FileType: "regular",
	}
	invalidPath := base
	invalidPath.Path = "/workspace/\xff"
	if err := invalidPath.Validate(); !errors.Is(err, ErrInvalidActivity) {
		t.Fatalf("invalid path UTF-8 error=%v want %v", err, ErrInvalidActivity)
	}
	invalidTarget := base
	invalidTarget.TargetPath = "/workspace/\xff"
	if err := invalidTarget.Validate(); !errors.Is(err, ErrInvalidActivity) {
		t.Fatalf("invalid target UTF-8 error=%v want %v", err, ErrInvalidActivity)
	}

	for name, subject := range map[string]FileSubject{
		"relative resolved": {
			Kind: ActivityFile, Path: "workspace/file",
			PathState: "resolved", PathClass: "workspace", FileType: "regular",
		},
		"empty resolved": {
			Kind:      ActivityFile,
			PathState: "resolved", PathClass: "workspace", FileType: "regular",
		},
		"nonempty unknown": {
			Kind: ActivityFile, Path: "/workspace/file",
			PathState: "unknown", PathClass: "unknown", FileType: "unknown",
		},
		"partial identity": {
			Kind: ActivityFile, Path: "/workspace/file",
			PathState: "resolved", PathClass: "workspace", FileType: "unknown",
			Device: 8,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := subject.Validate(); !errors.Is(err, ErrInvalidActivity) {
				t.Fatalf("error=%v want %v subject=%+v", err, ErrInvalidActivity, subject)
			}
		})
	}
}

func TestNetworkSubjectValidatesLogicalProxyTarget(t *testing.T) {
	base := NetworkSubject{
		Kind:              ActivityConnection,
		Protocol:          "tcp",
		IP:                "127.0.0.1",
		Port:              7890,
		DomainAttribution: AttributionUnknown,
		CorrelationReason: "proxy-target-unavailable",
		Route:             "proxy",
		Direction:         "egress",
		SocketCookie:      700,
	}
	domainTarget := base
	domainTarget.Domain = "proxy.example.test"
	domainTarget.TargetPort = 443
	domainTarget.DomainAttribution = AttributionExact
	domainTarget.CorrelationReason = "validated-proxy-target"
	if err := domainTarget.Validate(); err != nil {
		t.Fatalf("valid proxy domain target rejected: %v", err)
	}
	ipTarget := base
	ipTarget.TargetIP = "203.0.113.70"
	ipTarget.TargetPort = 9443
	ipTarget.CorrelationReason = "validated-proxy-ip-target"
	if err := ipTarget.Validate(); err != nil {
		t.Fatalf("valid proxy IP target rejected: %v", err)
	}

	for name, mutate := range map[string]func(*NetworkSubject){
		"target on direct route": func(subject *NetworkSubject) {
			subject.Route = "direct"
			subject.TargetIP = "203.0.113.70"
			subject.TargetPort = 443
		},
		"domain and target IP": func(subject *NetworkSubject) {
			subject.Domain = "proxy.example.test"
			subject.DomainAttribution = AttributionExact
			subject.TargetIP = "203.0.113.70"
			subject.TargetPort = 443
		},
		"target IP without port": func(subject *NetworkSubject) {
			subject.TargetIP = "203.0.113.70"
		},
		"domain target without port": func(subject *NetworkSubject) {
			subject.Domain = "proxy.example.test"
			subject.DomainAttribution = AttributionExact
		},
		"target port without logical target": func(subject *NetworkSubject) {
			subject.TargetPort = 443
		},
		"malformed target IP": func(subject *NetworkSubject) {
			subject.TargetIP = "not-an-ip"
			subject.TargetPort = 443
		},
	} {
		t.Run(name, func(t *testing.T) {
			subject := base
			mutate(&subject)
			if err := subject.Validate(); !errors.Is(err, ErrInvalidActivity) {
				t.Fatalf(
					"error=%v want %v subject=%+v",
					err,
					ErrInvalidActivity,
					subject,
				)
			}
		})
	}
}
