//go:build ignore

// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-only
//
// Package-owned CO-RE workload observation programs. Runtime code configures
// target_cgroup_id before loading and independently attaches/probes each hook;
// merely embedding this object never makes a subsystem Available.

#define SEC(name) __attribute__((section(name), used))
#define __always_inline inline __attribute__((always_inline))
#define __uint(name, value) int (*name)[value]
#define __type(name, value) value *name

typedef unsigned char __u8;
typedef unsigned short __u16;
typedef unsigned int __u32;
typedef int __s32;
typedef unsigned long long __u64;

enum {
	BPF_MAP_TYPE_HASH = 1,
	BPF_MAP_TYPE_ARRAY = 2,
	BPF_MAP_TYPE_PERCPU_ARRAY = 6,
	BPF_MAP_TYPE_RINGBUF = 27,
	BPF_ANY = 0,
};

enum hideout_process_kind {
	HIDEOUT_PROCESS_FORK = 1,
	HIDEOUT_PROCESS_EXEC = 2,
	HIDEOUT_PROCESS_EXIT = 3,
};

enum {
	HIDEOUT_MAX_ARGS = 4,
	HIDEOUT_ARG_BYTES = 64,
	HIDEOUT_PATH_BYTES = 128,
};

enum hideout_process_flags {
	HIDEOUT_EXECUTABLE_TRUNCATED = 1U << 0,
	HIDEOUT_ARGV_TRUNCATED = 1U << 1,
	HIDEOUT_ARGV_UNAVAILABLE = 1U << 2,
	HIDEOUT_EXECUTABLE_UNAVAILABLE = 1U << 3,
	HIDEOUT_STATE_UNAVAILABLE = 1U << 4,
	HIDEOUT_EXIT_UNAVAILABLE = 1U << 5,
};

struct trace_entry {
	__u16 type;
	__u8 flags;
	__u8 preempt_count;
	__s32 pid;
};

struct trace_event_raw_sys_enter {
	struct trace_entry ent;
	long id;
	unsigned long args[6];
};

struct trace_event_raw_sched_process_exec {
	struct trace_entry ent;
	__u32 __data_loc_filename;
	__s32 pid;
	__s32 old_pid;
};

struct trace_event_raw_sched_process_fork {
	struct trace_entry ent;
	char parent_comm[16];
	__s32 parent_pid;
	char child_comm[16];
	__s32 child_pid;
};

struct trace_event_raw_sched_process_template {
	struct trace_entry ent;
	char comm[16];
	__s32 pid;
	__s32 prio;
};

struct task_struct {
	__s32 pid;
	__s32 tgid;
	__s32 exit_code;
} __attribute__((preserve_access_index));

struct linux_binprm {
	const char *filename;
} __attribute__((preserve_access_index));

struct bpf_raw_tracepoint_args {
	__u64 args[0];
};

struct pending_exec {
	char executable[HIDEOUT_PATH_BYTES];
	__u32 argc;
	__u32 flags;
	char argv[HIDEOUT_MAX_ARGS][HIDEOUT_ARG_BYTES];
};

struct fork_parent {
	__u32 pid;
	__u64 exec_sequence;
};

struct collector_counters {
	__u64 matched_events;
	__u64 reserved_events;
	__u64 ringbuf_drops;
	__u64 state_drops;
};

struct hideout_process_event {
	__u32 kind;
	__u32 cpu;
	__u32 pid;
	__u32 tid;
	__u32 parent_pid;
	__u32 uid;
	__u32 gid;
	__u32 argc;
	__u32 exit_code;
	__u32 signal;
	__u32 flags;
	__u32 reserved;
	__u64 cgroup_id;
	__u64 observer_sequence;
	__u64 exec_sequence;
	__u64 parent_exec_sequence;
	__u64 monotonic_ns;
	char executable[HIDEOUT_PATH_BYTES];
	char argv[HIDEOUT_MAX_ARGS][HIDEOUT_ARG_BYTES];
};

