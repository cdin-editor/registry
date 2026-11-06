package collector

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	receivedEnv   [][]string
	directories   []string
}

func (g *recordingGit) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	g.receivedEnv = append(g.receivedEnv, append([]string(nil), env...))
	g.directories = append(g.directories, dir)
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

// TestREG026HTTPSAllowListMatchesHostnameWithExplicitDefaultPort verifies
// equivalent HTTPS host spellings do not differ merely because :443 is explicit,
// while the policy still rejects non-default ports, credentials and other schemes.
func TestREG026HTTPSAllowListMatchesHostnameWithExplicitDefaultPort(t *testing.T) {
	policy := HTTPSAllowList{"example.invalid"}
	if err := policy.ValidateRepository("https://example.invalid:443/repository.git"); err != nil {
		t.Fatalf("explicit default HTTPS port should be accepted: %v", err)
	}
	for _, uri := range []string{
		"https://example.invalid:8443/repository.git",
		"https://user:secret@example.invalid/repository.git",
		"http://example.invalid/repository.git",
		"https://sub.example.invalid/repository.git",
	} {
		if err := policy.ValidateRepository(uri); err == nil {
			t.Errorf("ValidateRepository(%q) unexpectedly succeeded", uri)
		}
	}
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
	for _, revision := range []string{"main", "v1.0.0", "01234567", "0123456789abcdef0123456789abcdef0123456g"} {
		t.Run(revision, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "prepared")
			git := &recordingGit{}
			graph := graphForSources(revision, "https://example.invalid/engine.git", catalog.SourceUnit{ID: "source"})
			_, err := Collect(context.Background(), graph, destination, Options{Git: git, Policy: allowTestRepository{}})
			if err == nil || !strings.Contains(err.Error(), "non-full Git object ID") {
				t.Fatalf("expected immutable revision error, got %v", err)
			}
			if git.hooksDisabled {
				t.Fatal("Git must not run for an invalid revision")
			}
		})
	}
}

// TestREG020RejectsParentTraversal proves a metadata subdirectory cannot escape
// the checked-out source root (including normalized path spellings).
func TestREG020RejectsParentTraversal(t *testing.T) {
	repository, revision := initTestRepository(t, nil)
	destination := filepath.Join(t.TempDir(), "prepared")
	for _, subdirectory := range []string{"../outside", "C:/outside", "C:outside"} {
		graph := graphForSources(revision, repository, catalog.SourceUnit{ID: "source", Subdirectory: subdirectory})
		_, err := Collect(context.Background(), graph, destination, Options{Git: CommandGit{}, Policy: allowTestRepository{}})
		if err == nil || !strings.Contains(err.Error(), "unsafe source subdirectory") {
			t.Errorf("subdirectory %q: expected unsafe-path rejection, got %v", subdirectory, err)
		}
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
	partialTarget := filepath.Join(destination, "components", "engine", "default", "sources", "source")
	if _, partialErr := os.Lstat(partialTarget); !os.IsNotExist(partialErr) {
		t.Fatalf("partial source tree remains after symlink rejection: %v", partialErr)
	}
	if _, statErr := os.Stat(outside); statErr != nil {
		t.Fatalf("outside file should remain untouched: %v", statErr)
	}
}


type fixedCredentialProvider struct {
	environment []string
	err         error
	calls       int
	lastURI     string
}

func (p *fixedCredentialProvider) GitEnvironment(_ context.Context, repositoryURI string) ([]string, error) {
	p.calls++
	p.lastURI = repositoryURI
	return append([]string(nil), p.environment...), p.err
}

type cancelDuringGit struct {
	delegate CommandGit
	cancel   context.CancelFunc
	trigger  string
	dirs     []string
	hooks    []string
}

func (g *cancelDuringGit) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	g.dirs = append(g.dirs, dir)
	for _, arg := range args {
		if strings.HasPrefix(arg, "core.hooksPath=") {
			g.hooks = append(g.hooks, strings.TrimPrefix(arg, "core.hooksPath="))
		}
	}
	for _, arg := range args {
		if arg == g.trigger {
			if g.trigger == "fetch" {
				g.cancel()
				return nil, context.Canceled
			}
			output, err := g.delegate.Run(ctx, dir, env, args...)
			if err == nil {
				g.cancel()
			}
			return output, err
		}
	}
	return g.delegate.Run(ctx, dir, env, args...)
}

