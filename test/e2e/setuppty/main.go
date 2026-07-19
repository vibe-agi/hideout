//go:build darwin || linux

package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

func main() {
	hideout := flag.String("hideout", "", "installed hideout executable")
	store := flag.String("store", "", "isolated Hideout store")
	out := flag.String("out", "", "captured terminal output")
	flag.Parse()
	if err := run(*hideout, *store, *out); err != nil {
		fmt.Fprintln(os.Stderr, "setup-pty:", err)
		os.Exit(1)
	}
}

func run(hideout, store, outputPath string) error {
	if strings.TrimSpace(hideout) == "" || strings.TrimSpace(store) == "" || strings.TrimSpace(outputPath) == "" {
		return errors.New("--hideout, --store, and --out are required")
	}
	interruptOutput, err := runInterrupted(hideout, store+"-interrupt")
	if err != nil {
		return err
	}
	cmd := exec.Command(hideout, "setup")
	cmd.Env = append(os.Environ(), "HIDEOUT_STORE_ROOT="+store)
	terminal, err := pty.Start(cmd)
	if err != nil {
		return err
	}
	defer terminal.Close()
	var output lockedCapture
	done := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&output, terminal)
		done <- copyErr
	}()
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(output.String(), "Set up this configuration? [y/N]") {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("confirmed setup prompt did not appear: %s", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := terminal.Write([]byte("yes\n")); err != nil {
		return err
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err := <-wait:
		if err != nil {
			return fmt.Errorf("setup exited: %w; output=%s", err, output.String())
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return errors.New("setup did not complete within 30s")
	}
	_ = terminal.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	text := output.String()
	for _, required := range []string{
		"Set up Hideout", "Set up this configuration? [y/N]", "Hideout configuration is ready.",
		"no VM start or runtime download", "read/write at /workspace",
	} {
		if !strings.Contains(text, required) {
			return fmt.Errorf("captured output lacks %q: %s", required, text)
		}
	}
	combined := append([]byte("--- interrupted setup ---\n"), interruptOutput...)
	combined = append(combined, []byte("\n--- confirmed setup ---\n")...)
	combined = append(combined, []byte(output.String())...)
	return os.WriteFile(outputPath, combined, 0o600)
}

type lockedCapture struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (capture *lockedCapture) Write(data []byte) (int, error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.data.Write(data)
}

func (capture *lockedCapture) String() string {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.data.String()
}

func runInterrupted(hideout, store string) ([]byte, error) {
	cmd := exec.Command(hideout, "setup")
	cmd.Env = append(os.Environ(), "HIDEOUT_STORE_ROOT="+store)
	terminal, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	defer terminal.Close()
	var output lockedCapture
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, terminal)
		close(done)
	}()
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(output.String(), "Set up this configuration? [y/N]") {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, fmt.Errorf("interrupted setup prompt did not appear: %s", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		return nil, err
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	select {
	case err := <-wait:
		if err == nil {
			return nil, errors.New("SIGINT setup unexpectedly succeeded")
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		return nil, errors.New("SIGINT setup did not terminate")
	}
	_ = terminal.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	for _, forbidden := range []string{
		filepath.Join(store, "profiles", "default", "profile.json"),
		filepath.Join(store, "environments"),
	} {
		if _, err := os.Lstat(forbidden); !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("SIGINT setup created forbidden state %s: %v", forbidden, err)
		}
	}
	stop := exec.Command(hideout, "daemon", "stop")
	stop.Env = append(os.Environ(), "HIDEOUT_STORE_ROOT="+store)
	_ = stop.Run()
	return []byte(output.String()), nil
}
