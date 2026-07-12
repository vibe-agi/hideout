// Package cmdgrammar holds authority-free command grammars that parse a guest
// command's argv into an app-agnostic unbound open-resource proposal.
//
// A grammar carries ZERO host authority. It never resolves host paths, never
// emits raw guest argv to a provider, and never selects a host binary. Its
// output is a proposal that Go (internal/hostcap) strictly re-decodes and
// field-validates before any provider acts, so a grammar bug or a hostile
// community-authored grammar cannot widen authority. Unknown flags are denied.
//
// Every command receives its declarative grammar from an immutable per-run
// binding. A missing binding has no compatibility grammar and fails closed.
package cmdgrammar
