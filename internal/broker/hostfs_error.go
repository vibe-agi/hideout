package broker

import (
	"errors"
	"fmt"
	"strings"
)

const (
	HostFSErrorPathHidden             = "hostfs.path.hidden"
	HostFSErrorReadApprovalRequired   = "hostfs.read.approval-required"
	HostFSErrorReadDenied             = "hostfs.read.denied"
	HostFSErrorReadRequestLimited     = "hostfs.read.request-limited"
	HostFSErrorDirectoryNotEnumerable = "hostfs.directory.not-enumerable"
	HostFSErrorDirectoryIncomplete    = "hostfs.directory.incomplete"
	HostFSErrorWriteUnauthorized      = "hostfs.write.unauthorized"
	HostFSErrorHostPrerequisiteFailed = "hostfs.host.prerequisite-failed"
	HostFSErrorOperationUnsupported   = "hostfs.operation.unsupported"
	HostFSErrorBrokerUnavailable      = "broker.unavailable"
)

const (
	ErrnoENOENT    = "ENOENT"
	ErrnoEACCES    = "EACCES"
	ErrnoEOVERFLOW = "EOVERFLOW"
	ErrnoEIO       = "EIO"
	ErrnoEROFS     = "EROFS"
)

type ErrorRecord struct {
	Code         string `json:"code"`
	Errno        string `json:"errno"`
	Retryable    bool   `json:"retryable"`
	DecisionRef  string `json:"decisionRef,omitempty"`
	RetryAfterMS *int64 `json:"retryAfterMs,omitempty"`
}

type hostFSErrorSpec struct {
	errno             string
	retryable         bool
	requireDecision   bool
	allowRetryAfterMS bool
}

var hostFSErrorSpecs = map[string]hostFSErrorSpec{
	HostFSErrorPathHidden:             {errno: ErrnoENOENT},
	HostFSErrorReadApprovalRequired:   {errno: ErrnoEACCES, retryable: true, requireDecision: true},
	HostFSErrorReadDenied:             {errno: ErrnoEACCES},
	HostFSErrorReadRequestLimited:     {errno: ErrnoEACCES, retryable: true, allowRetryAfterMS: true},
	HostFSErrorDirectoryNotEnumerable: {errno: ErrnoEACCES},
	HostFSErrorDirectoryIncomplete:    {errno: ErrnoEOVERFLOW},
	HostFSErrorWriteUnauthorized:      {errno: ErrnoEACCES},
	HostFSErrorHostPrerequisiteFailed: {errno: ErrnoEIO},
	HostFSErrorOperationUnsupported:   {errno: ErrnoEROFS},
	HostFSErrorBrokerUnavailable:      {errno: ErrnoEIO, retryable: true, allowRetryAfterMS: true},
}

func NewErrorRecord(code, decisionRef string, retryAfterMS *int64) (*ErrorRecord, error) {
	spec, ok := hostFSErrorSpecs[strings.TrimSpace(code)]
	if !ok {
		return nil, fmt.Errorf("unsupported HostFS error code %q", code)
	}
	record := &ErrorRecord{
		Code:         code,
		Errno:        spec.errno,
		Retryable:    spec.retryable,
		DecisionRef:  strings.TrimSpace(decisionRef),
		RetryAfterMS: retryAfterMS,
	}
	if err := ValidateErrorRecord(record); err != nil {
		return nil, err
	}
	return record, nil
}

func ValidateErrorRecord(record *ErrorRecord) error {
	if record == nil {
		return errors.New("typed HostFS error is required")
	}
	spec, ok := hostFSErrorSpecs[strings.TrimSpace(record.Code)]
	if !ok {
		return fmt.Errorf("unsupported HostFS error code %q", record.Code)
	}
	if record.Errno != spec.errno {
		return fmt.Errorf("HostFS error code %q requires errno %s", record.Code, spec.errno)
	}
	if record.Retryable != spec.retryable {
		return fmt.Errorf("HostFS error code %q requires retryable=%t", record.Code, spec.retryable)
	}
	decisionRef := strings.TrimSpace(record.DecisionRef)
	if spec.requireDecision && decisionRef == "" {
		return fmt.Errorf("HostFS error code %q requires decisionRef", record.Code)
	}
	if !spec.requireDecision && decisionRef != "" {
		return fmt.Errorf("HostFS error code %q forbids decisionRef", record.Code)
	}
	if record.RetryAfterMS != nil {
		if !spec.allowRetryAfterMS {
			return fmt.Errorf("HostFS error code %q forbids retryAfterMs", record.Code)
		}
		if *record.RetryAfterMS < 1 || *record.RetryAfterMS > 60_000 {
			return errors.New("retryAfterMs must be between 1 and 60000")
		}
	}
	return nil
}
