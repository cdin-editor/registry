package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/cdin-editor/registry/internal/validation"
)

// Fetcher retrieves release metadata bytes for a Catalog Index entry. REG-019
// requires digest verification before parsing; transport policy is deliberately
// injected so catalog parsing does not acquire network or credential authority.
// Load and LoadWithOptions may call Fetch concurrently up to four workers.
// Implementations must be safe for concurrent calls and honor context cancellation.
// See https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#4-catalog-index-and-component-release-metadata
// and https://github.com/cdin-editor/registry/blob/main/docs/implementation/catalog-ingestion.md.
type Fetcher interface {
	Fetch(context.Context, string) ([]byte, error)
}

// DigestFetcher is an optional extension for transports/caches that can bind
// retrieval to the immutable digest declared by the Catalog Index. Load still
// verifies the returned bytes independently before parsing (REG-019).
type DigestFetcher interface {
	FetchWithDigest(context.Context, string, string) ([]byte, error)
}

// Validator validates a JSON document against one embedded Registry v1 schema.
// Catalog delegates schema authority to validation instead of duplicating rules.
type Validator interface {
	Validate(validation.Kind, []byte, string) error
}

// Index is the validated Catalog Index document supplied by a catalog source.
// Its entries reference immutable release metadata, not component source code.
type Index struct {
	SchemaVersion string  `json:"schema_version"`
	CatalogID     string  `json:"catalog_id"`
	Entries       []Entry `json:"entries"`
}

// Entry identifies one immutable Component Release Metadata document and the
// digest that must be checked before the bytes are parsed (REG-019).
type Entry struct {
	ComponentID string `json:"component_id"`
	Variant     string `json:"variant"`
	Version     string `json:"version"`
	MetadataURI string `json:"metadata_uri"`
	MetadataSHA string `json:"metadata_sha256"`
}

// SourceUnit describes one independently collectible source repository/revision.
// Multiple source units are allowed for one logical component (REG-017/018).
type SourceUnit struct {
	ID            string `json:"source_unit_id"`
	RepositoryURI string `json:"repository_uri"`
	Revision      string `json:"revision"`
	Subdirectory  string `json:"subdirectory,omitempty"`
}

// Dependency and contract declarations are retained as typed data because the
// resolver must compute dependency closure and provider compatibility. These
// fields mirror the Component Release Metadata contract; they are not executable
// extension points. See REG-008 and REG-015/016 in
// https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md.

// Dependency declares one required component/variant and its frozen constraint.
type Dependency struct {
	ComponentID       string `json:"component_id"`
	Variant           string `json:"variant,omitempty"`
	VersionConstraint string `json:"version_constraint"`
}

// ContractProvider declares a contract ID and SemVer supplied by this release.
type ContractProvider struct {
	ContractID string `json:"contract_id"`
	Version    string `json:"version"`
}

// ContractRequirement declares a contract ID/version constraint required by
// this release. Provider selection belongs to the generic resolver (REG-015/016).
type ContractRequirement struct {
	ContractID        string `json:"contract_id"`
	VersionConstraint string `json:"version_constraint"`
}

// Contracts groups the public provides/requires declarations from release
// metadata; it has no consumer-specific behavior.
type Contracts struct {
	Provides []ContractProvider    `json:"provides"`
	Requires []ContractRequirement `json:"requires"`
}

// Release is a parsed Component Release Metadata document plus the verified
// metadata location/digest. Raw retains the exact bytes that passed REG-019.
type Release struct {
	SchemaVersion string       `json:"schema_version"`
	ComponentID   string       `json:"component_id"`
	Variant       string       `json:"variant"`
	Version       string       `json:"version"`
	Status        string       `json:"status"`
	SourceUnits   []SourceUnit `json:"source_units"`
	Dependencies  []Dependency `json:"dependencies"`
	Contracts     Contracts    `json:"contracts"`
	Raw           []byte       `json:"-"`
	MetadataURI   string       `json:"-"`
	MetadataSHA   string       `json:"-"`
}

