// Package workspace also owns Manifest replay because replay must cross the
// same private staging and atomic no-replace publication boundary as prepare.
//
// Replay verifies a persisted Manifest, reconstructs its already-selected graph,
// recollects only the recorded immutable Git commits, and requires the rebuilt
// canonical Manifest to be byte-identical before publication. It never invokes
// the Resolver or selects a release from current Catalog state.
package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/manifest"
	"github.com/cdin-editor/registry/internal/resolver"
	"github.com/cdin-editor/registry/internal/semver"
	"github.com/cdin-editor/registry/internal/validation"
)

// ReplayOptions controls how a saved Manifest is replayed.
//
// VerifyRemoteMetadata is opt-in. When false, replay is metadata-offline: it
// trusts only the digests and identities already recorded in the verified
// Manifest and does not contact Catalog or release-metadata URIs. Git still
// has to retrieve each exact commit unless the injected GitRunner can access
// the repository locally.
//
// When true, MetadataFetcher must retrieve the exact recorded Catalog Index
// and selected Release Metadata bytes. Every response is checked against its
// recorded SHA-256 and public schema before its identity is compared with the
// Manifest. Current Catalog contents are never used for version selection.
type ReplayOptions struct {
	Workspace            Options
	MetadataFetcher      catalog.Fetcher
	VerifyRemoteMetadata bool
}

// Replay consumes a persisted canonical Manifest and publishes a new workspace
// containing only the exact full source commits it records (REG-018/023/024/026).
// It never invokes Resolver/version selection or executes collected source.
//
// The Manifest is verified before remote metadata or Git access. Rebuilt
// canonical bytes must equal the input inside private staging before publication;
// a mismatch cannot leave a partial or divergent destination.
// Spec: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#7-resolved-manifest-and-resolution-report
// Trust boundary: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#8-security-and-trust-boundary
// Design: https://github.com/cdin-editor/registry/blob/main/docs/implementation/manifest.md and workspace.md.
func Replay(ctx context.Context, canonicalManifest []byte, expectedSHA256 string, options ReplayOptions) (Prepared, error) {
	if ctx == nil {
		return Prepared{}, fmt.Errorf("workspace: replay context is required")
	}
	if err := ctx.Err(); err != nil {
		return Prepared{}, err
	}

	validator := options.Workspace.Validator
	if validator == nil {
		var err error
		validator, err = validation.New()
		if err != nil {
			return Prepared{}, fmt.Errorf("workspace: initialize replay validator: %w", err)
		}
	}
	if err := manifest.Verify(canonicalManifest, expectedSHA256, validator); err != nil {
		return Prepared{}, fmt.Errorf("workspace: verify replay Manifest: %w", err)
	}

	var document manifest.Document
	if err := json.Unmarshal(canonicalManifest, &document); err != nil {
		return Prepared{}, fmt.Errorf("workspace: decode verified replay Manifest: %w", err)
	}
	if options.Workspace.GitObjectCache != nil {
		release, err := options.Workspace.GitObjectCache.BeginReplayLease(ctx, canonicalManifest)
		if err != nil {
			return Prepared{}, fmt.Errorf("workspace: acquire replay Git-object lease: %w", err)
		}
		defer release()
	}
	if options.VerifyRemoteMetadata {
		if options.MetadataFetcher == nil {
			return Prepared{}, fmt.Errorf("workspace: remote metadata verification requires a MetadataFetcher")
		}
		if leaser, ok := options.MetadataFetcher.(interface {
			BeginReplayLease(context.Context, []byte) (func(), error)
		}); ok {
			release, err := leaser.BeginReplayLease(ctx, canonicalManifest)
			if err != nil {
				return Prepared{}, fmt.Errorf("workspace: acquire replay metadata lease: %w", err)
			}
			defer release()
		}
		if err := verifyReplayMetadata(ctx, document, options.MetadataFetcher, validator); err != nil {
			return Prepared{}, err
		}
	}

	resolution, err := resolutionFromManifest(document)
	if err != nil {
		return Prepared{}, fmt.Errorf("workspace: reconstruct pinned replay graph: %w", err)
	}
	workspaceOptions := options.Workspace
	if workspaceOptions.RegistryVersion != "" && workspaceOptions.RegistryVersion != document.RegistryVersion {
		return Prepared{}, fmt.Errorf("workspace: replay Registry version %q differs from Manifest version %q",
			workspaceOptions.RegistryVersion, document.RegistryVersion)
	}
	workspaceOptions.RegistryVersion = document.RegistryVersion
	workspaceOptions.Validator = validator

	hooks := prepareHooks{
		afterManifest: func(rebuilt []byte) error {
			if !bytes.Equal(rebuilt, canonicalManifest) {
				return fmt.Errorf("rebuilt Manifest differs from the verified input")
			}
			return nil
		},
	}
	prepared, err := prepare(ctx, resolution, workspaceOptions, hooks)
	if err != nil {
		return Prepared{}, fmt.Errorf("workspace: replay: %w", err)
	}
	return prepared, nil
}

