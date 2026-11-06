package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/collector"
	"github.com/cdin-editor/registry/internal/resolver"
)

func resolutionWithTwoSourceUnits(repository, revision string) resolver.Result {
	result := successfulResolution(repository, revision)
	component := &result.Graph.Components[0]
	component.SourceUnits = append(component.SourceUnits, catalog.SourceUnit{
		ID:            "source-two",
		RepositoryURI: fileRepositoryURI(repository),
		Revision:      revision,
		Subdirectory:  "missing-source-subdirectory",
	})
	return result
}

func assertPrepareFailureLeavesNoArtifacts(t *testing.T, destination string, collectorTempDirs ...string) {
	t.Helper()
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed prepare left destination %q: %v", destination, err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(destination), "."+filepath.Base(destination)+".registry-stage-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("failed prepare leaked staging directories: %v", matches)
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(destination), "."+filepath.Base(destination)+".registry.lock")); !os.IsNotExist(err) {
		t.Fatalf("failed prepare leaked its lock: %v", err)
	}
	for _, tempDir := range collectorTempDirs {
		entries, err := os.ReadDir(tempDir)
		if err != nil {
			t.Fatalf("inspect Collector temporary directory %q: %v", tempDir, err)
		}
		if len(entries) != 0 {
			t.Fatalf("Collector leaked temporary Git clone or hook directories in %q: %v", tempDir, entries)
		}
	}
}

func newCollectorTempDir(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "collector-temp")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return directory
}

// TestREG022FailureAfterFirstSourceUnitWasStaged proves Collector rollback is
// composed correctly with Workspace rollback when a later source unit fails.
func TestREG022FailureAfterFirstSourceUnitWasStaged(t *testing.T) {
	repository, revision := createSourceRepository(t)
	destination := filepath.Join(t.TempDir(), "prepared")
	collectorTemp := newCollectorTempDir(t)
	options := workspaceOptions(destination)
	options.TempDir = collectorTemp
	_, err := Prepare(context.Background(), resolutionWithTwoSourceUnits(repository, revision), options)
	if err == nil || !strings.Contains(err.Error(), "collect selected sources") {
		t.Fatalf("Prepare error = %v, want second-source collection failure", err)
	}
	assertPrepareFailureLeavesNoArtifacts(t, destination, collectorTemp)
}

// TestREG022FailureAfterAllSourcesBeforeManifest covers the boundary after
// every source has been materialized but before Manifest/Report completion.
func TestREG022FailureAfterAllSourcesBeforeManifest(t *testing.T) {
	repository, revision := createSourceRepository(t)
	destination := filepath.Join(t.TempDir(), "prepared")
	collectorTemp := newCollectorTempDir(t)
	options := workspaceOptions(destination)
	options.TempDir = collectorTemp
	failure := errors.New("injected post-collection failure")
	hooks := prepareHooks{
		afterCollect: func(sources []collector.Source) error {
			if len(sources) != 1 {
				t.Fatalf("collected source count = %d, want 1", len(sources))
			}
			return failure
		},
	}
	_, err := prepare(context.Background(), successfulResolution(repository, revision), options, hooks)
	if !errors.Is(err, failure) {
		t.Fatalf("Prepare error = %v, want injected post-collection failure", err)
	}
	assertPrepareFailureLeavesNoArtifacts(t, destination, collectorTemp)
}

type faultWorkspaceFile struct {
	*os.File
	failWrite bool
	failSync  bool
}

func (f *faultWorkspaceFile) Write(data []byte) (int, error) {
	if f.failWrite {
		return 0, errors.New("injected staged-document write failure")
	}
	return f.File.Write(data)
}

func (f *faultWorkspaceFile) Sync() error {
	if f.failSync {
		return errors.New("injected staged-document sync failure")
	}
	return f.File.Sync()
}

