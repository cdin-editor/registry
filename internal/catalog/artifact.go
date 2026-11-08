package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/cdin-editor/registry/internal/platform"
)

// Target is an exact binary compatibility tuple. "any/any/none" is the only
// platform-independent target supported by the Registry 1.3 contract.
type Target struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	ABI          string `json:"abi"`
}

func (t Target) IsUniversal() bool {
	return t.OS == "any" && t.Architecture == "any" && t.ABI == "none"
}

// Normalize returns the canonical target spelling for common platform
// aliases (for example AMD64/x64 and aarch64). ABI compatibility is never
// inferred; only case/outer whitespace normalization is performed.
func (t Target) Normalize() Target {
	t.OS, t.Architecture, t.ABI = platform.NormalizeTargetAliases(t.OS, t.Architecture, t.ABI)
	return t
}

func (t Target) Validate() error {
	if t.IsUniversal() {
		return nil
	}
	switch t.OS {
	case "linux", "darwin", "windows", "freebsd", "openbsd":
	default:
		return fmt.Errorf("unsupported artifact target OS")
	}
	switch t.Architecture {
	case "x86_64", "arm64":
	default:
		return fmt.Errorf("unsupported artifact architecture")
	}
	if !validABI(t.ABI) {
		return fmt.Errorf("native artifact target requires a valid lower-case ABI identifier")
	}
	return nil
}

func validABI(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for i, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if i > 0 {
			valid = valid || r == '.' || r == '_' || r == '+' || r == '-'
		}
		if !valid {
			return false
		}
	}
	return true
}

// ArtifactCacheKey returns the v1 cache identity required by the public
// artifact contract. It deliberately includes release metadata identity,
// artifact ID, target, URI and content digest; filenames and versions alone
// never identify a package.
func ArtifactCacheKey(releaseMetadataSHA string, artifact Artifact) (string, error) {
	releaseMetadataSHA = strings.ToLower(releaseMetadataSHA)
	expectedSHA := strings.ToLower(artifact.SHA256)
	if !isSHA256(releaseMetadataSHA) || !isSHA256(expectedSHA) ||
		strings.TrimSpace(artifact.ID) == "" {
		return "", fmt.Errorf("invalid immutable artifact cache identity")
	}
	target := artifact.Target
	if err := target.Validate(); err != nil {
		return "", fmt.Errorf("invalid artifact cache target")
	}
	identity := strings.Join([]string{
		"registry-artifact-v1",
		releaseMetadataSHA,
		artifact.ID,
		target.OS,
		target.Architecture,
		target.ABI,
		artifact.URI,
		expectedSHA,
	}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:]), nil
}

// Artifact records one immutable package published by the component owner.
type Artifact struct {
	ID            string        `json:"artifact_id"`
	Kind          string        `json:"kind"`
	Target        Target        `json:"target"`
	Format        string        `json:"format"`
	FileName      string        `json:"file_name"`
	URI           string        `json:"uri"`
	SHA256        string        `json:"sha256"`
	SizeBytes     int64         `json:"size_bytes"`
	Layout        string        `json:"layout"`
	SourceUnitIDs []string      `json:"source_unit_ids"`
	Build         ArtifactBuild `json:"build"`
	Attestations  []Attestation `json:"attestations,omitempty"`
}

type ArtifactBuild struct {
	Recipe    ArtifactRecipe   `json:"recipe"`
	Toolchain []ToolchainEntry `json:"toolchain"`
	Runner    string           `json:"runner,omitempty"`
}

type ArtifactRecipe struct {
	SourceUnitID string `json:"source_unit_id"`
	Path         string `json:"path"`
}

type ToolchainEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Attestation struct {
	URI           string `json:"uri"`
	SHA256        string `json:"sha256"`
	PredicateType string `json:"predicate_type"`
}

