# Security Policy

## Reporting A Vulnerability

Do not open a public issue for a suspected vulnerability or include secrets,
tokens, private paths, or unredacted evidence in an issue.

Use GitHub's private vulnerability reporting form:

<https://github.com/vibe-agi/hideout/security/advisories/new>

Include the full output of `hideout version`, the affected package digest,
the backend and host platform, the expected boundary, and the smallest safe
reproduction. Use `hideout audit export` for diagnostic evidence that must
leave the machine; review its pre-export summary before approving the export.
Control-plane redaction removes Hideout-generated credentials, not all user
data, so review the exported content before attaching it.

We will acknowledge a report when it has been received and coordinate scope,
remediation, and disclosure through the private advisory. The public alpha has
no guaranteed response-time SLA.

## Supported Versions

Once a supervised public alpha package exists, only the newest verified alpha
package is supported. Development builds and older prereleases may be useful for
reproduction but do not receive a security-support promise.

## Boundaries

The current claims and explicit non-claims are maintained in
[`docs/claim-boundaries.md`](docs/claim-boundaries.md) and
[`docs/support-matrix.md`](docs/support-matrix.md). A successful local test or
native-backend run is not release evidence for Lima isolation.
