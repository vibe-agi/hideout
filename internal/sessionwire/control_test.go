package sessionwire

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStrictControlRoundTrip(t *testing.T) {
	t.Parallel()

	original := &SupervisorStart{
		Protocol:       SupervisorProtocol,
		SessionID:      "ses_wire_test",
		TargetUser:     "developer",
		GuestWork:      "/workspace",
		Argv:           []string{"sh", "-c", "printf '\\0binary'"},
		Env:            map[string]string{"TERM_PROGRAM": "hideout-test"},
		Terminal:       TerminalDescriptor{Mode: TerminalPTY, Rows: 24, Columns: 80, Term: DefaultTERM},
		ExpectedBootID: "boot-id-test",
		SessionSource:  "/hideout/runtime/sessions/ses_wire_test",
	}
	payload, err := MarshalControl(original)
	if err != nil {
		t.Fatal(err)
	}
	decodedControl, err := DecodeControl(TypeSupervisorStart, payload)
	if err != nil {
		t.Fatal(err)
	}
	decoded, ok := decodedControl.(*SupervisorStart)
	if !ok {
		t.Fatalf("decoded type=%T", decodedControl)
	}
	if decoded.SessionID != original.SessionID || decoded.Terminal != original.Terminal || decoded.Argv[2] != original.Argv[2] {
		t.Fatalf("decoded=%+v, want=%+v", decoded, original)
	}
}

func TestStrictControlRejectsUnknownAndTrailingJSON(t *testing.T) {
	t.Parallel()

	unknown := []byte(`{"rows":24,"columns":80,"ambientTheme":"dark"}`)
	if _, err := DecodeControl(TypeResize, unknown); !errors.Is(err, ErrInvalidControl) || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error=%v", err)
	}

	trailing := []byte(`{"rows":24,"columns":80} {"rows":25,"columns":81}`)
	if _, err := DecodeControl(TypeResize, trailing); !errors.Is(err, ErrInvalidControl) || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("trailing-data error=%v", err)
	}
}

func TestStrictControlRejectsInvalidResizeAndUnsafeText(t *testing.T) {
	t.Parallel()

	invalidResize, err := json.Marshal(Resize{Rows: 0, Columns: 80})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeControl(TypeResize, invalidResize); !errors.Is(err, ErrInvalidControl) {
		t.Fatalf("resize error=%v", err)
	}

	unsafeNotice, err := json.Marshal(Notice{Code: "session.notice", Summary: "safe\x1b[2Jnot-safe"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeControl(TypeNotice, unsafeNotice); !errors.Is(err, ErrInvalidControl) {
		t.Fatalf("unsafe notice error=%v", err)
	}
}

func TestEmptyControlMustHaveNoPayload(t *testing.T) {
	t.Parallel()

	if _, err := DecodeControl(TypeCancel, nil); err != nil {
		t.Fatalf("empty cancel rejected: %v", err)
	}
	if _, err := DecodeControl(TypeCancel, []byte(`{}`)); !errors.Is(err, ErrInvalidControl) {
		t.Fatalf("non-empty cancel error=%v", err)
	}
}

func TestRunRequestMetadataRequiresOneJSONObject(t *testing.T) {
	t.Parallel()

	request := &RunRequestMetadata{
		Schema:    RunRequestSchema,
		RequestID: "req_test",
		Request:   json.RawMessage(`{"profile":"privacy","argv":["true"]}`),
	}
	if _, err := MarshalControl(request); err != nil {
		t.Fatal(err)
	}
	request.Request = json.RawMessage(`["not-an-object"]`)
	if _, err := MarshalControl(request); !errors.Is(err, ErrInvalidControl) {
		t.Fatalf("non-object request error=%v", err)
	}
}
