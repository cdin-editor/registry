package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRenameDirectoryNoReplaceMovesCompleteDirectory(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "stage")
	destination := filepath.Join(parent, "published")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "manifest.json"), []byte("complete"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := renameDirectoryNoReplace(source, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Fatalf("source stage still exists after publication: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "manifest.json"))
	if err != nil || string(content) != "complete" {
		t.Fatalf("published document = %q, err=%v", content, err)
	}
}

func TestRenameDirectoryNoReplaceRejectsEmptyDestination(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "stage")
	destination := filepath.Join(parent, "published")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "manifest.json"), []byte("registry"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}

	err := renameDirectoryNoReplace(source, destination)
	if err == nil || !os.IsExist(err) {
		t.Fatalf("rename error = %v, want existing-destination error", err)
	}
	if _, err := os.Stat(filepath.Join(source, "manifest.json")); err != nil {
		t.Fatalf("failed source stage was not preserved: %v", err)
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("empty competing destination was changed: %v", entries)
	}
}

func TestRenameDirectoryNoReplaceConcurrentWritersHaveOneWinner(t *testing.T) {
	for iteration := 0; iteration < 8; iteration++ {
		parent := t.TempDir()
		destination := filepath.Join(parent, "published")
		sources := []string{filepath.Join(parent, "stage-a"), filepath.Join(parent, "stage-b")}
		for i, source := range sources {
			if err := os.Mkdir(source, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "winner.txt"), []byte{byte('a' + i)}, 0o600); err != nil {
				t.Fatal(err)
			}
		}

		start := make(chan struct{})
		errs := make([]error, len(sources))
		var wait sync.WaitGroup
		for i, source := range sources {
			wait.Add(1)
			go func(index int, oldPath string) {
				defer wait.Done()
				<-start
				errs[index] = renameDirectoryNoReplace(oldPath, destination)
			}(i, source)
		}
		close(start)
		wait.Wait()

		winners := 0
		for _, err := range errs {
			if err == nil {
				winners++
			} else if !os.IsExist(err) {
				t.Fatalf("rename error = %v, want success or destination-exists", err)
			}
		}
		if winners != 1 {
			t.Fatalf("iteration %d: successful publishers = %d, want exactly one; errors=%v", iteration, winners, errs)
		}
		entries, err := os.ReadDir(destination)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != "winner.txt" {
			t.Fatalf("published directory is not one complete winner: %v", entries)
		}
		value, err := os.ReadFile(filepath.Join(destination, "winner.txt"))
		if err != nil || len(value) != 1 || (value[0] != 'a' && value[0] != 'b') {
			t.Fatalf("published winner data = %q, err=%v", value, err)
		}
	}
}

func TestREG022EmptyDestinationRaceIsNotOverwritten(t *testing.T) {
	repository, revision := createSourceRepository(t)
	destination := filepath.Join(t.TempDir(), "prepared")
	collectorTemp := newCollectorTempDir(t)
	options := workspaceOptions(destination)
	options.TempDir = collectorTemp
	hooks := prepareHooks{
		rename: func(oldPath, newPath string) error {
			// Model an unrelated process creating an empty destination after
			// Prepare's last Lstat but before the publication syscall.
			if err := os.Mkdir(newPath, 0o700); err != nil {
				return err
			}
			return renameDirectoryNoReplace(oldPath, newPath)
		},
	}
	_, err := prepare(context.Background(), successfulResolution(repository, revision), options, hooks)
	if err == nil || !os.IsExist(err) {
		t.Fatalf("Prepare error = %v, want atomic destination-exists failure", err)
	}
	entries, readErr := os.ReadDir(destination)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("competing empty destination was changed: entries=%v err=%v", entries, readErr)
	}
	assertPrepareFailureLeavesNoArtifacts(t, destination, collectorTemp)
}

func TestRenameDirectoryNoReplaceDoesNotFollowDestinationSymlink(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "stage")
	destination := filepath.Join(parent, "published")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "manifest.json"), []byte("registry"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "sentinel"), []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, destination); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	err := renameDirectoryNoReplace(source, destination)
	if err == nil || !os.IsExist(err) {
		t.Fatalf("rename error = %v, want existing symlink destination error", err)
	}
	content, readErr := os.ReadFile(filepath.Join(outside, "sentinel"))
	if readErr != nil || string(content) != "external" {
		t.Fatalf("symlink target was modified: content=%q err=%v", content, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(source, "manifest.json")); statErr != nil {
		t.Fatalf("source stage disappeared: %v", statErr)
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("existing destination symlink was incorrectly treated as absent: %v", err)
	}
}

func TestREG022StagingSymlinkSwapBeforePublicationFailsClosed(t *testing.T) {
	repository, revision := createSourceRepository(t)
	parent := t.TempDir()
	destination := filepath.Join(parent, "prepared")
	outside := filepath.Join(t.TempDir(), "outside")
	collectorTemp := newCollectorTempDir(t)
	options := workspaceOptions(destination)
	options.TempDir = collectorTemp
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "sentinel"), []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	var movedStage string
	hooks := prepareHooks{
		beforePublish: func() error {
			stages, err := filepath.Glob(filepath.Join(parent, ".prepared.registry-stage-*"))
			if err != nil {
				return err
			}
			if len(stages) != 1 {
				return fmt.Errorf("staging entries = %v, want exactly one", stages)
			}
			movedStage = stages[0] + ".moved"
			if err := os.Rename(stages[0], movedStage); err != nil {
				return err
			}
			if err := os.Symlink(outside, stages[0]); err != nil {
				return err
			}
			return os.RemoveAll(movedStage)
		},
	}
	_, err := prepare(context.Background(), successfulResolution(repository, revision), options, hooks)
	if err == nil || !strings.Contains(err.Error(), "staging directory changed") {
		t.Fatalf("Prepare error = %v, want fail-closed staging symlink rejection", err)
	}
	assertPrepareFailureLeavesNoArtifacts(t, destination, collectorTemp)
	content, readErr := os.ReadFile(filepath.Join(outside, "sentinel"))
	if readErr != nil || string(content) != "external" {
		t.Fatalf("external symlink target changed: content=%q err=%v", content, readErr)
	}
}
