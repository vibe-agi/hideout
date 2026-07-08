package releasecompat

import (
	"strings"
	"testing"
)

func TestCompatibilityFixtures(t *testing.T) {
	families := FixtureFamilies()
	if err := ValidateFixtureInventory(families); err != nil {
		t.Fatalf("fixture inventory invalid: %v", err)
	}
	if len(families) != len(requiredFixtureFamilies()) {
		t.Fatalf("fixture family count=%d want=%d", len(families), len(requiredFixtureFamilies()))
	}
	for _, family := range families {
		if err := ValidateFixtureVersion(family, family.CurrentVersion); err != nil {
			t.Fatalf("%s accepted fixture rejected: %v", family.Name, err)
		}
		err := ValidateFixtureVersion(family, family.UnknownVersion)
		if err == nil {
			t.Fatalf("%s unknown fixture accepted", family.Name)
		}
		if !strings.Contains(err.Error(), family.Guidance) {
			t.Fatalf("%s unknown fixture lacks guidance: %v", family.Name, err)
		}
	}
}

func TestCompatibilityInventoryRejectsMissingFamily(t *testing.T) {
	families := FixtureFamilies()
	families = families[:len(families)-1]
	if err := ValidateFixtureInventory(families); err == nil {
		t.Fatal("inventory missing a family accepted")
	}
}
