package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
)

var (
	errInitDaemonUnavailable = errors.New("authenticated init daemon is unavailable")
	initDaemonPrepare        = func(ctx context.Context, store profile.Store, request manager.InitServiceRequest) (manager.PreparedInit, error) {
		var prepared manager.PreparedInit
		err := initDaemonRequest(ctx, store.Root, "/api/v1/init/plan", manager.InitAPIRequest{Request: &request}, &prepared)
		return prepared, err
	}
	initDaemonApply = func(ctx context.Context, store profile.Store, prepared manager.PreparedInit, confirmation *manager.InitConfirmation) (manager.InitApplyResult, error) {
		var result manager.InitApplyResult
		err := initDaemonRequest(ctx, store.Root, "/api/v1/init/apply", manager.InitAPIRequest{
			Prepared: &prepared, Confirmation: confirmation,
		}, &result)
		return result, err
	}
)

func (a app) ensureInitDaemon(ctx context.Context, store profile.Store) error {
	if a.initPrepare != nil || a.initApply != nil {
		if a.initPrepare == nil || a.initApply == nil {
			return errors.New("incomplete init authority test seam")
		}
		if a.ensureDaemon == nil {
			return nil
		}
	}
	executableFn := a.daemonExecutable
	if executableFn == nil {
		executableFn = runExecutable
	}
	executable, err := executableFn()
	if err != nil {
		return fmt.Errorf("resolve hideout executable: %w", err)
	}
	ensure := a.ensureDaemon
	if ensure == nil {
		ensure = ensureRunDaemon
	}
	_, err = ensure(ctx, daemon.EnsureStartedOptions{
		Store: store, Executable: executable, BuildID: daemonBuildID(), Diagnostics: a.stderr,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", errInitDaemonUnavailable, err)
	}
	return nil
}

func (a app) prepareInit(ctx context.Context, store profile.Store, request manager.InitServiceRequest) (manager.PreparedInit, error) {
	if a.initPrepare != nil {
		return a.initPrepare(ctx, store, request)
	}
	return initDaemonPrepare(ctx, store, request)
}

func (a app) applyInit(ctx context.Context, store profile.Store, prepared manager.PreparedInit, confirmation *manager.InitConfirmation) (manager.InitApplyResult, error) {
	if a.initApply != nil {
		return a.initApply(ctx, store, prepared, confirmation)
	}
	return initDaemonApply(ctx, store, prepared, confirmation)
}

func initDaemonRequest(ctx context.Context, storeRoot, path string, body, target any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode init request: %w", err)
	}
	client, base, token, err := daemon.DialClient(storeRoot)
	if err != nil {
		return fmt.Errorf("%w: %v", errInitDaemonUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Host = "localhost"
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", errInitDaemonUnavailable, err)
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if readErr != nil {
		return fmt.Errorf("%w: read response: %v", errInitDaemonUnavailable, readErr)
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []string        `json:"errors"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("%w: decode response: %v", errInitDaemonUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		requestErr := fmt.Errorf("daemon request failed (%s): %s", resp.Status, initErrorText(envelope.Errors, data))
		if resp.StatusCode == http.StatusBadRequest {
			return requestErr
		}
		return fmt.Errorf("%w: %v", errInitDaemonUnavailable, requestErr)
	}
	if len(envelope.Errors) != 0 {
		return errors.New(strings.Join(envelope.Errors, "; "))
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("%w: response omitted init data", errInitDaemonUnavailable)
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.Data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: decode response data: %v", errInitDaemonUnavailable, err)
	}
	return nil
}

func initErrorText(messages []string, raw []byte) string {
	if len(messages) != 0 {
		return strings.Join(messages, "; ")
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "empty response"
	}
	return text
}
