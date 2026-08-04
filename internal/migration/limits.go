package migration

import "fmt"

const (
	HardMaxEnvironments   uint32 = 32
	HardMaxLogicalBytes   uint64 = 4 << 40
	HardMaxPayloadRecords uint64 = 1 << 20
	HardMaxChunkBytes     uint32 = 4 << 20
	HardMaxManifestBytes  uint32 = 16 << 20
	HardMaxMetadataBytes  uint32 = 1 << 20
	HardMaxWorkingBytes   uint64 = 256 << 20
	HardMaxHeaderBytes    uint32 = 64 << 10
	HardMaxRecordOverhead uint32 = 64 << 10
	HardMaxArgonMemoryKiB uint32 = 256 << 10
	HardMaxArgonPasses    uint32 = 10
	HardMaxArgonLanes     uint8  = 8
)

// Limits is the authenticated v1 limit profile. Readers always enforce both
// this profile and the compiled hard maxima.
type Limits struct {
	MaxEnvironments   uint32 `json:"maxEnvironments"`
	MaxLogicalBytes   uint64 `json:"maxLogicalBytes"`
	MaxPayloadRecords uint64 `json:"maxPayloadRecords"`
	MaxChunkBytes     uint32 `json:"maxChunkBytes"`
	MaxManifestBytes  uint32 `json:"maxManifestBytes"`
	MaxMetadataBytes  uint32 `json:"maxMetadataBytes"`
	MaxWorkingBytes   uint64 `json:"maxWorkingBytes"`
}

// DefaultLimits returns the supported v1 envelope.
func DefaultLimits() Limits {
	return Limits{
		MaxEnvironments:   HardMaxEnvironments,
		MaxLogicalBytes:   HardMaxLogicalBytes,
		MaxPayloadRecords: HardMaxPayloadRecords,
		MaxChunkBytes:     HardMaxChunkBytes,
		MaxManifestBytes:  HardMaxManifestBytes,
		MaxMetadataBytes:  HardMaxMetadataBytes,
		MaxWorkingBytes:   HardMaxWorkingBytes,
	}
}

// Validate rejects zero or locally unsupported limit profiles before any
// allocation, decompression, or key derivation.
func (limits Limits) Validate() error {
	for label, value := range map[string]uint64{
		"environments":    uint64(limits.MaxEnvironments),
		"logical bytes":   limits.MaxLogicalBytes,
		"payload records": limits.MaxPayloadRecords,
		"chunk bytes":     uint64(limits.MaxChunkBytes),
		"manifest bytes":  uint64(limits.MaxManifestBytes),
		"metadata bytes":  uint64(limits.MaxMetadataBytes),
		"working bytes":   limits.MaxWorkingBytes,
	} {
		if value == 0 {
			return fmt.Errorf("%w: %s limit is zero", ErrInvalidBundle, label)
		}
	}
	if limits.MaxEnvironments > HardMaxEnvironments ||
		limits.MaxLogicalBytes > HardMaxLogicalBytes ||
		limits.MaxPayloadRecords > HardMaxPayloadRecords ||
		limits.MaxChunkBytes > HardMaxChunkBytes ||
		limits.MaxManifestBytes > HardMaxManifestBytes ||
		limits.MaxMetadataBytes > HardMaxMetadataBytes ||
		limits.MaxWorkingBytes > HardMaxWorkingBytes {
		return fmt.Errorf("%w: declared limit profile exceeds v1 hard maximum", ErrLimitExceeded)
	}
	return nil
}
