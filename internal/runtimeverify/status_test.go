package runtimeverify

import (
	"testing"

	"github.com/vibe-agi/hideout/internal/environment"
)

func TestBuildStatusNeverPromotesCustomStoppedOrMismatchedEnvironment(t *testing.T) {
	provenance := validProvenance()
	record := environment.Record{
		ID: validReceipt().EnvironmentID, ImageRef: provenance.ImageRef(), Runtime: &provenance, Backend: "lima",
		LastSessionID: validReceipt().SessionID, LastStartedAt: validReceipt().ObservedAt,
	}
	ready := validReceipt()
	ready.Results[0].Present = true
	ready.Results[0].Matched = true
	ready.Results[0].Reason = "ok"
	ready.FailedIDs = nil
	ready.RecoveryCode = ""
	ready.Status = StatusPreviewReady

	cases := []struct {
		name    string
		record  environment.Record
		running bool
		receipt *Receipt
		want    string
	}{
		{name: "ready", record: record, running: true, receipt: &ready, want: StatusPreviewReady},
		{name: "stopped", record: record, running: false, receipt: &ready, want: StatusNotRunning},
		{name: "missing", record: record, running: true, want: StatusUnknown},
		{name: "custom", record: environment.Record{ID: record.ID, ImageRef: record.ImageRef}, running: true, receipt: &ready, want: StatusCustomUnverified},
		{name: "image mismatch", record: record, running: true, receipt: func() *Receipt { r := ready; r.ImageRef = environment.BuiltinBaseImage; return &r }(), want: StatusUnknown},
		{name: "native", record: record, running: true, receipt: func() *Receipt { r := ready; r.Backend = "native"; r.BackendReal = false; return &r }(), want: StatusUnknown},
		{name: "malformed receipt", record: record, running: true, receipt: func() *Receipt { r := ready; r.Schema = "broken"; return &r }(), want: StatusUnknown},
		{name: "stale session", record: func() environment.Record { r := record; r.LastSessionID = "ses_new"; return r }(), running: true, receipt: &ready, want: StatusUnknown},
		{name: "forged native ready", record: record, running: true, receipt: func() *Receipt {
			r := ready
			r.Backend = "native"
			r.BackendReal = false
			r.Status = StatusPreviewReady
			return &r
		}(), want: StatusUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := BuildStatus(tc.record, tc.running, tc.receipt)
			if view.Status != tc.want {
				t.Fatalf("status=%q want %q view=%+v", view.Status, tc.want, view)
			}
		})
	}
}
