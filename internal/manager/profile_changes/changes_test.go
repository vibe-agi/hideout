package profilechanges

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/profile"
)

func TestBuildNetworkEnvironmentAndRetentionChanges(t *testing.T) {
	current := profile.Default("default")
	changes := []Change{
		mustChange(t, KindNetworkPosture, map[string]any{
			"mode": "proxy",
		}),
		mustChange(t, KindNetworkProxyRef, map[string]any{
			"ref": "local-proxy",
		}),
		mustChange(t, KindNetworkDNS, map[string]any{
			"mode": "doh", "serverIp": "1.1.1.1",
		}),
		mustChange(t, KindProfileEnvironment, map[string]any{
			"set":       map[string]string{"PUBLIC_NAME": "visible value"},
			"inherit":   []string{"PROJECT_MODE"},
			"deny":      []string{"*_TOKEN"},
			"unset":     []string{"OLD_NAME"},
			"uninherit": []string{"OLD_LANG"},
			"undeny":    []string{"OLD_*"},
		}),
		mustChange(t, KindActivityRetention, map[string]any{
			"maxBytes": 268435456, "maxAgeSeconds": 86400,
		}),
	}
	result, err := Build(current, changes, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Desired.Network.Mode != profile.NetworkModeTun2Socks ||
		result.Desired.Network.ProxySecretRef != "local-proxy" ||
		result.Desired.Network.MediatedResolver != "1.1.1.1" {
		t.Fatalf("network desired=%+v", result.Desired.Network)
	}
	if result.Desired.Env.Public["PUBLIC_NAME"] != "visible value" ||
		!contains(result.Desired.Env.Inherit, "PROJECT_MODE") ||
		!reflect.DeepEqual(result.Desired.Env.Deny, []string{"*_TOKEN"}) {
		t.Fatalf("environment desired=%+v", result.Desired.Env)
	}
	if result.Desired.Activity == nil ||
		result.Desired.Activity.Retention.MaxBytes != 268435456 ||
		result.Desired.Activity.Retention.MaxAgeSeconds != 86400 {
		t.Fatalf("activity desired=%+v", result.Desired.Activity)
	}
	var retentionDiff *Diff
	for index := range result.Diff {
		if result.Diff[index].Kind == KindActivityRetention {
			retentionDiff = &result.Diff[index]
			break
		}
	}
	if retentionDiff == nil ||
		retentionDiff.Before != "256 MiB / VM lifecycle (default)" ||
		retentionDiff.After != "256 MiB / 1 day" ||
		retentionDiff.Scope != "activity-owner" {
		t.Fatalf("activity retention review=%+v", retentionDiff)
	}
	encoded, err := json.Marshal(result.Diff)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("visible value")) {
		t.Fatalf("review diff leaked environment value: %s", encoded)
	}
}

