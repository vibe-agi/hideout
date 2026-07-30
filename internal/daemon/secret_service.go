package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/manager"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/secrets"
)

const (
	managedSecretResolveAttempts = 3
	managedSecretResolveTimeout  = 15 * time.Second
)

// daemonSecretService is the only object that combines Manager-visible secret
// metadata/mutation authority with runtime value resolution. The Manager API is
// handed only its SecretProvider methods, so no public route can type its way
// into Resolve.
type daemonSecretService struct {
	manager    *manager.SecretService
	runtime    secrets.RuntimeResolver
	beginApply func() (func(), error)
}

func newDaemonSecretService(
	core manager.Core,
	store secrets.RuntimeStore,
) *daemonSecretService {
	return &daemonSecretService{
		manager: manager.NewSecretService(core, store),
		runtime: store,
	}
}

func (service *daemonSecretService) ListSecrets(
	ctx context.Context,
	ref string,
) ([]secrets.Reference, error) {
	return service.manager.ListSecrets(ctx, ref)
}

func (service *daemonSecretService) NetworkSecretReference(
	ctx context.Context,
	ref string,
) (secrets.Reference, error) {
	references, err := service.ListSecrets(ctx, ref)
	if err != nil {
		return secrets.Reference{}, err
	}
	if len(references) != 1 ||
		references[0].Ref != ref ||
		references[0].Validate() != nil {
		return secrets.Reference{}, secrets.ErrSecretEnvelopeCorrupt
	}
	return references[0], nil
}

func (service *daemonSecretService) PlanSecret(
	ctx context.Context,
	draft manager.SecretDraft,
) (manager.SecretPlan, error) {
	return service.manager.PlanSecret(ctx, draft)
}

func (service *daemonSecretService) ApplySecret(
	ctx context.Context,
	request manager.SecretApplyRequest,
) (manager.SecretApplyResult, error) {
	var release func()
	if service != nil && service.beginApply != nil {
		var err error
		release, err = service.beginApply()
		if err != nil {
			if request.Value != nil {
				request.Value.Clear()
			}
			return manager.SecretApplyResult{}, fmt.Errorf(
				"invalidate active activity redaction snapshots before secret mutation: %w",
				err,
			)
		}
	}
	if release != nil {
		defer release()
	}
	return service.manager.ApplySecret(ctx, request)
}

func (service *daemonSecretService) reconcileOperationWithNetworkAuthorityReset(
	ctx context.Context,
	operationID string,
	reset *manager.NetworkAuthorityResetProof,
) (manager.SecretApplyResult, error) {
	var release func()
	if service != nil && service.beginApply != nil {
		var err error
		release, err = service.beginApply()
		if err != nil {
			return manager.SecretApplyResult{}, fmt.Errorf(
				"invalidate active activity redaction snapshots before secret reconciliation: %w",
				err,
			)
		}
	}
	if release != nil {
		defer release()
	}
	if reset != nil {
		return service.manager.
			ReconcileOperationAfterNetworkAuthorityReset(
				ctx,
				operationID,
				*reset,
			)
	}
	return service.manager.ReconcileOperation(ctx, operationID)
}

func (service *daemonSecretService) Resolve(
	ctx context.Context,
	ref string,
) (*secrets.Buffer, error) {
	if service == nil || service.runtime == nil {
		return nil, secrets.ErrProviderUnavailable
	}
	return service.runtime.Resolve(ctx, ref)
}

// daemonNetworkSecretResolver is the runtime-only bridge between network
// planning and daemon-managed secret storage. The startup environment is a
// captured compatibility fallback, never a live os.Environ view.
type daemonNetworkSecretResolver struct {
	managed *daemonSecretService
	startup netpolicy.EnvSecretResolver
}

func (resolver daemonNetworkSecretResolver) Resolve(
	ref string,
) (string, error) {
	resolution, err := resolver.ResolveSecret(ref)
	return resolution.Value, err
}

