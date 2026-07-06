package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	cliVersion = "hideout-test-cli 1.0"
	tokenValue = "hideout-test-cli-token"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "hideout-test-cli:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: hideout-test-cli <version|login|status|request|env|home|workload>; login supports --browser-redirect for preview/open smoke")
	}
	switch args[0] {
	case "version":
		fmt.Println(cliVersion)
		return nil
	case "login":
		return login(args[1:])
	case "status":
		return status()
	case "request":
		return request(args[1:])
	case "env":
		return envProbe(args[1:])
	case "home":
		return homeProbe()
	case "workload":
		return workload(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listen := fs.String("listen", "127.0.0.1:0", "local callback listen address")
	callbackPath := fs.String("callback-path", "/callback", "local callback path")
	state := fs.String("state", "gate-state", "expected callback state")
	code := fs.String("code", "gate-code", "expected callback code")
	browserRedirect := fs.Bool("browser-redirect", false, "redirect browser root requests to the local callback")
	selfCallback := fs.Bool("self-callback", false, "call the local callback URL from this process")
	expectTimeout := fs.Bool("expect-timeout", false, "treat a missing callback before --wait as success")
	wait := fs.Duration("wait", 10*time.Second, "maximum time to wait for callback")
	if err := fs.Parse(args); err != nil {
		return err
	}
	normalizedCallbackPath, err := normalizeCallbackPath(*callbackPath)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer ln.Close()

	done := make(chan error, 1)
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if *browserRedirect && r.URL.Path == "/" {
				http.Redirect(w, r, relativeCallbackURL(normalizedCallbackPath, *state, *code), http.StatusFound)
				return
			}
			if r.URL.Path != normalizedCallbackPath {
				http.NotFound(w, r)
				return
			}
			if r.URL.Query().Get("state") != *state || r.URL.Query().Get("code") != *code {
				http.Error(w, "bad callback", http.StatusBadRequest)
				done <- errors.New("callback query did not match")
				return
			}
			if err := writeToken(tokenValue); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				done <- err
				return
			}
			w.WriteHeader(http.StatusNoContent)
			done <- nil
		}),
	}
	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			done <- err
		}
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	callbackURL := localCallbackURL(ln.Addr().String(), normalizedCallbackPath, *state, *code)
	fmt.Printf("callback=%s\n", callbackURL)
	if *browserRedirect {
		fmt.Printf("browser=%s\n", localBrowserURL(ln.Addr().String()))
	}
	if *selfCallback {
		client := http.Client{Transport: &http.Transport{Proxy: nil}}
		resp, err := client.Get(callbackURL)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			return fmt.Errorf("callback returned HTTP %d", resp.StatusCode)
		}
	}

	timer := time.NewTimer(*wait)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return err
		}
		fmt.Println("login=ok")
		return nil
	case <-timer.C:
		if *expectTimeout {
			fmt.Println("login=timeout-ok")
			return nil
		}
		return errors.New("callback timed out")
	}
}

func normalizeCallbackPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("callback path is required")
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	if strings.Contains(value, "?") || strings.Contains(value, "#") {
		return "", errors.New("callback path must not include query or fragment")
	}
	return value, nil
}

func localBrowserURL(address string) string {
	return "http://" + address + "/"
}

func localCallbackURL(address, callbackPath, state, code string) string {
	u := url.URL{
		Scheme: "http",
		Host:   address,
		Path:   callbackPath,
	}
	query := u.Query()
	query.Set("state", state)
	query.Set("code", code)
	u.RawQuery = query.Encode()
	return u.String()
}

func relativeCallbackURL(callbackPath, state, code string) string {
	u := url.URL{Path: callbackPath}
	query := u.Query()
	query.Set("state", state)
	query.Set("code", code)
	u.RawQuery = query.Encode()
	return u.String()
}

func status() error {
	token, err := readToken()
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("empty token")
	}
	fmt.Println("status=authenticated")
	return nil
}

func request(args []string) error {
	fs := flag.NewFlagSet("request", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	target := fs.String("url", "", "HTTP URL to request")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*target) == "" {
		return errors.New("request requires --url")
	}
	token, err := readToken()
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, *target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	fmt.Printf("http_status=%d\n", resp.StatusCode)
	if len(body) > 0 {
		fmt.Printf("body=%s", body)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("request failed with HTTP %d", resp.StatusCode)
	}
	return nil
}

func envProbe(args []string) error {
	fs := flag.NewFlagSet("env", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	key := fs.String("key", "", "environment variable name to inspect")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*key) == "" {
		return errors.New("env requires --key")
	}
	value, ok := os.LookupEnv(*key)
	if ok {
		fmt.Printf("env=%s present len=%d\n", *key, len(value))
		return nil
	}
	fmt.Printf("env=%s absent\n", *key)
	return nil
}

