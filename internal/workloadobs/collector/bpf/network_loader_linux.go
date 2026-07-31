//go:build linux

package bpf

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"golang.org/x/sys/unix"
)

var (
	ErrNetworkCollectorTarget        = errors.New("workload observer network collector target is invalid")
	ErrNetworkCollectorProcessState  = errors.New("workload observer network collector process state is unavailable")
	ErrNetworkCollectorProxyEndpoint = errors.New(
		"workload observer proxy capture endpoint is invalid",
	)
	ErrNetworkProxyCapture = errors.New(
		"workload observer proxy capture state is unavailable",
	)
)

type NetworkEventReader struct {
	manifest    ArtifactManifest
	objects     networkObserverObjects
	reader      *ringbuf.Reader
	dnsReader   *ringbuf.Reader
	proxyReader *ringbuf.Reader
	links       []link.Link
	hooks       []string

	stopOnce  sync.Once
	stopErr   error
	closeOnce sync.Once
	closeErr  error
}

type NetworkCollectorCounters struct {
	MatchedEvents        uint64
	ReservedEvents       uint64
	RingbufDrops         uint64
	StateDrops           uint64
	CorrelationHits      uint64
	CorrelationMisses    uint64
	UnsupportedEvents    uint64
	DNSMatchedPackets    uint64
	DNSReservedPackets   uint64
	DNSRingbufDrops      uint64
	DNSCaptureFailures   uint64
	DNSTruncatedPackets  uint64
	DNSStateMisses       uint64
	ProxyMatchedPackets  uint64
	ProxyReservedChunks  uint64
	ProxyRingbufDrops    uint64
	ProxyCaptureFailures uint64
	ProxyTruncatedChunks uint64
	ProxyStateMisses     uint64
	ProxyCompletedSkips  uint64
	ProxyBudgetExhausted uint64
}

type NetworkProxyEndpoint struct {
	IP   string
	Port uint16
}

type NetworkReaderOptions struct {
	DNSPlaintextPort uint16
	DNSEncryptedPort uint16
	ProxyEndpoints   []NetworkProxyEndpoint
}

func OpenNetworkEventReader(
	targetCgroupPath string,
	targetCgroupID uint64,
	processReader *ProcessEventReader,
) (*NetworkEventReader, error) {
	return OpenNetworkEventReaderWithOptions(
		targetCgroupPath,
		targetCgroupID,
		processReader,
		NetworkReaderOptions{
			DNSPlaintextPort: 53,
			DNSEncryptedPort: 853,
		},
	)
}

