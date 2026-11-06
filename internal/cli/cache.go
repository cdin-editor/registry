package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/collector"
	"github.com/cdin-editor/registry/internal/manifest"
	"github.com/cdin-editor/registry/internal/validation"
)

type cacheReceipt struct {
	Operation         string `json:"operation"`
	ManifestSHA256    string `json:"manifest_sha256,omitempty"`
	RemovedEntries    int    `json:"removed_entries,omitempty"`
	RemovedBytes      int64  `json:"removed_bytes,omitempty"`
	RemainingBytes    int64  `json:"remaining_bytes,omitempty"`
	ProtectedEntries  int    `json:"protected_entries,omitempty"`
	RemovedGitObjects int    `json:"removed_git_objects,omitempty"`
	RemovedGitBytes   int64  `json:"removed_git_bytes,omitempty"`
	RemainingGitBytes int64  `json:"remaining_git_bytes,omitempty"`
	ProtectedGit      int    `json:"protected_git_objects,omitempty"`
}

type cacheCommandOptions struct {
	cacheDir       string
	manifestPath   string
	manifestSHA256 string
}

func runCache(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if ctx == nil {
		return usageError(stderr, "cache context is required")
	}
	if err := ctx.Err(); err != nil {
		fmt.Fprintln(stderr, "registry: cache operation canceled")
		return exitInvalid
	}
	if len(args) == 0 {
		return usageError(stderr, "expected pin, unpin or gc")
	}
	operation := args[0]
	if operation != "pin" && operation != "unpin" && operation != "gc" {
		return usageError(stderr, "unknown cache operation %q", operation)
	}
	options, err := parseCacheCommandOptions(operation, args[1:])
	if err != nil {
		return usageError(stderr, "%v", err)
	}
	metadataCache, err := catalog.NewMetadataCache(options.cacheDir)
	if err != nil {
		fmt.Fprintln(stderr, "registry: cannot initialize metadata cache")
		return exitInvalid
	}
	gitCache, err := collector.NewGitObjectCache(filepath.Join(options.cacheDir, ".git-objects"))
	if err != nil {
		fmt.Fprintln(stderr, "registry: cannot initialize Git-object cache")
		return exitInvalid
	}

	switch operation {
	case "pin":
		input, err := readInput(options.manifestPath, stdin)
		if err != nil {
			fmt.Fprintln(stderr, "registry: cannot read Manifest input")
			return exitInvalid
		}
		validator, err := validation.New()
		if err != nil {
			fmt.Fprintln(stderr, "registry: cannot initialize Registry schema validation")
			return exitInvalid
		}
		if err := manifest.Verify(input.data, "", validator); err != nil {
			fmt.Fprintln(stderr, "registry: Manifest must pass canonical-byte, public-schema and provenance verification before it can be retained")
			return exitInvalid
		}
		id, err := metadataCache.RetainManifest(input.data)
		if err != nil {
			fmt.Fprintln(stderr, "registry: could not retain Manifest metadata references")
			return exitInvalid
		}
		gitID, err := gitCache.RetainManifest(input.data)
		if err != nil {
			_ = metadataCache.ReleaseManifest(id)
			fmt.Fprintln(stderr, "registry: could not retain Manifest Git-object references")
			return exitInvalid
		}
		if gitID != id {
			_ = gitCache.ReleaseManifest(gitID)
			_ = metadataCache.ReleaseManifest(id)
			fmt.Fprintln(stderr, "registry: inconsistent Manifest retention identity")
			return exitInvalid
		}
		return writeCacheReceipt(stdout, stderr, cacheReceipt{Operation: "pin", ManifestSHA256: id})

	case "unpin":
		// Both releases are idempotent, so a transient failure can be retried
		// with the same Manifest digest without losing reference identity.
		if err := gitCache.ReleaseManifest(options.manifestSHA256); err != nil {
			if errors.Is(err, collector.ErrGitObjectCacheBusy) {
				fmt.Fprintln(stderr, "registry: Git-object cache maintenance is busy")
			} else {
				fmt.Fprintln(stderr, "registry: could not release Manifest Git-object pin")
			}
			return exitInvalid
		}
		if err := metadataCache.ReleaseManifest(options.manifestSHA256); err != nil {
			if errors.Is(err, catalog.ErrCacheMaintenanceBusy) {
				fmt.Fprintln(stderr, "registry: metadata cache maintenance is busy")
			} else {
				fmt.Fprintln(stderr, "registry: could not release Manifest metadata pin")
			}
			return exitInvalid
		}
		return writeCacheReceipt(stdout, stderr, cacheReceipt{Operation: "unpin", ManifestSHA256: options.manifestSHA256})

	case "gc":
		metadataResult, err := metadataCache.GarbageCollect(ctx)
		if err != nil {
			if errors.Is(err, catalog.ErrCacheMaintenanceBusy) {
				fmt.Fprintln(stderr, "registry: metadata cache maintenance is busy")
			} else if ctx.Err() != nil {
				fmt.Fprintln(stderr, "registry: metadata cache garbage collection canceled")
			} else {
				fmt.Fprintln(stderr, "registry: metadata cache garbage collection failed closed; reference snapshot is incomplete")
			}
			return exitInvalid
		}
		gitResult, err := gitCache.GarbageCollect(ctx)
		if err != nil {
			if errors.Is(err, collector.ErrGitObjectCacheBusy) {
				fmt.Fprintln(stderr, "registry: Git-object cache maintenance is busy")
			} else if ctx.Err() != nil {
				fmt.Fprintln(stderr, "registry: Git-object cache garbage collection canceled")
			} else {
				fmt.Fprintln(stderr, "registry: Git-object cache garbage collection failed closed; reference snapshot or inventory is incomplete")
			}
			return exitInvalid
		}
		return writeCacheReceipt(stdout, stderr, cacheReceipt{
			Operation:         "gc",
			RemovedEntries:    metadataResult.RemovedEntries,
			RemovedBytes:      metadataResult.RemovedBytes,
			RemainingBytes:    metadataResult.RemainingBytes,
			ProtectedEntries:  metadataResult.ProtectedEntries,
			RemovedGitObjects: gitResult.RemovedObjects,
			RemovedGitBytes:   gitResult.RemovedBytes,
			RemainingGitBytes: gitResult.RemainingBytes,
			ProtectedGit:      gitResult.ProtectedObjects,
		})
	}
	return exitUsage
}

