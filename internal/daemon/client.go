package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
)

// SocketPath returns the daemon socket path for a store root (client side).
func SocketPath(storeRoot string) string { return socketPathFor(storeRoot) }

// DialClient returns an HTTP client, base URL, and operator token for talking to
// a running daemon over its store socket. It reads the current operator token and
// fails closed if no daemon token is present (no running daemon).
func DialClient(storeRoot string) (client *http.Client, baseURL, token string, err error) {
	tok, err := readToken(storeRoot)
	if err != nil {
		return nil, "", "", fmt.Errorf("daemon: no running daemon for this store: %w", err)
	}
	sock := socketPathFor(storeRoot)
	c := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		},
	}
	return c, "http://localhost", tok, nil
}

// SubscribeEvents connects to the event stream at the exact sequence of the
// authoritative snapshot already held by the caller. Registration and sequence
// validation are atomic on the server; a conflict requires a fresh snapshot.
// The channel closes when the stream ends (daemon stop / credential expiry).
func SubscribeEvents(
	ctx context.Context,
	storeRoot string,
	since int,
) (<-chan liveconsole.Event, error) {
	if since < 0 {
		return nil, errors.New("daemon events: snapshot sequence must be non-negative")
	}
	client, base, token, err := DialClient(storeRoot)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("since", strconv.Itoa(since))
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		base+"/daemon/events?"+values.Encode(),
		nil,
	)
	if err != nil {
		return nil, err
	}
	req.Host = "localhost"
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("daemon events: %s", resp.Status)
	}
	ch := make(chan liveconsole.Event, 8)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data: ") {
				var ev liveconsole.Event
				if err := json.Unmarshal([]byte(strings.TrimPrefix(sc.Text(), "data: ")), &ev); err != nil {
					return
				}
				if err := liveconsole.ValidateEvent(ev); err != nil {
					return
				}
				select {
				case ch <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return ch, nil
}

// FetchOperatorSnapshot returns the sole authoritative seed for v2 event
// reduction. Callers must replace their local projection rather than merging
// this result with prior state.
func FetchOperatorSnapshot(
	ctx context.Context,
	storeRoot string,
	query manager.OperatorSnapshotQuery,
) (manager.OperatorSnapshot, error) {
	if err := query.Validate(); err != nil {
		return manager.OperatorSnapshot{}, err
	}
	client, base, token, err := DialClient(storeRoot)
	if err != nil {
		return manager.OperatorSnapshot{}, err
	}
	values := url.Values{}
	if query.Profile != "" {
		values.Set("profile", query.Profile)
	}
	if query.Session != "" {
		values.Set("session", query.Session)
	}
	values.Set("activityLimit", strconv.Itoa(query.ActivityLimit))
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		base+"/api/v1/operator/snapshot?"+values.Encode(),
		nil,
	)
	if err != nil {
		return manager.OperatorSnapshot{}, err
	}
	req.Host = "localhost"
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return manager.OperatorSnapshot{}, err
	}
	defer resp.Body.Close()
	var envelope struct {
		Version  string                   `json:"version"`
		Resource string                   `json:"resource"`
		Data     manager.OperatorSnapshot `json:"data"`
		Errors   []string                 `json:"errors"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 8<<20))
	if err := decoder.Decode(&envelope); err != nil {
		return manager.OperatorSnapshot{}, fmt.Errorf("decode operator snapshot: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		message := strings.Join(envelope.Errors, "; ")
		if message == "" {
			message = resp.Status
		}
		return manager.OperatorSnapshot{}, fmt.Errorf("operator snapshot: %s", message)
	}
	if envelope.Version != manager.APIVersion || envelope.Resource != "operator/snapshot" {
		return manager.OperatorSnapshot{}, errors.New("operator snapshot response contract mismatch")
	}
	if err := envelope.Data.Validate(); err != nil {
		return manager.OperatorSnapshot{}, fmt.Errorf("validate operator snapshot: %w", err)
	}
	return envelope.Data, nil
}

// FetchStatus returns the authenticated daemon seed state used before clients
// switch to event-only updates.
func FetchStatus(ctx context.Context, storeRoot string) (Status, error) {
	client, base, token, err := DialClient(storeRoot)
	if err != nil {
		return Status{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+statusPath, nil)
	if err != nil {
		return Status{}, err
	}
	req.Host = "localhost"
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return Status{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Status{}, fmt.Errorf("daemon status: %s", resp.Status)
	}
	var status Status
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return Status{}, fmt.Errorf("decode daemon status: %w", err)
	}
	return status, nil
}

// BrowserUIURL binds a daemon-advertised, non-secret loopback base URL to the
// current store credential. The returned credential stays in the fragment, so
// it is not sent in the initial HTTP request. A stale or foreign status is
// rejected before any URL is handed to a browser.
func BrowserUIURL(storeRoot string, status Status) (string, error) {
	if !sameSocketPath(status.Transport.Socket, socketPathFor(storeRoot)) {
		return "", errors.New("daemon browser console status belongs to another store")
	}
	base := status.Transport.BrowserURL
	parsed, err := url.ParseRequestURI(base)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" ||
		parsed.User != nil || parsed.Path != "/" || parsed.RawPath != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("daemon browser console did not publish a valid loopback URL")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("daemon browser console did not publish a valid loopback port")
	}
	_, _, token, err := DialClient(storeRoot)
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", errors.New("daemon browser console credential is unavailable")
	}
	return base + "#token=" + url.QueryEscape(token), nil
}

// StopRunning requests an ordered shutdown and returns only after the exact
// daemon instance named by the receipt no longer owns the store. A successful
// return therefore permits an immediate restart without racing the old owner.
func StopRunning(ctx context.Context, storeRoot string) error {
	if ctx == nil {
		return errors.New("daemon stop requires a bounded context")
	}
	client, base, token, err := DialClient(storeRoot)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+stopPath, nil)
	if err != nil {
		return err
	}
	req.Host = "localhost"
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request daemon stop: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return fmt.Errorf("daemon stop failed (%s): %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var receipt StopReceipt
	if err := json.NewDecoder(resp.Body).Decode(&receipt); err != nil {
		return fmt.Errorf("decode daemon stop receipt: %w", err)
	}
	if receipt.Version != stopReceiptVersion || receipt.Status != "stopping" || strings.TrimSpace(receipt.InstanceID) == "" {
		return fmt.Errorf("invalid daemon stop receipt: version=%q instance=%q status=%q", receipt.Version, receipt.InstanceID, receipt.Status)
	}
	return waitForStoppedInstance(ctx, storeRoot, receipt.InstanceID)
}

func waitForStoppedInstance(ctx context.Context, storeRoot, instanceID string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, statusErr := FetchStatus(ctx, storeRoot)
		if statusErr == nil {
			if status.InstanceID != instanceID {
				return nil
			}
		} else {
			released, err := daemonInstanceLockReleased(storeRoot)
			if err != nil {
				return fmt.Errorf("prove daemon shutdown ownership: %w", err)
			}
			if released {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("daemon instance %s did not complete ordered shutdown: %w", instanceID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func daemonInstanceLockReleased(storeRoot string) (bool, error) {
	path := filepath.Join(runtimeDir(storeRoot), lockName)
	lockFile, err := acquireLock(path)
	if err == nil {
		releaseLock(lockFile, path)
		return true, nil
	}
	if IsAlreadyRunning(err) {
		return false, nil
	}
	return false, err
}
