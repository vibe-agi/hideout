// Package bpf contains generated, package-owned workload observation programs.
//
// Generation is intentionally explicit and pinned. Runtime code never invokes
// a compiler or downloads a BPF program.
package bpf

//go:generate ../../../../scripts/generate-workload-observer-bpf.sh