func homeProbe() error {
	home := os.Getenv("HOME")
	config := os.Getenv("XDG_CONFIG_HOME")
	cache := os.Getenv("XDG_CACHE_HOME")
	data := os.Getenv("XDG_DATA_HOME")
	for _, item := range []struct {
		name  string
		value string
	}{
		{name: "HOME", value: home},
		{name: "XDG_CONFIG_HOME", value: config},
		{name: "XDG_CACHE_HOME", value: cache},
		{name: "XDG_DATA_HOME", value: data},
	} {
		if item.value == "" {
			fmt.Printf("%s=absent\n", item.name)
			continue
		}
		fmt.Printf("%s=%s\n", item.name, item.value)
	}
	path, err := tokenPath()
	if err != nil {
		return err
	}
	fmt.Printf("TOKEN_PATH=%s\n", path)
	return nil
}

func workload(args []string) error {
	fs := flag.NewFlagSet("workload", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	taskPath := fs.String("task", "", "workspace task file to read")
	outputPath := fs.String("output", "", "workspace output file to create or update")
	expected := fs.String("expected", "", "expected output content")
	endpoint := fs.String("url", "", "HTTP endpoint URL to request")
	expectStatus := fs.Int("expect-status", http.StatusOK, "expected endpoint HTTP status")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*taskPath) == "" {
		return errors.New("workload requires --task")
	}
	if strings.TrimSpace(*outputPath) == "" {
		return errors.New("workload requires --output")
	}
	if *expected == "" {
		return errors.New("workload requires --expected")
	}
	if strings.TrimSpace(*endpoint) == "" {
		return errors.New("workload requires --url")
	}
	if *expectStatus < 100 || *expectStatus > 999 {
		return errors.New("workload --expect-status must be an HTTP status code")
	}

	task, err := resolveExistingWorkspacePath(*taskPath)
	if err != nil {
		return err
	}
	output, err := resolveOutputWorkspacePath(*outputPath)
	if err != nil {
		return err
	}
	taskData, err := os.ReadFile(task)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(taskData)) == "" {
		return errors.New("workload task file is empty")
	}

	if err := os.WriteFile(output, []byte(*expected), 0o644); err != nil {
		return err
	}
	outputData, err := os.ReadFile(output)
	if err != nil {
		return err
	}
	if string(outputData) != *expected {
		return errors.New("workload success check failed")
	}
	fmt.Println("workspace-updated=yes")
	fmt.Println("success-check=passed")

	status, err := requestEndpoint(*endpoint)
	if err != nil {
		return err
	}
	fmt.Printf("http_status=%d\n", status)
	if status != *expectStatus {
		return fmt.Errorf("endpoint returned HTTP %d, want %d", status, *expectStatus)
	}
	fmt.Println("endpoint=reachable")
	return nil
}

func requestEndpoint(rawURL string) (int, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return 0, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return 0, fmt.Errorf("endpoint URL scheme %q is unsupported", parsed.Scheme)
	}
	client := http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	return resp.StatusCode, nil
}

func resolveExistingWorkspacePath(path string) (string, error) {
	resolved, err := resolveWorkspacePath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("workspace path %q is a directory", path)
	}
	return resolved, nil
}

func resolveOutputWorkspacePath(path string) (string, error) {
	resolved, err := resolveWorkspacePath(path)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(resolved)
	if info, err := os.Stat(parent); err != nil {
		return "", err
	} else if !info.IsDir() {
		return "", fmt.Errorf("workspace output parent is not a directory")
	}
	if info, err := os.Lstat(resolved); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("workspace output must not be a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return resolved, nil
}

func resolveWorkspacePath(path string) (string, error) {
	value := strings.TrimSpace(path)
	if value == "" {
		return "", errors.New("workspace path is required")
	}
	workspace, err := os.Getwd()
	if err != nil {
		return "", err
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = resolved
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(workspace, value)
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if !pathWithin(abs, workspace) {
		return "", fmt.Errorf("workspace path %q escapes current workspace", path)
	}
	return abs, nil
}

func pathWithin(path, root string) bool {
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return rel != ""
}

func writeToken(token string) error {
	path, err := tokenPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(token+"\n"), 0o600)
}

func readToken() (string, error) {
	path, err := tokenPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func tokenPath() (string, error) {
	root := os.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", errors.New("HOME is not set")
		}
		root = filepath.Join(home, ".config")
	}
	return filepath.Join(root, "hideout-test-cli", "token"), nil
}
