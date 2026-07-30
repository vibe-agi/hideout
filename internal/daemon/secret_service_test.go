package daemon

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
)

func TestDaemonSecretServiceKeepsRuntimeResolveOutsideManagerProvider(t *testing.T) {
	providerType := reflect.TypeOf((*manager.SecretProvider)(nil)).Elem()
	for _, forbidden := range []string{"Resolve", "Get", "Read", "Value"} {
		if _, exists := providerType.MethodByName(forbidden); exists {
			t.Fatalf("Manager SecretProvider exposes %s", forbidden)
		}
	}

	store := &daemonSecretStoreFixture{
		value: []byte("socks5://daemon-only@127.0.0.1:7890"),
	}
	service := newDaemonSecretService(
		manager.New(profile.Store{Root: t.TempDir()}),
		store,
	)
	var public manager.SecretProvider = service
	references, err := public.ListSecrets(context.Background(), "local-proxy")
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 1 ||
		references[0].Availability != secrets.AvailabilityAvailable {
		t.Fatalf("public metadata=%+v", references)
	}

	buffer, err := service.Resolve(context.Background(), "local-proxy")
	if err != nil {
		t.Fatal(err)
	}
	if err := buffer.Use(func(value []byte) error {
		if string(value) != "socks5://daemon-only@127.0.0.1:7890" {
			t.Fatalf("runtime value=%q", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonSecretApplyInvalidatesActivitySnapshotBeforeMutation(
	t *testing.T,
) {
	store := &daemonSecretStoreFixture{}
	service := newDaemonSecretService(
		manager.New(profile.Store{Root: t.TempDir()}),
		store,
	)
	invalidationErr := errors.New("activity redaction invalidation failed")
	invalidationCalls := 0
	service.beginApply = func() (func(), error) {
		invalidationCalls++
		return nil, invalidationErr
	}
	buffer, err := secrets.NewBuffer([]byte("must-be-cleared"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.ApplySecret(
		context.Background(),
		manager.SecretApplyRequest{Value: buffer},
	)
	if !errors.Is(err, invalidationErr) {
		t.Fatalf("ApplySecret error=%v, want invalidation failure", err)
	}
	if invalidationCalls != 1 {
		t.Fatalf("invalidation calls=%d, want 1", invalidationCalls)
	}
	if store.setCalls != 0 {
		t.Fatalf("secret store mutations=%d, want 0", store.setCalls)
	}
	if err := buffer.Use(func([]byte) error { return nil }); !errors.Is(
		err,
		secrets.ErrSecretBufferUsed,
	) {
		t.Fatalf("secret buffer remained usable after refusal: %v", err)
	}
}

func TestDaemonSecretApplyReleasesMutationGateAfterManagerReturns(
	t *testing.T,
) {
	store := &daemonSecretStoreFixture{}
	service := newDaemonSecretService(
		manager.New(profile.Store{Root: t.TempDir()}),
		store,
	)
	releaseCalls := 0
	service.beginApply = func() (func(), error) {
		return func() { releaseCalls++ }, nil
	}
	buffer, err := secrets.NewBuffer([]byte("must-be-cleared"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.ApplySecret(
		context.Background(),
		manager.SecretApplyRequest{Value: buffer},
	); err == nil {
		t.Fatal("invalid Manager request unexpectedly succeeded")
	}
	if releaseCalls != 1 {
		t.Fatalf("mutation gate releases=%d, want 1", releaseCalls)
	}
}

func TestDaemonNetworkSecretResolverPrefersManagedAndBoundsGeneration(t *testing.T) {
	store := &daemonSecretStoreFixture{
		value: []byte("socks5://managed-user:managed-password@127.0.0.1:7890"),
	}
	service := newDaemonSecretService(
		manager.New(profile.Store{Root: t.TempDir()}),
		store,
	)
	resolver := daemonNetworkSecretResolver{
		managed: service,
		startup: netpolicy.EnvSecretResolver{Env: []string{
			netpolicy.SecretEnvName("local-proxy") +
				"=socks5://fallback-user:fallback-password@127.0.0.1:7891",
		}},
	}
	resolution, err := resolver.ResolveSecret("local-proxy")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Source != netpolicy.SecretSourceManaged ||
		resolution.Generation != 1 ||
		resolution.Deprecated ||
		!strings.Contains(resolution.Value, "managed-password") ||
		strings.Contains(resolution.Value, "fallback-password") {
		t.Fatalf("managed resolution=%+v", resolution)
	}
}

func TestDaemonNetworkSecretResolverUsesOnlyCapturedStartupFallback(t *testing.T) {
	store := &daemonSecretStoreFixture{
		availability: secrets.AvailabilityMissing,
		resolveErr:   secrets.ErrSecretMissing,
	}
	service := newDaemonSecretService(
		manager.New(profile.Store{Root: t.TempDir()}),
		store,
	)
	const fallback = "socks5://fallback-user:fallback-password@127.0.0.1:7891"
	resolver := daemonNetworkSecretResolver{
		managed: service,
		startup: netpolicy.EnvSecretResolver{Env: []string{
			netpolicy.SecretEnvName("local-proxy") + "=" + fallback,
		}},
	}
	resolution, err := resolver.ResolveSecret("local-proxy")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Value != fallback ||
		resolution.Source != netpolicy.SecretSourceStartupEnvironment ||
		!resolution.Deprecated ||
		resolution.RecoveryCommand != "hideout secret set local-proxy" {
		t.Fatalf("fallback resolution=%+v", resolution)
	}

	late := daemonNetworkSecretResolver{
		managed: service,
		startup: netpolicy.EnvSecretResolver{Env: []string{}},
	}
	t.Setenv(
		netpolicy.SecretEnvName("local-proxy"),
		"socks5://late-user:late-password@127.0.0.1:7892",
	)
	if _, err := late.ResolveSecret("local-proxy"); err == nil ||
		!strings.Contains(err.Error(), "hideout secret set local-proxy") ||
		strings.Contains(err.Error(), "late-password") ||
		strings.Contains(err.Error(), "HIDEOUT_SECRET_") {
		t.Fatalf("late environment resolution error=%v", err)
	}
}

func TestDaemonNetworkSecretResolverDoesNotDowngradeLockedManagedSecret(t *testing.T) {
	const canary = "provider error contained user:password@private.invalid"
	store := &daemonSecretStoreFixture{
		availability: secrets.AvailabilityLocked,
		resolveErr: &secrets.ProviderError{
			Provider: "memory-keychain",
			Reason:   "keychain-locked",
			Cause:    errors.Join(secrets.ErrSecretLocked, errors.New(canary)),
		},
	}
	service := newDaemonSecretService(
		manager.New(profile.Store{Root: t.TempDir()}),
		store,
	)
	resolver := daemonNetworkSecretResolver{
		managed: service,
		startup: netpolicy.EnvSecretResolver{Env: []string{
			netpolicy.SecretEnvName("local-proxy") +
				"=socks5://fallback-user:fallback-password@127.0.0.1:7891",
		}},
	}
	if _, err := resolver.ResolveSecret("local-proxy"); err == nil ||
		!strings.Contains(err.Error(), "locked") ||
		strings.Contains(err.Error(), canary) ||
		strings.Contains(err.Error(), "fallback-password") {
		t.Fatalf("locked managed secret error=%v", err)
	}
}

func TestDaemonStartWiresCapturedResolverIntoEveryRunTransport(t *testing.T) {
	const startup = "socks5://startup-user:startup-password@127.0.0.1:7891"
	const late = "socks5://late-user:late-password@127.0.0.1:7892"
	t.Setenv(netpolicy.SecretEnvName("local-proxy"), startup)
	store := &daemonSecretStoreFixture{
		availability: secrets.AvailabilityMissing,
		resolveErr:   secrets.ErrSecretMissing,
	}
	d, err := Start(Options{
		Store:       testStore(t),
		SecretStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Stop(context.Background()) })

	t.Setenv(netpolicy.SecretEnvName("local-proxy"), late)
	resolvers := []netpolicy.SecretResolver{
		d.api.RunSecretResolver,
		d.sessionServer.networkResolver,
	}
	for index, resolver := range resolvers {
		detailed, ok := resolver.(netpolicy.DetailedSecretResolver)
		if !ok {
			t.Fatalf("resolver[%d] lacks detailed provenance", index)
		}
		resolution, err := detailed.ResolveSecret("local-proxy")
		if err != nil {
			t.Fatalf("resolver[%d]: %v", index, err)
		}
		if resolution.Value != startup ||
			resolution.Source != netpolicy.SecretSourceStartupEnvironment ||
			strings.Contains(resolution.Value, "late-password") {
			t.Fatalf("resolver[%d] used non-startup state: %+v", index, resolution)
		}
	}
}

type daemonSecretStoreFixture struct {
	value        []byte
	availability string
	resolveErr   error
	setCalls     int
}

func (store *daemonSecretStoreFixture) Provider() string {
	return "memory-keychain"
}

func (store *daemonSecretStoreFixture) List(
	context.Context,
) ([]secrets.Reference, error) {
	reference, err := store.Reference(context.Background(), "local-proxy")
	return []secrets.Reference{reference}, err
}

func (store *daemonSecretStoreFixture) Reference(
	context.Context,
	string,
) (secrets.Reference, error) {
	availability := store.availability
	if availability == "" {
		availability = secrets.AvailabilityAvailable
	}
	generation := uint64(1)
	updatedAt := time.Date(
		2026, 7, 29, 21, 0, 0, 0, time.UTC,
	)
	reason := ""
	if availability != secrets.AvailabilityAvailable {
		generation = 0
		updatedAt = time.Time{}
		switch availability {
		case secrets.AvailabilityMissing:
			reason = "secret-missing"
		case secrets.AvailabilityLocked:
			reason = "keychain-locked"
		default:
			reason = "provider-unavailable"
		}
	}
	return secrets.Reference{
		Schema: secrets.SecretReferenceSchema, Ref: "local-proxy",
		Provider: store.Provider(), Availability: availability,
		Generation: generation, UpdatedAt: updatedAt, Reason: reason,
	}, nil
}

func (store *daemonSecretStoreFixture) Set(
	context.Context,
	secrets.WriteRequest,
) (secrets.Reference, error) {
	store.setCalls++
	return store.Reference(context.Background(), "local-proxy")
}

func (store *daemonSecretStoreFixture) Delete(
	context.Context,
	secrets.DeleteRequest,
) (secrets.Reference, error) {
	return secrets.Reference{}, secrets.ErrSecretMissing
}

func (store *daemonSecretStoreFixture) Resolve(
	context.Context,
	string,
) (*secrets.Buffer, error) {
	if store.resolveErr != nil {
		return nil, store.resolveErr
	}
	return secrets.NewBuffer(store.value)
}

var _ secrets.RuntimeStore = (*daemonSecretStoreFixture)(nil)
