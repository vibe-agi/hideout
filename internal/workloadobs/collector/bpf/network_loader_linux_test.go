//go:build linux

package bpf

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/rlimit"
)

func TestOpenNetworkEventReaderRejectsInvalidBoundaryState(t *testing.T) {
	if _, err := OpenNetworkEventReader(
		"",
		0,
		nil,
	); !errors.Is(err, ErrNetworkCollectorTarget) {
		t.Fatalf("zero target error=%v want=%v", err, ErrNetworkCollectorTarget)
	}
	if _, err := OpenNetworkEventReader(
		"relative",
		1,
		nil,
	); !errors.Is(err, ErrNetworkCollectorTarget) {
		t.Fatalf("relative target error=%v want=%v", err, ErrNetworkCollectorTarget)
	}
	if _, err := OpenNetworkEventReader(
		"/sys/fs/cgroup/hideout/fixture",
		1,
		nil,
	); !errors.Is(err, ErrNetworkCollectorProcessState) {
		t.Fatalf(
			"missing process state error=%v want=%v",
			err,
			ErrNetworkCollectorProcessState,
		)
	}
	if _, err := OpenNetworkEventReaderWithOptions(
		"/sys/fs/cgroup/hideout/fixture",
		1,
		nil,
		NetworkReaderOptions{},
	); !errors.Is(err, ErrNetworkCollectorTarget) {
		t.Fatalf(
			"zero DNS ports error=%v want=%v",
			err,
			ErrNetworkCollectorTarget,
		)
	}
	for name, endpoints := range map[string][]NetworkProxyEndpoint{
		"malformed IP":   {{IP: "not-an-ip", Port: 7890}},
		"unspecified IP": {{IP: "0.0.0.0", Port: 7890}},
		"zero port":      {{IP: "127.0.0.1"}},
		"duplicate": {
			{IP: "127.0.0.1", Port: 7890},
			{IP: "127.0.0.1", Port: 7890},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validatedProxyEndpointKeys(endpoints); !errors.Is(
				err,
				ErrNetworkCollectorProxyEndpoint,
			) {
				t.Fatalf(
					"error=%v want=%v",
					err,
					ErrNetworkCollectorProxyEndpoint,
				)
			}
		})
	}
	tooMany := make([]NetworkProxyEndpoint, 257)
	for index := range tooMany {
		tooMany[index] = NetworkProxyEndpoint{
			IP:   "127.0.0.1",
			Port: uint16(index + 1),
		}
	}
	if _, err := validatedProxyEndpointKeys(tooMany); !errors.Is(
		err,
		ErrNetworkCollectorProxyEndpoint,
	) {
		t.Fatalf(
			"too many endpoints error=%v want=%v",
			err,
			ErrNetworkCollectorProxyEndpoint,
		)
	}
}

