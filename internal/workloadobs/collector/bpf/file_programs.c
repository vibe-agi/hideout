//go:build ignore

// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-only
//
// Package-owned file observation programs. The loader replaces
// exec_sequences and observer_sequences with the process collector's maps so
// file evidence uses the same execution identities and per-CPU sequence space.

#define SEC(name) __attribute__((section(name), used))
#define __always_inline inline __attribute__((always_inline))
#define __uint(name, value) int (*name)[value]
#define __type(name, value) value *name

typedef unsigned char __u8;
typedef unsigned short __u16;
typedef unsigned int __u32;
typedef int __s32;
typedef unsigned long long __u64;
typedef long long __s64;

enum {
	BPF_MAP_TYPE_HASH = 1,
	BPF_MAP_TYPE_PERCPU_ARRAY = 6,
	BPF_MAP_TYPE_RINGBUF = 27,
	BPF_ANY = 0,
};

enum hideout_file_kind {
	HIDEOUT_FILE_OPEN = 1,
	HIDEOUT_FILE_READ = 2,
	HIDEOUT_FILE_WRITE = 3,
	HIDEOUT_FILE_MMAP = 4,
	HIDEOUT_FILE_CREATE = 5,
	HIDEOUT_FILE_TRUNCATE = 6,
	HIDEOUT_FILE_RENAME = 7,
	HIDEOUT_FILE_UNLINK = 8,
	HIDEOUT_FILE_METADATA = 9,
	HIDEOUT_FILE_HARDLINK = 10,
	HIDEOUT_FILE_SYMLINK = 11,
	HIDEOUT_FILE_MKDIR = 12,
	HIDEOUT_FILE_RMDIR = 13,
};

enum {
	HIDEOUT_FILE_PATH_BYTES = 512,
	HIDEOUT_FILE_NAME_BYTES = 128,
	HIDEOUT_FILE_COMPACT_RECORD_BYTES = 120,
	HIDEOUT_FILE_CACHED_RECORD_BYTES =
		HIDEOUT_FILE_COMPACT_RECORD_BYTES + HIDEOUT_FILE_PATH_BYTES,
};

enum hideout_file_flags {
	HIDEOUT_FILE_PATH_TRUNCATED = 1U << 0,
	HIDEOUT_FILE_PATH_UNAVAILABLE = 1U << 1,
	HIDEOUT_FILE_PATH_ALIASED = 1U << 2,
	HIDEOUT_FILE_TARGET_TRUNCATED = 1U << 3,
	HIDEOUT_FILE_TARGET_UNAVAILABLE = 1U << 4,
	HIDEOUT_FILE_IDENTITY_UNAVAILABLE = 1U << 5,
	HIDEOUT_FILE_BYTES_UNAVAILABLE = 1U << 6,
	HIDEOUT_FILE_STATE_UNAVAILABLE = 1U << 7,
	HIDEOUT_FILE_OUTCOME_UNKNOWN = 1U << 8,
	HIDEOUT_FILE_AUTHORIZATION_HOOK = 1U << 9,
};

enum hideout_file_type {
	HIDEOUT_FILE_TYPE_UNKNOWN = 0,
	HIDEOUT_FILE_TYPE_REGULAR = 1,
	HIDEOUT_FILE_TYPE_DIRECTORY = 2,
	HIDEOUT_FILE_TYPE_SYMLINK = 3,
	HIDEOUT_FILE_TYPE_SOCKET = 4,
	HIDEOUT_FILE_TYPE_FIFO = 5,
	HIDEOUT_FILE_TYPE_DEVICE = 6,
};

