package main

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTestCLILoginStatusAndRequest(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("HOME", t.TempDir())

	if err := run([]string{"login", "--self-callback"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	if err := run([]string{"status"}); err != nil {
		t.Fatalf("status: %v", err)
	}

	seenAuth := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("auth-ok\n"))
	}))
	defer server.Close()

	if err := run([]string{"request", "--url", server.URL}); err != nil {
		t.Fatalf("request: %v", err)
	}
	if seenAuth != "Bearer "+tokenValue {
		t.Fatalf("request authorization = %q, want bearer token", seenAuth)
	}
}

func TestTestCLIRequestRequiresLogin(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request should not reach server without token")
	}))
	defer server.Close()

	if err := run([]string{"request", "--url", server.URL}); err == nil {
		t.Fatal("request should fail before login")
	}
}

func TestTestCLILoginExpectedTimeout(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	if err := run([]string{"login", "--wait", (10 * time.Millisecond).String(), "--expect-timeout"}); err != nil {
		t.Fatalf("expected timeout login should succeed: %v", err)
	}
	if err := run([]string{"status"}); err == nil {
		t.Fatal("expected timeout login must not create auth state")
	}
}

func TestTestCLILoginBrowserRedirect(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	address := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close probe listener: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- run([]string{"login", "--listen", address, "--browser-redirect", "--wait", (2 * time.Second).String()})
	}()

	client := http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.After(2 * time.Second)
	for {
		resp, err := client.Get("http://" + address + "/")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("browser redirect final status=%d", resp.StatusCode)
			}
			break
		}
		select {
		case err := <-done:
			t.Fatalf("login exited before browser callback: %v", err)
		case <-deadline:
			t.Fatalf("browser redirect never became reachable: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("login: %v", err)
	}
	if err := run([]string{"status"}); err != nil {
		t.Fatalf("status: %v", err)
	}
}

func TestTestCLIEnvAndHomeProbes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("VISIBLE_TO_TEST_CLI", "secret-value")

	if err := run([]string{"env", "--key", "VISIBLE_TO_TEST_CLI"}); err != nil {
		t.Fatalf("env present: %v", err)
	}
	if err := run([]string{"env", "--key", "MISSING_FROM_TEST_CLI"}); err != nil {
		t.Fatalf("env missing: %v", err)
	}
	if err := run([]string{"home"}); err != nil {
		t.Fatalf("home: %v", err)
	}
}

func TestTestCLIWorkloadWritesOutputAndReachesEndpoint(t *testing.T) {
	workspace := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}
	defer func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()

	if err := os.WriteFile("task.txt", []byte("write the expected result\n"), 0o644); err != nil {
		t.Fatalf("write task: %v", err)
	}

	seenPath := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	stdout, err := captureStdout(func() error {
		return run([]string{
			"workload",
			"--task", "task.txt",
			"--output", "result.txt",
			"--expected", "done\n",
			"--url", server.URL + "/workload",
			"--expect-status", "204",
		})
	})
	if err != nil {
		t.Fatalf("workload: %v", err)
	}
	if seenPath != "/workload" {
		t.Fatalf("endpoint path = %q, want /workload", seenPath)
	}
	data, err := os.ReadFile("result.txt")
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != "done\n" {
		t.Fatalf("output = %q, want expected content", string(data))
	}
	for _, marker := range []string{
		"workspace-updated=yes",
		"success-check=passed",
		"endpoint=reachable",
		"http_status=204",
	} {
		if !strings.Contains(stdout, marker) {
			t.Fatalf("stdout missing marker %q in:\n%s", marker, stdout)
		}
	}
	if strings.Contains(stdout, server.URL) {
		t.Fatalf("stdout leaked endpoint URL: %s", stdout)
	}
}

func TestTestCLIWorkloadRejectsOutsideWorkspaceOutput(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}
	defer func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	if err := os.WriteFile("task.txt", []byte("task\n"), 0o644); err != nil {
		t.Fatalf("write task: %v", err)
	}

	err = run([]string{
		"workload",
		"--task", "task.txt",
		"--output", filepath.Join(root, "outside.txt"),
		"--expected", "done\n",
		"--url", "http://127.0.0.1:1/workload",
	})
	if err == nil {
		t.Fatal("workload should reject output outside workspace")
	}
}

func TestTestCLIWorkloadRequiresEndpoint(t *testing.T) {
	workspace := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}
	defer func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	}()
	if err := os.WriteFile("task.txt", []byte("task\n"), 0o644); err != nil {
		t.Fatalf("write task: %v", err)
	}

	err = run([]string{
		"workload",
		"--task", "task.txt",
		"--output", "result.txt",
		"--expected", "done\n",
	})
	if err == nil {
		t.Fatal("workload should require endpoint URL")
	}
}

func captureStdout(fn func() error) (string, error) {
	old := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = writer
	err = fn()
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	os.Stdout = old
	var buf bytes.Buffer
	if _, copyErr := io.Copy(&buf, reader); err == nil {
		err = copyErr
	}
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	return buf.String(), err
}
