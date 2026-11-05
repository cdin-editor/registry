package collector

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/resolver"
)

type allowTestRepository struct{}

func (allowTestRepository) ValidateRepository(string) error { return nil }

type recordingGit struct {
	delegate      CommandGit
	hooksDisabled bool
}

func (g *recordingGit) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "core.hooksPath" || args[i] == "core.hooksPath=" {
			g.hooksDisabled = true
		}
		if strings.HasPrefix(args[i], "core.hooksPath=") && args[i] != "core.hooksPath=" {
			g.hooksDisabled = true
		}
	}
	return g.delegate.Run(ctx, dir, env, args...)
}

func initTestRepository(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	repository := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repository}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("initial"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("init", "--quiet")
	runGit("config", "user.name", "Registry Test")
	runGit("config", "user.email", "registry-test@example.invalid")
	for name, value := range files {
		target := filepath.Join(repository, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit("add", ".")
	runGit("commit", "--quiet", "-m", "fixture")
	revision := runGit("rev-parse", "HEAD")
	return repository, revision
}

func graphForSources(revision, repository string, units ...catalog.SourceUnit) *resolver.Graph {
	for i := range units {
		units[i].RepositoryURI = repository
		units[i].Revision = revision
	}
	return &resolver.Graph{Components: []resolver.ResolvedComponent{{
		ComponentID: "engine",
		Variant:     "default",
		Version:     "1.0.0",
		SourceUnits: units,
	}}}
}

// TestREG017CollectsMultipleDeclaredSourceUnits verifies each source unit is
// materialized at its own stable path, including two units from one repository.
func TestREG017CollectsMultipleDeclaredSourceUnits(t *testing.T) {
	repository, revision := initTestRepository(t, map[string]string{"src/engine.c": "int engine(void) { return 1; }"})
	destination := filepath.Join(t.TempDir(), "prepared")
	git := &recordingGit{}
	graph := graphForSources(revision, repository,
		catalog.SourceUnit{ID: "primary"},
		catalog.SourceUnit{ID: "secondary", Subdirectory: "src"},
	)
	collected, err := Collect(context.Background(), graph, destination, Options{Git: git, Policy: allowTestRepository{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(collected) != 2 {
		t.Fatalf("collected %d source units, want 2", len(collected))
	}
	primary := filepath.Join(destination, "components", "engine", "default", "sources", "primary", "README.md")
	if _, err := os.Stat(primary); err != nil {
		t.Fatalf("primary source missing: %v", err)
	}
	secondary := filepath.Join(destination, "components", "engine", "default", "sources", "secondary", "engine.c")
	if _, err := os.Stat(secondary); err != nil {
		t.Fatalf("subdirectory source missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "components", "engine", "default", "sources", "primary", ".git")); !os.IsNotExist(err) {
		t.Fatalf("Git metadata must not be copied into the prepared source tree: %v", err)
	}
	if !git.hooksDisabled {
		t.Fatal("Git hook path was not disabled for retrieval/checkout")
	}
}

// TestREG018RejectsNonObjectRevision confirms branch/tag names and abbreviations
// fail before Git is invoked; only full 40/64-character object IDs are accepted.
func TestREG018RejectsNonObjectRevision(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "prepared")
	git := &recordingGit{}
	graph := graphForSources("v1.0.0", "https://example.invalid/engine.git", catalog.SourceUnit{ID: "source"})
	_, err := Collect(context.Background(), graph, destination, Options{Git: git, Policy: allowTestRepository{}})
	if err == nil || !strings.Contains(err.Error(), "non-full Git object ID") {
		t.Fatalf("expected immutable revision error, got %v", err)
	}
	if git.hooksDisabled {
		t.Fatal("Git must not run for an invalid revision")
	}
}

// TestREG020RejectsParentTraversal proves a metadata subdirectory cannot escape
// the checked-out source root (including normalized path spellings).
func TestREG020RejectsParentTraversal(t *testing.T) {
	repository, revision := initTestRepository(t, nil)
	destination := filepath.Join(t.TempDir(), "prepared")
	graph := graphForSources(revision, repository, catalog.SourceUnit{ID: "source", Subdirectory: "../outside"})
	_, err := Collect(context.Background(), graph, destination, Options{Git: CommandGit{}, Policy: allowTestRepository{}})
	if err == nil || !strings.Contains(err.Error(), "unsafe source subdirectory") {
		t.Fatalf("expected path traversal rejection, got %v", err)
	}
}

// TestREG020RejectsSymlinkSourceTrees verifies source-tree links are rejected
// instead of being followed outside the assigned workspace root.
func TestREG020RejectsSymlinkSourceTrees(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	repository, revision := initTestRepository(t, nil)
	link := filepath.Join(repository, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation is unavailable on this host: %v", err)
	}
	cmd := exec.Command("git", "-C", repository, "add", "escape")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stage symlink: %v: %s", err, output)
	}
	cmd = exec.Command("git", "-C", repository, "commit", "--quiet", "-m", "add symlink")
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Registry Test", "GIT_AUTHOR_EMAIL=registry-test@example.invalid", "GIT_COMMITTER_NAME=Registry Test", "GIT_COMMITTER_EMAIL=registry-test@example.invalid")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit symlink: %v: %s", err, output)
	}
	cmd = exec.Command("git", "-C", repository, "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	revision = strings.TrimSpace(string(output))
	destination := filepath.Join(t.TempDir(), "prepared")
	graph := graphForSources(revision, repository, catalog.SourceUnit{ID: "source"})
	_, err = Collect(context.Background(), graph, destination, Options{Git: CommandGit{}, Policy: allowTestRepository{}})
	if err == nil || !strings.Contains(err.Error(), "contains symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
	if _, statErr := os.Stat(outside); statErr != nil {
		t.Fatalf("outside file should remain untouched: %v", statErr)
	}
}
