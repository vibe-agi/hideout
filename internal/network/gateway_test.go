package network

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestEnvironmentGatewayRequiresAuthenticationAndForwardsDirect(t *testing.T) {
	target, targetAddress := startGatewayEchoTarget(t)
	defer target.Close()
	registry := NewGatewayRegistry()
	defer registry.Close()
	binding, change, err := registry.Stage("env_gateway", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := change.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := change.Commit(); err != nil {
		t.Fatal(err)
	}

	connection, err := dialGateway(binding, targetAddress)
	if err != nil {
		t.Fatalf("dial authenticated gateway: %v", err)
	}
	defer connection.Close()
	if _, err := connection.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(connection, response); err != nil || string(response) != "ping" {
		t.Fatalf("gateway response=%q err=%v", response, err)
	}

	wrong := binding
	wrong.Password = "wrong"
	if connection, err := dialGateway(wrong, targetAddress); err == nil {
		_ = connection.Close()
		t.Fatal("gateway accepted an invalid credential")
	}
}

func TestEnvironmentGatewayRollbackRestoresPreviousRoute(t *testing.T) {
	target, targetAddress := startGatewayEchoTarget(t)
	defer target.Close()
	registry := NewGatewayRegistry()
	defer registry.Close()
	binding, initial, err := registry.Stage("env_rollback", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := initial.Commit(); err != nil {
		t.Fatal(err)
	}

	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	go func() {
		for {
			connection, acceptErr := proxy.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer connection.Close()
				request, readErr := http.ReadRequest(bufio.NewReader(connection))
				if readErr == nil {
					_ = request.Body.Close()
				}
				_, _ = io.WriteString(connection, "HTTP/1.1 502 staged-route\r\nContent-Length: 0\r\n\r\n")
			}()
		}
	}()

	_, staged, err := registry.Stage("env_rollback", "http://"+proxy.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	connection, err := dialGateway(binding, targetAddress)
	if err != nil {
		t.Fatalf("prepared route changed traffic before activation: %v", err)
	}
	_ = connection.Close()
	if err := staged.Activate(); err != nil {
		t.Fatal(err)
	}
	if connection, err := dialGateway(binding, targetAddress); err == nil {
		_ = connection.Close()
		t.Fatal("activated route was not used for a new connection")
	}
	if err := staged.Rollback(); err != nil {
		t.Fatal(err)
	}
	connection, err = dialGateway(binding, targetAddress)
	if err != nil {
		t.Fatalf("previous direct route was not restored: %v", err)
	}
	_ = connection.Close()
}

func TestEnvironmentGatewayAcceptedConnectionKeepsPreviousRoute(t *testing.T) {
	target, targetAddress := startGatewayEchoTarget(t)
	defer target.Close()
	registry := NewGatewayRegistry()
	defer registry.Close()
	binding, initial, err := registry.Stage("env_existing", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := initial.Commit(); err != nil {
		t.Fatal(err)
	}

	existing, err := net.DialTimeout("tcp", binding.Address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer existing.Close()
	deadline := time.Now().Add(time.Second)
	for {
		registry.mu.Lock()
		entry := registry.entries["env_existing"]
		registry.mu.Unlock()
		entry.connMu.Lock()
		accepted := len(entry.conns) == 1
		entry.connMu.Unlock()
		if accepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("gateway did not accept the pre-switch connection")
		}
		time.Sleep(time.Millisecond)
	}

	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	go func() {
		for {
			connection, acceptErr := proxy.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, _ = io.WriteString(connection, "HTTP/1.1 502 switched-route\r\nContent-Length: 0\r\n\r\n")
			}()
		}
	}()
	_, change, err := registry.Stage("env_existing", "http://"+proxy.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer change.Rollback()
	preActivation, err := dialGateway(binding, targetAddress)
	if err != nil {
		t.Fatalf("prepared route changed new traffic before activation: %v", err)
	}
	_ = preActivation.Close()
	if err := change.Activate(); err != nil {
		t.Fatal(err)
	}

	if err := handshakeGateway(existing, binding, targetAddress); err != nil {
		t.Fatalf("accepted connection did not retain direct route: %v", err)
	}
	if _, err := existing.Write([]byte("old")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 3)
	if _, err := io.ReadFull(existing, response); err != nil || string(response) != "old" {
		t.Fatalf("existing response=%q err=%v", response, err)
	}
	if connection, err := dialGateway(binding, targetAddress); err == nil {
		_ = connection.Close()
		t.Fatal("new connection did not use the staged route")
	}
}

func TestEnvironmentGatewayBindingUsesGuestHostWithoutLeakingUpstream(t *testing.T) {
	registry := NewGatewayRegistry()
	defer registry.Close()
	binding, change, err := registry.Stage("env_binding", "socks5://operator:secret@127.0.0.1:1080")
	if err != nil {
		t.Fatal(err)
	}
	defer change.Rollback()
	value, err := binding.ProxyURL("host.lima.internal")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Hostname() != "host.lima.internal" || parsed.User == nil || strings.Contains(value, "operator") || strings.Contains(value, "secret") {
		t.Fatalf("unexpected guest binding %q", value)
	}
}

func TestEnvironmentGatewayConcurrentTransitionWaitIsBounded(t *testing.T) {
	registry := NewGatewayRegistry()
	defer registry.Close()
	_, first, err := registry.Stage("env_transition", "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, second, stageErr := registry.Stage("env_transition", "")
		if stageErr == nil {
			stageErr = second.Rollback()
		}
		done <- stageErr
	}()
	select {
	case err := <-done:
		t.Fatalf("concurrent transition did not wait for the first change: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := first.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent transition did not continue after the first change resolved")
	}
}

func startGatewayEchoTarget(t *testing.T) (net.Listener, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()
	return listener, listener.Addr().String()
}

func dialGateway(binding GatewayBinding, target string) (net.Conn, error) {
	connection, err := net.DialTimeout("tcp", binding.Address, time.Second)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (net.Conn, error) {
		_ = connection.Close()
		return nil, err
	}
	if err := handshakeGateway(connection, binding, target); err != nil {
		return fail(err)
	}
	return connection, nil
}

func handshakeGateway(connection net.Conn, binding GatewayBinding, target string) error {
	if _, err := connection.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		return err
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(connection, response); err != nil || response[1] != 0x02 {
		return fmt.Errorf("gateway method response=%v err=%v", response, err)
	}
	auth := []byte{0x01, byte(len(binding.Username))}
	auth = append(auth, binding.Username...)
	auth = append(auth, byte(len(binding.Password)))
	auth = append(auth, binding.Password...)
	if _, err := connection.Write(auth); err != nil {
		return err
	}
	if _, err := io.ReadFull(connection, response); err != nil || response[1] != 0x00 {
		return fmt.Errorf("gateway auth response=%v err=%v", response, err)
	}
	request, err := encodeSOCKSConnect(target)
	if err != nil {
		return err
	}
	if _, err := connection.Write(request); err != nil {
		return err
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(connection, reply); err != nil || reply[1] != 0x00 {
		return fmt.Errorf("gateway connect response=%v err=%v", reply, err)
	}
	return nil
}
