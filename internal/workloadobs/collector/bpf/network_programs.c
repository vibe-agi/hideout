//go:build ignore

// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-only
//
// Package-owned cgroup network observation programs. The sock-address hooks
// capture the calling process and requested endpoint without changing either.
// Packet hooks copy only bounded DNS metadata or bounded SOCKS handshake
// prefixes for positively configured endpoints into transient ring buffers.

#define SEC(name) __attribute__((section(name), used))
#define __always_inline inline __attribute__((always_inline))
#define __uint(name, value) int (*name)[value]
#define __type(name, value) value *name

typedef unsigned char __u8;
typedef unsigned short __u16;
typedef unsigned int __u32;
typedef unsigned long long __u64;

enum {
	BPF_MAP_TYPE_HASH = 1,
	BPF_MAP_TYPE_PERCPU_ARRAY = 6,
	BPF_MAP_TYPE_LRU_HASH = 9,
	BPF_MAP_TYPE_RINGBUF = 27,
	BPF_ANY = 0,
};

enum hideout_network_kind {
	HIDEOUT_NETWORK_CONNECT = 1,
	HIDEOUT_NETWORK_SENDMSG = 2,
};

enum hideout_network_family {
	HIDEOUT_NETWORK_IPV4 = 4,
	HIDEOUT_NETWORK_IPV6 = 6,
};

enum hideout_network_protocol {
	HIDEOUT_NETWORK_TCP = 6,
	HIDEOUT_NETWORK_UDP = 17,
};

enum hideout_network_flags {
	HIDEOUT_NETWORK_EXECUTION_UNAVAILABLE = 1U << 0,
	HIDEOUT_NETWORK_COOKIE_UNAVAILABLE = 1U << 1,
	HIDEOUT_NETWORK_STATE_UNAVAILABLE = 1U << 2,
};

enum hideout_dns_direction {
	HIDEOUT_DNS_EGRESS = 1,
	HIDEOUT_DNS_INGRESS = 2,
};

enum hideout_dns_flags {
	HIDEOUT_DNS_TRUNCATED = 1U << 0,
	HIDEOUT_DNS_ENCRYPTED = 1U << 1,
	HIDEOUT_DNS_EXECUTION_UNAVAILABLE = 1U << 2,
};

enum hideout_proxy_flags {
	HIDEOUT_PROXY_TRUNCATED = 1U << 0,
	HIDEOUT_PROXY_EXECUTION_UNAVAILABLE = 1U << 1,
};

enum {
	HIDEOUT_DNS_PAYLOAD_BYTES = 512,
	HIDEOUT_PROXY_PAYLOAD_BYTES = 512,
	HIDEOUT_PROXY_FLOW_CAPTURE_BYTES = 4096,
};

struct bpf_sock;

struct bpf_sock_addr {
	__u32 user_family;
	__u32 user_ip4;
	__u32 user_ip6[4];
	__u32 user_port;
	__u32 family;
	__u32 type;
	__u32 protocol;
	__u32 msg_src_ip4;
	__u32 msg_src_ip6[4];
	struct bpf_sock *sk;
};

struct __sk_buff {
	__u32 len;
	__u32 pkt_type;
	__u32 mark;
	__u32 queue_mapping;
	__u32 protocol;
	__u32 vlan_present;
	__u32 vlan_tci;
	__u32 vlan_proto;
	__u32 priority;
	__u32 ingress_ifindex;
	__u32 ifindex;
};

struct fork_parent {
	__u32 pid;
	__u64 exec_sequence;
};

struct network_collector_counters {
	__u64 matched_events;
	__u64 reserved_events;
	__u64 ringbuf_drops;
	__u64 state_drops;
	__u64 correlation_hits;
	__u64 correlation_misses;
	__u64 unsupported_events;
	__u64 dns_matched_packets;
	__u64 dns_reserved_packets;
	__u64 dns_ringbuf_drops;
	__u64 dns_capture_failures;
	__u64 dns_truncated_packets;
	__u64 dns_state_misses;
	__u64 proxy_matched_packets;
	__u64 proxy_reserved_chunks;
	__u64 proxy_ringbuf_drops;
	__u64 proxy_capture_failures;
	__u64 proxy_truncated_chunks;
	__u64 proxy_state_misses;
	__u64 proxy_completed_skips;
	__u64 proxy_budget_exhausted;
};

