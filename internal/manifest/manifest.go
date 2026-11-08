// Package manifest projects resolver results and Collector outputs into the
// public Registry v1 Manifest and Resolution Report contracts.
//
// Spec traceability:
//
//	REG-023: exact selected releases, source identities, dependency edges,
//	         contract declarations and workspace-relative source paths.
//	REG-024: deterministic output without host paths, timestamps or random IDs.
//	REG-025: no successful Manifest is built from a failed or incomplete graph.
//	Contracts: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#7-resolved-manifest-and-resolution-report
//	Architecture: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry.md#5-source-collection-and-handoff
//	Local design: https://github.com/cdin-editor/registry/blob/main/docs/implementation/manifest.md
//
// The package does no selection, network access, Git operations or workspace
// writes. It translates typed values, sorts set-like arrays and emits RFC 8785
// canonical JSON, including ECMAScript-compatible IEEE-754 number formatting
// for extension fields allowed by the public JSON contracts.
package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/collector"
	"github.com/cdin-editor/registry/internal/resolver"
	"github.com/cdin-editor/registry/internal/semver"
	"github.com/cdin-editor/registry/internal/validation"
)

type VerificationStage string

const (
	VerificationStageCanonicalBytes VerificationStage = "canonical-bytes"
	VerificationStagePublicSchema   VerificationStage = "public-schema"
	VerificationStageProvenance     VerificationStage = "provenance"
)

// VerificationError exposes only the failed verification stage. Its underlying
// cause remains available to internal callers through errors.Is/As, but the
// default message does not echo untrusted Manifest values or schema fragments.
type VerificationError struct {
	Stage VerificationStage
	cause error
}

func (e *VerificationError) Error() string {
	return fmt.Sprintf("manifest: %s verification failed", e.Stage)
}

func (e *VerificationError) Unwrap() error {
	return e.cause
}

const schemaVersion = "1.0.0"

// Document is the public Resolved Manifest v1 data model. It is a
// reproducibility record, not a mutable product configuration or lockfile.
type Document struct {
	SchemaVersion        string                    `json:"schema_version"`
	RegistryVersion      string                    `json:"registry_version"`
	Resolution           *resolver.ResolutionPolicy `json:"resolution,omitempty"`
	Catalogs             []Catalog                 `json:"catalogs"`
	Roots                []Root                    `json:"roots"`
	EnvironmentContracts []EnvironmentContract     `json:"environment_contracts,omitempty"`
	Components           []Component               `json:"components"`
	ResolutionOrder      []resolver.Node           `json:"resolution_order"`
}

// Catalog records the exact Catalog Index source bytes used for resolution.
type Catalog struct {
	CatalogID   string "json:\"catalog_id\""
	MetadataURI string "json:\"metadata_uri\""
	MetadataSHA string "json:\"metadata_sha256\""
}

// Root records the exact resolved version for a Product Definition root.
type Root struct {
	ComponentID string "json:\"component_id\""
	Variant     string "json:\"variant\""
	Version     string "json:\"version\""
}

// EnvironmentContract records a target-environment provider declared as data.
type EnvironmentContract struct {
	ContractID string "json:\"contract_id\""
	Version    string "json:\"version\""
}

// Component records one selected immutable Component Release.
type Component struct {
	ComponentID        string            `json:"component_id"`
	Variant            string            `json:"variant"`
	Version            string            `json:"version"`
	ReleaseMetadataURI string            `json:"release_metadata_uri"`
	ReleaseMetadataSHA string            `json:"release_metadata_sha256"`
	Delivery           string            `json:"delivery,omitempty"`
	SelectedArtifact   *SelectedArtifact `json:"selected_artifact,omitempty"`
	SourceUnits        []SourceUnit      `json:"source_units,omitempty"`
	Dependencies       []Dependency      `json:"dependencies"`
	Contracts          catalog.Contracts `json:"contracts,omitempty"`
}

// ArtifactPackage is the verified package directory staged by Workspace.
type ArtifactPackage struct {
	ComponentID   string
	Variant       string
	ArtifactID    string
	WorkspacePath string
}

// SourceProvenance preserves immutable source identity without a source checkout.
type SourceProvenance struct {
	SourceUnitID  string `json:"source_unit_id"`
	RepositoryURI string `json:"repository_uri"`
	Revision      string `json:"revision"`
	Subdirectory  string `json:"subdirectory,omitempty"`
}

// SelectedArtifact is the immutable release asset plus its staged workspace path.
type SelectedArtifact struct {
	catalog.Artifact
	WorkspacePath   string             `json:"workspace_path"`
	SourceProvenance []SourceProvenance `json:"source_provenance"`
}

// SourceUnit records the verified full Git commit and deterministic workspace path.
type SourceUnit struct {
	SourceUnitID  string "json:\"source_unit_id\""
	RepositoryURI string "json:\"repository_uri\""
	Revision      string "json:\"revision\""
	Subdirectory  string "json:\"subdirectory,omitempty\""
	WorkspacePath string "json:\"workspace_path\""
}

// Dependency records a concrete selected edge, not the original constraint.
type Dependency struct {
	ComponentID string "json:\"component_id\""
	Variant     string "json:\"variant\""
	Version     string "json:\"version\""
}

// Build creates a Manifest only when every selected source unit has a matching
// Collector result and each Catalog has immutable URI/digest provenance.
func Build(graph *resolver.Graph, collected []collector.Source, registryVersion string) (Document, error) {
	return BuildWithArtifacts(graph, collected, nil, registryVersion)
}

