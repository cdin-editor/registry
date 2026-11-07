package workspace

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/collector"
	"github.com/cdin-editor/registry/internal/resolver"
	"github.com/cdin-editor/registry/internal/validation"
)

type localRepositoryPolicy struct{}

func (localRepositoryPolicy) ValidateRepository(string) error { return nil }

func createSourceRepository(t *testing.T) (string, string) {
	t.Helper()
	repository := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", repository}, args...)...)
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Registry Test",
			"GIT_AUTHOR_EMAIL=registry-test@example.invalid",
			"GIT_COMMITTER_NAME=Registry Test",
			"GIT_COMMITTER_EMAIL=registry-test@example.invalid",
			"GIT_TERMINAL_PROMPT=0",
		)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	if err := os.WriteFile(filepath.Join(repository, "engine.c"), []byte("int engine(void) { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("init", "--quiet")
	runGit("add", ".")
	runGit("commit", "--quiet", "-m", "source fixture")
	return repository, runGit("rev-parse", "HEAD")
}

func fileRepositoryURI(repository string) string {
	path := filepath.ToSlash(repository)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func successfulResolution(repository, revision string) resolver.Result {
	return resolver.Result{
		Graph: &resolver.Graph{
			SchemaVersion: "1.0.0",
			Catalogs: []resolver.CatalogReference{{
				CatalogID:   "stable",
				MetadataURI: "https://catalog.example.invalid/index.json",
				MetadataSHA: strings.Repeat("a", 64),
			}},
			Roots: []resolver.Requirement{{
				ComponentID:       "engine",
				Variant:           "default",
				VersionConstraint: "=1.0.0",
			}},
			Components: []resolver.ResolvedComponent{{
				ComponentID:        "engine",
				Variant:            "default",
				Version:            "1.0.0",
				ReleaseMetadataURI: "https://catalog.example.invalid/engine-1.0.0.json",
				ReleaseMetadataSHA: strings.Repeat("b", 64),
				SourceUnits: []catalog.SourceUnit{{
					ID:            "source",
					RepositoryURI: fileRepositoryURI(repository),
					Revision:      revision,
				}},
				Dependencies: []resolver.ResolvedDependency{},
				Contracts: catalog.Contracts{
					Provides: []catalog.ContractProvider{},
					Requires: []catalog.ContractRequirement{},
				},
			}},
			ResolutionOrder: []resolver.Node{{
				ComponentID: "engine",
				Variant:     "default",
				Version:     "1.0.0",
			}},
		},
		Report: resolver.Report{
			SchemaVersion:      "1.1.0",
			Result:             "success",
			AppliedConstraints: []resolver.AppliedConstraint{},
			Selected: []resolver.SelectedCandidate{{
				ComponentID: "engine",
				Variant:     "default",
				Version:     "1.0.0",
			}},
			RejectedCandidates: []resolver.RejectedCandidate{},
			Conflicts:          []resolver.Conflict{},
			Warnings:           []resolver.Warning{},
		},
	}
}

func workspaceOptions(destination string) Options {
	validator, err := validation.New()
	if err != nil {
		panic(err)
	}
	return Options{
		Destination:      destination,
		RegistryVersion:  "1.2.3",
		Git:              collector.CommandGit{},
		RepositoryPolicy: localRepositoryPolicy{},
		Validator:        validator,
	}
}

// TestREG021REG022PublishesCompleteWorkspace verifies source collection,
// Manifest/Report schema validation and final atomic publication as one result.
func TestREG021REG022PublishesCompleteWorkspace(t *testing.T) {
	repository, revision := createSourceRepository(t)
	destination := filepath.Join(t.TempDir(), "prepared")
	prepared, err := Prepare(context.Background(), successfulResolution(repository, revision), workspaceOptions(destination))
	if err != nil {
		t.Fatal(err)
	}
	canonicalParent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		t.Fatal(err)
	}
	wantWorkspacePath := filepath.Join(canonicalParent, filepath.Base(destination))
	if prepared.WorkspacePath != wantWorkspacePath {
		t.Fatalf("workspace path = %q, want %q", prepared.WorkspacePath, wantWorkspacePath)
	}
	for _, path := range []string{
		prepared.ManifestPath,
		prepared.ReportPath,
		filepath.Join(destination, "components", "engine", "default", "sources", "source", "engine.c"),
	} {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("published workspace entry %q is missing or not regular: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(destination, "components", "engine", "default", "sources", "source", ".git")); !os.IsNotExist(err) {
		t.Fatalf("Git internals must not be included in the workspace: %v", err)
	}
	if len(prepared.ManifestSHA256) != 64 || len(prepared.ReportSHA256) != 64 {
		t.Fatalf("detached document digests are missing: %+v", prepared)
	}
}

// TestREG022CollectionFailureDoesNotPublishDestination injects an unavailable
// commit after the staging directory is created; no output directory may remain.
func TestREG022CollectionFailureDoesNotPublishDestination(t *testing.T) {
	repository, _ := createSourceRepository(t)
	destination := filepath.Join(t.TempDir(), "prepared")
	resolution := successfulResolution(repository, strings.Repeat("f", 40))
	if _, err := Prepare(context.Background(), resolution, workspaceOptions(destination)); err == nil {
		t.Fatal("expected collection failure for a missing commit")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed prepare published a destination: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(destination), ".prepared.registry-stage-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("staging directories leaked after failure: %v", matches)
	}
}

// TestREG022NeverOverwritesAnExistingDestination ensures a pre-existing
// workspace and its content remain unchanged after a rejected prepare attempt.
func TestREG022NeverOverwritesAnExistingDestination(t *testing.T) {
	repository, revision := createSourceRepository(t)
	destination := filepath.Join(t.TempDir(), "prepared")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(destination, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(context.Background(), successfulResolution(repository, revision), workspaceOptions(destination)); err == nil {
		t.Fatal("expected existing destination to be rejected")
	}
	content, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "keep" {
		t.Fatalf("existing destination was changed: %q", content)
	}
}

// TestREG022RejectsFailedResolution prevents a failure report from reaching the
// Collector or producing a Manifest/workspace.
func TestREG022RejectsFailedResolution(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "prepared")
	resolution := resolver.Result{
		Report: resolver.Report{SchemaVersion: "1.0.0", Result: "failure"},
	}
	if _, err := Prepare(context.Background(), resolution, workspaceOptions(destination)); err == nil {
		t.Fatal("failed resolution must not enter workspace preparation")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed resolution created destination: %v", err)
	}
}
