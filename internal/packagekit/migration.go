package packagekit

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
)

func CheckMigrationCompatibility(manifest Manifest, existing InstallState) (MigrationDecision, error) {
	decision := MigrationDecision{
		InstalledStateSchema:    existing.Schema,
		PreviousPackageSchema:   existing.Package.Schema,
		NewPackageSchema:        manifest.Schema,
		InstalledProductVersion: existing.Package.Release.ProductVersion,
		CandidateProductVersion: manifest.Release.ProductVersion,
		AllowedInstalledSchemas: slices.Clone(manifest.Migration.FromInstalledSchemas),
		MinimumPackageSchema:    manifest.Migration.MinimumPackageSchema,
		MaximumPackageSchema:    manifest.Migration.MaximumPackageSchema,
		Guidance:                "install a compatible package or reinstall into a clean prefix",
	}
	fail := func(reason string) (MigrationDecision, error) {
		decision.Reason = reason
		return decision, fmt.Errorf("%s; installedState=%q previousPackage=%q installedVersion=%q candidateVersion=%q supportedInstalledSchemas=%v supportedPackageRange=%s..%s; hint: %s",
			reason,
			decision.InstalledStateSchema,
			decision.PreviousPackageSchema,
			decision.InstalledProductVersion,
			decision.CandidateProductVersion,
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
	installedVersion, err := semver.StrictNewVersion(existing.Package.Release.ProductVersion)
	if err != nil || installedVersion.Prerelease() == "" {
		return fail("installed product version is unpublished legacy or invalid")
	}
	candidateVersion, err := semver.StrictNewVersion(manifest.Release.ProductVersion)
	if err != nil || candidateVersion.Prerelease() == "" {
		return fail("candidate product version is invalid")
	}
	if candidateVersion.LessThan(installedVersion) {
		return fail("unsupported package downgrade")
	}
	if candidateVersion.Equal(installedVersion) &&
		(existing.Package.Release.Tag != manifest.Release.Tag ||
			existing.Package.Release.Channel != manifest.Release.Channel ||
			existing.Package.SourceCommit() != manifest.SourceCommit()) {
		return fail("same-version package identity differs from installed package")
	}
	decision.Compatible = true
	decision.Reason = "compatible"
	return decision, nil
}
