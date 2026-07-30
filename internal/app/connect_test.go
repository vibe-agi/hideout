package app

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestConnectPlanAndApplyUseExactReviewedOperation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := profile.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(profile.Default("default")); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Main([]string{
		"connect", "plan",
		"--profile", "default",
		"--through", "local-proxy",
		"--dns", "1.1.1.1",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("connect plan exit=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{
		"Canonical connection review:",
		"Diff:",
		"Effects:",
		"Rollback:",
		"No state has changed.",
		"hideout connect apply op_",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("connect plan missing %q:\n%s", want, stdout.String())
		}
	}
	before, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if before.Network.Mode != profile.NetworkModeDirect {
		t.Fatalf("planning changed profile network: %+v", before.Network)
	}
	match := regexp.MustCompile(
		`hideout connect apply (op_[A-Za-z0-9_-]+) --yes`,
	).FindStringSubmatch(stdout.String())
	if len(match) != 2 {
		t.Fatalf("connect plan omitted exact apply command:\n%s", stdout.String())
	}
	operationID := match[1]

	stdout.Reset()
	stderr.Reset()
	code = Main([]string{
		"connect", "apply", operationID, "--yes",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("connect apply exit=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{
		"Operation: " + operationID,
		"Confirmation: accepted by explicit --yes.",
		"Desired:",
		"Updated:",
		"Evidence: operation " + operationID,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("connect apply missing %q:\n%s", want, stdout.String())
		}
	}
	after, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if after.Network.Mode != profile.NetworkModeTun2Socks ||
		after.Network.ProxySecretRef != "local-proxy" ||
		after.Network.MediatedResolver != "1.1.1.1" {
		t.Fatalf("exact reviewed operation was not applied: %+v", after.Network)
	}
	operation, err := (manager.OperationStore{
		Root: store.Root,
	}).Load(operationID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Phase != manager.OperationSucceeded {
		t.Fatalf("operation phase=%s", operation.Phase)
	}
}

func TestConnectWithoutTTYOrYesLeavesReviewedPlanUnapplied(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := profile.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Main([]string{
		"connect", "through", "local-proxy", "using", "1.1.1.1",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("unconfirmed non-TTY connect succeeded:\n%s", stdout.String())
	}
	match := regexp.MustCompile(
		`hideout connect apply (op_[A-Za-z0-9_-]+) --yes`,
	).FindStringSubmatch(stdout.String())
	if len(match) != 2 {
		t.Fatalf("unconfirmed connect omitted exact operation:\n%s", stdout.String())
	}
	for _, want := range []string{
		"code: configuration.confirmation-required",
		"no configuration state was changed",
		match[1],
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("confirmation guidance missing %q:\n%s", want, stderr.String())
		}
	}
	current, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if current.Network.Mode != profile.NetworkModeDirect {
		t.Fatalf("unconfirmed plan changed profile: %+v", current.Network)
	}
	operation, err := (manager.OperationStore{
		Root: store.Root,
	}).Load(match[1])
	if err != nil {
		t.Fatal(err)
	}
	if operation.Phase != manager.OperationPlanned {
		t.Fatalf("unconfirmed operation phase=%s", operation.Phase)
	}
}

func TestConnectApplyFailureKeepsExactOperationRecoveryIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := profile.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	core := manager.New(store)
	service := manager.NewProfileTransactionService(core)
	authority := &failingConfigurationCommandAuthority{
		delegate: localConfigurationCommandAuthority{service: service},
		applyErr: errors.New("simulated response loss"),
	}
	var stdout bytes.Buffer
	application := app{
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		stdin:  strings.NewReader(""),
		terminalInteractive: func() bool {
			return false
		},
		configurationAuthority: func(manager.Core) configurationCommandAuthority {
			return authority
		},
	}
	err = application.connectCommand([]string{
		"through", "local-proxy", "using", "1.1.1.1", "--yes",
	})
	var outcome *configurationApplyOutcomeError
	if !errors.As(err, &outcome) {
		t.Fatalf("connect error=%T %v", err, err)
	}
	if outcome.operationID == "" ||
		outcome.operationID != authority.plan.OperationID {
		t.Fatalf(
			"outcome operation=%q planned=%q",
			outcome.operationID,
			authority.plan.OperationID,
		)
	}
	guidance := guidanceForError(err, defaultCommandCatalog())
	if guidance.Code != "operation.outcome-unknown" ||
		!strings.Contains(guidance.Reason, outcome.operationID) {
		t.Fatalf("outcome guidance=%+v", guidance)
	}
	combined := strings.Join(
		append(
			append([]string{guidance.Reason}, guidance.Next...),
			guidance.Notes...,
		),
		"\n",
	)
	for _, forbidden := range []string{
		"retry the command",
		"apply again",
	} {
		if strings.Contains(strings.ToLower(combined), forbidden) {
			t.Fatalf("unsafe response-loss guidance:\n%s", combined)
		}
	}
	current, loadErr := store.Load("default")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if current.Network.Mode != profile.NetworkModeTun2Socks ||
		current.Network.ProxySecretRef != "local-proxy" {
		t.Fatalf(
			"simulated accepted response loss did not commit once: %+v",
			current.Network,
		)
	}

	stdout.Reset()
	replay := app{
		stdout: &stdout,
		stderr: &bytes.Buffer{},
		stdin:  strings.NewReader(""),
	}
	if err := replay.connectCommand([]string{
		"apply", outcome.operationID, "--yes",
	}); err != nil {
		t.Fatalf("exact terminal replay: %v", err)
	}
	for _, want := range []string{
		"Exact operation replay:",
		"Operation: " + outcome.operationID,
		"Terminal phase: succeeded",
		"No new plan or mutation was created",
		"Current Desired connection:",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("terminal replay missing %q:\n%s", want, stdout.String())
		}
	}
	replayed, loadErr := (manager.OperationStore{
		Root: store.Root,
	}).Load(outcome.operationID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if replayed.Phase != manager.OperationSucceeded {
		t.Fatalf("terminal replay phase=%s", replayed.Phase)
	}
}

func TestConnectRecoveryGuidanceKeepsExactOperationIdentity(t *testing.T) {
	const operationID = "op_recovery123456"
	err := configurationApplyFailure(
		operationID,
		manager.ErrConfigurationRecoveryRequired,
	)
	var recovery *configurationRecoveryRequiredError
	if !errors.As(err, &recovery) ||
		recovery.operationID != operationID {
		t.Fatalf("recovery error=%T %v", err, err)
	}
	guidance := guidanceForError(err, defaultCommandCatalog())
	if guidance.Code != "operation.recovery-required" ||
		!strings.Contains(guidance.Reason, operationID) ||
		!strings.Contains(
			strings.Join(guidance.Notes, "\n"),
			"do not create a replacement mutation",
		) {
		t.Fatalf("recovery guidance=%+v", guidance)
	}
}

type failingConfigurationCommandAuthority struct {
	delegate configurationCommandAuthority
	plan     manager.ConfigurationPlan
	applyErr error
}

func (authority *failingConfigurationCommandAuthority) PlanConfiguration(
	ctx context.Context,
	draft manager.ConfigurationDraft,
) (manager.ConfigurationPlan, error) {
	plan, err := authority.delegate.PlanConfiguration(ctx, draft)
	authority.plan = plan
	return plan, err
}

func (authority *failingConfigurationCommandAuthority) ApplyConfiguration(
	ctx context.Context,
	request manager.ConfigurationApplyRequest,
) (manager.ConfigurationApplyResult, error) {
	result, err := authority.delegate.ApplyConfiguration(ctx, request)
	if err != nil {
		return result, err
	}
	return manager.ConfigurationApplyResult{}, authority.applyErr
}