// BuildWithArtifacts creates a canonical Manifest only when each selected
// component has exactly the verified source or package materialization required.
func BuildWithArtifacts(graph *resolver.Graph, collected []collector.Source, packages []ArtifactPackage, registryVersion string) (Document, error) {
	if graph == nil {
		return Document{}, fmt.Errorf("manifest: a complete resolved graph is required")
	}
	if len(graph.Roots) == 0 || len(graph.Components) == 0 {
		return Document{}, fmt.Errorf("manifest: graph must contain at least one root and one selected component")
	}
	if _, err := semver.Parse(registryVersion); err != nil {
		return Document{}, fmt.Errorf("manifest: invalid Registry version %q: %w", registryVersion, err)
	}

	collectedByKey := make(map[string]collector.Source, len(collected))
	for _, source := range collected {
		key := sourceKey(source.ComponentID, source.Variant, source.SourceUnitID)
		if _, exists := collectedByKey[key]; exists {
			return Document{}, fmt.Errorf("manifest: duplicate Collector record for %s", key)
		}
		if !safeWorkspacePath(source.WorkspacePath) {
			return Document{}, fmt.Errorf("manifest: unsafe Collector workspace path %q", source.WorkspacePath)
		}
		collectedByKey[key] = source
	}
	packageByKey := make(map[string]ArtifactPackage, len(packages))
	for _, item := range packages {
		key := componentKey(item.ComponentID, item.Variant)
		if _, exists := packageByKey[key]; exists {
			return Document{}, fmt.Errorf("manifest: duplicate artifact package materialization for %s", key)
		}
		if item.ArtifactID == "" || !safeWorkspacePath(item.WorkspacePath) {
			return Document{}, fmt.Errorf("manifest: artifact materialization identity or path is invalid")
		}
		packageByKey[key] = item
	}

	environmentContracts := make([]EnvironmentContract, 0, len(graph.EnvironmentContracts))
	for _, contract := range graph.EnvironmentContracts {
		environmentContracts = append(environmentContracts, EnvironmentContract{
			ContractID: contract.ContractID,
			Version:    contract.Version,
		})
	}
	documentSchema := graph.SchemaVersion
	if documentSchema == "" {
		documentSchema = schemaVersion
	}
	doc := Document{
		SchemaVersion:        documentSchema,
		RegistryVersion:      registryVersion,
		Resolution:           nil,
		Catalogs:             make([]Catalog, 0, len(graph.Catalogs)),
		Roots:                make([]Root, 0, len(graph.Roots)),
		EnvironmentContracts: environmentContracts,
		Components:           make([]Component, 0, len(graph.Components)),
		ResolutionOrder:      append([]resolver.Node(nil), graph.ResolutionOrder...),
	}
	if graph.Resolution.DeliveryMode != "" {
		resolution := graph.Resolution
		doc.Resolution = &resolution
	}

	for _, source := range graph.Catalogs {
		if source.CatalogID == "" || source.MetadataURI == "" || !validSHA256(source.MetadataSHA) {
			return Document{}, fmt.Errorf("manifest: catalog %q lacks immutable URI/digest provenance", source.CatalogID)
		}
		parsed, err := url.Parse(source.MetadataURI)
		if err != nil || !parsed.IsAbs() {
			return Document{}, fmt.Errorf("manifest: catalog %q has invalid metadata URI", source.CatalogID)
		}
		doc.Catalogs = append(doc.Catalogs, Catalog{
			CatalogID:   source.CatalogID,
			MetadataURI: source.MetadataURI,
			MetadataSHA: strings.ToLower(source.MetadataSHA),
		})
	}

	selected := make(map[string]resolver.ResolvedComponent, len(graph.Components))
	for _, component := range graph.Components {
		key := componentKey(component.ComponentID, component.Variant)
		if _, exists := selected[key]; exists {
			return Document{}, fmt.Errorf("manifest: duplicate resolved component %s", key)
		}
		selected[key] = component
	}

	for _, root := range graph.Roots {
		variant := root.Variant
		if variant == "" {
			variant = "default"
		}
		resolved, ok := selected[componentKey(root.ComponentID, variant)]
		if !ok {
			return Document{}, fmt.Errorf("manifest: root %s/%s is absent from resolved graph", root.ComponentID, variant)
		}
		doc.Roots = append(doc.Roots, Root{
			ComponentID: root.ComponentID,
			Variant:     variant,
			Version:     resolved.Version,
		})
	}

	for _, resolved := range graph.Components {
		contracts := resolved.Contracts
		if contracts.Provides == nil {
			contracts.Provides = []catalog.ContractProvider{}
		}
		if contracts.Requires == nil {
			contracts.Requires = []catalog.ContractRequirement{}
		}
		delivery := resolved.Delivery
		if delivery == "" {
			delivery = "source"
		}
		if delivery != "source" && delivery != "artifact" {
			return Document{}, fmt.Errorf("manifest: component %s/%s has an invalid delivery mode", resolved.ComponentID, resolved.Variant)
		}
		if delivery == "source" && len(resolved.SourceUnits) == 0 {
			return Document{}, fmt.Errorf("manifest: selected source component %s/%s has no source units", resolved.ComponentID, resolved.Variant)
		}
		component := Component{
			ComponentID:        resolved.ComponentID,
			Variant:            resolved.Variant,
			Version:            resolved.Version,
			ReleaseMetadataURI: resolved.ReleaseMetadataURI,
			ReleaseMetadataSHA: strings.ToLower(resolved.ReleaseMetadataSHA),
			Dependencies:       make([]Dependency, 0, len(resolved.Dependencies)),
			Contracts:          contracts,
		}
		if documentSchema != schemaVersion {
			component.Delivery = delivery
		}
		if component.ReleaseMetadataURI == "" || !validSHA256(component.ReleaseMetadataSHA) {
			return Document{}, fmt.Errorf("manifest: release %s/%s@%s has no verified metadata identity", resolved.ComponentID, resolved.Variant, resolved.Version)
		}
		if parsed, err := url.Parse(component.ReleaseMetadataURI); err != nil || !parsed.IsAbs() {
			return Document{}, fmt.Errorf("manifest: release %s has invalid metadata URI", resolved.ComponentID)
		}

		if delivery == "source" {
			component.SourceUnits = make([]SourceUnit, 0, len(resolved.SourceUnits))
			for _, source := range resolved.SourceUnits {
				key := sourceKey(resolved.ComponentID, resolved.Variant, source.ID)
				collectedSource, ok := collectedByKey[key]
				if !ok {
					return Document{}, fmt.Errorf("manifest: source unit %s has no verified Collector result", key)
				}
				if strings.ToLower(collectedSource.Revision) != strings.ToLower(source.Revision) {
					return Document{}, fmt.Errorf("manifest: Collector revision mismatch for %s", key)
				}
				expectedPath := fmt.Sprintf("components/%s/%s/sources/%s", resolved.ComponentID, resolved.Variant, source.ID)
				if collectedSource.WorkspacePath != expectedPath {
					return Document{}, fmt.Errorf("manifest: non-canonical workspace path for %s: %q", key, collectedSource.WorkspacePath)
				}
				component.SourceUnits = append(component.SourceUnits, SourceUnit{
					SourceUnitID: source.ID, RepositoryURI: source.RepositoryURI,
					Revision: strings.ToLower(collectedSource.Revision),
					Subdirectory: source.Subdirectory, WorkspacePath: expectedPath,
				})
				delete(collectedByKey, key)
			}
			sort.Slice(component.SourceUnits, func(i, j int) bool {
				return component.SourceUnits[i].SourceUnitID < component.SourceUnits[j].SourceUnitID
			})
		} else {
			if resolved.SelectedArtifact == nil {
				return Document{}, fmt.Errorf("manifest: artifact-delivered component %s/%s has no selected artifact", resolved.ComponentID, resolved.Variant)
			}
			key := componentKey(resolved.ComponentID, resolved.Variant)
			materialized, ok := packageByKey[key]
			if !ok || materialized.ArtifactID != resolved.SelectedArtifact.ID {
				return Document{}, fmt.Errorf("manifest: selected artifact for %s/%s was not materialized", resolved.ComponentID, resolved.Variant)
			}
			expectedPath := fmt.Sprintf("components/%s/%s/package", resolved.ComponentID, resolved.Variant)
			if materialized.WorkspacePath != expectedPath {
				return Document{}, fmt.Errorf("manifest: non-canonical artifact workspace path for %s/%s", resolved.ComponentID, resolved.Variant)
			}
			sourceByID := make(map[string]catalog.SourceUnit, len(resolved.SourceUnits))
			for _, source := range resolved.SourceUnits {
				sourceByID[source.ID] = source
			}
			provenance := make([]SourceProvenance, 0, len(resolved.SelectedArtifact.SourceUnitIDs))
			seenProvenance := make(map[string]bool)
			for _, sourceID := range resolved.SelectedArtifact.SourceUnitIDs {
				source, ok := sourceByID[sourceID]
				if !ok || seenProvenance[sourceID] {
					return Document{}, fmt.Errorf("manifest: artifact source provenance is inconsistent")
				}
				seenProvenance[sourceID] = true
				provenance = append(provenance, SourceProvenance{
					SourceUnitID: source.ID, RepositoryURI: source.RepositoryURI,
					Revision: strings.ToLower(source.Revision), Subdirectory: source.Subdirectory,
				})
			}
			if !seenProvenance[resolved.SelectedArtifact.Build.Recipe.SourceUnitID] {
				return Document{}, fmt.Errorf("manifest: artifact build recipe source is not in provenance")
			}
			sort.Slice(provenance, func(i, j int) bool { return provenance[i].SourceUnitID < provenance[j].SourceUnitID })
			component.SelectedArtifact = &SelectedArtifact{
				Artifact: *resolved.SelectedArtifact,
				WorkspacePath: expectedPath,
				SourceProvenance: provenance,
			}
			delete(packageByKey, key)
		}

		for _, dependency := range resolved.Dependencies {
			component.Dependencies = append(component.Dependencies, Dependency{
				ComponentID: dependency.ComponentID,
				Variant:     dependency.Variant,
				Version:     dependency.Version,
			})
		}
		sort.Slice(component.Dependencies, func(i, j int) bool {
			if component.Dependencies[i].ComponentID != component.Dependencies[j].ComponentID {
				return component.Dependencies[i].ComponentID < component.Dependencies[j].ComponentID
			}
			return component.Dependencies[i].Variant < component.Dependencies[j].Variant
		})
		sort.Slice(component.Contracts.Provides, func(i, j int) bool {
			if component.Contracts.Provides[i].ContractID != component.Contracts.Provides[j].ContractID {
				return component.Contracts.Provides[i].ContractID < component.Contracts.Provides[j].ContractID
			}
			return component.Contracts.Provides[i].Version < component.Contracts.Provides[j].Version
		})
		sort.Slice(component.Contracts.Requires, func(i, j int) bool {
			if component.Contracts.Requires[i].ContractID != component.Contracts.Requires[j].ContractID {
				return component.Contracts.Requires[i].ContractID < component.Contracts.Requires[j].ContractID
			}
			return component.Contracts.Requires[i].VersionConstraint < component.Contracts.Requires[j].VersionConstraint
		})
		doc.Components = append(doc.Components, component)
	}

	if len(packageByKey) != 0 {
		return Document{}, fmt.Errorf("manifest: unclaimed artifact package materializations remain")
	}
	if len(collectedByKey) != 0 {
		extra := make([]string, 0, len(collectedByKey))
		for key := range collectedByKey {
			extra = append(extra, key)
		}
		sort.Strings(extra)
		return Document{}, fmt.Errorf("manifest: undeclared Collector source units: %s", strings.Join(extra, ", "))
	}

	sort.Slice(doc.Catalogs, func(i, j int) bool {
		return doc.Catalogs[i].CatalogID < doc.Catalogs[j].CatalogID
	})
	sort.Slice(doc.Roots, func(i, j int) bool {
		if doc.Roots[i].ComponentID != doc.Roots[j].ComponentID {
			return doc.Roots[i].ComponentID < doc.Roots[j].ComponentID
		}
		return doc.Roots[i].Variant < doc.Roots[j].Variant
	})
	sort.Slice(doc.EnvironmentContracts, func(i, j int) bool {
		if doc.EnvironmentContracts[i].ContractID != doc.EnvironmentContracts[j].ContractID {
			return doc.EnvironmentContracts[i].ContractID < doc.EnvironmentContracts[j].ContractID
		}
		return doc.EnvironmentContracts[i].Version < doc.EnvironmentContracts[j].Version
	})
	sort.Slice(doc.Components, func(i, j int) bool {
		if doc.Components[i].ComponentID != doc.Components[j].ComponentID {
			return doc.Components[i].ComponentID < doc.Components[j].ComponentID
		}
		return doc.Components[i].Variant < doc.Components[j].Variant
	})
	return doc, nil
}

