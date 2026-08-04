package migration

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/profile"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestPortableProfileRoundTripKeepsInertConfigAndExcludesAuthority(t *testing.T) {
	source := profile.Default("dev")
	source.Identity = profile.Identity{
		User: "developer", Hostname: "portable-dev", Timezone: "Asia/Singapore", Locale: "en_US.UTF-8",
	}
	source.Workspace.PathMode = profile.WorkspacePathModePreserve
	source.Env.Public = map[string]string{"API_TOKEN": "sentinel-profile-secret"}
	source.Env.Deny = []string{"SECOND", "FIRST"}
	source.Env.Inherit = []string{"AWS_SECRET_ACCESS_KEY"}
	source.Git = profile.Git{UserName: "Portable Developer", UserEmail: "portable@example.test"}
	source.Tools.ExpectedCommands = []string{"zsh", "git"}
	source.Network = profile.Network{
		Mode: profile.NetworkModeTun2Socks, ProxySecretRef: "local-proxy",
		MediatedResolver: "1.1.1.1",
	}
	source.EndpointExposure.HostToGuest = []profile.EndpointCandidate{{
		ID: "dev-api", Owner: "developer", Proto: "tcp", TargetAddress: "127.0.0.1:8080",
	}}
	source.Activity = &profile.ActivityConfig{
		Retention: workloadtypes.ActivityRetentionPolicy{MaxBytes: 4096, MaxAgeSeconds: 3600},
	}
	source.Metadata = map[string]string{
		"profileId": "prf_0123456789abcdef", "identityId": "id_0123456789abcdef",
		"machineId": strings.Repeat("a", 32), "lineageMode": "create",
	}
	if err := source.Validate(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := NormalizePortableProfile(source)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Environment.BaseImage != source.BaseImageOrBuiltin() ||
		!slices.Equal(snapshot.EnvDeny, []string{"FIRST", "SECOND"}) ||
		!slices.Equal(snapshot.ExpectedCommands, []string{"git", "zsh"}) {
		t.Fatalf("portable fields were not normalized: %+v", snapshot)
	}
	encoded, err := EncodePortableProfile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	encodedAgain, err := EncodePortableProfile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, encodedAgain) {
		t.Fatal("portable profile encoding is not deterministic")
	}
	for _, forbidden := range []string{
		"sentinel-profile-secret", "AWS_SECRET_ACCESS_KEY", "local-proxy",
		"1.1.1.1", "127.0.0.1:8080", "prf_0123456789abcdef",
		"id_0123456789abcdef", strings.Repeat("a", 32),
	} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("portable profile retained excluded authority/identity %q: %s", forbidden, encoded)
		}
	}
	decoded, err := DecodePortableProfile(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, snapshot) {
		t.Fatalf("portable round trip drifted:\nwant=%+v\n got=%+v", snapshot, decoded)
	}

	destination, err := decoded.DestinationProfile("dev-imported")
	if err != nil {
		t.Fatal(err)
	}
	if destination.Metadata != nil || len(destination.Env.Public) != 0 ||
		len(destination.Env.Inherit) != 0 || destination.Network.ProxySecretRef != "" ||
		destination.Network.MediatedResolver != "" || len(destination.EndpointExposure.HostToGuest) != 0 ||
		len(destination.HostFS.Grants) != 0 || len(destination.HostFS.Deny) != 0 ||
		len(destination.CommandAdapters.Adapters) != 0 || len(destination.Policy.ScriptRefs) != 0 ||
		!slices.Equal(destination.Policy.MaxCapabilities, []string{"guest.exec"}) ||
		destination.HostCapabilities.Open.AllowURLs ||
		destination.HostCapabilities.Open.AllowLocalURLs ||
		destination.HostCapabilities.Open.AllowPrivateNetworkURLs ||
		destination.HostCapabilities.Open.AllowWorkspaceFiles || !destination.Audit.Enabled {
		t.Fatalf("destination staging profile retained imported authority: %+v", destination)
	}
	if destination.Identity.Hostname != source.Identity.Hostname ||
		destination.Git != source.Git ||
		destination.BaseImageOrBuiltin() != source.BaseImageOrBuiltin() {
		t.Fatalf("destination staging profile lost inert configuration: %+v", destination)
	}
}

func TestPortableProfileDecodeRejectsUnknownLegacyAndNoncanonicalDocuments(t *testing.T) {
	snapshot, err := NormalizePortableProfile(profile.Default("dev"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodePortableProfile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, encoded, "", "  "); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePortableProfile(pretty.Bytes()); err == nil {
		t.Fatal("noncanonical portable profile was accepted")
	}

	var unknown map[string]any
	if err := json.Unmarshal(encoded, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["proxySecretRef"] = "must-not-enter-portable-profile"
	unknownBytes, err := canonicalMarshal(unknown)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePortableProfile(unknownBytes); err == nil {
		t.Fatal("unknown authority field was accepted")
	}

	legacy := snapshot
	legacy.Schema = "hideout.migration-portable-profile/v0"
	legacyBytes, err := canonicalMarshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePortableProfile(legacyBytes); err == nil {
		t.Fatal("legacy portable profile was implicitly upgraded")
	}

	unsorted := snapshot
	unsorted.ExpectedCommands = []string{"zsh", "git"}
	if _, err := EncodePortableProfile(unsorted); err == nil {
		t.Fatal("noncanonical expected-command order was accepted")
	}
}

func TestPortableProfileNormalizationDoesNotAliasSourceState(t *testing.T) {
	source := profile.Default("dev")
	source.Env.Deny = []string{"SECRET"}
	source.Tools.ExpectedCommands = []string{"git"}
	snapshot, err := NormalizePortableProfile(source)
	if err != nil {
		t.Fatal(err)
	}
	source.Env.Deny[0] = "MUTATED"
	source.Tools.ExpectedCommands[0] = "mutated"
	if !slices.Equal(snapshot.EnvDeny, []string{"SECRET"}) ||
		!slices.Equal(snapshot.ExpectedCommands, []string{"git"}) {
		t.Fatalf("portable snapshot aliases source profile: %+v", snapshot)
	}
}

func TestPortableProfileRejectsCredentialLikeImageQuery(t *testing.T) {
	source := profile.Default("dev")
	source.Environment.BaseImage = "https://images.example.test/dev.qcow2?token=sentinel#sha256:" + strings.Repeat("b", 64)
	if err := source.Validate(); err != nil {
		t.Fatalf("fixture no longer reaches migration-specific query validation: %v", err)
	}
	if _, err := NormalizePortableProfile(source); err == nil {
		t.Fatal("portable profile accepted a query-bearing image URL")
	}
}