func OpenNetworkEventReaderWithOptions(
	targetCgroupPath string,
	targetCgroupID uint64,
	processReader *ProcessEventReader,
	options NetworkReaderOptions,
) (*NetworkEventReader, error) {
	proxyEndpointKeys, err := validatedProxyEndpointKeys(
		options.ProxyEndpoints,
	)
	if err != nil {
		return nil, err
	}
	if targetCgroupID == 0 ||
		!validCgroupPathSyntax(targetCgroupPath) ||
		options.DNSPlaintextPort == 0 ||
		options.DNSEncryptedPort == 0 ||
		options.DNSPlaintextPort == options.DNSEncryptedPort {
		return nil, ErrNetworkCollectorTarget
	}
	if processReader == nil ||
		processReader.objects.ExecSequences == nil ||
		processReader.objects.ObserverSequences == nil ||
		processReader.objects.TargetCgroupId == nil {
		return nil, ErrNetworkCollectorProcessState
	}
	var processTarget uint64
	if err := processReader.objects.TargetCgroupId.Get(&processTarget); err != nil {
		return nil, errors.Join(ErrNetworkCollectorProcessState, err)
	}
	if processTarget != targetCgroupID {
		return nil, ErrNetworkCollectorTarget
	}
	if err := validateNetworkCgroupIdentity(
		targetCgroupPath,
		targetCgroupID,
	); err != nil {
		return nil, err
	}

	spec, manifest, err := LoadEmbeddedNetworkSpec()
	if err != nil {
		return nil, err
	}
	target := spec.Variables[networkObserverVarNetworkTargetCgroupId]
	if target == nil || !target.Constant() {
		return nil, errors.New(
			"workload observer network target cgroup constant is missing",
		)
	}
	if err := target.Set(targetCgroupID); err != nil {
		return nil, fmt.Errorf(
			"set workload observer network cgroup target: %w",
			err,
		)
	}
	plaintextPort := spec.Variables[networkObserverVarDnsPlaintextPort]
	encryptedPort := spec.Variables[networkObserverVarDnsEncryptedPort]
	if plaintextPort == nil ||
		!plaintextPort.Constant() ||
		encryptedPort == nil ||
		!encryptedPort.Constant() {
		return nil, errors.New(
			"workload observer DNS port constants are missing",
		)
	}
	if err := plaintextPort.Set(uint32(options.DNSPlaintextPort)); err != nil {
		return nil, fmt.Errorf(
			"set workload observer plaintext DNS port: %w",
			err,
		)
	}
	if err := encryptedPort.Set(uint32(options.DNSEncryptedPort)); err != nil {
		return nil, fmt.Errorf(
			"set workload observer encrypted DNS port: %w",
			err,
		)
	}

	result := &NetworkEventReader{manifest: manifest}
	collectionOptions := ebpf.CollectionOptions{
		MapReplacements: map[string]*ebpf.Map{
			networkObserverMapExecSequences:     processReader.objects.ExecSequences,
			networkObserverMapObserverSequences: processReader.objects.ObserverSequences,
		},
	}
	if err := spec.LoadAndAssign(
		&result.objects,
		&collectionOptions,
	); err != nil {
		return nil, errors.Join(
			fmt.Errorf("load workload observer network programs: %w", err),
			closeNetworkObserverObjects(&result.objects),
		)
	}
	for _, key := range proxyEndpointKeys {
		if err := result.objects.ProxyEndpoints.Put(
			key,
			uint32(1),
		); err != nil {
			return nil, errors.Join(
				fmt.Errorf(
					"configure workload observer proxy endpoint: %w",
					err,
				),
				result.Close(),
			)
		}
	}
	result.reader, err = ringbuf.NewReader(
		result.objects.NetworkObservationEvents,
	)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("open workload observer network ring: %w", err),
			closeNetworkObserverObjects(&result.objects),
		)
	}
	result.dnsReader, err = ringbuf.NewReader(
		result.objects.DnsPacketEvents,
	)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("open workload observer DNS packet ring: %w", err),
			result.Close(),
		)
	}
	result.proxyReader, err = ringbuf.NewReader(
		result.objects.ProxyHandshakeEvents,
	)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf(
				"open workload observer proxy handshake ring: %w",
				err,
			),
			result.Close(),
		)
	}

	hooks := []struct {
		name       string
		attachType ebpf.AttachType
		program    *ebpf.Program
	}{
		{
			name:       "cgroup/connect4",
			attachType: ebpf.AttachCGroupInet4Connect,
			program:    result.objects.HideoutObserveConnect4,
		},
		{
			name:       "cgroup/connect6",
			attachType: ebpf.AttachCGroupInet6Connect,
			program:    result.objects.HideoutObserveConnect6,
		},
		{
			name:       "cgroup/sendmsg4",
			attachType: ebpf.AttachCGroupUDP4Sendmsg,
			program:    result.objects.HideoutObserveSendmsg4,
		},
		{
			name:       "cgroup/sendmsg6",
			attachType: ebpf.AttachCGroupUDP6Sendmsg,
			program:    result.objects.HideoutObserveSendmsg6,
		},
		{
			name:       "cgroup_skb/egress",
			attachType: ebpf.AttachCGroupInetEgress,
			program:    result.objects.HideoutCorrelateEgress,
		},
		{
			name:       "cgroup_skb/ingress",
			attachType: ebpf.AttachCGroupInetIngress,
			program:    result.objects.HideoutObserveIngress,
		},
	}
	for _, hook := range hooks {
		if hook.program == nil {
			return nil, errors.Join(
				fmt.Errorf(
					"workload observer network hook %s program is missing",
					hook.name,
				),
				result.Close(),
			)
		}
		attached, attachErr := link.AttachCgroup(link.CgroupOptions{
			Path:    targetCgroupPath,
			Attach:  hook.attachType,
			Program: hook.program,
		})
		if attachErr != nil {
			return nil, errors.Join(
				fmt.Errorf(
					"attach workload observer network hook %s: %w",
					hook.name,
					attachErr,
				),
				result.Close(),
			)
		}
		result.links = append(result.links, attached)
		result.hooks = append(result.hooks, hook.name)
	}
	return result, nil
}

