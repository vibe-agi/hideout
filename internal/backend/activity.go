package backend

import (
	"encoding/hex"
	"errors"
	"strings"
	"unicode"

	"github.com/vibe-agi/hideout/internal/sessionwire"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

// ActivityPreparation is immutable Manager-bound identity for one observer
// generation. It contains no stream credential; the daemon callback mints that
// authority immediately before the supervisor start frame is written.
type ActivityPreparation struct {
	Owner                workloadtypes.ActivityOwner
	SessionID            string
	EnvironmentID        string
	Backend              string
	BackendIncarnationID string
	GuestBootID          string
	ObserverGeneration   uint64
	ObserverHelperDigest string
	Retention            workloadtypes.ActivityRetentionPolicy
}

func (preparation ActivityPreparation) Validate() error {
	if err := preparation.Owner.Validate(); err != nil {
		return err
	}
	if !validActivityOpaque(preparation.SessionID, "ses_", 128) ||
		!validActivityOpaque(preparation.EnvironmentID, "env_", 128) ||
		!validActivityText(preparation.Backend, 32) ||
		!validActivityText(preparation.BackendIncarnationID, 256) ||
		!validActivityText(preparation.GuestBootID, 256) ||
		preparation.ObserverGeneration == 0 ||
		preparation.Retention.Validate() != nil {
		return errors.New("activity preparation identity is invalid")
	}
	if preparation.Owner.Backend != preparation.Backend ||
		preparation.Owner.BackendIncarnationID != preparation.BackendIncarnationID {
		return errors.New("activity preparation owner identity drift")
	}
	switch preparation.Owner.Kind {
	case workloadtypes.OwnerReusableEnvironment:
		if preparation.Owner.EnvironmentID != preparation.EnvironmentID {
			return errors.New("activity preparation environment identity drift")
		}
	case workloadtypes.OwnerDisposableSession:
		if preparation.Owner.SessionID != preparation.SessionID {
			return errors.New("activity preparation session identity drift")
		}
	default:
		return errors.New("activity preparation owner kind is invalid")
	}
	digest := strings.TrimPrefix(preparation.ObserverHelperDigest, "sha256:")
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 ||
		preparation.ObserverHelperDigest != "sha256:"+strings.ToLower(digest) {
		return errors.New("activity preparation observer helper digest is invalid")
	}
	return nil
}

// ActivityStreams is the daemon-owned observer lifecycle attached to one
// backend run. Backends must call BoundaryReady before Ready, must deliver
// every decoded envelope (including duplicates), and must call SessionClosed
// on every path after Prepare succeeds.
type ActivityStreams struct {
	Prepare        func(ActivityPreparation) (sessionwire.SupervisorActivityExpectation, error)
	BoundaryReady  func(*sessionwire.SupervisorActivityReady) error
	Observe        func(sessionwire.ObservationEnvelope) error
	ObserveBatch   func([]sessionwire.ObservationEnvelope) error
	ObserverClosed func(error) error
	SessionClosed  func(*sessionwire.SupervisorActivityCompletion, error) error
}

func (streams *ActivityStreams) Validate() error {
	if streams == nil || streams.Prepare == nil || streams.BoundaryReady == nil ||
		streams.Observe == nil || streams.ObserverClosed == nil ||
		streams.SessionClosed == nil {
		return errors.New("activity stream callbacks are incomplete")
	}
	return nil
}

func validActivityText(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validActivityOpaque(value, prefix string, maximum int) bool {
	if !strings.HasPrefix(value, prefix) || !validActivityText(value, maximum) {
		return false
	}
	for _, character := range value[len(prefix):] {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return len(value) > len(prefix)
}
