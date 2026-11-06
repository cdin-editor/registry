// Package workspace publishes a complete, verified Registry preparation as
// one directory. It coordinates the Collector, Manifest and Report without
// taking over version selection or executing collected source code.
//
// Spec traceability:
//   - REG-020: paths remain relative and confined to the workspace root.
//   - REG-021: use the canonical prepared/components/<id>/<variant>/sources/<id>
//     layout plus registry-manifest.json and resolution-report.json.
//   - REG-022: stage privately and publish only after every source, document and
//     schema check succeeds; failure never publishes a partial workspace.
//   - REG-023/024/025: output reproducibility documents are complete and
//     deterministic; failed resolution cannot be prepared.
//   - Architecture: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry.md#5-source-collection-and-handoff
//   - Contracts: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#6-source-collection-and-workspace
//   - Local design: https://github.com/cdin-editor/registry/blob/main/docs/implementation/workspace.md
//
// A private staging directory is created beside the final destination so the
// final directory rename stays on one filesystem. Existing destinations are
// never intentionally overwritten. A per-destination exclusive lock also
// serializes concurrent Registry publishers targeting the same path.
package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cdin-editor/registry/internal/collector"
	"github.com/cdin-editor/registry/internal/manifest"
	"github.com/cdin-editor/registry/internal/resolver"
	"github.com/cdin-editor/registry/internal/validation"
)

const (
	manifestFilename = "registry-manifest.json"
	reportFilename   = "resolution-report.json"
)

// stagedFile is the minimum file contract needed for durable staged documents.
// The unexported hook seam below lets package tests inject write and sync faults
// without changing the public Workspace API.
type stagedFile interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type prepareHooks struct {
	afterCollect      func([]collector.Source) error
	afterManifest     func([]byte) error
	beforeReportWrite func() error
	afterReport       func([]byte) error
	beforePublish     func() error
	openFile          func(string, int, os.FileMode) (stagedFile, error)
	rename            func(string, string) error
}

// Options contains the external boundaries required for one prepare operation.
// Destination must not already exist; Registry never mutates an older workspace.
type Options struct {
	Destination      string
	RegistryVersion  string
	TempDir          string
	Git              collector.GitRunner
	RepositoryPolicy collector.RepositoryPolicy
	Credentials      collector.GitCredentialProvider
	Validator        *validation.Validator
}

// Prepared is returned only after the complete workspace is atomically
// published. Digests are detached SHA-256 values over exact canonical bytes.
type Prepared struct {
	WorkspacePath  string
	ManifestPath   string
	ReportPath     string
	ManifestSHA256 string
	ReportSHA256   string
	Sources        []collector.Source
}

// Prepare collects a successful resolver graph, builds and validates both
// public output documents, then atomically publishes the complete workspace.
// It returns no Prepared value unless the final rename succeeds.
func Prepare(ctx context.Context, resolution resolver.Result, options Options) (Prepared, error) {
	return prepare(ctx, resolution, options, prepareHooks{})
}