struct {
	__uint(type, BPF_MAP_TYPE_RINGBUF);
	__uint(max_entries, 4 * 1024 * 1024);
} observation_events SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 65536);
	__type(key, __u32);
	__type(value, struct pending_exec);
} pending_execs SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, struct pending_exec);
} pending_exec_scratch SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 65536);
	__type(key, __u32);
	__type(value, struct fork_parent);
} fork_parents SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 65536);
	__type(key, __u32);
	__type(value, struct fork_parent);
} exec_sequences SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, __u64);
} next_exec_sequence SEC(".maps");

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
	__type(value, struct collector_counters);
} process_counters SEC(".maps");

const volatile __u64 target_cgroup_id = 0;

static void *(*bpf_map_lookup_elem)(void *map, const void *key) = (void *)1;
static long (*bpf_map_update_elem)(void *map, const void *key,
				  const void *value, __u64 flags) = (void *)2;
static long (*bpf_map_delete_elem)(void *map, const void *key) = (void *)3;
static __u64 (*bpf_ktime_get_ns)(void) = (void *)5;
static __u32 (*bpf_get_smp_processor_id)(void) = (void *)8;
static __u64 (*bpf_get_current_pid_tgid)(void) = (void *)14;
static __u64 (*bpf_get_current_uid_gid)(void) = (void *)15;
static void *(*bpf_get_current_task)(void) = (void *)35;
static __u64 (*bpf_get_current_cgroup_id)(void) = (void *)80;
static long (*bpf_probe_read_user)(void *dst, __u32 size,
				  const void *unsafe_ptr) = (void *)112;
static long (*bpf_probe_read_user_str)(void *dst, __u32 size,
				      const void *unsafe_ptr) = (void *)114;
static long (*bpf_probe_read_kernel)(void *dst, __u32 size,
				    const void *unsafe_ptr) = (void *)113;
static long (*bpf_probe_read_kernel_str)(void *dst, __u32 size,
					const void *unsafe_ptr) = (void *)115;
static void *(*bpf_ringbuf_reserve)(void *ringbuf, __u64 size,
				    __u64 flags) = (void *)131;
static void (*bpf_ringbuf_submit)(void *data, __u64 flags) = (void *)132;

static __always_inline int in_target_cgroup(void)
{
	__u64 current = bpf_get_current_cgroup_id();

	return target_cgroup_id != 0 && current == target_cgroup_id;
}

static __always_inline __u64 next_observer_sequence(void)
{
	__u32 zero = 0;
	__u64 *sequence = bpf_map_lookup_elem(&observer_sequences, &zero);

	if (!sequence)
		return 0;
	return __sync_fetch_and_add(sequence, 1) + 1;
}

static __always_inline __u64 next_execution_sequence(void)
{
	__u32 zero = 0;
	__u64 *sequence = bpf_map_lookup_elem(&next_exec_sequence, &zero);

	if (!sequence)
		return 0;
	return __sync_fetch_and_add(sequence, 1) + 1;
}

static __always_inline void note_state_drop(void)
{
	struct collector_counters *counters;
	__u32 zero = 0;

	counters = bpf_map_lookup_elem(&process_counters, &zero);
	if (counters)
		counters->state_drops++;
}

static __always_inline void mark_state_unavailable(
	struct hideout_process_event *event)
{
	if (event)
		event->flags |= HIDEOUT_STATE_UNAVAILABLE;
	note_state_drop();
}

