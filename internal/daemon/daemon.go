package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	lockName  = "daemon.lock"
	auditName = "daemon-audit.jsonl"
)

// errAlreadyRunning is returned when a live daemon already serves the store.
var errAlreadyRunning = errors.New("daemon: already running for this store")

// IsAlreadyRunning reports whether err indicates an existing live daemon.
func IsAlreadyRunning(err error) bool { return errors.Is(err, errAlreadyRunning) }

// Options configures a daemon start.
type Options struct {
	Store      profile.Store
	TTL        time.Duration
	Now        func() time.Time
	RunBackend manager.RunBackendFactory
	RunOpener  manager.RunOpenerFactory
	// LiveResources lists resources that could survive a restart (running
	// environments). Defaults to none; the daemon reports any it cannot prove it
	// owns as orphans. Injectable for tests.
	LiveResources func(storeRoot string) []LiveResource
}

// Daemon is the single per-store local control-plane process.
type Daemon struct {
	store      profile.Store
	runtimeDir string
	socket     string
	token      string
	startedAt  time.Time
	api        manager.API
	audit      *auditLog
	bus        *eventBus
	bg         *bgRegistry
	own        *ownership
	orphans    []LiveResource
	ln         net.Listener
	server     *http.Server
	uiServer   *http.Server
	uiURL      string
	lockFile   *os.File
	tailStop   chan struct{}
	done       chan struct{}

	mu    sync.Mutex
	state string
}

// Start acquires single-instance ownership, mints the operator token, opens the
// persistent daemon audit log, and serves the parity-locked Manager API over the
// store-rooted Unix socket. It fails closed on placement, permission, or
// single-instance violations. It returns errAlreadyRunning if a live daemon is
// already serving the store (check with IsAlreadyRunning).
func Start(opts Options) (*Daemon, error) {
	if opts.Store.Root == "" {
		return nil, errors.New("daemon: store root is required")
	}
	dir, err := ensurePlacement(opts.Store.Root)
	if err != nil {
		return nil, err
	}
	if probeSocket(filepath.Join(dir, socketName)) {
		return nil, errAlreadyRunning
	}
	lockFile, err := acquireLock(filepath.Join(dir, lockName))
	if err != nil {
		return nil, err
	}
	// The lock is held, so no live daemon owns the store; a stale socket file from a
	// crash can be safely reclaimed.
	_ = os.Remove(filepath.Join(dir, socketName))

	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	ttl := opts.TTL
	if ttl == 0 {
		ttl = 15 * time.Minute
	}
	token, err := mintToken(dir)
	if err != nil {
		releaseLock(lockFile, filepath.Join(dir, lockName))
		return nil, err
	}
	al, err := openAuditLog(filepath.Join(dir, auditName))
	if err != nil {
		releaseLock(lockFile, filepath.Join(dir, lockName))
		return nil, err
	}
	ln, sock, err := listenSocket(opts.Store.Root)
	if err != nil {
		_ = al.close()
		releaseLock(lockFile, filepath.Join(dir, lockName))
		return nil, err
	}
	bus := newEventBus()
	al.publish = bus.publishAudit
	core := manager.New(opts.Store)
	core.Observer = bus
	d := &Daemon{
		store:      opts.Store,
		runtimeDir: dir,
		socket:     sock,
		token:      token,
		startedAt:  now,
		audit:      al,
		bus:        bus,
		bg:         newBGRegistry(),
		own:        newOwnership(),
		ln:         ln,
		lockFile:   lockFile,
		done:       make(chan struct{}),
		state:      "serving",
		api: manager.API{
			Core:         core,
			Token:        token,
			ExpiresAt:    now.Add(ttl),
			AllowedHosts: []string{"localhost", "hideoutd"},
			Now:          opts.Now,
			RunBackend:   opts.RunBackend,
			RunOpener:    opts.RunOpener,
		},
	}
	d.server = &http.Server{Handler: d.buildHandler()}
	d.startAuditTail()
	d.startLoopbackUI()
	d.audit.record("daemon.start", "allow", map[string]any{"socket": sock})

	// Restart fail-closed: any live resource the current instance cannot prove it
	// owns (it owns nothing at start) is reported and audited as an orphan — never
	// silently re-adopted and never destroyed.
	if opts.LiveResources != nil {
		d.orphans = d.own.detectOrphans(opts.LiveResources(opts.Store.Root))
		for _, res := range d.orphans {
			d.audit.record("daemon.orphan", "deny", map[string]any{
				"resource": res.ID,
				"kind":     res.Kind,
				"reason":   "live resource not owned by current instance; not re-adopted, not destroyed",
			})
		}
	}

	go func() { _ = d.server.Serve(ln) }()
	return d, nil
}

