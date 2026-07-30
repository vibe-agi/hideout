package environment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const (
	DisposableIdentitySchema = "hideout.disposable-identity/v1"
	RemovalIdentitySchema    = "hideout.environment-removal-identity/v1"
)

// DisposableIdentity is the immutable, versioned projection persisted by the
// lifecycle disposal protocol. Mutable run state is deliberately excluded so
// a cleanup retry cannot invalidate its own authorization.
type DisposableIdentity struct {
	Schema            string    `json:"schema"`
	EnvironmentID     string    `json:"environmentId"`
	RecordVersion     string    `json:"recordVersion"`
	Backend           string    `json:"backend"`
	Mode              Mode      `json:"mode"`
	Profile           string    `json:"profile"`
	MachineIdentityID string    `json:"machineIdentityId"`
	InstanceName      string    `json:"instanceName"`
	Disposable        bool      `json:"disposable"`
	CreatedAt         time.Time `json:"createdAt"`
	Digest            string    `json:"digest"`
}

type disposableIdentityProjection struct {
	Schema            string    `json:"schema"`
	EnvironmentID     string    `json:"environmentId"`
	RecordVersion     string    `json:"recordVersion"`
	Backend           string    `json:"backend"`
	Mode              Mode      `json:"mode"`
	Profile           string    `json:"profile"`
	MachineIdentityID string    `json:"machineIdentityId"`
	InstanceName      string    `json:"instanceName"`
	Disposable        bool      `json:"disposable"`
	CreatedAt         time.Time `json:"createdAt"`
}

// NewDisposableIdentity validates current record authority and computes the
// canonical digest used to bind a durable disposal intent. A name or status is
// never consulted as authorization.
func NewDisposableIdentity(record Record) (DisposableIdentity, error) {
	if err := record.Validate(); err != nil {
		return DisposableIdentity{}, err
	}
	if !record.Disposable || record.Mode != ModeDedicated {
		return DisposableIdentity{}, errors.New("environment record is not authorized for disposable cleanup")
	}
	if record.InstanceName == "" {
		return DisposableIdentity{}, errors.New("disposable environment instance identity is required")
	}
	projection := disposableIdentityProjection{
		Schema:            DisposableIdentitySchema,
		EnvironmentID:     record.ID,
		RecordVersion:     record.Version,
		Backend:           record.Backend,
		Mode:              record.Mode,
		Profile:           record.Profile,
		MachineIdentityID: record.MachineIdentityID,
		InstanceName:      record.InstanceName,
		Disposable:        record.Disposable,
		CreatedAt:         record.CreatedAt.UTC(),
	}
	data, err := json.Marshal(projection)
	if err != nil {
		return DisposableIdentity{}, err
	}
	sum := sha256.Sum256(data)
	return DisposableIdentity{
		Schema:            projection.Schema,
		EnvironmentID:     projection.EnvironmentID,
		RecordVersion:     projection.RecordVersion,
		Backend:           projection.Backend,
		Mode:              projection.Mode,
		Profile:           projection.Profile,
		MachineIdentityID: projection.MachineIdentityID,
		InstanceName:      projection.InstanceName,
		Disposable:        projection.Disposable,
		CreatedAt:         projection.CreatedAt,
		Digest:            hex.EncodeToString(sum[:]),
	}, nil
}

// MatchesRecord revalidates the record and compares the complete immutable
// projection, not only its digest.
func (identity DisposableIdentity) MatchesRecord(record Record) bool {
	current, err := NewDisposableIdentity(record)
	return err == nil && current == identity
}

// RemovalIdentity binds an explicitly confirmed environment removal to the
// complete immutable machine identity. Mutable run status is deliberately not
// included so the same authorization remains usable across crash recovery.
type RemovalIdentity struct {
	Schema        string `json:"schema"`
	EnvironmentID string `json:"environmentId"`
	Backend       string `json:"backend"`
	InstanceName  string `json:"instanceName"`
	Digest        string `json:"digest"`
}

type removalIdentityProjection struct {
	Schema              string             `json:"schema"`
	RecordVersion       string             `json:"recordVersion"`
	EnvironmentID       string             `json:"environmentId"`
	Name                string             `json:"name"`
	AutoNamed           bool               `json:"autoNamed"`
	ImageRef            string             `json:"imageRef"`
	Runtime             *RuntimeProvenance `json:"runtime,omitempty"`
	Profile             string             `json:"profile"`
	Backend             string             `json:"backend"`
	Mode                Mode               `json:"mode"`
	SharedSlot          string             `json:"sharedSlot,omitempty"`
	MachineIdentityID   string             `json:"machineIdentityId"`
	BootConfigurationID string             `json:"bootConfigurationId"`
	DedicatedWorkspace  string             `json:"dedicatedWorkspace,omitempty"`
	DedicatedGuestRoot  string             `json:"dedicatedGuestRoot,omitempty"`
	BoundWorkspace      string             `json:"boundWorkspace,omitempty"`
	BoundGuestRoot      string             `json:"boundGuestRoot,omitempty"`
	User                string             `json:"user,omitempty"`
	Hostname            string             `json:"hostname,omitempty"`
	InstanceName        string             `json:"instanceName"`
	Disposable          bool               `json:"disposable"`
	CreatedAt           time.Time          `json:"createdAt"`
}

// NewRemovalIdentity computes the canonical identity for an explicit clean or
// delete. Unlike NewDisposableIdentity, authority comes from the confirmed
// operator plan rather than the record's Disposable marker.
func NewRemovalIdentity(record Record) (RemovalIdentity, error) {
	if err := record.Validate(); err != nil {
		return RemovalIdentity{}, err
	}
	if record.InstanceName == "" {
		return RemovalIdentity{}, errors.New("environment removal instance identity is required")
	}
	projection := removalIdentityProjection{
		Schema:              RemovalIdentitySchema,
		RecordVersion:       record.Version,
		EnvironmentID:       record.ID,
		Name:                record.Name,
		AutoNamed:           record.AutoNamed,
		ImageRef:            record.ImageRef,
		Runtime:             record.Runtime,
		Profile:             record.Profile,
		Backend:             record.Backend,
		Mode:                record.Mode,
		SharedSlot:          record.SharedSlot,
		MachineIdentityID:   record.MachineIdentityID,
		BootConfigurationID: record.BootConfigurationID,
		DedicatedWorkspace:  record.DedicatedWorkspace,
		DedicatedGuestRoot:  record.DedicatedGuestRoot,
		BoundWorkspace:      record.BoundWorkspace,
		BoundGuestRoot:      record.BoundGuestRoot,
		User:                record.User,
		Hostname:            record.Hostname,
		InstanceName:        record.InstanceName,
		Disposable:          record.Disposable,
		CreatedAt:           record.CreatedAt.UTC(),
	}
	data, err := json.Marshal(projection)
	if err != nil {
		return RemovalIdentity{}, err
	}
	sum := sha256.Sum256(data)
	return RemovalIdentity{
		Schema:        RemovalIdentitySchema,
		EnvironmentID: record.ID,
		Backend:       record.Backend,
		InstanceName:  record.InstanceName,
		Digest:        hex.EncodeToString(sum[:]),
	}, nil
}

func (identity RemovalIdentity) MatchesRecord(record Record) bool {
	current, err := NewRemovalIdentity(record)
	return err == nil && current == identity
}