enum {
	HIDEOUT_S_IFMT = 00170000,
	HIDEOUT_S_IFIFO = 0010000,
	HIDEOUT_S_IFCHR = 0020000,
	HIDEOUT_S_IFDIR = 0040000,
	HIDEOUT_S_IFBLK = 0060000,
	HIDEOUT_S_IFREG = 0100000,
	HIDEOUT_S_IFLNK = 0120000,
	HIDEOUT_S_IFSOCK = 0140000,
	HIDEOUT_FMODE_CREATED = 1U << 20,
	HIDEOUT_ENAMETOOLONG = 36,
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

struct trace_event_raw_sys_exit {
	struct trace_entry ent;
	long id;
	long ret;
};

struct trace_event_raw_sched_process_template {
	struct trace_entry ent;
	char comm[16];
	__s32 pid;
	__s32 prio;
};

struct vfsmount;
struct super_block {
	__u32 s_dev;
} __attribute__((preserve_access_index));

struct inode {
	__u16 i_mode;
	struct super_block *i_sb;
	unsigned long i_ino;
} __attribute__((preserve_access_index));

struct qstr {
	const unsigned char *name;
} __attribute__((preserve_access_index));

struct dentry {
	struct inode *d_inode;
	struct qstr d_name;
} __attribute__((preserve_access_index));

struct path {
	struct vfsmount *mnt;
	struct dentry *dentry;
} __attribute__((preserve_access_index));

struct file {
	struct inode *f_inode;
	unsigned int f_flags;
	unsigned int f_mode;
	struct path f_path;
} __attribute__((preserve_access_index));

struct execution_ref {
	__u32 pid;
	__u64 exec_sequence;
};

struct file_metadata {
	__u64 device;
	__u64 inode;
	__u32 file_type;
	__u32 flags;
	__u32 created;
	__u32 announced;
	char path[HIDEOUT_FILE_PATH_BYTES];
};

struct file_collector_counters {
	__u64 matched_events;
	__u64 reserved_events;
	__u64 ringbuf_drops;
	__u64 state_drops;
	__u64 path_failures;
	__u64 identity_failures;
};

struct hideout_file_event {
	__u32 kind;
	__u32 cpu;
	__u32 pid;
	__u32 tid;
	__u32 execution_pid;
	__u32 uid;
	__u32 gid;
	__u32 flags;
	__u32 file_type;
	__u32 reserved;
	__s64 result;
	__u64 cgroup_id;
	__u64 observer_sequence;
	__u64 exec_sequence;
	__u64 monotonic_ns;
	__u64 bytes;
	__u64 device;
	__u64 inode;
	__u64 mount_id;
	__u64 file_key;
	char path[HIDEOUT_FILE_PATH_BYTES];
	char path_name[HIDEOUT_FILE_NAME_BYTES];
	char target_path[HIDEOUT_FILE_PATH_BYTES];
	char target_name[HIDEOUT_FILE_NAME_BYTES];
};

/*
 * Both maps are replaced by the process collector's live maps before load.
 * Their definitions must remain byte-for-byte map-compatible with programs.c.
 */
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 65536);
	__type(key, __u32);
	__type(value, struct execution_ref);
} exec_sequences SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, __u64);
} observer_sequences SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_RINGBUF);
	__uint(max_entries, 8 * 1024 * 1024);
} file_observation_events SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 65536);
	__type(key, __u64);
	__type(value, struct file_metadata);
} observed_files SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, struct file_metadata);
} file_metadata_scratch SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 65536);
	__type(key, __u64);
	__type(value, __u64);
} mmap_lengths SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, struct file_collector_counters);
} file_counters SEC(".maps");

const volatile __u64 file_target_cgroup_id = 0;

static void *(*bpf_map_lookup_elem)(void *map, const void *key) = (void *)1;
static long (*bpf_map_update_elem)(void *map, const void *key,
				  const void *value, __u64 flags) = (void *)2;
static long (*bpf_map_delete_elem)(void *map, const void *key) = (void *)3;
static __u64 (*bpf_ktime_get_ns)(void) = (void *)5;
static __u32 (*bpf_get_smp_processor_id)(void) = (void *)8;
static __u64 (*bpf_get_current_pid_tgid)(void) = (void *)14;
static __u64 (*bpf_get_current_uid_gid)(void) = (void *)15;
static __u64 (*bpf_get_current_cgroup_id)(void) = (void *)80;
static long (*bpf_probe_read_kernel)(void *dst, __u32 size,
				    const void *unsafe_ptr) = (void *)113;
static long (*bpf_probe_read_kernel_str)(void *dst, __u32 size,
					const void *unsafe_ptr) = (void *)115;
static void *(*bpf_ringbuf_reserve)(void *ringbuf, __u64 size,
				    __u64 flags) = (void *)131;
static void (*bpf_ringbuf_submit)(void *data, __u64 flags) = (void *)132;
static long (*bpf_d_path)(struct path *path, char *buf, __u32 size) =
	(void *)147;

static __always_inline int in_target_cgroup(void)
{
	__u64 current = bpf_get_current_cgroup_id();

	return file_target_cgroup_id != 0 &&
	       current == file_target_cgroup_id;
}

static __always_inline struct file_collector_counters *counters(void)
{
	__u32 zero = 0;

	return bpf_map_lookup_elem(&file_counters, &zero);
}

static __always_inline void note_state_drop(void)
{
	struct file_collector_counters *value = counters();

	if (value)
		value->state_drops++;
}

static __always_inline void note_path_failure(void)
{
	struct file_collector_counters *value = counters();

	if (value)
		value->path_failures++;
}

static __always_inline void note_identity_failure(void)
{
	struct file_collector_counters *value = counters();

	if (value)
		value->identity_failures++;
}

static __always_inline __u64 next_observer_sequence(void)
{
	__u32 zero = 0;
	__u64 *sequence = bpf_map_lookup_elem(&observer_sequences, &zero);

	if (!sequence)
		return 0;
	return __sync_fetch_and_add(sequence, 1) + 1;
}

static __always_inline __u32 file_type_from_mode(__u16 mode)
{
	switch (mode & HIDEOUT_S_IFMT) {
	case HIDEOUT_S_IFREG:
		return HIDEOUT_FILE_TYPE_REGULAR;
	case HIDEOUT_S_IFDIR:
		return HIDEOUT_FILE_TYPE_DIRECTORY;
	case HIDEOUT_S_IFLNK:
		return HIDEOUT_FILE_TYPE_SYMLINK;
	case HIDEOUT_S_IFSOCK:
		return HIDEOUT_FILE_TYPE_SOCKET;
	case HIDEOUT_S_IFIFO:
		return HIDEOUT_FILE_TYPE_FIFO;
	case HIDEOUT_S_IFCHR:
	case HIDEOUT_S_IFBLK:
		return HIDEOUT_FILE_TYPE_DEVICE;
	default:
		return HIDEOUT_FILE_TYPE_UNKNOWN;
	}
}

