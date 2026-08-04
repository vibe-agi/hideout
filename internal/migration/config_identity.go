package migration

import (
	"crypto/sha256"
	"fmt"
	"slices"
)

// ConfigIdentityUnavailableEvidence is a public, domain-separated marker. It
// means the config-only exporter intentionally did not observe or preserve a
// guest machine ID or SSH host key; it must never be interpreted as an identity
// digest captured from a VM.
func ConfigIdentityUnavailableEvidence() GuestIdentityEvidence {
	machine := sha256.Sum256([]byte("hideout/config-only/machine-identity-not-captured/v1"))
	ssh := sha256.Sum256([]byte("hideout/config-only/ssh-host-identity-not-captured/v1"))
	return GuestIdentityEvidence{
		MachineIDDigest: Digest(fmt.Sprintf("sha256:%x", machine[:])),
		SSHHostKeyDigests: []Digest{
			Digest(fmt.Sprintf("sha256:%x", ssh[:])),
		},
	}
}

func IsConfigIdentityUnavailableEvidence(value GuestIdentityEvidence) bool {
	expected := ConfigIdentityUnavailableEvidence()
	return value.MachineIDDigest == expected.MachineIDDigest &&
		slices.Equal(value.SSHHostKeyDigests, expected.SSHHostKeyDigests)
}