static __always_inline struct hideout_process_event *
reserve_process_event(__u32 kind)
{
	struct collector_counters *counters;
	struct hideout_process_event *event;
	__u64 pid_tgid;
	__u64 uid_gid;
	__u32 zero = 0;

	if (!in_target_cgroup())
		return 0;
	counters = bpf_map_lookup_elem(&process_counters, &zero);
	if (counters)
		counters->matched_events++;
	event = bpf_ringbuf_reserve(&observation_events, sizeof(*event), 0);
	if (!event) {
		if (counters)
			counters->ringbuf_drops++;
		return 0;
	}
	if (counters)
		counters->reserved_events++;
	__builtin_memset(event, 0, sizeof(*event));
	pid_tgid = bpf_get_current_pid_tgid();
	uid_gid = bpf_get_current_uid_gid();
	event->kind = kind;
	event->cpu = bpf_get_smp_processor_id();
	event->pid = pid_tgid >> 32;
	event->tid = (__u32)pid_tgid;
	event->uid = (__u32)uid_gid;
	event->gid = uid_gid >> 32;
	event->cgroup_id = bpf_get_current_cgroup_id();
	event->observer_sequence = next_observer_sequence();
	if (!event->observer_sequence)
		mark_state_unavailable(event);
	event->monotonic_ns = bpf_ktime_get_ns();
	return event;
}

static __always_inline int capture_exec_argv(
	const void *filename, const void *const *argv)
{
	struct pending_exec *pending;
	__u64 pid_tgid;
	__u32 zero = 0;
	__u32 pid;
	long read_result;
	int i;

	if (!in_target_cgroup())
		return 0;
	pending = bpf_map_lookup_elem(&pending_exec_scratch, &zero);
	if (!pending) {
		note_state_drop();
		return 0;
	}
	/*
	 * The scratch slot is reused per CPU. Clear every byte so copying the
	 * fixed-width value into the ring can never disclose a prior process's
	 * argv tail after the first NUL.
	 */
	__builtin_memset(pending, 0, sizeof(*pending));
	pid_tgid = bpf_get_current_pid_tgid();
	pid = pid_tgid >> 32;
	read_result = bpf_probe_read_user_str(
		pending->executable, sizeof(pending->executable),
		filename);
	if (read_result < 0)
		pending->flags |= HIDEOUT_EXECUTABLE_UNAVAILABLE;
	else if (read_result == sizeof(pending->executable))
		pending->flags |= HIDEOUT_EXECUTABLE_TRUNCATED;
	if (!argv)
		goto store;
#pragma unroll
	for (i = 0; i < HIDEOUT_MAX_ARGS; i++) {
		const void *argument = 0;

		if (bpf_probe_read_user(&argument, sizeof(argument), &argv[i]) < 0) {
			pending->flags |= HIDEOUT_ARGV_UNAVAILABLE;
			break;
		}
		if (!argument)
			break;
		read_result = bpf_probe_read_user_str(
			pending->argv[i], sizeof(pending->argv[i]), argument);
		if (read_result < 0) {
			pending->flags |= HIDEOUT_ARGV_UNAVAILABLE;
			break;
		}
		if (read_result == sizeof(pending->argv[i]))
			pending->flags |= HIDEOUT_ARGV_TRUNCATED;
		pending->argc++;
	}
	if (pending->argc == HIDEOUT_MAX_ARGS) {
		const void *argument = 0;

		if (bpf_probe_read_user(&argument, sizeof(argument),
					&argv[HIDEOUT_MAX_ARGS]) < 0)
			pending->flags |= HIDEOUT_ARGV_UNAVAILABLE;
		else if (argument)
			pending->flags |= HIDEOUT_ARGV_TRUNCATED;
	}
store:
	if (bpf_map_update_elem(&pending_execs, &pid, pending, BPF_ANY) < 0)
		note_state_drop();
	return 0;
}

SEC("tracepoint/syscalls/sys_enter_execve")
int hideout_capture_exec_argv(struct trace_event_raw_sys_enter *ctx)
{
	return capture_exec_argv(
		(const void *)ctx->args[0],
		(const void *const *)ctx->args[1]);
}

SEC("tracepoint/syscalls/sys_enter_execveat")
int hideout_capture_execveat_argv(struct trace_event_raw_sys_enter *ctx)
{
	return capture_exec_argv(
		(const void *)ctx->args[1],
		(const void *const *)ctx->args[2]);
}