static __always_inline void clear_file_event_header(
	struct hideout_file_event *event)
{
	event->kind = 0;
	event->cpu = 0;
	event->pid = 0;
	event->tid = 0;
	event->execution_pid = 0;
	event->uid = 0;
	event->gid = 0;
	event->flags = 0;
	event->file_type = 0;
	event->reserved = 0;
	event->result = 0;
	event->cgroup_id = 0;
	event->observer_sequence = 0;
	event->exec_sequence = 0;
	event->monotonic_ns = 0;
	event->bytes = 0;
	event->device = 0;
	event->inode = 0;
	event->mount_id = 0;
	event->file_key = 0;
}

static __always_inline void clear_file_event(
	struct hideout_file_event *event)
{
	int index;

	clear_file_event_header(event);
#pragma unroll
	for (index = 0; index < HIDEOUT_FILE_PATH_BYTES / 8; index++) {
		((__u64 *)event->path)[index] = 0;
		((__u64 *)event->target_path)[index] = 0;
	}
#pragma unroll
	for (index = 0; index < HIDEOUT_FILE_NAME_BYTES / 8; index++) {
		((__u64 *)event->path_name)[index] = 0;
		((__u64 *)event->target_name)[index] = 0;
	}
}

static __always_inline void clear_cached_file_event_path(
	struct hideout_file_event *event)
{
	int index;

#pragma unroll
	for (index = 0; index < HIDEOUT_FILE_PATH_BYTES / 8; index++)
		((__u64 *)event->path)[index] = 0;
}

static __always_inline void clear_file_metadata(
	struct file_metadata *metadata)
{
	int index;

	metadata->device = 0;
	metadata->inode = 0;
	metadata->file_type = 0;
	metadata->flags = 0;
	metadata->created = 0;
	metadata->announced = 0;
#pragma unroll
	for (index = 0; index < HIDEOUT_FILE_PATH_BYTES / 8; index++)
		((__u64 *)metadata->path)[index] = 0;
}

static __always_inline void fill_inode_identity(
	struct inode *inode, __u64 *device, __u64 *inode_number,
	__u32 *file_type, __u32 *flags)
{
	struct super_block *superblock = 0;
	unsigned long number = 0;
	__u32 dev = 0;
	__u16 mode = 0;

	if (!inode ||
	    bpf_probe_read_kernel(&superblock, sizeof(superblock),
				  __builtin_preserve_access_index(
					  &inode->i_sb)) < 0 ||
	    !superblock ||
	    bpf_probe_read_kernel(&number, sizeof(number),
				  __builtin_preserve_access_index(
					  &inode->i_ino)) < 0 ||
	    bpf_probe_read_kernel(&mode, sizeof(mode),
				  __builtin_preserve_access_index(
					  &inode->i_mode)) < 0 ||
	    bpf_probe_read_kernel(&dev, sizeof(dev),
				  __builtin_preserve_access_index(
					  &superblock->s_dev)) < 0 ||
	    number == 0) {
		*flags |= HIDEOUT_FILE_IDENTITY_UNAVAILABLE;
		note_identity_failure();
		return;
	}
	*device = dev;
	*inode_number = number;
	*file_type = file_type_from_mode(mode);
}

static __always_inline void fill_file_identity(
	struct file *file, __u64 *device, __u64 *inode_number,
	__u32 *file_type, __u32 *flags)
{
	struct inode *inode = 0;

	if (!file ||
	    bpf_probe_read_kernel(&inode, sizeof(inode),
				  __builtin_preserve_access_index(
					  &file->f_inode)) < 0) {
		*flags |= HIDEOUT_FILE_IDENTITY_UNAVAILABLE;
		note_identity_failure();
		return;
	}
	fill_inode_identity(inode, device, inode_number, file_type, flags);
}

static __always_inline void fill_dentry_identity(
	struct dentry *dentry, __u64 *device, __u64 *inode_number,
	__u32 *file_type, __u32 *flags)
{
	struct inode *inode = 0;

	if (!dentry ||
	    bpf_probe_read_kernel(&inode, sizeof(inode),
				  __builtin_preserve_access_index(
					  &dentry->d_inode)) < 0) {
		*flags |= HIDEOUT_FILE_IDENTITY_UNAVAILABLE;
		note_identity_failure();
		return;
	}
	fill_inode_identity(inode, device, inode_number, file_type, flags);
}

static __always_inline void fill_path(
	struct path *path, char *buffer, __u32 size,
	__u32 unavailable_flag, __u32 truncated_flag, __u32 *flags)
{
	long result;

	if (!path) {
		*flags |= unavailable_flag;
		note_path_failure();
		return;
	}
	result = bpf_d_path(path, buffer, size);
	if (result < 0) {
		if (result == -HIDEOUT_ENAMETOOLONG)
			*flags |= truncated_flag;
		else
			*flags |= unavailable_flag;
		note_path_failure();
	} else if (result == size) {
		*flags |= truncated_flag;
	}
	/*
	 * bpf_d_path may leave its internal end-of-buffer copy after the first
	 * NUL. Every destination is zeroed before this helper runs, and the
	 * userspace decoder deterministically clears bytes from the first NUL
	 * before caching or exposing the record. Keeping that linear scrub out of
	 * the synchronous BPF open hook avoids hundreds of instructions per open
	 * without changing the decoded path.
	 */
}

