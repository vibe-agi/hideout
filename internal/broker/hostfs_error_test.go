package broker

import "testing"

func TestHostFSErrorVocabularyAndSchema(t *testing.T) {
	schema := compileBrokerEnvelopeSchema(t)
	tests := []struct {
		code        string
		errno       string
		retryable   bool
		decisionRef string
	}{
		{HostFSErrorPathHidden, ErrnoENOENT, false, ""},
		{HostFSErrorReadApprovalRequired, ErrnoEACCES, true, "dec_hfr_abc"},
		{HostFSErrorReadDenied, ErrnoEACCES, false, ""},
		{HostFSErrorReadRequestLimited, ErrnoEACCES, true, ""},
		{HostFSErrorDirectoryNotEnumerable, ErrnoEACCES, false, ""},
		{HostFSErrorDirectoryIncomplete, ErrnoEOVERFLOW, false, ""},
		{HostFSErrorWriteUnauthorized, ErrnoEACCES, false, ""},
		{HostFSErrorHostPrerequisiteFailed, ErrnoEIO, false, ""},
		{HostFSErrorOperationUnsupported, ErrnoEROFS, false, ""},
		{HostFSErrorBrokerUnavailable, ErrnoEIO, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			record, err := NewErrorRecord(tt.code, tt.decisionRef, nil)
			if err != nil {
				t.Fatalf("NewErrorRecord: %v", err)
			}
			if record.Errno != tt.errno || record.Retryable != tt.retryable {
				t.Fatalf("unexpected record: %+v", record)
			}
			resp := Response{ID: "req_hostfs", Decision: "deny", Status: "denied", ExitCode: 126, Stderr: "human context", Error: record}
			if err := validateBrokerEnvelope(schema, resp); err != nil {
				t.Fatalf("typed response should match schema: %v", err)
			}
		})
	}
}

func TestHostFSErrorValidationRejectsAuthorityDrift(t *testing.T) {
	one := int64(1)
	tests := []ErrorRecord{
		{Code: "hostfs.unknown", Errno: ErrnoEIO},
		{Code: HostFSErrorPathHidden, Errno: ErrnoEACCES},
		{Code: HostFSErrorReadDenied, Errno: ErrnoEACCES, Retryable: true},
		{Code: HostFSErrorReadApprovalRequired, Errno: ErrnoEACCES, Retryable: true},
		{Code: HostFSErrorReadDenied, Errno: ErrnoEACCES, DecisionRef: "dec_hfr_bad"},
		{Code: HostFSErrorPathHidden, Errno: ErrnoENOENT, RetryAfterMS: &one},
	}
	for _, record := range tests {
		if err := ValidateErrorRecord(&record); err == nil {
			t.Fatalf("expected validation failure for %+v", record)
		}
	}

	tooLarge := int64(60_001)
	if _, err := NewErrorRecord(HostFSErrorReadRequestLimited, "", &tooLarge); err == nil {
		t.Fatal("expected oversized retryAfterMs to fail")
	}
}