// MarshalCanonical emits RFC 8785 canonical JSON. Property names are ordered
// by UTF-16 code units and numbers use ECMAScript-compatible binary64 formatting.
func MarshalCanonical(value any) ([]byte, error) {
	if err := validateJSONStrings(reflect.ValueOf(value)); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("manifest: encode JSON value: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("manifest: decode intermediate JSON: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("manifest: unexpected trailing JSON value")
	}
	var output bytes.Buffer
	if err := appendCanonical(&output, decoded); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// ParseCanonicalStringOnly verifies that input is already RFC 8785 canonical
// JSON. Unlike MarshalCanonical, this function never normalizes caller bytes:
// whitespace, property-order differences, duplicate object names, non-canonical
// number spellings and trailing values are rejected by the byte-for-byte check.
// The decoder preserves number tokens so comparison uses the same JCS rules as
// the serializer. This is the verification boundary for persisted/replayed
// Manifest and Report bytes (REG-024); see RFC 8785:
// https://www.rfc-editor.org/rfc/rfc8785.
func ParseCanonicalStringOnly(input []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("manifest: decode canonical JSON: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return fmt.Errorf("manifest: canonical JSON contains a trailing value")
		}
		return fmt.Errorf("manifest: canonical JSON has trailing data: %w", err)
	}

	var canonical bytes.Buffer
	if err := appendCanonical(&canonical, decoded); err != nil {
		return err
	}
	if !bytes.Equal(input, canonical.Bytes()) {
		return fmt.Errorf("manifest: input is not RFC 8785 canonical JSON")
	}
	return nil
}

