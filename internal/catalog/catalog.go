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

	"github.com/cdin-editor/registry/internal/validation"
)

// Fetcher retrieves release metadata bytes for a Catalog Index entry. REG-019
// requires digest verification before parsing; transport policy is deliberately
// injected so catalog parsing does not acquire network or credential authority.
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
func Load(ctx context.Context, indexBytes []byte, fetcher Fetcher, validator Validator) (*Catalog, error) {
	if ctx == nil {
		return nil, fmt.Errorf("catalog: context is nil")
	}
	if fetcher == nil || validator == nil {
		return nil, fmt.Errorf("catalog: fetcher and validator are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
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

	out := &Catalog{ID: index.CatalogID}
	seen := make(map[identity]Release, len(index.Entries))
	for i, entry := range index.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		variant := entry.Variant
		if variant == "" {
			variant = "default"
		}
		key := identity{entry.ComponentID, variant, entry.Version}

		expected := strings.ToLower(entry.MetadataSHA)
		var payload []byte
		var err error
		if digestFetcher, ok := fetcher.(DigestFetcher); ok {
			payload, err = digestFetcher.FetchWithDigest(ctx, entry.MetadataURI, expected)
		} else {
			payload, err = fetcher.Fetch(ctx, entry.MetadataURI)
		}
		if err != nil {
			// Preserve caller cancellation and the local redirect-policy error type.
			// Arbitrary Fetcher errors are untrusted and may echo URLs or credentials.
			if errors.Is(err, context.Canceled) {
				return nil, context.Canceled
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, context.DeadlineExceeded
			}
			var policyError *safePolicyError
			if errors.As(err, &policyError) {
				return nil, fmt.Errorf("catalog: fetch entry %d metadata: %w", i, policyError)
			}
			return nil, fmt.Errorf("catalog: fetch entry %d metadata: transport failure", i)
		}
		sum := sha256.Sum256(payload)
		actual := hex.EncodeToString(sum[:])
		if actual != expected {
			return nil, fmt.Errorf("catalog: metadata digest mismatch for %s/%s@%s: expected %s, got %s",
				key.component, key.variant, key.version, expected, actual)
		}

		if err := validator.Validate(validation.ComponentRelease, payload, "component-release.json"); err != nil {
			return nil, fmt.Errorf("catalog: validate release %s/%s@%s: %w", key.component, key.variant, key.version, err)
		}
		var release Release
		if err := json.Unmarshal(payload, &release); err != nil {
			return nil, fmt.Errorf("catalog: decode release %s/%s@%s: %w", key.component, key.variant, key.version, err)
		}
		if release.Variant == "" {
			release.Variant = "default"
		}
		if release.ComponentID != key.component || release.Variant != key.variant || release.Version != key.version {
			return nil, fmt.Errorf("catalog: index identity does not match release metadata for %s/%s@%s", key.component, key.variant, key.version)
		}
		release.Raw = append([]byte(nil), payload...)
		release.MetadataURI = entry.MetadataURI
		release.MetadataSHA = actual

		if prior, ok := seen[key]; ok {
			if prior.MetadataSHA != release.MetadataSHA {
				return nil, fmt.Errorf("catalog: conflicting metadata identities for %s/%s@%s", key.component, key.variant, key.version)
			}
			continue
		}
		seen[key] = release
		out.Releases = append(out.Releases, release)
	}
	return out, nil
}
