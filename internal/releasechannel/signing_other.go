//go:build !darwin

package releasechannel

import (
	"context"
	"errors"
	"time"
)

func ObserveDarwinSigning(context.Context, string, []string, time.Time) (SigningObservation, error) {
	return SigningObservation{}, errors.New("Developer ID signing observation requires macOS")
}
