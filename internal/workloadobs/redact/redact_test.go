package redact

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestRedactorRemovesCredentialCanariesBeforePersistence(t *testing.T) {
	const (
		secret       = "HIDEOUT-CANARY-SECRET-045"
		controlToken = "HIDEOUT-CONTROL-TOKEN-045"
	)
	redactor, err := New(Config{
		KnownSecrets:  [][]byte{[]byte(secret)},
		ControlTokens: []string{controlToken},
	})
	if err != nil {
		t.Fatal(err)
	}
	record := processRecordFixture([]string{
		"agent",
		"--token", secret,
		"--password=not-registered-but-sensitive",
		"https://alice:proxy-pass@example.test/path?ok=1&access_token=" + secret,
		"Authorization: Bearer " + controlToken,
		"/Users/alice/projects/visible-local-path",
	})
	safe, err := redactor.Activity(record)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		secret,
		controlToken,
		"alice:proxy-pass",
		"not-registered-but-sensitive",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("persistable activity retained %q: %s", forbidden, encoded)
		}
	}
	if !strings.Contains(string(encoded), "/Users/alice/projects/visible-local-path") {
		t.Fatalf("ordinary local path was unnecessarily hidden: %s", encoded)
	}
	if safe.RedactionStatus != workloadtypes.RedactionPassed {
		t.Fatalf("redaction status=%q want %q", safe.RedactionStatus, workloadtypes.RedactionPassed)
	}
}

