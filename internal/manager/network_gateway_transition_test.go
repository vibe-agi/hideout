package manager

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/testproxy/socks5"
)

func TestGatewayNetworkTransitionProviderMovesOnlyNewConnections(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	target, targetAddress := startManagerGatewayEchoTarget(t)
	t.Cleanup(func() { _ = target.Close() })
	firstProxy := startManagerSOCKSProxy(t, ctx)
	secondProxy := startManagerSOCKSProxy(t, ctx)
	firstURL, err := firstProxy.URL("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	secondURL, err := secondProxy.URL("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	const environmentID = "env_gatewaylive"
	root := t.TempDir()
	registry := netpolicy.NewGatewayRegistry()
	t.Cleanup(func() { _ = registry.Close() })
	binding, initial, err := registry.StageRoute(
		environmentID,
		netpolicy.GatewayRouteSpec{
			UpstreamProxyURL: firstURL,
			ProxySecretRef:   "local-proxy",
			SecretGeneration: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := initial.Commit(); err != nil {
		t.Fatal(err)
	}
	writeManagerGatewayServiceState(
		t,
		root,
		environmentID,
		binding.ID,
	)
	oldConnection := dialManagerGateway(
		t,
		binding,
		targetAddress,
	)
	t.Cleanup(func() { _ = oldConnection.Close() })
	assertManagerGatewayEcho(t, oldConnection, "old-before")
	firstTargetsBefore := len(firstProxy.Targets())

	provider := GatewayNetworkTransitionProvider{
		StoreRoot: root,
		Gateways:  registry,
		ProbeTarget: func(
			NetworkTransitionPlan,
		) string {
			return targetAddress
		},
	}
	from, err := provider.ObserveNetworkRoute(
		ctx,
		environmentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	desired := NetworkRouteConfiguration{
		Mode:           netpolicy.ModeTun2Socks,
		ProxySecretRef: "rotated-proxy", ProxySecretGeneration: 2,
		MediatedResolver: "1.1.1.1",
	}
	service := NetworkTransitionService{Provider: provider}
	plan, err := service.Plan(ctx, NetworkTransitionDraft{
		EnvironmentID: environmentID,
		Desired:       desired,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(
		ctx,
		plan,
		NetworkCandidateMaterial{UpstreamProxyURL: secondURL},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Phase != NetworkTransitionSucceeded ||
		result.From != from ||
		result.Effective != desired ||
		result.ConnectionsRetained != 1 {
		t.Fatalf("gateway transition result=%+v", result)
	}

	// The connection accepted before activation remains usable through its
	// immutable old route.
	assertManagerGatewayEcho(t, oldConnection, "old-after")
	if got := len(firstProxy.Targets()); got != firstTargetsBefore {
		t.Fatalf(
			"old proxy accepted a post-activation route: before=%d after=%d targets=%v",
			firstTargetsBefore,
			got,
			firstProxy.Targets(),
		)
	}
	newConnection := dialManagerGateway(
		t,
		binding,
		targetAddress,
	)
	assertManagerGatewayEcho(t, newConnection, "new-after")
	_ = newConnection.Close()
	if len(secondProxy.Targets()) < 2 {
		t.Fatalf(
			"candidate proxy did not receive probe and new connection: %v",
			secondProxy.Targets(),
		)
	}
	effective, err := provider.ObserveNetworkRoute(
		ctx,
		environmentID,
	)
	if err != nil || effective != desired {
		t.Fatalf("effective route=%+v err=%v", effective, err)
	}
}

func TestGatewayNetworkTransitionProviderProbeFailureRestoresPriorRoute(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	target, targetAddress := startManagerGatewayEchoTarget(t)
	t.Cleanup(func() { _ = target.Close() })
	firstProxy := startManagerSOCKSProxy(t, ctx)
	firstURL, err := firstProxy.URL("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	unavailable, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unavailableURL := "socks5://" + unavailable.Addr().String()
	_ = unavailable.Close()

	const environmentID = "env_gatewayrollback"
	root := t.TempDir()
	registry := netpolicy.NewGatewayRegistry()
	t.Cleanup(func() { _ = registry.Close() })
	binding, initial, err := registry.StageRoute(
		environmentID,
		netpolicy.GatewayRouteSpec{
			UpstreamProxyURL: firstURL,
			ProxySecretRef:   "local-proxy",
			SecretGeneration: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := initial.Commit(); err != nil {
		t.Fatal(err)
	}
	writeManagerGatewayServiceState(
		t,
		root,
		environmentID,
		binding.ID,
	)
	provider := GatewayNetworkTransitionProvider{
		StoreRoot: root,
		Gateways:  registry,
		ProbeTarget: func(
			NetworkTransitionPlan,
		) string {
			return targetAddress
		},
	}
	from, err := provider.ObserveNetworkRoute(
		ctx,
		environmentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	desired := NetworkRouteConfiguration{
		Mode:           netpolicy.ModeTun2Socks,
		ProxySecretRef: "broken-proxy", ProxySecretGeneration: 2,
		MediatedResolver: "1.1.1.1",
	}
	service := NetworkTransitionService{Provider: provider}
	plan, err := service.Plan(ctx, NetworkTransitionDraft{
		EnvironmentID: environmentID,
		Desired:       desired,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(
		ctx,
		plan,
		NetworkCandidateMaterial{
			UpstreamProxyURL: unavailableURL,
		},
	)
	if !errors.Is(err, ErrNetworkTransitionRolledBack) ||
		result.Phase != NetworkTransitionRolledBack ||
		!result.EffectiveProved ||
		result.Effective != from {
		t.Fatalf("probe rollback result=%+v err=%v", result, err)
	}
	effective, observeErr := provider.ObserveNetworkRoute(
		ctx,
		environmentID,
	)
	if observeErr != nil || effective != from {
		t.Fatalf(
			"prior route was not restored: effective=%+v err=%v",
			effective,
			observeErr,
		)
	}
}

func TestGatewayNetworkTransitionProviderBatchCommitAndRollback(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	firstProxy := startManagerSOCKSProxy(t, ctx)
	secondProxy := startManagerSOCKSProxy(t, ctx)
	firstURL, err := firstProxy.URL("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	secondURL, err := secondProxy.URL("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	registry := netpolicy.NewGatewayRegistry()
	t.Cleanup(func() { _ = registry.Close() })
	environmentIDs := []string{"env_batcha", "env_batchb"}
	provider := GatewayNetworkTransitionProvider{
		StoreRoot: root,
		Gateways:  registry,
	}
	service := NetworkTransitionService{Provider: provider}
	desired := NetworkRouteConfiguration{
		Mode:           netpolicy.ModeTun2Socks,
		ProxySecretRef: "rotated-proxy", ProxySecretGeneration: 2,
		MediatedResolver: "1.1.1.1",
	}
	plans := make([]NetworkTransitionPlan, len(environmentIDs))
	from := make([]NetworkRouteConfiguration, len(environmentIDs))
	for index, environmentID := range environmentIDs {
		binding, initial, stageErr := registry.StageRoute(
			environmentID,
			netpolicy.GatewayRouteSpec{
				UpstreamProxyURL: firstURL,
				ProxySecretRef:   "local-proxy",
				SecretGeneration: 1,
			},
		)
		if stageErr != nil {
			t.Fatal(stageErr)
		}
		if err := initial.Activate(); err != nil {
			t.Fatal(err)
		}
		if err := initial.Commit(); err != nil {
			t.Fatal(err)
		}
		writeManagerGatewayServiceState(
			t,
			root,
			environmentID,
			binding.ID,
		)
		from[index], err = provider.ObserveNetworkRoute(
			ctx,
			environmentID,
		)
		if err != nil {
			t.Fatal(err)
		}
		plans[index], err = service.Plan(
			ctx,
			NetworkTransitionDraft{
				EnvironmentID: environmentID,
				Desired:       desired,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	stage := func() []NetworkStagedCandidate {
		t.Helper()
		handles := make(
			[]NetworkStagedCandidate,
			len(plans),
		)
		for index, plan := range plans {
			handle, proof, stageErr :=
				provider.StageNetworkCandidate(
					ctx,
					plan,
					NetworkCandidateMaterial{
						UpstreamProxyURL: secondURL,
					},
				)
			if stageErr != nil {
				t.Fatal(stageErr)
			}
			if err := proof.Validate(plan.Desired); err != nil {
				t.Fatal(err)
			}
			handles[index] = handle
		}
		return handles
	}

	handles := stage()
	for _, handle := range handles {
		if _, err := handle.ActivateNetworkCandidate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	proofs, err := provider.RollbackNetworkCandidates(
		ctx,
		handles,
	)
	if err != nil || len(proofs) != len(handles) {
		t.Fatalf("batch rollback proofs=%+v err=%v", proofs, err)
	}
	for index, environmentID := range environmentIDs {
		effective, observeErr := provider.ObserveNetworkRoute(
			ctx,
			environmentID,
		)
		if observeErr != nil || effective != from[index] ||
			proofs[index].Validate(from[index]) != nil {
			t.Fatalf(
				"rollback environment=%s effective=%+v proof=%+v err=%v",
				environmentID,
				effective,
				proofs[index],
				observeErr,
			)
		}
		plans[index], err = service.Plan(
			ctx,
			NetworkTransitionDraft{
				EnvironmentID: environmentID,
				Desired:       desired,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	handles = stage()
	for _, handle := range handles {
		if _, err := handle.ActivateNetworkCandidate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := provider.CommitNetworkCandidates(ctx, handles); err != nil {
		t.Fatal(err)
	}
	for _, environmentID := range environmentIDs {
		effective, observeErr := provider.ObserveNetworkRoute(
			ctx,
			environmentID,
		)
		if observeErr != nil || effective != desired {
			t.Fatalf(
				"commit environment=%s effective=%+v err=%v",
				environmentID,
				effective,
				observeErr,
			)
		}
	}
}

func startManagerSOCKSProxy(
	t *testing.T,
	ctx context.Context,
) *socks5.Server {
	t.Helper()
	server, err := socks5.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()
	t.Cleanup(func() {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("SOCKS fixture: %v", err)
			}
		default:
		}
	})
	return server
}

func startManagerGatewayEchoTarget(
	t *testing.T,
) (net.Listener, string) {
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

func writeManagerGatewayServiceState(
	t *testing.T,
	root string,
	environmentID string,
	gatewayID string,
) {
	t.Helper()
	plan := netpolicy.Plan{
		Mode:                     netpolicy.ModeTun2Socks,
		MediatedResolver:         "1.1.1.1",
		GatewayID:                gatewayID,
		ConfigurationFingerprint: strings.Repeat("a", 64),
		ConfigurationID: "sha256:" +
			strings.Repeat("b", 64),
	}
	state, err := netpolicy.BuildServiceState(
		environmentID,
		plan,
		netpolicy.ServiceReady,
		"01234567-89ab-cdef-0123-456789abcdef",
		time.Now().Add(-time.Minute).UTC(),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(
		(environment.Store{Root: root}).
			RuntimeNetworkServiceDir(environmentID),
		"state.json",
	)
	if err := netpolicy.WriteServiceState(path, state); err != nil {
		t.Fatal(err)
	}
}

func dialManagerGateway(
	t *testing.T,
	binding netpolicy.GatewayBinding,
	target string,
) net.Conn {
	t.Helper()
	connection, err := net.DialTimeout(
		"tcp",
		binding.Address,
		5*time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	fail := func(err error) {
		_ = connection.Close()
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		fail(err)
	}
	response := make([]byte, 2)
	if _, err := io.ReadFull(connection, response); err != nil {
		fail(err)
	}
	if response[0] != 0x05 || response[1] != 0x02 {
		fail(errors.New("gateway refused username/password authentication"))
	}
	username := []byte(binding.Username)
	password := []byte(binding.Password)
	auth := make([]byte, 0, 3+len(username)+len(password))
	auth = append(auth, 0x01, byte(len(username)))
	auth = append(auth, username...)
	auth = append(auth, byte(len(password)))
	auth = append(auth, password...)
	if _, err := connection.Write(auth); err != nil {
		fail(err)
	}
	if _, err := io.ReadFull(connection, response); err != nil {
		fail(err)
	}
	if response[0] != 0x01 || response[1] != 0x00 {
		fail(errors.New("gateway authentication failed"))
	}
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		fail(err)
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		fail(errors.New("test gateway target must be IPv4"))
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		fail(err)
	}
	request := []byte{0x05, 0x01, 0x00, 0x01}
	request = append(request, ip...)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	request = append(request, portBytes...)
	if _, err := connection.Write(request); err != nil {
		fail(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(connection, reply); err != nil {
		fail(err)
	}
	if reply[0] != 0x05 || reply[1] != 0x00 {
		fail(errors.New("gateway CONNECT failed"))
	}
	return connection
}

func assertManagerGatewayEcho(
	t *testing.T,
	connection net.Conn,
	value string,
) {
	t.Helper()
	if err := connection.SetDeadline(
		time.Now().Add(5 * time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte(value)); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len(value))
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != value {
		t.Fatalf("gateway echo=%q want=%q", response, value)
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
}
