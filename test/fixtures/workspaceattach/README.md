# Workspace Attachment Fixtures

`generate.sh` creates deterministic, bounded fixtures on demand. Large trees
are not checked into Git.

- `relations`: disjoint, same and nested root markers;
- `git-10k`: one committed 10,000-file repository; and
- `package-20k`: a 20,000-file package-manager-style metadata tree.

Evidence records the generated tree digest and generator commit. Performance
comparisons must generate baseline and candidate fixtures from the same commit.
