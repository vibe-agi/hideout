// Package daemon implements hideoutd, the per-user local control-plane daemon.
//
// The daemon serves the existing typed Manager API by mounting
// manager.API.Handler() as a parity-locked subrouter over a store-rooted Unix
// socket, so behavior parity with embedded mode holds by construction: it adds no
// Manager operation class, no raw profile write, and no host execution. Its own
// lifecycle/observability endpoints (status, and — in later slices — event
// subscription) are a separate surface outside /api/v1/.
//
// Trust shape follows docs/threat-model.md: the socket lives under an
// operator-private runtime subdirectory of the store, which is structurally
// unreachable from real backend guests; for a weak native target that shares the
// operator UID, the operator token is the sole defense. Every request is
// token-authenticated (reusing manager.API.authorize); unauthenticated refusals
// are recorded in a persistent, session-unbound daemon-local audit log without
// ever storing client-supplied token material. Daemon absence changes nothing —
// existing surfaces keep using embedded Manager Core.
package daemon