func assertCollectorTemporaryPathsRemoved(t *testing.T, tempRoot string, git *cancelDuringGit) {
	t.Helper()
	for _, directory := range git.dirs {
		if _, err := os.Lstat(directory); !os.IsNotExist(err) {
			t.Errorf("temporary Git repository remains after collection failure: %s (%v)", directory, err)
		}
	}
	for _, hooks := range git.hooks {
		if _, err := os.Lstat(hooks); !os.IsNotExist(err) {
			t.Errorf("temporary hook directory remains after collection failure: %s (%v)", hooks, err)
		}
	}
	for _, pattern := range []string{"registry-git-source-*", "registry-disabled-hooks-*"} {
		matches, err := filepath.Glob(filepath.Join(tempRoot, pattern))
		if err != nil || len(matches) != 0 {
			t.Errorf("temporary paths leaked for %s: %v (%v)", pattern, matches, err)
		}
	}
}

// TestREG017SharedRepositoryAcrossComponentsAndVariants keeps one immutable
// repository source reusable by distinct logical selections without collapsing
// their canonical workspace paths.
func TestREG017SharedRepositoryAcrossComponentsAndVariants(t *testing.T) {
	repository, revision := initTestRepository(t, map[string]string{"shared.txt": "shared source"})
	components := []resolver.ResolvedComponent{
		{ComponentID: "engine", Variant: "default", Version: "1.0.0", SourceUnits: []catalog.SourceUnit{{ID: "source", RepositoryURI: repository, Revision: revision}}},
		{ComponentID: "engine", Variant: "optimized", Version: "1.0.0", SourceUnits: []catalog.SourceUnit{{ID: "source", RepositoryURI: repository, Revision: revision}}},
		{ComponentID: "tools", Variant: "default", Version: "1.0.0", SourceUnits: []catalog.SourceUnit{{ID: "source", RepositoryURI: repository, Revision: revision}}},
	}
	graph := &resolver.Graph{Components: components}
	destination := filepath.Join(t.TempDir(), "prepared")
	git := &recordingGit{}
	collected, err := Collect(context.Background(), graph, destination, Options{Git: git, Policy: allowTestRepository{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(collected) != 3 {
		t.Fatalf("collected %d logical source units, want 3", len(collected))
	}
	seen := make(map[string]bool)
	for _, source := range collected {
		if seen[source.WorkspacePath] {
			t.Fatalf("logical sources share a workspace path: %q", source.WorkspacePath)
		}
		seen[source.WorkspacePath] = true
		path := filepath.Join(destination, filepath.FromSlash(source.WorkspacePath), "shared.txt")
		content, err := os.ReadFile(path)
		if err != nil || string(content) != "shared source" {
			t.Fatalf("shared repository content missing at %s: %q (%v)", path, content, err)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("got %d distinct canonical paths, want 3", len(seen))
	}
}

// TestREG026CollectUsesOnlyExplicitCredentialProvider passes the provider's
// validated non-interactive environment to Git and never infers it from ambient
// process configuration.
func TestREG026CollectUsesOnlyExplicitCredentialProvider(t *testing.T) {
	repository, revision := initTestRepository(t, nil)
	destination := filepath.Join(t.TempDir(), "prepared")
	askpass := filepath.Join(t.TempDir(), "registry-askpass")
	if err := os.WriteFile(askpass, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	const secret = "collector-explicit-secret-marker"
	provider := &fixedCredentialProvider{environment: []string{
		"GIT_ASKPASS=" + askpass,
		"GIT_ASKPASS_REQUIRE=force",
		"REGISTRY_GIT_CREDENTIAL_TOKEN=" + secret,
	}}
	git := &recordingGit{}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "credential.helper")
	t.Setenv("GIT_CONFIG_VALUE_0", "store")
	_, err := Collect(context.Background(), graphForSources(revision, repository, catalog.SourceUnit{ID: "source"}), destination,
		Options{Git: git, Policy: allowTestRepository{}, Credentials: provider})
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || provider.lastURI != repository {
		t.Fatalf("credential provider calls/URI = %d/%q", provider.calls, provider.lastURI)
	}
	var received []string
	for _, env := range git.receivedEnv {
		received = env
		if strings.Contains(strings.Join(env, "\n"), "GIT_CONFIG_COUNT") {
			t.Fatal("ambient Git configuration reached the injected runner")
		}
	}
	joined := strings.Join(received, "\n")
	for _, required := range []string{"GIT_ASKPASS=" + askpass, "GIT_ASKPASS_REQUIRE=force", "REGISTRY_GIT_CREDENTIAL_TOKEN=" + secret} {
		if !strings.Contains(joined, required) {
			t.Errorf("explicit credential value missing from GitRunner environment: %s", required)
		}
	}
}

// TestREG026CancellationDuringFetchCleansCollectorArtifacts verifies a fetch
// cancellation removes the isolated repository and its disabled-hook directory.
func TestREG026CancellationDuringFetchCleansCollectorArtifacts(t *testing.T) {
	repository, revision := initTestRepository(t, nil)
	tempRoot := t.TempDir()
	destination := filepath.Join(t.TempDir(), "prepared")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	git := &cancelDuringGit{cancel: cancel, trigger: "fetch"}
	_, err := Collect(ctx, graphForSources(revision, repository, catalog.SourceUnit{ID: "source"}), destination,
		Options{Git: git, Policy: allowTestRepository{}, TempDir: tempRoot})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect error = %v, want context.Canceled", err)
	}
	assertCollectorTemporaryPathsRemoved(t, tempRoot, git)
	target := filepath.Join(destination, "components", "engine", "default", "sources", "source")
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("partial source target remains after cancelled fetch: %v", err)
	}
}

// TestREG026CancellationDuringCopyCleansPartialSourceTree cancels immediately
// after HEAD verification, when copyTree starts, and asserts target rollback.
func TestREG026CancellationDuringCopyCleansPartialSourceTree(t *testing.T) {
	repository, revision := initTestRepository(t, map[string]string{"large.txt": strings.Repeat("x", 4096)})
	tempRoot := t.TempDir()
	destination := filepath.Join(t.TempDir(), "prepared")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	git := &cancelDuringGit{cancel: cancel, trigger: "HEAD^{commit}"}
	_, err := Collect(ctx, graphForSources(revision, repository, catalog.SourceUnit{ID: "source"}), destination,
		Options{Git: git, Policy: allowTestRepository{}, TempDir: tempRoot})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect error = %v, want context.Canceled", err)
	}
	assertCollectorTemporaryPathsRemoved(t, tempRoot, git)
	target := filepath.Join(destination, "components", "engine", "default", "sources", "source")
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("partial copied source tree remains after cancellation: %v", err)
	}
}

