package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	githubAPIBaseURL = "https://api.github.com"
	githubAPIUserAgent = "cdin-registry/1"
	githubAPIMaxAttempts = 3
)

// ListGitHubReleases implements the ADR-0028 provider adapter. Pagination is
// controlled by the caller so the Catalog loader can enforce a finite complete
// inventory budget and reject partial discovery.
func (f *HTTPFetcher) ListGitHubReleases(ctx context.Context, source ReleaseSource, page, perPage int) ([]GitHubRelease, error) {
	if page < 1 || page > maxGitHubReleasePages || perPage < 1 || perPage > githubReleasePageSize {
		return nil, fmt.Errorf("GITHUB_DISCOVERY_LIMIT: invalid GitHub Release pagination request")
	}
	owner, repository, err := githubRepositoryParts(source.RepositoryURI)
	if err != nil {
		return nil, err
	}
	endpoint := githubAPIBaseURL + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) +
		"/releases?per_page=" + strconv.Itoa(perPage) + "&page=" + strconv.Itoa(page)
	payload, err := f.fetchGitHubAPI(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var releases []GitHubRelease
	if err := json.Unmarshal(payload, &releases); err != nil {
		return nil, fmt.Errorf("GITHUB_DISCOVERY_INVALID_RESPONSE: release inventory JSON is invalid")
	}
	if len(releases) > perPage {
		return nil, fmt.Errorf("GITHUB_DISCOVERY_LIMIT: GitHub returned more releases than the requested page size")
	}
	return releases, nil
}

// ResolveGitHubTagCommit resolves both lightweight and annotated Git tags. The
// bounded tag-object walk prevents recursive or malformed tag chains from
// exhausting resources.
func (f *HTTPFetcher) ResolveGitHubTagCommit(ctx context.Context, source ReleaseSource, tag string) (string, error) {
	owner, repository, err := githubRepositoryParts(source.RepositoryURI)
	if err != nil {
		return "", err
	}
	endpoint := githubAPIBaseURL + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) +
		"/git/ref/tags/" + url.PathEscape(tag)
	payload, err := f.fetchGitHubAPI(ctx, endpoint)
	if err != nil {
		return "", err
	}
	var ref struct {
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	if err := json.Unmarshal(payload, &ref); err != nil || ref.Object.SHA == "" {
		return "", fmt.Errorf("GITHUB_TAG_RESOLUTION_FAILED: GitHub tag reference is invalid")
	}
	object := ref.Object
	for depth := 0; depth < maxGitHubTagDepth; depth++ {
		switch object.Type {
		case "commit":
			if !validGitRevision(object.SHA) {
				return "", fmt.Errorf("GITHUB_TAG_RESOLUTION_FAILED: tag resolved to an invalid commit SHA")
			}
			return strings.ToLower(object.SHA), nil
		case "tag":
			tagURL := githubAPIBaseURL + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) +
				"/git/tags/" + url.PathEscape(object.SHA)
			tagPayload, fetchErr := f.fetchGitHubAPI(ctx, tagURL)
			if fetchErr != nil {
				return "", fetchErr
			}
			var annotated struct {
				Object struct {
					SHA  string `json:"sha"`
					Type string `json:"type"`
				} `json:"object"`
			}
			if err := json.Unmarshal(tagPayload, &annotated); err != nil || annotated.Object.SHA == "" {
				return "", fmt.Errorf("GITHUB_TAG_RESOLUTION_FAILED: annotated tag object is invalid")
			}
			object = annotated.Object
		default:
			return "", fmt.Errorf("GITHUB_TAG_RESOLUTION_FAILED: Git tag does not resolve to a commit")
		}
	}
	return "", fmt.Errorf("GITHUB_TAG_RESOLUTION_FAILED: annotated tag chain exceeds the depth limit")
}

