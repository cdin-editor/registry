package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/collector"
	"github.com/cdin-editor/registry/internal/manifest"
	"github.com/cdin-editor/registry/internal/validation"
	"github.com/cdin-editor/registry/internal/workspace"
)

type manifestCommandOptions struct {
	inputPath            string
	outputPath           string
	expectedSHA256       string
	allowedHosts         []string
	cacheDir             string
	verifyRemoteMetadata bool
}

type manifestVerifyReceipt struct {
	Valid          bool   `json:"valid"`
	SchemaVersion  string `json:"schema_version"`
	RegistryVersion string `json:"registry_version"`
	ManifestSHA256 string `json:"manifest_sha256"`
	Components     int    `json:"components"`
	SourceUnits    int    `json:"source_units"`
}

type manifestReplayReceipt struct {
	WorkspacePath  string `json:"workspace_path"`
	ManifestPath   string `json:"manifest_path"`
	ReportPath     string `json:"report_path"`
	ManifestSHA256 string `json:"manifest_sha256"`
	ReportSHA256   string `json:"report_sha256"`
}

func runManifest(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runManifestWithDependencies(ctx, args, stdin, stdout, stderr,
		func(options catalog.HTTPFetcherOptions) (catalog.Fetcher, error) {
			return catalog.NewHTTPFetcher(options)
		},
		collector.CommandGit{},
	)
}

// runManifestWithDependencies keeps the CLI composition boundary injectable.
// Production uses the bounded HTTPS Fetcher and non-interactive CommandGit;
// tests use deterministic metadata and temporary Git repositories.
func runManifestWithDependencies(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	newFetcher catalogFetcherFactory,
	git collector.GitRunner,
) int {
	if len(args) == 0 {
		return manifestUsageError(stderr, "expected verify or replay")
	}
	operation := args[0]
	if operation != "verify" && operation != "replay" {
		return manifestUsageError(stderr, "unknown Manifest operation")
	}

	options, err := parseManifestCommandOptions(operation, args[1:])
	if err != nil {
		return manifestUsageError(stderr, "%s", err)
	}
	if ctx == nil {
		return writeManifestError(stderr, "INTERNAL_ERROR", "CLI context is required")
	}
	if err := ctx.Err(); err != nil {
		return writeManifestError(stderr, "OPERATION_CANCELED", "operation canceled before it started")
	}

	input, err := readInput(options.inputPath, stdin)
	if err != nil {
		return writeManifestError(stderr, "MANIFEST_INPUT_READ_FAILED", "cannot read Manifest input; check --input and standard input")
	}
	validator, err := validation.New()
	if err != nil {
		return writeManifestError(stderr, "INTERNAL_ERROR", "cannot initialize the embedded Registry schema validator")
	}
	if err := verifyManifestInput(input.data, options.expectedSHA256, validator); err != nil {
		switch {
		case errors.Is(err, errManifestDigestInvalid):
			return writeManifestError(stderr, "MANIFEST_DIGEST_INVALID", "expected digest must be 64 lowercase hexadecimal SHA-256 characters")
		case errors.Is(err, errManifestDigestMismatch):
			return writeManifestError(stderr, "MANIFEST_DIGEST_MISMATCH", "Manifest bytes do not match the supplied SHA-256 digest")
		default:
			return writeManifestError(stderr, "MANIFEST_INVALID", "Manifest failed canonical-byte, public-schema, or provenance verification")
		}
	}

	if operation == "verify" {
		var document manifest.Document
		if err := json.Unmarshal(input.data, &document); err != nil {
			return writeManifestError(stderr, "MANIFEST_INVALID", "verified Manifest could not be decoded")
		}
		sourceCount := 0
		for _, component := range document.Components {
			sourceCount += len(component.SourceUnits)
		}
		receipt := manifestVerifyReceipt{
			Valid:           true,
			SchemaVersion:   document.SchemaVersion,
			RegistryVersion: document.RegistryVersion,
			ManifestSHA256:  manifest.Digest(input.data),
			Components:      len(document.Components),
			SourceUnits:     sourceCount,
		}
		if err := writeJSONReceipt(stdout, receipt); err != nil {
			return writeManifestError(stderr, "CLI_OUTPUT_WRITE_FAILED", "Manifest is valid, but the verification receipt could not be written to stdout")
		}
		return exitOK
	}

	if git == nil {
		return writeManifestError(stderr, "INTERNAL_ERROR", "Git runner is not configured")
	}
	replayOptions := workspace.ReplayOptions{
		Workspace: workspace.Options{
			Destination:      options.outputPath,
			Git:              git,
			RepositoryPolicy: catalogRepositoryPolicy{hosts: options.allowedHosts},
			Validator:        validator,
		},
		VerifyRemoteMetadata: options.verifyRemoteMetadata,
	}
	if options.verifyRemoteMetadata {
		if newFetcher == nil {
			return writeManifestError(stderr, "INTERNAL_ERROR", "remote metadata fetcher is not configured")
		}
		fetcher, err := newFetcher(catalog.HTTPFetcherOptions{
			AllowedHosts: options.allowedHosts,
			CacheDir:     options.cacheDir,
		})
		if err != nil {
			return writeManifestError(stderr, "REPLAY_METADATA_CONFIG_FAILED", "cannot configure strict metadata verification; check --allow-host and --cache-dir")
		}
		replayOptions.MetadataFetcher = fetcher
	}

	prepared, err := workspace.Replay(ctx, input.data, options.expectedSHA256, replayOptions)
	if err != nil {
		code, message := classifyManifestReplayError(err, options.verifyRemoteMetadata)
		return writeManifestError(stderr, code, message)
	}
	receipt := manifestReplayReceipt{
		WorkspacePath:  prepared.WorkspacePath,
		ManifestPath:   prepared.ManifestPath,
		ReportPath:     prepared.ReportPath,
		ManifestSHA256: prepared.ManifestSHA256,
		ReportSHA256:   prepared.ReportSHA256,
	}
	if err := writeJSONReceipt(stdout, receipt); err != nil {
		return writeManifestError(stderr, "CLI_OUTPUT_WRITE_FAILED", "Workspace was published, but the replay receipt could not be written to stdout")
	}
	return exitOK
}