// Verify checks a persisted Resolved Manifest without fetching metadata,
// selecting releases, collecting sources or writing to a Workspace. The
// detached digest is optional because it is stored outside the Manifest bytes;
// when supplied, it must be the lowercase SHA-256 of the exact input bytes.
// A nil Validator initializes the embedded Spec-owned schemas locally (REG-023/024).
// Spec: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#7-resolved-manifest-and-resolution-report
// Design: https://github.com/cdin-editor/registry/blob/main/docs/implementation/manifest.md.
func Verify(canonicalBytes []byte, expectedSHA256 string, validator *validation.Validator) error {
	if len(canonicalBytes) == 0 {
		return fmt.Errorf("manifest: input is empty")
	}
	if err := ParseCanonicalStringOnly(canonicalBytes); err != nil {
		return &VerificationError{Stage: VerificationStageCanonicalBytes, cause: err}
	}
	if expectedSHA256 != "" {
		if !validSHA256(expectedSHA256) || expectedSHA256 != strings.ToLower(expectedSHA256) {
			return fmt.Errorf("manifest: expected detached digest must be lowercase SHA-256")
		}
		if actual := Digest(canonicalBytes); actual != expectedSHA256 {
			return fmt.Errorf("manifest: detached SHA-256 mismatch")
		}
	}
	if validator == nil {
		var err error
		validator, err = validation.New()
		if err != nil {
			return fmt.Errorf("manifest: initialize Spec schema validator: %w", err)
		}
	}
	if err := validator.Validate(validation.ResolvedManifest, canonicalBytes, "registry-manifest.json"); err != nil {
		return &VerificationError{Stage: VerificationStagePublicSchema, cause: err}
	}
	var document Document
	if err := json.Unmarshal(canonicalBytes, &document); err != nil {
		return fmt.Errorf("manifest: decode verified document: %w", err)
	}
	if err := validateDocument(document); err != nil {
		return &VerificationError{Stage: VerificationStageProvenance, cause: err}
	}
	return nil
}

