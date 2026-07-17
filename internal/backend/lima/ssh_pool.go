package lima

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const sshPoolHealthTimeout = 2 * time.Second

type sshPoolKey struct {
	instance                         string
	hostName                         string
	port                             string
	user                             string
	identityFile                     string
	userKnownHostsFile               string
	strictHostKeyChecking            string
	noHostAuthenticationForLocalhost string
}

type sshPoolEntry struct {
	client *ssh.Client
	refs   int
}

// SSHClientPool owns daemon-scoped Lima SSH transports. Individual runs own
// SSH channels through a lease; they never own or close a shared transport.
// The pool is process-local and carries no capability or persisted authority.
type SSHClientPool struct {
	mu      sync.Mutex
	closed  bool
	entries map[sshPoolKey]*sshPoolEntry
	check   func(context.Context, *ssh.Client) error
	close   func(*ssh.Client) error
}

// NewSSHClientPool creates a bounded-by-instance transport pool. A single SSH
// connection can multiplex concurrent run, setup, and bridge channels.
func NewSSHClientPool() *SSHClientPool {
	return newSSHClientPool(checkPooledSSHClient, func(client *ssh.Client) error {
		if client == nil {
			return nil
		}
		return client.Close()
	})
}

func newSSHClientPool(check func(context.Context, *ssh.Client) error, closeClient func(*ssh.Client) error) *SSHClientPool {
	return &SSHClientPool{
		entries: make(map[sshPoolKey]*sshPoolEntry),
		check:   check,
		close:   closeClient,
	}
}

type sshClientLease struct {
	pool   *SSHClientPool
	key    sshPoolKey
	client *ssh.Client
	once   sync.Once
}

func (l *sshClientLease) Client() *ssh.Client {
	if l == nil {
		return nil
	}
	return l.client
}

// Close releases the channel owner's reference. Pooled transports remain live
// for reuse; a direct, non-pooled lease closes its transport here.
func (l *sshClientLease) Close() error {
	if l == nil {
		return nil
	}
	var err error
	l.once.Do(func() {
		if l.pool == nil {
			if l.client != nil {
				err = l.client.Close()
			}
			return
		}
		err = l.pool.release(l.key, l.client)
	})
	return err
}

// Invalidate removes a transport that has proved unusable. This is reserved
// for connection-level failure or an observed VM lifecycle transition.
func (l *sshClientLease) Invalidate() error {
	if l == nil {
		return nil
	}
	var err error
	l.once.Do(func() {
		if l.pool == nil {
			if l.client != nil {
				err = l.client.Close()
			}
			return
		}
		err = l.pool.invalidate(l.key, l.client)
	})
	return err
}

func (p *SSHClientPool) acquire(
	ctx context.Context,
	key sshPoolKey,
	connect func() (*ssh.Client, error),
) (*sshClientLease, error) {
	if p == nil {
		return nil, errors.New("Lima SSH pool is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("Lima SSH pool is closed")
	}
	if existing := p.entries[key]; existing != nil {
		if existing.refs == 0 && p.check != nil {
			if err := p.check(ctx, existing.client); err != nil {
				delete(p.entries, key)
				_ = p.closeClient(existing.client)
			} else {
				existing.refs++
				return &sshClientLease{pool: p, key: key, client: existing.client}, nil
			}
		} else {
			existing.refs++
			return &sshClientLease{pool: p, key: key, client: existing.client}, nil
		}
	}

	// A changed Lima SSH endpoint or identity supersedes every idle transport
	// for the same instance/user. Live transports are retained until their
	// owners release or the lifecycle stop path invalidates the instance.
	for candidate, entry := range p.entries {
		if candidate.instance == key.instance && candidate.user == key.user && candidate != key && entry.refs == 0 {
			delete(p.entries, candidate)
			_ = p.closeClient(entry.client)
		}
	}
	client, err := connect()
	if err != nil {
		return nil, err
	}
	p.entries[key] = &sshPoolEntry{client: client, refs: 1}
	return &sshClientLease{pool: p, key: key, client: client}, nil
}

func (p *SSHClientPool) release(key sshPoolKey, client *ssh.Client) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entries[key]
	if entry == nil || entry.client != client {
		return nil
	}
	if entry.refs <= 0 {
		return errors.New("Lima SSH lease reference underflow")
	}
	entry.refs--
	return nil
}

func (p *SSHClientPool) invalidate(key sshPoolKey, client *ssh.Client) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entries[key]
	if entry == nil || entry.client != client {
		return nil
	}
	delete(p.entries, key)
	return p.closeClient(entry.client)
}

// InvalidateInstance closes all transports tied to an instance before a VM
// stop/delete transition. Lifecycle serialization proves no live run owns one.
func (p *SSHClientPool) InvalidateInstance(instance string) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var errs []error
	for key, entry := range p.entries {
		if key.instance != instance {
			continue
		}
		delete(p.entries, key)
		errs = append(errs, p.closeClient(entry.client))
	}
	return errors.Join(errs...)
}

// Close releases every daemon-owned transport. It is idempotent.
func (p *SSHClientPool) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	var errs []error
	for key, entry := range p.entries {
		delete(p.entries, key)
		errs = append(errs, p.closeClient(entry.client))
	}
	return errors.Join(errs...)
}

func (p *SSHClientPool) closeClient(client *ssh.Client) error {
	if p.close == nil || client == nil {
		return nil
	}
	return p.close(client)
}

func checkPooledSSHClient(ctx context.Context, client *ssh.Client) error {
	if client == nil {
		return errors.New("pooled Lima SSH client is nil")
	}
	checkCtx, cancel := context.WithTimeout(ctx, sshPoolHealthTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-checkCtx.Done():
		_ = client.Close()
		return fmt.Errorf("pooled Lima SSH health check: %w", checkCtx.Err())
	}
}

func sshKey(instance string, cfg limaSSHConfig) sshPoolKey {
	return sshPoolKey{
		instance:                         instance,
		hostName:                         cfg.HostName,
		port:                             cfg.Port,
		user:                             cfg.User,
		identityFile:                     cfg.IdentityFile,
		userKnownHostsFile:               cfg.UserKnownHostsFile,
		strictHostKeyChecking:            cfg.StrictHostKeyChecking,
		noHostAuthenticationForLocalhost: cfg.NoHostAuthenticationForLocalhost,
	}
}