// TestREG018MissingCommitAndMalformedRepositoryFailClosed checks immutable
// object retrieval failures and ensures neither temporary nor destination trees
// survive either failure.
func TestREG018MissingCommitAndMalformedRepositoryFailClosed(t *testing.T) {
	validRepository, _ := initTestRepository(t, nil)
	malformedRepository := t.TempDir()
	for _, testCase := range []struct {
		name       string
		repository string
		revision   string
	}{
		{name: "missing object", repository: validRepository, revision: strings.Repeat("0", 40)},
		{name: "malformed repository", repository: malformedRepository, revision: strings.Repeat("1", 40)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tempRoot := t.TempDir()
			destination := filepath.Join(t.TempDir(), "prepared")
			_, err := Collect(context.Background(), graphForSources(testCase.revision, testCase.repository, catalog.SourceUnit{ID: "source"}), destination,
				Options{Git: CommandGit{}, Policy: allowTestRepository{}, TempDir: tempRoot})
			if err == nil {
				t.Fatal("expected declared commit retrieval to fail")
			}
			if _, statErr := os.Lstat(filepath.Join(destination, "components", "engine", "default", "sources", "source")); !os.IsNotExist(statErr) {
				t.Fatalf("failed source target remains: %v", statErr)
			}
			for _, pattern := range []string{"registry-git-source-*", "registry-disabled-hooks-*"} {
				matches, globErr := filepath.Glob(filepath.Join(tempRoot, pattern))
				if globErr != nil || len(matches) != 0 {
					t.Fatalf("temporary artifacts remain for %s: %v (%v)", pattern, matches, globErr)
				}
			}
		})
	}
}

