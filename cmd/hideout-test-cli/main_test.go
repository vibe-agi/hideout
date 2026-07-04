package main

import (
	"net"
	"net/http"
	"net/http/httptest"
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
