package workspaceattach

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/lifecycle"
)

const defaultPortalCredentialTTL = 30 * time.Minute

type PortalProviderFactoryOptions struct {
	Listen        func() (net.Listener, error)
	Advertise     func(string) (string, error)
	CredentialTTL time.Duration
	Now           func() time.Time
}

type PortalProviderFactory struct {
	options PortalProviderFactoryOptions

	mu        sync.Mutex
	providers map[string]*portalProvider
	admission AdmissionController
}

func NewPortalProviderFactory(options PortalProviderFactoryOptions) *PortalProviderFactory {
	if options.Listen == nil {
		options.Listen = func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
	}
	if options.Advertise == nil {
		options.Advertise = func(address string) (string, error) { return address, nil }
	}
	if options.CredentialTTL <= 0 {
		options.CredentialTTL = defaultPortalCredentialTTL
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	admission, err := NewAdmissionController(SelectedLimits())
	if err != nil {
		panic(err)
	}
	return &PortalProviderFactory{options: options, providers: make(map[string]*portalProvider), admission: admission}
}

func (factory *PortalProviderFactory) Start(_ context.Context, spec ProviderSpec) (Provider, error) {
	if factory == nil {
		return nil, errors.New("workspace Portal provider factory is required")
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	canonical, identity, err := CaptureRootIdentity(spec.CanonicalHostRoot)
	if err != nil {
		return nil, err
	}
	if canonical != spec.CanonicalHostRoot || identity != spec.RootFileIdentity {
		return nil, ErrPortalRootReplaced
	}

	factory.mu.Lock()
	defer factory.mu.Unlock()
	if existing := factory.providers[spec.ProviderID]; existing != nil {
		if existing.Spec() != spec {
			return nil, errors.New("workspace provider id is already bound to different authority")
		}
		if existing.ready() {
			return existing, nil
		}
		return nil, ErrProviderUnproved
	}
	authority := NewPortalCredentialAuthority()
	authority.now = factory.options.Now
	server, err := NewPortalServer(PortalServerOptions{
		Root: spec.CanonicalHostRoot, Authority: authority, Limits: portalLimitsFromSelected(spec.Limits),
		EnvironmentID: spec.EnvironmentID, ProviderID: spec.ProviderID, Admission: factory.admission,
	})
	if err != nil {
		return nil, err
	}
	listener, err := factory.options.Listen()
	if err != nil {
		_ = server.Close()
		return nil, err
	}
	if err := server.Start(listener); err != nil {
		_ = listener.Close()
		_ = server.Close()
		return nil, err
	}
	endpoint, err := factory.options.Advertise(server.Addr())
	if err != nil {
		_ = server.Close()
		return nil, err
	}
	provider := &portalProvider{
		spec: spec, server: server, authority: authority, endpoint: endpoint,
		credentialTTL: factory.options.CredentialTTL, now: factory.options.Now,
		views: make(map[string]*portalGuestView), admission: factory.admission,
	}
	provider.onReleased = func() {
		factory.mu.Lock()
		if factory.providers[spec.ProviderID] == provider {
			delete(factory.providers, spec.ProviderID)
		}
		factory.mu.Unlock()
	}
	factory.providers[spec.ProviderID] = provider
	return provider, nil
}

func (factory *PortalProviderFactory) Observe(_ context.Context, ref lifecycle.ResourceRef) (Observation, error) {
	if factory == nil {
		return Observation{}, errors.New("workspace Portal provider factory is required")
	}
	if err := ref.Validate(); err != nil || ref.Kind != lifecycle.KindWorkspaceHostProvider {
		return Observation{}, errors.New("workspace provider observation reference is invalid")
	}
	factory.mu.Lock()
	provider := factory.providers[ref.ID]
	factory.mu.Unlock()
	if provider == nil {
		return provedObservation(ObservationAbsent, factory.options.Now()), nil
	}
	return provider.Observe(context.Background())
}

func portalLimitsFromSelected(limits LimitSet) PortalLimits {
	return PortalLimits{
		HandlesPerSession: limits.HandlesPerSession, InFlightPerSession: limits.InFlightPerSession,
		QueuedBytesPerSession: limits.QueuedBytesPerSession, FrameBytes: limits.FrameBytes,
		DirectoryEntries: limits.DirectoryEntries,
	}
}

type portalProvider struct {
	spec          ProviderSpec
	server        *PortalServer
	authority     *PortalCredentialAuthority
	endpoint      string
	credentialTTL time.Duration
	now           func() time.Time
	onReleased    func()
	admission     AdmissionController

	mu       sync.Mutex
	views    map[string]*portalGuestView
	released bool
	unproved bool
}

func (provider *portalProvider) Spec() ProviderSpec {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.spec
}

func (provider *portalProvider) Endpoint() string {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.endpoint
}

func (provider *portalProvider) BindIncarnation(_ context.Context, incarnation lifecycle.EnvironmentRef) error {
	if err := incarnation.Validate(true); err != nil {
		return errors.New("workspace provider requires an observed backend incarnation")
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.released || provider.unproved {
		return ErrProviderUnproved
	}
	current := provider.spec.Incarnation
	if current == incarnation {
		return nil
	}
	if current.BootID != "" || current.EnvironmentID != incarnation.EnvironmentID ||
		current.StartGeneration != incarnation.StartGeneration || current.InstanceName != incarnation.InstanceName {
		return errors.New("workspace provider incarnation changed outside the boot binding barrier")
	}
	provider.spec.Incarnation = incarnation
	return nil
}

func (provider *portalProvider) Attach(ctx context.Context, spec ViewSpec) (GuestView, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.released || provider.unproved {
		return nil, ErrProviderUnproved
	}
	if provider.spec.Incarnation.BootID == "" {
		return nil, errors.New("workspace provider incarnation is not bound")
	}
	if err := spec.Validate(provider.spec); err != nil {
		return nil, err
	}
	sessionID := spec.Attachment.SessionID
	if provider.views[sessionID] != nil {
		return nil, errors.New("workspace session already has a Portal view")
	}
	admission, err := provider.admission.Acquire(ctx, AdmissionRequest{
		EnvironmentID: provider.spec.EnvironmentID, ProviderID: provider.spec.ProviderID,
		SessionID: sessionID, Class: AdmissionOrdinary, Views: 1,
	})
	if err != nil {
		return nil, err
	}
	credential, err := provider.authority.Issue(
		sessionID, provider.spec.EnvironmentID, provider.spec.Incarnation.BootID,
		PortalAudience, provider.credentialTTL,
	)
	if err != nil {
		admission.Release()
		return nil, err
	}
	view := &portalGuestView{
		provider: provider, attachment: spec.Attachment, credential: credential,
		endpoint: provider.endpoint, now: provider.now, admission: admission,
	}
	provider.views[sessionID] = view
	return view, nil
}

func (provider *portalProvider) Observe(_ context.Context) (Observation, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	switch {
	case provider.unproved:
		return Observation{State: ObservationUnproved, ObservedAt: provider.now().UTC(), ReasonCode: "portal-provider-unproved"}, nil
	case provider.released:
		return provedObservation(ObservationAbsent, provider.now()), nil
	default:
		return provedObservation(ObservationReady, provider.now()), nil
	}
}

func (provider *portalProvider) Release(_ context.Context) (Observation, error) {
	provider.mu.Lock()
	if provider.released {
		provider.mu.Unlock()
		return provedObservation(ObservationAbsent, provider.now()), nil
	}
	if provider.unproved {
		provider.mu.Unlock()
		return Observation{State: ObservationUnproved, ObservedAt: provider.now().UTC(), ReasonCode: "portal-provider-unproved"}, ErrProviderUnproved
	}
	if len(provider.views) != 0 {
		provider.mu.Unlock()
		return provedObservation(ObservationReady, provider.now()), nil
	}
	provider.released = true
	provider.mu.Unlock()
	if err := provider.server.Close(); err != nil {
		provider.mu.Lock()
		provider.unproved = true
		provider.mu.Unlock()
		return Observation{State: ObservationUnproved, ObservedAt: provider.now().UTC(), ReasonCode: "portal-provider-close-unproved"}, err
	}
	if provider.onReleased != nil {
		provider.onReleased()
	}
	return provedObservation(ObservationAbsent, provider.now()), nil
}

func (provider *portalProvider) ready() bool {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return !provider.released && !provider.unproved
}

type PortalGuestView interface {
	GuestView
	Endpoint() string
	WriteCredential(string) error
}

type portalGuestView struct {
	provider   *portalProvider
	attachment Attachment
	credential PortalCredential
	endpoint   string
	now        func() time.Time
	admission  AdmissionLease

	mu       sync.Mutex
	revoked  bool
	released bool
}

func (view *portalGuestView) Attachment() Attachment { return view.attachment }
func (view *portalGuestView) Endpoint() string       { return view.endpoint }

func (view *portalGuestView) WriteCredential(path string) error {
	view.mu.Lock()
	defer view.mu.Unlock()
	if view.revoked || view.released {
		return ErrPortalCredentialRevoked
	}
	return WritePortalCredential(path, view.credential)
}

func (view *portalGuestView) Observe(_ context.Context) (Observation, error) {
	view.mu.Lock()
	defer view.mu.Unlock()
	if view.released {
		return provedObservation(ObservationAbsent, view.now()), nil
	}
	if view.revoked {
		return Observation{State: ObservationUnproved, ObservedAt: view.now().UTC(), ReasonCode: "portal-view-revoked"}, nil
	}
	return provedObservation(ObservationReady, view.now()), nil
}

func (view *portalGuestView) Revoke(_ context.Context) error {
	view.mu.Lock()
	defer view.mu.Unlock()
	if view.released || view.revoked {
		return nil
	}
	if err := view.provider.authority.Revoke(view.attachment.SessionID); err != nil && !errors.Is(err, ErrPortalAuthentication) {
		return err
	}
	view.revoked = true
	return nil
}

func (view *portalGuestView) Flush(ctx context.Context) error {
	view.mu.Lock()
	released := view.released
	view.mu.Unlock()
	if released {
		return nil
	}
	return view.provider.server.FlushSession(ctx, view.attachment.SessionID)
}

func (view *portalGuestView) Release(ctx context.Context) (Observation, error) {
	if err := view.Revoke(ctx); err != nil {
		return Observation{State: ObservationUnproved, ObservedAt: view.now().UTC(), ReasonCode: "portal-view-revoke-unproved"}, err
	}
	view.mu.Lock()
	if view.released {
		view.mu.Unlock()
		return provedObservation(ObservationAbsent, view.now()), nil
	}
	view.released = true
	view.mu.Unlock()
	view.provider.mu.Lock()
	delete(view.provider.views, view.attachment.SessionID)
	view.provider.mu.Unlock()
	view.admission.Release()
	return provedObservation(ObservationAbsent, view.now()), nil
}

func provedObservation(state ObservationState, now time.Time) Observation {
	return Observation{State: state, ObservedAt: now.UTC()}
}