// Catalog is one fully loaded index and its validated release records. Load
// never returns a partial Catalog after a fetch/validation failure.
type Catalog struct {
	ID          string
	MetadataURI string
	MetadataSHA string
	Releases    []Release
}

// LoadFromSource is the production entry point when the caller knows the
// immutable Catalog Index URI. It hashes the exact index bytes so a Manifest can
// identify the catalog input that produced the selected graph (REG-023/024).
// The URI is recorded as provenance; Fetcher policy still governs each metadata
// request and is not inferred from this field.
func LoadFromSource(ctx context.Context, indexBytes []byte, indexURI string, fetcher Fetcher, validator Validator) (*Catalog, error) {
	if strings.TrimSpace(indexURI) == "" {
		return nil, fmt.Errorf("catalog: source URI is required")
	}
	parsed, err := url.Parse(indexURI)
	if err != nil || !parsed.IsAbs() || parsed.Scheme == "" {
		return nil, fmt.Errorf("catalog: source URI must be absolute")
	}
	loaded, err := Load(ctx, indexBytes, fetcher, validator)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(indexBytes)
	loaded.MetadataURI = indexURI
	loaded.MetadataSHA = hex.EncodeToString(sum[:])
	return loaded, nil
}

type identity struct {
	component string
	variant   string
	version   string
}

// Load validates a Catalog Index, verifies each referenced metadata digest before
// decoding, normalizes omitted variants and rejects conflicting identities.
// The returned Catalog is all-or-nothing: consumers never observe partial input.
// See REG-004/005/019 and
// https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#4-catalog-index-and-component-release-metadata.
const (
	maxConcurrentReleaseMetadataFetches = 4
	defaultReleaseMetadataFetches       = maxConcurrentReleaseMetadataFetches
	maxCatalogIndexBytes                = 8 << 20
	maxCatalogEntries                   = 4096
	maxReleaseMetadataBytes             = 8 << 20
	maxCatalogMetadataBytes             = 256 << 20
)

type releaseMetadataSizeError struct {
	size int
}

// LoadOptions controls bounded Catalog metadata retrieval. Zero selects the
// qualified four-worker default; callers may choose one to three workers.
// Values above the hard maximum are rejected rather than silently clamped.
type LoadOptions struct {
	MaxConcurrentMetadataFetches int
}

func (e *releaseMetadataSizeError) Error() string {
	return fmt.Sprintf("catalog: Release Metadata exceeds the %d-byte entry limit (got %d bytes)", maxReleaseMetadataBytes, e.size)
}

type releaseFetchResult struct {
	index   int
	payload []byte
	err     error
}

// Load validates a Catalog Index and retrieves its immutable Release Metadata
// with the default bounded worker pool. Results are still verified and processed
// in original index order. It never returns a partial Catalog after any fetch or
// validation failure. The Validator is called serially; Fetcher implementations
// must be safe for concurrent calls and honor context cancellation.
func Load(ctx context.Context, indexBytes []byte, fetcher Fetcher, validator Validator) (*Catalog, error) {
	return LoadWithOptions(ctx, indexBytes, fetcher, validator, LoadOptions{})
}