static __always_inline void fill_dentry_name(
	struct dentry *dentry, char *buffer, __u32 size,
	__u32 unavailable_flag, __u32 truncated_flag, __u32 *flags)
{
	const unsigned char *name = 0;
	long result;

	if (!dentry ||
	    bpf_probe_read_kernel(&name, sizeof(name),
				  __builtin_preserve_access_index(
					  &dentry->d_name.name)) < 0 ||
	    !name) {
		*flags |= unavailable_flag;
		note_path_failure();
		return;
	}
	result = bpf_probe_read_kernel_str(buffer, size, name);
	if (result < 0) {
		*flags |= unavailable_flag;
		note_path_failure();
	} else if (result == size) {
		*flags |= truncated_flag;
	}
}

/*
 * bpf_d_path is accepted only from a narrow verifier allowlist of tracing
 * targets. The security_path_* wrappers are deliberately used even when the
 * BPF LSM is not active, so reconstruct the best authoritative alias that is
 * available there: the final dentry name for the supplied path. Callers mark
 * the whole event aliased; userspace must never present this relative value as
 * a canonical path.
 */
static __always_inline void fill_path_alias(
	struct path *path, char *buffer, __u32 size,
	__u32 unavailable_flag, __u32 truncated_flag, __u32 *flags)
{
	struct dentry *dentry = 0;

	if (!path ||
	    bpf_probe_read_kernel(
		    &dentry, sizeof(dentry),
		    __builtin_preserve_access_index(&path->dentry)) < 0 ||
	    !dentry) {
		*flags |= unavailable_flag;
		note_path_failure();
		return;
	}
	fill_dentry_name(
		dentry, buffer, size, unavailable_flag, truncated_flag, flags);
}

static __always_inline void populate_file_event_header(
	struct hideout_file_event *event, __u32 kind)
{
	struct execution_ref *execution;
	__u64 pid_tgid;
	__u64 uid_gid;
	__u32 pid;

	pid_tgid = bpf_get_current_pid_tgid();
	uid_gid = bpf_get_current_uid_gid();
	pid = pid_tgid >> 32;
	event->kind = kind;
	event->cpu = bpf_get_smp_processor_id();
	event->pid = pid;
	event->tid = (__u32)pid_tgid;
	event->uid = (__u32)uid_gid;
	event->gid = uid_gid >> 32;
	event->cgroup_id = bpf_get_current_cgroup_id();
	event->observer_sequence = next_observer_sequence();
	if (!event->observer_sequence) {
		event->flags |= HIDEOUT_FILE_STATE_UNAVAILABLE;
		note_state_drop();
	}
	event->monotonic_ns = bpf_ktime_get_ns();
	execution = bpf_map_lookup_elem(&exec_sequences, &pid);
	if (!execution || !execution->pid || !execution->exec_sequence) {
		event->flags |= HIDEOUT_FILE_STATE_UNAVAILABLE;
		note_state_drop();
	} else {
		event->execution_pid = execution->pid;
		event->exec_sequence = execution->exec_sequence;
	}
}

static __always_inline struct hideout_file_event *
reserve_file_event(__u32 kind)
{
	struct file_collector_counters *counter;
	struct hideout_file_event *event;
	__u32 zero = 0;

	if (!in_target_cgroup())
		return 0;
	counter = bpf_map_lookup_elem(&file_counters, &zero);
	if (counter)
		counter->matched_events++;
	event = bpf_ringbuf_reserve(
		&file_observation_events, sizeof(*event), 0);
	if (!event) {
		if (counter)
			counter->ringbuf_drops++;
		return 0;
	}
	if (counter)
		counter->reserved_events++;
	clear_file_event(event);
	populate_file_event_header(event, kind);
	return event;
}

static __always_inline struct hideout_file_event *
reserve_compact_file_event(__u32 kind)
{
	struct file_collector_counters *counter;
	struct hideout_file_event *event;
	__u32 zero = 0;

	if (!in_target_cgroup())
		return 0;
	counter = bpf_map_lookup_elem(&file_counters, &zero);
	if (counter)
		counter->matched_events++;
	event = bpf_ringbuf_reserve(
		&file_observation_events,
		HIDEOUT_FILE_COMPACT_RECORD_BYTES, 0);
	if (!event) {
		if (counter)
			counter->ringbuf_drops++;
		return 0;
	}
	if (counter)
		counter->reserved_events++;
	clear_file_event_header(event);
	populate_file_event_header(event, kind);
	return event;
}

/*
 * Cached file events carry only the common header and canonical path. Open,
 * mmap, descriptor truncate, and the first I/O event never use path_name or a
 * target, so reserving and clearing the full mutation record on every open
 * needlessly amplifies a file-heavy workload.
 */
static __always_inline struct hideout_file_event *
reserve_cached_file_event(__u32 kind)
{
	struct file_collector_counters *counter;
	struct hideout_file_event *event;
	__u32 zero = 0;

	if (!in_target_cgroup())
		return 0;
	counter = bpf_map_lookup_elem(&file_counters, &zero);
	if (counter)
		counter->matched_events++;
	event = bpf_ringbuf_reserve(
		&file_observation_events,
		HIDEOUT_FILE_CACHED_RECORD_BYTES, 0);
	if (!event) {
		if (counter)
			counter->ringbuf_drops++;
		return 0;
	}
	if (counter)
		counter->reserved_events++;
	/*
	 * Do not zero the path here: successful callers overwrite every byte
	 * from initialized metadata. Each metadata-miss branch must clear it
	 * before submission so no reserved ring-buffer byte is exposed.
	 */
	clear_file_event_header(event);
	populate_file_event_header(event, kind);
	return event;
}