// prepare keeps failure injection private to this package. Production callers
// always use Prepare and cannot replace filesystem operations or stage hooks.
func prepare(ctx context.Context, resolution resolver.Result, options Options, hooks prepareHooks) (Prepared, error) {
	if ctx == nil {
		return Prepared{}, fmt.Errorf("workspace: context is required")
	}
	if err := ctx.Err(); err != nil {
		return Prepared{}, err
	}
	if resolution.Graph == nil || resolution.Report.Result != "success" || len(resolution.Report.Conflicts) != 0 {
		return Prepared{}, fmt.Errorf("workspace: only a successful conflict-free resolution can be prepared")
	}
	if options.Destination == "" {
		return Prepared{}, fmt.Errorf("workspace: destination is required")
	}
	if options.Git == nil || options.RepositoryPolicy == nil || options.Validator == nil {
		return Prepared{}, fmt.Errorf("workspace: GitRunner, RepositoryPolicy and Validator are required")
	}
	if strings.TrimSpace(options.RegistryVersion) == "" {
		return Prepared{}, fmt.Errorf("workspace: Registry version is required")
	}

	destination, err := filepath.Abs(options.Destination)
	if err != nil {
		return Prepared{}, fmt.Errorf("workspace: resolve destination: %w", err)
	}
	destination = filepath.Clean(destination)
	base := filepath.Base(destination)
	parent := filepath.Dir(destination)
	if base == "." || base == string(filepath.Separator) || parent == destination {
		return Prepared{}, fmt.Errorf("workspace: destination must be a non-root directory")
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Prepared{}, fmt.Errorf("workspace: create destination parent: %w", err)
	}
	// Resolve OS-owned symlink aliases such as macOS /tmp before applying
	// root-confinement checks. All subsequent writes use this canonical parent.
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return Prepared{}, fmt.Errorf("workspace: canonicalize destination parent: %w", err)
	}
	destination = filepath.Join(parent, base)
	if err := ensureNoSymlinkAncestors(parent); err != nil {
		return Prepared{}, fmt.Errorf("workspace: unsafe destination parent: %w", err)
	}
	if _, err := os.Lstat(destination); err == nil {
		return Prepared{}, fmt.Errorf("workspace: destination already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Prepared{}, fmt.Errorf("workspace: inspect destination: %w", err)
	}

	lockPath := filepath.Join(parent, "."+base+".registry.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return Prepared{}, fmt.Errorf("workspace: another prepare operation holds destination lock %s", lockPath)
		}
		return Prepared{}, fmt.Errorf("workspace: acquire destination lock: %w", err)
	}
	if closeErr := lock.Close(); closeErr != nil {
		_ = os.Remove(lockPath)
		return Prepared{}, fmt.Errorf("workspace: close destination lock: %w", closeErr)
	}
	defer os.Remove(lockPath)

	if err := ctx.Err(); err != nil {
		return Prepared{}, err
	}
	stage, err := os.MkdirTemp(parent, "."+base+".registry-stage-*")
	if err != nil {
		return Prepared{}, fmt.Errorf("workspace: create same-filesystem staging directory: %w", err)
	}
	defer os.RemoveAll(stage)

	sources, err := collector.Collect(ctx, resolution.Graph, stage, collector.Options{
		Git:         options.Git,
		Policy:      options.RepositoryPolicy,
		Credentials: options.Credentials,
		TempDir:     options.TempDir,
	})
	if err != nil {
		return Prepared{}, fmt.Errorf("workspace: collect selected sources: %w", err)
	}
	if hooks.afterCollect != nil {
		if err := hooks.afterCollect(sources); err != nil {
			return Prepared{}, fmt.Errorf("workspace: after source collection: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return Prepared{}, err
	}

	document, err := manifest.Build(resolution.Graph, sources, options.RegistryVersion)
	if err != nil {
		return Prepared{}, fmt.Errorf("workspace: build Resolved Manifest: %w", err)
	}
	manifestBytes, err := manifest.MarshalCanonical(document)
	if err != nil {
		return Prepared{}, fmt.Errorf("workspace: canonicalize Resolved Manifest: %w", err)
	}
	if err := options.Validator.Validate(validation.ResolvedManifest, manifestBytes, manifestFilename); err != nil {
		return Prepared{}, fmt.Errorf("workspace: validate Resolved Manifest: %w", err)
	}
	if hooks.afterManifest != nil {
		if err := hooks.afterManifest(manifestBytes); err != nil {
			return Prepared{}, fmt.Errorf("workspace: after Manifest validation: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return Prepared{}, err
	}

	reportBytes, err := manifest.MarshalReport(resolution.Report)
	if err != nil {
		return Prepared{}, fmt.Errorf("workspace: canonicalize Resolution Report: %w", err)
	}
	if err := options.Validator.Validate(validation.ResolutionReport, reportBytes, reportFilename); err != nil {
		return Prepared{}, fmt.Errorf("workspace: validate Resolution Report: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Prepared{}, err
	}

	if err := writeExclusiveUsing(stage, manifestFilename, manifestBytes, hooks.openFile); err != nil {
		return Prepared{}, fmt.Errorf("workspace: write Resolved Manifest: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Prepared{}, err
	}
	if hooks.beforeReportWrite != nil {
		if err := hooks.beforeReportWrite(); err != nil {
			return Prepared{}, fmt.Errorf("workspace: before Resolution Report write: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return Prepared{}, err
	}
	if err := writeExclusiveUsing(stage, reportFilename, reportBytes, hooks.openFile); err != nil {
		return Prepared{}, fmt.Errorf("workspace: write Resolution Report: %w", err)
	}
	if hooks.afterReport != nil {
		if err := hooks.afterReport(reportBytes); err != nil {
			return Prepared{}, fmt.Errorf("workspace: after Resolution Report write: %w", err)
		}
	}
	if err := ensureNoSymlinkAncestors(stage); err != nil {
		return Prepared{}, fmt.Errorf("workspace: staging directory changed unexpectedly: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Prepared{}, err
	}
	if hooks.beforePublish != nil {
		if err := hooks.beforePublish(); err != nil {
			return Prepared{}, fmt.Errorf("workspace: before publication: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return Prepared{}, err
	}
	if _, err := os.Lstat(destination); err == nil {
		return Prepared{}, fmt.Errorf("workspace: destination appeared during prepare; refusing to overwrite: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Prepared{}, fmt.Errorf("workspace: recheck destination: %w", err)
	}
	rename := hooks.rename
	if rename == nil {
		rename = renameDirectoryNoReplace
	}
	if err := rename(stage, destination); err != nil {
		return Prepared{}, fmt.Errorf("workspace: publish complete workspace without replacing destination: %w", err)
	}

	return Prepared{
		WorkspacePath:  destination,
		ManifestPath:   filepath.Join(destination, manifestFilename),
		ReportPath:     filepath.Join(destination, reportFilename),
		ManifestSHA256: manifest.Digest(manifestBytes),
		ReportSHA256:   manifest.Digest(reportBytes),
		Sources:        append([]collector.Source(nil), sources...),
	}, nil
}

// writeExclusive writes and fsyncs a complete document before it becomes part
// of the staged workspace. O_EXCL prevents accidental replacement within stage.
func writeExclusive(root, filename string, content []byte) error {
	return writeExclusiveUsing(root, filename, content, nil)
}

func writeExclusiveUsing(root, filename string, content []byte, open func(string, int, os.FileMode) (stagedFile, error)) error {
	target := filepath.Join(root, filename)
	if open == nil {
		open = func(path string, flag int, mode os.FileMode) (stagedFile, error) {
			return os.OpenFile(path, flag, mode)
		}
	}
	file, err := open(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	written, writeErr := file.Write(content)
	if writeErr == nil && written != len(content) {
		writeErr = io.ErrShortWrite
	}
	syncErr := error(nil)
	if writeErr == nil {
		syncErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// ensureNoSymlinkAncestors rejects a symlink at any path component, not only
// the final element. This avoids a trusted-looking workspace path traversing an
// attacker-controlled parent symlink (REG-020/022).
func ensureNoSymlinkAncestors(value string) error {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return err
	}
	current := filepath.Clean(absolute)
	for {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace: path contains symlink ancestor %q", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}