func validateDocument(document Document) error {
	if document.SchemaVersion != schemaVersion && document.SchemaVersion != "1.1.0" {
		return fmt.Errorf("unsupported Manifest schema version %q", document.SchemaVersion)
	}
	artifactManifest := document.SchemaVersion == "1.1.0"
	if artifactManifest {
		if document.Resolution == nil {
			return fmt.Errorf("artifact Manifest is missing its resolution policy")
		}
		mode := document.Resolution.DeliveryMode
		if mode != "source" && mode != "artifact" && mode != "artifact-or-source" {
			return fmt.Errorf("artifact Manifest has an invalid delivery mode")
		}
		if mode != "source" {
			if document.Resolution.Target == nil || document.Resolution.Target.Validate() != nil || document.Resolution.Target.IsUniversal() {
				return fmt.Errorf("artifact Manifest has an invalid target tuple")
			}
		} else if document.Resolution.Target != nil && document.Resolution.Target.Validate() != nil {
			return fmt.Errorf("source Manifest has an invalid optional target tuple")
		}
	} else if document.Resolution != nil {
		return fmt.Errorf("legacy source Manifest cannot contain a delivery policy")
	}
	if _, err := semver.Parse(document.RegistryVersion); err != nil {
		return fmt.Errorf("invalid Registry version: %w", err)
	}
	if len(document.Roots) == 0 || len(document.Components) == 0 {
		return fmt.Errorf("Manifest must contain at least one root and one selected component")
	}
	if err := requireStrictOrder(document.Catalogs, func(a, b Catalog) bool { return a.CatalogID < b.CatalogID }, "catalogs"); err != nil {
		return err
	}
	catalogIDs := make(map[string]struct{}, len(document.Catalogs))
	for _, catalog := range document.Catalogs {
		if catalog.CatalogID == "" {
			return fmt.Errorf("catalog ID is required")
		}
		if _, exists := catalogIDs[catalog.CatalogID]; exists {
			return fmt.Errorf("duplicate Catalog ID %q", catalog.CatalogID)
		}
		catalogIDs[catalog.CatalogID] = struct{}{}
		if !lowerSHA256(catalog.MetadataSHA) {
			return fmt.Errorf("Catalog %q has missing or non-canonical metadata SHA-256", catalog.CatalogID)
		}
		if err := validateMetadataURI(catalog.MetadataURI); err != nil {
			return fmt.Errorf("Catalog %q metadata URI: %w", catalog.CatalogID, err)
		}
	}
	if err := requireStrictOrder(document.Roots, func(a, b Root) bool {
		if a.ComponentID != b.ComponentID {
			return a.ComponentID < b.ComponentID
		}
		return a.Variant < b.Variant
	}, "roots"); err != nil {
		return err
	}
	if err := requireStrictOrder(document.Components, func(a, b Component) bool {
		if a.ComponentID != b.ComponentID {
			return a.ComponentID < b.ComponentID
		}
		return a.Variant < b.Variant
	}, "components"); err != nil {
		return err
	}
	if err := requireStrictOrder(document.EnvironmentContracts, func(a, b EnvironmentContract) bool {
		if a.ContractID != b.ContractID {
			return a.ContractID < b.ContractID
		}
		return a.Version < b.Version
	}, "environment contracts"); err != nil {
		return err
	}

	components := make(map[string]Component, len(document.Components))
	workspacePaths := make(map[string]struct{})
	for _, component := range document.Components {
		key := componentKey(component.ComponentID, component.Variant)
		if _, exists := components[key]; exists {
			return fmt.Errorf("duplicate selected component %s/%s", component.ComponentID, component.Variant)
		}
		components[key] = component
		if !lowerSHA256(component.ReleaseMetadataSHA) {
			return fmt.Errorf("component %s/%s has missing or non-canonical release metadata SHA-256", component.ComponentID, component.Variant)
		}
		if err := validateMetadataURI(component.ReleaseMetadataURI); err != nil {
			return fmt.Errorf("component %s/%s release metadata URI: %w", component.ComponentID, component.Variant, err)
		}
		delivery := "source"
		if artifactManifest {
			delivery = component.Delivery
			if delivery != "source" && delivery != "artifact" {
				return fmt.Errorf("component %s/%s has invalid delivery mode", component.ComponentID, component.Variant)
			}
			switch document.Resolution.DeliveryMode {
			case "source":
				if delivery != "source" { return fmt.Errorf("source-only Manifest contains an artifact component") }
			case "artifact":
				if delivery != "artifact" { return fmt.Errorf("artifact-only Manifest contains a source component") }
			}
		} else if component.Delivery != "" || component.SelectedArtifact != nil {
			return fmt.Errorf("legacy source Manifest contains artifact delivery fields")
		}
		if delivery == "source" {
			if len(component.SourceUnits) == 0 || component.SelectedArtifact != nil {
				return fmt.Errorf("source component %s/%s has no sources or unexpectedly contains an artifact", component.ComponentID, component.Variant)
			}
			if err := requireStrictOrder(component.SourceUnits, func(a, b SourceUnit) bool {
				return a.SourceUnitID < b.SourceUnitID
			}, "source units for "+key); err != nil {
				return err
			}
		} else {
			if len(component.SourceUnits) != 0 || component.SelectedArtifact == nil {
				return fmt.Errorf("artifact component %s/%s has source checkout fields or no selected artifact", component.ComponentID, component.Variant)
			}
			if err := validateSelectedArtifact(component, *document.Resolution); err != nil {
				return fmt.Errorf("artifact component %s/%s: %w", component.ComponentID, component.Variant, err)
			}
			if _, exists := workspacePaths[component.SelectedArtifact.WorkspacePath]; exists {
				return fmt.Errorf("duplicate workspace path %q", component.SelectedArtifact.WorkspacePath)
			}
			workspacePaths[component.SelectedArtifact.WorkspacePath] = struct{}{}
		}
		if err := requireStrictOrder(component.Dependencies, func(a, b Dependency) bool {
			if a.ComponentID != b.ComponentID {
				return a.ComponentID < b.ComponentID
			}
			return a.Variant < b.Variant
		}, "dependencies for "+key); err != nil {
			return err
		}
		if err := requireStrictOrder(component.Contracts.Provides, func(a, b catalog.ContractProvider) bool {
			if a.ContractID != b.ContractID {
				return a.ContractID < b.ContractID
			}
			return a.Version < b.Version
		}, "provided contracts for "+key); err != nil {
			return err
		}
		if err := requireStrictOrder(component.Contracts.Requires, func(a, b catalog.ContractRequirement) bool {
			if a.ContractID != b.ContractID {
				return a.ContractID < b.ContractID
			}
			return a.VersionConstraint < b.VersionConstraint
		}, "required contracts for "+key); err != nil {
			return err
		}
		for _, source := range component.SourceUnits {
			if !fullGitObjectID(source.Revision) {
				return fmt.Errorf("source %s/%s/%s does not record a lowercase full Git object ID", component.ComponentID, component.Variant, source.SourceUnitID)
			}
			if err := validateRepositoryURI(source.RepositoryURI); err != nil {
				return fmt.Errorf("source %s/%s/%s repository URI: %w", component.ComponentID, component.Variant, source.SourceUnitID, err)
			}
			if source.Subdirectory != "" && !safeWorkspacePath(source.Subdirectory) {
				return fmt.Errorf("source %s/%s/%s has unsafe subdirectory %q", component.ComponentID, component.Variant, source.SourceUnitID, source.Subdirectory)
			}
			expectedPath := fmt.Sprintf("components/%s/%s/sources/%s", component.ComponentID, component.Variant, source.SourceUnitID)
			if !safeWorkspacePath(source.WorkspacePath) || source.WorkspacePath != expectedPath {
				return fmt.Errorf("source %s/%s/%s has non-canonical or unsafe workspace path %q", component.ComponentID, component.Variant, source.SourceUnitID, source.WorkspacePath)
			}
			if _, exists := workspacePaths[source.WorkspacePath]; exists {
				return fmt.Errorf("duplicate workspace path %q", source.WorkspacePath)
			}
			workspacePaths[source.WorkspacePath] = struct{}{}
		}
	}
	for _, root := range document.Roots {
		component, exists := components[componentKey(root.ComponentID, root.Variant)]
		if !exists {
			return fmt.Errorf("root %s/%s is absent from selected components", root.ComponentID, root.Variant)
		}
		if root.Version != component.Version {
			return fmt.Errorf("root %s/%s version %q does not match selected version %q", root.ComponentID, root.Variant, root.Version, component.Version)
		}
	}

	order := make(map[string]int, len(document.ResolutionOrder))
	if len(document.ResolutionOrder) != len(document.Components) {
		return fmt.Errorf("resolution order contains %d entries for %d selected components", len(document.ResolutionOrder), len(document.Components))
	}
	for index, node := range document.ResolutionOrder {
		key := componentKey(node.ComponentID, node.Variant)
		component, exists := components[key]
		if !exists || component.Version != node.Version {
			return fmt.Errorf("resolution order entry %s/%s@%s does not match a selected component", node.ComponentID, node.Variant, node.Version)
		}
		if _, exists := order[key]; exists {
			return fmt.Errorf("duplicate resolution order entry %s/%s", node.ComponentID, node.Variant)
		}
		order[key] = index
	}
	for _, component := range document.Components {
		key := componentKey(component.ComponentID, component.Variant)
		for _, dependency := range component.Dependencies {
			dependencyKey := componentKey(dependency.ComponentID, dependency.Variant)
			selected, exists := components[dependencyKey]
			if !exists || selected.Version != dependency.Version {
				return fmt.Errorf("dependency %s/%s@%s from %s does not match a selected component", dependency.ComponentID, dependency.Variant, dependency.Version, key)
			}
			if order[dependencyKey] >= order[key] {
				return fmt.Errorf("resolution order places dependency %s before its dependent %s incorrectly", dependencyKey, key)
			}
		}
	}
	return nil
}