func (reader *NetworkEventReader) ReadDNSPacket() (
	RawDNSPacket,
	error,
) {
	if reader == nil || reader.dnsReader == nil {
		return RawDNSPacket{}, ringbuf.ErrClosed
	}
	record, err := reader.dnsReader.Read()
	if err != nil {
		return RawDNSPacket{}, err
	}
	return DecodeDNSPacket(record.RawSample)
}

func (reader *NetworkEventReader) ReadProxyChunk() (
	RawProxyChunk,
	error,
) {
	if reader == nil || reader.proxyReader == nil {
		return RawProxyChunk{}, ringbuf.ErrClosed
	}
	record, err := reader.proxyReader.Read()
	if err != nil {
		return RawProxyChunk{}, err
	}
	return DecodeProxyChunk(record.RawSample)
}

func (reader *NetworkEventReader) ReadNetworkEvent() (
	RawNetworkEvent,
	error,
) {
	if reader == nil || reader.reader == nil {
		return RawNetworkEvent{}, ringbuf.ErrClosed
	}
	record, err := reader.reader.Read()
	if err != nil {
		return RawNetworkEvent{}, err
	}
	return DecodeNetworkEvent(record.RawSample)
}

func (reader *NetworkEventReader) SocketEvidence(
	event RawNetworkEvent,
) (NetworkSocketEvidence, error) {
	if reader == nil ||
		reader.objects.NetworkSocketStates == nil ||
		event.SocketCookie == 0 {
		return NetworkSocketEvidence{}, ErrNetworkCorrelation
	}
	var state networkObserverNetworkSocketState
	if err := reader.objects.NetworkSocketStates.Lookup(
		event.SocketCookie,
		&state,
	); err != nil {
		return NetworkSocketEvidence{}, errors.Join(
			ErrNetworkCorrelation,
			err,
		)
	}
	if state.Kind != event.Kind ||
		state.Pid != event.PID ||
		state.ExecutionPid != event.ExecutionPID ||
		state.Uid != event.UID ||
		state.Gid != event.GID ||
		state.Family != event.Family ||
		state.Protocol != event.Protocol ||
		state.DestinationPort != event.DestinationPort ||
		state.ObserverSequence != event.ObserverSequence ||
		state.ExecSequence != event.ExecSequence ||
		state.CgroupId != event.CgroupID ||
		!bytes.Equal(state.Address[:], event.Address[:]) {
		return NetworkSocketEvidence{}, ErrNetworkCorrelation
	}
	evidence := NetworkSocketEvidence{
		ObserverSequence: state.ObserverSequence,
		SocketCookie:     event.SocketCookie,
		IfIndex:          state.Ifindex,
		EgressPackets:    state.EgressPackets,
		EgressBytes:      state.EgressBytes,
	}
	if err := evidence.ValidateFor(event); err != nil {
		return NetworkSocketEvidence{}, err
	}
	return evidence, nil
}

func (reader *NetworkEventReader) SetDeadline(deadline time.Time) {
	if reader != nil && reader.reader != nil {
		reader.reader.SetDeadline(deadline)
	}
}

func (reader *NetworkEventReader) SetDNSDeadline(deadline time.Time) {
	if reader != nil && reader.dnsReader != nil {
		reader.dnsReader.SetDeadline(deadline)
	}
}

func (reader *NetworkEventReader) SetProxyDeadline(deadline time.Time) {
	if reader != nil && reader.proxyReader != nil {
		reader.proxyReader.SetDeadline(deadline)
	}
}

// FlushPending drains each active network stream through its read boundary.
// Flush is safe to invoke concurrently with a blocked ringbuf Read, whereas
// SetDeadline serializes on the reader lock and cannot be used for shutdown.
func (reader *NetworkEventReader) FlushPending() error {
	if reader == nil {
		return ringbuf.ErrClosed
	}
	var result error
	for _, current := range []*ringbuf.Reader{
		reader.reader,
		reader.dnsReader,
		reader.proxyReader,
	} {
		if current != nil {
			result = errors.Join(result, current.Flush())
		}
	}
	return result
}

