package app

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/operatorintent"
	"github.com/vibe-agi/hideout/internal/profile"
)

type configurationCommandAuthority interface {
	PlanConfiguration(
		context.Context,
		manager.ConfigurationDraft,
	) (manager.ConfigurationPlan, error)
	ApplyConfiguration(
		context.Context,
		manager.ConfigurationApplyRequest,
	) (manager.ConfigurationApplyResult, error)
}

type localConfigurationCommandAuthority struct {
	service *manager.ProfileTransactionService
}

func (authority localConfigurationCommandAuthority) PlanConfiguration(
	ctx context.Context,
	draft manager.ConfigurationDraft,
) (manager.ConfigurationPlan, error) {
	return authority.service.Plan(ctx, draft)
}

func (authority localConfigurationCommandAuthority) ApplyConfiguration(
	ctx context.Context,
	request manager.ConfigurationApplyRequest,
) (manager.ConfigurationApplyResult, error) {
	return authority.service.Apply(ctx, request)
}

type configurationConfirmationRequiredError struct {
	operationID string
}

func (err *configurationConfirmationRequiredError) Error() string {
	if err == nil || err.operationID == "" {
		return "configuration apply requires explicit confirmation"
	}
	return "configuration apply requires explicit confirmation for operation " +
		err.operationID
}

type configurationApplyOutcomeError struct {
	operationID string
	cause       error
}

func (err *configurationApplyOutcomeError) Error() string {
	if err == nil || err.operationID == "" {
		return "configuration apply outcome is unknown"
	}
	return "configuration apply outcome is unknown for operation " +
		err.operationID
}