// TestREG026AmbientGlobalHooksAndFiltersAreIgnored verifies hostile global Git
// config cannot activate a checkout hook or smudge filter in the Collector.
func TestREG026AmbientGlobalHooksAndFiltersAreIgnored(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-based hostile Git filter fixture is Unix-specific")
	}
	repository, revision := initTestRepository(t, map[string]string{
		".gitattributes": "payload.txt filter=evil\n",
		"payload.txt":    "unmodified payload\n",
	})
	root := t.TempDir()
	marker := filepath.Join(root, "filter-or-hook-ran")
	hooks := filepath.Join(root, "hostile-hooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(hooks, "post-checkout")
	hookBody := "#!/bin/sh\nprintf hook > " + marker + "\n"
	if err := os.WriteFile(hook, []byte(hookBody), 0o700); err != nil {
		t.Fatal(err)
	}
	globalConfig := filepath.Join(root, "hostile.gitconfig")
	configBody := "[core]\n\thooksPath = " + hooks + "\n[filter \"evil\"]\n\tsmudge = /bin/sh -c \"printf filter > " + marker + "; cat\"\n\trequired = true\n"
	if err := os.WriteFile(globalConfig, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "0")
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	destination := filepath.Join(t.TempDir(), "prepared")
	graph := graphForSources(revision, repository, catalog.SourceUnit{ID: "source"})
	if _, err := Collect(context.Background(), graph, destination, Options{Git: CommandGit{}, Policy: allowTestRepository{}}); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(destination, "components", "engine", "default", "sources", "source", "payload.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "unmodified payload\n" {
		t.Fatalf("hostile global smudge filter transformed source bytes: %q", payload)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("hostile global hook/filter executed: %v", err)
	}
}


// TestREG026SubmodulesAreNotFetchedOrMaterialized keeps source collection
// confined to the declared commit tree even when ambient Git config requests
// recursive submodule checkout.
func TestREG026SubmodulesAreNotFetchedOrMaterialized(t *testing.T) {
	child, _ := initTestRepository(t, map[string]string{"child.txt": "must not be collected"})
	parent, _ := initTestRepository(t, map[string]string{"parent.txt": "parent only"})
	command := exec.Command("git", "-c", "protocol.file.allow=always", "-C", parent, "submodule", "add", child, "modules/child")
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Registry Test",
		"GIT_AUTHOR_EMAIL=registry-test@example.invalid",
		"GIT_COMMITTER_NAME=Registry Test",
		"GIT_COMMITTER_EMAIL=registry-test@example.invalid",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create submodule fixture: %v: %s", err, output)
	}
	command = exec.Command("git", "-C", parent, "commit", "--quiet", "-m", "add submodule")
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Registry Test",
		"GIT_AUTHOR_EMAIL=registry-test@example.invalid",
		"GIT_COMMITTER_NAME=Registry Test",
		"GIT_COMMITTER_EMAIL=registry-test@example.invalid",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("commit submodule fixture: %v: %s", err, output)
	}
	command = exec.Command("git", "-C", parent, "rev-parse", "HEAD")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	revision := strings.TrimSpace(string(output))
	globalConfig := filepath.Join(t.TempDir(), "submodule.gitconfig")
	if err := os.WriteFile(globalConfig, []byte("[submodule]\n\trecurse = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "0")
	destination := filepath.Join(t.TempDir(), "prepared")
	graph := graphForSources(revision, parent, catalog.SourceUnit{ID: "source"})
	if _, err := Collect(context.Background(), graph, destination, Options{Git: CommandGit{}, Policy: allowTestRepository{}}); err != nil {
		t.Fatal(err)
	}
	collectedRoot := filepath.Join(destination, "components", "engine", "default", "sources", "source")
	if _, err := os.Stat(filepath.Join(collectedRoot, "parent.txt")); err != nil {
		t.Fatalf("parent source is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(collectedRoot, "modules", "child", "child.txt")); !os.IsNotExist(err) {
		t.Fatalf("submodule content was fetched/materialized: %v", err)
	}
}