static __always_inline int fill_metadata_from_file(
	struct file *file, struct file_metadata *metadata)
{
	struct path *path;
	__u32 mode = 0;

	if (!file || !metadata)
		return -1;
	clear_file_metadata(metadata);
	fill_file_identity(
		file, &metadata->device, &metadata->inode,
		&metadata->file_type, &metadata->flags);
	path = __builtin_preserve_access_index(&file->f_path);
	fill_path(
		path, metadata->path, sizeof(metadata->path),
		HIDEOUT_FILE_PATH_UNAVAILABLE,
		HIDEOUT_FILE_PATH_TRUNCATED,
		&metadata->flags);
	if (bpf_probe_read_kernel(
		    &mode, sizeof(mode),
		    __builtin_preserve_access_index(&file->f_mode)) < 0) {
		metadata->flags |= HIDEOUT_FILE_STATE_UNAVAILABLE;
		note_state_drop();
	} else if (mode & HIDEOUT_FMODE_CREATED) {
		metadata->created = 1;
	}
	return 0;
}

static __always_inline int cache_file(struct file *file)
{
	struct file_metadata *scratch;
	__u64 key = (__u64)file;
	__u32 zero = 0;

	if (!file)
		return -1;
	scratch = bpf_map_lookup_elem(&file_metadata_scratch, &zero);
	if (!scratch) {
		note_state_drop();
		return -1;
	}
	if (fill_metadata_from_file(file, scratch) < 0 ||
	    bpf_map_update_elem(&observed_files, &key, scratch, BPF_ANY) < 0) {
		note_state_drop();
		return -1;
	}
	return 0;
}

/*
 * Descriptions inherited from the supervisor predate the target cgroup, so
 * security_file_open cannot seed their paths. Capture their stable identity
 * once without attaching security_file_permission to every read/write. The
 * resulting path-unavailable limitation is explicit and subsequent I/O can
 * use the compact event path.
 */
static __always_inline int cache_file_identity_only(struct file *file)
{
	struct file_metadata *scratch;
	__u64 key = (__u64)file;
	__u32 zero = 0;

	if (!file)
		return -1;
	scratch = bpf_map_lookup_elem(&file_metadata_scratch, &zero);
	if (!scratch) {
		note_state_drop();
		return -1;
	}
	clear_file_metadata(scratch);
	fill_file_identity(
		file, &scratch->device, &scratch->inode,
		&scratch->file_type, &scratch->flags);
	scratch->flags |= HIDEOUT_FILE_PATH_UNAVAILABLE;
	if (bpf_map_update_elem(
		    &observed_files, &key, scratch, BPF_ANY) < 0) {
		note_state_drop();
		return -1;
	}
	return 0;
}

static __always_inline void copy_metadata_identity(
	struct hideout_file_event *event,
	const struct file_metadata *metadata)
{
	event->device = metadata->device;
	event->inode = metadata->inode;
	event->file_type = metadata->file_type;
	event->flags |= metadata->flags;
}

static __always_inline void copy_metadata(
	struct hideout_file_event *event,
	const struct file_metadata *metadata)
{
	copy_metadata_identity(event, metadata);
	__builtin_memcpy(
		event->path, metadata->path, sizeof(event->path));
}

static __always_inline int emit_cached_file(
	__u32 kind, struct file *file, __s64 result,
	__u64 bytes, int outcome_unknown, int authorization_hook)
{
	struct hideout_file_event *event;
	struct file_metadata *metadata;
	__u64 key = (__u64)file;

	event = reserve_cached_file_event(kind);
	if (!event)
		return 0;
	event->file_key = key;
	metadata = bpf_map_lookup_elem(&observed_files, &key);
	if (metadata) {
		copy_metadata(event, metadata);
		metadata->announced = 1;
	} else {
		clear_cached_file_event_path(event);
		event->flags |= HIDEOUT_FILE_PATH_UNAVAILABLE |
				HIDEOUT_FILE_STATE_UNAVAILABLE;
		fill_file_identity(
			file, &event->device, &event->inode,
			&event->file_type, &event->flags);
		note_state_drop();
	}
	event->result = result;
	event->bytes = bytes;
	if (outcome_unknown && result == 0)
		event->flags |= HIDEOUT_FILE_OUTCOME_UNKNOWN;
	if (authorization_hook)
		event->flags |= HIDEOUT_FILE_AUTHORIZATION_HOOK;
	bpf_ringbuf_submit(event, 0);
	return 0;
}

static __always_inline int emit_compact_io(
	__u32 kind, struct file *file, __s64 result, __u64 bytes)
{
	struct hideout_file_event *event;
	struct file_metadata *metadata;
	__u64 key = (__u64)file;

	/*
	 * The tracing hooks are system-wide. Reject non-target I/O before touching
	 * observed_files so unrelated guest processes cannot populate or evict the
	 * target's bounded metadata cache.
	 */
	if (!file || !in_target_cgroup())
		return 0;
	metadata = bpf_map_lookup_elem(&observed_files, &key);
	if (!metadata) {
		cache_file_identity_only(file);
		metadata = bpf_map_lookup_elem(&observed_files, &key);
	}
	if (!metadata || !metadata->announced)
		return emit_cached_file(
			kind, file, result, bytes, 0, 0);
	event = reserve_compact_file_event(kind);
	if (!event)
		return 0;
	event->file_key = key;
	copy_metadata_identity(event, metadata);
	event->result = result;
	event->bytes = bytes;
	bpf_ringbuf_submit(event, 0);
	return 0;
}