func resolutionFromManifest(document manifest.Document) (resolver.Result, error) {
	graph := &resolver.Graph{
		SchemaVersion:        document.SchemaVersion,
		Catalogs:             make([]resolver.CatalogReference, 0, len(document.Catalogs)),
		Roots:                make([]resolver.Requirement, 0, len(document.Roots)),
		EnvironmentContracts: make([]resolver.EnvironmentContract, 0, len(document.EnvironmentContracts)),
		Components:           make([]resolver.ResolvedComponent, 0, len(document.Components)),
		ResolutionOrder:      append([]resolver.Node(nil), document.ResolutionOrder...),
	}
	if document.Resolution != nil {
		graph.Resolution = *document.Resolution
	}
	for _, item := range document.Catalogs {
		graph.Catalogs = append(graph.Catalogs, resolver.CatalogReference{
			CatalogID: item.CatalogID, MetadataURI: item.MetadataURI, MetadataSHA: item.MetadataSHA,
		})
	}
	for _, item := range document.Roots {
		graph.Roots = append(graph.Roots, resolver.Requirement{
			ComponentID: item.ComponentID, Variant: item.Variant, VersionConstraint: "=" + item.Version,
		})
	}
	for _, item := range document.EnvironmentContracts {
		graph.EnvironmentContracts = append(graph.EnvironmentContracts, resolver.EnvironmentContract{
			ContractID: item.ContractID, Version: item.Version,
		})
	}

	report := resolver.Report{
		SchemaVersion:      "1.1.0",
		Result:             "success",
		Selected:           make([]resolver.SelectedCandidate, 0, len(document.Components)),
		AppliedConstraints: []resolver.AppliedConstraint{},
		RejectedCandidates: []resolver.RejectedCandidate{},
		Conflicts:          []resolver.Conflict{},
		Warnings: []resolver.Warning{{
			Code:    "MANIFEST_REPLAY_PINNED",
			Message: "Replayed the exact selected versions, source commits and immutable package artifacts from the verified Manifest; version and artifact selection were not rerun.",
		}},
	}
	for _, item := range document.Components {
		component := resolver.ResolvedComponent{
			ComponentID:        item.ComponentID,
			Variant:            item.Variant,
			Version:            item.Version,
			ReleaseMetadataURI: item.ReleaseMetadataURI,
			ReleaseMetadataSHA: item.ReleaseMetadataSHA,
			Delivery:           item.Delivery,
			SourceUnits:        make([]catalog.SourceUnit, 0, len(item.SourceUnits)),
			Dependencies:       make([]resolver.ResolvedDependency, 0, len(item.Dependencies)),
			Contracts:          item.Contracts,
		}
		if item.Delivery == "artifact" {
			if item.SelectedArtifact == nil {
				return resolver.Result{}, fmt.Errorf("artifact component is missing its selected artifact descriptor")
			}
			selected := item.SelectedArtifact.Artifact
			component.SelectedArtifact = &selected
			for _, source := range item.SelectedArtifact.SourceProvenance {
				component.SourceUnits = append(component.SourceUnits, catalog.SourceUnit{
					ID: source.SourceUnitID, RepositoryURI: source.RepositoryURI,
					Revision: source.Revision, Subdirectory: source.Subdirectory,
				})
			}
		} else {
			for _, source := range item.SourceUnits {
				component.SourceUnits = append(component.SourceUnits, catalog.SourceUnit{
					ID: source.SourceUnitID, RepositoryURI: source.RepositoryURI,
					Revision: source.Revision, Subdirectory: source.Subdirectory,
				})
			}
		}
		for _, dependency := range item.Dependencies {
			component.Dependencies = append(component.Dependencies, resolver.ResolvedDependency{
				ComponentID: dependency.ComponentID, Variant: dependency.Variant, Version: dependency.Version,
			})
			report.AppliedConstraints = append(report.AppliedConstraints, resolver.AppliedConstraint{
				ComponentID:       dependency.ComponentID,
				Variant:           dependency.Variant,
				VersionConstraint: "=" + dependency.Version,
				Source: resolver.ConstraintSource{
					Type:        resolver.ConstraintSourceDependency,
					ComponentID: item.ComponentID,
					Variant:     item.Variant,
					Version:     item.Version,
				},
			})
		}
		graph.Components = append(graph.Components, component)
		report.Selected = append(report.Selected, resolver.SelectedCandidate{
			ComponentID: item.ComponentID, Variant: item.Variant, Version: item.Version,
		})
	}
	for _, root := range document.Roots {
		report.AppliedConstraints = append(report.AppliedConstraints, resolver.AppliedConstraint{
			ComponentID:       root.ComponentID,
			Variant:           root.Variant,
			VersionConstraint: "=" + root.Version,
			Source:            resolver.ConstraintSource{Type: resolver.ConstraintSourceRoot},
		})
	}
	return resolver.Result{Graph: graph, Report: report}, nil
}