// TestREG022ManifestAndReportWriteAndSyncFailures checks failures on both
// public documents and at both persistence boundaries (Write and Sync).
func TestREG022ManifestAndReportWriteAndSyncFailures(t *testing.T) {
	cases := []struct {
		name      string
		filename  string
		failWrite bool
		failSync  bool
	}{
		{name: "manifest-write", filename: manifestFilename, failWrite: true},
		{name: "manifest-sync", filename: manifestFilename, failSync: true},
		{name: "report-write", filename: reportFilename, failWrite: true},
		{name: "report-sync", filename: reportFilename, failSync: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repository, revision := createSourceRepository(t)
			destination := filepath.Join(t.TempDir(), "prepared")
			collectorTemp := newCollectorTempDir(t)
			options := workspaceOptions(destination)
			options.TempDir = collectorTemp
			hooks := prepareHooks{
				openFile: func(path string, flag int, mode os.FileMode) (stagedFile, error) {
					file, err := os.OpenFile(path, flag, mode)
					if err != nil {
						return nil, err
					}
					inject := filepath.Base(path) == testCase.filename
					return &faultWorkspaceFile{File: file, failWrite: inject && testCase.failWrite, failSync: inject && testCase.failSync}, nil
				},
			}
			_, err := prepare(context.Background(), successfulResolution(repository, revision), options, hooks)
			if err == nil {
				t.Fatal("Prepare unexpectedly succeeded with an injected document persistence failure")
			}
			assertPrepareFailureLeavesNoArtifacts(t, destination, collectorTemp)
		})
	}
}

// TestREG022CancellationAtWorkspaceBoundaries ensures a cancelled operation
// never publishes, even after sources or one/both staged documents exist.
func TestREG022CancellationAtWorkspaceBoundaries(t *testing.T) {
	cases := []struct {
		name string
		hook func(*prepareHooks, context.CancelFunc)
	}{
		{name: "after-collection", hook: func(h *prepareHooks, cancel context.CancelFunc) {
			h.afterCollect = func([]collector.Source) error { cancel(); return nil }
		}},
		{name: "after-manifest-validation", hook: func(h *prepareHooks, cancel context.CancelFunc) {
			h.afterManifest = func([]byte) error { cancel(); return nil }
		}},
		{name: "before-report-write", hook: func(h *prepareHooks, cancel context.CancelFunc) {
			h.beforeReportWrite = func() error { cancel(); return nil }
		}},
		{name: "after-report-write", hook: func(h *prepareHooks, cancel context.CancelFunc) {
			h.afterReport = func([]byte) error { cancel(); return nil }
		}},
		{name: "before-publication", hook: func(h *prepareHooks, cancel context.CancelFunc) {
			h.beforePublish = func() error { cancel(); return nil }
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repository, revision := createSourceRepository(t)
			destination := filepath.Join(t.TempDir(), "prepared")
			collectorTemp := newCollectorTempDir(t)
			options := workspaceOptions(destination)
			options.TempDir = collectorTemp
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			hooks := prepareHooks{}
			testCase.hook(&hooks, cancel)

			_, err := prepare(ctx, successfulResolution(repository, revision), options, hooks)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Prepare error = %v, want context.Canceled", err)
			}
			assertPrepareFailureLeavesNoArtifacts(t, destination, collectorTemp)
		})
	}
}

