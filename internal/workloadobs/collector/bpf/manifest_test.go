package bpf

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cilium/ebpf"
)

func TestEmbeddedObserverArtifactManifestAndObjectAreExact(t *testing.T) {
	manifest, err := VerifyEmbeddedArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	if manifest.BPF2GoVersion != ObserverBPF2GoVersion ||
		manifest.CompilerVersion != ObserverCompilerVersion ||
		manifest.License != ObserverSourceLicense ||
		manifest.KernelProgramLicense != ObserverProgramLicense {
		t.Fatalf("manifest=%+v", manifest)
	}
	digest, err := EmbeddedObjectDigest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != "sha256:"+manifest.ObjectSHA256 {
		t.Fatalf("digest=%q manifest=%q", digest, manifest.ObjectSHA256)
	}
	spec, loadedManifest, err := LoadEmbeddedSpec()
	if err != nil {
		t.Fatal(err)
	}
	programTypes := map[string]ebpf.ProgramType{
		"hideout_capture_exec_argv":     ebpf.TracePoint,
		"hideout_capture_execveat_argv": ebpf.TracePoint,
		"hideout_observe_process_fork":  ebpf.RawTracepoint,
		"hideout_observe_process_exec":  ebpf.RawTracepoint,
		"hideout_observe_process_exit":  ebpf.RawTracepoint,
	}
	for name, programType := range programTypes {
		program := spec.Programs[name]
		if program == nil || program.Type != programType ||
			program.License != ObserverProgramLicense {
			t.Fatalf("spec program %q=%+v", name, program)
		}
	}
	for _, name := range []string{
		"observation_events",
		"exec_sequences",
		"fork_parents",
		"pending_execs",
		"process_counters",
	} {
		if spec.Maps[name] == nil {
			t.Fatalf("spec map %q is missing", name)
		}
	}
	if loadedManifest.ObjectSHA256 != manifest.ObjectSHA256 {
		t.Fatalf("loadedManifest=%+v", loadedManifest)
	}
}

func TestEmbeddedFileObserverArtifactManifestAndObjectAreExact(t *testing.T) {
	manifest, err := VerifyEmbeddedFileArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Source != FileObserverSourcePath ||
		manifest.Object != FileObserverObjectPath ||
		manifest.GeneratedGo != FileObserverGeneratedGoPath ||
		manifest.BPF2GoVersion != ObserverBPF2GoVersion ||
		manifest.CompilerVersion != ObserverCompilerVersion ||
		manifest.License != ObserverSourceLicense ||
		manifest.KernelProgramLicense != ObserverProgramLicense {
		t.Fatalf("manifest=%+v", manifest)
	}
	digest, err := EmbeddedFileObjectDigest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != "sha256:"+manifest.ObjectSHA256 {
		t.Fatalf("digest=%q manifest=%q", digest, manifest.ObjectSHA256)
	}
	spec, loadedManifest, err := LoadEmbeddedFileSpec()
	if err != nil {
		t.Fatal(err)
	}
	programTypes := map[string]ebpf.ProgramType{
		"hideout_forget_file":             ebpf.Tracing,
		"hideout_observe_file_open":       ebpf.Tracing,
		"hideout_observe_vfs_read":        ebpf.Tracing,
		"hideout_observe_vfs_write":       ebpf.Tracing,
		"hideout_observe_vfs_readv":       ebpf.Tracing,
		"hideout_observe_vfs_writev":      ebpf.Tracing,
		"hideout_observe_copy_file_range": ebpf.Tracing,
		"hideout_capture_mmap_length":     ebpf.TracePoint,
		"hideout_forget_mmap_length":      ebpf.TracePoint,
		"hideout_observe_mmap_file":       ebpf.Tracing,
		"hideout_observe_path_truncate":   ebpf.Tracing,
		"hideout_observe_file_truncate":   ebpf.Tracing,
		"hideout_observe_path_unlink":     ebpf.Tracing,
		"hideout_observe_path_rename":     ebpf.Tracing,
		"hideout_observe_path_link":       ebpf.Tracing,
		"hideout_observe_path_symlink":    ebpf.Tracing,
		"hideout_observe_path_chmod":      ebpf.Tracing,
		"hideout_observe_path_chown":      ebpf.Tracing,
		"hideout_observe_path_mkdir":      ebpf.Tracing,
		"hideout_observe_path_rmdir":      ebpf.Tracing,
		"hideout_cleanup_file_thread":     ebpf.TracePoint,
	}
	for name, expectedType := range programTypes {
		program := spec.Programs[name]
		if program == nil || program.Type != expectedType ||
			program.License != ObserverProgramLicense {
			t.Fatalf("spec program %q=%+v", name, program)
		}
	}
	for _, name := range []string{
		"file_observation_events",
		"observed_files",
		"mmap_lengths",
		"file_counters",
		"exec_sequences",
		"observer_sequences",
	} {
		if spec.Maps[name] == nil {
			t.Fatalf("spec map %q is missing", name)
		}
	}
	if loadedManifest.ObjectSHA256 != manifest.ObjectSHA256 {
		t.Fatalf("loadedManifest=%+v", loadedManifest)
	}
}