// ValidateReleaseArtifacts performs semantic checks that JSON Schema cannot
// express: stable artifact identity, source provenance references, URL policy,
// normalized recipe paths and kind/layout/target agreement.
func ValidateReleaseArtifacts(sources []SourceUnit, artifacts []Artifact) error {
	if len(artifacts) == 0 {
		return nil
	}
	sourceIDs := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		if _, exists := sourceIDs[source.ID]; exists {
			return fmt.Errorf("duplicate source unit identity")
		}
		sourceIDs[source.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.ID == "" {
			return fmt.Errorf("empty artifact identity")
		}
		if _, exists := seen[artifact.ID]; exists {
			return fmt.Errorf("duplicate artifact identity")
		}
		seen[artifact.ID] = struct{}{}
		if err := artifact.Target.Validate(); err != nil {
			return fmt.Errorf("invalid target for artifact %q: %w", artifact.ID, err)
		}
		if artifact.Format != "tar.gz" && artifact.Format != "zip" {
			return fmt.Errorf("unsupported artifact archive format")
		}
		if artifact.SizeBytes <= 0 {
			return fmt.Errorf("artifact size must be positive")
		}
		if len(artifact.SHA256) != 64 {
			return fmt.Errorf("artifact SHA-256 is invalid")
		}
		for _, ch := range artifact.SHA256 {
			if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F') {
				return fmt.Errorf("artifact SHA-256 is invalid")
			}
		}
		parsed, err := url.Parse(artifact.URI)
		if err != nil || !parsed.IsAbs() || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("artifact URI must be an absolute HTTPS URI without credentials, query or fragment")
		}
		if artifact.FileName == "" || path.Base(artifact.FileName) != artifact.FileName || strings.ContainsAny(artifact.FileName, "/\\:\x00") || !utf8.ValidString(artifact.FileName) {
			return fmt.Errorf("artifact file name must be a safe basename")
		}
		if artifact.Kind == "runtime-module-archive" {
			if artifact.Layout != "lua-module-tree-v1" || !artifact.Target.IsUniversal() {
				return fmt.Errorf("runtime module artifacts require lua-module-tree-v1 and the universal target")
			}
		} else if artifact.Kind == "native-development-package" || artifact.Kind == "sdk-package" {
			if artifact.Layout != "cmake-prefix-v1" || artifact.Target.IsUniversal() {
				return fmt.Errorf("native packages require cmake-prefix-v1 and a concrete target")
			}
		} else {
			return fmt.Errorf("unsupported artifact kind")
		}
		if len(artifact.SourceUnitIDs) == 0 {
			return fmt.Errorf("artifact has no source provenance")
		}
		provenance := make(map[string]struct{}, len(artifact.SourceUnitIDs))
		for _, id := range artifact.SourceUnitIDs {
			if _, duplicate := provenance[id]; duplicate {
				return fmt.Errorf("artifact contains duplicate source provenance")
			}
			if _, ok := sourceIDs[id]; !ok {
				return fmt.Errorf("artifact references an unknown source unit")
			}
			provenance[id] = struct{}{}
		}
		if _, ok := provenance[artifact.Build.Recipe.SourceUnitID]; !ok || artifact.Build.Recipe.Path == "" {
			return fmt.Errorf("artifact build recipe must reference one of its source units")
		}
		recipePath := artifact.Build.Recipe.Path
		if strings.Contains(recipePath, "\\") || strings.HasPrefix(recipePath, "/") || path.Clean(recipePath) != recipePath || recipePath == "." || recipePath == ".." || strings.HasPrefix(recipePath, "../") || strings.Contains(recipePath, ":") || !utf8.ValidString(recipePath) {
			return fmt.Errorf("artifact recipe path must be a normalized relative POSIX path")
		}
		if len(artifact.Build.Toolchain) == 0 {
			return fmt.Errorf("artifact build toolchain is empty")
		}
		for _, tool := range artifact.Build.Toolchain {
			if strings.TrimSpace(tool.Name) == "" || strings.TrimSpace(tool.Version) == "" {
				return fmt.Errorf("artifact toolchain entry is incomplete")
			}
		}
	}
	return nil
}
