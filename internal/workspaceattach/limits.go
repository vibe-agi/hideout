package workspaceattach

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// LimitSet is the transport-neutral admission contract selected by Phase R.
// Per-provider/global values are bounded derivations of the measured
// per-session limits, not additional benchmark claims.
type LimitSet struct {
	ViewsPerEnvironment        int
	ViewsPerSession            int
	HandlesPerSession          int
	HandlesPerProvider         int
	InFlightPerSession         int
	InFlightGlobal             int
	QueuedBytesPerSession      int64
	QueuedBytesGlobal          int64
	FrameBytes                 int
	DirectoryEntries           int
	DirectoryPageEntries       int
	TeardownInFlightPerSession int
}

func SelectedLimits() LimitSet {
	const (
		viewsPerEnvironment   = 16
		handlesPerSession     = 4096
		inFlightPerSession    = 256
		queuedBytesPerSession = 8 << 20
	)
	return LimitSet{
		ViewsPerEnvironment:        viewsPerEnvironment,
		ViewsPerSession:            1,
		HandlesPerSession:          handlesPerSession,
		HandlesPerProvider:         viewsPerEnvironment * handlesPerSession,
		InFlightPerSession:         inFlightPerSession,
		InFlightGlobal:             viewsPerEnvironment * inFlightPerSession,
		QueuedBytesPerSession:      queuedBytesPerSession,
		QueuedBytesGlobal:          viewsPerEnvironment * queuedBytesPerSession,
		FrameBytes:                 1 << 20,
		DirectoryEntries:           65536,
		DirectoryPageEntries:       4096,
		TeardownInFlightPerSession: 2,
	}
}

func (limits LimitSet) Validate() error {
	if limits.ViewsPerEnvironment < 1 || limits.ViewsPerEnvironment > 256 ||
		limits.ViewsPerSession < 1 || limits.ViewsPerSession > limits.ViewsPerEnvironment ||
		limits.HandlesPerSession < 1 || limits.HandlesPerSession > 1<<20 || limits.HandlesPerProvider < limits.HandlesPerSession ||
		limits.HandlesPerProvider > limits.ViewsPerEnvironment*limits.HandlesPerSession ||
		limits.InFlightPerSession < 1 || limits.InFlightPerSession > 1<<16 || limits.InFlightGlobal < limits.InFlightPerSession ||
		limits.InFlightGlobal > limits.ViewsPerEnvironment*limits.InFlightPerSession ||
		limits.QueuedBytesPerSession < 4096 || limits.QueuedBytesPerSession > 1<<30 || limits.QueuedBytesGlobal < limits.QueuedBytesPerSession ||
		limits.QueuedBytesGlobal > int64(limits.ViewsPerEnvironment)*limits.QueuedBytesPerSession ||
		limits.FrameBytes < 1024 || int64(limits.FrameBytes) > limits.QueuedBytesPerSession ||
		limits.DirectoryEntries < 1 || limits.DirectoryPageEntries < 1 || limits.DirectoryPageEntries > limits.DirectoryEntries ||
		limits.TeardownInFlightPerSession < 1 || limits.TeardownInFlightPerSession > limits.InFlightPerSession {
		return errors.New("workspace provider limits are invalid or internally inconsistent")
	}
	return nil
}

type AdmissionClass string

const (
	AdmissionOrdinary AdmissionClass = "ordinary"
	AdmissionTeardown AdmissionClass = "teardown"
)

type AdmissionRequest struct {
	EnvironmentID  string
	ProviderID     string
	SessionID      string
	Class          AdmissionClass
	Views          int
	Handles        int
	InFlight       int
	QueuedBytes    int64
	FrameBytes     int
	DirectoryItems int
}

func (request AdmissionRequest) Validate(limits LimitSet) error {
	if err := limits.Validate(); err != nil {
		return err
	}
	if request.EnvironmentID == "" || request.ProviderID == "" || request.SessionID == "" ||
		(request.Class != AdmissionOrdinary && request.Class != AdmissionTeardown) {
		return errors.New("workspace admission identity or class is invalid")
	}
	for label, value := range map[string]int{
		"views": request.Views, "handles": request.Handles, "inFlight": request.InFlight,
		"frameBytes": request.FrameBytes, "directoryItems": request.DirectoryItems,
	} {
		if value < 0 {
			return fmt.Errorf("workspace admission %s cannot be negative", label)
		}
	}
	if request.QueuedBytes < 0 {
		return errors.New("workspace admission queuedBytes cannot be negative")
	}
	if request.Views > limits.ViewsPerSession || request.Handles > limits.HandlesPerSession ||
		request.QueuedBytes > limits.QueuedBytesPerSession || request.FrameBytes > limits.FrameBytes ||
		request.DirectoryItems > limits.DirectoryEntries {
		return ErrProviderOverloaded
	}
	if request.Class == AdmissionTeardown {
		if request.Views != 0 || request.Handles != 0 || request.QueuedBytes != 0 || request.DirectoryItems != 0 ||
			request.InFlight > limits.TeardownInFlightPerSession {
			return ErrProviderOverloaded
		}
		return nil
	}
	if request.InFlight > limits.InFlightPerSession {
		return ErrProviderOverloaded
	}
	return nil
}

type AdmissionLease interface {
	Release()
}

