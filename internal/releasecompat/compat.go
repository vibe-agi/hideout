package releasecompat

import (
	"fmt"
	"strings"
)

type FixtureFamily struct {
	Name           string
	CurrentVersion string
	UnknownVersion string
	Guidance       string
}

func FixtureFamilies() []FixtureFamily {
	return []FixtureFamily{
		{Name: "profile", CurrentVersion: "hideout.profile/v1", UnknownVersion: "hideout.profile/v99", Guidance: "recreate or migrate the profile"},
		{Name: "package-manifest", CurrentVersion: "hideout.package-manifest/v1", UnknownVersion: "hideout.package-manifest/v99", Guidance: "install a compatible package"},
		{Name: "adapter-pack-manifest", CurrentVersion: "hideout.adapter-pack-manifest/v1", UnknownVersion: "hideout.adapter-pack-manifest/v99", Guidance: "upgrade or reinstall the adapter pack"},
		{Name: "adapter-pack-registry", CurrentVersion: "hideout.adapter-pack-registry/v1", UnknownVersion: "hideout.adapter-pack-registry/v99", Guidance: "rebuild the adapter pack registry"},
		{Name: "doctor-report", CurrentVersion: "hideout.doctor-report/v1", UnknownVersion: "hideout.doctor-report/v99", Guidance: "regenerate the doctor report"},
		{Name: "export-artifact", CurrentVersion: "hideout.export-artifact/v1", UnknownVersion: "hideout.export-artifact/v99", Guidance: "regenerate the export artifact"},
		{Name: "decision-record", CurrentVersion: "hideout.decision-record/v1", UnknownVersion: "hideout.decision-record/v99", Guidance: "recreate the decision through Manager"},
		{Name: "notice-record", CurrentVersion: "hideout.notice-record/v1", UnknownVersion: "hideout.notice-record/v99", Guidance: "recreate the notice through Manager"},
		{Name: "hostfs-write-decision", CurrentVersion: "hideout.hostfs-write-decision/v1", UnknownVersion: "hideout.hostfs-write-decision/v99", Guidance: "discard and restage the HostFS write"},
		{Name: "hostfs-write-event", CurrentVersion: "hideout.hostfs-write-event/v1", UnknownVersion: "hideout.hostfs-write-event/v99", Guidance: "discard and restage the HostFS write"},
		{Name: "onboarding-evidence", CurrentVersion: "hideout.onboarding-evidence/v1", UnknownVersion: "hideout.onboarding-evidence/v99", Guidance: "rerun onboarding"},
		{Name: "daemon-status", CurrentVersion: "hideout.daemon-status/v1", UnknownVersion: "hideout.daemon-status/v99", Guidance: "restart the daemon with a compatible binary"},
		{Name: "daemon-event", CurrentVersion: "hideout.daemon-event/v1", UnknownVersion: "hideout.daemon-event/v99", Guidance: "reconnect to a compatible daemon"},
		{Name: "live-console-seed", CurrentVersion: "hideout.live-console-seed/v1", UnknownVersion: "hideout.live-console-seed/v99", Guidance: "refresh the live console seed"},
		{Name: "run-result", CurrentVersion: "hideout.run-result/v1", UnknownVersion: "hideout.run-result/v99", Guidance: "rerun the command"},
		{Name: "init-plan", CurrentVersion: "hideout.init/v1", UnknownVersion: "hideout.init/v99", Guidance: "regenerate the init plan"},
		{Name: "init-audit", CurrentVersion: "hideout.init-audit/v1", UnknownVersion: "hideout.init-audit/v99", Guidance: "regenerate init audit evidence"},
	}
}

func ValidateFixtureVersion(family FixtureFamily, version string) error {
	version = strings.TrimSpace(version)
	if version == family.CurrentVersion {
		return nil
	}
	if strings.TrimSpace(family.Guidance) == "" {
		return fmt.Errorf("%s fixture version %q is unsupported", family.Name, version)
	}
	return fmt.Errorf("%s fixture version %q is unsupported; %s", family.Name, version, family.Guidance)
}

func ValidateFixtureInventory(families []FixtureFamily) error {
	if len(families) == 0 {
		return fmt.Errorf("fixture inventory is empty")
	}
	seen := map[string]bool{}
	for _, family := range families {
		if strings.TrimSpace(family.Name) == "" {
			return fmt.Errorf("fixture family missing name")
		}
		if seen[family.Name] {
			return fmt.Errorf("duplicate fixture family %q", family.Name)
		}
		seen[family.Name] = true
		if strings.TrimSpace(family.CurrentVersion) == "" || strings.TrimSpace(family.UnknownVersion) == "" {
			return fmt.Errorf("%s fixture family needs accepted and rejected versions", family.Name)
		}
		if err := ValidateFixtureVersion(family, family.CurrentVersion); err != nil {
			return fmt.Errorf("%s accepted fixture failed: %w", family.Name, err)
		}
		if err := ValidateFixtureVersion(family, family.UnknownVersion); err == nil {
			return fmt.Errorf("%s unknown fixture unexpectedly accepted", family.Name)
		}
	}
	for _, name := range requiredFixtureFamilies() {
		if !seen[name] {
			return fmt.Errorf("missing fixture family %q", name)
		}
	}
	return nil
}

func requiredFixtureFamilies() []string {
	return []string{
		"profile",
		"package-manifest",
		"adapter-pack-manifest",
		"adapter-pack-registry",
		"doctor-report",
		"export-artifact",
		"decision-record",
		"notice-record",
		"hostfs-write-decision",
		"hostfs-write-event",
		"onboarding-evidence",
		"daemon-status",
		"daemon-event",
		"live-console-seed",
		"run-result",
		"init-plan",
		"init-audit",
	}
}