// FetchGitHubReleaseAsset fetches a browser download URL using the same bounded
// HTTPS transport, host policy, redirect checks and public-address restrictions
// as ordinary Registry metadata. The caller independently verifies the
// GitHub API asset size and SHA-256 before parsing it.
func (f *HTTPFetcher) FetchGitHubReleaseAsset(ctx context.Context, assetURI string, maxBytes int64) ([]byte, error) {
	if f == nil || f.client == nil {
		return nil, fmt.Errorf("GITHUB_ASSET_FETCH_FAILED: HTTP fetcher is not initialized")
	}
	if maxBytes <= 0 || maxBytes > maxReleaseMetadataBytes || maxBytes > f.maxBytes {
		return nil, fmt.Errorf("GITHUB_DISCOVERY_LIMIT: metadata asset byte budget is invalid")
	}
	parsed, err := url.Parse(assetURI)
	if err != nil {
		return nil, fmt.Errorf("GITHUB_ASSET_URI_REJECTED: metadata asset URI is invalid")
	}
	if err := f.validateURL(parsed); err != nil {
		return nil, fmt.Errorf("GITHUB_ASSET_URI_REJECTED: metadata asset URI is not permitted")
	}
	payload, err := f.fetch(ctx, assetURI)
	if err != nil {
		return nil, fmt.Errorf("GITHUB_ASSET_FETCH_FAILED: metadata asset retrieval failed")
	}
	if int64(len(payload)) > maxBytes {
		return nil, fmt.Errorf("GITHUB_DISCOVERY_LIMIT: metadata asset exceeds the configured byte limit")
	}
	return payload, nil
}

func githubRepositoryParts(repositoryURI string) (string, string, error) {
	canonical, err := CanonicalGitHubRepositoryURI(repositoryURI)
	if err != nil {
		return "", "", fmt.Errorf("GITHUB_REPOSITORY_URI_REJECTED: repository URI is invalid")
	}
	parsed, _ := url.Parse(canonical)
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) != 2 {
		return "", "", fmt.Errorf("GITHUB_REPOSITORY_URI_REJECTED: repository URI is invalid")
	}
	return segments[0], segments[1], nil
}

func (f *HTTPFetcher) fetchGitHubAPI(ctx context.Context, endpoint string) ([]byte, error) {
	if f == nil || f.client == nil || ctx == nil {
		return nil, fmt.Errorf("GITHUB_DISCOVERY_FAILED: GitHub API transport is unavailable")
	}
	if f.offline {
		return nil, fmt.Errorf("GITHUB_DISCOVERY_OFFLINE_MISS: no verified paginated Release inventory snapshot is cached for this source")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || !parsed.IsAbs() || parsed.Scheme != "https" ||
		parsed.Host != "api.github.com" || parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("GITHUB_API_URI_REJECTED: GitHub API URI is invalid")
	}
	var lastErr error
	for attempt := 0; attempt < githubAPIMaxAttempts; attempt++ {
		payload, fetchErr := f.fetch(ctx, endpoint)
		if fetchErr == nil {
			return payload, nil
		}
		lastErr = fetchErr
		message := strings.ToLower(fetchErr.Error())
		retryable := strings.Contains(message, "http status 403") ||
			strings.Contains(message, "http status 429") ||
			strings.Contains(message, "http status 500") ||
			strings.Contains(message, "http status 502") ||
			strings.Contains(message, "http status 503") ||
			strings.Contains(message, "http status 504")
		if !retryable || attempt+1 == githubAPIMaxAttempts {
			break
		}
		delay := time.Duration(attempt+1) * 200 * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr != nil {
		message := strings.ToLower(lastErr.Error())
		if strings.Contains(message, "http status 403") || strings.Contains(message, "http status 429") {
			return nil, fmt.Errorf("GITHUB_DISCOVERY_RATE_LIMIT: GitHub Release discovery retry budget exhausted")
		}
	}
	return nil, fmt.Errorf("GITHUB_DISCOVERY_FAILED: GitHub Release API request failed")
}

var _ GitHubReleaseClient = (*HTTPFetcher)(nil)