static __always_inline int emit_path_event(
	__u32 kind, struct path *path, struct dentry *dentry,
	struct path *target_path, struct dentry *target_dentry,
	__s64 result, __u32 extra_flags, __u32 forced_file_type)
{
	struct hideout_file_event *event;

	event = reserve_file_event(kind);
	if (!event)
		return 0;
	event->flags |= extra_flags | HIDEOUT_FILE_PATH_ALIASED |
			HIDEOUT_FILE_AUTHORIZATION_HOOK;
	if (result == 0)
		event->flags |= HIDEOUT_FILE_OUTCOME_UNKNOWN;
	event->result = result;
	if (dentry) {
		fill_path_alias(
			path, event->path, sizeof(event->path),
			HIDEOUT_FILE_PATH_UNAVAILABLE,
			HIDEOUT_FILE_PATH_TRUNCATED,
			&event->flags);
		fill_dentry_name(
			dentry, event->path_name, sizeof(event->path_name),
			HIDEOUT_FILE_PATH_UNAVAILABLE,
			HIDEOUT_FILE_PATH_TRUNCATED,
			&event->flags);
		fill_dentry_identity(
			dentry, &event->device, &event->inode,
			&event->file_type, &event->flags);
	} else {
		fill_path_alias(
			path, event->path, sizeof(event->path),
			HIDEOUT_FILE_PATH_UNAVAILABLE,
			HIDEOUT_FILE_PATH_TRUNCATED,
			&event->flags);
		if (path) {
			struct dentry *path_dentry = 0;

			if (bpf_probe_read_kernel(
				    &path_dentry, sizeof(path_dentry),
				    __builtin_preserve_access_index(
					    &path->dentry)) < 0) {
				event->flags |=
					HIDEOUT_FILE_IDENTITY_UNAVAILABLE;
				note_identity_failure();
			} else {
				fill_dentry_identity(
					path_dentry, &event->device,
					&event->inode, &event->file_type,
					&event->flags);
			}
		}
	}
	if (target_path) {
		fill_path_alias(
			target_path, event->target_path,
			sizeof(event->target_path),
			HIDEOUT_FILE_TARGET_UNAVAILABLE,
			HIDEOUT_FILE_TARGET_TRUNCATED,
			&event->flags);
	}
	if (target_dentry) {
		fill_dentry_name(
			target_dentry, event->target_name,
			sizeof(event->target_name),
			HIDEOUT_FILE_TARGET_UNAVAILABLE,
			HIDEOUT_FILE_TARGET_TRUNCATED,
			&event->flags);
	}
	if (forced_file_type)
		event->file_type = forced_file_type;
	bpf_ringbuf_submit(event, 0);
	return 0;
}

/*
 * Trace the security wrappers rather than attaching as a BPF LSM. A kernel
 * may be built with CONFIG_BPF_LSM and accept an LSM link while omitting
 * "bpf" from the active boot-time LSM list; such a link is inert. The
 * security wrappers remain the stable, BTF-described boundary used by the
 * active LSMs and let the observer prove that each hook is actually reached
 * without changing authorization decisions.
 */
SEC("fentry/security_file_free")
int hideout_forget_file(unsigned long long *ctx)
{
	struct file *file = (struct file *)ctx[0];
	__u64 key = (__u64)file;

	if (file && bpf_map_lookup_elem(&observed_files, &key) &&
	    bpf_map_delete_elem(&observed_files, &key) < 0)
		note_state_drop();
	return 0;
}

SEC("fexit/security_file_open")
int hideout_observe_file_open(unsigned long long *ctx)
{
	struct file *file = (struct file *)ctx[0];
	struct file_metadata *metadata;
	__u64 key;
	__s64 result = (__s64)ctx[1];
	__u32 kind = HIDEOUT_FILE_OPEN;

	if (!in_target_cgroup() || !file)
		return 0;
	key = (__u64)file;
	/*
	 * Resolve and replace the metadata once at the wrapper's outcome boundary.
	 * This is both the authoritative open record and the cache seed for later
	 * compact I/O events; a separate entry hook would duplicate the hot-path
	 * trampoline without adding evidence.
	 */
	cache_file(file);
	metadata = bpf_map_lookup_elem(&observed_files, &key);
	if (metadata && metadata->created)
		kind = HIDEOUT_FILE_CREATE;
	return emit_cached_file(kind, file, result, 0, 0, 0);
}

static __always_inline int emit_io(
	__u32 kind, struct file *file, __s64 result)
{
	__u64 bytes = 0;

	if (result >= 0)
		bytes = result;
	return emit_compact_io(kind, file, result, bytes);
}

SEC("fexit/vfs_read")
int hideout_observe_vfs_read(unsigned long long *ctx)
{
	return emit_io(
		HIDEOUT_FILE_READ, (struct file *)ctx[0], (__s64)ctx[4]);
}

SEC("fexit/vfs_write")
int hideout_observe_vfs_write(unsigned long long *ctx)
{
	return emit_io(
		HIDEOUT_FILE_WRITE, (struct file *)ctx[0], (__s64)ctx[4]);
}

