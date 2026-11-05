package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cdin-editor/registry/internal/validation"
)

// Fetcher retrieves an immutable metadata payload. Implementations own transport
// policy (timeouts, host allow-lists, authentication and response-size limits).
type Fetcher interface {
	Fetch(context.Context, string) ([]byte, error)
}

type Validator interface {
	Validate(validation.Kind, []byte, string) error
}

type Index struct {
	SchemaVersion string  `json:"schema_version"`
	CatalogID     string  `json:"catalog_id"`
	Entries       []Entry `json:"entries"`
}

type Entry struct {
	ComponentID string `json:"component_id"`
	Variant     string `json:"variant"`
	Version     string `json:"version"`
	MetadataURI string `json:"metadata_uri"`
	MetadataSHA string `json:"metadata_sha256"`
}

type SourceUnit struct {
	ID            string `json:"source_unit_id"`
	RepositoryURI string `json:"repository_uri"`
	Revision      string `json:"revision"`
	Subdirectory  string `json:"subdirectory,omitempty"`
}

type Release struct {
	SchemaVersion string       `json:"schema_version"`
	ComponentID   string       `json:"component_id"`
	Variant       string       `json:"variant"`
	Version       string       `json:"version"`
	Status        string       `json:"status"`
	SourceUnits   []SourceUnit `json:"source_units"`
	Raw           []byte       `json:"-"`
	MetadataURI   string       `json:"-"`
	MetadataSHA   string       `json:"-"`
}

type Catalog struct {
	ID       string
	Releases []Release
}

type identity struct {
	component string
	variant   string
	version   string
}

// Load validates the index, verifies each metadata digest before parsing,
// validates each release document, and rejects conflicting identities.
// An omitted variant is normalized to "default" before identity comparison.
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

		payload, err := fetcher.Fetch(ctx, entry.MetadataURI)
		if err != nil {
			return nil, fmt.Errorf("catalog: fetch entry %d metadata %q: %w", i, entry.MetadataURI, err)
		}
		sum := sha256.Sum256(payload)
		actual := hex.EncodeToString(sum[:])
		expected := strings.ToLower(entry.MetadataSHA)
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
			continue // byte-identical release metadata is safely deduplicated.
		}
		seen[key] = release
		out.Releases = append(out.Releases, release)
	}
	return out, nil
}
