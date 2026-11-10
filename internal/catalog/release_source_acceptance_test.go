package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type scriptedReleaseDiscoveryClient struct {
	pages      map[int][]GitHubRelease
	payloads   map[string][]byte
	commits    map[string]string
	listErrors map[int]error
	pageCalls  []int
	tagCalls   []string
	assetCalls []string
}

func (c *scriptedReleaseDiscoveryClient) Fetch(context.Context, string) ([]byte, error) {
	return nil, errors.New("unexpected ordinary Catalog metadata fetch")
}

func (c *scriptedReleaseDiscoveryClient) ListGitHubReleases(_ context.Context, _ ReleaseSource, page, perPage int) ([]GitHubRelease, error) {
	c.pageCalls = append(c.pageCalls, page)
	if perPage != githubReleasePageSize {
		return nil, fmt.Errorf("unexpected GitHub page size %d", perPage)
	}
	if err := c.listErrors[page]; err != nil {
		return nil, err
	}
	return append([]GitHubRelease(nil), c.pages[page]...), nil
}

func (c *scriptedReleaseDiscoveryClient) ResolveGitHubTagCommit(_ context.Context, _ ReleaseSource, tag string) (string, error) {
	c.tagCalls = append(c.tagCalls, tag)
	commit, ok := c.commits[tag]
	if !ok {
		return "", fmt.Errorf("no fixture commit for tag %s", tag)
	}
	return commit, nil
}

func (c *scriptedReleaseDiscoveryClient) FetchGitHubReleaseAsset(_ context.Context, assetURI string, maxBytes int64) ([]byte, error) {
	c.assetCalls = append(c.assetCalls, assetURI)
	payload, ok := c.payloads[assetURI]
	if !ok {
		return nil, fmt.Errorf("no fixture payload for %s", assetURI)
	}
	if int64(len(payload)) > maxBytes {
		return nil, fmt.Errorf("fixture asset exceeds requested limit")
	}
	return append([]byte(nil), payload...), nil
}

func singleADR0028Script(t *testing.T, version string, id int64) (
	ReleaseSource,
	GitHubRelease,
	[]byte,
	string,
	*scriptedReleaseDiscoveryClient,
) {
	t.Helper()
	source, release, fixture := testADR0028Fixture(t, version, "2026-11-10T18:00:00Z", id)
	payload := append([]byte(nil), fixture.metadata...)
	commit := fixture.commits[release.TagName]
	client := &scriptedReleaseDiscoveryClient{
		pages: map[int][]GitHubRelease{1: {release}},
		payloads: map[string][]byte{
			release.Assets[0].BrowserDownloadURL: payload,
		},
		commits:    map[string]string{release.TagName: commit},
		listErrors: make(map[int]error),
	}
	return source, release, payload, commit, client
}

func setScriptedAssetPayload(release *GitHubRelease, client *scriptedReleaseDiscoveryClient, payload []byte) {
	sum := sha256.Sum256(payload)
	asset := &release.Assets[0]
	asset.Size = int64(len(payload))
	asset.Digest = "sha256:" + hex.EncodeToString(sum[:])
	client.payloads[asset.BrowserDownloadURL] = append([]byte(nil), payload...)
}