struct network_socket_state {
	__u32 kind;
	__u32 pid;
	__u32 execution_pid;
	__u32 uid;
	__u32 gid;
	__u32 family;
	__u32 protocol;
	__u32 destination_port;
	__u32 ifindex;
	__u64 observer_sequence;
	__u64 exec_sequence;
	__u64 cgroup_id;
	__u64 egress_packets;
	__u64 egress_bytes;
	__u64 proxy_capture_bytes;
	__u8 address[16];
};

struct hideout_network_event {
	__u32 kind;
	__u32 cpu;
	__u32 pid;
	__u32 tid;
	__u32 uid;
	__u32 gid;
	__u32 family;
	__u32 protocol;
	__u32 destination_port;
	__u32 execution_pid;
	__u32 flags;
	__u32 reserved;
	__u64 cgroup_id;
	__u64 observer_sequence;
	__u64 exec_sequence;
	__u64 monotonic_ns;
	__u64 socket_cookie;
	__u64 bytes;
	__u8 address[16];
};

struct hideout_dns_packet_event {
	__u32 direction;
	__u32 cpu;
	__u32 pid;
	__u32 tid;
	__u32 execution_pid;
	__u32 uid;
	__u32 gid;
	__u32 family;
	__u32 protocol;
	__u32 resolver_port;
	__u32 flags;
	__u32 wire_length;
	__u32 captured_length;
	__u32 reserved;
	__u64 cgroup_id;
	__u64 observer_sequence;
	__u64 exec_sequence;
	__u64 monotonic_ns;
	__u64 socket_cookie;
	__u8 address[16];
	__u8 payload[HIDEOUT_DNS_PAYLOAD_BYTES];
};

struct hideout_proxy_chunk_event {
	__u32 direction;
	__u32 cpu;
	__u32 pid;
	__u32 tid;
	__u32 execution_pid;
	__u32 uid;
	__u32 gid;
	__u32 family;
	__u32 proxy_port;
	__u32 flags;
	__u32 wire_length;
	__u32 captured_length;
	__u32 tcp_sequence;
	__u32 reserved;
	__u64 cgroup_id;
	__u64 observer_sequence;
	__u64 exec_sequence;
	__u64 monotonic_ns;
	__u64 socket_cookie;
	__u8 address[16];
	__u8 payload[HIDEOUT_PROXY_PAYLOAD_BYTES];
};

struct proxy_endpoint_key {
	__u32 family;
	__u32 port;
	__u8 address[16];
};

struct packet_metadata {
	__u32 family;
	__u32 protocol;
	__u32 source_port;
	__u32 destination_port;
	__u32 payload_offset;
	__u32 payload_length;
	__u32 tcp_sequence;
	__u8 source_address[16];
	__u8 destination_address[16];
};

struct {
	__uint(type, BPF_MAP_TYPE_RINGBUF);
	__uint(max_entries, 4 * 1024 * 1024);
} network_observation_events SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_RINGBUF);
	__uint(max_entries, 4 * 1024 * 1024);
} dns_packet_events SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_RINGBUF);
	__uint(max_entries, 4 * 1024 * 1024);
} proxy_handshake_events SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__uint(max_entries, 65536);
	__type(key, __u64);
	__type(value, struct network_socket_state);
} network_socket_states SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 256);
	__type(key, struct proxy_endpoint_key);
	__type(value, __u32);
} proxy_endpoints SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__uint(max_entries, 65536);
	__type(key, __u64);
	__type(value, __u32);
} proxy_completed_sockets SEC(".maps");

