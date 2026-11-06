# Catalog HTTP transport and cache

## Responsibility and references

The production transport is implemented in `internal/catalog/httpfetcher.go`. It satisfies the transport-neutral `catalog.Fetcher` interface and also implements the optional digest-aware extension used by `catalog.Load`.

- [REG-019 — Catalog metadata digest before parsing](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#11-normative-registry-requirements)
- [REG-026 — untrusted Registry metadata](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#8-security-and-trust-boundary)
- [Registry security boundary](https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry.md#7-security-and-reproducibility)

## Policy choices

- HTTPS only; exact hostname allow-list, no wildcard entries.
- Reject IP literals, URI userinfo, query/fragment components and non-443 ports.
- Every redirect is revalidated against the same allow-list and capped by a redirect count.
- Bounded HTTP client timeout, response-header timeout and response body read. Both declared Content-Length and streamed body length are checked.
- DNS resolution is performed inside the dial boundary. Every resolved address is checked before a socket is opened, and the validated IP is the IP actually dialed. Private, loopback, link-local, multicast, unspecified, shared-address and selected reserved/documentation ranges are rejected.
- TLS minimum version is 1.2. Proxy environment variables are not used; deployments must provide explicit egress controls rather than inheriting an ambient proxy configuration.
- The client does not accept credentials from Catalog metadata and does not log full URLs or secrets.

An exact hostname allow-list and IP checks reduce SSRF exposure, but they do not replace network egress controls at the deployment boundary. Operators should block private/link-local destinations at the firewall and monitor DNS/route policy changes.

## Credential and error boundary

- Catalog metadata requests are public, unauthenticated HTTPS GETs. The fetcher has no credential-provider option, cookie jar, Authorization header or ambient proxy configuration.
- URI userinfo, query strings and fragments are rejected. Credentials are never inferred from Catalog metadata or local machine configuration.
- Private Catalog metadata authentication is not supported. Any future authentication must be introduced as an explicit injected credential-provider boundary with short-lived credentials and tests proving that values never enter URLs, command arguments, diagnostics or logs.
- Transport and response-body failures are reported using safe classifications. Arbitrary RoundTripper, DNS, socket and server-controlled error text is discarded because it may echo URLs, userinfo, tokens or other secret material. Local cancellation, timeout and redirect-policy failures remain distinguishable.
- Git source retrieval is likewise public-only today. `CommandGit` receives an allow-listed process environment and does not inherit ambient credential helpers, askpass programs, SSH agent state or proxy settings. See [Collector design](collector.md).

## Bounded Catalog metadata loading

Catalog Index admission is bounded before schema parsing to 8 MiB and 4,096
entries. A single Release Metadata document is limited to 8 MiB and the
aggregate payload budget for one Catalog is 256 MiB. These are implementation
resource limits, not public schema fields.

The qualified default worker pool runs at most four Release Metadata fetches
concurrently. The `Load` entry point uses four workers by default;
`LoadWithOptions` can request a lower count from one to four. This default was
enabled after cancellation, cleanup, deterministic ordering, cache-integrity,
race-detector and native Linux/macOS/Windows tests passed. The worker pool has a
fixed size; it does not create one goroutine per Index entry. Fetch completion
may be out of order, but digest checks, schema validation, identity checks,
duplicate handling and final Release ordering occur in original Index order.
The Validator therefore remains serial and no Catalog output depends on
network completion timing.

The first cancellation or index-ordered fetch/processing failure cancels the
worker context, closes the job stream and joins all started workers before
returning. `Load` returns no Catalog on any error. Injected `Fetcher`
implementations must be safe for concurrent calls and must honor context
cancellation. Tests assert the maximum in-flight count, cancellation cleanup,
out-of-order completion with stable Release ordering and failure without
partial Catalog publication.

## Digest-bound cache

`HTTPFetcher.FetchWithDigest` keys a cache entry by SHA-256 of the immutable metadata URI plus the declared expected SHA-256. A cache hit is used only if the file is a regular non-symlink file, is within the configured size bound and its actual bytes hash to the expected digest. Corrupt entries are removed and fetched again.

Network bytes are checked against the expected digest before cache write. Cache writes use a private temporary file, restrictive permissions, fsync and atomic rename. Cache failure is non-fatal after successful digest verification: cache is an optimization, not an authority. `catalog.Load` computes the digest again before schema validation, so the transport cannot bypass the Catalog trust boundary.

## Metadata cache retention and eviction policy

This policy defines the Phase 7 lifecycle work without changing cache identity
or the digest trust boundary:

- **Identity is immutable:** retain the current key,
  `SHA-256(exact metadata URI bytes || NUL || lowercase expected SHA-256)`.
  Do not normalize, rewrite or alias the URI. Entries fetched without an
  expected digest remain ineligible for persistent cache reads and writes.
- **Admission bound:** one metadata entry is at most 8 MiB (the existing
  default fetch bound). The persistent cache has a 256 MiB target budget.
- **Age bound:** an unpinned entry becomes eligible for eviction after 90 days
  without a successful digest-verified cache hit or write. The access timestamp
  is eviction metadata only; it is never an integrity input.
- **Eviction order:** when the age bound is exceeded or the cache is above its
  target budget, evict the least-recently verified unpinned entries first.
  Break equal timestamps by ascending cache filename so the choice is stable.
  A GC pass may leave the cache above budget when protected entries alone
  exceed the budget; it must never break a pin to force a hard quota.
- **Manifest retention:** every metadata identity referenced by a retained
  Manifest is pinned for at least as long as that Manifest is retained.
  Explicit replay operations hold a lease for every identity they may access
  until they finish. Pins and active leases are separate from ordinary LRU
  recency.
- **Fail-closed collection:** GC must take the cache's exclusive maintenance
  lock and operate on a complete, validated snapshot of retained-Manifest pins
  and active replay leases. If either reference source is unavailable,
  malformed or incomplete, GC removes nothing. A cleanup pass must not race an
  active fetch/write/replay.
- **Integrity remains authoritative:** every cache hit is checked as a regular
  non-symlink file, checked against the configured size limit and re-hashed
  against the expected digest before use. Corrupt entries are discarded and
  fetched again. Network bytes are digest-verified before storage, and
  Catalog.Load independently verifies them again before parsing.

The limits above are Registry implementation policy, not public contract
fields. The implementation now enforces:

- `registry cache pin --manifest <file|-> --cache-dir <directory>` verifies a
  canonical Manifest and stores an immutable pin identified by the exact
  Manifest SHA-256. Repeating the same pin is idempotent.
- `registry cache unpin --manifest-sha256 <hex> --cache-dir <directory>`
  releases that explicit retention reference.
- `registry cache gc --cache-dir <directory>` takes the exclusive maintenance
  lock, validates the complete pin/lease snapshot, then removes unprotected
  entries older than 90 days and least-recently-verified entries until the
  256 MiB target budget is met. Protected entries may keep the cache above
  budget.
- Strict replay holds a lease for every metadata identity in its saved
  Manifest; each digest-aware fetch also holds an entry lease from lookup
  through verification and cache write. Cache hits update their
  last-verified time only after digest verification.
- If a pin/lease snapshot is malformed or unsafe, GC removes nothing. A
  leftover maintenance lock or lease after abnormal termination is preserved
  and requires operator investigation; Registry does not guess that it is
  stale or delete it automatically.

The cache continues to be keyed by the exact URI bytes plus lowercase expected
SHA-256. A pin protects the cache identity, not potentially corrupt bytes;
every read is still re-hashed before use and corruption triggers refetch.

## Tests

- URL policy and host allow-list rejection.
- Declared and streamed response-size limits.
- Redirect to an unapproved host.
- Cache reuse, corrupt-cache refetch, digest mismatch and symlink entry rejection.
- Private address resolution and context cancellation.
- Request/header timeout, stalled response body and truncated/read-failure behavior.
- Redirect-limit exhaustion and unsafe redirect targets.
- Mixed public/prohibited DNS answers and proof that the checked IP is the dialed IP.
- Authorization/cookie/proxy absence and remote-error redaction.
- Bounded concurrent release metadata fetches (maximum four), cancellation/join behavior, stable index-order validation and no partial Catalog return.
- Catalog Index (8 MiB/4,096 entries), per-document (8 MiB) and aggregate metadata (256 MiB) admission/resource limits.
- Age/budget eviction order, explicit Manifest pins, active replay leases,
  maintenance contention and malformed-reference fail-closed behavior.

These tests validate transport behavior using an injected HTTP RoundTripper and resolver; deployment firewall and public DNS routing must be validated in the deployment environment.