// TestREG022LockContentionAndStaleLockFailClosed documents the lock contract:
// an existing lock is never guessed stale or removed automatically. Operators
// must establish that its owner is gone before manually removing it.
func TestREG022LockContentionAndStaleLockFailClosed(t *testing.T) {
	for _, name := range []string{"contended", "left-by-abnormal-termination"} {
		t.Run(name, func(t *testing.T) {
			repository, revision := createSourceRepository(t)
			parent := t.TempDir()
			destination := filepath.Join(parent, "prepared")
			lockPath := filepath.Join(parent, ".prepared.registry.lock")
			if err := os.WriteFile(lockPath, []byte("foreign-owner"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Prepare(context.Background(), successfulResolution(repository, revision), workspaceOptions(destination))
			if err == nil || !strings.Contains(err.Error(), "holds destination lock") {
				t.Fatalf("Prepare error = %v, want lock contention", err)
			}
			content, readErr := os.ReadFile(lockPath)
			if readErr != nil || string(content) != "foreign-owner" {
				t.Fatalf("pre-existing lock was modified or removed: content=%q err=%v", content, readErr)
			}
			if _, statErr := os.Lstat(destination); !os.IsNotExist(statErr) {
				t.Fatalf("lock contention published destination: %v", statErr)
			}
		})
	}
}

// TestREG022OwnedLockIsRemovedOnFailure verifies only the lock acquired by
// this Prepare call is removed when a later stage fails.
func TestREG022OwnedLockIsRemovedOnFailure(t *testing.T) {
	repository, revision := createSourceRepository(t)
	parent := t.TempDir()
	destination := filepath.Join(parent, "prepared")
	collectorTemp := newCollectorTempDir(t)
	options := workspaceOptions(destination)
	options.TempDir = collectorTemp
	hooks := prepareHooks{
		afterCollect: func([]collector.Source) error { return errors.New("injected failure") },
	}
	_, err := prepare(context.Background(), successfulResolution(repository, revision), options, hooks)
	if err == nil {
		t.Fatal("expected injected failure")
	}
	if _, err := os.Lstat(filepath.Join(parent, ".prepared.registry.lock")); !os.IsNotExist(err) {
		t.Fatalf("owned lock was not cleaned up: %v", err)
	}
	assertPrepareFailureLeavesNoArtifacts(t, destination, collectorTemp)
}

// TestREG022ConcurrentDestinationCreationIsNotOverwritten creates a competing
// destination inside the final rename boundary and verifies its data survives.
func TestREG022ConcurrentDestinationCreationIsNotOverwritten(t *testing.T) {
	repository, revision := createSourceRepository(t)
	destination := filepath.Join(t.TempDir(), "prepared")
	collectorTemp := newCollectorTempDir(t)
	options := workspaceOptions(destination)
	options.TempDir = collectorTemp
	hooks := prepareHooks{
		rename: func(oldPath, newPath string) error {
			if err := os.Mkdir(newPath, 0o755); err != nil {
				return err
			}
			sentinel := filepath.Join(newPath, "created-by-other-process.txt")
			if err := os.WriteFile(sentinel, []byte("external"), 0o644); err != nil {
				return err
			}
			return renameDirectoryNoReplace(oldPath, newPath)
		},
	}
	_, err := prepare(context.Background(), successfulResolution(repository, revision), options, hooks)
	if err == nil {
		t.Fatal("publication replaced a concurrently-created destination")
	}
	content, readErr := os.ReadFile(filepath.Join(destination, "created-by-other-process.txt"))
	if readErr != nil || string(content) != "external" {
		t.Fatalf("concurrent destination was overwritten: content=%q err=%v", content, readErr)
	}
	matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(destination), ".prepared.registry-stage-*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("staging directories leaked after destination race: %v (%v)", matches, globErr)
	}
	if _, statErr := os.Lstat(filepath.Join(filepath.Dir(destination), ".prepared.registry.lock")); !os.IsNotExist(statErr) {
		t.Fatalf("owned lock leaked after destination race: %v", statErr)
	}
	entries, tempErr := os.ReadDir(collectorTemp)
	if tempErr != nil || len(entries) != 0 {
		t.Fatalf("Collector leaked temporary clones/hooks after destination race: %v (%v)", entries, tempErr)
	}
}

// TestREG022WorkspacePermissionsAndSymlinkedParent verifies private staging,
// restrictive document files and canonicalized parent aliases. Windows does
// not expose POSIX permission bits with equivalent semantics.
func TestREG022WorkspacePermissionsAndSymlinkedParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission-bit assertions are not portable to Windows")
	}
	repository, revision := createSourceRepository(t)
	realParent := t.TempDir()
	aliasParent := filepath.Join(t.TempDir(), "parent-alias")
	if err := os.Symlink(realParent, aliasParent); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	destination := filepath.Join(aliasParent, "prepared")
	hooks := prepareHooks{
		afterCollect: func([]collector.Source) error {
			lockInfo, err := os.Stat(filepath.Join(realParent, ".prepared.registry.lock"))
			if err != nil {
				return err
			}
			if lockInfo.Mode().Perm() != 0o600 {
				t.Errorf("lock permissions = %o, want 600", lockInfo.Mode().Perm())
			}
			stages, err := filepath.Glob(filepath.Join(realParent, ".prepared.registry-stage-*"))
			if err != nil || len(stages) != 1 {
				t.Errorf("staging entries = %v, err=%v; want one", stages, err)
				return nil
			}
			stageInfo, err := os.Stat(stages[0])
			if err != nil {
				return err
			}
			if stageInfo.Mode().Perm() != 0o700 {
				t.Errorf("staging permissions = %o, want 700", stageInfo.Mode().Perm())
			}
			return nil
		},
	}
	prepared, err := prepare(context.Background(), successfulResolution(repository, revision), workspaceOptions(destination), hooks)
	if err != nil {
		t.Fatal(err)
	}
	canonicalParent, err := filepath.EvalSymlinks(realParent)
	if err != nil {
		t.Fatal(err)
	}
	wantWorkspacePath := filepath.Join(canonicalParent, "prepared")
	if prepared.WorkspacePath != wantWorkspacePath {
		t.Fatalf("workspace path = %q, want canonical parent path %q", prepared.WorkspacePath, wantWorkspacePath)
	}
	for _, filename := range []string{manifestFilename, reportFilename} {
		info, err := os.Stat(filepath.Join(prepared.WorkspacePath, filename))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Errorf("%s permissions = %o, want 644", filename, info.Mode().Perm())
		}
	}
	rootInfo, err := os.Stat(prepared.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if rootInfo.Mode().Perm() != 0o700 {
		t.Errorf("published workspace permissions = %o, want 700", rootInfo.Mode().Perm())
	}
}