/*
 * Replaced by the process collector maps at load time. Keeping the exact
 * names and layouts makes execution identity and observer sequencing shared
 * rather than guessed from a second userspace process table.
 */
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 65536);
	__type(key, __u32);
	__type(value, struct fork_parent);
} exec_sequences SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, __u64);
} observer_sequences SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, struct network_collector_counters);
} network_counters SEC(".maps");

const volatile __u64 network_target_cgroup_id = 0;
const volatile __u32 dns_plaintext_port = 53;
const volatile __u32 dns_encrypted_port = 853;

static void *(*bpf_map_lookup_elem)(void *map, const void *key) = (void *)1;
static long (*bpf_map_update_elem)(void *map, const void *key,
				  const void *value, __u64 flags) = (void *)2;
static long (*bpf_map_delete_elem)(void *map, const void *key) = (void *)3;
static __u64 (*bpf_ktime_get_ns)(void) = (void *)5;
static __u32 (*bpf_get_smp_processor_id)(void) = (void *)8;
static __u64 (*bpf_get_current_pid_tgid)(void) = (void *)14;
static __u64 (*bpf_get_current_uid_gid)(void) = (void *)15;
static long (*bpf_skb_load_bytes)(const void *ctx, __u32 offset,
				  void *to, __u32 len) = (void *)26;
static __u64 (*bpf_get_socket_cookie)(void *ctx) = (void *)46;
static __u64 (*bpf_get_current_cgroup_id)(void) = (void *)80;
static void *(*bpf_ringbuf_reserve)(void *ringbuf, __u64 size,
				    __u64 flags) = (void *)131;
static void (*bpf_ringbuf_submit)(void *data, __u64 flags) = (void *)132;
static void (*bpf_ringbuf_discard)(void *data, __u64 flags) = (void *)133;

static __always_inline struct network_collector_counters *
current_counters(void)
{
	__u32 zero = 0;

	return bpf_map_lookup_elem(&network_counters, &zero);
}

static __always_inline void note_state_drop(void)
{
	struct network_collector_counters *counters = current_counters();

	if (counters)
		counters->state_drops++;
}

static __always_inline __u64 next_observer_sequence(void)
{
	__u32 zero = 0;
	__u64 *sequence = bpf_map_lookup_elem(&observer_sequences, &zero);

	if (!sequence)
		return 0;
	return __sync_fetch_and_add(sequence, 1) + 1;
}

static __always_inline int in_target_cgroup(void)
{
	__u64 current = bpf_get_current_cgroup_id();

	return network_target_cgroup_id != 0 &&
	       current == network_target_cgroup_id;
}

static __always_inline int supported_protocol(__u32 protocol)
{
	return protocol == HIDEOUT_NETWORK_TCP ||
	       protocol == HIDEOUT_NETWORK_UDP;
}

static __always_inline void snapshot_address(
	__u8 destination[16],
	struct bpf_sock_addr *ctx,
	__u32 family)
{
	__u32 *words = (__u32 *)destination;

	if (family == HIDEOUT_NETWORK_IPV4) {
		words[0] = ctx->user_ip4;
		return;
	}
	words[0] = ctx->user_ip6[0];
	words[1] = ctx->user_ip6[1];
	words[2] = ctx->user_ip6[2];
	words[3] = ctx->user_ip6[3];
}

static __always_inline __u32 read_be16(const __u8 *value)
{
	return ((__u32)value[0] << 8) | value[1];
}

static __always_inline __u32 read_be32(const __u8 *value)
{
	return ((__u32)value[0] << 24) |
	       ((__u32)value[1] << 16) |
	       ((__u32)value[2] << 8) |
	       value[3];
}

static __always_inline int packet_address_equal(
	const __u8 left[16],
	const __u8 right[16],
	__u32 family)
{
	int index;

#pragma unroll
	for (index = 0; index < 16; index++) {
		if (family == HIDEOUT_NETWORK_IPV4 && index >= 4)
			continue;
		if (left[index] != right[index])
			return 0;
	}
	return 1;
}

static __always_inline int load_packet_metadata(
	struct __sk_buff *ctx,
	struct packet_metadata *metadata)
{
	__u8 header[40] = {};
	__u8 transport[20] = {};
	__u32 transport_offset;
	__u32 packet_length;
	__u32 header_length;
	__u32 transport_length;
	__u32 version;