func (err *configurationApplyOutcomeError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

type configurationRecoveryRequiredError struct {
	operationID string
	cause       error
}

func (err *configurationRecoveryRequiredError) Error() string {
	if err == nil || err.operationID == "" {
		return "configuration operation requires exact recovery"
	}
	return "configuration operation " + err.operationID +
		" requires exact recovery"
}

func (err *configurationRecoveryRequiredError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func (a app) connectCommand(args []string) error {
	if len(args) == 0 {
		return errors.New(
			"usage: hideout connect directly|through <proxy-secret> " +
				"[using <resolver>] [for profile <name>] [--yes] | " +
				"hideout connect plan|apply ...",
		)
	}
	switch args[0] {
	case "plan":
		return a.connectPlanCommand(args[1:])
	case "apply":
		return a.connectApplyCommand(args[1:])
	}
	naturalArgs, yes, err := extractExactYesFlag(args)
	if err != nil {
		return err
	}
	intent, err := operatorintent.Parse(
		append([]string{"connect"}, naturalArgs...),
	)
	if err != nil {
		return err
	}
	connection, ok := intent.(operatorintent.Connect)
	if !ok {
		return errors.New("connect command did not produce a connection intent")
	}
	return a.applyNaturalConnectionIntent(connection, yes)
}

func (a app) connectPlanCommand(args []string) error {
	fs := flag.NewFlagSet("connect plan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	profileName := fs.String("profile", "default", "profile to change")
	direct := fs.Bool("direct", false, "plan a direct connection")
	proxyRef := fs.String(
		"through",
		"",
		"daemon-managed proxy secret reference",
	)
	resolver := fs.String("dns", "", "mediated DNS resolver IP")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf(
			"unexpected connect plan argument %q",
			fs.Arg(0),
		)
	}
	if *direct == (strings.TrimSpace(*proxyRef) != "") {
		return errors.New(
			"connect plan requires exactly one of --direct or --through <ref>",
		)
	}
	options := manager.ProfileNetworkOptions{
		ProfileName: strings.TrimSpace(*profileName),
	}
	if *direct {
		if strings.TrimSpace(*resolver) != "" {
			return errors.New("--dns is valid only with --through")
		}
		options.Mode = profile.NetworkModeDirect
	} else {
		options.Mode = profile.NetworkModeTun2Socks
		options.ProxySecretRef = strings.TrimSpace(*proxyRef)
		options.MediatedResolver = strings.TrimSpace(*resolver)
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	authority := a.configurationCommandAuthority(core)
	legacyPlan, plan, err := planProfileNetworkConfiguration(
		context.Background(),
		core,
		authority,
		options,
	)
	if err != nil {
		return err
	}
	if !legacyPlan.Changed {
		fmt.Fprintln(a.stdout, "No change required.")
		fmt.Fprint(a.stdout, "Desired: Already set: ")
		return writeNaturalConnection(a.stdout, legacyPlan.After)
	}
	writeConfigurationPlanReview(a.stdout, plan)
	writeConfigurationPlanApplyCommand(a.stdout, plan.OperationID)
	return nil
}

func (a app) connectApplyCommand(args []string) error {
	remaining, yes, err := extractExactYesFlag(args)
	if err != nil {
		return err
	}
	if len(remaining) != 1 {
		return errors.New(
			"usage: hideout connect apply <operation-id> [--yes]",
		)
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	operation, err := (manager.OperationStore{
		Root: store.Root,
	}).Load(remaining[0])
	if err != nil {
		return err
	}
	plan, inspectErr := manager.NewProfileTransactionService(core).InspectPlan(
		context.Background(),
		remaining[0],
	)
	if inspectErr != nil {
		if errors.Is(inspectErr, os.ErrNotExist) &&
			operation.Terminal() {
			return a.replayTerminalConfigurationOperation(
				context.Background(),
				core,
				a.configurationCommandAuthority(core),
				operation,
			)
		}
		return inspectErr
	}
	if !isNetworkConfigurationPlan(plan) {
		return errors.New(
			"the selected operation is not a connection configuration plan",
		)
	}
	writeConfigurationPlanReview(a.stdout, plan)
	confirmed, err := a.confirmConfigurationPlan(plan.OperationID, yes)
	if err != nil {
		return err
	}
	if !confirmed {
		return nil
	}
	return a.applyReviewedConnectionPlan(
		context.Background(),
		core,
		a.configurationCommandAuthority(core),
		plan,
	)
}

func (a app) replayTerminalConfigurationOperation(
	ctx context.Context,
	core manager.Core,
	authority configurationCommandAuthority,
	operation manager.Operation,
) error {
	request, err := manager.ConfigurationApplyRequestForOperation(operation)
	if err != nil {
		return err
	}
	result, err := authority.ApplyConfiguration(ctx, request)
	if err != nil {
		return configurationApplyFailure(operation.ID, err)
	}
	if result.Operation.Validate() != nil ||
		result.Operation.ID != operation.ID ||
		result.Operation.Owner != operation.Owner ||
		result.Operation.PlanDigest != operation.PlanDigest ||
		result.Operation.BaseRevision != operation.BaseRevision ||
		!result.Operation.Terminal() {
		return &configurationApplyOutcomeError{
			operationID: operation.ID,
			cause: errors.New(
				"Hideout returned final evidence for a different operation",
			),
		}
	}
	fmt.Fprintln(a.stdout, "Exact operation replay:")
	fmt.Fprintf(a.stdout, "  Operation: %s\n", result.Operation.ID)
	fmt.Fprintf(a.stdout, "  Profile: %s\n", result.Operation.Owner.ID)
	fmt.Fprintf(a.stdout, "  Terminal phase: %s\n", result.Operation.Phase)
	if result.Operation.Result != nil {
		fmt.Fprintf(
			a.stdout,
			"  Result: %s\n",
			sanitizeGuidanceText(result.Operation.Result.Summary),
		)
	}
	fmt.Fprintln(
		a.stdout,
		"No new plan or mutation was created; this is the stored terminal outcome.",
	)
	state, stateErr := core.ProfileNetwork(result.Operation.Owner.ID)
	if stateErr != nil {
		return stateErr
	}
	fmt.Fprint(a.stdout, "Current Desired connection: ")
	if err := writeNaturalConnection(a.stdout, state); err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, "Next: hideout show connection")
	return nil
}

func (a app) applyNaturalConnectionIntent(
	intent operatorintent.Connect,
	yes bool,
) error {
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	core := manager.New(store)
	options := manager.ProfileNetworkOptions{
		ProfileName: intent.ProfileName,
	}
	switch intent.Connection {
	case operatorintent.ConnectionDirect:
		options.Mode = profile.NetworkModeDirect
	case operatorintent.ConnectionProxy:
		options.Mode = profile.NetworkModeTun2Socks
		options.ProxySecretRef = intent.ProxyName
		options.MediatedResolver = intent.Resolver
	default:
		return fmt.Errorf(
			"unsupported connection intent %q",
			intent.Connection,
		)
	}
	authority := a.configurationCommandAuthority(core)
	legacyPlan, plan, err := planProfileNetworkConfiguration(
		context.Background(),
		core,
		authority,
		options,
	)
	if err != nil {
		return err
	}
	if !legacyPlan.Changed {
		fmt.Fprintln(a.stdout, "Desired:")
		fmt.Fprint(a.stdout, "  Already set: ")
		if err := writeNaturalConnection(
			a.stdout,
			legacyPlan.After,
		); err != nil {
			return err
		}
		fmt.Fprintln(
			a.stdout,
			"Effective: Existing sessions and accepted connections are unchanged.",
		)
		fmt.Fprintln(a.stdout, "Next: hideout show connection")
		return nil
	}
	writeConfigurationPlanReview(a.stdout, plan)
	confirmed, err := a.confirmConfigurationPlan(plan.OperationID, yes)
	if err != nil {
		return err
	}
	if !confirmed {
		return nil
	}
	return a.applyReviewedConnectionPlan(
		context.Background(),
		core,
		authority,
		plan,
	)
}

func (a app) configurationCommandAuthority(
	core manager.Core,
) configurationCommandAuthority {
	if a.configurationAuthority != nil {
		return a.configurationAuthority(core)
	}
	if client, _, _, err := daemon.DialClient(core.Store.Root); err == nil {
		client.CloseIdleConnections()
		return newTUIConfigurationClient(core.Store.Root)
	}
	return localConfigurationCommandAuthority{
		service: manager.NewProfileTransactionService(core),
	}
}

func planProfileNetworkConfiguration(
	ctx context.Context,
	core manager.Core,
	authority configurationCommandAuthority,
	options manager.ProfileNetworkOptions,
) (
	manager.ProfileNetworkPlan,
	manager.ConfigurationPlan,
	error,
) {
	legacyPlan, err := core.PlanProfileNetwork(options)
	if err != nil || !legacyPlan.Changed {
		return legacyPlan, manager.ConfigurationPlan{}, err
	}
	changes, err := manager.ConfigurationChangesForProfileNetworkPlan(
		legacyPlan,
	)
	if err != nil {
		return manager.ProfileNetworkPlan{}, manager.ConfigurationPlan{}, err
	}
	projection, err := (manager.ProfileProjectionService{
		Store: core.Store,
	}).Load(legacyPlan.Profile)
	if err != nil {
		return manager.ProfileNetworkPlan{}, manager.ConfigurationPlan{}, err
	}
	plan, err := authority.PlanConfiguration(ctx, manager.ConfigurationDraft{
		Schema:       manager.ConfigurationDraftSchema,
		Profile:      legacyPlan.Profile,
		BaseRevision: projection.Revision,
		ClientNonce: fmt.Sprintf(
			"cli-connect-%d",
			time.Now().UTC().UnixNano(),
		),
		Changes: changes,
	})
	if err != nil {
		return manager.ProfileNetworkPlan{}, manager.ConfigurationPlan{}, err
	}
	if plan.VerifyDigest() != nil ||
		plan.Profile != legacyPlan.Profile ||
		plan.BaseRevision != projection.Revision ||
		!isNetworkConfigurationPlan(plan) {
		return manager.ProfileNetworkPlan{}, manager.ConfigurationPlan{},
			errors.New(
				"Hideout returned a connection plan for different state",
			)
	}
	return legacyPlan, plan, nil
}

func (a app) confirmConfigurationPlan(
	operationID string,
	yes bool,
) (bool, error) {
	if yes {
		fmt.Fprintln(
			a.stdout,
			"Confirmation: accepted by explicit --yes.",
		)
		return true, nil
	}
	if !a.isInteractiveTerminal() {
		writeConfigurationPlanApplyCommand(a.stdout, operationID)
		return false, &configurationConfirmationRequiredError{
			operationID: operationID,
		}
	}
	fmt.Fprint(a.stdout, "Apply this exact reviewed plan? [y/N]: ")
	answer, err := bufio.NewReader(a.stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	default:
		fmt.Fprintln(a.stdout, "Cancelled; no state changed.")
		return false, nil
	}
}

func (a app) applyReviewedConnectionPlan(
	ctx context.Context,
	core manager.Core,
	authority configurationCommandAuthority,
	plan manager.ConfigurationPlan,
) error {
	result, err := authority.ApplyConfiguration(
		ctx,
		manager.ConfigurationApplyRequest{
			Schema:       manager.ConfigurationApplySchema,
			OperationID:  plan.OperationID,
			Profile:      plan.Profile,
			BaseRevision: plan.BaseRevision,
			PlanDigest:   plan.PlanDigest,
			Confirmed:    true,
		},
	)
	if err != nil {
		return configurationApplyFailure(plan.OperationID, err)
	}
	if result.Operation.Validate() != nil ||
		result.Operation.ID != plan.OperationID ||
		result.Operation.Owner != (manager.OperationOwner{
			Kind: "profile",
			ID:   plan.Profile,
		}) {
		return &configurationApplyOutcomeError{
			operationID: plan.OperationID,
			cause: errors.New(
				"Hideout returned configuration evidence for a different operation",
			),
		}
	}
	state, err := core.ProfileNetwork(plan.Profile)
	if err != nil {
		return &configurationApplyOutcomeError{
			operationID: plan.OperationID,
			cause:       err,
		}
	}
	operation := result.Operation
	fmt.Fprintln(a.stdout, "Desired:")
	fmt.Fprint(a.stdout, "  Updated: ")
	if err := writeNaturalConnection(a.stdout, state); err != nil {
		return err
	}
	writeProfileNetworkTransitionOutcome(
		a.stdout,
		manager.ProfileNetworkResult{
			Applied:   true,
			Network:   state,
			Operation: &operation,
		},
	)
	fmt.Fprintln(a.stdout, "Next: hideout show connection")
	return nil
}

func configurationApplyFailure(
	operationID string,
	err error,
) error {
	switch {
	case errors.Is(err, manager.ErrStaleConfigurationPlan),
		errors.Is(err, manager.ErrStaleProfileRevision),
		errors.Is(err, manager.ErrConfigurationConfirmationRequired),
		errors.Is(err, manager.ErrConfigurationPlanExpired),
		errors.Is(err, manager.ErrConfigurationBlocked),
		errors.Is(err, manager.ErrConfigurationMutationConflict),
		errors.Is(err, manager.ErrOperationMismatch):
		return err
	case errors.Is(err, manager.ErrConfigurationRecoveryRequired),
		errors.Is(err, manager.ErrOperationTerminalUnproved):
		return &configurationRecoveryRequiredError{
			operationID: operationID,
			cause:       err,
		}
	}
	var apiErr *tuiConfigurationAPIError
	if errors.As(err, &apiErr) && apiErr.status < 500 {
		return err
	}
	return &configurationApplyOutcomeError{
		operationID: operationID,
		cause:       err,
	}
}

func writeConfigurationPlanReview(
	w io.Writer,
	plan manager.ConfigurationPlan,
) {
	fmt.Fprintln(w, "Canonical connection review:")
	fmt.Fprintf(w, "  Operation: %s\n", plan.OperationID)
	fmt.Fprintf(
		w,
		"  Profile: %s (revision %d)\n",
		plan.Profile,
		plan.BaseRevision,
	)
	fmt.Fprintf(w, "  Plan digest: %s\n", plan.PlanDigest)
	fmt.Fprintf(
		w,
		"  Expires: %s\n",
		plan.ExpiresAt.UTC().Format(time.RFC3339),
	)
	fmt.Fprintln(w, "Diff:")
	for _, diff := range plan.Diff {
		fmt.Fprintf(
			w,
			"  %s: %s -> %s (scope: %s)\n",
			diff.Field,
			diff.Before,
			diff.After,
			diff.Scope,
		)
	}
	fmt.Fprintln(w, "Effects:")
	for _, effect := range plan.Effects {
		timing := "next-attach"
		if effect.Live {
			timing = "live"
		}
		fmt.Fprintf(
			w,
			"  %s [%s, %s]: %s\n",
			effect.ID,
			effect.Scope,
			timing,
			effect.Summary,
		)
		if len(effect.ProofRequired) != 0 {
			fmt.Fprintf(
				w,
				"    proof: %s\n",
				strings.Join(effect.ProofRequired, ", "),
			)
		}
	}
	if len(plan.Blockers) != 0 {
		fmt.Fprintln(w, "Blockers:")
		for _, blocker := range plan.Blockers {
			fmt.Fprintf(
				w,
				"  %s: %s; recovery: %s\n",
				blocker.Code,
				blocker.Summary,
				blocker.Recovery,
			)
		}
	}
	if len(plan.Warnings) != 0 {
		fmt.Fprintln(w, "Warnings:")
		for _, warning := range plan.Warnings {
			fmt.Fprintf(w, "  %s: %s\n", warning.Code, warning.Summary)
		}
	}
	fmt.Fprintf(
		w,
		"Rollback: %s (%s)\n",
		plan.Rollback.Summary,
		plan.Rollback.Mode,
	)
	for _, effect := range plan.Rollback.Effects {
		fmt.Fprintf(w, "  %s\n", effect)
	}
	fmt.Fprintln(w, "No state has changed.")
}

func writeConfigurationPlanApplyCommand(
	w io.Writer,
	operationID string,
) {
	fmt.Fprintln(w, "Apply this exact reviewed plan with:")
	fmt.Fprintf(w, "  hideout connect apply %s --yes\n", operationID)
}

func isNetworkConfigurationPlan(
	plan manager.ConfigurationPlan,
) bool {
	if plan.Schema != manager.ConfigurationPlanSchema ||
		len(plan.CanonicalChanges) == 0 {
		return false
	}
	for _, change := range plan.CanonicalChanges {
		switch change.Kind {
		case manager.ChangeNetworkPosture,
			manager.ChangeNetworkProxyRef,
			manager.ChangeNetworkDNS:
		default:
			return false
		}
	}
	return true
}

func extractExactYesFlag(
	args []string,
) ([]string, bool, error) {
	remaining := make([]string, 0, len(args))
	yes := false
	for _, arg := range args {
		if arg != "--yes" {
			remaining = append(remaining, arg)
			continue
		}
		if yes {
			return nil, false, errors.New("--yes may be specified only once")
		}
		yes = true
	}
	return remaining, yes, nil
}