func TestNetworkEventReaderRealKernel(t *testing.T) {
	if os.Getenv("HIDEOUT_TEST_BPF_ATTACH") != "1" {
		t.Skip("set HIDEOUT_TEST_BPF_ATTACH=1 in a privileged Linux test guest")
	}
	if err := rlimit.RemoveMemlock(); err != nil {
		t.Fatalf("remove memlock limit: %v", err)
	}
	cgroup, cgroupID := newIsolatedTestCgroup(t)
	processReader, err := OpenProcessEventReader(cgroupID)
	if err != nil {
		t.Fatalf("open process state: %+v", err)
	}
	defer processReader.Close()

	reader, err := OpenNetworkEventReader(
		cgroup.Name(),
		cgroupID,
		processReader,
	)
	if err != nil {
		var verifier *ebpf.VerifierError
		if errors.As(err, &verifier) {
			t.Logf("verifier:\n%+v", verifier)
		}
		t.Fatalf("%+v", err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	if hooks := reader.AttachedHooks(); len(hooks) != 6 {
		t.Fatalf("hooks=%q", hooks)
	}
	var configuredCgroupID uint64
	if err := reader.objects.NetworkTargetCgroupId.Get(
		&configuredCgroupID,
	); err != nil {
		t.Fatal(err)
	}
	if configuredCgroupID != cgroupID {
		t.Fatalf(
			"configured cgroup id=%d want=%d",
			configuredCgroupID,
			cgroupID,
		)
	}

	tcp4 := listenNetworkTCP(t, "tcp4", "127.0.0.1:0")
	tcp6 := listenNetworkTCP(t, "tcp6", "[::1]:0")
	tcpFork := listenNetworkTCP(t, "tcp4", "127.0.0.1:0")
	udp4 := listenNetworkUDP(t, "udp4", "127.0.0.1:0")
	udp6 := listenNetworkUDP(t, "udp6", "[::1]:0")

	tcpResults := make(chan error, 3)
	for _, listener := range []net.Listener{tcp4, tcp6, tcpFork} {
		go func(listener net.Listener) {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				tcpResults <- acceptErr
				return
			}
			defer connection.Close()
			var payload [1]byte
			_, readErr := io.ReadFull(connection, payload[:])
			tcpResults <- readErr
		}(listener)
	}

	command := exec.Command(
		os.Args[0],
		"-test.run=^TestNetworkKernelHelper$",
	)
	command.Env = append(
		os.Environ(),
		"HIDEOUT_NETWORK_HELPER=1",
		"HIDEOUT_NETWORK_TCP4="+tcp4.Addr().String(),
		"HIDEOUT_NETWORK_TCP6="+tcp6.Addr().String(),
		"HIDEOUT_NETWORK_UDP4="+udp4.LocalAddr().String(),
		"HIDEOUT_NETWORK_UDP6="+udp6.LocalAddr().String(),
	)
	command.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    int(cgroup.Fd()),
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("network helper: %v\n%s", err, output)
	}
	_, forkPort, err := net.SplitHostPort(tcpFork.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	forkCommand := exec.Command(
		"/bin/bash",
		"-c",
		"(printf x >/dev/tcp/127.0.0.1/"+forkPort+")",
	)
	forkCommand.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    int(cgroup.Fd()),
	}
	if output, err := forkCommand.CombinedOutput(); err != nil {
		t.Fatalf("fork-without-exec helper: %v\n%s", err, output)
	}
	for range 3 {
		if err := <-tcpResults; err != nil {
			t.Fatalf("TCP fixture: %v", err)
		}
	}

	// Traffic from the observer's own cgroup must not enter the target ring or
	// counters even when it uses the same destination.
	outside, err := net.DialUDP(
		"udp4",
		nil,
		udp4.LocalAddr().(*net.UDPAddr),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outside.Write([]byte{0xff}); err != nil {
		outside.Close()
		t.Fatal(err)
	}
	if err := outside.Close(); err != nil {
		t.Fatal(err)
	}

	inheritedKey := networkEndpointKey(
		NetworkEventConnect,
		NetworkProtocolTCP,
		tcpFork.Addr(),
	)
	expected := map[string]bool{
		networkEndpointKey(
			NetworkEventConnect,
			NetworkProtocolTCP,
			tcp4.Addr(),
		): false,
		networkEndpointKey(
			NetworkEventConnect,
			NetworkProtocolTCP,
			tcp6.Addr(),
		): false,
		inheritedKey: false,
		networkEndpointKey(
			NetworkEventSendmsg,
			NetworkProtocolUDP,
			udp4.LocalAddr(),
		): false,
		networkEndpointKey(
			NetworkEventSendmsg,
			NetworkProtocolUDP,
			udp6.LocalAddr(),
		): false,
	}
	reader.SetDeadline(time.Now().Add(10 * time.Second))
	for remaining := len(expected); remaining != 0; {
		event, err := reader.ReadNetworkEvent()
		if err != nil {
			counters, _ := reader.Counters()
			t.Fatalf(
				"read remaining=%d expected=%v counters=%+v: %v",
				remaining,
				expected,
				counters,
				err,
			)
		}
		key := fmt.Sprintf(
			"%d/%d/%s/%d",
			event.Kind,
			event.Protocol,
			event.DestinationIP().String(),
			event.DestinationPort,
		)
		seen, exists := expected[key]
		if !exists {
			t.Fatalf("unexpected network event key=%q event=%+v", key, event)
		}
		if seen {
			t.Fatalf("duplicate network event key=%q event=%+v", key, event)
		}
		if event.CgroupID != cgroupID ||
			event.ExecSequence == 0 ||
			event.ExecutionPID == 0 ||
			event.SocketCookie == 0 ||
			event.Flags != 0 {
			t.Fatalf("network event=%+v", event)
		}
		if key == inheritedKey && event.PID == event.ExecutionPID {
			t.Fatalf(
				"fork-without-exec lost inherited execution owner: %+v",
				event,
			)
		}
		evidence, err := reader.SocketEvidence(event)
		if err != nil {
			t.Fatalf("socket evidence event=%+v: %v", event, err)
		}
		if evidence.IfIndex == 0 ||
			evidence.EgressPackets == 0 ||
			evidence.EgressBytes == 0 {
			t.Fatalf("socket evidence=%+v event=%+v", evidence, event)
		}
		expected[key] = true
		remaining--
	}
	counters, err := reader.Counters()
	if err != nil {
		t.Fatal(err)
	}
	if counters.MatchedEvents != 5 ||
		counters.ReservedEvents != 5 ||
		counters.RingbufDrops != 0 ||
		counters.StateDrops != 0 ||
		counters.CorrelationHits < 5 ||
		counters.CorrelationMisses != 0 ||
		counters.UnsupportedEvents != 0 {
		t.Fatalf("network counters=%+v", counters)
	}
}

func TestNetworkKernelHelper(t *testing.T) {
	if os.Getenv("HIDEOUT_NETWORK_HELPER") != "1" {
		t.Skip("network kernel helper process")
	}
	for _, fixture := range []struct {
		network string
		address string
	}{
		{"tcp4", os.Getenv("HIDEOUT_NETWORK_TCP4")},
		{"tcp6", os.Getenv("HIDEOUT_NETWORK_TCP6")},
	} {
		connection, err := net.DialTimeout(
			fixture.network,
			fixture.address,
			2*time.Second,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := connection.Write([]byte{1}); err != nil {
			connection.Close()
			t.Fatal(err)
		}
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, fixture := range []struct {
		network string
		address string
	}{
		{"udp4", os.Getenv("HIDEOUT_NETWORK_UDP4")},
		{"udp6", os.Getenv("HIDEOUT_NETWORK_UDP6")},
	} {
		address, err := net.ResolveUDPAddr(fixture.network, fixture.address)
		if err != nil {
			t.Fatal(err)
		}
		socket, err := net.ListenUDP(fixture.network, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := socket.WriteToUDP([]byte{1}, address); err != nil {
			socket.Close()
			t.Fatal(err)
		}
		if err := socket.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDNSPacketReaderRealKernel(t *testing.T) {
	if os.Getenv("HIDEOUT_TEST_BPF_ATTACH") != "1" {
		t.Skip("set HIDEOUT_TEST_BPF_ATTACH=1 in a privileged Linux test guest")
	}
	if err := rlimit.RemoveMemlock(); err != nil {
		t.Fatalf("remove memlock limit: %v", err)
	}
	cgroup, cgroupID := newIsolatedTestCgroup(t)
	processReader, err := OpenProcessEventReader(cgroupID)
	if err != nil {
		t.Fatalf("open process state: %+v", err)
	}
	defer processReader.Close()

	server := listenNetworkUDP(t, "udp4", "127.0.0.1:0")
	serverPort := uint16(server.LocalAddr().(*net.UDPAddr).Port)
	encryptedPort := uint16(65534)
	if encryptedPort == serverPort {
		encryptedPort--
	}
	reader, err := OpenNetworkEventReaderWithOptions(
		cgroup.Name(),
		cgroupID,
		processReader,
		NetworkReaderOptions{
			DNSPlaintextPort: serverPort,
			DNSEncryptedPort: encryptedPort,
		},
	)
	if err != nil {
		var verifier *ebpf.VerifierError
		if errors.As(err, &verifier) {
			t.Logf("verifier:\n%+v", verifier)
		}
		t.Fatalf("%+v", err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()

	query := dnsKernelQuery()
	response := dnsKernelResponse(query)
	serverResults := make(chan error, 1)
	go func() {
		for range 2 {
			buffer := make([]byte, 512)
			count, address, readErr := server.ReadFromUDP(buffer)
			if readErr != nil {
				serverResults <- readErr
				return
			}
			if !bytes.Equal(buffer[:count], query) {
				serverResults <- fmt.Errorf(
					"DNS query=%x want=%x",
					buffer[:count],
					query,
				)
				return
			}
			if _, writeErr := server.WriteToUDP(response, address); writeErr != nil {
				serverResults <- writeErr
				return
			}
		}
		serverResults <- nil
	}()

	command := exec.Command(
		os.Args[0],
		"-test.run=^TestDNSKernelHelper$",
	)
	command.Env = append(
		os.Environ(),
		"HIDEOUT_DNS_HELPER=1",
		"HIDEOUT_DNS_SERVER="+server.LocalAddr().String(),
	)
	command.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    int(cgroup.Fd()),
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("DNS helper: %v\n%s", err, output)
	}

	// The same exchange outside the target cgroup must not enter the target
	// packet ring or its counters.
	outside, err := net.DialUDP(
		"udp4",
		nil,
		server.LocalAddr().(*net.UDPAddr),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outside.Write(query); err != nil {
		outside.Close()
		t.Fatal(err)
	}
	if err := outside.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		outside.Close()
		t.Fatal(err)
	}
	outsideResponse := make([]byte, len(response))
	if _, err := io.ReadFull(outside, outsideResponse); err != nil {
		outside.Close()
		t.Fatal(err)
	}
	if err := outside.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(outsideResponse, response) {
		t.Fatalf("outside response=%x want=%x", outsideResponse, response)
	}
	if err := <-serverResults; err != nil {
		t.Fatal(err)
	}

	expected := map[uint32][]byte{
		DNSPacketEgress:  query,
		DNSPacketIngress: response,
	}
	reader.SetDNSDeadline(time.Now().Add(10 * time.Second))
	var targetCookie uint64
	for len(expected) != 0 {
		packet, err := reader.ReadDNSPacket()
		if err != nil {
			counters, _ := reader.Counters()
			t.Fatalf(
				"read DNS remaining=%v counters=%+v: %v",
				expected,
				counters,
				err,
			)
		}
		want, exists := expected[packet.Direction]
		if !exists {
			t.Fatalf("unexpected DNS packet=%+v", packet)
		}
		if packet.CgroupID != cgroupID ||
			packet.ExecutionPID == 0 ||
			packet.ExecSequence == 0 ||
			packet.ResolverPort != uint32(serverPort) ||
			packet.ResolverIP().String() != "127.0.0.1" ||
			packet.Flags != 0 {
			t.Fatalf("DNS packet=%+v", packet)
		}
		if targetCookie == 0 {
			targetCookie = packet.SocketCookie
		} else if packet.SocketCookie != targetCookie {
			t.Fatalf(
				"DNS response cookie=%d want query cookie=%d",
				packet.SocketCookie,
				targetCookie,
			)
		}
		payload := packet.TakePayload()
		if !bytes.Equal(payload, want) {
			t.Fatalf(
				"DNS direction=%d payload=%x want=%x",
				packet.Direction,
				payload,
				want,
			)
		}
		clear(payload)
		delete(expected, packet.Direction)
	}
	counters, err := reader.Counters()
	if err != nil {
		t.Fatal(err)
	}
	if counters.DNSMatchedPackets != 2 ||
		counters.DNSReservedPackets != 2 ||
		counters.DNSRingbufDrops != 0 ||
		counters.DNSCaptureFailures != 0 ||
		counters.DNSTruncatedPackets != 0 ||
		counters.DNSStateMisses != 0 {
		t.Fatalf("DNS counters=%+v", counters)
	}
}

func TestDNSKernelHelper(t *testing.T) {
	if os.Getenv("HIDEOUT_DNS_HELPER") != "1" {
		t.Skip("DNS kernel helper process")
	}
	address, err := net.ResolveUDPAddr(
		"udp4",
		os.Getenv("HIDEOUT_DNS_SERVER"),
	)
	if err != nil {
		t.Fatal(err)
	}
	socket, err := net.DialUDP("udp4", nil, address)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	query := dnsKernelQuery()
	if _, err := socket.Write(query); err != nil {
		t.Fatal(err)
	}
	if err := socket.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len(dnsKernelResponse(query)))
	if _, err := io.ReadFull(socket, response); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response, dnsKernelResponse(query)) {
		t.Fatalf("response=%x", response)
	}
}

func TestProxyChunkReaderRealKernelStopsAtValidatedHandshake(t *testing.T) {
	if os.Getenv("HIDEOUT_TEST_BPF_ATTACH") != "1" {
		t.Skip("set HIDEOUT_TEST_BPF_ATTACH=1 in a privileged Linux test guest")
	}
	if err := rlimit.RemoveMemlock(); err != nil {
		t.Fatalf("remove memlock limit: %v", err)
	}
	cgroup, cgroupID := newIsolatedTestCgroup(t)
	processReader, err := OpenProcessEventReader(cgroupID)
	if err != nil {
		t.Fatalf("open process state: %+v", err)
	}
	defer processReader.Close()

	server := listenNetworkTCP(t, "tcp4", "127.0.0.1:0")
	unconfiguredServer := listenNetworkTCP(t, "tcp4", "127.0.0.1:0")
	serverPort := uint16(server.Addr().(*net.TCPAddr).Port)
	reader, err := OpenNetworkEventReaderWithOptions(
		cgroup.Name(),
		cgroupID,
		processReader,
		NetworkReaderOptions{
			DNSPlaintextPort: 53,
			DNSEncryptedPort: 853,
			ProxyEndpoints: []NetworkProxyEndpoint{{
				IP:   "127.0.0.1",
				Port: serverPort,
			}},
		},
	)
	if err != nil {
		var verifier *ebpf.VerifierError
		if errors.As(err, &verifier) {
			t.Logf("verifier:\n%+v", verifier)
		}
		t.Fatalf("%+v", err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()

	request := proxyKernelRequest()
	unconfiguredCanary := []byte("not-a-configured-proxy")
	tunnelCanary := []byte(
		"Authorization: Bearer tunnel-payload-must-not-enter-ring",
	)
	unconfiguredDone := make(chan error, 1)
	go func() {
		connection, acceptErr := unconfiguredServer.Accept()
		if acceptErr != nil {
			unconfiguredDone <- acceptErr
			return
		}
		defer connection.Close()
		received := make([]byte, len(unconfiguredCanary))
		if _, readErr := io.ReadFull(connection, received); readErr != nil {
			unconfiguredDone <- readErr
			return
		}
		if !bytes.Equal(received, unconfiguredCanary) {
			unconfiguredDone <- fmt.Errorf(
				"unconfigured payload=%q want=%q",
				received,
				unconfiguredCanary,
			)
			return
		}
		unconfiguredDone <- nil
	}()
	requestRead := make(chan error, 1)
	allowTunnel := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		connection, acceptErr := server.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer connection.Close()
		greeting := make([]byte, 3)
		if _, readErr := io.ReadFull(connection, greeting); readErr != nil {
			serverDone <- readErr
			return
		}
		if !bytes.Equal(greeting, []byte{0x05, 0x01, 0x00}) {
			serverDone <- fmt.Errorf("SOCKS greeting=%x", greeting)
			return
		}
		if _, writeErr := connection.Write([]byte{0x05, 0x00}); writeErr != nil {
			serverDone <- writeErr
			return
		}
		receivedRequest := make([]byte, len(request))
		if _, readErr := io.ReadFull(
			connection,
			receivedRequest,
		); readErr != nil {
			serverDone <- readErr
			return
		}
		if !bytes.Equal(receivedRequest, request) {
			serverDone <- fmt.Errorf(
				"SOCKS request=%x want=%x",
				receivedRequest,
				request,
			)
			return
		}
		requestRead <- nil
		<-allowTunnel
		if _, writeErr := connection.Write([]byte{
			0x05, 0x00, 0x00, 0x01,
			127, 0, 0, 1, 0x1e, 0xd2,
		}); writeErr != nil {
			serverDone <- writeErr
			return
		}
		receivedTunnel := make([]byte, len(tunnelCanary))
		if _, readErr := io.ReadFull(
			connection,
			receivedTunnel,
		); readErr != nil {
			serverDone <- readErr
			return
		}
		if !bytes.Equal(receivedTunnel, tunnelCanary) {
			serverDone <- fmt.Errorf(
				"tunnel=%q want=%q",
				receivedTunnel,
				tunnelCanary,
			)
			return
		}
		serverDone <- nil
	}()

	command := exec.Command(
		os.Args[0],
		"-test.run=^TestProxyKernelHelper$",
	)
	command.Env = append(
		os.Environ(),
		"HIDEOUT_PROXY_HELPER=1",
		"HIDEOUT_PROXY_SERVER="+server.Addr().String(),
		"HIDEOUT_PROXY_UNCONFIGURED_SERVER="+
			unconfiguredServer.Addr().String(),
		"HIDEOUT_PROXY_UNCONFIGURED_CANARY="+string(unconfiguredCanary),
		"HIDEOUT_PROXY_TUNNEL="+string(tunnelCanary),
	)
	command.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    int(cgroup.Fd()),
	}
	var commandOutput bytes.Buffer
	command.Stdout = &commandOutput
	command.Stderr = &commandOutput
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	commandWait := make(chan error, 1)
	go func() { commandWait <- command.Wait() }()

	reader.SetProxyDeadline(time.Now().Add(10 * time.Second))
	expectedEgress := append(
		[]byte{0x05, 0x01, 0x00},
		request...,
	)
	var egress []byte
	var ingress []byte
	var socketCookie uint64
	nextTCP := make(map[uint32]uint32)
	initialized := make(map[uint32]bool)
	for !bytes.Equal(egress, expectedEgress) ||
		!bytes.Equal(ingress, []byte{0x05, 0x00}) {
		chunk, err := reader.ReadProxyChunk()
		if err != nil {
			counters, _ := reader.Counters()
			t.Fatalf(
				"read proxy egress=%x ingress=%x counters=%+v: %v",
				egress,
				ingress,
				counters,
				err,
			)
		}
		if chunk.CgroupID != cgroupID ||
			chunk.ExecutionPID == 0 ||
			chunk.ExecSequence == 0 ||
			chunk.ProxyPort != uint32(serverPort) ||
			chunk.ProxyIP().String() != "127.0.0.1" ||
			chunk.Flags != 0 {
			t.Fatalf("proxy chunk=%+v", chunk)
		}
		if socketCookie == 0 {
			socketCookie = chunk.SocketCookie
		} else if chunk.SocketCookie != socketCookie {
			t.Fatalf(
				"proxy cookie=%d want=%d",
				chunk.SocketCookie,
				socketCookie,
			)
		}
		if initialized[chunk.Direction] &&
			chunk.TCPSequence != nextTCP[chunk.Direction] {
			t.Fatalf(
				"proxy TCP discontinuity direction=%d sequence=%d want=%d",
				chunk.Direction,
				chunk.TCPSequence,
				nextTCP[chunk.Direction],
			)
		}
		initialized[chunk.Direction] = true
		payload := chunk.TakePayload()
		nextTCP[chunk.Direction] =
			chunk.TCPSequence + uint32(len(payload))
		switch chunk.Direction {
		case ProxyChunkEgress:
			egress = append(egress, payload...)
		case ProxyChunkIngress:
			ingress = append(ingress, payload...)
		default:
			clear(payload)
			t.Fatalf("proxy direction=%d", chunk.Direction)
		}
		clear(payload)
		if len(egress) > len(expectedEgress) ||
			len(ingress) > 2 {
			t.Fatalf(
				"unexpected handshake egress=%x ingress=%x",
				egress,
				ingress,
			)
		}
	}
	if err := <-requestRead; err != nil {
		t.Fatal(err)
	}
	if err := reader.CompleteProxyCapture(socketCookie); err != nil {
		t.Fatal(err)
	}
	close(allowTunnel)
	if err := <-commandWait; err != nil {
		t.Fatalf("proxy helper: %v\n%s", err, commandOutput.Bytes())
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if err := <-unconfiguredDone; err != nil {
		t.Fatal(err)
	}

	reader.SetProxyDeadline(time.Now().Add(300 * time.Millisecond))
	if chunk, err := reader.ReadProxyChunk(); err == nil {
		payload := chunk.TakePayload()
		clear(payload)
		t.Fatalf("tunnel payload entered proxy ring: %+v", chunk)
	}
	counters, err := reader.Counters()
	if err != nil {
		t.Fatal(err)
	}
	if counters.ProxyMatchedPackets < 3 ||
		counters.ProxyReservedChunks < 3 ||
		counters.ProxyRingbufDrops != 0 ||
		counters.ProxyCaptureFailures != 0 ||
		counters.ProxyTruncatedChunks != 0 ||
		counters.ProxyStateMisses != 0 ||
		counters.ProxyCompletedSkips == 0 ||
		counters.ProxyBudgetExhausted != 0 {
		t.Fatalf("proxy counters=%+v", counters)
	}
}

func TestProxyKernelHelper(t *testing.T) {
	if os.Getenv("HIDEOUT_PROXY_HELPER") != "1" {
		t.Skip("proxy kernel helper process")
	}
	unconfigured, err := net.DialTimeout(
		"tcp4",
		os.Getenv("HIDEOUT_PROXY_UNCONFIGURED_SERVER"),
		2*time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unconfigured.Write(
		[]byte(os.Getenv("HIDEOUT_PROXY_UNCONFIGURED_CANARY")),
	); err != nil {
		unconfigured.Close()
		t.Fatal(err)
	}
	if err := unconfigured.Close(); err != nil {
		t.Fatal(err)
	}

	connection, err := net.DialTimeout(
		"tcp4",
		os.Getenv("HIDEOUT_PROXY_SERVER"),
		2*time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if tcp, ok := connection.(*net.TCPConn); ok {
		if err := tcp.SetNoDelay(true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := connection.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(connection, method); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(method, []byte{0x05, 0x00}) {
		t.Fatalf("method=%x", method)
	}
	if _, err := connection.Write(proxyKernelRequest()); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 10)
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response[:4], []byte{0x05, 0x00, 0x00, 0x01}) {
		t.Fatalf("response=%x", response)
	}
	if _, err := connection.Write(
		[]byte(os.Getenv("HIDEOUT_PROXY_TUNNEL")),
	); err != nil {
		t.Fatal(err)
	}
}

func proxyKernelRequest() []byte {
	domain := "kernel.proxy.example.test"
	result := []byte{0x05, 0x01, 0x00, 0x03, byte(len(domain))}
	result = append(result, domain...)
	return append(result, 0x01, 0xbb)
}

func dnsKernelQuery() []byte {
	result := make([]byte, 12)
	binary.BigEndian.PutUint16(result[0:2], 0x4a3b)
	binary.BigEndian.PutUint16(result[2:4], 0x0100)
	binary.BigEndian.PutUint16(result[4:6], 1)
	for _, label := range []string{"fixture", "hideout", "invalid"} {
		result = append(result, byte(len(label)))
		result = append(result, label...)
	}
	result = append(result, 0)
	result = append(result, 0, 1, 0, 1)
	return result
}

func dnsKernelResponse(query []byte) []byte {
	result := append([]byte(nil), query...)
	binary.BigEndian.PutUint16(result[2:4], 0x8180)
	binary.BigEndian.PutUint16(result[6:8], 1)
	result = append(result, 0xc0, 0x0c)
	result = append(result, 0, 1, 0, 1)
	result = append(result, 0, 0, 0, 30)
	result = append(result, 0, 4, 192, 0, 2, 45)
	return result
}

func listenNetworkTCP(
	t *testing.T,
	network, address string,
) net.Listener {
	t.Helper()
	listener, err := net.Listen(network, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func listenNetworkUDP(
	t *testing.T,
	network, address string,
) *net.UDPConn {
	t.Helper()
	resolved, err := net.ResolveUDPAddr(network, address)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUDP(network, resolved)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func networkEndpointKey(
	kind, protocol uint32,
	address net.Addr,
) string {
	host, port, err := net.SplitHostPort(address.String())
	if err != nil {
		panic(err)
	}
	parsedPortValue, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf(
		"%d/%d/%s/%d",
		kind,
		protocol,
		net.ParseIP(host).String(),
		parsedPortValue,
	)
}
