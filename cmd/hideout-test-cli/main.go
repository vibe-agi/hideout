package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
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
		return errors.New("usage: hideout-test-cli <version|login|status|request|env|home>")
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
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listen := fs.String("listen", "127.0.0.1:0", "local callback listen address")
	selfCallback := fs.Bool("self-callback", false, "call the local callback URL from this process")
	expectTimeout := fs.Bool("expect-timeout", false, "treat a missing callback before --wait as success")
	wait := fs.Duration("wait", 10*time.Second, "maximum time to wait for callback")
	if err := fs.Parse(args); err != nil {
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
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}
			if r.URL.Query().Get("state") != "gate-state" || r.URL.Query().Get("code") != "gate-code" {
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

	callbackURL := fmt.Sprintf("http://%s/callback?state=gate-state&code=gate-code", ln.Addr().String())
	fmt.Printf("callback=%s\n", callbackURL)
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
