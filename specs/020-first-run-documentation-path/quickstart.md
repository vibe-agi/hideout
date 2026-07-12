# Quickstart: First-Run Documentation Path

<!-- markdownlint-disable MD013 -->

Run:

```bash
scripts/test-first-run-docs-smoke.sh
npx --yes markdownlint-cli2 README.md docs/**/*.md specs/020-first-run-documentation-path/**/*.md
```

Expected result:

- first-run docs smoke passes;
- markdownlint reports zero errors.