SEC("fexit/vfs_readv")
int hideout_observe_vfs_readv(unsigned long long *ctx)
{
	return emit_io(
		HIDEOUT_FILE_READ, (struct file *)ctx[0], (__s64)ctx[5]);
}

SEC("fexit/vfs_writev")
int hideout_observe_vfs_writev(unsigned long long *ctx)
{
	return emit_io(
		HIDEOUT_FILE_WRITE, (struct file *)ctx[0], (__s64)ctx[5]);
}

SEC("fexit/vfs_copy_file_range")
int hideout_observe_copy_file_range(unsigned long long *ctx)
{
	struct file *source = (struct file *)ctx[0];
	struct file *target = (struct file *)ctx[2];
	__s64 result = (__s64)ctx[6];

	emit_io(HIDEOUT_FILE_READ, source, result);
	emit_io(HIDEOUT_FILE_WRITE, target, result);
	return 0;
}

SEC("tracepoint/syscalls/sys_enter_mmap")
int hideout_capture_mmap_length(struct trace_event_raw_sys_enter *ctx)
{
	__u64 key;
	__u64 length;

	if (!in_target_cgroup())
		return 0;
	key = bpf_get_current_pid_tgid();
	length = ctx->args[1];
	if (bpf_map_update_elem(&mmap_lengths, &key, &length, BPF_ANY) < 0)
		note_state_drop();
	return 0;
}

SEC("tracepoint/syscalls/sys_exit_mmap")
int hideout_forget_mmap_length(struct trace_event_raw_sys_exit *ctx)
{
	__u64 key = bpf_get_current_pid_tgid();

	(void)ctx;
	if (bpf_map_lookup_elem(&mmap_lengths, &key) &&
	    bpf_map_delete_elem(&mmap_lengths, &key) < 0)
		note_state_drop();
	return 0;
}

SEC("fexit/security_mmap_file")
int hideout_observe_mmap_file(unsigned long long *ctx)
{
	struct file *file = (struct file *)ctx[0];
	struct hideout_file_event *event;
	struct file_metadata *metadata;
	__u64 key = bpf_get_current_pid_tgid();
	__u64 file_key = (__u64)file;
	__u64 *length;
	int ret = (__s32)ctx[3];

	if (!in_target_cgroup() || !file)
		return 0;
	event = reserve_cached_file_event(HIDEOUT_FILE_MMAP);
	if (!event)
		return 0;
	event->file_key = file_key;
	metadata = bpf_map_lookup_elem(&observed_files, &file_key);
	if (metadata) {
		copy_metadata(event, metadata);
		metadata->announced = 1;
	} else {
		clear_cached_file_event_path(event);
		event->flags |= HIDEOUT_FILE_PATH_UNAVAILABLE |
				HIDEOUT_FILE_STATE_UNAVAILABLE;
		fill_file_identity(
			file, &event->device, &event->inode,
			&event->file_type, &event->flags);
		note_state_drop();
	}
	length = bpf_map_lookup_elem(&mmap_lengths, &key);
	if (length)
		event->bytes = *length;
	else
		event->flags |= HIDEOUT_FILE_BYTES_UNAVAILABLE;
	event->result = ret;
	event->flags |= HIDEOUT_FILE_AUTHORIZATION_HOOK;
	if (ret == 0)
		event->flags |= HIDEOUT_FILE_OUTCOME_UNKNOWN;
	bpf_ringbuf_submit(event, 0);
	return 0;
}

SEC("fexit/security_path_truncate")
int hideout_observe_path_truncate(unsigned long long *ctx)
{
	struct path *path = (struct path *)ctx[0];
	int ret = (__s32)ctx[1];

	emit_path_event(
		HIDEOUT_FILE_TRUNCATE, path, 0, 0, 0, ret, 0, 0);
	return 0;
}

SEC("fexit/security_file_truncate")
int hideout_observe_file_truncate(unsigned long long *ctx)
{
	struct file *file = (struct file *)ctx[0];
	int ret = (__s32)ctx[1];

	if (in_target_cgroup() && file)
		emit_cached_file(
			HIDEOUT_FILE_TRUNCATE, file, ret, 0, 1, 1);
	return 0;
}

SEC("fexit/security_path_unlink")
int hideout_observe_path_unlink(unsigned long long *ctx)
{
	struct path *dir = (struct path *)ctx[0];
	struct dentry *dentry = (struct dentry *)ctx[1];
	int ret = (__s32)ctx[2];

	emit_path_event(
		HIDEOUT_FILE_UNLINK, dir, dentry, 0, 0, ret, 0, 0);
	return 0;
}

SEC("fexit/security_path_rename")
int hideout_observe_path_rename(unsigned long long *ctx)
{
	struct path *old_dir = (struct path *)ctx[0];
	struct dentry *old_dentry = (struct dentry *)ctx[1];
	struct path *new_dir = (struct path *)ctx[2];
	struct dentry *new_dentry = (struct dentry *)ctx[3];
	int ret = (__s32)ctx[5];

	emit_path_event(
		HIDEOUT_FILE_RENAME, old_dir, old_dentry,
		new_dir, new_dentry, ret, 0, 0);
	return 0;
}

