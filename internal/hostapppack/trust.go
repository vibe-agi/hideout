package hostapppack

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const UnverifiedAppTrustVersion = "hideout.unverified-app-trust/v1"

var bundleTreeDigest = regexp.MustCompile(`^bundle-tree-v1:sha256:[0-9a-f]{64}$`)

// UnverifiedAppTrust is an explicit operator acceptance embedded in one
// profile enablement. It carries no path and grants no launch by itself.
type UnverifiedAppTrust struct {
	Schema              string    `json:"schema"`
	QualifiedAppRef     string    `json:"qualifiedAppRef"`
	RootClass           string    `json:"rootClass"`
	CanonicalPathDigest string    `json:"canonicalPathDigest"`
	ContentDigest       string    `json:"contentDigest"`
	IdentityDigest      string    `json:"identityDigest"`
	AcceptedAt          time.Time `json:"acceptedAt"`
}

func ValidateUnverifiedAppTrust(record UnverifiedAppTrust) error {
	if record.Schema != UnverifiedAppTrustVersion {
		return fmt.Errorf("unsupported unverified-app trust schema %q", record.Schema)
	}
	if err := validateText("qualified app ref", record.QualifiedAppRef, 1, 384); err != nil {
		return err
	}
	if strings.Count(record.QualifiedAppRef, "/") != 2 {
		return errors.New("unverified-app trust must bind an exact pack/revision/app reference")
	}
	switch record.RootClass {
	case "system-applications", "applications", "operator-applications":
	default:
		return fmt.Errorf("unverified-app trust root class %q is unsupported", record.RootClass)
	}
	if !validDigest(record.CanonicalPathDigest) || !validDigest(record.IdentityDigest) {
		return errors.New("unverified-app trust identity digests are invalid")
	}
	if !bundleTreeDigest.MatchString(record.ContentDigest) {
		return errors.New("unverified-app trust content digest is invalid")
	}
	if record.AcceptedAt.IsZero() {
		return errors.New("unverified-app trust acceptedAt is required")
	}
	return nil
}

func ValidateUnverifiedAppTrustSet(records []UnverifiedAppTrust) error {
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if err := ValidateUnverifiedAppTrust(record); err != nil {
			return err
		}
		if _, exists := seen[record.QualifiedAppRef]; exists {
			return fmt.Errorf("unverified-app trust for %q is duplicated", record.QualifiedAppRef)
		}
		seen[record.QualifiedAppRef] = struct{}{}
	}
	return nil
}

func SortUnverifiedAppTrust(records []UnverifiedAppTrust) {
	sort.Slice(records, func(i, j int) bool { return records[i].QualifiedAppRef < records[j].QualifiedAppRef })
}

func MatchesUnverifiedAppTrust(record UnverifiedAppTrust, qualifiedAppRef, rootClass, canonicalPathDigest, contentDigest, identityDigest string) bool {
	return ValidateUnverifiedAppTrust(record) == nil &&
		record.QualifiedAppRef == qualifiedAppRef &&
		record.RootClass == rootClass &&
		record.CanonicalPathDigest == canonicalPathDigest &&
		record.ContentDigest == contentDigest &&
		record.IdentityDigest == identityDigest
}
