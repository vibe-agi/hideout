// Package hostcap is the Core-owned host capability projection layer.
//
// It holds a static, package-owned registry of host capabilities
// (CapabilityDescriptor), the Go providers that execute them, immutable
// command-to-application bindings assembled by Manager from host-app packs,
// and the strict bound open-resource provider contract.
//
// Authority boundary: this package is the ONLY place a projected command's
// host effect is executed. Untrusted layers (command grammars in
// internal/cmdgrammar, JS adapters, profiles, ecosystem artifacts) may only
// parse arguments and propose a structured intent; they never pass raw argv to
// the host and never supply a host binary path, bundle id, or script source.
// There is no runtime interface to register a new descriptor or provider: the
// registry is static Go so new authority is a reviewed code change, never a
// runtime plugin.
//
// v1 implements exactly one capability family, host.app.open-resource. Named
// applications remain pack/profile data. adb, AppleScript templates, and
// result-streaming capabilities exist in the registry model as design-ready
// descriptors and MUST fail closed if dispatched.
package hostcap
