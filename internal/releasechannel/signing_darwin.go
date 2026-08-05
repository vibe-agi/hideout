//go:build darwin

package releasechannel

import (
	"bytes"
	"context"
	"encoding/json"
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
		entitlementsVerified, err := observeDeclaredDarwinEntitlements(
			ctx,
			rel,
			path,
		)
		if err != nil {
			return SigningObservation{}, err
		}
		notarized := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", "--verbose=4", "--check-notarization", "-R=notarized", path)
		_, notarizedErr := notarized.CombinedOutput()
		observation.Binaries = append(observation.Binaries, BinarySignature{
			Path: rel, Identifier: fields["Identifier"], CDHash: fields["CDHash"],
			SecureTimestamp: fields["Timestamp"] != "", HardenedRuntime: codeDirectoryHasHardenedRuntime(string(output)),
			StrictVerified: true, OnlineNotarizationValid: notarizedErr == nil,
			RequiredEntitlementsVerified: entitlementsVerified,
		})
	}
	if err := observation.Validate(true); err != nil {
		return SigningObservation{}, err
	}
	return observation, nil
}

func observeDeclaredDarwinEntitlements(
	ctx context.Context,
	relative,
	path string,
) (bool, error) {
	required := relative == darwinVirtualizationHelperPath
	var document bytes.Buffer
	var diagnostics bytes.Buffer
	inspect := exec.CommandContext(
		ctx,
		"/usr/bin/codesign",
		"-d",
		"--entitlements",
		":-",
		"--xml",
		path,
	)
	inspect.Stdout = &document
	inspect.Stderr = &diagnostics
	if err := inspect.Run(); err != nil {
		return false, fmt.Errorf(
			"codesign inspect entitlements %s: %w: %s",
			relative,
			err,
			strings.TrimSpace(diagnostics.String()),
		)
	}
	if len(bytes.TrimSpace(document.Bytes())) == 0 {
		if required {
			return false, fmt.Errorf(
				"required virtualization entitlement is absent from %s",
				relative,
			)
		}
		return false, nil
	}
	convert := exec.CommandContext(
		ctx,
		"/usr/bin/plutil",
		"-convert",
		"json",
		"-o",
		"-",
		"--",
		"-",
	)
	convert.Stdin = bytes.NewReader(document.Bytes())
	converted, err := convert.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf(
			"parse signing entitlements for %s: %w: %s",
			relative,
			err,
			strings.TrimSpace(string(converted)),
		)
	}
	var entitlements map[string]any
	if err := json.Unmarshal(converted, &entitlements); err != nil {
		return false, fmt.Errorf(
			"decode signing entitlements for %s: %w",
			relative,
			err,
		)
	}
	if !required {
		if len(entitlements) != 0 {
			return false, fmt.Errorf(
				"undeclared signing entitlements are present on %s",
				relative,
			)
		}
		return false, nil
	}
	virtualization, ok := entitlements["com.apple.security.virtualization"].(bool)
	if len(entitlements) != 1 || !ok || !virtualization {
		return false, fmt.Errorf(
			"required virtualization entitlement is invalid on %s",
			relative,
		)
	}
	return true, nil
}