func parseCacheCommandOptions(operation string, args []string) (cacheCommandOptions, error) {
	options := cacheCommandOptions{}
	seen := make(map[string]bool)
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if flag != "--cache-dir" && flag != "--manifest" && flag != "--manifest-sha256" {
			return options, fmt.Errorf("unknown cache option %q", flag)
		}
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			return options, fmt.Errorf("%s requires a value", flag)
		}
		if seen[flag] {
			return options, fmt.Errorf("%s may be specified only once", flag)
		}
		seen[flag] = true
		value := args[i+1]
		i++
		switch flag {
		case "--cache-dir":
			options.cacheDir = value
		case "--manifest":
			options.manifestPath = value
		case "--manifest-sha256":
			options.manifestSHA256 = value
		}
	}
	if options.cacheDir == "" {
		return options, fmt.Errorf("--cache-dir is required")
	}
	switch operation {
	case "pin":
		if options.manifestPath == "" || options.manifestSHA256 != "" {
			return options, fmt.Errorf("pin requires --manifest and does not accept --manifest-sha256")
		}
	case "unpin":
		if options.manifestSHA256 == "" || options.manifestPath != "" {
			return options, fmt.Errorf("unpin requires --manifest-sha256 and does not accept --manifest")
		}
		if len(options.manifestSHA256) != 64 || strings.ToLower(options.manifestSHA256) != options.manifestSHA256 {
			return options, fmt.Errorf("--manifest-sha256 must be lowercase SHA-256")
		}
		for _, value := range options.manifestSHA256 {
			if (value < '0' || value > '9') && (value < 'a' || value > 'f') {
				return options, fmt.Errorf("--manifest-sha256 must be lowercase SHA-256")
			}
		}
	case "gc":
		if options.manifestPath != "" || options.manifestSHA256 != "" {
			return options, fmt.Errorf("gc accepts only --cache-dir")
		}
	}
	return options, nil
}

func writeCacheReceipt(stdout, stderr io.Writer, receipt cacheReceipt) int {
	encoded, err := json.Marshal(receipt)
	if err != nil {
		fmt.Fprintln(stderr, "registry: could not encode cache receipt")
		return exitInvalid
	}
	if _, err := stdout.Write(append(encoded, '\n')); err != nil {
		fmt.Fprintln(stderr, "registry: could not write cache receipt")
		return exitInvalid
	}
	return exitOK
}