func TestEmbeddedNetworkObserverArtifactManifestAndObjectAreExact(t *testing.T) {
	manifest, err := VerifyEmbeddedNetworkArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Source != NetworkObserverSourcePath ||
		manifest.Object != NetworkObserverObjectPath ||
		manifest.GeneratedGo != NetworkObserverGeneratedGoPath ||
		manifest.BPF2GoVersion != ObserverBPF2GoVersion ||
		manifest.CompilerVersion != ObserverCompilerVersion ||
		manifest.License != ObserverSourceLicense ||
		manifest.KernelProgramLicense != ObserverProgramLicense {
		t.Fatalf("manifest=%+v", manifest)
	}
	digest, err := EmbeddedNetworkObjectDigest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != "sha256:"+manifest.ObjectSHA256 {
		t.Fatalf("digest=%q manifest=%q", digest, manifest.ObjectSHA256)
	}
	spec, loadedManifest, err := LoadEmbeddedNetworkSpec()
	if err != nil {
		t.Fatal(err)
	}
	programTypes := map[string]ebpf.ProgramType{
		"hideout_observe_connect4": ebpf.CGroupSockAddr,
		"hideout_observe_connect6": ebpf.CGroupSockAddr,
		"hideout_observe_sendmsg4": ebpf.CGroupSockAddr,
		"hideout_observe_sendmsg6": ebpf.CGroupSockAddr,
		"hideout_correlate_egress": ebpf.CGroupSKB,
	}
	for name, expectedType := range programTypes {
		program := spec.Programs[name]
		if program == nil || program.Type != expectedType ||
			program.License != ObserverProgramLicense {
			t.Fatalf("spec program %q=%+v", name, program)
		}
	}
	for _, name := range []string{
		"network_observation_events",
		"network_socket_states",
		"network_counters",
		"exec_sequences",
		"observer_sequences",
	} {
		if spec.Maps[name] == nil {
			t.Fatalf("spec map %q is missing", name)
		}
	}
	if loadedManifest.ObjectSHA256 != manifest.ObjectSHA256 {
		t.Fatalf("loadedManifest=%+v", loadedManifest)
	}
}

func TestObserverArtifactManifestRejectsUnknownFieldsAndDigestDrift(t *testing.T) {
	manifest, err := EmbeddedArtifactManifest()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(struct {
		ArtifactManifest
		Unknown bool `json:"unknown"`
	}{ArtifactManifest: manifest, Unknown: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeArtifactManifest(data); !errors.Is(err, ErrArtifactManifest) {
		t.Fatalf("unknown field error=%v", err)
	}

	mutated := append([]byte(nil), _ObserverBytes...)
	mutated[len(mutated)-1] ^= 0xff
	if err := verifyArtifactObject(manifest, mutated); !errors.Is(err, ErrArtifactIntegrity) {
		t.Fatalf("mutated object error=%v", err)
	}
	manifest.ObjectSHA256 = strings.Repeat("z", 64)
	if err := verifyArtifactObject(manifest, _ObserverBytes); !errors.Is(err, ErrArtifactManifest) {
		t.Fatalf("invalid digest error=%v", err)
	}
}