	if (!ctx || !metadata || ctx->len < 20 ||
	    bpf_skb_load_bytes(ctx, 0, header, 20) < 0)
		return -1;
	version = header[0] >> 4;
	if (version == 4) {
		header_length = (header[0] & 0x0f) * 4;
		packet_length = read_be16(&header[2]);
		if (header_length < 20 || header_length > 60 ||
		    packet_length < header_length ||
		    packet_length > ctx->len ||
		    (read_be16(&header[6]) & 0x3fff) != 0)
			return -1;
		metadata->family = HIDEOUT_NETWORK_IPV4;
		metadata->protocol = header[9];
		__builtin_memcpy(
			metadata->source_address,
			&header[12],
			4);
		__builtin_memcpy(
			metadata->destination_address,
			&header[16],
			4);
		transport_offset = header_length;
	} else if (version == 6) {
		if (ctx->len < 40 ||
		    bpf_skb_load_bytes(ctx, 0, header, 40) < 0)
			return -1;
		packet_length = 40 + read_be16(&header[4]);
		if (packet_length < 40 || packet_length > ctx->len)
			return -1;
		metadata->family = HIDEOUT_NETWORK_IPV6;
		metadata->protocol = header[6];
		__builtin_memcpy(
			metadata->source_address,
			&header[8],
			16);
		__builtin_memcpy(
			metadata->destination_address,
			&header[24],
			16);
		transport_offset = 40;
	} else {
		return -1;
	}
	if (metadata->protocol != HIDEOUT_NETWORK_TCP &&
	    metadata->protocol != HIDEOUT_NETWORK_UDP)
		return -1;
	if (packet_length < transport_offset + 20 ||
	    bpf_skb_load_bytes(
		    ctx,
		    transport_offset,
		    transport,
		    sizeof(transport)) < 0)
		return -1;
	metadata->source_port = read_be16(&transport[0]);
	metadata->destination_port = read_be16(&transport[2]);
	if (metadata->protocol == HIDEOUT_NETWORK_UDP) {
		transport_length = read_be16(&transport[4]);
		if (transport_length < 8 ||
		    transport_length > packet_length - transport_offset)
			return -1;
		metadata->payload_offset = transport_offset + 8;
		metadata->payload_length = transport_length - 8;
	} else {
		header_length = (transport[12] >> 4) * 4;
		if (header_length < 20 ||
		    header_length > packet_length - transport_offset)
			return -1;
		metadata->tcp_sequence = read_be32(&transport[4]);
		metadata->payload_offset = transport_offset + header_length;
		metadata->payload_length =
			packet_length - metadata->payload_offset;
	}
	return 0;
}

static __always_inline int load_bounded_packet_payload(
	struct __sk_buff *ctx,
	__u32 offset,
	__u8 *payload,
	__u32 captured_length)
{
	if (!captured_length ||
	    captured_length > HIDEOUT_DNS_PAYLOAD_BYTES)
		return -1;
	/*
	 * The verifier does not retain the enum clamp through all of the ring
	 * buffer writes above the helper call. Keep the exact upper-bound branch
	 * and mask the remaining branch so it can prove the 512-byte destination
	 * cannot be overrun.
	 */
	if (captured_length == HIDEOUT_DNS_PAYLOAD_BYTES)
		return bpf_skb_load_bytes(
			ctx, offset, payload, HIDEOUT_DNS_PAYLOAD_BYTES);
	asm volatile("" : "+r"(captured_length));
	captured_length &= HIDEOUT_DNS_PAYLOAD_BYTES - 1;
	if (!captured_length)
		return -1;
	return bpf_skb_load_bytes(ctx, offset, payload, captured_length);
}