func validateSelectedArtifact(component Component, resolution resolver.ResolutionPolicy) error {
	selected := component.SelectedArtifact
	if selected == nil {
		return fmt.Errorf("selected artifact is required")
	}
	expectedPath := fmt.Sprintf("components/%s/%s/package", component.ComponentID, component.Variant)
	if !safeWorkspacePath(selected.WorkspacePath) || selected.WorkspacePath != expectedPath {
		return fmt.Errorf("artifact package path is not canonical")
	}
	if !lowerSHA256(selected.SHA256) {
		return fmt.Errorf("artifact digest is missing or non-canonical")
	}
	if resolution.Target == nil {
		return fmt.Errorf("artifact target cannot be checked without a resolved target")
	}
	if selected.Target != *resolution.Target && !selected.Target.IsUniversal() {
		return fmt.Errorf("selected artifact target does not match the resolved target")
	}
	if err := requireStrictOrder(selected.SourceProvenance, func(a,b SourceProvenance) bool {
		return a.SourceUnitID < b.SourceUnitID
	}, "artifact source provenance"); err != nil {
		return err
	}
	sourceUnits := make([]catalog.SourceUnit, 0, len(selected.SourceProvenance))
	provenanceIDs := make(map[string]bool, len(selected.SourceProvenance))
	for _, source := range selected.SourceProvenance {
		if !fullGitObjectID(source.Revision) {
			return fmt.Errorf("artifact source provenance has an invalid immutable revision")
		}
		if err := validateRepositoryURI(source.RepositoryURI); err != nil {
			return fmt.Errorf("artifact source provenance repository URI: %w", err)
		}
		if source.Subdirectory != "" && !safeWorkspacePath(source.Subdirectory) {
			return fmt.Errorf("artifact source provenance has an unsafe subdirectory")
		}
		if provenanceIDs[source.SourceUnitID] {
			return fmt.Errorf("artifact source provenance contains duplicates")
		}
		provenanceIDs[source.SourceUnitID] = true
		sourceUnits = append(sourceUnits, catalog.SourceUnit{
			ID: source.SourceUnitID, RepositoryURI: source.RepositoryURI,
			Revision: source.Revision, Subdirectory: source.Subdirectory,
		})
	}
	if len(sourceUnits) != len(selected.SourceUnitIDs) {
		return fmt.Errorf("artifact source provenance does not cover every source unit")
	}
	for _, id := range selected.SourceUnitIDs {
		if !provenanceIDs[id] {
			return fmt.Errorf("artifact source provenance does not match the artifact source-unit set")
		}
	}
	if !provenanceIDs[selected.Build.Recipe.SourceUnitID] {
		return fmt.Errorf("artifact build recipe is not covered by source provenance")
	}
	if err := catalog.ValidateReleaseArtifacts(sourceUnits, []catalog.Artifact{selected.Artifact}); err != nil {
		return fmt.Errorf("artifact descriptor is invalid: %w", err)
	}
	return nil
}