// LoadWithOptions validates and loads a Catalog with an explicit bounded worker
// count. A value of zero selects the qualified default (four workers). Values
// above the hard maximum are rejected rather than silently clamped.
// LoadWithOptions applies the fixed metadata-fetch ceiling while preserving
// Catalog Index order and all-or-nothing publication (REG-004/005/019).
// Spec: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#4-catalog-index-and-component-release-metadata
// Design: https://github.com/cdin-editor/registry/blob/main/docs/implementation/catalog-transport.md.
func LoadWithOptions(ctx context.Context, indexBytes []byte, fetcher Fetcher, validator Validator, options LoadOptions) (*Catalog, error) {
	concurrency := options.MaxConcurrentMetadataFetches
	if concurrency == 0 {
		concurrency = defaultReleaseMetadataFetches
	}
	if concurrency < 1 || concurrency > maxConcurrentReleaseMetadataFetches {
		return nil, fmt.Errorf("catalog: metadata fetch concurrency must be between 1 and %d", maxConcurrentReleaseMetadataFetches)
	}
	if ctx == nil {
		return nil, fmt.Errorf("catalog: context is nil")
	}
	if fetcher == nil || validator == nil {
		return nil, fmt.Errorf("catalog: fetcher and validator are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(indexBytes) > maxCatalogIndexBytes {
		return nil, fmt.Errorf("catalog: Catalog Index exceeds the %d-byte input limit", maxCatalogIndexBytes)
	}
	if err := validator.Validate(validation.CatalogIndex, indexBytes, "catalog-index.json"); err != nil {
		return nil, fmt.Errorf("catalog: validate index: %w", err)
	}

	var index Index
	if err := json.Unmarshal(indexBytes, &index); err != nil {
		return nil, fmt.Errorf("catalog: decode index: %w", err)
	}
	if strings.TrimSpace(index.CatalogID) == "" {
		return nil, fmt.Errorf("catalog: empty catalog_id")
	}
	if len(index.Entries) > maxCatalogEntries {
		return nil, fmt.Errorf("catalog: Catalog entry count exceeds the limit of %d (got %d)", maxCatalogEntries, len(index.Entries))
	}

	out := &Catalog{ID: index.CatalogID}
	seen := make(map[identity]Release, len(index.Entries))
	var totalMetadataBytes int64
	err := fetchAndProcessEntries(ctx, index.Entries, fetcher, concurrency, func(i int, payload []byte) error {
		nextTotal, sizeErr := accountCatalogMetadataBytes(totalMetadataBytes, int64(len(payload)))
		if sizeErr != nil {
			return sizeErr
		}
		totalMetadataBytes = nextTotal
		entry := index.Entries[i]
		variant := entry.Variant
		if variant == "" {
			variant = "default"
		}
		key := identity{entry.ComponentID, variant, entry.Version}

		expected := strings.ToLower(entry.MetadataSHA)
		sum := sha256.Sum256(payload)
		actual := hex.EncodeToString(sum[:])
		if actual != expected {
			return fmt.Errorf("catalog: metadata digest mismatch for %s/%s@%s: expected %s, got %s",
				key.component, key.variant, key.version, expected, actual)
		}

		if err := validator.Validate(validation.ComponentRelease, payload, "component-release.json"); err != nil {
			return fmt.Errorf("catalog: validate release %s/%s@%s: %w", key.component, key.variant, key.version, err)
		}
		var release Release
		if err := json.Unmarshal(payload, &release); err != nil {
			return fmt.Errorf("catalog: decode release %s/%s@%s: %w", key.component, key.variant, key.version, err)
		}
		if release.Variant == "" {
			release.Variant = "default"
		}
		if release.ComponentID != key.component || release.Variant != key.variant || release.Version != key.version {
			return fmt.Errorf("catalog: index identity does not match release metadata for %s/%s@%s", key.component, key.variant, key.version)
		}
		release.Raw = append([]byte(nil), payload...)
		release.MetadataURI = entry.MetadataURI
		release.MetadataSHA = actual

		if prior, ok := seen[key]; ok {
			if prior.MetadataSHA != release.MetadataSHA {
				return fmt.Errorf("catalog: conflicting metadata identities for %s/%s@%s", key.component, key.variant, key.version)
			}
			return nil
		}
		seen[key] = release
		out.Releases = append(out.Releases, release)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func accountCatalogMetadataBytes(currentTotal, nextEntry int64) (int64, error) {
	if nextEntry < 0 || nextEntry > maxReleaseMetadataBytes {
		return currentTotal, fmt.Errorf("catalog: Release Metadata exceeds the %d-byte entry limit", maxReleaseMetadataBytes)
	}
	if currentTotal < 0 || nextEntry > maxCatalogMetadataBytes-currentTotal {
		return currentTotal, fmt.Errorf("catalog: Release Metadata exceeds the %d-byte Catalog budget", maxCatalogMetadataBytes)
	}
	return currentTotal + nextEntry, nil
}

// fetchAndProcessEntries keeps at most the configured bounded number of metadata
// requests in flight. Results
// may complete out of order, but the callback always runs in Catalog Index order.
// A failure cancels the remaining window and drains every worker before return,
// so callers never observe background requests after Load has failed.
func fetchAndProcessEntries(
	ctx context.Context,
	entries []Entry,
	fetcher Fetcher,
	concurrency int,
	process func(index int, payload []byte) error,
) error {
	if len(entries) == 0 {
		return ctx.Err()
	}
	workerCount := concurrency
	if len(entries) < workerCount {
		workerCount = len(entries)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	jobs := make(chan int)
	results := make(chan releaseFetchResult, workerCount)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for index := range jobs {
				result := releaseFetchResult{index: index}
				if err := workerCtx.Err(); err != nil {
					result.err = err
				} else {
					entry := entries[index]
					if digestFetcher, ok := fetcher.(DigestFetcher); ok {
						result.payload, result.err = digestFetcher.FetchWithDigest(workerCtx, entry.MetadataURI, strings.ToLower(entry.MetadataSHA))
					} else {
						result.payload, result.err = fetcher.Fetch(workerCtx, entry.MetadataURI)
					}
					if result.err == nil && len(result.payload) > maxReleaseMetadataBytes {
						result.err = &releaseMetadataSizeError{size: len(result.payload)}
						result.payload = nil
					}
				}
				results <- result
			}
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()

	stopWorkers := func() {
		cancel()
		close(jobs)
		for range results {
			// Drain bounded in-flight results before returning. Fetchers are
			// required to honor context cancellation.
		}
	}
	defer cancel()

	nextToSubmit := 0
	for nextToSubmit < workerCount {
		select {
		case jobs <- nextToSubmit:
			nextToSubmit++
		case <-ctx.Done():
			stopWorkers()
			return ctx.Err()
		}
	}

	pending := make(map[int]releaseFetchResult, workerCount)
	for nextToProcess := 0; nextToProcess < len(entries); {
		if err := ctx.Err(); err != nil {
			stopWorkers()
			return err
		}
		result, ok := pending[nextToProcess]
		if !ok {
			select {
			case received, open := <-results:
				if !open {
					return fmt.Errorf("catalog: metadata fetch workers stopped before completing the index")
				}
				pending[received.index] = received
				continue
			case <-ctx.Done():
				stopWorkers()
				return ctx.Err()
			}
		}
		delete(pending, nextToProcess)
		if result.err != nil {
			stopWorkers()
			if errors.Is(result.err, context.Canceled) {
				if err := ctx.Err(); err != nil {
					return err
				}
				return context.Canceled
			}
			if errors.Is(result.err, context.DeadlineExceeded) {
				return context.DeadlineExceeded
			}
			var sizeError *releaseMetadataSizeError
			if errors.As(result.err, &sizeError) {
				return fmt.Errorf("catalog: fetch entry %d metadata: %w", nextToProcess, sizeError)
			}
			var policyError *safePolicyError
			if errors.As(result.err, &policyError) {
				return fmt.Errorf("catalog: fetch entry %d metadata: %w", nextToProcess, policyError)
			}
			return fmt.Errorf("catalog: fetch entry %d metadata: transport failure", nextToProcess)
		}
		if err := process(nextToProcess, result.payload); err != nil {
			stopWorkers()
			return err
		}
		nextToProcess++
		if nextToSubmit < len(entries) {
			select {
			case jobs <- nextToSubmit:
				nextToSubmit++
			case <-ctx.Done():
				stopWorkers()
				return ctx.Err()
			}
		}
	}
	close(jobs)
	for range results {
		// All entries have been processed in input order. This also joins the
		// worker set and guarantees Load returns after all fetch activity ends.
	}
	return ctx.Err()
}