var (
	errManifestDigestInvalid  = errors.New("manifest CLI: invalid detached digest")
	errManifestDigestMismatch = errors.New("manifest CLI: detached digest mismatch")
)

func verifyManifestInput(data []byte, expectedSHA256 string, validator *validation.Validator) error {
	if expectedSHA256 != "" {
		if len(expectedSHA256) != 64 || strings.ToLower(expectedSHA256) != expectedSHA256 {
			return errManifestDigestInvalid
		}
		for _, character := range expectedSHA256 {
			if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
				return errManifestDigestInvalid
			}
		}
		if manifest.Digest(data) != expectedSHA256 {
			return errManifestDigestMismatch
		}
	}
	return manifest.Verify(data, expectedSHA256, validator)
}

func parseManifestCommandOptions(operation string, args []string) (manifestCommandOptions, error) {
	options := manifestCommandOptions{}
	seen := make(map[string]bool)
	for i := 0; i < len(args); i++ {
		flag := args[i]
		switch flag {
		case "--verify-remote-metadata":
			if operation != "replay" {
				return options, fmt.Errorf("--verify-remote-metadata is supported only by replay")
			}
			if seen[flag] {
				return options, fmt.Errorf("%s may be specified only once", flag)
			}
			seen[flag] = true
			options.verifyRemoteMetadata = true
			continue
		case "--input", "--sha256", "--output", "--allow-host", "--cache-dir":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return options, fmt.Errorf("%s requires a value", flag)
			}
		default:
			return options, fmt.Errorf("unknown option %q", flag)
		}

		value := args[i+1]
		i++
		switch flag {
		case "--input":
			if seen[flag] {
				return options, fmt.Errorf("%s may be specified only once", flag)
			}
			seen[flag] = true
			options.inputPath = value
		case "--sha256":
			if seen[flag] {
				return options, fmt.Errorf("%s may be specified only once", flag)
			}
			seen[flag] = true
			options.expectedSHA256 = value
		case "--output":
			if operation != "replay" {
				return options, fmt.Errorf("--output is supported only by replay")
			}
			if seen[flag] {
				return options, fmt.Errorf("%s may be specified only once", flag)
			}
			seen[flag] = true
			options.outputPath = value
		case "--allow-host":
			if operation != "replay" {
				return options, fmt.Errorf("--allow-host is supported only by replay")
			}
			for _, host := range strings.Split(value, ",") {
				host = strings.ToLower(strings.TrimSpace(host))
				if host == "" {
					return options, fmt.Errorf("--allow-host contains an empty host")
				}
				options.allowedHosts = append(options.allowedHosts, host)
			}
		case "--cache-dir":
			if operation != "replay" {
				return options, fmt.Errorf("--cache-dir is supported only by replay")
			}
			if seen[flag] {
				return options, fmt.Errorf("%s may be specified only once", flag)
			}
			seen[flag] = true
			options.cacheDir = value
		}
	}
	if options.inputPath == "" {
		return options, fmt.Errorf("--input is required")
	}
	if operation == "replay" {
		if options.outputPath == "" {
			return options, fmt.Errorf("--output is required for replay")
		}
		if len(options.allowedHosts) == 0 {
			return options, fmt.Errorf("at least one --allow-host is required for pinned Git source retrieval")
		}
		if options.cacheDir != "" && !options.verifyRemoteMetadata {
			return options, fmt.Errorf("--cache-dir requires --verify-remote-metadata")
		}
	}
	return options, nil
}

