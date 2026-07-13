# Contributing To Hideout

Hideout accepts focused bug fixes, tests, documentation corrections, command
adapters, and host-app recipe proposals. Start with an issue when a change
alters a public contract, authority boundary, persistent schema, or release
claim.

## Development

Use Go 1.25 and run:

```bash
go build ./...
go vet ./...
go test ./...
scripts/test-gate0.sh
```

Run `gofmt` on changed Go files and `git diff --check` before opening a pull
request. Feature work follows the repository's `specs/` artifacts and must
keep requirements, implementation, tests, and documentation consistent.

## Security And Authority

- Go Core owns authorization, validation, redaction floors, and host effects.
- JavaScript and community recipes may classify or propose only capabilities
  that Core already exposes; they cannot introduce a new host effect.
- New outward evidence must have deterministic redaction and a real proof.
- Do not replace real Lima gates with native/local substitutes.
- Do not add generic host command execution or implicit fallback authority.

Report vulnerabilities privately according to [`SECURITY.md`](SECURITY.md).

By contributing, you agree that your contribution is licensed under the
Apache License 2.0.
