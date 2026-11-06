# Manifest CLI operations

The CLI exposes the read-only verification API and transactional replay engine.
Both commands consume a persisted `registry-manifest.json`; neither runs the
Resolver or chooses a release from current Catalog state.

## Commands

### Verify

```sh
registry manifest verify --input <file|-> [--sha256 <lowercase-hex>]
```

Use `--input -` to read the Manifest bytes from standard input. The optional
`--sha256` is a detached receipt; it must be 64 lowercase hexadecimal
characters and must match the exact input bytes. Omitting it does not skip
canonical JSON, embedded public-schema, provenance, identity or path checks.

A successful command writes one JSON object and a trailing newline to stdout:

```json
{
  "valid": true,
  "schema_version": "1.0.0",
  "registry_version": "1.2.3",
  "manifest_sha256": "<sha256-of-canonical-input>",
  "components": 2,
  "source_units": 3
}
```

Verification is local and read-only. It does not contact Catalogs, fetch Git
objects, write files or assert publisher authenticity.

### Replay

```sh
registry manifest replay \
  --input <file|-> \
  --output <new-directory> \
  --allow-host <host[,host]>... \
  [--sha256 <lowercase-hex>] \
  [--verify-remote-metadata] \
  [--cache-dir <directory>]
```

Replay first verifies the persisted Manifest, then collects only the exact
full Git commit IDs it records. It does not invoke version selection. The
destination must not exist. The Registry uses its existing private staging,
owned sibling lock and native atomic no-replace publication boundary; a failed
operation does not publish a partial Workspace and does not overwrite a
pre-existing destination.

Each source repository hostname must appear in an explicit `--allow-host`.
The same list must include Catalog Index and Release Metadata hosts when
`--verify-remote-metadata` is enabled. Exact host matching is used; wildcard
hosts and inferred trust are not supported.

Metadata behavior is explicit:

- **Default/offline metadata mode:** no Catalog Index or Release Metadata
  request is made. The verified Manifest is the selection record. Git still
  has to provide each exact commit, either from an accessible remote or via a
  configured local Git runner.
- **Strict mode:** `--verify-remote-metadata` re-fetches the exact recorded
  Catalog Index and selected Release Metadata documents. Bytes are checked
  against the recorded SHA-256 and schemas before identities, source units,
  dependency targets and contracts are compared. A changed snapshot fails
  closed; it never causes a new version selection.
- `--cache-dir` configures the digest-addressed metadata cache for strict
  metadata verification and the content-addressed Git-object cache for
  `prepare` and `manifest replay`. The Git cache lives under
  `<cache-dir>/.git-objects`; offline replay can therefore reuse verified
  commits without contacting the source remote.

A successful replay writes a JSON receipt to stdout containing the published
Workspace, Manifest and Report paths and detached digests. The Workspace is
already published before the receipt is written. Therefore, a stdout write
failure returns `CLI_OUTPUT_WRITE_FAILED` but does not roll back the completed
Workspace; the caller can inspect the explicit `--output` directory.

## Output and exit codes

For both commands, success writes only the JSON receipt to stdout. Diagnostics
are written to stderr. Operational/input failures return exit code `1`;
usage errors return `2`; success returns `0`. Existing `validate`,
`resolve` and `prepare` exit-code behavior is unchanged.

New operations use the stable stderr form:

```text
registry: error[CODE]: actionable diagnostic
```

