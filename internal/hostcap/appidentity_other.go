//go:build !darwin

package hostcap

import "context"

// ObserveDarwinSigningIdentityContext exists on non-darwin builds only so the
// module keeps cross-compiling: internal/manager wires it as the default
// signing observer for the macOS-only host-app projection, and that reference
// is not itself build-tagged. Linux guest helpers and the Linux test binaries
// the real gates compile come out of this same module, so a darwin-only symbol
// reached from portable code breaks `GOOS=linux go build ./...`.
//
// Callers reach this stub only if a platform guard is bypassed, so it is
// fail-closed and observes nothing.
func ObserveDarwinSigningIdentityContext(_ context.Context, _ string) (SigningObservation, error) {
	return SigningObservation{}, &Error{
		Code:   CodeAppAbsent,
		Reason: "host application signing identity can only be observed on a macOS host",
	}
}