type replayReleaseIdentity struct {
	componentID string
	variant     string
	version     string
}

func verifyReplayMetadata(ctx context.Context, document manifest.Document, fetcher catalog.Fetcher, validator *validation.Validator) error {
	entries := make(map[replayReleaseIdentity][]catalog.Entry)
	for _, recorded := range document.Catalogs {
		payload, err := fetchReplayBytes(ctx, fetcher, recorded.MetadataURI, recorded.MetadataSHA)
		if err != nil {
			return fmt.Errorf("workspace: replay Catalog metadata verification failed: %w", err)
		}
		if err := validator.Validate(validation.CatalogIndex, payload, "catalog-index.json"); err != nil {
			return fmt.Errorf("workspace: replay Catalog metadata schema mismatch")
		}
		var index catalog.Index
		if err := json.Unmarshal(payload, &index); err != nil {
			return fmt.Errorf("workspace: replay Catalog metadata decode failed")
		}
		if index.CatalogID != recorded.CatalogID {
			return fmt.Errorf("workspace: replay Catalog identity mismatch")
		}
		for _, entry := range index.Entries {
			variant := entry.Variant
			if variant == "" {
				variant = "default"
			}
			key := replayReleaseIdentity{componentID: entry.ComponentID, variant: variant, version: entry.Version}
			entries[key] = append(entries[key], entry)
		}
	}

	selected := make(map[replayReleaseIdentity]manifest.Component, len(document.Components))
	for _, component := range document.Components {
		key := replayReleaseIdentity{componentID: component.ComponentID, variant: component.Variant, version: component.Version}
		selected[key] = component
		matches := entries[key]
		if len(matches) == 0 {
			return fmt.Errorf("workspace: replay selected release is absent from recorded Catalog metadata")
		}
		for _, entry := range matches {
			if entry.MetadataURI != component.ReleaseMetadataURI || !strings.EqualFold(entry.MetadataSHA, component.ReleaseMetadataSHA) {
				return fmt.Errorf("workspace: replay selected release provenance differs from recorded Catalog metadata")
			}
		}
	}

	verifiedReleaseBytes := make(map[string][]byte)
	for _, component := range document.Components {
		if err := ctx.Err(); err != nil {
			return err
		}
		cacheKey := component.ReleaseMetadataURI + "\x00" + component.ReleaseMetadataSHA
		payload, ok := verifiedReleaseBytes[cacheKey]
		if !ok {
			var err error
			payload, err = fetchReplayBytes(ctx, fetcher, component.ReleaseMetadataURI, component.ReleaseMetadataSHA)
			if err != nil {
				return fmt.Errorf("workspace: replay release metadata verification failed: %w", err)
			}
			if err := validator.Validate(validation.ComponentRelease, payload, "component-release.json"); err != nil {
				return fmt.Errorf("workspace: replay release metadata schema mismatch")
			}
			verifiedReleaseBytes[cacheKey] = payload
		}
		var release catalog.Release
		if err := json.Unmarshal(payload, &release); err != nil {
			return fmt.Errorf("workspace: replay release metadata decode failed")
		}
		if release.Variant == "" {
			release.Variant = "default"
		}
		if release.ComponentID != component.ComponentID || release.Variant != component.Variant || release.Version != component.Version {
			return fmt.Errorf("workspace: replay release identity mismatch")
		}
		if err := catalog.ValidateReleaseArtifacts(release.SourceUnits, release.Artifacts); err != nil {
			return fmt.Errorf("workspace: replay release artifact metadata is invalid")
		}
		if err := compareReplayRelease(component, release, selected); err != nil {
			return err
		}
	}
	return nil
}