| Code | Exit | Meaning |
| --- | ---: | --- |
| `USAGE_ERROR` | 2 | Unknown operation/flag, missing value or invalid flag combination |
| `MANIFEST_INPUT_READ_FAILED` | 1 | Input file or stdin could not be read |
| `MANIFEST_DIGEST_INVALID` | 1 | Supplied detached digest is not lowercase SHA-256 |
| `MANIFEST_DIGEST_MISMATCH` | 1 | Input bytes do not match the supplied digest |
| `MANIFEST_INVALID` | 1 | Canonical bytes, public schema or provenance validation failed |
| `REPLAY_METADATA_CONFIG_FAILED` | 1 | Strict metadata transport could not be configured |
| `REPLAY_GIT_CACHE_CONFIG_FAILED` | 1 | Content-addressed Git-object cache could not be configured |
| `REPLAY_METADATA_FAILED` | 1 | Recorded remote metadata failed digest/schema/identity checks |
| `REPLAY_SOURCE_FAILED` | 1 | A pinned source commit could not be retrieved or verified |
| `REPLAY_DESTINATION_EXISTS` | 1 | Destination already exists and is preserved |
| `REPLAY_DESTINATION_BUSY` | 1 | Destination lock is held; stale locks require operator investigation |
| `REPLAY_OUTPUT_MISMATCH` | 1 | Recollected sources did not reproduce the exact Manifest |
| `OPERATION_CANCELED` | 1 | Context or process signal canceled the operation |
| `REPLAY_FAILED` | 1 | Other replay failure before publication |
| `CLI_OUTPUT_WRITE_FAILED` | 1 | Receipt could not be written to stdout |
| `INTERNAL_ERROR` | 1 | A required CLI dependency could not be initialized |

Diagnostics use stable categories and deliberately omit raw HTTP errors, Git
stderr, environment values, credential-bearing URLs and machine-specific
temporary paths. Callers should branch on the error code, not the English
message.

## Cancellation and cleanup

On Linux and macOS, Ctrl+C and SIGTERM cancel the command context. On Windows,
Ctrl+C cancels the command context. The context is passed to strict metadata
requests and Git child processes. Replay cancellation follows the same
Workspace cleanup path as any other failure: private staging and the lock
created by that operation are removed. Existing destinations and locks not
owned by the operation are preserved. Cancellation cannot undo a Workspace
whose atomic publication already completed.

## Security boundaries

Manifest verification does not fetch or execute source code. Replay does not
execute collected component code. HTTPS host allow-lists are not a deployment
firewall; deployments remain responsible for Git network egress controls.
SHA-256 detects byte changes relative to a trusted receipt but is not a
signature, attestation or publisher-authenticity proof.


## Git-object cache retention and collection

When a cache directory is supplied to `prepare` or `manifest replay`, full
Git commit IDs are used as content addresses under
`<cache-dir>/.git-objects`. A cache hit is verified with Git's object-type
check, a recomputed commit-object hash and strict object-graph `git fsck`
before it is used. Corrupt/incomplete entries are discarded and fetched again.
The bare cache stores no remote configuration or credential material.

The same `cache pin`, `cache unpin` and `cache gc` commands manage both
metadata and Git-object retention. A verified Manifest pin protects all its
recorded source commit IDs. A replay-wide lease protects every source commit
until the whole replay finishes, including objects not yet collected. Individual
object-use leases also protect checkout and copy operations.

Git-object GC uses a 90-day age bound and a 1 GiB target budget, evicting the
oldest unprotected verified repositories first. Protected objects may keep the
cache above budget. Each cache's complete reference snapshot and inventory
must be readable and safe before its GC pass can delete anything. The metadata and Git-object passes are independently
serialized rather than one cross-cache transaction; when one pass fails after
the other has completed, repeating `cache gc` is safe and idempotent.
## Metadata cache retention and collection

The metadata cache keeps the exact URI+expected-digest identity. Entries may
be removed only by explicit garbage collection; retained Manifests and active
strict replays protect their referenced identities. The Git-object cache uses
the full commit ID and applies a separate 1 GiB budget with the same 90-day
age bound.

```sh
registry cache pin --manifest ./prepared/registry-manifest.json --cache-dir ./registry-cache
registry cache unpin --manifest-sha256 <manifest-sha256> --cache-dir ./registry-cache
registry cache gc --cache-dir ./registry-cache
```

`pin` accepts only a Manifest that passes canonical-byte, public-schema and
provenance verification. Its receipt reports the SHA-256 of the exact Manifest
bytes; use that value with `unpin`. `gc` uses a 256 MiB target budget and a
90-day age bound. It evicts the oldest unprotected entries first and may remain
above budget when pins or leases alone exceed the target. The output is a
machine-readable JSON receipt with removal counts and bytes.

GC fails closed when the pin/lease snapshot is missing, malformed or unsafe.
The maintenance lock is exclusive. A maintenance lock, per-object lock or
lease left by an abnormally terminated process is not automatically reclaimed:
investigate the owner and remove only a confirmed-stale control file as an
operator action.