func TestEnvironmentReviewCanonicalizationHidesValues(t *testing.T) {
	private, err := Normalize(KindProfileEnvironment, json.RawMessage(
		`{"set":{"SERVICE_TOKEN":"user:password@private.invalid"},"unset":["OLD"]}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	review, err := Review(KindProfileEnvironment, private)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(review, []byte("user:password")) ||
		!bytes.Contains(review, []byte(environmentValueProvided)) {
		t.Fatalf("unsafe environment review=%s", review)
	}
	reviewAgain, err := Review(KindProfileEnvironment, review)
	if err != nil || !bytes.Equal(reviewAgain, review) {
		t.Fatalf("review is not canonical: first=%s second=%s err=%v", review, reviewAgain, err)
	}
}

func TestHostFSChangeIsStrictAndDeterministic(t *testing.T) {
	current := profile.Default("default")
	change := mustChange(t, KindProfileHostFS, map[string]any{
		"operation": "add",
		"rule":      "read:/tmp/example.txt",
		"reason":    "review fixture",
	})
	first, err := Build(current, []Change{change}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(current, []Change{change}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Desired.HostFS, second.Desired.HostFS) ||
		len(first.Desired.HostFS.Grants) != 1 ||
		!strings.HasPrefix(first.Desired.HostFS.Grants[0].ID, "hfs_") {
		t.Fatalf(
			"HostFS build is not deterministic: first=%+v second=%+v",
			first.Desired.HostFS,
			second.Desired.HostFS,
		)
	}
	if _, err := Normalize(
		KindProfileHostFS,
		json.RawMessage(
			`{"operation":"add","rule":"read:/tmp/example.txt","reason":"x","hostCommand":"rm"}`,
		),
	); err == nil {
		t.Fatal("HostFS change accepted an unknown authority field")
	}
}

func TestCommandProxyAndAdapterChangesUseProfileValidation(t *testing.T) {
	profileDir := t.TempDir()
	scriptPath := filepath.Join(profileDir, "adapter.js")
	if err := os.WriteFile(
		scriptPath,
		[]byte(`function decideCommandAdapter(){return {outcome:"deny",reason:"fixture"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	current := profile.Default("default")
	result, err := Build(
		current,
		[]Change{
			mustChange(t, KindProfileCommandProxy, map[string]any{
				"operation": "add-open", "command": "browse",
			}),
			mustChange(t, KindProfileCommandAdapter, map[string]any{
				"operation":  "add-local",
				"adapterId":  "fixture",
				"path":       scriptPath,
				"entrypoint": "decideCommandAdapter",
				"commands":   []string{"inspect"},
			}),
		},
		Options{ProfileDir: profileDir},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Desired.CommandProxy.Commands["browse"].Action != "host.open" {
		t.Fatalf("command proxy desired=%+v", result.Desired.CommandProxy)
	}
	adapter := result.Desired.CommandAdapters.Adapters["fixture"]
	if !adapter.Enabled || adapter.Digest == "" ||
		!reflect.DeepEqual(adapter.Commands, []string{"inspect"}) {
		t.Fatalf("command adapter desired=%+v", adapter)
	}

	duplicate := mustChange(t, KindProfileCommandAdapter, map[string]any{
		"operation": "add-local", "adapterId": "fixture",
		"path": scriptPath, "commands": []string{"browse"},
	})
	if _, err := Build(
		result.Desired,
		[]Change{duplicate},
		Options{ProfileDir: profileDir},
	); err == nil {
		t.Fatal("command adapter accepted a command already owned by a proxy")
	}
}

func TestNormalizeRejectsMalformedClosedUnionValues(t *testing.T) {
	tests := []struct {
		kind string
		raw  string
	}{
		{KindNetworkPosture, `{"mode":"vpn"}`},
		{KindNetworkProxyRef, `{"ref":"local-proxy","value":"secret"}`},
		{KindNetworkDNS, `{"mode":"system","serverIp":"1.1.1.1"}`},
		{KindProfileEnvironment, `{"set":{"1INVALID":"value"}}`},
		{KindProfileCommandProxy, `{"operation":"exec","command":"open"}`},
		{KindProfileCommandAdapter, `{"operation":"remove","adapterId":"x","path":"/tmp/x"}`},
		{KindActivityRetention, `{"maxBytes":0,"maxAgeSeconds":1}`},
		{"future.unknown", `{"enabled":true}`},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			if _, err := Normalize(
				test.kind,
				json.RawMessage(test.raw),
			); err == nil {
				t.Fatalf("Normalize(%q, %s) succeeded", test.kind, test.raw)
			}
		})
	}
}

func TestBuildRejectsNoopAndIncompleteProxy(t *testing.T) {
	current := profile.Default("default")
	_, err := Build(current, []Change{
		mustChange(t, KindNetworkPosture, map[string]any{"mode": "direct"}),
	}, Options{})
	if !errors.Is(err, ErrNoChange) {
		t.Fatalf("noop error=%v want %v", err, ErrNoChange)
	}
	_, err = Build(current, []Change{
		mustChange(t, KindNetworkPosture, map[string]any{"mode": "proxy"}),
	}, Options{})
	if err == nil {
		t.Fatal("incomplete proxy posture succeeded")
	}
}

func TestNetworkDirectPostureKeepsReusableProxyReferenceAndResolver(t *testing.T) {
	current := profile.Default("default")
	current.Network.Mode = profile.NetworkModeTun2Socks
	current.Network.ProxySecretRef = "local-proxy"
	current.Network.MediatedResolver = "1.1.1.1"
	result, err := Build(current, []Change{
		mustChange(t, KindNetworkPosture, map[string]any{"mode": "direct"}),
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Desired.Network.Mode != profile.NetworkModeDirect ||
		result.Desired.Network.ProxySecretRef != "local-proxy" ||
		result.Desired.Network.MediatedResolver != "1.1.1.1" {
		t.Fatalf(
			"direct posture forgot reusable proxy configuration: %+v",
			result.Desired.Network,
		)
	}
}

func mustChange(
	t *testing.T,
	kind string,
	value any,
) Change {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := Normalize(kind, raw)
	if err != nil {
		t.Fatal(err)
	}
	return Change{Kind: kind, Value: normalized}
}