func requireStrictOrder[T any](values []T, less func(T, T) bool, field string) error {
	for i := 1; i < len(values); i++ {
		if !less(values[i-1], values[i]) {
			return fmt.Errorf("%s must be strictly sorted and contain no duplicate identities", field)
		}
	}
	return nil
}

func lowerSHA256(value string) bool {
	return validSHA256(value) && value == strings.ToLower(value)
}

func fullGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func validateMetadataURI(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.User != nil {
		return fmt.Errorf("must be an absolute URI without embedded credentials")
	}
	switch parsed.Scheme {
	case "https":
		if parsed.Host == "" {
			return fmt.Errorf("HTTPS URI must include a host")
		}
	case "file":
		if parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") {
			return fmt.Errorf("file URI must have an absolute local path")
		}
	default:
		return fmt.Errorf("unsupported metadata URI scheme %q", parsed.Scheme)
	}
	return nil
}

func validateRepositoryURI(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("must be an absolute HTTPS repository URI without embedded credentials")
	}
	return nil
}

// MarshalReport sorts set-like report arrays before canonical serialization.
// CyclePath remains ordered because its sequence describes a graph walk.
func MarshalReport(report resolver.Report) ([]byte, error) {
	report.Selected = append([]resolver.SelectedCandidate{}, report.Selected...)
	report.AppliedConstraints = append([]resolver.AppliedConstraint{}, report.AppliedConstraints...)
	report.RejectedCandidates = append([]resolver.RejectedCandidate{}, report.RejectedCandidates...)
	report.Conflicts = append([]resolver.Conflict{}, report.Conflicts...)
	report.Warnings = append([]resolver.Warning{}, report.Warnings...)
	sort.Slice(report.Selected, func(i, j int) bool {
		a, b := report.Selected[i], report.Selected[j]
		if a.ComponentID != b.ComponentID {
			return a.ComponentID < b.ComponentID
		}
		if a.Variant != b.Variant {
			return a.Variant < b.Variant
		}
		return a.Version < b.Version
	})
	sort.Slice(report.AppliedConstraints, func(i, j int) bool {
		a, b := report.AppliedConstraints[i], report.AppliedConstraints[j]
		if a.ComponentID != b.ComponentID {
			return a.ComponentID < b.ComponentID
		}
		if a.Variant != b.Variant {
			return a.Variant < b.Variant
		}
		if a.VersionConstraint != b.VersionConstraint {
			return a.VersionConstraint < b.VersionConstraint
		}
		if a.Source.Type != b.Source.Type {
			return a.Source.Type < b.Source.Type
		}
		if a.Source.ComponentID != b.Source.ComponentID {
			return a.Source.ComponentID < b.Source.ComponentID
		}
		if a.Source.Variant != b.Source.Variant {
			return a.Source.Variant < b.Source.Variant
		}
		return a.Source.Version < b.Source.Version
	})
	sort.Slice(report.RejectedCandidates, func(i, j int) bool {
		a, b := report.RejectedCandidates[i], report.RejectedCandidates[j]
		if a.ComponentID != b.ComponentID {
			return a.ComponentID < b.ComponentID
		}
		if a.Variant != b.Variant {
			return a.Variant < b.Variant
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		return a.ReasonCode < b.ReasonCode
	})
	for i := range report.Conflicts {
		report.Conflicts[i].InvolvedComponents = append([]string(nil), report.Conflicts[i].InvolvedComponents...)
		sort.Strings(report.Conflicts[i].InvolvedComponents)
	}
	sort.Slice(report.Conflicts, func(i, j int) bool {
		if report.Conflicts[i].Code != report.Conflicts[j].Code {
			return report.Conflicts[i].Code < report.Conflicts[j].Code
		}
		return report.Conflicts[i].Message < report.Conflicts[j].Message
	})
	sort.Slice(report.Warnings, func(i, j int) bool {
		if report.Warnings[i].Code != report.Warnings[j].Code {
			return report.Warnings[i].Code < report.Warnings[j].Code
		}
		return report.Warnings[i].Message < report.Warnings[j].Message
	})
	return MarshalCanonical(report)
}

// Digest returns the detached SHA-256 of canonical document bytes. The digest
// is stored alongside or signed over the Manifest, never embedded in itself.
func Digest(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// validateJSONStrings rejects invalid UTF-8 before encoding/json can replace it
// with U+FFFD. JCS requires Unicode scalar strings and must preserve the
// original code points without normalization or lossy replacement.
// jsonValueVisit identifies reference-backed JSON values during the UTF-8
// preflight. Slices include their visible bounds so distinct views over the same
// backing array are not mistaken for the same graph node.
type jsonValueVisit struct {
	typeOfValue reflect.Type
	pointer     uintptr
	length      int
	capacity    int
}

func validateJSONStrings(value reflect.Value) error {
	return validateJSONStringsVisited(value, make(map[jsonValueVisit]struct{}))
}

func validateJSONStringsVisited(value reflect.Value, visited map[jsonValueVisit]struct{}) error {
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return nil
		}
		return validateJSONStringsVisited(value.Elem(), visited)
	case reflect.Pointer, reflect.Map, reflect.Slice:
		if value.IsNil() {
			return nil
		}
		if value.Kind() == reflect.Slice && value.Len() == 0 {
			return nil
		}
		identity := jsonValueVisit{typeOfValue: value.Type(), pointer: uintptr(value.UnsafePointer())}
		if value.Kind() == reflect.Slice {
			identity.length = value.Len()
			identity.capacity = value.Cap()
		}
		if identity.pointer != 0 {
			if _, exists := visited[identity]; exists {
				// The JSON encoder will return its standard cycle error after
				// preflight; returning here avoids unbounded recursive descent.
				return nil
			}
			visited[identity] = struct{}{}
		}
		switch value.Kind() {
		case reflect.Pointer:
			return validateJSONStringsVisited(value.Elem(), visited)
		case reflect.Map:
			iter := value.MapRange()
			for iter.Next() {
				key := iter.Key()
				if key.Kind() == reflect.String && !utf8.ValidString(key.String()) {
					return fmt.Errorf("manifest: JSON object key contains invalid UTF-8")
				}
				if err := validateJSONStringsVisited(iter.Value(), visited); err != nil {
					return err
				}
			}
		case reflect.Slice:
			for i := 0; i < value.Len(); i++ {
				if err := validateJSONStringsVisited(value.Index(i), visited); err != nil {
					return err
				}
			}
		}
	case reflect.String:
		if !utf8.ValidString(value.String()) {
			return fmt.Errorf("manifest: JSON string contains invalid UTF-8")
		}
	case reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if err := validateJSONStringsVisited(value.Index(i), visited); err != nil {
				return err
			}
		}
	case reflect.Struct:
		typeOfValue := value.Type()
		for i := 0; i < value.NumField(); i++ {
			field := typeOfValue.Field(i)
			if field.PkgPath != "" || field.Tag.Get("json") == "-" {
				continue
			}
			if err := validateJSONStringsVisited(value.Field(i), visited); err != nil {
				return err
			}
		}
	}
	return nil
}

