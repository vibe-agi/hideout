# Contract: Migration Bundle Format v1

## Scope

This contract defines the portable container boundary. It does not grant host or
backend authority and does not define the Manager workflow. A conforming reader
must treat every byte as untrusted.

Default extension: `.hideout-migration`
Partial extension: `.hideout-migration.partial`
File mode on creation: `0600`
Directory mode when Hideout creates the parent: `0700`

## Security properties

A v1 bundle provides:

- confidentiality of manifests, names, configuration, disk bytes, secret values,
  paths, and guest identity evidence;
- authentication of every record and of the complete ordered record sequence;
- explicit incomplete-file detection;
- bounded parsing, key derivation, allocation, and decompression;
- independent record recovery at authenticated boundaries;
- no plaintext intermediate archive;
- immutable sealed input suitable for multiple imports.

It does not provide:

- authenticity of the human or computer that created the bundle;
- protection against a weak passphrase beyond the configured Argon2id cost;
- revocation or proof that another copy was deleted;
- confidentiality of total file size, format version, cryptographic suite, or
  creation time.

## Physical framing

All integers are unsigned, fixed-width, big-endian. Additions to reserved fields
are forbidden within v1; readers reject nonzero reserved bytes.

```text
Prologue
PublicHeader
RecordFrame[0]
RecordFrame[1]
...
RecordFrame[n]        # encrypted FinalManifest record
RecordFrame[n+1]      # encrypted Completion record
Trailer
EOF                   # trailing bytes are invalid
```

### Prologue

The fixed prologue contains:

- 8-byte magic `HIDMIG01`;
- major and minor format versions;
- public-header byte length;
- cryptographic-suite ID;
- reserved zero bytes.

The public-header length must be within the v1 header limit before it is read.

### PublicHeader

The public header is strict JSON with duplicate fields rejected. It contains only:

- random `bundle_id`;
- `created_at`;
- exact Argon2id parameters and random salt;
- master-key-wrap nonce and ciphertext;
- suite/version facts;
- declared global hard-limit profile.

The wrapped master key is XChaCha20-Poly1305 ciphertext under the
passphrase-derived key. Its associated data is the prologue plus the canonical
public-header fields other than the nonce/ciphertext themselves. Unsupported or
out-of-range parameters fail before Argon2id runs.

Wrong passphrase, corrupt wrapped key, and altered authenticated header produce
the same public error class: `migration.bundle.authentication_failed`.

### RecordFrame

The public fixed-size frame header contains:

- frame magic and frame version;
- strictly increasing sequence number;
- ciphertext length;
- random XChaCha20-Poly1305 nonce;
- digest of the preceding complete frame, or all zeroes for sequence zero;
- digest of the encrypted record's private header;
- reserved zero bytes.

The per-record key is derived from the bundle master key, bundle ID, suite ID, and
sequence number using HKDF-SHA-256 with a distinct v1 label. AEAD associated data
is the prologue digest, public-header digest, and entire public frame header.

After decryption, each record begins with a strict private header:

- record type and flags;
- component ID and component ordinal;
- logical offset;
- exact plaintext and encoded lengths;
- plaintext SHA-256 digest;
- private-header version and reserved zero fields.

The public private-header digest must match before payload interpretation. The
payload must decrypt, decompress to exactly the declared plaintext length, match
its digest, remain within its component, and not overlap another extent.

### Payload record types

Required v1 types:

- `Metadata`: normalized strict JSON document or schema-bound receipt.
- `DataChunk`: independently zstd-compressed bytes.
- `RawChunk`: bytes stored without compression when compression is not useful.
- `ZeroExtent`: a logical zero range with no payload.
- `HoleExtent`: a sparse logical range with no payload.
- `SecretValue`: an explicitly selected secret value and bundle-local reference.
- `Checkpoint`: completed component/ordinal set and cumulative logical/encoded
  counters.
- `FinalManifest`: the canonical encrypted index described by
  `migration-manifest.schema.json`.
- `Completion`: manifest sequence/digest, record count, final prefix digest, and
  aggregate logical/encoded counters.

Unknown required types fail with `migration.bundle.unsupported_record`. A type
may be skipped only when the format version and its authenticated flag declare
it optional and the reader understands the extension envelope.

### Trailer

The fixed trailer contains:

- 8-byte magic `HIDEND01`;
- completion-record offset and sequence;
- completion-frame digest;
- bundle-prefix digest through the completion frame;
- reserved zero bytes.

The encrypted Completion record authenticates the trailer values. A file with no
complete trailer, a mismatched offset or digest, or trailing bytes is not sealed.
`inspect` may report its recoverable checkpoint but `import` must reject it.

## Canonical logical disk digest

Disk integrity is independent of sparse physical representation. The logical
digest is SHA-256 over an ordered stream of domain-separated extent descriptors
and content:

