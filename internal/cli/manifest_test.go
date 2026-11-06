package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/collector"
	"github.com/cdin-editor/registry/internal/manifest"
	"github.com/cdin-editor/registry/internal/resolver"
)

const replayFixtureRepositoryURI = "https://source.example.invalid/engine.git"

type cliReplayGitRunner struct {
	repositoryURI string
	localPath     string
}

func (g cliReplayGitRunner) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	mapped := append([]string(nil), args...)
	for i := range mapped {
		if mapped[i] == g.repositoryURI {
			mapped[i] = g.localPath
		}
	}
	return (collector.CommandGit{}).Run(ctx, dir, env, mapped...)
}

type cliCancelGitRunner struct {
	cancel context.CancelFunc
}

func (g cliCancelGitRunner) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	g.cancel()
	return nil, context.Canceled
}

type cliMetadataFixture map[string][]byte

func (f cliMetadataFixture) Fetch(ctx context.Context, uri string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, ok := f[uri]
	if !ok {
		return nil, errors.New("fixture metadata unavailable")
	}
	return append([]byte(nil), data...), nil
}

func createCLIReplayRepository(t *testing.T) (string, string) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = directory
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	run("init", "-q")
	run("config", "user.name", "Registry CLI Test")
	run("config", "user.email", "registry-cli-test@example.invalid")
	if err := os.WriteFile(filepath.Join(directory, "engine.c"), []byte("int engine(void) { return 7; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "engine.c")
	run("commit", "-q", "-m", "fixture source")
	revision := run("rev-parse", "HEAD")
	if len(revision) != 40 {
		t.Fatalf("fixture Git commit has unexpected length %d", len(revision))
	}
	return directory, revision
}

func buildCLIReplayManifest(t *testing.T, revision string, strictMetadata bool) ([]byte, string, cliMetadataFixture) {
	t.Helper()
	const indexURI = "https://metadata.example.invalid/catalog/index.json"
	const releaseURI = "https://metadata.example.invalid/releases/engine-1.0.0.json"

	catalogURI := "https://catalog.example.invalid/index.json"
	catalogDigest := strings.Repeat("a", 64)
	releaseMetadataURI := "https://catalog.example.invalid/engine-1.0.0.json"
	releaseDigest := strings.Repeat("b", 64)
	metadata := cliMetadataFixture{}
	if strictMetadata {
		releaseBytes, err := json.Marshal(catalog.Release{
			SchemaVersion: "1.0.0",
			ComponentID:   "engine",
			Variant:       "default",
			Version:       "1.0.0",
			Status:        "active",
			SourceUnits: []catalog.SourceUnit{{
				ID:            "source",
				RepositoryURI: replayFixtureRepositoryURI,
				Revision:      revision,
			}},
			Dependencies: []catalog.Dependency{},
			Contracts: catalog.Contracts{
				Provides: []catalog.ContractProvider{},
				Requires: []catalog.ContractRequirement{},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		releaseDigest = manifest.Digest(releaseBytes)
		releaseMetadataURI = releaseURI
		indexBytes, err := json.Marshal(catalog.Index{
			SchemaVersion: "1.0.0",
			CatalogID:     "stable",
			Entries: []catalog.Entry{{
				ComponentID: "engine",
				Variant:     "default",
				Version:     "1.0.0",
				MetadataURI: releaseURI,
				MetadataSHA: releaseDigest,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		catalogURI = indexURI
		catalogDigest = manifest.Digest(indexBytes)
		metadata[indexURI] = indexBytes
		metadata[releaseURI] = releaseBytes
	}

	graph := &resolver.Graph{
		SchemaVersion: "1.0.0",
		Catalogs: []resolver.CatalogReference{{
			CatalogID:   "stable",
			MetadataURI: catalogURI,
			MetadataSHA: catalogDigest,
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
			ReleaseMetadataURI: releaseMetadataURI,
			ReleaseMetadataSHA: releaseDigest,
			SourceUnits: []catalog.SourceUnit{{
				ID:            "source",
				RepositoryURI: replayFixtureRepositoryURI,
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
	}
	sources := []collector.Source{{
		ComponentID:   "engine",
		Variant:       "default",
		SourceUnitID:  "source",
		Revision:      revision,
		WorkspacePath: "components/engine/default/sources/source",
	}}
	document, err := manifest.Build(graph, sources, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := manifest.MarshalCanonical(document)
	if err != nil {
		t.Fatal(err)
	}
	return canonical, manifest.Digest(canonical), metadata
}

func writeCLIManifest(t *testing.T, payload []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry-manifest.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runCLIManifestReplay(
	ctx context.Context,
	args []string,
	newFetcher catalogFetcherFactory,
	git collector.GitRunner,
	stdout, stderr *bytes.Buffer,
) int {
	return runManifestWithDependencies(ctx, args, strings.NewReader(""), stdout, stderr, newFetcher, git)
}

func TestManifestCLIVerifyFileAndStdin(t *testing.T) {
	_, revision := createCLIReplayRepository(t)
	payload, digest, _ := buildCLIReplayManifest(t, revision, false)
	path := writeCLIManifest(t, payload)

	for _, testCase := range []struct {
		name  string
		args  []string
		stdin string
	}{
		{name: "file", args: []string{"verify", "--input", path, "--sha256", digest}},
		{name: "stdin", args: []string{"verify", "--input", "-"}, stdin: string(payload)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runManifestWithDependencies(context.Background(), testCase.args, strings.NewReader(testCase.stdin), &stdout, &stderr, nil, nil)
			if code != exitOK {
				t.Fatalf("verify exit=%d stderr=%s", code, stderr.String())
			}
			var receipt manifestVerifyReceipt
			if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
				t.Fatalf("decode verification receipt: %v: %s", err, stdout.String())
			}
			if !receipt.Valid || receipt.ManifestSHA256 != digest || receipt.Components != 1 || receipt.SourceUnits != 1 {
				t.Fatalf("unexpected verification receipt: %+v", receipt)
			}
			if stderr.Len() != 0 {
				t.Fatalf("successful verify wrote stderr: %s", stderr.String())
			}
		})
	}
}

func TestManifestCLIVerifyUsesStableInvalidAndIntegrityCodes(t *testing.T) {
	_, revision := createCLIReplayRepository(t)
	payload, digest, _ := buildCLIReplayManifest(t, revision, false)
	path := writeCLIManifest(t, payload)

	cases := []struct {
		name     string
		input    []byte
		digest   string
		wantCode string
	}{
		{name: "invalid canonical bytes", input: append(append([]byte(nil), payload...), ' '), wantCode: "MANIFEST_INVALID"},
		{name: "digest mismatch", input: payload, digest: strings.Repeat("0", 64), wantCode: "MANIFEST_DIGEST_MISMATCH"},
		{name: "invalid digest format", input: payload, digest: "SHA256:" + digest, wantCode: "MANIFEST_DIGEST_INVALID"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			inputPath := writeCLIManifest(t, testCase.input)
			var stdout, stderr bytes.Buffer
			args := []string{"verify", "--input", inputPath}
			if testCase.digest != "" {
				args = append(args, "--sha256", testCase.digest)
			}
			code := runManifestWithDependencies(context.Background(), args, strings.NewReader(""), &stdout, &stderr, nil, nil)
			if code != exitInvalid || !strings.Contains(stderr.String(), "error["+testCase.wantCode+"]") {
				t.Fatalf("verify exit=%d stderr=%s, want code %s", code, stderr.String(), testCase.wantCode)
			}
			if stdout.Len() != 0 {
				t.Fatalf("failed verify wrote stdout: %s", stdout.String())
			}
		})
	}
	var runStdout, runStderr bytes.Buffer
	runCode := Run(context.Background(), []string{"manifest", "verify", "--input", path}, strings.NewReader(""), &runStdout, &runStderr)
	if runCode != exitOK || !bytes.Contains(runStdout.Bytes(), []byte("\"valid\": true")) {
		t.Fatalf("Run did not route manifest verify: exit=%d stdout=%s stderr=%s", runCode, runStdout.String(), runStderr.String())
	}
}

func TestManifestCLIReplayPublishesPinnedWorkspace(t *testing.T) {
	repository, revision := createCLIReplayRepository(t)
	payload, digest, _ := buildCLIReplayManifest(t, revision, false)
	input := writeCLIManifest(t, payload)
	destination := filepath.Join(t.TempDir(), "replayed")
	runner := cliReplayGitRunner{repositoryURI: replayFixtureRepositoryURI, localPath: repository}

	var stdout, stderr bytes.Buffer
	code := runCLIManifestReplay(context.Background(), []string{
		"replay", "--input", input, "--sha256", digest,
		"--output", destination, "--allow-host", "source.example.invalid",
	}, nil, runner, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("replay exit=%d stderr=%s", code, stderr.String())
	}
	var receipt manifestReplayReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
		t.Fatalf("decode replay receipt: %v: %s", err, stdout.String())
	}
	if receipt.ManifestSHA256 != digest || receipt.WorkspacePath == "" || receipt.ReportSHA256 == "" {
		t.Fatalf("unexpected replay receipt: %+v", receipt)
	}
	sourcePath := filepath.Join(destination, "components", "engine", "default", "sources", "source", "engine.c")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(source) != "int engine(void) { return 7; }\n" {
		t.Fatalf("replayed source contents differ: %q", source)
	}
	report, err := os.ReadFile(receipt.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(report, []byte("MANIFEST_REPLAY_PINNED")) {
		t.Fatal("replay report does not identify pinned Manifest replay")
	}
}

func TestManifestCLIReplayRejectsSourceFailureCancellationAndDestinationCollision(t *testing.T) {
	repository, revision := createCLIReplayRepository(t)
	payload, _, _ := buildCLIReplayManifest(t, revision, false)
	input := writeCLIManifest(t, payload)
	runner := cliReplayGitRunner{repositoryURI: replayFixtureRepositoryURI, localPath: repository}

	t.Run("source retrieval failure", func(t *testing.T) {
		badPayload, _, _ := buildCLIReplayManifest(t, strings.Repeat("0", 40), false)
		badInput := writeCLIManifest(t, badPayload)
		destination := filepath.Join(t.TempDir(), "source-failed")
		var stdout, stderr bytes.Buffer
		code := runCLIManifestReplay(context.Background(), []string{
			"replay", "--input", badInput, "--output", destination, "--allow-host", "source.example.invalid",
		}, nil, runner, &stdout, &stderr)
		if code != exitInvalid || !strings.Contains(stderr.String(), "error[REPLAY_SOURCE_FAILED]") {
			t.Fatalf("source failure exit=%d stderr=%s", code, stderr.String())
		}
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatalf("failed source retrieval published destination: %v", err)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		destination := filepath.Join(t.TempDir(), "canceled")
		var stdout, stderr bytes.Buffer
		code := runCLIManifestReplay(ctx, []string{
			"replay", "--input", input, "--output", destination, "--allow-host", "source.example.invalid",
		}, nil, cliCancelGitRunner{cancel: cancel}, &stdout, &stderr)
		if code != exitInvalid || !strings.Contains(stderr.String(), "error[OPERATION_CANCELED]") {
			t.Fatalf("canceled replay exit=%d stderr=%s", code, stderr.String())
		}
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatalf("canceled replay published destination: %v", err)
		}
	})

	t.Run("destination collision", func(t *testing.T) {
		destination := filepath.Join(t.TempDir(), "existing")
		if err := os.Mkdir(destination, 0o700); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(destination, "keep.txt")
		if err := os.WriteFile(marker, []byte("foreign"), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := runCLIManifestReplay(context.Background(), []string{
			"replay", "--input", input, "--output", destination, "--allow-host", "source.example.invalid",
		}, nil, runner, &stdout, &stderr)
		if code != exitInvalid || !strings.Contains(stderr.String(), "error[REPLAY_DESTINATION_EXISTS]") {
			t.Fatalf("collision exit=%d stderr=%s", code, stderr.String())
		}
		if data, err := os.ReadFile(marker); err != nil || string(data) != "foreign" {
			t.Fatalf("foreign destination changed after collision: data=%q err=%v", data, err)
		}
	})
}

func TestManifestCLIReplayStrictMetadataUsesExplicitAllowList(t *testing.T) {
	repository, revision := createCLIReplayRepository(t)
	payload, digest, metadata := buildCLIReplayManifest(t, revision, true)
	input := writeCLIManifest(t, payload)
	destination := filepath.Join(t.TempDir(), "strict-replay")
	fetcher := cliMetadataFixture(metadata)
	factoryCalls := 0
	factory := func(options catalog.HTTPFetcherOptions) (catalog.Fetcher, error) {
		factoryCalls++
		allowed := strings.Join(options.AllowedHosts, ",")
		if !strings.Contains(allowed, "source.example.invalid") || !strings.Contains(allowed, "metadata.example.invalid") {
			t.Fatalf("strict fetcher did not receive explicit host policy: %#v", options.AllowedHosts)
		}
		return fetcher, nil
	}
	var stdout, stderr bytes.Buffer
	code := runCLIManifestReplay(context.Background(), []string{
		"replay", "--input", input, "--sha256", digest,
		"--output", destination,
		"--allow-host", "source.example.invalid",
		"--allow-host", "metadata.example.invalid",
		"--verify-remote-metadata",
	}, factory, cliReplayGitRunner{repositoryURI: replayFixtureRepositoryURI, localPath: repository}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("strict replay exit=%d stderr=%s", code, stderr.String())
	}
	if factoryCalls != 1 {
		t.Fatalf("metadata Fetcher factory called %d times, want once", factoryCalls)
	}
}

func TestManifestCLIUsageErrorsHaveStableExitCodeAndCode(t *testing.T) {
	cases := [][]string{
		{"verify"},
		{"verify", "--input"},
		{"replay"},
		{"replay", "--input", "manifest.json", "--output", "out"},
		{"replay", "--input", "manifest.json", "--allow-host", "source.example.invalid"},
		{"verify", "--input", "manifest.json", "--verify-remote-metadata"},
		{"replay", "--input", "manifest.json", "--output", "out", "--allow-host", "source.example.invalid", "--cache-dir", "cache"},
		{"unknown"},
	}
	for _, args := range cases {
		var stdout, stderr bytes.Buffer
		code := runManifestWithDependencies(context.Background(), args, strings.NewReader(""), &stdout, &stderr, nil, nil)
		if code != exitUsage || !strings.Contains(stderr.String(), "error[USAGE_ERROR]") {
			t.Errorf("args=%v exit=%d stderr=%s, want usage exit 2 and stable USAGE_ERROR", args, code, stderr.String())
		}
	}
}

type cliCountingGitRunner struct {
	delegate collector.GitRunner
	calls    int
}

func (g *cliCountingGitRunner) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	g.calls++
	return g.delegate.Run(ctx, dir, env, args...)
}

func TestManifestCLIReplayRejectsInvalidInputBeforeGitOrPublication(t *testing.T) {
	repository, revision := createCLIReplayRepository(t)
	payload, _, _ := buildCLIReplayManifest(t, revision, false)
	invalidInput := writeCLIManifest(t, append(append([]byte(nil), payload...), ' '))
	destination := filepath.Join(t.TempDir(), "invalid-manifest")
	git := &cliCountingGitRunner{
		delegate: cliReplayGitRunner{repositoryURI: replayFixtureRepositoryURI, localPath: repository},
	}
	var stdout, stderr bytes.Buffer
	code := runCLIManifestReplay(context.Background(), []string{
		"replay", "--input", invalidInput, "--output", destination, "--allow-host", "source.example.invalid",
	}, nil, git, &stdout, &stderr)
	if code != exitInvalid || !strings.Contains(stderr.String(), "error[MANIFEST_INVALID]") {
		t.Fatalf("invalid replay exit=%d stderr=%s", code, stderr.String())
	}
	if git.calls != 0 {
		t.Fatalf("invalid Manifest reached Git %d times before rejection", git.calls)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("invalid Manifest published a destination: %v", err)
	}
}

func TestManifestCLIInputReadFailureHasStableCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runManifestWithDependencies(context.Background(), []string{
		"verify", "--input", filepath.Join(t.TempDir(), "missing-manifest.json"),
	}, strings.NewReader(""), &stdout, &stderr, nil, nil)
	if code != exitInvalid || !strings.Contains(stderr.String(), "error[MANIFEST_INPUT_READ_FAILED]") {
		t.Fatalf("input read failure exit=%d stderr=%s", code, stderr.String())
	}
}

func TestManifestCLIReplayStrictMetadataFailureHasStableCode(t *testing.T) {
	repository, revision := createCLIReplayRepository(t)
	payload, digest, metadata := buildCLIReplayManifest(t, revision, true)
	input := writeCLIManifest(t, payload)
	changedMetadata := cliMetadataFixture{}
	for uri, data := range metadata {
		changedMetadata[uri] = append([]byte(nil), data...)
	}
	for uri := range changedMetadata {
		if strings.Contains(uri, "/catalog/") {
			changedMetadata[uri] = []byte(`{"schema_version":"1.0.0","catalog_id":"stable","entries":[]}`)
		}
	}
	destination := filepath.Join(t.TempDir(), "strict-metadata-rejected")
	var stdout, stderr bytes.Buffer
	code := runCLIManifestReplay(context.Background(), []string{
		"replay", "--input", input, "--sha256", digest,
		"--output", destination,
		"--allow-host", "source.example.invalid",
		"--allow-host", "metadata.example.invalid",
		"--verify-remote-metadata",
	}, func(options catalog.HTTPFetcherOptions) (catalog.Fetcher, error) {
		return changedMetadata, nil
	}, cliReplayGitRunner{repositoryURI: replayFixtureRepositoryURI, localPath: repository}, &stdout, &stderr)
	if code != exitInvalid || !strings.Contains(stderr.String(), "error[REPLAY_METADATA_FAILED]") {
		t.Fatalf("strict metadata failure exit=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("strict metadata failure published a destination: %v", err)
	}
}
