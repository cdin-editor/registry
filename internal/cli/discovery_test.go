package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/manifest"
	"github.com/cdin-editor/registry/internal/resolver"
)

const discoveryIndexURI = "https://metadata.example.invalid/catalog/index.json"
const discoveryReleaseURI = "https://metadata.example.invalid/releases/engine-1.0.0.json"
const discoveryNewReleaseURI = "https://metadata.example.invalid/releases/engine-2.0.0.json"

func discoveryReleasePayload(t *testing.T, version, status, revision string) []byte {
	t.Helper()
	payload, err := json.Marshal(catalog.Release{
		SchemaVersion: "1.0.0",
		ComponentID:   "engine",
		Variant:       "default",
		Version:       version,
		Status:        status,
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
	return payload
}

type discoveryFixture struct {
	manifestPath  string
	manifestBytes []byte
	productPath   string
	catalogPath   string
	fetcher       cliMetadataFixture
	reportPath    string
}

func writeDiscoveryFixture(
	t *testing.T,
	manifestBytes []byte,
	oldReleasePayload []byte,
	newReleasePayload []byte,
	productConstraint string,
) discoveryFixture {
	t.Helper()
	root := t.TempDir()
	manifestPath := filepath.Join(root, "historical-manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	productPath := filepath.Join(root, "product-definition.yaml")
	product := `schema_version: "1.0.0"
product:
  id: com.example.engine
  name: Example Engine
components:
  - component_id: engine
    variant: default
    version_constraint: "` + productConstraint + `"
environment_contracts: []
policy:
  allow_prerelease: false
`
	if err := os.WriteFile(productPath, []byte(product), 0o600); err != nil {
		t.Fatal(err)
	}

	entries := []catalog.Entry{{
		ComponentID: "engine",
		Variant:     "default",
		Version:     "1.0.0",
		MetadataURI: discoveryReleaseURI,
		MetadataSHA: manifest.Digest(oldReleasePayload),
	}}
	fetcher := cliMetadataFixture{discoveryReleaseURI: oldReleasePayload}
	if newReleasePayload != nil {
		entries = append(entries, catalog.Entry{
			ComponentID: "engine",
			Variant:     "default",
			Version:     "2.0.0",
			MetadataURI: discoveryNewReleaseURI,
			MetadataSHA: manifest.Digest(newReleasePayload),
		})
		fetcher[discoveryNewReleaseURI] = newReleasePayload
	}
	indexBytes, err := json.Marshal(catalog.Index{
		SchemaVersion: "1.0.0",
		CatalogID:     "current",
		Entries:       entries,
	})
	if err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(root, "current-catalog-index.json")
	if err := os.WriteFile(catalogPath, indexBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return discoveryFixture{
		manifestPath:  manifestPath,
		manifestBytes: append([]byte(nil), manifestBytes...),
		productPath:   productPath,
		catalogPath:   catalogPath,
		fetcher:       fetcher,
		reportPath:    filepath.Join(root, "update-report.json"),
	}
}

func runDiscoveryFixture(t *testing.T, fixture discoveryFixture, constraint string, extraArgs ...string) (int, manifest.UpdateDiscoveryReport, bytes.Buffer, bytes.Buffer) {
	t.Helper()
	args := []string{
		"discover",
		"--input", fixture.manifestPath,
		"--product", fixture.productPath,
		"--catalog", fixture.catalogPath,
		"--allow-host", "metadata.example.invalid",
	}
	if constraint == "write-report" {
		args = append(args, "--report", fixture.reportPath)
	}
	args = append(args, extraArgs...)
	var stdout, stderr bytes.Buffer
	code := runManifestWithDependencies(
		context.Background(), args, strings.NewReader(""),
		&stdout, &stderr,
		func(options catalog.HTTPFetcherOptions) (catalog.Fetcher, error) {
			return fixture.fetcher, nil
		},
		nil,
	)
	var report manifest.UpdateDiscoveryReport
	if len(stdout.Bytes()) > 0 {
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &report); err != nil {
			t.Fatalf("decode discovery report: %v: %s", err, stdout.String())
		}
	}
	return code, report, stdout, stderr
}

func TestManifestDiscoverReportsUnchangedAndWritesImmutableAuditFile(t *testing.T) {
	revision := strings.Repeat("a", 40)
	historical, _, metadata := buildCLIReplayManifest(t, revision, true)
	oldRelease := metadata[discoveryReleaseURI]
	fixture := writeDiscoveryFixture(t, historical, oldRelease, nil, "=1.0.0")

	code, report, stdout, stderr := runDiscoveryFixture(t, fixture, "write-report")
	if code != exitOK {
		t.Fatalf("discover exit=%d stderr=%q", code, stderr.String())
	}
	if report.Result != manifest.UpdateResultNoChanges {
		t.Fatalf("report result = %q, want no_changes", report.Result)
	}
	if report.HistoricalManifestSHA256 != manifest.Digest(historical) {
		t.Fatalf("historical identity = %q, want exact Manifest SHA-256", report.HistoricalManifestSHA256)
	}
	if len(report.Decisions) != 1 ||
		report.Decisions[0].SelectionReasonCode != manifest.UpdateReasonUnchanged ||
		report.Decisions[0].HistoricalReleaseState != "active" {
		t.Fatalf("unchanged selection decision = %+v", report.Decisions)
	}
	if len(report.CandidateDecisions) != 1 ||
		report.CandidateDecisions[0].ReasonCode != manifest.UpdateCandidateSelected {
		t.Fatalf("candidate decision = %+v", report.CandidateDecisions)
	}
	storedReport, err := os.ReadFile(fixture.reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedReport, bytes.TrimSpace(stdout.Bytes())) {
		t.Fatal("saved report bytes differ from the canonical stdout report")
	}
	after, err := os.ReadFile(fixture.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, historical) {
		t.Fatal("update discovery modified the historical Manifest")
	}

	// Report paths are immutable: a second run cannot replace the first audit
	// record, even when the same inputs are supplied.
	args := []string{
		"discover", "--input", fixture.manifestPath, "--product", fixture.productPath,
		"--catalog", fixture.catalogPath, "--allow-host", "metadata.example.invalid",
		"--report", fixture.reportPath,
	}
	var retryOut, retryErr bytes.Buffer
	code = runManifestWithDependencies(context.Background(), args, strings.NewReader(""), &retryOut, &retryErr,
		func(catalog.HTTPFetcherOptions) (catalog.Fetcher, error) { return fixture.fetcher, nil }, nil)
	if code != exitInvalid || !strings.Contains(retryErr.String(), "DISCOVERY_REPORT_FILE_FAILED") {
		t.Fatalf("second report write exit=%d stderr=%q; want no-replace failure", code, retryErr.String())
	}
	storedAgain, err := os.ReadFile(fixture.reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedAgain, storedReport) {
		t.Fatal("existing audit report was modified by a repeated discovery")
	}
}

func TestManifestDiscoverRecordsAvailableUpdateAndCandidateReasons(t *testing.T) {
	revision := strings.Repeat("b", 40)
	historical, _, metadata := buildCLIReplayManifest(t, revision, true)
	oldRelease := metadata[discoveryReleaseURI]
	newRelease := discoveryReleasePayload(t, "2.0.0", "active", strings.Repeat("c", 40))
	fixture := writeDiscoveryFixture(t, historical, oldRelease, newRelease, ">=1.0.0 <3.0.0")

	code, report, _, stderr := runDiscoveryFixture(t, fixture, "")
	if code != exitOK {
		t.Fatalf("discover exit=%d stderr=%q", code, stderr.String())
	}
	if report.Result != manifest.UpdateResultChangesDetected {
		t.Fatalf("report result = %q, want changes_detected", report.Result)
	}
	if len(report.Decisions) != 1 ||
		report.Decisions[0].SelectionReasonCode != manifest.UpdateReasonUpdateAvailable ||
		report.Decisions[0].Current == nil ||
		report.Decisions[0].Current.Version != "2.0.0" {
		t.Fatalf("update decision = %+v", report.Decisions)
	}
	if len(report.CandidateDecisions) != 2 {
		t.Fatalf("candidate decision count = %d, want 2", len(report.CandidateDecisions))
	}
	foundSelected, foundHistorical := false, false
	for _, candidate := range report.CandidateDecisions {
		if candidate.Version == "2.0.0" && candidate.ReasonCode == manifest.UpdateCandidateSelected {
			foundSelected = true
		}
		if candidate.Version == "1.0.0" {
			foundHistorical = true
		}
	}
	if !foundSelected || !foundHistorical {
		t.Fatalf("audit candidate decisions lack selected/current and historical candidates: %+v", report.CandidateDecisions)
	}
}

func TestManifestDiscoverPreservesWithdrawalDecisionWhenMetadataDigestChanges(t *testing.T) {
	revision := strings.Repeat("d", 40)
	historical, _, _ := buildCLIReplayManifest(t, revision, true)
	// Withdrawal is published as a new immutable Release Metadata document for
	// the same version, so its digest differs from the historical Manifest.
	withdrawn := discoveryReleasePayload(t, "1.0.0", "withdrawn", revision)
	fixture := writeDiscoveryFixture(t, historical, withdrawn, nil, "=1.0.0")

	code, report, stdout, stderr := runDiscoveryFixture(t, fixture, "")
	if code != exitInvalid {
		t.Fatalf("discover exit=%d, want resolution failure; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if report.Result != manifest.UpdateResultResolutionFailed {
		t.Fatalf("report result = %q, want resolution_failed", report.Result)
	}
	if len(report.Decisions) != 1 ||
		report.Decisions[0].HistoricalReleaseState != "withdrawn" ||
		report.Decisions[0].SelectionReasonCode != manifest.UpdateReasonHistoricalWithdrawn {
		t.Fatalf("withdrawal decision = %+v", report.Decisions)
	}
	if len(report.CandidateDecisions) != 1 ||
		report.CandidateDecisions[0].ReasonCode != string(resolver.RejectionWithdrawn) {
		t.Fatalf("withdrawn candidate audit = %+v", report.CandidateDecisions)
	}
}

func TestManifestDiscoverDoesNotMutateManifestOrChangePinnedReplay(t *testing.T) {
	repository, revision := createCLIReplayRepository(t)
	historical, _, metadata := buildCLIReplayManifest(t, revision, true)
	oldRelease := metadata[discoveryReleaseURI]
	newRelease := discoveryReleasePayload(t, "2.0.0", "active", strings.Repeat("e", 40))
	fixture := writeDiscoveryFixture(t, historical, oldRelease, newRelease, ">=1.0.0 <3.0.0")

	code, report, _, stderr := runDiscoveryFixture(t, fixture, "")
	if code != exitOK || len(report.Decisions) != 1 ||
		report.Decisions[0].SelectionReasonCode != manifest.UpdateReasonUpdateAvailable {
		t.Fatalf("discovery exit=%d decision=%+v stderr=%q", code, report.Decisions, stderr.String())
	}
	beforeReplay, err := os.ReadFile(fixture.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeReplay, historical) {
		t.Fatal("discovery rewrote the historical Manifest before replay")
	}

	replayPath := filepath.Join(t.TempDir(), "replayed-workspace")
	var replayOut, replayErr bytes.Buffer
	replayCode := runManifestWithDependencies(context.Background(), []string{
		"replay", "--input", fixture.manifestPath,
		"--output", replayPath,
		"--allow-host", "source.example.invalid",
	}, strings.NewReader(""), &replayOut, &replayErr, nil,
		cliReplayGitRunner{repositoryURI: replayFixtureRepositoryURI, localPath: repository})
	if replayCode != exitOK {
		t.Fatalf("offline replay after discovery exit=%d stderr=%q", replayCode, replayErr.String())
	}
	replayed, err := os.ReadFile(filepath.Join(replayPath, "registry-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(replayed, historical) {
		t.Fatal("replay after update discovery did not reproduce the historical Manifest bytes")
	}
	afterReplay, err := os.ReadFile(fixture.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterReplay, historical) {
		t.Fatal("replay after discovery modified the historical Manifest")
	}
}

func TestManifestDiscoverRejectsInvalidInputsBeforeCatalogFetch(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "invalid.json")
	productPath := filepath.Join(root, "product.yaml")
	if err := os.WriteFile(manifestPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(productPath, []byte("invalid: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	var stdout, stderr bytes.Buffer
	code := runManifestWithDependencies(context.Background(), []string{
		"discover", "--input", manifestPath, "--product", productPath,
		"--catalog", filepath.Join(root, "missing-index.json"),
		"--allow-host", "metadata.example.invalid",
	}, strings.NewReader(""), &stdout, &stderr,
		func(catalog.HTTPFetcherOptions) (catalog.Fetcher, error) {
			called = true
			return cliMetadataFixture{}, nil
		},
		nil,
	)
	if code != exitInvalid || called {
		t.Fatalf("invalid Manifest discovery exit=%d fetcherCalled=%v stderr=%q", code, called, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("invalid Manifest unexpectedly emitted report: %s", stdout.String())
	}
}
