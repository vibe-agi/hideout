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
	observation, ok := registry.Observation("env_gateway")
	if !ok {
		t.Fatal("gateway observation is unavailable")
	}
	want := GatewayObservation{
		Accepted: 2, Authenticated: 1, AuthenticationFailed: 1,
		RequestParsed: 1, UpstreamDialStarted: 1, UpstreamConnected: 1,
	}
	if observation != want {
		t.Fatalf("gateway observation=%+v want=%+v", observation, want)
	}
}

func TestEnvironmentGatewayObservationDistinguishesRouteAndDialFailures(t *testing.T) {
	registry := NewGatewayRegistry()
	defer registry.Close()

	missingBinding, missingChange, err := registry.Stage("env_missingroute", "")
	if err != nil {
		t.Fatal(err)
	}
	if connection, dialErr := dialGateway(missingBinding, "127.0.0.1:1"); dialErr == nil {
		_ = connection.Close()
		t.Fatal("gateway without an active route accepted a connection")
	}
	if err := missingChange.Rollback(); err != nil {
		t.Fatal(err)
	}
	missing, ok := registry.Observation("env_missingroute")
	if !ok || missing.Accepted != 1 || missing.Authenticated != 1 ||
		missing.RequestParsed != 1 || missing.RouteMissing != 1 ||
		missing.UpstreamDialStarted != 0 {
		t.Fatalf("missing-route observation=%+v available=%t", missing, ok)
	}

	dialBinding, dialChange, err := registry.Stage("env_dialfailure", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := dialChange.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := dialChange.Commit(); err != nil {
		t.Fatal(err)
	}
	unavailable, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unavailableAddress := unavailable.Addr().String()
	if err := unavailable.Close(); err != nil {
		t.Fatal(err)
	}
	if connection, dialErr := dialGateway(dialBinding, unavailableAddress); dialErr == nil {
		_ = connection.Close()
		t.Fatal("gateway connected to a closed target port")
	}
	failed, ok := registry.Observation("env_dialfailure")
	if !ok || failed.Accepted != 1 || failed.Authenticated != 1 ||
		failed.RequestParsed != 1 || failed.UpstreamDialStarted != 1 ||
		failed.UpstreamDialFailed != 1 || failed.UpstreamConnected != 0 {
		t.Fatalf("dial-failure observation=%+v available=%t", failed, ok)
	}
}

func TestGatewayObservationSinceDoesNotUnderflow(t *testing.T) {
	current := GatewayObservation{
		Accepted: 3, Authenticated: 2, UpstreamConnected: 1,
	}
	previous := GatewayObservation{
		Accepted: 1, Authenticated: 4, UpstreamConnected: 1,
	}
	got := current.Since(previous)
	want := GatewayObservation{Accepted: 2, Authenticated: 2}
	if got != want {
		t.Fatalf("observation delta=%+v want=%+v", got, want)
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

func TestEnvironmentGatewayRejectsGuestAliasAsHostUpstream(t *testing.T) {
	registry := NewGatewayRegistry()
	defer registry.Close()
	_, _, err := registry.Stage("env_guestalias", "socks5://host.lima.internal:7890")
	if err == nil || !strings.Contains(err.Error(), "use 127.0.0.1") {
		t.Fatalf("guest-alias upstream error=%v", err)
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
