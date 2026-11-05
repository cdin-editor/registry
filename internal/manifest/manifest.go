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
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/collector"
	"github.com/cdin-editor/registry/internal/resolver"
	"github.com/cdin-editor/registry/internal/semver"
)

const schemaVersion = "1.0.0"

// Document is the public Resolved Manifest v1 data model. It is a
// reproducibility record, not a mutable product configuration or lockfile.
type Document struct {
	SchemaVersion        string                "json:\"schema_version\""
	RegistryVersion      string                "json:\"registry_version\""
	Catalogs             []Catalog             "json:\"catalogs\""
	Roots                []Root                "json:\"roots\""
	EnvironmentContracts []EnvironmentContract "json:\"environment_contracts,omitempty\""
	Components           []Component           "json:\"components\""
	ResolutionOrder      []resolver.Node       "json:\"resolution_order\""
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
	ComponentID        string            "json:\"component_id\""
	Variant            string            "json:\"variant\""
	Version            string            "json:\"version\""
	ReleaseMetadataURI string            "json:\"release_metadata_uri\""
	ReleaseMetadataSHA string            "json:\"release_metadata_sha256\""
	SourceUnits        []SourceUnit      "json:\"source_units\""
	Dependencies       []Dependency      "json:\"dependencies\""
	Contracts          catalog.Contracts "json:\"contracts,omitempty\""
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

	environmentContracts := make([]EnvironmentContract, 0, len(graph.EnvironmentContracts))
	for _, contract := range graph.EnvironmentContracts {
		environmentContracts = append(environmentContracts, EnvironmentContract{
			ContractID: contract.ContractID,
			Version:    contract.Version,
		})
	}
	doc := Document{
		SchemaVersion:        schemaVersion,
		RegistryVersion:      registryVersion,
		Catalogs:             make([]Catalog, 0, len(graph.Catalogs)),
		Roots:                make([]Root, 0, len(graph.Roots)),
		EnvironmentContracts: environmentContracts,
		Components:           make([]Component, 0, len(graph.Components)),
		ResolutionOrder:      append([]resolver.Node(nil), graph.ResolutionOrder...),
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
		if len(resolved.SourceUnits) == 0 {
			return Document{}, fmt.Errorf("manifest: selected component %s/%s has no source units", resolved.ComponentID, resolved.Variant)
		}
		component := Component{
			ComponentID:        resolved.ComponentID,
			Variant:            resolved.Variant,
			Version:            resolved.Version,
			ReleaseMetadataURI: resolved.ReleaseMetadataURI,
			ReleaseMetadataSHA: strings.ToLower(resolved.ReleaseMetadataSHA),
			SourceUnits:        make([]SourceUnit, 0, len(resolved.SourceUnits)),
			Dependencies:       make([]Dependency, 0, len(resolved.Dependencies)),
			Contracts:          contracts,
		}
		if component.ReleaseMetadataURI == "" || !validSHA256(component.ReleaseMetadataSHA) {
			return Document{}, fmt.Errorf("manifest: release %s/%s@%s has no verified metadata identity", resolved.ComponentID, resolved.Variant, resolved.Version)
		}
		if parsed, err := url.Parse(component.ReleaseMetadataURI); err != nil || !parsed.IsAbs() {
			return Document{}, fmt.Errorf("manifest: release %s has invalid metadata URI", resolved.ComponentID)
		}

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
				SourceUnitID:  source.ID,
				RepositoryURI: source.RepositoryURI,
				Revision:      strings.ToLower(collectedSource.Revision),
				Subdirectory:  source.Subdirectory,
				WorkspacePath: expectedPath,
			})
			delete(collectedByKey, key)
		}

		for _, dependency := range resolved.Dependencies {
			component.Dependencies = append(component.Dependencies, Dependency{
				ComponentID: dependency.ComponentID,
				Variant:     dependency.Variant,
				Version:     dependency.Version,
			})
		}
		sort.Slice(component.SourceUnits, func(i, j int) bool {
			return component.SourceUnits[i].SourceUnitID < component.SourceUnits[j].SourceUnitID
		})
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

// MarshalReport sorts set-like report arrays before canonical serialization.
// CyclePath remains ordered because its sequence describes a graph walk.
func MarshalReport(report resolver.Report) ([]byte, error) {
	report.Selected = append([]resolver.SelectedCandidate{}, report.Selected...)
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
