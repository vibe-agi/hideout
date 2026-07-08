package doctor

import (
	"bytes"
	"strings"
	"testing"
)

func TestReportSummaryAndRedaction(t *testing.T) {
	b := NewBuilder(Request{Profile: "default", Backend: "native"})
	b.Add("store", "store", "ok", "writable")
	b.Add("proxy", "network", "error", "HIDEOUT_SECRET_PROXY=socks5://example cap_0123456789abcdef0123456789abcdef", WithDetails(map[string]any{
		"machineId": "0123456789abcdef0123456789abcdef",
		"keep":      "value",
	}))
	report := b.Report()
	if !report.Summary.Failed || report.Summary.ExitCode != 1 {
		t.Fatalf("summary did not fail on required error: %+v", report.Summary)
	}
	data := new(bytes.Buffer)
	if err := WriteJSON(data, report); err != nil {
		t.Fatal(err)
	}
	text := data.String()
	for _, leak := range []string{"HIDEOUT_SECRET_PROXY", "cap_0123456789abcdef0123456789abcdef", "0123456789abcdef0123456789abcdef"} {
		if strings.Contains(text, leak) {
			t.Fatalf("report leaked %s:\n%s", leak, text)
		}
	}
	if !strings.Contains(text, `"keep": "value"`) {
		t.Fatalf("report removed user data unexpectedly:\n%s", text)
	}
}