static __always_inline void observe_dns_packet(
	struct __sk_buff *ctx,
	struct network_socket_state *state,
	__u64 cookie,
	__u32 direction)
{
	struct network_collector_counters *counters;
	struct hideout_dns_packet_event *event;
	struct network_socket_state snapshot = {};
	struct packet_metadata metadata = {};
	const __u8 *remote_address;
	__u32 remote_port;
	__u32 captured_length;
	int encrypted;

	if (!state || !cookie)
		return;
	snapshot = *state;
	if (snapshot.destination_port != dns_plaintext_port &&
	    snapshot.destination_port != dns_encrypted_port)
		return;
	counters = current_counters();
	if (load_packet_metadata(ctx, &metadata) < 0) {
		if (counters)
			counters->dns_capture_failures++;
		return;
	}
	if (direction == HIDEOUT_DNS_EGRESS) {
		remote_port = metadata.destination_port;
		remote_address = metadata.destination_address;
	} else {
		remote_port = metadata.source_port;
		remote_address = metadata.source_address;
	}
	if (metadata.family != snapshot.family ||
	    metadata.protocol != snapshot.protocol ||
	    remote_port != snapshot.destination_port ||
	    !packet_address_equal(
		    remote_address,
		    snapshot.address,
		    snapshot.family)) {
		if (counters)
			counters->dns_state_misses++;
		return;
	}
	encrypted = remote_port == dns_encrypted_port;
	if ((!encrypted && remote_port != dns_plaintext_port) ||
	    (encrypted && metadata.protocol != HIDEOUT_NETWORK_TCP) ||
	    (!encrypted && metadata.payload_length == 0))
		return;
	if (counters)
		counters->dns_matched_packets++;
	event = bpf_ringbuf_reserve(
		&dns_packet_events,
		sizeof(*event),
		0);
	if (!event) {
		if (counters)
			counters->dns_ringbuf_drops++;
		return;
	}
	if (counters)
		counters->dns_reserved_packets++;
	__builtin_memset(event, 0, sizeof(*event));
	event->direction = direction;
	event->cpu = bpf_get_smp_processor_id();
	event->pid = snapshot.pid;
	event->tid = snapshot.pid;
	event->execution_pid = snapshot.execution_pid;
	event->uid = snapshot.uid;
	event->gid = snapshot.gid;
	event->family = snapshot.family;
	event->protocol = snapshot.protocol;
	event->resolver_port = snapshot.destination_port;
	event->cgroup_id = snapshot.cgroup_id;
	event->observer_sequence = next_observer_sequence();
	if (!event->observer_sequence) {
		if (counters)
			counters->dns_capture_failures++;
		bpf_ringbuf_discard(event, 0);
		return;
	}
	event->exec_sequence = snapshot.exec_sequence;
	if (!snapshot.execution_pid || !snapshot.exec_sequence)
		event->flags |= HIDEOUT_DNS_EXECUTION_UNAVAILABLE;
	event->monotonic_ns = bpf_ktime_get_ns();
	event->socket_cookie = cookie;
	__builtin_memcpy(
		event->address,
		snapshot.address,
		sizeof(event->address));
	if (encrypted) {
		event->flags |= HIDEOUT_DNS_ENCRYPTED;
		bpf_ringbuf_submit(event, 0);
		return;
	}
	event->wire_length = metadata.payload_length;
	captured_length = metadata.payload_length;
	if (captured_length > HIDEOUT_DNS_PAYLOAD_BYTES) {
		captured_length = HIDEOUT_DNS_PAYLOAD_BYTES;
		event->flags |= HIDEOUT_DNS_TRUNCATED;
		if (counters)
			counters->dns_truncated_packets++;
	}
	event->captured_length = captured_length;
	if (load_bounded_packet_payload(
		    ctx,
		    metadata.payload_offset,
		    event->payload,
		    captured_length) < 0) {
		if (counters)
			counters->dns_capture_failures++;
		bpf_ringbuf_discard(event, 0);
		return;
	}
	bpf_ringbuf_submit(event, 0);
}

