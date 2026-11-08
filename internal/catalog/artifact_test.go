package catalog

import "testing"

func validSource() SourceUnit {
	return SourceUnit{ID: "runtime-source", RepositoryURI: "https://github.com/example/runtime", Revision: "0123456789abcdef0123456789abcdef01234567"}
}
func validRuntimeArtifact() Artifact {
	return Artifact{
		ID: "runtime-any", Kind: "runtime-module-archive",
		Target: Target{OS: "any", Architecture: "any", ABI: "none"},
		Format: "tar.gz", FileName: "runtime.tar.gz",
		URI:       "https://github.com/example/runtime/releases/download/v1/runtime.tar.gz",
		SHA256:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SizeBytes: 1024, Layout: "lua-module-tree-v1",
		SourceUnitIDs: []string{"runtime-source"},
		Build: ArtifactBuild{
			Recipe:    ArtifactRecipe{SourceUnitID: "runtime-source", Path: "scripts/package.sh"},
			Toolchain: []ToolchainEntry{{Name: "tar", Version: "1.35"}},
			Runner:    "ubuntu-24.04",
		},
	}
}

func TestValidateReleaseArtifactsAcceptsUniversalRuntimeArchive(t *testing.T) {
	if err := ValidateReleaseArtifacts([]SourceUnit{validSource()}, []Artifact{validRuntimeArtifact()}); err != nil {
		t.Fatalf("expected valid artifact metadata, got %v", err)
	}
}
func TestValidateReleaseArtifactsRejectsDuplicateArtifactID(t *testing.T) {
	a := validRuntimeArtifact()
	b := validRuntimeArtifact()
	b.Target = Target{OS: "any", Architecture: "any", ABI: "none"}
	b.FileName = "other.tar.gz"
	if err := ValidateReleaseArtifacts([]SourceUnit{validSource()}, []Artifact{a, b}); err == nil {
		t.Fatal("expected duplicate artifact ID rejection")
	}
}
func TestValidateReleaseArtifactsRejectsUnknownSourceProvenance(t *testing.T) {
	a := validRuntimeArtifact()
	a.SourceUnitIDs = []string{"other"}
	if err := ValidateReleaseArtifacts([]SourceUnit{validSource()}, []Artifact{a}); err == nil {
		t.Fatal("expected unknown provenance rejection")
	}
}
func TestValidateReleaseArtifactsRejectsRecipePathTraversal(t *testing.T) {
	a := validRuntimeArtifact()
	a.Build.Recipe.Path = "../../package.sh"
	if err := ValidateReleaseArtifacts([]SourceUnit{validSource()}, []Artifact{a}); err == nil {
		t.Fatal("expected recipe path traversal rejection")
	}
}
func TestValidateReleaseArtifactsRejectsNativeUniversalArtifact(t *testing.T) {
	a := validRuntimeArtifact()
	a.Kind = "sdk-package"
	a.Layout = "cmake-prefix-v1"
	if err := ValidateReleaseArtifacts([]SourceUnit{validSource()}, []Artifact{a}); err == nil {
		t.Fatal("expected native package to reject universal target")
	}
}
func TestValidateReleaseArtifactsRejectsHTTPURI(t *testing.T) {
	a := validRuntimeArtifact()
	a.URI = "http://example.com/runtime.tar.gz"
	if err := ValidateReleaseArtifacts([]SourceUnit{validSource()}, []Artifact{a}); err == nil {
		t.Fatal("expected insecure URI rejection")
	}
}