SEC("raw_tracepoint/sched_process_fork")
int hideout_observe_process_fork(
	struct bpf_raw_tracepoint_args *ctx)
{
	struct hideout_process_event *event;
	struct fork_parent current = {};
	struct fork_parent *parent;
	struct task_struct *parent_task;
	struct task_struct *child_task;
	__u32 parent_pid;
	__u32 child_pid;

	if (!in_target_cgroup())
		return 0;
	parent_task = (struct task_struct *)ctx->args[0];
	child_task = (struct task_struct *)ctx->args[1];
	if (!parent_task || !child_task ||
	    bpf_probe_read_kernel(
		    &parent_pid, sizeof(parent_pid),
		    __builtin_preserve_access_index(&parent_task->pid)) < 0 ||
	    bpf_probe_read_kernel(
		    &child_pid, sizeof(child_pid),
		    __builtin_preserve_access_index(&child_task->pid)) < 0) {
		note_state_drop();
		return 0;
	}
	event = reserve_process_event(HIDEOUT_PROCESS_FORK);
	parent = bpf_map_lookup_elem(&exec_sequences, &parent_pid);
	if (parent)
		current = *parent;
	else {
		current.pid = parent_pid;
		mark_state_unavailable(event);
	}
	/*
	 * exec_sequences is really the current execution reference. Copying the
	 * owning PID as well as its sequence preserves ancestry when a process
	 * forks multiple generations before any descendant execs.
	 */
	if (bpf_map_update_elem(
		    &fork_parents, &child_pid, &current, BPF_ANY) < 0) {
		mark_state_unavailable(event);
	}
	if (bpf_map_update_elem(
		    &exec_sequences, &child_pid, &current, BPF_ANY) < 0) {
		mark_state_unavailable(event);
	}
	/*
	 * State updates deliberately happen even when the ring is full. Losing
	 * one record must not corrupt later ancestry or leave stale PID state.
	 */
	if (!event)
		return 0;
	event->pid = child_pid;
	event->tid = child_pid;
	event->parent_pid = current.pid;
	event->parent_exec_sequence = current.exec_sequence;
	bpf_ringbuf_submit(event, 0);
	return 0;
}

SEC("raw_tracepoint/sched_process_exec")
int hideout_observe_process_exec(
	struct bpf_raw_tracepoint_args *ctx)
{
	struct hideout_process_event *event;
	struct pending_exec *pending;
	struct fork_parent *parent;
	struct fork_parent parent_copy = {};
	struct fork_parent current = {};
	struct linux_binprm *binary;
	const char *filename = 0;
	__u64 exec_sequence;
	__u64 pid_tgid;
	__u32 pid;
	int has_parent = 0;
	int has_pending = 0;
	long read_result;
	int i;

