# Data Model: Profile Templates And Onboarding

<!-- markdownlint-disable MD013 -->

## Profile Template

Built-in Go definition for one first-run posture.

Fields:

- `id`: `privacy`, `hardened`, `dev`, or `debug`.
- `recommended`: true only for `privacy`.
- `description`: short operator-facing explanation.
- `networkPosture`: `privacy-mediated`, `direct-dev`, or `debug-local`.
- `hostfsPosture`: always `none-by-default` in v1.
- `adapterPackPosture`: always `none-by-default` in v1.
- `requiresPrivilege`: `enforced` for `hardened`, otherwise `not-required`.
- `warnings`: weaker or degraded posture messages.
- `nonClaims`: explicit statements such as no guest-root containment for
  privacy/dev/debug and no hardened claim for degraded fallback.
- `followUps`: commands for adding HostFS grants, adapter packs, or doctor
  checks after onboarding.

Validation:

- id must be one of the four built-ins;
- no template may include HostFS grants or command adapter bindings by default;
- every rendered profile must pass `profile.Profile.Validate()`;
- every template must render deterministic evidence.

## Onboarding Request

Operator choices passed to the Go-owned init planner.

Fields:

- `profileName`
- `templateID`
- `backend`
- `network`
- `proxySecretRef`
- `mediatedResolver`
- `privilegeStatus`: `enforced`, `degraded`, or `unknown`
- `privilegeReason`
- `allowDegradedTemplate`
- `interactive`
- `confirmed`
- `dryRun`

Validation:

- `--no-input` requires explicit profile, template, backend, and network flags;
- `privacy` and `hardened` require `network=tun2socks`, proxy secret ref, and
  mediated resolver;
- `tun2socks` requires proxy secret ref and mediated resolver;
- `direct` rejects proxy secret ref and mediated resolver;
- hardened requires `privilegeStatus=enforced`;
- degraded hardened fallback requires `allowDegradedTemplate=true` and must be
  marked in metadata/evidence;
- profile collisions fail before mutation.

## Privilege Fact

The privilege separation fact consumed by onboarding.

Fields:

- `status`: `enforced`, `degraded`, or `unknown`.
- `reason`
- `guidance`
- `source`: `observed`, `injected-test`, or `operator-declared`.

Rules:

- `enforced` allows hardened.
- `degraded` and `unknown` block hardened unless degraded fallback is explicit.
- non-hardened templates may continue with warnings and non-claims.

## Generated Profile

Existing `profile.Profile` rendered from `profile.Default(name)` and then
adjusted by the selected template/request.

Template metadata keys:

- `templateId`
- `templatePosture`
- `templateRecommended`
- `templatePrivilegeStatus`
- `templatePrivilegeRequirement`
- `templateDegraded`
- `templateCreatedAt`

Rules:

- HostFS grants remain empty.
- Command adapters remain empty.
- Adapter-pack metadata is absent.
- `privacy` and `hardened` set `network.mode=tun2socks`, `proxySecretRef`, and
  `mediatedResolver`.
- Profile metadata must not store proxy secret values or generated machine ids
  outside existing profile identity metadata.

## Onboarding Plan

Extended init plan returned by `manager.PlanInit`.

Fields:

- existing InitTask plan fields;
- `templateId`
- `templateSummary`
- `privilegeStatus`
- `warnings`
- `nonClaims`
- `evidencePath`
- `requiresConfirmation`

Rules:

- plan construction fails before tasks when required choices are missing;
- blocked hardened plans are not applyable;
- review output is derived from this plan.

## Onboarding Evidence Summary

Durable JSON written on successful apply.

Fields:

- `version`: `hideout.onboarding-evidence/v1`
- `time`
- `profile`
- `template`
- `backend`
- `network`
- `proxySecretRef`: redacted ref label, never secret value
- `mediatedResolver`
- `hostfsPosture`
- `adapterPackPosture`
- `privilege`
- `warnings`
- `nonClaims`
- `profilePath`
- `initAuditPath`
- `nextSteps`

Rules:

- only written after successful profile/init apply;
- not written on cancellation or confirmation failure;
- deterministic control-plane redaction runs before write;
- schema validation is part of Gate 0 smoke.