func mutateReleaseMetadata(t *testing.T, payload []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	mutate(document)
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func loadScriptedReleaseSource(ctx context.Context, source ReleaseSource, client *scriptedReleaseDiscoveryClient) (*Catalog, error) {
	return loadFromGitHubReleaseSources(
		ctx,
		Index{SchemaVersion: "1.1.0", CatalogID: "test.catalog", ReleaseSources: []ReleaseSource{source}},
		client,
		&acceptingValidator{},
		1,
	)
}

func TestValidateGitHubReleaseTagRequiresConfiguredPrefixAndStrictSemVer(t *testing.T) {
	source := ReleaseSource{TagPrefix: "v"}
	tests := []struct {
		name    string
		tag     string
		want    string
		wantErr string
	}{
		{name: "stable", tag: "v1.2.3", want: "1.2.3"},
		{name: "prerelease", tag: "v1.2.3-rc.1", want: "1.2.3-rc.1"},
		{name: "build metadata", tag: "v1.2.3+build.4", want: "1.2.3+build.4"},
		{name: "missing prefix", tag: "1.2.3", wantErr: "configured prefix"},
		{name: "empty suffix", tag: "v", wantErr: "configured prefix"},
		{name: "incomplete SemVer", tag: "v1.2", wantErr: "valid SemVer"},
		{name: "leading zero", tag: "v01.2.3", wantErr: "valid SemVer"},
		{name: "whitespace", tag: "v1.2.3 ", wantErr: "valid SemVer"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got, _, err := ValidateGitHubReleaseTag(source, testCase.tag)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != testCase.want {
				t.Fatalf("version = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestLoadFromGitHubReleaseSourcesWalksEveryPageAndIgnoresDrafts(t *testing.T) {
	source, newerRelease, newerPayload, newerCommit, _ := singleADR0028Script(t, "2.0.0", 21)
	_, olderRelease, olderPayload, olderCommit, _ := singleADR0028Script(t, "1.0.0", 20)

	newerRelease.PublishedAt = "2024-01-02T03:04:05Z"
	olderRelease.PublishedAt = "2026-01-02T03:04:05Z"
	firstPage := make([]GitHubRelease, 0, githubReleasePageSize)
	for i := 0; i < githubReleasePageSize-1; i++ {
		firstPage = append(firstPage, GitHubRelease{
			ID: int64(i + 1), TagName: "this-is-a-draft-tag", Draft: true,
		})
	}
	firstPage = append(firstPage, newerRelease)
	client := &scriptedReleaseDiscoveryClient{
		pages: map[int][]GitHubRelease{
			1: firstPage,
			2: {olderRelease},
		},
		payloads: map[string][]byte{
			newerRelease.Assets[0].BrowserDownloadURL: newerPayload,
			olderRelease.Assets[0].BrowserDownloadURL: olderPayload,
		},
		commits: map[string]string{
			newerRelease.TagName: newerCommit,
			olderRelease.TagName: olderCommit,
		},
		listErrors: make(map[int]error),
	}

	loaded, err := loadScriptedReleaseSource(context.Background(), source, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(client.pageCalls) != 2 || client.pageCalls[0] != 1 || client.pageCalls[1] != 2 {
		t.Fatalf("GitHub pages visited = %v, want [1 2]", client.pageCalls)
	}
	if len(client.tagCalls) != 2 || len(client.assetCalls) != 2 {
		t.Fatalf("draft releases reached tag/asset retrieval: tags=%v assets=%v", client.tagCalls, client.assetCalls)
	}
	if len(loaded.Releases) != 2 || loaded.Releases[0].Version != "1.0.0" || loaded.Releases[1].Version != "2.0.0" {
		t.Fatalf("discovered candidates are not SemVer-ordered: %#v", loaded.Releases)
	}
	if loaded.Releases[0].Discovery.PublishedAt != olderRelease.PublishedAt ||
		loaded.Releases[1].Discovery.PublishedAt != newerRelease.PublishedAt {
		t.Fatal("Release publication timestamps were not retained as provenance")
	}
}

func TestLoadFromGitHubReleaseSourcesFailsWhenPageBudgetPreventsCompleteInventory(t *testing.T) {
	source, _, _, _, _ := singleADR0028Script(t, "1.0.0", 22)
	fullDraftPage := make([]GitHubRelease, githubReleasePageSize)
	for i := range fullDraftPage {
		fullDraftPage[i] = GitHubRelease{ID: int64(i + 1), Draft: true}
	}
	client := &scriptedReleaseDiscoveryClient{
		pages:      map[int][]GitHubRelease{},
		payloads:   map[string][]byte{},
		commits:    map[string]string{},
		listErrors: make(map[int]error),
	}
	for page := 1; page <= maxGitHubReleasePages; page++ {
		client.pages[page] = fullDraftPage
	}
	_, err := loadScriptedReleaseSource(context.Background(), source, client)
	if err == nil || !strings.Contains(err.Error(), "limit reached before all pages") {
		t.Fatalf("error = %v, want bounded-inventory failure", err)
	}
	if len(client.pageCalls) != maxGitHubReleasePages || client.pageCalls[len(client.pageCalls)-1] != maxGitHubReleasePages {
		t.Fatalf("page budget calls = %v, want exactly %d pages", client.pageCalls, maxGitHubReleasePages)
	}
}

func TestLoadFromGitHubReleaseSourcesRejectsOversizedPage(t *testing.T) {
	source, _, _, _, _ := singleADR0028Script(t, "1.0.0", 23)
	oversized := make([]GitHubRelease, githubReleasePageSize+1)
	for i := range oversized {
		oversized[i] = GitHubRelease{ID: int64(i + 1), Draft: true}
	}
	client := &scriptedReleaseDiscoveryClient{
		pages:      map[int][]GitHubRelease{1: oversized},
		payloads:   map[string][]byte{},
		commits:    map[string]string{},
		listErrors: make(map[int]error),
	}
	_, err := loadScriptedReleaseSource(context.Background(), source, client)
	if err == nil || !strings.Contains(err.Error(), "page exceeded the configured item limit") {
		t.Fatalf("error = %v, want oversized-page rejection", err)
	}
	if len(client.pageCalls) != 1 {
		t.Fatalf("page calls = %v, want immediate stop after page 1", client.pageCalls)
	}
}

func TestLoadFromGitHubReleaseSourcesRejectsMissingAndDuplicateMetadataAssets(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GitHubRelease)
		found  string
	}{
		{
			name: "missing metadata asset",
			mutate: func(release *GitHubRelease) {
				release.Assets = nil
			},
			found: "found 0",
		},
		{
			name: "duplicate metadata asset",
			mutate: func(release *GitHubRelease) {
				release.Assets = append(release.Assets, release.Assets[0])
			},
			found: "found 2",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			source, release, _, _, client := singleADR0028Script(t, "1.0.0", 24)
			testCase.mutate(&release)
			client.pages[1] = []GitHubRelease{release}
			_, err := loadScriptedReleaseSource(context.Background(), source, client)
			if err == nil || !strings.Contains(err.Error(), testCase.found) {
				t.Fatalf("error = %v, want asset cardinality failure containing %q", err, testCase.found)
			}
			if len(client.assetCalls) != 0 {
				t.Fatalf("asset fetch occurred despite invalid asset cardinality: %v", client.assetCalls)
			}
		})
	}
}

func TestLoadFromGitHubReleaseSourcesRejectsAssetSizeAndDigestMismatch(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*GitHubRelease)
		wantError string
	}{
		{
			name: "downloaded byte count mismatch",
			mutate: func(release *GitHubRelease) {
				release.Assets[0].Size++
			},
			wantError: "metadata asset size mismatch",
		},
		{
			name: "downloaded digest mismatch",
			mutate: func(release *GitHubRelease) {
				release.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64)
			},
			wantError: "metadata asset digest mismatch",
		},
		{
			name: "malformed API digest",
			mutate: func(release *GitHubRelease) {
				release.Assets[0].Digest = "sha512:" + strings.Repeat("a", 128)
			},
			wantError: "no valid SHA-256 digest",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			source, release, _, _, client := singleADR0028Script(t, "1.0.0", 25)
			testCase.mutate(&release)
			client.pages[1] = []GitHubRelease{release}
			_, err := loadScriptedReleaseSource(context.Background(), source, client)
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("error = %v, want %q", err, testCase.wantError)
			}
		})
	}
}

