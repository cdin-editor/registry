package catalog

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

const maxDeclaredArtifactBytes int64 = 2 << 30

// Target is a platform compatibility identity. ABI is intentionally opaque
// and compared exactly; Registry never infers compatibility from toolchain names.
type Target struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	ABI          string `json:"abi"`
}

// UniversalTarget is the only platform-independent artifact target.
func UniversalTarget() Target {
	return Target{OS: "any", Architecture: "any", ABI: "none"}
}

func (t Target) IsUniversal() bool {
	return t.OS == "any" && t.Architecture == "any" && t.ABI == "none"
}

func (t Target) Valid() bool {
	return validArtifactTarget(t)
}

func (t Target) Matches(want Target) bool {
	return t == want || t.IsUniversal()
}

// ArtifactRecipe identifies the source unit and recipe entry point used to
// produce an immutable package. Registry records it but never executes it.
type ArtifactRecipe struct {
	SourceUnitID string `json:"source_unit_id"`
	Path         string `json:"path"`
}

type ArtifactToolchain struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ArtifactBuild struct {
	Recipe    ArtifactRecipe      `json:"recipe"`
	Toolchain []ArtifactToolchain `json:"toolchain"`
	Runner    string              `json:"runner,omitempty"`
}

type ArtifactAttestation struct {
	URI           string `json:"uri"`
	SHA256        string `json:"sha256"`
	PredicateType string `json:"predicate_type"`
}

// Artifact describes one immutable package file published for a component
// release and one exact target (or the explicit universal target).
type Artifact struct {
	ID            string                `json:"artifact_id"`
	Kind          string                `json:"kind"`
	Target        Target                `json:"target"`
	Format        string                `json:"format"`
	FileName      string                `json:"file_name"`
	URI           string                `json:"uri"`
	SHA256        string                `json:"sha256"`
	SizeBytes     int64                 `json:"size_bytes"`
	Layout        string                `json:"layout"`
	SourceUnitIDs []string              `json:"source_unit_ids"`
	Build         ArtifactBuild         `json:"build"`
	Attestations  []ArtifactAttestation `json:"attestations,omitempty"`
}

func artifactToken(value string, limit int) bool {
	if value == "" || len(value) > limit {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '.' && r != '-' {
			return false
		}
	}
	return value[0] != '.' && value[0] != '-'
}

func validArtifactTarget(target Target) bool {
	if target.IsUniversal() {
		return true
	}
	if target.OS == "any" || target.Architecture == "any" || target.ABI == "none" {
		return false
	}
	switch target.OS {
	case "linux", "darwin", "windows", "freebsd", "openbsd":
	default:
		return false
	}
	switch target.Architecture {
	case "x86_64", "arm64":
	default:
		return false
	}
	if len(target.ABI) == 0 || len(target.ABI) > 64 {
		return false
	}
	for _, r := range target.ABI {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && !strings.ContainsRune("._+-", r) {
			return false
		}
	}
	return true
}