func appendCanonical(out *bytes.Buffer, value any) error {
	switch item := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		if item {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case string:
		appendJSONString(out, item)
	case json.Number:
		canonical, err := canonicalNumber(item.String())
		if err != nil {
			return err
		}
		out.WriteString(canonical)
	case []any:
		out.WriteByte('[')
		for i, child := range item {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := appendCanonical(out, child); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(item))
		for key := range item {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return lessUTF16(keys[i], keys[j]) })
		out.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			appendJSONString(out, key)
			out.WriteByte(':')
			if err := appendCanonical(out, item[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		return fmt.Errorf("manifest: unsupported JSON value type %T", value)
	}
	return nil
}

// canonicalNumber converts a JSON number through IEEE-754 binary64 and emits
// the shortest round-trippable ECMAScript decimal representation required by
// RFC 8785. Fixed notation is used for 1e-6 <= abs(x) < 1e21; other values use
// normalized exponent notation with an explicit plus sign for positive powers.
func canonicalNumber(raw string) (string, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return "", fmt.Errorf("manifest: JSON number is outside the finite IEEE-754 range: %q", raw)
	}
	if value == 0 {
		return "0", nil
	}

	negative := math.Signbit(value)
	absolute := math.Abs(value)
	scientific := strconv.FormatFloat(absolute, 'e', -1, 64)
	parts := strings.Split(scientific, "e")
	if len(parts) != 2 {
		return "", fmt.Errorf("manifest: cannot normalize JSON number %q", raw)
	}
	exponent, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", fmt.Errorf("manifest: invalid normalized number exponent %q: %w", parts[1], err)
	}
	digits := strings.ReplaceAll(parts[0], ".", "")
	if digits == "" {
		return "", fmt.Errorf("manifest: invalid normalized number mantissa %q", parts[0])
	}

	var result string
	if absolute >= 1e-6 && absolute < 1e21 {
		decimalPosition := exponent + 1
		switch {
		case decimalPosition <= 0:
			result = "0." + strings.Repeat("0", -decimalPosition) + digits
		case decimalPosition >= len(digits):
			result = digits + strings.Repeat("0", decimalPosition-len(digits))
		default:
			result = digits[:decimalPosition] + "." + digits[decimalPosition:]
		}
	} else {
		result = digits[:1]
		if len(digits) > 1 {
			result += "." + digits[1:]
		}
		result += "e"
		if exponent >= 0 {
			result += "+"
		}
		result += strconv.Itoa(exponent)
	}
	if negative {
		result = "-" + result
	}
	return result, nil
}

// appendJSONString applies only the minimal JSON escapes required by JCS and
// does not use encoding/json HTML escaping.
func appendJSONString(out *bytes.Buffer, value string) {
	const hexDigits = "0123456789abcdef"
	out.WriteByte('"')
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		value = value[size:]
		switch r {
		case '"':
			out.WriteString("\\\"")
		case '\\':
			out.WriteString("\\\\")
		case '\b':
			out.WriteString("\\b")
		case '\t':
			out.WriteString("\\t")
		case '\n':
			out.WriteString("\\n")
		case '\f':
			out.WriteString("\\f")
		case '\r':
			out.WriteString("\\r")
		default:
			if r < 0x20 {
				out.WriteString("\\u00")
				out.WriteByte(hexDigits[(r>>4)&0xf])
				out.WriteByte(hexDigits[r&0xf])
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
}

// lessUTF16 implements the object-key ordering required by RFC 8785.
func lessUTF16(left, right string) bool {
	a := utf16.Encode([]rune(left))
	b := utf16.Encode([]rune(right))
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func componentKey(component, variant string) string {
	return component + "\x00" + variant
}

func sourceKey(component, variant, unit string) string {
	return componentKey(component, variant) + "\x00" + unit
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

func safeWorkspacePath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