SEC("fexit/security_path_link")
int hideout_observe_path_link(unsigned long long *ctx)
{
	struct dentry *old_dentry = (struct dentry *)ctx[0];
	struct path *new_dir = (struct path *)ctx[1];
	struct dentry *new_dentry = (struct dentry *)ctx[2];
	struct hideout_file_event *event;
	int ret = (__s32)ctx[3];

	event = reserve_file_event(HIDEOUT_FILE_HARDLINK);
	if (!event)
		return 0;
	event->flags |= HIDEOUT_FILE_PATH_ALIASED |
			HIDEOUT_FILE_AUTHORIZATION_HOOK;
	if (ret == 0)
		event->flags |= HIDEOUT_FILE_OUTCOME_UNKNOWN;
	event->result = ret;
	fill_dentry_name(
		old_dentry, event->path_name, sizeof(event->path_name),
		HIDEOUT_FILE_PATH_UNAVAILABLE,
		HIDEOUT_FILE_PATH_TRUNCATED, &event->flags);
	fill_dentry_identity(
		old_dentry, &event->device, &event->inode,
		&event->file_type, &event->flags);
	fill_path_alias(
		new_dir, event->target_path, sizeof(event->target_path),
		HIDEOUT_FILE_TARGET_UNAVAILABLE,
		HIDEOUT_FILE_TARGET_TRUNCATED, &event->flags);
	fill_dentry_name(
		new_dentry, event->target_name,
		sizeof(event->target_name),
		HIDEOUT_FILE_TARGET_UNAVAILABLE,
		HIDEOUT_FILE_TARGET_TRUNCATED, &event->flags);
	bpf_ringbuf_submit(event, 0);
	return 0;
}

SEC("fexit/security_path_symlink")
int hideout_observe_path_symlink(unsigned long long *ctx)
{
	struct path *dir = (struct path *)ctx[0];
	struct dentry *dentry = (struct dentry *)ctx[1];
	const char *old_name = (const char *)ctx[2];
	struct hideout_file_event *event;
	long result;
	int ret = (__s32)ctx[3];

	event = reserve_file_event(HIDEOUT_FILE_SYMLINK);
	if (!event)
		return 0;
	event->flags |= HIDEOUT_FILE_PATH_ALIASED |
			HIDEOUT_FILE_AUTHORIZATION_HOOK |
			HIDEOUT_FILE_IDENTITY_UNAVAILABLE;
	if (ret == 0)
		event->flags |= HIDEOUT_FILE_OUTCOME_UNKNOWN;
	event->result = ret;
	note_identity_failure();
	fill_path_alias(
		dir, event->path, sizeof(event->path),
		HIDEOUT_FILE_PATH_UNAVAILABLE,
		HIDEOUT_FILE_PATH_TRUNCATED, &event->flags);
	fill_dentry_name(
		dentry, event->path_name, sizeof(event->path_name),
		HIDEOUT_FILE_PATH_UNAVAILABLE,
		HIDEOUT_FILE_PATH_TRUNCATED, &event->flags);
	result = bpf_probe_read_kernel_str(
		event->target_path, sizeof(event->target_path), old_name);
	if (result < 0) {
		event->flags |= HIDEOUT_FILE_TARGET_UNAVAILABLE;
		note_path_failure();
	} else if (result == sizeof(event->target_path)) {
		event->flags |= HIDEOUT_FILE_TARGET_TRUNCATED;
	}
	bpf_ringbuf_submit(event, 0);
	return 0;
}

SEC("fexit/security_path_chmod")
int hideout_observe_path_chmod(unsigned long long *ctx)
{
	struct path *path = (struct path *)ctx[0];
	int ret = (__s32)ctx[2];

	emit_path_event(
		HIDEOUT_FILE_METADATA, path, 0, 0, 0, ret, 0, 0);
	return 0;
}

SEC("fexit/security_path_chown")
int hideout_observe_path_chown(unsigned long long *ctx)
{
	struct path *path = (struct path *)ctx[0];
	int ret = (__s32)ctx[3];

	emit_path_event(
		HIDEOUT_FILE_METADATA, path, 0, 0, 0, ret, 0, 0);
	return 0;
}

SEC("fexit/security_path_mkdir")
int hideout_observe_path_mkdir(unsigned long long *ctx)
{
	struct path *dir = (struct path *)ctx[0];
	struct dentry *dentry = (struct dentry *)ctx[1];
	int ret = (__s32)ctx[3];

	emit_path_event(
		HIDEOUT_FILE_MKDIR, dir, dentry, 0, 0, ret, 0,
		HIDEOUT_FILE_TYPE_DIRECTORY);
	return 0;
}

SEC("fexit/security_path_rmdir")
int hideout_observe_path_rmdir(unsigned long long *ctx)
{
	struct path *dir = (struct path *)ctx[0];
	struct dentry *dentry = (struct dentry *)ctx[1];
	int ret = (__s32)ctx[2];

	emit_path_event(
		HIDEOUT_FILE_RMDIR, dir, dentry, 0, 0, ret, 0,
		HIDEOUT_FILE_TYPE_DIRECTORY);
	return 0;
}

SEC("tracepoint/sched/sched_process_exit")
int hideout_cleanup_file_thread(
	struct trace_event_raw_sched_process_template *ctx)
{
	__u64 key = bpf_get_current_pid_tgid();

	(void)ctx;
	if (bpf_map_lookup_elem(&mmap_lengths, &key) &&
	    bpf_map_delete_elem(&mmap_lengths, &key) < 0)
		note_state_drop();
	return 0;
}

char hideout_file_bpf_license[] SEC("license") = "GPL";
