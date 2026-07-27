package hostcap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxDarwinIdentityCommandOutput = 256 << 10
	// Gatekeeper serializes some spctl assessments. Under concurrent Lima and
	// GUI startup load a valid notarized app can exceed ten seconds even though
	// the same assessment completes immediately afterward. Keep the complete
	// identity observation bounded, but leave enough time for the host trust
	// service to answer.
	darwinIdentityOperationTimeout = 30 * time.Second
	// System policy assessment is materially slower than signature integrity
	// verification. Cache only a successful assessment for the exact observed
	// bundle path and signing facts; codesign verify/display still run on every
	// observation and any identity change gets a different key.
	darwinTrustCacheTTL = 5 * time.Minute
)

var errDarwinIdentityOutputLimit = errors.New("darwin identity command output exceeded its Core limit")

type boundedDarwinIdentityOutput struct {
	buffer   bytes.Buffer
	exceeded bool
}

func (b *boundedDarwinIdentityOutput) Write(data []byte) (int, error) {
	written := len(data)
	remaining := maxDarwinIdentityCommandOutput - b.buffer.Len()
	if remaining > 0 {
		if remaining > len(data) {
			remaining = len(data)
		}
		_, _ = b.buffer.Write(data[:remaining])
	}
	if remaining < len(data) {
		b.exceeded = true
	}
	return written, nil
}

func (b *boundedDarwinIdentityOutput) Bytes() []byte { return b.buffer.Bytes() }

type darwinIdentityCommandRunner func(context.Context, string, ...string) ([]byte, error)

type darwinTrustCache struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

var processDarwinTrustCache = &darwinTrustCache{entries: map[string]time.Time{}}

func execDarwinIdentityCommand(ctx context.Context, executable string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	// codesign/spctl may delegate work while inheriting the output pipes. Kill
	// the isolated process group on timeout so a helper cannot keep
	// output collection blocked after the direct process exits.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	cmd.WaitDelay = 500 * time.Millisecond
	var output boundedDarwinIdentityOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	if output.exceeded {
		return output.Bytes(), errDarwinIdentityOutputLimit
	}
	return output.Bytes(), err
}

// ObserveDarwinSigningIdentity validates the bundle with the host codesign
// service and then reads signing facts. It never accepts a caller-supplied
// requirement as an authentication anchor.
func ObserveDarwinSigningIdentity(bundlePath string) (SigningObservation, error) {
	return ObserveDarwinSigningIdentityContext(context.Background(), bundlePath)
}

// ObserveDarwinSigningIdentityContext binds every codesign and Gatekeeper step
// to one caller-cancellable operation budget. A slow trust service therefore
// cannot outlive the broker request and cross the host-effect boundary later.
func ObserveDarwinSigningIdentityContext(ctx context.Context, bundlePath string) (SigningObservation, error) {
	return observeDarwinSigningIdentityCachedContext(ctx, bundlePath, execDarwinIdentityCommand, darwinIdentityOperationTimeout, processDarwinTrustCache, time.Now().UTC())
}

func observeDarwinSigningIdentity(bundlePath string, run darwinIdentityCommandRunner) (SigningObservation, error) {
	return observeDarwinSigningIdentityWithTimeout(bundlePath, run, darwinIdentityOperationTimeout)
}

func observeDarwinSigningIdentityWithTimeout(bundlePath string, run darwinIdentityCommandRunner, timeout time.Duration) (SigningObservation, error) {
	return observeDarwinSigningIdentityCached(bundlePath, run, timeout, nil, time.Time{})
}

func observeDarwinSigningIdentityCached(bundlePath string, run darwinIdentityCommandRunner, timeout time.Duration, cache *darwinTrustCache, now time.Time) (SigningObservation, error) {
	return observeDarwinSigningIdentityCachedContext(context.Background(), bundlePath, run, timeout, cache, now)
}

func observeDarwinSigningIdentityCachedContext(parent context.Context, bundlePath string, run darwinIdentityCommandRunner, timeout time.Duration, cache *darwinTrustCache, now time.Time) (SigningObservation, error) {
	if run == nil {
		return SigningObservation{}, errors.New("darwin identity command runner is unavailable")
	}
	if timeout <= 0 {
		return SigningObservation{}, errors.New("darwin identity command timeout must be positive")
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if output, err := runDarwinIdentityStep(ctx, run, "/usr/bin/codesign", "--verify", "--strict", "--all-architectures", bundlePath); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return SigningObservation{}, fmt.Errorf("codesign verification timed out: %w", context.DeadlineExceeded)
		}
		if errors.Is(err, errDarwinIdentityOutputLimit) {
			return SigningObservation{}, err
		}
		message := strings.TrimSpace(string(output))
		if strings.Contains(message, "code object is not signed at all") || strings.Contains(message, "not signed") {
			return SigningObservation{}, nil
		}
		return SigningObservation{}, fmt.Errorf("codesign verification failed: %w", err)
	}
	output, err := runDarwinIdentityStep(ctx, run, "/usr/bin/codesign", "--display", "--verbose=4", bundlePath)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return SigningObservation{}, fmt.Errorf("codesign identity observation timed out: %w", context.DeadlineExceeded)
		}
		return SigningObservation{}, fmt.Errorf("codesign identity observation failed: %w", err)
	}
	facts, err := parseDarwinSigningFacts(string(output))
	if err != nil {
		return SigningObservation{}, err
	}
	trustKey := strings.Join([]string{bundlePath, facts.BundleID, facts.TeamID, facts.CodeIdentity}, "\x00")
	if cache != nil && cache.accepted(trustKey, now) {
		facts.Trusted = true
		facts.TrustAnchor = "macos-system-policy"
		return facts, nil
	}
	if _, trustErr := runDarwinIdentityStep(ctx, run, "/usr/sbin/spctl", "--assess", "--type", "execute", "--verbose=4", bundlePath); trustErr == nil {
		facts.Trusted = true
		facts.TrustAnchor = "macos-system-policy"
		if cache != nil {
			cache.remember(trustKey, now)
		}
	} else if errors.Is(trustErr, context.DeadlineExceeded) {
		return SigningObservation{}, fmt.Errorf("macOS system trust assessment timed out: %w", context.DeadlineExceeded)
	}
	return facts, nil
}

func (c *darwinTrustCache) accepted(key string, now time.Time) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	expiresAt, ok := c.entries[key]
	if !ok || !now.Before(expiresAt) {
		delete(c.entries, key)
		return false
	}
	return true
}

func (c *darwinTrustCache) remember(key string, now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]time.Time{}
	}
	c.entries[key] = now.Add(darwinTrustCacheTTL)
}

func runDarwinIdentityStep(ctx context.Context, run darwinIdentityCommandRunner, executable string, args ...string) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("darwin identity command context is required")
	}
	output, err := run(ctx, executable, args...)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return output, context.DeadlineExceeded
	}
	return output, err
}

func parseDarwinSigningFacts(output string) (SigningObservation, error) {
	values := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			values[key] = strings.TrimSpace(value)
		}
	}
	if values["Identifier"] == "" || values["CDHash"] == "" {
		return SigningObservation{}, errors.New("codesign output omitted required observed identity facts")
	}
	teamID := values["TeamIdentifier"]
	if teamID == "not set" {
		teamID = ""
	}
	return SigningObservation{
		Signed:       true,
		BundleID:     values["Identifier"],
		TeamID:       teamID,
		CodeIdentity: values["CDHash"],
	}, nil
}
