package collector

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
)

type cacheCountingGit struct {
	delegate CommandGit
	mu       sync.Mutex
	commands [][]string
}

func (g *cacheCountingGit) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	g.mu.Lock()
	g.commands = append(g.commands, append([]string(nil), args...))
	g.mu.Unlock()
	return g.delegate.Run(ctx, dir, env, args...)
}

func (g *cacheCountingGit) remoteFetchCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	count := 0
	for _, args := range g.commands {
		fetchIndex := -1
		for i, arg := range args {
			if arg == "fetch" {
				fetchIndex = i
				break
			}
		}
		if fetchIndex >= 0 {
			count++
		}
	}
	return count
}

func TestCollectGitObjectCacheHitAvoidsRemoteFetchAndCorruptionRefetches(t *testing.T) {
	repository, revision := initTestRepository(t, map[string]string{
		"src/engine.c": "int engine(void) { return 42; }\n",
	})
	cache, err := NewGitObjectCache(filepath.Join(t.TempDir(), "git-cache"))
	if err != nil {
		t.Fatal(err)
	}
	git := &cacheCountingGit{}
	credentials := &fixedCredentialProvider{}
	graph := graphForSources(revision, repository, catalog.SourceUnit{ID: "source", Subdirectory: "src"})

	collect := func(name string) {
		t.Helper()
		destination := filepath.Join(t.TempDir(), name)
		sources, err := Collect(context.Background(), graph, destination, Options{
			Git: git, Policy: allowTestRepository{}, Credentials: credentials, GitCache: cache,
		})
		if err != nil {
			t.Fatalf("collect %s: %v", name, err)
		}
		if len(sources) != 1 || sources[0].Revision != revision {
			t.Fatalf("collected sources = %+v", sources)
		}
		sourceFile := filepath.Join(destination, "components", "engine", "default", "sources", "source", "engine.c")
		data, err := os.ReadFile(sourceFile)
		if err != nil {
			t.Fatalf("read collected source: %v", err)
		}
		if string(data) != "int engine(void) { return 42; }\n" {
			t.Fatalf("collected source bytes = %q", data)
		}
		if _, err := os.Lstat(filepath.Join(filepath.Dir(sourceFile), ".git")); !os.IsNotExist(err) {
			t.Fatalf("Git metadata leaked into source tree: %v", err)
		}
	}

	collect("first")
	cachePath := cache.objectPath(revision)
	cachedConfig, err := os.ReadFile(filepath.Join(cachePath, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cachedConfig), repository) {
		t.Fatal("bare Git cache persisted the source repository URI in config")
	}
	if _, err := os.Stat(filepath.Join(cachePath, "FETCH_HEAD")); !os.IsNotExist(err) {
		t.Fatalf("bare Git cache retained FETCH_HEAD metadata: %v", err)
	}
	if got := git.remoteFetchCount(); got != 1 {
		t.Fatalf("remote fetches after initial population = %d, want 1", got)
	}
	if credentials.calls != 1 {
		t.Fatalf("credential provider calls after population = %d, want 1", credentials.calls)
	}
	collect("cache-hit")
	if got := git.remoteFetchCount(); got != 1 {
		t.Fatalf("remote fetches after cache hit = %d, want no additional network fetch", got)
	}
	if credentials.calls != 1 {
		t.Fatalf("credential provider called on verified cache hit: %d calls", credentials.calls)
	}

	corruptGitObjectStore(t, filepath.Join(cachePath, "objects"))
	collect("corrupt-refetch")
	if got := git.remoteFetchCount(); got != 2 {
		t.Fatalf("remote fetches after cache corruption = %d, want refetch", got)
	}
	if credentials.calls != 2 {
		t.Fatalf("credential provider calls after corruption recovery = %d, want 2", credentials.calls)
	}
	if _, err := os.Stat(filepath.Join(cachePath, "objects")); err != nil {
		t.Fatalf("re-fetched object database was not republished: %v", err)
	}
}

func TestGitObjectHashMatchesNativeGitHashObject(t *testing.T) {
	repository, revision := initTestRepository(t, nil)
	cmd := exec.Command("git", "-C", repository, "cat-file", "commit", revision)
	object, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := gitObjectHash(revision, object); got != strings.ToLower(revision) {
		t.Fatalf("computed Git object hash = %s, want %s", got, revision)
	}
}

func corruptGitObjectStore(t *testing.T, objectDirectory string) {
	t.Helper()
	var target string
	walkErr := filepath.WalkDir(objectDirectory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(objectDirectory, path)
		if err != nil {
			return err
		}
		parts := strings.Split(relative, string(filepath.Separator))
		loose := len(parts) == 2 && len(parts[0]) == 2 && len(parts[1]) == 38
		packed := len(parts) == 2 && parts[0] == "pack" && strings.HasSuffix(parts[1], ".pack")
		if loose || packed {
			target = path
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	if target == "" {
		t.Fatal("Git cache did not contain a loose object or pack file to corrupt")
	}
	if err := os.Chmod(target, 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("Git object storage file is empty")
	}
	data[0] ^= 0xff
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
