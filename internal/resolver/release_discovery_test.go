package resolver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/validation"
)

func releaseWithGitHubDiscovery(componentID, version string, releaseID int64, publishedAt string) catalog.Release {
	tag := "v" + version
	assetName := componentID + "-" + version + ".json"
	repositoryURI := "https://github.com/acme/" + componentID
	apiURI := fmt.Sprintf("https://api.github.com/repos/acme/%s/releases/%d", componentID, releaseID)
	assetURI := fmt.Sprintf("%s/releases/download/%s/%s", repositoryURI, tag, assetName)
	metadataSHA := fmt.Sprintf("%064x", releaseID)

	item := release(componentID, "default", version, "active")
	item.SourceUnits[0].RepositoryURI = repositoryURI
	item.MetadataURI = assetURI
	item.MetadataSHA = metadataSHA
	item.Discovery = &catalog.ReleaseDiscoveryProvenance{
		SourceID:          componentID + "-source",
		Provider:          "github-releases-v1",
		RepositoryURI:     repositoryURI,
		ReleaseID:         releaseID,
		ReleaseAPIURI:     apiURI,
		Tag:               tag,
		TagCommit:         testRevision,
		MetadataAssetName: assetName,
		MetadataAssetSize: 256,
		PublishedAt:       publishedAt,
	}
	return item
}

func TestResolverVersionsReportFromEmittedReleaseDiscoveryEvidence(t *testing.T) {
	first := releaseWithGitHubDiscovery("engine", "1.0.0+build.1", 101, "2024-01-01T00:00:00Z")
	second := releaseWithGitHubDiscovery("engine", "1.0.0+build.2", 102, "2026-01-01T00:00:00Z")
	result := Resolve(product(Requirement{
		ComponentID: "engine", Variant: "default", VersionConstraint: ">=1.0.0 <2.0.0",
	}), []*catalog.Catalog{catalogOf("static-mirror", first, second)})

	if result.Graph != nil || len(result.Report.Conflicts) != 1 ||
		result.Report.Conflicts[0].Code != ConflictAmbiguousRelease {
		t.Fatalf("equal-precedence immutable identities should be ambiguous: %+v", result.Report)
	}
	if result.Report.SchemaVersion != "1.2.0" {
		t.Fatalf("report schema version = %q, want 1.2.0 when release provenance is emitted", result.Report.SchemaVersion)
	}
	evidence := result.Report.Conflicts[0].CandidateEvidence
	if len(evidence) != 2 || evidence[0].ReleaseDiscovery == nil || evidence[1].ReleaseDiscovery == nil {
		t.Fatalf("ambiguous candidate evidence lost ADR-0028 provenance: %#v", evidence)
	}

	encoded, err := json.Marshal(result.Report)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := validation.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.Validate(validation.ResolutionReport, encoded, "resolution-report.json"); err != nil {
		t.Fatalf("provenance-bearing Resolution Report did not satisfy the Spec schema: %v\n%s", err, encoded)
	}
}

func TestResolverSelectsBySemVerNotGitHubPublicationTime(t *testing.T) {
	olderVersion := releaseWithGitHubDiscovery("engine", "1.0.0", 201, "2026-10-01T00:00:00Z")
	newerVersion := releaseWithGitHubDiscovery("engine", "2.0.0", 202, "2024-01-01T00:00:00Z")
	result := Resolve(product(Requirement{
		ComponentID: "engine", Variant: "default", VersionConstraint: ">=1.0.0 <3.0.0",
	}), []*catalog.Catalog{catalogOf("static-mirror", olderVersion, newerVersion)})

	if result.Graph == nil || result.Report.Result != "success" {
		t.Fatalf("resolution failed: %+v", result.Report)
	}
	if len(result.Graph.Components) != 1 || result.Graph.Components[0].Version != "2.0.0" {
		t.Fatalf("selected version = %+v, want SemVer 2.0.0 despite its earlier publication timestamp", result.Graph.Components)
	}
	if result.Graph.Components[0].ReleaseDiscovery == nil ||
		result.Graph.Components[0].ReleaseDiscovery.PublishedAt != "2024-01-01T00:00:00Z" {
		t.Fatalf("selected Release provenance was not retained: %#v", result.Graph.Components[0].ReleaseDiscovery)
	}
	if strings.Contains(result.Report.SchemaVersion, "1.1") {
		t.Fatalf("report used legacy schema despite containing GitHub provenance: %q", result.Report.SchemaVersion)
	}
}