// TestREG021WorkspaceSharedRepositoryPreservesComponentAndVariantPaths verifies
// the full Workspace keeps separate logical paths when two selected variants
// reuse the same physical repository and immutable commit.
func TestREG021WorkspaceSharedRepositoryPreservesComponentAndVariantPaths(t *testing.T) {
	repository, revision := createSourceRepository(t)
	resolution := successfulResolution(repository, revision)
	second := resolution.Graph.Components[0]
	second.ComponentID = "engine-alt"
	second.Variant = "tui"
	second.SourceUnits = append(second.SourceUnits, catalog.SourceUnit{
		ID:            "source-two",
		RepositoryURI: fileRepositoryURI(repository),
		Revision:      revision,
	})
	resolution.Graph.Components = append(resolution.Graph.Components, second)
	resolution.Graph.Roots = append(resolution.Graph.Roots, resolver.Requirement{
		ComponentID:       "engine-alt",
		Variant:           "tui",
		VersionConstraint: "=1.0.0",
	})
	resolution.Graph.ResolutionOrder = append(resolution.Graph.ResolutionOrder, resolver.Node{
		ComponentID: "engine-alt",
		Variant:     "tui",
		Version:     "1.0.0",
	})
	resolution.Report.Selected = append(resolution.Report.Selected, resolver.SelectedCandidate{
		ComponentID: "engine-alt",
		Variant:     "tui",
		Version:     "1.0.0",
	})

	destination := filepath.Join(t.TempDir(), "prepared")
	prepared, err := Prepare(context.Background(), resolution, workspaceOptions(destination))
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{
		filepath.Join(destination, "components", "engine", "default", "sources", "source", "engine.c"),
		filepath.Join(destination, "components", "engine-alt", "tui", "sources", "source", "engine.c"),
		filepath.Join(destination, "components", "engine-alt", "tui", "sources", "source-two", "engine.c"),
	}
	if len(prepared.Sources) != 3 {
		t.Fatalf("materialized source count = %d, want 3", len(prepared.Sources))
	}
	for _, path := range wantPaths {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("shared-repository source path %q missing or not regular: %v", path, err)
		}
	}
	if prepared.Sources[0].Revision != revision || prepared.Sources[1].Revision != revision {
		t.Fatalf("shared repository revisions differ from requested immutable commit: %+v", prepared.Sources)
	}
	if prepared.Sources[0].WorkspacePath == prepared.Sources[1].WorkspacePath {
		t.Fatalf("logical source paths collided: %+v", prepared.Sources)
	}
}

// TestREG022FailureImmediatelyBeforeRename checks the final publication
// boundary independently from cancellation and competing-destination cases.
func TestREG022FailureImmediatelyBeforeRename(t *testing.T) {
	repository, revision := createSourceRepository(t)
	destination := filepath.Join(t.TempDir(), "prepared")
	collectorTemp := newCollectorTempDir(t)
	options := workspaceOptions(destination)
	options.TempDir = collectorTemp
	failure := errors.New("injected pre-rename failure")
	hooks := prepareHooks{
		beforePublish: func() error { return failure },
	}
	_, err := prepare(context.Background(), successfulResolution(repository, revision), options, hooks)
	if !errors.Is(err, failure) {
		t.Fatalf("Prepare error = %v, want injected pre-rename failure", err)
	}
	assertPrepareFailureLeavesNoArtifacts(t, destination)
}
