package manager

import (
	"bytes"
	"encoding/json"
	"testing"
)

func FuzzTypedChangeNormalizationAndReview(f *testing.F) {
	const canary = "fuzz-user:fuzz-password@proxy.invalid"
	for _, seed := range []struct {
		kind string
		raw  string
	}{
		{
			kind: ChangeNetworkDNS,
			raw:  `{"serverIp":"1.1.1.1","mode":"doh"}`,
		},
		{
			kind: ChangeProfileEnvironment,
			raw: `{"set":{"LOCAL_PROXY":"socks5://` +
				canary + `:7890"}}`,
		},
		{
			kind: ChangeActivityRetention,
			raw:  `{"maxBytes":268435456,"maxAgeSeconds":86400}`,
		},
		{
			kind: "future.unknown",
			raw:  `{"enabled":true}`,
		},
	} {
		f.Add(seed.kind, []byte(seed.raw))
	}

	f.Fuzz(func(t *testing.T, kind string, raw []byte) {
		registry := DefaultTypedChangeRegistry()
		draft := ConfigurationDraft{
			Schema:       ConfigurationDraftSchema,
			Profile:      "default",
			BaseRevision: 1,
			Changes: []TypedChange{{
				Kind:  kind,
				Value: append(json.RawMessage(nil), raw...),
			}},
		}
		normalized, err := registry.NormalizeDraft(draft)
		if err != nil {
			return
		}
		if len(normalized.Changes) != 1 ||
			!json.Valid(normalized.Changes[0].Value) {
			t.Fatalf("accepted change is not one valid JSON value: %+v", normalized)
		}

		again, err := registry.NormalizeDraft(normalized)
		if err != nil || !rawChangesEqual(normalized.Changes, again.Changes) {
			t.Fatalf(
				"normalization is not idempotent: first=%s second=%s err=%v",
				normalized.Changes[0].Value,
				changeValueForFuzzLog(again.Changes),
				err,
			)
		}

		reviewed, err := registry.ReviewChanges(normalized.Changes)
		if err != nil || len(reviewed) != 1 ||
			!json.Valid(reviewed[0].Value) {
			t.Fatalf("review failed for accepted change: reviewed=%+v err=%v", reviewed, err)
		}
		reviewedAgain, err := registry.ReviewChanges(reviewed)
		if err != nil || !rawChangesEqual(reviewed, reviewedAgain) {
			t.Fatalf(
				"review is not idempotent: first=%s second=%s err=%v",
				reviewed[0].Value,
				changeValueForFuzzLog(reviewedAgain),
				err,
			)
		}
		if bytes.Contains(raw, []byte(canary)) &&
			bytes.Contains(reviewed[0].Value, []byte(canary)) {
			t.Fatalf("public review retained environment canary: %s", reviewed[0].Value)
		}
	})
}

func changeValueForFuzzLog(changes []TypedChange) json.RawMessage {
	if len(changes) == 0 {
		return nil
	}
	return changes[0].Value
}