func TestLoadFromGitHubReleaseSourcesRejectsMalformedMetadataAndPublisherRevisionMismatch(t *testing.T) {
	tests := []struct {
		name    string
		payload func(*testing.T, []byte) []byte
		wantErr string
	}{
		{
			name: "malformed metadata JSON",
			payload: func(_ *testing.T, _ []byte) []byte {
				return []byte("{not-json")
			},
			wantErr: "decode discovered Release Metadata",
		},
		{
			name: "publisher source revision mismatch",
			payload: func(t *testing.T, original []byte) []byte {
				return mutateReleaseMetadata(t, original, func(document map[string]any) {
					units := document["source_units"].([]any)
					units[0].(map[string]any)["revision"] = strings.Repeat("f", 40)
				})
			},
			wantErr: "publisher source revision does not match tag commit",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			source, release, original, _, client := singleADR0028Script(t, "1.0.0", 26)
			payload := testCase.payload(t, original)
			setScriptedAssetPayload(&release, client, payload)
			client.pages[1] = []GitHubRelease{release}
			_, err := loadScriptedReleaseSource(context.Background(), source, client)
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("error = %v, want %q", err, testCase.wantErr)
			}
		})
	}
}


func TestValidateReleaseSourcesRejectsDuplicateSourceIDAndUnsafeTagPrefix(t *testing.T) {
	base := ReleaseSource{
		SourceID: "shared-source", Provider: githubReleaseProvider,
		RepositoryURI: "https://github.com/acme/engine", ComponentID: "engine", Variant: "default",
	}
	duplicateID := base
	duplicateID.RepositoryURI = "https://github.com/acme/other"
	duplicateID.ComponentID = "other"
	if _, err := ValidateReleaseSources([]ReleaseSource{base, duplicateID}); err == nil ||
		!strings.Contains(err.Error(), "duplicate Release Source ID") {
		t.Fatalf("duplicate source ID error = %v", err)
	}

	unsafePrefix := base
	unsafePrefix.TagPrefix = "v/"
	if _, err := ValidateReleaseSources([]ReleaseSource{unsafePrefix}); err == nil {
		t.Fatal("unsafe Release Source tag prefix unexpectedly passed validation")
	}
}

func TestLoadFromGitHubReleaseSourcesRejectsTagAndMetadataVersionMismatch(t *testing.T) {
	source, release, payload, _, client := singleADR0028Script(t, "1.0.0", 27)
	changed := mutateReleaseMetadata(t, payload, func(document map[string]any) {
		document["version"] = "1.0.1"
	})
	setScriptedAssetPayload(&release, client, changed)
	client.pages[1] = []GitHubRelease{release}

	_, err := loadScriptedReleaseSource(context.Background(), source, client)
	if err == nil || !strings.Contains(err.Error(), "identity does not match source/tag") {
		t.Fatalf("error = %v, want tag/metadata version mismatch", err)
	}
}