func classifyManifestReplayError(err error, verifyRemoteMetadata bool) (string, string) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "OPERATION_CANCELED", "replay was canceled; staged files and the owned destination lock were cleaned up"
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "destination already exists"):
		return "REPLAY_DESTINATION_EXISTS", "destination already exists; choose a new --output path"
	case strings.Contains(message, "destination lock") || strings.Contains(message, "another prepare operation holds"):
		return "REPLAY_DESTINATION_BUSY", "destination is locked by another Registry operation; confirm no owner is active before removing a stale lock"
	case strings.Contains(message, "collect selected sources"):
		return "REPLAY_SOURCE_FAILED", "one or more pinned Git commits could not be retrieved or verified; check --allow-host and commit availability"
	case strings.Contains(message, "rebuilt Manifest differs"):
		return "REPLAY_OUTPUT_MISMATCH", "replayed sources did not reproduce the exact input Manifest; no destination was published"
	case verifyRemoteMetadata && (strings.Contains(message, "metadata") || strings.Contains(message, "Catalog") || strings.Contains(message, "release identity") || strings.Contains(message, "source-unit provenance") || strings.Contains(message, "dependency") || strings.Contains(message, "contract declarations")):
		return "REPLAY_METADATA_FAILED", "recorded Catalog or Release Metadata could not be revalidated; check host policy and immutable metadata availability"
	default:
		return "REPLAY_FAILED", "replay failed before successful publication; check the Manifest, Git host policy, destination parent and permissions"
	}
}

func writeJSONReceipt(stdout io.Writer, receipt any) error {
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	_, err = stdout.Write(append(encoded, '\n'))
	return err
}

func writeManifestError(stderr io.Writer, code, message string) int {
	fmt.Fprintf(stderr, "registry: error[%s]: %s\n", code, message)
	return exitInvalid
}

func manifestUsageError(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "registry: error[USAGE_ERROR]: %s\n\n", fmt.Sprintf(format, args...))
	printUsage(stderr)
	return exitUsage
}
