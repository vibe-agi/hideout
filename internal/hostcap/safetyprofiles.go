package hostcap

import (
	_ "embed"
	"errors"

	"github.com/vibe-agi/hideout/internal/hostcap/appopen"
)

// safetyProfilesJSON is reviewed Core package data. Community packs may request
// an ID but cannot supply profile contents or register another profile.
//
//go:embed recipes/safety-profiles.json
var safetyProfilesJSON []byte

func init() {
	if _, err := loadCoreSafetyProfiles(safetyProfilesJSON); err != nil {
		panic("hostcap: invalid embedded safety profiles: " + err.Error())
	}
}

func loadCoreSafetyProfiles(raw []byte) ([]appopen.SafetyProfile, error) {
	return appopen.DecodeSafetyProfileCatalog(raw)
}

// CoreSafetyProfiles returns a deep copy of the closed reviewed catalog.
func CoreSafetyProfiles() []appopen.SafetyProfile {
	profiles, err := loadCoreSafetyProfiles(safetyProfilesJSON)
	if err != nil {
		panic("hostcap: embedded safety profile changed after initialization: " + err.Error())
	}
	return profiles
}

func SelectCoreSafetyProfile(requestedID string, identity appopen.SafetyIdentity) (appopen.SafetyProfile, error) {
	return appopen.SelectSafetyProfile(CoreSafetyProfiles(), requestedID, identity)
}

// CoreSafetyProfile returns reviewed profile metadata without claiming that it
// is compatible with an application. Runtime compatibility still requires
// SelectCoreSafetyProfile with a Core-observed identity.
func CoreSafetyProfile(requestedID string) (appopen.SafetyProfile, error) {
	for _, profile := range CoreSafetyProfiles() {
		if profile.ID == requestedID {
			return profile, nil
		}
	}
	return appopen.SafetyProfile{}, errors.New("hostcap: requested safety profile is not Core-owned")
}