func (resolver daemonNetworkSecretResolver) ResolveSecret(
	ref string,
) (netpolicy.SecretResolution, error) {
	if err := secrets.ValidateRef(ref); err != nil {
		return netpolicy.SecretResolution{}, errors.New("secret ref is invalid")
	}
	if resolver.managed == nil {
		return resolver.startupFallback(ref, secrets.AvailabilityUnavailable)
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		managedSecretResolveTimeout,
	)
	defer cancel()

	for attempt := 0; attempt < managedSecretResolveAttempts; attempt++ {
		before, err := daemonManagedSecretReference(ctx, resolver.managed, ref)
		if err != nil {
			return resolver.handleManagedError(ref, err)
		}
		switch before.Availability {
		case secrets.AvailabilityMissing, secrets.AvailabilityUnavailable:
			return resolver.startupFallback(ref, before.Availability)
		case secrets.AvailabilityLocked:
			return netpolicy.SecretResolution{}, daemonLockedSecretError(ref)
		case secrets.AvailabilityAvailable:
		default:
			return netpolicy.SecretResolution{}, daemonInvalidSecretError(ref)
		}

		buffer, err := resolver.managed.Resolve(ctx, ref)
		if err != nil {
			if errors.Is(err, secrets.ErrSecretMissing) {
				continue
			}
			return resolver.handleManagedError(ref, err)
		}

		after, err := daemonManagedSecretReference(ctx, resolver.managed, ref)
		if err != nil {
			buffer.Clear()
			return resolver.handleManagedError(ref, err)
		}
		if after.Availability != secrets.AvailabilityAvailable ||
			after.Generation != before.Generation {
			buffer.Clear()
			continue
		}

		var value string
		err = buffer.Use(func(raw []byte) error {
			if len(raw) == 0 || !utf8.Valid(raw) {
				return errors.New("managed secret value is invalid")
			}
			// network.Plan owns the bounded runtime string from this point. The
			// provider buffer is cleared immediately after this callback.
			value = string(raw)
			return nil
		})
		if err != nil {
			return netpolicy.SecretResolution{}, daemonInvalidSecretError(ref)
		}
		return netpolicy.SecretResolution{
			Value:      value,
			Source:     netpolicy.SecretSourceManaged,
			Generation: before.Generation,
			Reason:     "daemon-managed secret",
		}, nil
	}
	return netpolicy.SecretResolution{}, fmt.Errorf(
		"secret ref %s changed while it was being resolved; retry",
		ref,
	)
}

func daemonManagedSecretReference(
	ctx context.Context,
	service *daemonSecretService,
	ref string,
) (secrets.Reference, error) {
	references, err := service.ListSecrets(ctx, ref)
	if err != nil {
		return secrets.Reference{}, err
	}
	if len(references) != 1 ||
		references[0].Ref != ref ||
		references[0].Validate() != nil {
		return secrets.Reference{}, secrets.ErrSecretEnvelopeCorrupt
	}
	return references[0], nil
}

func (resolver daemonNetworkSecretResolver) handleManagedError(
	ref string,
	err error,
) (netpolicy.SecretResolution, error) {
	switch {
	case errors.Is(err, secrets.ErrSecretLocked):
		return netpolicy.SecretResolution{}, daemonLockedSecretError(ref)
	case errors.Is(err, secrets.ErrSecretMissing):
		return resolver.startupFallback(ref, secrets.AvailabilityMissing)
	case errors.Is(err, secrets.ErrProviderUnavailable),
		errors.Is(err, manager.ErrSecretProviderUnavailable),
		errors.Is(err, context.DeadlineExceeded):
		return resolver.startupFallback(ref, secrets.AvailabilityUnavailable)
	case errors.Is(err, secrets.ErrSecretEnvelopeCorrupt):
		return netpolicy.SecretResolution{}, daemonInvalidSecretError(ref)
	default:
		return netpolicy.SecretResolution{}, fmt.Errorf(
			"managed secret ref %s could not be resolved; retry or inspect it with hideout secret status %s",
			ref,
			ref,
		)
	}
}

func (resolver daemonNetworkSecretResolver) startupFallback(
	ref string,
	availability string,
) (netpolicy.SecretResolution, error) {
	startup := resolver.startup
	if startup.Env == nil {
		startup.Env = []string{}
	}
	resolution, err := startup.ResolveSecret(ref)
	if err == nil {
		return resolution, nil
	}
	if availability == secrets.AvailabilityUnavailable {
		return netpolicy.SecretResolution{}, fmt.Errorf(
			"secret provider is unavailable for ref %s; retry or store it with hideout secret set %s",
			ref,
			ref,
		)
	}
	return netpolicy.SecretResolution{}, fmt.Errorf(
		"secret ref %s is not set; store it with hideout secret set %s",
		ref,
		ref,
	)
}

func daemonLockedSecretError(ref string) error {
	return fmt.Errorf(
		"secret ref %s is locked; unlock the system keychain and retry",
		ref,
	)
}

func daemonInvalidSecretError(ref string) error {
	return fmt.Errorf(
		"managed secret ref %s is invalid or corrupt; inspect it with hideout secret status %s",
		ref,
		ref,
	)
}

var _ manager.SecretProvider = (*daemonSecretService)(nil)
var _ netpolicy.DetailedSecretResolver = daemonNetworkSecretResolver{}