// CompleteProxyCapture prevents any later payload from the socket from being
// copied into the transient handshake ring. A fresh connect with a reused
// cookie clears this tombstone in the kernel before recording socket state.
func (reader *NetworkEventReader) CompleteProxyCapture(
	socketCookie uint64,
) error {
	if reader == nil ||
		reader.objects.ProxyCompletedSockets == nil ||
		socketCookie == 0 {
		return ErrNetworkProxyCapture
	}
	if err := reader.objects.ProxyCompletedSockets.Put(
		socketCookie,
		uint32(1),
	); err != nil {
		return errors.Join(ErrNetworkProxyCapture, err)
	}
	return nil
}

func (reader *NetworkEventReader) AttachedHooks() []string {
	if reader == nil {
		return nil
	}
	return append([]string(nil), reader.hooks...)
}

func (reader *NetworkEventReader) ArtifactManifest() ArtifactManifest {
	if reader == nil {
		return ArtifactManifest{}
	}
	return reader.manifest
}

func (reader *NetworkEventReader) Counters() (
	NetworkCollectorCounters,
	error,
) {
	if reader == nil || reader.objects.NetworkCounters == nil {
		return NetworkCollectorCounters{}, ringbuf.ErrClosed
	}
	cpus, err := ebpf.PossibleCPU()
	if err != nil {
		return NetworkCollectorCounters{}, err
	}
	values := make([]networkObserverNetworkCollectorCounters, cpus)
	if err := reader.objects.NetworkCounters.Lookup(
		uint32(0),
		&values,
	); err != nil {
		return NetworkCollectorCounters{}, err
	}
	var result NetworkCollectorCounters
	for _, value := range values {
		additions := []struct {
			target *uint64
			value  uint64
		}{
			{&result.MatchedEvents, value.MatchedEvents},
			{&result.ReservedEvents, value.ReservedEvents},
			{&result.RingbufDrops, value.RingbufDrops},
			{&result.StateDrops, value.StateDrops},
			{&result.CorrelationHits, value.CorrelationHits},
			{&result.CorrelationMisses, value.CorrelationMisses},
			{&result.UnsupportedEvents, value.UnsupportedEvents},
			{&result.DNSMatchedPackets, value.DnsMatchedPackets},
			{&result.DNSReservedPackets, value.DnsReservedPackets},
			{&result.DNSRingbufDrops, value.DnsRingbufDrops},
			{&result.DNSCaptureFailures, value.DnsCaptureFailures},
			{&result.DNSTruncatedPackets, value.DnsTruncatedPackets},
			{&result.DNSStateMisses, value.DnsStateMisses},
			{&result.ProxyMatchedPackets, value.ProxyMatchedPackets},
			{&result.ProxyReservedChunks, value.ProxyReservedChunks},
			{&result.ProxyRingbufDrops, value.ProxyRingbufDrops},
			{&result.ProxyCaptureFailures, value.ProxyCaptureFailures},
			{&result.ProxyTruncatedChunks, value.ProxyTruncatedChunks},
			{&result.ProxyStateMisses, value.ProxyStateMisses},
			{&result.ProxyCompletedSkips, value.ProxyCompletedSkips},
			{&result.ProxyBudgetExhausted, value.ProxyBudgetExhausted},
		}
		for _, addition := range additions {
			if math.MaxUint64-*addition.target < addition.value {
				return NetworkCollectorCounters{}, errors.New(
					"workload observer network counters overflow",
				)
			}
			*addition.target += addition.value
		}
	}
	return result, nil
}

func (reader *NetworkEventReader) Close() error {
	if reader == nil {
		return nil
	}
	reader.closeOnce.Do(func() {
		reader.closeErr = errors.Join(reader.closeErr, reader.Stop())
		for index := len(reader.links) - 1; index >= 0; index-- {
			reader.closeErr = errors.Join(
				reader.closeErr,
				reader.links[index].Close(),
			)
		}
		reader.closeErr = errors.Join(
			reader.closeErr,
			closeNetworkObserverObjects(&reader.objects),
		)
	})
	return reader.closeErr
}