func TestRedactorRemovesExecutionCanariesBeforePersistence(t *testing.T) {
	const secret = "HIDEOUT-EXECUTION-SECRET-045"
	redactor, err := New(Config{KnownSecrets: [][]byte{[]byte(secret)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(redactor.Clear)
	now := time.Date(2026, 7, 29, 4, 30, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner(
		"env_fixture",
		"lima",
		"machine-incarnation-execution",
	)
	if err != nil {
		t.Fatal(err)
	}
	identity := workloadtypes.ExecutionIdentityInput{
		Owner: owner, SessionID: "ses_execution_fixture",
		GuestBootID: "boot-execution-fixture", ObserverGeneration: 1,
		PID: 42, ExecSequence: 7, StartedAtMonoNS: uint64(now.UnixNano()),
	}
	executionID, err := workloadtypes.NewExecutionID(identity)
	if err != nil {
		t.Fatal(err)
	}
	execution := workloadtypes.Execution{
		Schema: workloadtypes.ExecutionSchema, ID: executionID,
		Owner: owner, SessionID: identity.SessionID,
		GuestBootID: identity.GuestBootID, ObserverGeneration: 1,
		PID: identity.PID, TID: identity.PID,
		ExecSequence:    identity.ExecSequence,
		StartedAtMonoNS: identity.StartedAtMonoNS, StartedAt: now,
		Executable: "/usr/bin/agent",
		Argv: []string{
			"agent", "--token", secret,
			"socks5://alice:password@example.test:7890",
			"/Users/alice/projects/visible-local-path",
		},
		Cwd: "/Users/alice/projects/visible-local-path",
		Identity: workloadtypes.GuestIdentity{
			UID: 1000, GID: 1000, User: "developer",
		},
		Limitations: []string{},
	}
	safe, err := redactor.Execution(execution)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secret, "alice:password"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("persistable execution retained %q: %s", forbidden, encoded)
		}
	}
	if safe.ID != execution.ID ||
		!strings.Contains(
			string(encoded),
			"/Users/alice/projects/visible-local-path",
		) {
		t.Fatalf("execution identity/path changed unexpectedly: %s", encoded)
	}
}

func TestRedactorHandlesSplitEqualsQueryAndAuthorizationForms(t *testing.T) {
	redactor, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	input := []string{
		"tool",
		"--api-key", "split-value",
		"--password=equals-value",
		"https://example.test/?token=query-value&safe=yes",
		"authorization=Bearer field-value",
	}
	output, truncation, err := redactor.Argv(input)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(output, "\n")
	for _, forbidden := range []string{"split-value", "equals-value", "query-value", "field-value"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("argv retained %q: %q", forbidden, joined)
		}
	}
	if len(truncation) != 0 || strings.Count(joined, Replacement) < 4 {
		t.Fatalf("unexpected redaction result: argv=%q truncation=%v", joined, truncation)
	}
}

func TestRedactorFailsPrivateForTruncatedURIUserinfo(t *testing.T) {
	redactor, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	record := processRecordFixture([]string{
		"agent",
		"socks5://uri-user-canary:password-prefix-canary",
		"socks5://uri-user-canary:12345",
	})
	record.Truncation = []string{"argv-truncated"}

	safe, err := redactor.Activity(record)
	if err != nil {
		t.Fatal(err)
	}
	subject := safe.Subject.(workloadtypes.ProcessSubject)
	joined := strings.Join(subject.Argv, "\n")
	for _, forbidden := range []string{
		"uri-user-canary",
		"password-prefix-canary",
		"12345",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("truncated URI retained %q: %q", forbidden, joined)
		}
	}
	if strings.Count(joined, Replacement) != 2 {
		t.Fatalf("truncated URI replacements=%q", joined)
	}

	ordinary, _, err := redactor.Argv([]string{
		"client",
		"socks5://127.0.0.1:7890",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ordinary[1] != "socks5://127.0.0.1:7890" {
		t.Fatalf("complete endpoint was unnecessarily changed: %q", ordinary)
	}

	malformed, _, err := redactor.Argv([]string{
		"client",
		"socks5://uri-user-canary:password-prefix-canary",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(malformed, "\n"), "uri-user-canary") {
		t.Fatalf("malformed URI userinfo survived: %q", malformed)
	}
}

func TestRedactorCoversFileTargetPathBeforePersistence(t *testing.T) {
	const secret = "FILE-TARGET-SECRET-045"
	redactor, err := New(Config{KnownSecrets: [][]byte{[]byte(secret)}})
	if err != nil {
		t.Fatal(err)
	}
	record := processRecordFixture(nil)
	record.Kind = workloadtypes.ActivityFile
	record.Operation = "rename"
	record.Subject = workloadtypes.FileSubject{
		Kind: workloadtypes.ActivityFile,
		Path: "/workspace/source.txt",
		TargetPath: "/workspace/https://alice:password@example.test/" +
			secret,
		PathState: "resolved", PathClass: "workspace", FileType: "regular",
	}
	safe, err := redactor.Activity(record)
	if err != nil {
		t.Fatal(err)
	}
	subject := safe.Subject.(workloadtypes.FileSubject)
	if subject.Path != "/workspace/source.txt" ||
		strings.Contains(subject.TargetPath, secret) ||
		strings.Contains(subject.TargetPath, "alice:password") ||
		!strings.Contains(subject.TargetPath, "example.test") {
		t.Fatalf("file target-path redaction=%+v", subject)
	}
}

func TestRedactorTruncatesBeforeScanningAndFailsClosed(t *testing.T) {
	const secret = "TAIL-CANARY-MUST-NOT-SURVIVE"
	redactor, err := New(Config{
		KnownSecrets:   [][]byte{[]byte(secret)},
		MaxValueBytes:  32,
		MaxOutputBytes: 256,
	})
	if err != nil {
		t.Fatal(err)
	}
	output, truncation, err := redactor.Argv([]string{"tool", strings.Repeat("x", 128) + secret})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(output, ""), secret) || len(truncation) == 0 {
		t.Fatalf("truncation leaked tail canary: output=%q truncation=%v", output, truncation)
	}

	failClosed, err := New(Config{MaxOutputBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := failClosed.Argv([]string{"tool", "ordinary-value"}); !errors.Is(err, ErrRedactionFailed) {
		t.Fatalf("oversize redaction error=%v want %v", err, ErrRedactionFailed)
	}
	if _, err := New(Config{KnownSecrets: [][]byte{{}}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty secret error=%v want %v", err, ErrInvalidConfig)
	}
}

func processRecordFixture(argv []string) workloadtypes.ActivityRecord {
	now := time.Date(2026, 7, 29, 4, 0, 0, 0, time.UTC)
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "machine-incarnation-a")
	if err != nil {
		panic(err)
	}
	return workloadtypes.ActivityRecord{
		Schema:    workloadtypes.ActivityRecordSchema,
		ID:        "act_fixture0001",
		Owner:     owner,
		SessionID: "ses_fixture",
		Kind:      workloadtypes.ActivityProcess,
		Operation: "exec",
		Subject: workloadtypes.ProcessSubject{
			Kind:          workloadtypes.ActivityProcess,
			ExecutionID:   "exec_fixture0001",
			Executable:    "/usr/bin/agent",
			Argv:          argv,
			Cwd:           "/Users/alice/projects/visible-local-path",
			GuestIdentity: workloadtypes.GuestIdentity{UID: 1000, GID: 1000},
		},
		Outcome:         workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:           1,
		FirstAt:         now,
		LastAt:          now,
		FirstSequence:   1,
		LastSequence:    1,
		Attribution:     workloadtypes.AttributionExact,
		CoverageID:      "cov_fixture0001",
		RedactionStatus: workloadtypes.RedactionPending,
	}
}
