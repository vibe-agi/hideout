# Adversarial Review: Migration Portable Core

**Batch**: T010–T015 types, cryptography, framing, and hostile-input parsing
**Date**: 2026-08-02
**Scope**: `internal/migration` bundle core and fuzz corpus

## Fresh-eyes findings

1. Struct-order JSON was initially emitted as if it were canonical JSON. The
   reader correctly rejected the writer's first bundle. Writers now normalize
   every JSON document through the same duplicate-aware canonicalizer used by
   readers, and UTC creation timestamps are parsed rather than suffix-checked.
2. Aggregate logical bytes were initially incremented for metadata and the final
   manifest, while no cross-component 4 TiB check existed. Only persistent
   logical extents now contribute to the aggregate, and the checked addition is
   shared by writer and reader before state is committed.
3. A hostile trailer completion offset could overflow in `offset + headerSize`.
   The reader now uses subtraction after proving order, before Argon2id or any
   frame allocation. A `MaxUint64` fixture fails with `bundle.corrupt`.
4. A reader could mutate its nonce/completion bookkeeping before returning an
   error and report a different error if called again. Its first non-EOF failure
   is now terminal and sticky. This prevents callers from advancing or changing
   classification after an authentication failure.
5. If sealing failed after FinalManifest had been appended, retrying on the same
   writer could append a second final sequence. The writer is now poisoned from
   the first final-manifest write until the completion and trailer both land;
   recovery must reopen and authenticate the partial artifact.
6. Unknown required record types lacked the contract's stable
   `migration.bundle.unsupported_record` projection. The typed error and code are
   now defined. V1 has no understood optional extension envelope, so unknown
   optional records also fail closed with that code.
7. Error projection previously trusted arbitrary `Code` and `ComponentID`
   strings. It now emits only registered codes and syntactically valid opaque
   component IDs, preventing a hostile URI or credential-shaped value from
   reaching ordinary diagnostics.

No unresolved finding remains inside T010–T015. Stable-file identity checks,
exclusive `0600` path creation, sync/rename publication, authenticated resume,
full manifest graph validation, and Manager secret-input ownership remain in
their later explicit tasks.

## Red-green and mutation proof

The format tests were written before their implementation and first failed to
compile because the reader, writer, and framing types did not exist. The first
implemented round trip then failed because writer-produced JSON was not
canonical, demonstrating that the reader was enforcing the invariant. The
canonical writer fix made the suite green.

Two additional implementation mutations were applied temporarily:

- Removing `clear(buffer.value)` made
  `TestKDFBoundsNonceUniquenessAndSensitiveBufferCleanup` fail with
  `sensitive buffer was not cleared on callback failure`.
- Disabling the earlier-trailer check made
  `TestBundleReaderRejectsHostileLengthsTamperTruncationAndTrailingData` fail
  because a file with trailing bytes was misclassified as incomplete.

Both mutations were removed and the unchanged tests returned green.

## Negative fixtures

The deterministic suite rejects or distinguishes:

- wrong passphrases, ciphertext tampering, header/AAD changes, and nonce reuse;
- out-of-range Argon2id parameters and hostile frame lengths before allocation;
- duplicate, unknown, noncanonical, fractional, and trailing JSON input;
- reserved prologue/frame/trailer bytes and malformed UTC timestamps;
- truncation, trailing bytes, overflowed trailer offsets, and changed ordering;
- component ordinal gaps, logical overlap, and aggregate logical-size overflow;
- inexact zstd output and invalid sparse extent graphs;
- a second operation on a failed reader or partially sealed writer; and
- unregistered error codes and credential-shaped component identifiers.

Six native fuzz targets cover public/private headers, frames, manifests,
trailers, and bounded zstd records. Their committed corpus includes malformed
duplicate, oversized, truncated, and non-zstd seeds.

## Validation

```text
go test ./internal/migration -count=1
go vet ./internal/migration
go test -race ./internal/migration -count=1
go test ./internal/migration -coverprofile=...  # 75.8% statements
GOMAXPROCS=2 go test ./internal/migration -run='^$' \
  -fuzz=<each target> -fuzztime=20000x -timeout=2m
```

All commands passed after the mutations were restored. The original six
time-bounded fuzz runs executed 427,509 inputs without a crash or invariant
failure. Release smoke now uses a deterministic 20,000 mutation executions per
target plus the committed corpus, with a separate two-minute hang bound, so
host scheduling cannot turn normal fuzz-deadline shutdown into a false failure.
