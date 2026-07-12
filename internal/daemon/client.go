package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/vibe-agi/hideout/internal/liveconsole"
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

// SubscribeEvents connects to a running daemon's event stream and returns typed
// live-console events. The channel closes when the stream ends (daemon stop /
// credential expiry). Returns an error if no daemon is reachable, so callers can
// fall back to their daemon-less behavior.
func SubscribeEvents(ctx context.Context, storeRoot string) (<-chan liveconsole.Event, error) {
	client, base, token, err := DialClient(storeRoot)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/daemon/events", nil)
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
				default:
				}
			}
		}
	}()
	return ch, nil
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
