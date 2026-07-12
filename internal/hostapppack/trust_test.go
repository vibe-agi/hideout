package hostapppack

import (
	"strings"
	"testing"
	"time"
)

func TestUnverifiedAppTrustRequiresExactCoreDigests(t *testing.T) {
	record := UnverifiedAppTrust{
		Schema: UnverifiedAppTrustVersion, QualifiedAppRef: "community.editor/rev_123/editor",
		RootClass: "operator-applications", CanonicalPathDigest: "sha256:" + strings.Repeat("a", 64),
		ContentDigest:  "bundle-tree-v1:sha256:" + strings.Repeat("b", 64),
		IdentityDigest: "sha256:" + strings.Repeat("c", 64), AcceptedAt: time.Now().UTC(),
	}
	if err := ValidateUnverifiedAppTrust(record); err != nil {
		t.Fatal(err)
	}
	if !MatchesUnverifiedAppTrust(record, record.QualifiedAppRef, record.RootClass, record.CanonicalPathDigest, record.ContentDigest, record.IdentityDigest) {
		t.Fatal("exact Core-observed trust did not match")
	}
	if MatchesUnverifiedAppTrust(record, record.QualifiedAppRef, record.RootClass, record.CanonicalPathDigest, "bundle-tree-v1:sha256:"+strings.Repeat("d", 64), record.IdentityDigest) {
		t.Fatal("changed unsigned app content inherited trust")
	}
	record.AcceptedAt = time.Time{}
	if err := ValidateUnverifiedAppTrust(record); err == nil {
		t.Fatal("trust without an explicit acceptance time was accepted")
	}
}