```text
disk-format-version || disk-logical-size ||
for each contiguous extent in offset order:
    extent-kind || offset || length || content-for-data/raw
```

Readers must reject gaps, overlaps, offsets beyond the declared logical size, and
noncanonical adjacent extents of the same non-data kind. A destination may choose
a different sparse allocation while preserving the same logical digest.

## Limits

Every reader enforces the smaller of its compiled v1 limits and the header's
declared profile.

| Item | v1 hard maximum |
| --- | ---: |
| Environments | 32 |
| Aggregate logical persistent data | 4 TiB |
| Payload records | 1,048,576 |
| Plaintext chunk | 4 MiB |
| Final manifest plaintext | 16 MiB |
| Other non-payload record plaintext | 1 MiB |
| Record ciphertext overhead | At most 64 KiB plus encoded payload |
| Argon2id memory | 256 MiB |
| Argon2id passes | 10 |
| Argon2id lanes | 8 |

All count, offset, length, and addition/multiplication operations must be checked
for overflow before conversion to platform `int` or allocation. The writer's
default Argon2id profile is 64 MiB, three passes, and four lanes; a reader accepts
only the bounded suite, not arbitrary KDF algorithms.

## Writer protocol

1. Resolve the final and partial paths without following an unsafe output alias.
2. Claim the output in the Manager operation and create the partial file with
   O_EXCL and `0600`.
3. Prompt for the passphrase through an approved secret-input channel. Never read
   it from argv or environment.
4. Write and sync the prologue/header.
5. Stream canonical components in manifest order. For every complete record,
   update the operation ledger and periodic encrypted checkpoint.
6. Write and sync FinalManifest and Completion records.
7. Write and sync the trailer; close; reopen read-only; authenticate the header,
   footer, manifest, counts, and ordered frame chain.
8. Atomically rename the partial file to an unoccupied final path and sync its
   parent directory.
9. Mark the operation complete and release provider snapshots/claims.

The final path is never overwritten. A failure before step 8 leaves no sealed
file at that path.

## Resume protocol

1. Bind to the original export operation ID, output path, source revisions, and
   provider snapshot handles.
2. Re-prompt for the passphrase and authenticate the public header.
3. Compare the operation ledger with encrypted checkpoints. The ledger may speed
   lookup but cannot establish authenticity by itself.
4. Scan frames from the last independently authenticated checkpoint, verify the
   chain, and truncate any torn or unauthenticated tail to the last complete frame.
5. Revalidate that source snapshot handles still name the original immutable
   inputs.
6. Continue at the first incomplete component/ordinal.

If any binding differs, resume fails closed. The operator may remove the partial
artifact or begin a fresh export with a new bundle and operation ID.

## Reader/import protocol

1. Open without write permission and capture stable file identity/size.
2. Validate prologue, public lengths, version, suite, and KDF bounds.
3. Obtain the passphrase through an approved secret-input channel and unwrap the
   master key.
4. Locate and validate the exact EOF trailer.
5. Authenticate Completion and FinalManifest before constructing an import draft.
6. Validate schemas, aggregate counts, graph closure, required capabilities, and
   record ranges.
7. During materialization, authenticate every frame in sequence and re-check each
   component digest. Detect replacement/truncation by stable file identity, size,
   footer binding, and digest.
8. Never modify, append, truncate, rename, or delete the sealed input.

## Secret-input contract

CLI prompts read from a controlling terminal or an explicit already-open file
descriptor whose provenance the local client validates. TUI and WebUI use a
one-shot local Manager secret-input session. In all surfaces:

- the value is masked and never echoed;
- clipboard use is explicit;
- the value is not serialized into request logs or operation JSON;
- the Manager zeroes replaceable buffers after key derivation where Go permits;
- resume requires re-entry;
- status/inspect output never distinguishes wrong password from authenticated
  header corruption.

## Stable error classes

| Code | Meaning |
| --- | --- |
| `migration.bundle.incomplete` | Missing/torn footer; owner may resume |
| `migration.bundle.authentication_failed` | Password/header auth failed |
| `migration.bundle.unsupported_version` | Unsupported version/suite |
| `migration.bundle.unsupported_record` | Unsupported record/extension |
| `migration.bundle.limit_exceeded` | Declared or observed hard limit exceeded |
| `migration.bundle.corrupt` | Invalid frame, digest, graph, or schema |
| `migration.bundle.trailing_data` | Bytes exist after the exact trailer |
| `migration.bundle.changed_during_import` | Input changed during import |
| `migration.bundle.output_exists` | Requested final/partial output conflicts |

Errors may include a record sequence or bundle-local component ID, but never
plaintext names, secret values, passphrases, keys, non-redacted URLs, or raw
payload bytes.