// Stop interrupts every userspace ring reader while retaining programs and
// maps for the final aggregate loss snapshot.
func (reader *NetworkEventReader) Stop() error {
	if reader == nil {
		return nil
	}
	reader.stopOnce.Do(func() {
		if reader.reader != nil {
			reader.stopErr = errors.Join(
				reader.stopErr,
				reader.reader.Close(),
			)
		}
		if reader.dnsReader != nil {
			reader.stopErr = errors.Join(
				reader.stopErr,
				reader.dnsReader.Close(),
			)
		}
		if reader.proxyReader != nil {
			reader.stopErr = errors.Join(
				reader.stopErr,
				reader.proxyReader.Close(),
			)
		}
	})
	return reader.stopErr
}

func validCgroupPathSyntax(value string) bool {
	return filepath.IsAbs(value) &&
		filepath.Clean(value) == value &&
		strings.HasPrefix(value, "/sys/fs/cgroup/") &&
		len(value) <= 4096 &&
		strings.IndexByte(value, 0) < 0
}

func validatedProxyEndpointKeys(
	endpoints []NetworkProxyEndpoint,
) ([]networkObserverProxyEndpointKey, error) {
	if len(endpoints) > 256 {
		return nil, ErrNetworkCollectorProxyEndpoint
	}
	result := make([]networkObserverProxyEndpointKey, 0, len(endpoints))
	seen := make(map[networkObserverProxyEndpointKey]struct{}, len(endpoints))
	for _, endpoint := range endpoints {
		ip := net.ParseIP(endpoint.IP)
		if ip == nil ||
			ip.IsUnspecified() ||
			endpoint.Port == 0 {
			return nil, ErrNetworkCollectorProxyEndpoint
		}
		key := networkObserverProxyEndpointKey{
			Port: uint32(endpoint.Port),
		}
		if ipv4 := ip.To4(); ipv4 != nil {
			key.Family = NetworkFamilyIPv4
			copy(key.Address[:net.IPv4len], ipv4)
		} else {
			ipv6 := ip.To16()
			if ipv6 == nil {
				return nil, ErrNetworkCollectorProxyEndpoint
			}
			key.Family = NetworkFamilyIPv6
			copy(key.Address[:], ipv6)
		}
		if _, exists := seen[key]; exists {
			return nil, ErrNetworkCollectorProxyEndpoint
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result, nil
}

func validateNetworkCgroupIdentity(path string, expectedID uint64) error {
	info, err := os.Lstat(path)
	if err != nil ||
		!info.IsDir() ||
		info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(ErrNetworkCollectorTarget, err)
	}
	handle, _, err := unix.NameToHandleAt(unix.AT_FDCWD, path, 0)
	if err != nil {
		return errors.Join(ErrNetworkCollectorTarget, err)
	}
	value := handle.Bytes()
	if len(value) != 8 ||
		binary.NativeEndian.Uint64(value) != expectedID {
		return ErrNetworkCollectorTarget
	}
	return nil
}

func closeNetworkObserverObjects(
	objects *networkObserverObjects,
) error {
	if objects == nil {
		return nil
	}
	var result error
	programs := []*ebpf.Program{
		objects.HideoutCorrelateEgress,
		objects.HideoutObserveIngress,
		objects.HideoutObserveConnect4,
		objects.HideoutObserveConnect6,
		objects.HideoutObserveSendmsg4,
		objects.HideoutObserveSendmsg6,
	}
	for _, program := range programs {
		if program != nil {
			result = errors.Join(result, program.Close())
		}
	}
	maps := []*ebpf.Map{
		objects.DnsPacketEvents,
		objects.ExecSequences,
		objects.NetworkCounters,
		objects.NetworkObservationEvents,
		objects.NetworkSocketStates,
		objects.ObserverSequences,
		objects.ProxyCompletedSockets,
		objects.ProxyEndpoints,
		objects.ProxyHandshakeEvents,
	}
	for _, currentMap := range maps {
		if currentMap != nil {
			result = errors.Join(result, currentMap.Close())
		}
	}
	return result
}
