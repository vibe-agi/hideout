package packagekit

import (
	"fmt"
	"slices"
	"strings"
)

func CheckMigrationCompatibility(manifest Manifest, existing InstallState) (MigrationDecision, error) {
	decision := MigrationDecision{
		InstalledStateSchema:    existing.Schema,
		PreviousPackageSchema:   existing.Package.Schema,
		NewPackageSchema:        manifest.Schema,
		AllowedInstalledSchemas: slices.Clone(manifest.Migration.FromInstalledSchemas),
		MinimumPackageSchema:    manifest.Migration.MinimumPackageSchema,
		MaximumPackageSchema:    manifest.Migration.MaximumPackageSchema,
		Guidance:                "install a compatible package or reinstall into a clean prefix",
	}
	fail := func(reason string) (MigrationDecision, error) {
		decision.Reason = reason
		return decision, fmt.Errorf("%s; installedState=%q previousPackage=%q supportedInstalledSchemas=%v supportedPackageRange=%s..%s; hint: %s",
			reason,
			decision.InstalledStateSchema,
			decision.PreviousPackageSchema,
			decision.AllowedInstalledSchemas,
			decision.MinimumPackageSchema,
			decision.MaximumPackageSchema,
			decision.Guidance,
		)
	}
	if strings.TrimSpace(existing.Schema) == "" {
		return fail("installed package schema is missing")
	}
	if !slices.Contains(manifest.Migration.FromInstalledSchemas, existing.Schema) {
		return fail("installed package schema is outside migration range")
	}
	if strings.TrimSpace(existing.Package.Schema) == "" {
		return fail("previous package schema is missing")
	}
	minSchema := strings.TrimSpace(manifest.Migration.MinimumPackageSchema)
	maxSchema := strings.TrimSpace(manifest.Migration.MaximumPackageSchema)
	if minSchema == "" || maxSchema == "" {
		return fail("package schema migration range is incomplete")
	}
	if existing.Package.Schema < minSchema || existing.Package.Schema > maxSchema {
		return fail("previous package schema is outside migration range")
	}
	decision.Compatible = true
	decision.Reason = "compatible"
	return decision, nil
}