func fetchReplayBytes(ctx context.Context, fetcher catalog.Fetcher, uri, expectedSHA256 string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var (
		payload []byte
		err     error
	)
	if digestFetcher, ok := fetcher.(catalog.DigestFetcher); ok {
		payload, err = digestFetcher.FetchWithDigest(ctx, uri, expectedSHA256)
	} else {
		payload, err = fetcher.Fetch(ctx, uri)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("metadata transport failure")
	}
	if manifest.Digest(payload) != expectedSHA256 {
		return nil, fmt.Errorf("metadata digest mismatch")
	}
	return payload, nil
}

func compareReplayRelease(component manifest.Component, release catalog.Release, selected map[replayReleaseIdentity]manifest.Component) error {
	manifestSources := make(map[string]manifest.SourceProvenance)
	if component.Delivery == "artifact" {
		if component.SelectedArtifact == nil {
			return fmt.Errorf("workspace: replay artifact descriptor is missing")
		}
		for _, source := range component.SelectedArtifact.SourceProvenance {
			manifestSources[source.SourceUnitID] = source
		}
		artifactMatches := 0
		for _, candidate := range release.Artifacts {
			if candidate.ID == component.SelectedArtifact.ID {
				artifactMatches++
				if !reflect.DeepEqual(candidate, component.SelectedArtifact.Artifact) {
					return fmt.Errorf("workspace: replay selected artifact descriptor differs from release metadata")
				}
			}
		}
		if artifactMatches != 1 {
			return fmt.Errorf("workspace: replay selected artifact identity is absent or ambiguous in release metadata")
		}
	} else {
		if len(release.SourceUnits) != len(component.SourceUnits) {
			return fmt.Errorf("workspace: replay source-unit set mismatch")
		}
		for _, source := range component.SourceUnits {
			manifestSources[source.SourceUnitID] = manifest.SourceProvenance{
				SourceUnitID: source.SourceUnitID, RepositoryURI: source.RepositoryURI,
				Revision: source.Revision, Subdirectory: source.Subdirectory,
			}
		}
	}
	seenSources := make(map[string]struct{}, len(manifestSources))
	for _, source := range release.SourceUnits {
		recorded, ok := manifestSources[source.ID]
		if !ok {
			// Artifact manifests intentionally retain only source units used by
			// the selected package, not every auxiliary release source unit.
			if component.Delivery == "artifact" {
				continue
			}
			return fmt.Errorf("workspace: replay source-unit identity mismatch")
		}
		if _, duplicate := seenSources[source.ID]; duplicate {
			return fmt.Errorf("workspace: replay duplicate source-unit identity")
		}
		seenSources[source.ID] = struct{}{}
		if recorded.RepositoryURI != source.RepositoryURI ||
			recorded.Revision != strings.ToLower(source.Revision) ||
			recorded.Subdirectory != source.Subdirectory {
			return fmt.Errorf("workspace: replay source-unit provenance mismatch")
		}
	}
	if len(seenSources) != len(manifestSources) {
		return fmt.Errorf("workspace: replay artifact source provenance is absent from release metadata")
	}

	if len(release.Dependencies) != len(component.Dependencies) {
		return fmt.Errorf("workspace: replay dependency set mismatch")
	}
	manifestDependencies := make(map[replayReleaseIdentity]manifest.Dependency, len(component.Dependencies))
	for _, dependency := range component.Dependencies {
		key := replayReleaseIdentity{componentID: dependency.ComponentID, variant: dependency.Variant}
		if _, duplicate := manifestDependencies[key]; duplicate {
			return fmt.Errorf("workspace: replay duplicate dependency identity")
		}
		manifestDependencies[key] = dependency
	}
	seenDependencies := make(map[replayReleaseIdentity]struct{}, len(release.Dependencies))
	for _, dependency := range release.Dependencies {
		variant := dependency.Variant
		if variant == "" {
			variant = "default"
		}
		key := replayReleaseIdentity{componentID: dependency.ComponentID, variant: variant}
		recorded, ok := manifestDependencies[key]
		if !ok {
			return fmt.Errorf("workspace: replay dependency identity mismatch")
		}
		if _, duplicate := seenDependencies[key]; duplicate {
			return fmt.Errorf("workspace: replay duplicate dependency identity")
		}
		seenDependencies[key] = struct{}{}
		target, ok := selected[replayReleaseIdentity{componentID: dependency.ComponentID, variant: variant, version: recorded.Version}]
		if !ok {
			return fmt.Errorf("workspace: replay dependency target mismatch")
		}
		constraint, err := semver.ParseConstraint(dependency.VersionConstraint)
		if err != nil {
			return fmt.Errorf("workspace: replay dependency constraint is invalid")
		}
		version, err := semver.Parse(target.Version)
		if err != nil || !constraint.Allows(version) {
			return fmt.Errorf("workspace: replay selected dependency does not satisfy recorded release metadata")
		}
	}
	if !sameReplayProviders(component.Contracts.Provides, release.Contracts.Provides) ||
		!sameReplayRequirements(component.Contracts.Requires, release.Contracts.Requires) {
		return fmt.Errorf("workspace: replay contract declarations mismatch")
	}
	return nil
}

func sameReplayProviders(left, right []catalog.ContractProvider) bool {
	left = append([]catalog.ContractProvider(nil), left...)
	right = append([]catalog.ContractProvider(nil), right...)
	less := func(values []catalog.ContractProvider, i, j int) bool {
		if values[i].ContractID != values[j].ContractID {
			return values[i].ContractID < values[j].ContractID
		}
		return values[i].Version < values[j].Version
	}
	sort.Slice(left, func(i, j int) bool { return less(left, i, j) })
	sort.Slice(right, func(i, j int) bool { return less(right, i, j) })
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sameReplayRequirements(left, right []catalog.ContractRequirement) bool {
	left = append([]catalog.ContractRequirement(nil), left...)
	right = append([]catalog.ContractRequirement(nil), right...)
	less := func(values []catalog.ContractRequirement, i, j int) bool {
		if values[i].ContractID != values[j].ContractID {
			return values[i].ContractID < values[j].ContractID
		}
		return values[i].VersionConstraint < values[j].VersionConstraint
	}
	sort.Slice(left, func(i, j int) bool { return less(left, i, j) })
	sort.Slice(right, func(i, j int) bool { return less(right, i, j) })
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
