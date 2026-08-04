package migration

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"
)

var machineIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// MachineIdentityDigest canonicalizes the Linux machine-id text before hashing.
// The digest, rather than the raw host identity, is safe to bind into plans and
// adoption receipts.
func MachineIdentityDigest(raw []byte) (Digest, error) {
	value := strings.TrimSuffix(string(raw), "\n")
	if !machineIDPattern.MatchString(value) ||
		(string(raw) != value && string(raw) != value+"\n") {
		return "", fmt.Errorf("%w: Linux machine-id is invalid", ErrInvalidBundle)
	}
	return identityDigest([]byte(value)), nil
}

// SSHHostPublicKeyDigest hashes the parsed SSH public-key wire representation,
// excluding comments and whitespace that are not part of host identity.
func SSHHostPublicKeyDigest(raw []byte) (Digest, error) {
	publicKey, _, _, rest, err := ssh.ParseAuthorizedKey(raw)
	if err != nil || len(strings.TrimSpace(string(rest))) != 0 {
		return "", fmt.Errorf("%w: SSH host public key is invalid", ErrInvalidBundle)
	}
	return identityDigest(publicKey.Marshal()), nil
}

func (identity GuestIdentityEvidence) Equal(other GuestIdentityEvidence) bool {
	if identity.MachineIDDigest != other.MachineIDDigest ||
		len(identity.SSHHostKeyDigests) != len(other.SSHHostKeyDigests) {
		return false
	}
	left := append([]Digest(nil), identity.SSHHostKeyDigests...)
	right := append([]Digest(nil), other.SSHHostKeyDigests...)
	sort.Slice(left, func(i, j int) bool { return left[i] < left[j] })
	sort.Slice(right, func(i, j int) bool { return right[i] < right[j] })
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func identityDigest(value []byte) Digest {
	digest := sha256.Sum256(value)
	return Digest(fmt.Sprintf("sha256:%x", digest[:]))
}