func safeArtifactRecipePath(value string) bool {
	if value == "" || strings.ContainsRune(value, '\\') || strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

// ValidateReleaseArtifacts performs semantic checks that JSON Schema cannot
// express: uniqueness, source-unit cross references and kind/layout coupling.
func ValidateReleaseArtifacts(release Release) error {
	if len(release.Artifacts) == 0 {
		return nil
	}
	sourceUnits := make(map[string]struct{}, len(release.SourceUnits))
	for _, source := range release.SourceUnits {
		if _, duplicate := sourceUnits[source.ID]; duplicate {
			return fmt.Errorf("catalog: duplicate source unit %q in release %s/%s@%s", source.ID, release.ComponentID, release.Variant, release.Version)
		}
		sourceUnits[source.ID] = struct{}{}
	}

	ids := make(map[string]struct{}, len(release.Artifacts))
	targets := make(map[Target]struct{}, len(release.Artifacts))
	for _, item := range release.Artifacts {
		if !artifactToken(item.ID, 128) {
			return fmt.Errorf("catalog: release %s/%s@%s has an invalid artifact ID", release.ComponentID, release.Variant, release.Version)
		}
		if _, duplicate := ids[item.ID]; duplicate {
			return fmt.Errorf("catalog: release %s/%s@%s has duplicate artifact ID %q", release.ComponentID, release.Variant, release.Version, item.ID)
		}
		ids[item.ID] = struct{}{}
		if _, duplicate := targets[item.Target]; duplicate {
			return fmt.Errorf("catalog: release %s/%s@%s has multiple artifacts for one target", release.ComponentID, release.Variant, release.Version)
		}
		targets[item.Target] = struct{}{}
		if !validArtifactTarget(item.Target) {
			return fmt.Errorf("catalog: artifact %q has an invalid target tuple", item.ID)
		}
		if item.SizeBytes < 1 || item.SizeBytes > maxDeclaredArtifactBytes {
			return fmt.Errorf("catalog: artifact %q exceeds the declared package size limit", item.ID)
		}
		if item.Format != "tar.gz" && item.Format != "zip" {
			return fmt.Errorf("catalog: artifact %q uses an unsupported archive format", item.ID)
		}
		if item.URI == "" {
			return fmt.Errorf("catalog: artifact %q has no URI", item.ID)
		}
		parsed, err := url.Parse(item.URI)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" {
			return fmt.Errorf("catalog: artifact %q must have an absolute HTTPS URI without embedded credentials", item.ID)
		}
		if len(item.SHA256) != 64 {
			return fmt.Errorf("catalog: artifact %q has an invalid SHA-256 digest", item.ID)
		}
		for _, r := range item.SHA256 {
			if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') && !(r >= 'A' && r <= 'F') {
				return fmt.Errorf("catalog: artifact %q has an invalid SHA-256 digest", item.ID)
			}
		}
		switch item.Kind {
		case "runtime-module-archive":
			if item.Layout != "lua-module-tree-v1" || !item.Target.IsUniversal() {
				return fmt.Errorf("catalog: Runtime module artifact %q must use the universal Lua module layout", item.ID)
			}
		case "native-development-package", "sdk-package":
			if item.Layout != "cmake-prefix-v1" || item.Target.IsUniversal() {
				return fmt.Errorf("catalog: native artifact %q must use a concrete CMake prefix target", item.ID)
			}
		default:
			return fmt.Errorf("catalog: artifact %q has an unsupported package kind", item.ID)
		}
		if len(item.SourceUnitIDs) == 0 {
			return fmt.Errorf("catalog: artifact %q has no source provenance", item.ID)
		}
		seenSources := make(map[string]struct{}, len(item.SourceUnitIDs))
		for _, sourceID := range item.SourceUnitIDs {
			if _, ok := sourceUnits[sourceID]; !ok {
				return fmt.Errorf("catalog: artifact %q refers to an undeclared source unit", item.ID)
			}
			if _, duplicate := seenSources[sourceID]; duplicate {
				return fmt.Errorf("catalog: artifact %q repeats a source unit", item.ID)
			}
			seenSources[sourceID] = struct{}{}
		}
		if _, ok := seenSources[item.Build.Recipe.SourceUnitID]; !ok || !safeArtifactRecipePath(item.Build.Recipe.Path) {
			return fmt.Errorf("catalog: artifact %q has an invalid source build recipe reference", item.ID)
		}
		if len(item.Build.Toolchain) == 0 {
			return fmt.Errorf("catalog: artifact %q has no build toolchain identity", item.ID)
		}
		for _, tool := range item.Build.Toolchain {
			if strings.TrimSpace(tool.Name) == "" || strings.TrimSpace(tool.Version) == "" {
				return fmt.Errorf("catalog: artifact %q has an incomplete toolchain identity", item.ID)
			}
		}
	}
	return nil
}