static __always_inline int proxy_endpoint_configured(
	const struct network_socket_state *state)
{
	struct proxy_endpoint_key key = {};

	if (!state ||
	    state->protocol != HIDEOUT_NETWORK_TCP ||
	    (state->family != HIDEOUT_NETWORK_IPV4 &&
	     state->family != HIDEOUT_NETWORK_IPV6))
		return 0;
	key.family = state->family;
	key.port = state->destination_port;
	__builtin_memcpy(
		key.address,
		state->address,
		sizeof(key.address));
	return bpf_map_lookup_elem(&proxy_endpoints, &key) != 0;
}

static __always_inline void observe_proxy_packet(
	struct __sk_buff *ctx,
	struct network_socket_state *state,
	__u64 cookie,
	__u32 direction)
{
	struct network_collector_counters *counters;
	struct hideout_proxy_chunk_event *event;
	struct network_socket_state snapshot = {};
	struct packet_metadata metadata = {};
	const __u8 *remote_address;
	__u64 prior_capture_bytes;
	__u64 remaining;
	__u32 remote_port;
	__u32 captured_length;

	if (!state || !cookie || !proxy_endpoint_configured(state))
		return;
	counters = current_counters();
	if (bpf_map_lookup_elem(&proxy_completed_sockets, &cookie)) {
		if (counters)
			counters->proxy_completed_skips++;
		return;
	}
	snapshot = *state;
	if (load_packet_metadata(ctx, &metadata) < 0) {
		if (counters)
			counters->proxy_capture_failures++;
		return;
	}
	if (metadata.protocol != HIDEOUT_NETWORK_TCP ||
	    metadata.payload_length == 0)
		return;
	if (direction == HIDEOUT_DNS_EGRESS) {
		remote_port = metadata.destination_port;
		remote_address = metadata.destination_address;
	} else {
		remote_port = metadata.source_port;
		remote_address = metadata.source_address;
	}
	if (metadata.family != snapshot.family ||
	    remote_port != snapshot.destination_port ||
	    !packet_address_equal(
		    remote_address,
		    snapshot.address,
		    snapshot.family)) {
		if (counters)
			counters->proxy_state_misses++;
		return;
	}

	prior_capture_bytes = __sync_fetch_and_add(
		&state->proxy_capture_bytes,
		metadata.payload_length);
	if (prior_capture_bytes >= HIDEOUT_PROXY_FLOW_CAPTURE_BYTES) {
		if (counters)
			counters->proxy_budget_exhausted++;
		return;
	}
	remaining =
		HIDEOUT_PROXY_FLOW_CAPTURE_BYTES - prior_capture_bytes;
	captured_length = metadata.payload_length;
	if (captured_length > HIDEOUT_PROXY_PAYLOAD_BYTES)
		captured_length = HIDEOUT_PROXY_PAYLOAD_BYTES;
	if ((__u64)captured_length > remaining)
		captured_length = remaining;
	if (!captured_length) {
		if (counters)
			counters->proxy_budget_exhausted++;
		return;
	}

	if (counters)
		counters->proxy_matched_packets++;
	event = bpf_ringbuf_reserve(
		&proxy_handshake_events,
		sizeof(*event),
		0);
	if (!event) {
		if (counters)
			counters->proxy_ringbuf_drops++;
		return;
	}
	if (counters)
		counters->proxy_reserved_chunks++;
	__builtin_memset(event, 0, sizeof(*event));
	event->direction = direction;
	event->cpu = bpf_get_smp_processor_id();
	event->pid = snapshot.pid;
	event->tid = snapshot.pid;
	event->execution_pid = snapshot.execution_pid;
	event->uid = snapshot.uid;
	event->gid = snapshot.gid;
	event->family = snapshot.family;
	event->proxy_port = snapshot.destination_port;
	event->wire_length = metadata.payload_length;
	event->captured_length = captured_length;
	event->tcp_sequence = metadata.tcp_sequence;
	event->cgroup_id = snapshot.cgroup_id;
	event->observer_sequence = next_observer_sequence();
	if (!event->observer_sequence) {
		if (counters)
			counters->proxy_capture_failures++;
		bpf_ringbuf_discard(event, 0);
		return;
	}
	event->exec_sequence = snapshot.exec_sequence;
	if (!snapshot.execution_pid || !snapshot.exec_sequence)
		event->flags |= HIDEOUT_PROXY_EXECUTION_UNAVAILABLE;
	event->monotonic_ns = bpf_ktime_get_ns();
	event->socket_cookie = cookie;
	__builtin_memcpy(
		event->address,
		snapshot.address,
		sizeof(event->address));
	if (captured_length < metadata.payload_length) {
		event->flags |= HIDEOUT_PROXY_TRUNCATED;
		if (counters)
			counters->proxy_truncated_chunks++;
	}
	if (load_bounded_packet_payload(
		    ctx,
		    metadata.payload_offset,
		    event->payload,
		    captured_length) < 0) {
		if (counters)
			counters->proxy_capture_failures++;
		bpf_ringbuf_discard(event, 0);
		return;
	}
	bpf_ringbuf_submit(event, 0);
}