	if (!in_target_cgroup())
		return 0;
	event = reserve_process_event(HIDEOUT_PROCESS_EXEC);
	pid_tgid = bpf_get_current_pid_tgid();
	pid = pid_tgid >> 32;
	exec_sequence = next_execution_sequence();
	if (!exec_sequence)
		mark_state_unavailable(event);
	current.pid = pid;
	current.exec_sequence = exec_sequence;
	if (bpf_map_update_elem(
		    &exec_sequences, &pid, &current, BPF_ANY) < 0) {
		mark_state_unavailable(event);
	}
	parent = bpf_map_lookup_elem(&fork_parents, &pid);
	if (parent) {
		parent_copy = *parent;
		has_parent = 1;
		if (!parent_copy.exec_sequence)
			mark_state_unavailable(event);
		if (bpf_map_delete_elem(&fork_parents, &pid) < 0)
			mark_state_unavailable(event);
	}
	pending = bpf_map_lookup_elem(&pending_execs, &pid);
	if (pending) {
		has_pending = 1;
		if (event) {
			event->argc = pending->argc;
			event->flags |= pending->flags;
			__builtin_memcpy(event->executable,
					 pending->executable,
					 sizeof(event->executable));
#pragma unroll
			for (i = 0; i < HIDEOUT_MAX_ARGS; i++)
				__builtin_memcpy(
					event->argv[i], pending->argv[i],
					sizeof(event->argv[i]));
		}
		if (bpf_map_delete_elem(&pending_execs, &pid) < 0)
			mark_state_unavailable(event);
	}
	if (!event)
		return 0;
	event->exec_sequence = exec_sequence;
	if (has_parent) {
		event->parent_pid = parent_copy.pid;
		event->parent_exec_sequence = parent_copy.exec_sequence;
	}
	if (!has_pending || event->executable[0] == 0) {
		binary = (struct linux_binprm *)ctx->args[2];
		if (!binary ||
		    bpf_probe_read_kernel(
			    &filename, sizeof(filename),
			    __builtin_preserve_access_index(
				    &binary->filename)) < 0 ||
		    !filename) {
			read_result = -1;
		} else {
			read_result = bpf_probe_read_kernel_str(
				event->executable, sizeof(event->executable),
				filename);
		}
		event->flags &= ~HIDEOUT_EXECUTABLE_UNAVAILABLE;
		if (read_result < 0)
			event->flags |= HIDEOUT_EXECUTABLE_UNAVAILABLE;
		else if (read_result == sizeof(event->executable))
			event->flags |= HIDEOUT_EXECUTABLE_TRUNCATED;
	}
	if (!has_pending)
		event->flags |= HIDEOUT_ARGV_UNAVAILABLE;
	bpf_ringbuf_submit(event, 0);
	return 0;
}

SEC("raw_tracepoint/sched_process_exit")
int hideout_observe_process_exit(
	struct bpf_raw_tracepoint_args *ctx)
{
	struct hideout_process_event *event;
	struct task_struct *task;
	struct fork_parent *current;
	struct pending_exec *pending;
	struct fork_parent *parent;
	__u64 pid_tgid;
	__s32 status = 0;
	__u32 pid;

	(void)ctx;
	pid_tgid = bpf_get_current_pid_tgid();
	if ((__u32)pid_tgid != pid_tgid >> 32)
		return 0;
	if (!in_target_cgroup())
		return 0;
	event = reserve_process_event(HIDEOUT_PROCESS_EXIT);
	pid = pid_tgid >> 32;
	current = bpf_map_lookup_elem(&exec_sequences, &pid);
	if (current && current->pid == pid) {
		if (event)
			event->exec_sequence = current->exec_sequence;
		if (!current->exec_sequence)
			mark_state_unavailable(event);
	}
	else if (!current) {
		mark_state_unavailable(event);
	}
	if (event) {
		task = bpf_get_current_task();
		if (bpf_probe_read_kernel(
			    &status, sizeof(status),
			    __builtin_preserve_access_index(
				    &task->exit_code)) < 0) {
			event->flags |= HIDEOUT_EXIT_UNAVAILABLE;
			note_state_drop();
		} else {
			event->exit_code = (status >> 8) & 0xff;
			event->signal = status & 0x7f;
		}
	}
	if (current && bpf_map_delete_elem(&exec_sequences, &pid) < 0)
		mark_state_unavailable(event);
	pending = bpf_map_lookup_elem(&pending_execs, &pid);
	if (pending && bpf_map_delete_elem(&pending_execs, &pid) < 0)
		mark_state_unavailable(event);
	parent = bpf_map_lookup_elem(&fork_parents, &pid);
	if (parent && bpf_map_delete_elem(&fork_parents, &pid) < 0)
		mark_state_unavailable(event);
	if (!event)
		return 0;
	bpf_ringbuf_submit(event, 0);
	return 0;
}

char hideout_bpf_license[] SEC("license") = "GPL";
