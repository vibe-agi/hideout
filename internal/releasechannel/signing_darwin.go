//go:build darwin

package releasechannel

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var signingLineRE = regexp.MustCompile(`(?m)^([A-Za-z][A-Za-z ]+)=([^\r\n]+)$`)

// ObserveDarwinSigning independently examines every declared Mach-O path.
// The paths must come from package inventory, not package-provided identities.
func ObserveDarwinSigning(ctx context.Context, root string, paths []string, now time.Time) (SigningObservation, error) {
	if len(paths) == 0 {
		return SigningObservation{}, errors.New("at least one Mach-O path is required")
	}
	manifestDigest, _, err := RootedFileSHA256(root, "package-manifest.json")
	if err != nil {
		return SigningObservation{}, err
	}
	observation := SigningObservation{Schema: SigningObservationSchema, Status: "developer-id-verified", ObservedAt: now.UTC(), HostOS: "darwin", PackageManifestSHA256: manifestDigest}
	for _, rel := range paths {
		if err := ValidateRelativePath(rel); err != nil {
			return SigningObservation{}, err
		}
		path := filepath.Join(root, rel)
		verify := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", "--verbose=4", path)
		if output, err := verify.CombinedOutput(); err != nil {
			return SigningObservation{}, fmt.Errorf("codesign verify %s: %w: %s", rel, err, strings.TrimSpace(string(output)))
		}
		detail := exec.CommandContext(ctx, "/usr/bin/codesign", "-dvvv", path)
		output, err := detail.CombinedOutput()
		if err != nil {
			return SigningObservation{}, fmt.Errorf("codesign inspect %s: %w", rel, err)
		}
		fields := map[string]string{}
		for _, match := range signingLineRE.FindAllStringSubmatch(string(output), -1) {
			key := strings.TrimSpace(match[1])
			if _, exists := fields[key]; !exists {
				fields[key] = strings.TrimSpace(match[2])
			}
		}
		authority := fields["Authority"]
		team := fields["TeamIdentifier"]
		if observation.TeamID == "" {
			observation.TeamID, observation.CommonName = team, authority
		} else if observation.TeamID != team || observation.CommonName != authority {
			return SigningObservation{}, errors.New("package Mach-O signing identities differ")
		}
		notarized := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", "--verbose=4", "--check-notarization", "-R=notarized", path)
		_, notarizedErr := notarized.CombinedOutput()
		observation.Binaries = append(observation.Binaries, BinarySignature{
			Path: rel, Identifier: fields["Identifier"], CDHash: fields["CDHash"],
			SecureTimestamp: fields["Timestamp"] != "", HardenedRuntime: codeDirectoryHasHardenedRuntime(string(output)),
			StrictVerified: true, OnlineNotarizationValid: notarizedErr == nil,
		})
	}
	if err := observation.Validate(true); err != nil {
		return SigningObservation{}, err
	}
	return observation, nil
}
