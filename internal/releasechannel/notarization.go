package releasechannel

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type appleNotaryResult struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func ObserveAcceptedNotarization(packageRoot, submissionArchive, resultPath string, observedAt time.Time) (NotarizationObservation, error) {
	if observedAt.IsZero() {
		return NotarizationObservation{}, errors.New("observedAt is required")
	}
	resultFile, err := os.Open(resultPath)
	if err != nil {
		return NotarizationObservation{}, err
	}
	data, err := io.ReadAll(io.LimitReader(resultFile, MaxJSONBytes+1))
	closeErr := resultFile.Close()
	if err != nil {
		return NotarizationObservation{}, err
	}
	if closeErr != nil {
		return NotarizationObservation{}, closeErr
	}
	if int64(len(data)) > MaxJSONBytes {
		return NotarizationObservation{}, errors.New("notary result exceeds size limit")
	}
	var result appleNotaryResult
	if err := json.Unmarshal(data, &result); err != nil {
		return NotarizationObservation{}, fmt.Errorf("parse notary result: %w", err)
	}
	if strings.ToLower(strings.TrimSpace(result.Status)) != "accepted" || strings.TrimSpace(result.ID) == "" {
		return NotarizationObservation{}, fmt.Errorf("notarization was not accepted: status=%q", result.Status)
	}
	submissionDigest, _, err := FileSHA256(submissionArchive)
	if err != nil {
		return NotarizationObservation{}, err
	}
	manifestDigest, _, err := RootedFileSHA256(packageRoot, "package-manifest.json")
	if err != nil {
		return NotarizationObservation{}, err
	}
	observation := NotarizationObservation{
		Schema: NotarizationObservationSchema, Status: "accepted",
		SubmissionID: strings.TrimSpace(result.ID), SubmissionSHA256: submissionDigest,
		PackageManifestSHA256: manifestDigest, ObservedAt: observedAt.UTC(),
		TicketMode: "online", StapleStatus: "not-applicable-tar-gz",
	}
	if err := observation.Validate(true); err != nil {
		return NotarizationObservation{}, err
	}
	return observation, nil
}
