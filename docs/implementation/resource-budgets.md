# Resource budgets and performance verification

These are implementation limits for the standalone Registry engine. They are
not product-specific policy and are not public schema fields. The limits bound
untrusted Catalog, resolver and source-tree work while preserving deterministic
output.

## Enforced budgets

| Boundary | Default / hard limit | Enforcement |
| --- | ---: | --- |
| Catalog Index input | 8 MiB | Rejected before schema parsing |
| Entries in one Catalog Index | 4,096 | Rejected before any metadata fetch |
| One Release Metadata document | 8 MiB | Rejected before digest/schema processing |
| Aggregate Release Metadata bytes per Catalog | 256 MiB | Accounted in Catalog Index order; Catalog is all-or-nothing |
| Concurrent Release Metadata fetches | 4 by default; hard maximum 4 | Fixed worker window; no unbounded goroutine-per-entry behavior |
| HTTP metadata request timeout | 20 s | HTTP client timeout / response-header timeout |
| HTTP metadata body | 8 MiB by default | Declared and streamed body sizes are bounded |
| HTTP redirect count | 3 by default | Every redirect is revalidated |
| Catalog release candidates across all supplied Catalogs | 16,384 | Rejected before Resolver candidate-map construction |
| Product Definition root requirements / resolved components | 1,024 | Excess roots or transitive graph nodes fail with `RESOLUTION_LIMIT` and no partial Graph |
| Dependency/contract/source declarations across all Catalog releases and Product Definition environment contracts | 65,536 | Rejected before candidate-map construction |
| Resolver search states | 100,000 | Exceeding the limit returns `RESOLUTION_LIMIT`, never a partial Graph |
| Metadata cache | 256 MiB target; 90-day age | Pinned/leased entries are protected; GC may remain above target |
| Git-object cache | 1 GiB target; 90-day age | Pinned/leased objects are protected; GC may remain above target |
| Source-unit collection concurrency | 1 | Deliberately sequential; Workspace rollback remains simple and deterministic |
| Files per Source Unit | 100,000 | Fail before copying the file that exceeds the unit budget |
| Bytes per Source Unit | 1 GiB | Accounted from file metadata; changed/growing files are bounded during copy |
| Files across one Workspace | 500,000 | Aggregate limit across all collected Source Units |
| Source bytes across one Workspace | 8 GiB | Aggregate limit across all collected Source Units |
| Source tree copy buffer | 32 KiB | Bounded streaming copy with cancellation checks |

The default metadata worker count is four, and the hard maximum is also four.
This default was enabled only after the worker pool passed cancellation,
cleanup, index-order determinism, cache-integrity, race-detector and native
Linux/macOS/Windows tests. `LoadOptions` can request a lower worker count for
constrained callers. Fetcher implementations must be safe for concurrent calls
and honor context cancellation. The worker pool is fixed-size and never creates
one goroutine per Catalog entry.
Metadata fetching may complete out of order, but digest verification, schema
validation, identity checks, deduplication and Catalog publication happen in
original Catalog Index order. On cancellation or the first index-ordered
failure, the worker context is canceled and every worker is joined before
`LoadWithOptions` returns.

The Resolver bounds total Catalog candidates at 16,384, dependency/contract/
source declarations at 65,536, root/transitive graph nodes at 1,024 and
backtracking at 100,000 search states. Exceeding any of
these budgets returns `RESOLUTION_LIMIT` and never returns a partial Graph.
Candidate order, constraint expansion and report evidence remain deterministic.
Registry does not silently raise a search limit for a particular consumer.

The source Collector is intentionally sequential. It streams files through a
32 KiB buffer and checks cancellation between reads/writes. Each Source Unit is
limited to 100,000 regular files and 1 GiB; the entire Workspace is limited to
500,000 files and 8 GiB. These are source-copy admission limits. Git temporary
fetch storage and deployment-wide filesystem quotas remain separate concerns.
Parallel Git fetching or source copying is not enabled because it would change
cleanup, credential lifetime, shared Git-object cache locking and Workspace
rollback semantics.

## Performance measurements

CI runs one iteration of the focused benchmark set to catch benchmark breakage
and record comparable allocation/time output. Wall-clock thresholds are not
hard CI gates because hosted runner CPU and I/O vary. Use `benchstat` to compare
repeated runs on the same machine and Go toolchain.

Run focused measurements with:

```sh
go test -run '^$' -bench 'BenchmarkLoadLargeCatalog|BenchmarkMetadataCacheGarbageCollectEmpty' -benchtime=1x -benchmem ./internal/catalog
go test -run '^$' -bench '^BenchmarkResolve' -benchtime=1x -benchmem ./internal/resolver
go test -run '^$' -bench 'BenchmarkCopyLargeSourceTree|BenchmarkGitObjectCacheGarbageCollectEmpty' -benchtime=1x -benchmem ./internal/collector
```

The benchmark set covers a 512-entry Catalog, wide and deep 128-component
graphs, a 32-candidate backtracking fallback case, copying a 1 MiB/256-file
source tree, and empty metadata/Git cache GC passes. Resource ceilings and
correctness tests are hard gates; elapsed-time numbers are recorded evidence,
not portable performance promises.

## Qualification

These limits bound the named Registry operations. They do not provide a
deployment-wide disk quota, network egress firewall, publisher authenticity,
component vulnerability scan, build sandbox or uniform crash durability.
See [Security and trust boundaries](../security/trust-boundaries.md).