// SubmitBackground runs an allowed background operation (v1: environment
// stop/clean) in a daemon goroutine with queryable status, recording ownership
// for the current instance. It rejects any other op class.
func (d *Daemon) SubmitBackground(op string, run func(context.Context) error) (string, error) {
	id, err := d.bg.submit(op, run)
	if err != nil {
		return "", err
	}
	d.own.record(id, d.startedAt.Format(time.RFC3339))
	return id, nil
}

// BackgroundStatusOf returns the status of a background operation.
func (d *Daemon) BackgroundStatusOf(id string) (string, bool) {
	return d.bg.status(id)
}

// Orphans returns the live resources reported as orphans at start.
func (d *Daemon) Orphans() []LiveResource {
	return append([]LiveResource(nil), d.orphans...)
}

// Socket returns the daemon's Unix socket path.
func (d *Daemon) Socket() string { return d.socket }

// Token returns the current operator token.
func (d *Daemon) Token() string { return d.token }

// RuntimeDir returns the daemon's runtime directory.
func (d *Daemon) RuntimeDir() string { return d.runtimeDir }

// Status returns the current daemon status inventory.
func (d *Daemon) Status() Status {
	d.mu.Lock()
	state := d.state
	d.mu.Unlock()
	return Status{
		Version:    statusVersion,
		State:      state,
		StartedAt:  d.startedAt.Format(time.RFC3339),
		Transport:  StatusTransport{Socket: d.socket},
		Background: d.bg.inventory(),
	}
}

// Stop performs an ordered shutdown: drain in-flight requests, record the stop,
// and remove the socket and lock. It is idempotent.
func (d *Daemon) Stop(ctx context.Context) error {
	d.mu.Lock()
	if d.state == "stopping" || d.state == "stopped" {
		d.mu.Unlock()
		return nil
	}
	d.state = "stopping"
	d.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	if d.bg != nil {
		// Ordered, fail-closed: in-flight background work finishes or is cancelled
		// and failed within a bound; stop never hangs on a stuck op.
		drainTimeout := 5 * time.Second
		if dl, ok := ctx.Deadline(); ok {
			if remaining := time.Until(dl); remaining > 0 {
				drainTimeout = remaining
			}
		}
		d.bg.drain(drainTimeout)
	}
	if d.tailStop != nil {
		close(d.tailStop)
	}
	if d.bus != nil {
		d.bus.closeAll()
	}
	if d.uiServer != nil {
		_ = d.uiServer.Shutdown(ctx)
	}
	if d.server != nil {
		_ = d.server.Shutdown(ctx)
	}
	d.audit.record("daemon.stop", "allow", map[string]any{"socket": d.socket})
	_ = d.audit.close()
	_ = os.Remove(d.socket)
	releaseLock(d.lockFile, filepath.Join(d.runtimeDir, lockName))

	d.mu.Lock()
	d.state = "stopped"
	d.mu.Unlock()
	close(d.done)
	return nil
}

// Done returns a channel closed when the daemon has fully stopped (for example
// after a /daemon/stop request), so a foreground `daemon start` can exit.
func (d *Daemon) Done() <-chan struct{} { return d.done }
