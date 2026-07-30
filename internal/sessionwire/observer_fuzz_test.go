package sessionwire

import (
	"bytes"
	"encoding/json"
	"testing"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func FuzzObserverEnvelopeStrictRoundTrip(f *testing.F) {
	owner, err := workloadtypes.NewReusableOwner(
		"env_fuzz",
		"lima",
		"incarnation-fuzz",
	)
	if err != nil {
		f.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"pid":          uint32(42),
		"tid":          uint32(42),
		"execSequence": uint64(1),
	})
	if err != nil {
		f.Fatal(err)
	}
	valid := ObservationEnvelope{
		Schema:             ObservationSchema,
		Owner:              owner,
		SessionID:          "ses_observer_fuzz",
		CgroupID:           3141,
		ObserverGeneration: 1,
		CPU:                2,
		Sequence:           1,
		MonotonicNS:        1001,
		Kind:               "process.exec",
		Payload:            payload,
	}
	var seed bytes.Buffer
	if err := WriteObserverEnvelope(&seed, valid); err != nil {
		f.Fatal(err)
	}
	f.Add(seed.Bytes())
	f.Add([]byte{})
	f.Add([]byte(`{"schema":"hideout.observation.v1"}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		decoded, err := ReadObserverEnvelope(bytes.NewReader(raw))
		if err != nil {
			return
		}
		if err := decoded.Validate(); err != nil {
			t.Fatalf("decoder accepted an invalid envelope: %v", err)
		}

		var first bytes.Buffer
		if err := WriteObserverEnvelope(&first, decoded); err != nil {
			t.Fatalf("accepted envelope could not be encoded: %v", err)
		}
		roundTrip, err := ReadObserverEnvelope(bytes.NewReader(first.Bytes()))
		if err != nil {
			t.Fatalf("canonical envelope could not be decoded: %v", err)
		}
		var second bytes.Buffer
		if err := WriteObserverEnvelope(&second, roundTrip); err != nil {
			t.Fatalf("round-trip envelope could not be encoded: %v", err)
		}
		if !bytes.Equal(first.Bytes(), second.Bytes()) {
			t.Fatalf("observer envelope encoding is not stable")
		}
	})
}