// AdmissionController owns accounting. Implementations must reserve teardown
// capacity independently from ordinary saturation and must isolate sessions.
type AdmissionController interface {
	Acquire(context.Context, AdmissionRequest) (AdmissionLease, error)
	Snapshot() AdmissionSnapshot
}

type AdmissionSnapshot struct {
	Views       int
	Handles     int
	InFlight    int
	QueuedBytes int64
}

type admissionController struct {
	limits LimitSet

	mu           sync.Mutex
	sessions     map[string]AdmissionSnapshot
	providers    map[string]AdmissionSnapshot
	environments map[string]AdmissionSnapshot
	teardown     map[string]int
}

func NewAdmissionController(limits LimitSet) (AdmissionController, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	return &admissionController{
		limits: limits, sessions: make(map[string]AdmissionSnapshot),
		providers: make(map[string]AdmissionSnapshot), environments: make(map[string]AdmissionSnapshot),
		teardown: make(map[string]int),
	}, nil
}

func (controller *admissionController) Acquire(ctx context.Context, request AdmissionRequest) (AdmissionLease, error) {
	if err := request.Validate(controller.limits); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	controller.mu.Lock()
	defer controller.mu.Unlock()
	if request.Class == AdmissionTeardown {
		key := admissionSessionKey(request)
		if controller.teardown[key]+request.InFlight > controller.limits.TeardownInFlightPerSession {
			return nil, ErrProviderOverloaded
		}
		controller.teardown[key] += request.InFlight
		return &admissionLease{release: func() { controller.releaseTeardown(key, request.InFlight) }}, nil
	}

	sessionKey := admissionSessionKey(request)
	providerKey := admissionProviderKey(request)
	session := addAdmission(controller.sessions[sessionKey], request)
	provider := addAdmission(controller.providers[providerKey], request)
	environment := addAdmission(controller.environments[request.EnvironmentID], request)
	if session.Views > controller.limits.ViewsPerSession ||
		session.Handles > controller.limits.HandlesPerSession ||
		session.InFlight > controller.limits.InFlightPerSession ||
		session.QueuedBytes > controller.limits.QueuedBytesPerSession ||
		provider.Handles > controller.limits.HandlesPerProvider ||
		environment.Views > controller.limits.ViewsPerEnvironment ||
		environment.InFlight > controller.limits.InFlightGlobal ||
		environment.QueuedBytes > controller.limits.QueuedBytesGlobal {
		return nil, ErrProviderOverloaded
	}
	controller.sessions[sessionKey] = session
	controller.providers[providerKey] = provider
	controller.environments[request.EnvironmentID] = environment
	return &admissionLease{release: func() { controller.releaseOrdinary(request) }}, nil
}

func (controller *admissionController) Snapshot() AdmissionSnapshot {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	var total AdmissionSnapshot
	for _, usage := range controller.environments {
		total.Views += usage.Views
		total.Handles += usage.Handles
		total.InFlight += usage.InFlight
		total.QueuedBytes += usage.QueuedBytes
	}
	return total
}

type admissionLease struct {
	once    sync.Once
	release func()
}

func (lease *admissionLease) Release() {
	if lease == nil {
		return
	}
	lease.once.Do(lease.release)
}

func (controller *admissionController) releaseOrdinary(request AdmissionRequest) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	sessionKey := admissionSessionKey(request)
	providerKey := admissionProviderKey(request)
	controller.sessions[sessionKey] = subtractAdmission(controller.sessions[sessionKey], request)
	controller.providers[providerKey] = subtractAdmission(controller.providers[providerKey], request)
	controller.environments[request.EnvironmentID] = subtractAdmission(controller.environments[request.EnvironmentID], request)
	if emptyAdmission(controller.sessions[sessionKey]) {
		delete(controller.sessions, sessionKey)
	}
	if emptyAdmission(controller.providers[providerKey]) {
		delete(controller.providers, providerKey)
	}
	if emptyAdmission(controller.environments[request.EnvironmentID]) {
		delete(controller.environments, request.EnvironmentID)
	}
}

func (controller *admissionController) releaseTeardown(key string, count int) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.teardown[key] -= count
	if controller.teardown[key] <= 0 {
		delete(controller.teardown, key)
	}
}

func admissionSessionKey(request AdmissionRequest) string {
	return request.EnvironmentID + "\x00" + request.ProviderID + "\x00" + request.SessionID
}

func admissionProviderKey(request AdmissionRequest) string {
	return request.EnvironmentID + "\x00" + request.ProviderID
}

func addAdmission(current AdmissionSnapshot, request AdmissionRequest) AdmissionSnapshot {
	current.Views += request.Views
	current.Handles += request.Handles
	current.InFlight += request.InFlight
	current.QueuedBytes += request.QueuedBytes
	return current
}

func subtractAdmission(current AdmissionSnapshot, request AdmissionRequest) AdmissionSnapshot {
	current.Views -= request.Views
	current.Handles -= request.Handles
	current.InFlight -= request.InFlight
	current.QueuedBytes -= request.QueuedBytes
	return current
}

func emptyAdmission(value AdmissionSnapshot) bool {
	return value.Views == 0 && value.Handles == 0 && value.InFlight == 0 && value.QueuedBytes == 0
}