static __always_inline int observe_socket_address(
	struct bpf_sock_addr *ctx,
	__u32 kind,
	__u32 family,
	__u32 forced_protocol)
{
	struct network_collector_counters *counters;
	struct hideout_network_event *event;
	struct network_socket_state state = {};
	struct fork_parent *execution;
	__u8 address[16] = {};
	__u64 pid_tgid;
	__u64 uid_gid;
	__u64 cookie;
	__u32 protocol;
	__u32 user_port;
	__u32 pid;

	if (!in_target_cgroup())
		return 1;
	protocol = forced_protocol ? forced_protocol : ctx->protocol;
	user_port = ctx->user_port;
	snapshot_address(address, ctx, family);
	cookie = bpf_get_socket_cookie(ctx);
	counters = current_counters();
	if (counters)
		counters->matched_events++;
	if (!supported_protocol(protocol) ||
	    (kind == HIDEOUT_NETWORK_SENDMSG &&
	     protocol != HIDEOUT_NETWORK_UDP) ||
	    (__u16)user_port == 0) {
		if (counters)
			counters->unsupported_events++;
		return 1;
	}

	event = bpf_ringbuf_reserve(
		&network_observation_events,
		sizeof(*event),
		0);
	if (!event) {
		if (counters)
			counters->ringbuf_drops++;
		return 1;
	}
	if (counters)
		counters->reserved_events++;
	__builtin_memset(event, 0, sizeof(*event));
	pid_tgid = bpf_get_current_pid_tgid();
	uid_gid = bpf_get_current_uid_gid();
	pid = pid_tgid >> 32;
	event->kind = kind;
	event->cpu = bpf_get_smp_processor_id();
	event->pid = pid;
	event->tid = (__u32)pid_tgid;
	event->uid = (__u32)uid_gid;
	event->gid = uid_gid >> 32;
	event->family = family;
	event->protocol = protocol;
	event->destination_port =
		(__u32)__builtin_bswap16((__u16)user_port);
	event->cgroup_id = bpf_get_current_cgroup_id();
	event->observer_sequence = next_observer_sequence();
	if (!event->observer_sequence) {
		event->flags |= HIDEOUT_NETWORK_STATE_UNAVAILABLE;
		note_state_drop();
	}
	event->monotonic_ns = bpf_ktime_get_ns();
	__builtin_memcpy(event->address, address, sizeof(event->address));

	execution = bpf_map_lookup_elem(&exec_sequences, &pid);
	if (execution && execution->pid && execution->exec_sequence) {
		event->execution_pid = execution->pid;
		event->exec_sequence = execution->exec_sequence;
	} else {
		event->flags |= HIDEOUT_NETWORK_EXECUTION_UNAVAILABLE;
		note_state_drop();
	}

	event->socket_cookie = cookie;
	if (!cookie) {
		event->flags |= HIDEOUT_NETWORK_COOKIE_UNAVAILABLE;
		note_state_drop();
	} else {
		state.kind = event->kind;
		state.pid = event->pid;
		state.execution_pid = event->execution_pid;
		state.uid = event->uid;
		state.gid = event->gid;
		state.family = event->family;
		state.protocol = event->protocol;
		state.destination_port = event->destination_port;
		state.observer_sequence = event->observer_sequence;
		state.exec_sequence = event->exec_sequence;
		state.cgroup_id = event->cgroup_id;
			__builtin_memcpy(
				state.address,
				event->address,
				sizeof(state.address));
			/*
			 * Socket cookies may be reused after close. A fresh connect
			 * authoritatively resets the completion tombstone and the
			 * zero-initialized per-flow capture budget.
			 */
			bpf_map_delete_elem(
				&proxy_completed_sockets,
				&cookie);
			if (bpf_map_update_elem(
			    &network_socket_states,
			    &cookie,
			    &state,
			    BPF_ANY) < 0) {
			event->flags |= HIDEOUT_NETWORK_STATE_UNAVAILABLE;
			note_state_drop();
		}
	}
	bpf_ringbuf_submit(event, 0);
	return 1;
}

