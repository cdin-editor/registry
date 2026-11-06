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
fields. The current transport already enforces URI+digest identity and
verify-before-use; age/budget eviction, manifest pins, replay leases and
serialized garbage collection are **defined here but remain Phase 7
implementation work**. Until that work lands, entries are retained rather
than opportunistically deleted.

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

These tests validate transport behavior using an injected HTTP RoundTripper and resolver; deployment firewall and public DNS routing must be validated in the deployment environment.