SEC("cgroup/connect4")
int hideout_observe_connect4(struct bpf_sock_addr *ctx)
{
	return observe_socket_address(
		ctx,
		HIDEOUT_NETWORK_CONNECT,
		HIDEOUT_NETWORK_IPV4,
		0);
}

SEC("cgroup/connect6")
int hideout_observe_connect6(struct bpf_sock_addr *ctx)
{
	return observe_socket_address(
		ctx,
		HIDEOUT_NETWORK_CONNECT,
		HIDEOUT_NETWORK_IPV6,
		0);
}

SEC("cgroup/sendmsg4")
int hideout_observe_sendmsg4(struct bpf_sock_addr *ctx)
{
	return observe_socket_address(
		ctx,
		HIDEOUT_NETWORK_SENDMSG,
		HIDEOUT_NETWORK_IPV4,
		HIDEOUT_NETWORK_UDP);
}

SEC("cgroup/sendmsg6")
int hideout_observe_sendmsg6(struct bpf_sock_addr *ctx)
{
	return observe_socket_address(
		ctx,
		HIDEOUT_NETWORK_SENDMSG,
		HIDEOUT_NETWORK_IPV6,
		HIDEOUT_NETWORK_UDP);
}

SEC("cgroup_skb/egress")
int hideout_correlate_egress(struct __sk_buff *ctx)
{
	struct network_collector_counters *counters;
	struct network_socket_state *state;
	__u64 cookie;

	counters = current_counters();
	cookie = bpf_get_socket_cookie(ctx);
	if (!cookie) {
		if (counters)
			counters->correlation_misses++;
		return 1;
	}
	state = bpf_map_lookup_elem(&network_socket_states, &cookie);
	if (!state) {
		if (counters)
			counters->correlation_misses++;
		return 1;
	}
	if (ctx->ifindex && !state->ifindex)
		state->ifindex = ctx->ifindex;
	__sync_fetch_and_add(&state->egress_bytes, ctx->len);
	/* packets is the commit marker for the preceding evidence fields. */
	__sync_fetch_and_add(&state->egress_packets, 1);
	if (counters)
		counters->correlation_hits++;
	observe_dns_packet(
		ctx,
		state,
		cookie,
		HIDEOUT_DNS_EGRESS);
	observe_proxy_packet(
		ctx,
		state,
		cookie,
		HIDEOUT_DNS_EGRESS);
	return 1;
}

SEC("cgroup_skb/ingress")
int hideout_observe_ingress(struct __sk_buff *ctx)
{
	struct network_socket_state *state;
	__u64 cookie;

	cookie = bpf_get_socket_cookie(ctx);
	if (!cookie)
		return 1;
	state = bpf_map_lookup_elem(&network_socket_states, &cookie);
	if (!state)
		return 1;
	observe_dns_packet(
		ctx,
		state,
		cookie,
		HIDEOUT_DNS_INGRESS);
	observe_proxy_packet(
		ctx,
		state,
		cookie,
		HIDEOUT_DNS_INGRESS);
	return 1;
}

char hideout_network_bpf_license[] SEC("license") = "GPL";
