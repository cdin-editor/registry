package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/manifest"
	"github.com/cdin-editor/registry/internal/resolver"
	"github.com/cdin-editor/registry/internal/validation"
)

type updateDiscoveryOptions struct {
	inputPath      string
	expectedSHA256 string
	productPath    string
	catalogs       []string
	allowedHosts   []string
	cacheDir       string
	reportPath     string
}

// runManifestDiscover evaluates current Catalog state against a persisted
// Manifest. It never collects sources, creates a Workspace, or modifies the
// historical input. The report is emitted as canonical JSON and can optionally
// be saved to a new, never-overwritten report path.
func runManifestDiscover(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	newFetcher catalogFetcherFactory,
) int {
	options, err := parseUpdateDiscoveryOptions(args)
	if err != nil {
		return manifestUsageError(stderr, "%s", err)
	}
	if ctx == nil {
		return writeManifestError(stderr, "INTERNAL_ERROR", "CLI context is required")
	}
	if err := ctx.Err(); err != nil {
		return writeManifestError(stderr, "OPERATION_CANCELED", "update discovery was canceled before it started")
	}

	input, err := readInput(options.inputPath, stdin)
	if err != nil {
		return writeManifestError(stderr, "MANIFEST_INPUT_READ_FAILED", "cannot read historical Manifest input")
	}
	validator, err := validation.New()
	if err != nil {
		return writeManifestError(stderr, "INTERNAL_ERROR", "cannot initialize the embedded Registry schema validator")
	}
	if err := verifyManifestInput(input.data, options.expectedSHA256, validator); err != nil {
		switch {
		case err == errManifestDigestInvalid:
			return writeManifestError(stderr, "MANIFEST_DIGEST_INVALID", "expected digest must be lowercase SHA-256")
		case err == errManifestDigestMismatch:
			return writeManifestError(stderr, "MANIFEST_DIGEST_MISMATCH", "historical Manifest bytes do not match the supplied digest")
		default:
			return writeManifestError(stderr, "MANIFEST_INVALID", "historical Manifest failed canonical-byte, schema or provenance verification")
		}
	}
	var historical manifest.Document
	if err := json.Unmarshal(input.data, &historical); err != nil {
		return writeManifestError(stderr, "MANIFEST_INVALID", "verified historical Manifest could not be decoded")
	}

	productFile, err := os.ReadFile(options.productPath)
	if err != nil {
		return writeManifestError(stderr, "DISCOVERY_PRODUCT_INPUT_READ_FAILED", "cannot read current Product Definition input")
	}
	normalizedProduct, err := validator.Normalize(validation.ProductDefinition, productFile, options.productPath)
	if err != nil {
		return writeManifestError(stderr, "DISCOVERY_PRODUCT_INVALID", "current Product Definition failed public-schema or normalization validation")
	}
	var definition resolver.ProductDefinition
	if err := json.Unmarshal(normalizedProduct, &definition); err != nil {
		return writeManifestError(stderr, "DISCOVERY_PRODUCT_INVALID", "current Product Definition could not be decoded")
	}
	if newFetcher == nil {
		return writeManifestError(stderr, "DISCOVERY_CATALOG_CONFIG_FAILED", "Catalog metadata fetcher is not configured")
	}
	fetcher, err := newFetcher(catalog.HTTPFetcherOptions{
		AllowedHosts: options.allowedHosts,
		CacheDir:     options.cacheDir,
	})
	if err != nil {
		return writeManifestError(stderr, "DISCOVERY_CATALOG_CONFIG_FAILED", "cannot configure current Catalog transport; check --allow-host and --cache-dir")
	}

	catalogs := make([]*catalog.Catalog, 0, len(options.catalogs))
	for _, catalogInput := range options.catalogs {
		if err := ctx.Err(); err != nil {
			return writeManifestError(stderr, "OPERATION_CANCELED", "update discovery was canceled while loading current Catalogs")
		}
		indexBytes, indexURI, err := readCatalogInput(ctx, catalogInput, fetcher)
		if err != nil {
			return writeManifestError(stderr, "DISCOVERY_CATALOG_INPUT_FAILED", "cannot read one of the supplied current Catalog Index inputs")
		}
		loaded, err := catalog.LoadFromSource(ctx, indexBytes, indexURI, fetcher, validator)
		if err != nil {
			if ctx.Err() != nil {
				return writeManifestError(stderr, "OPERATION_CANCELED", "update discovery was canceled while retrieving current release metadata")
			}
			return writeManifestError(stderr, "DISCOVERY_CATALOG_LOAD_FAILED", "a current Catalog failed digest, schema, identity or all-or-nothing loading checks")
		}
		catalogs = append(catalogs, loaded)
	}

	if err := ctx.Err(); err != nil {
		return writeManifestError(stderr, "OPERATION_CANCELED", "update discovery was canceled before resolution")
	}
	current := resolver.Resolve(definition, catalogs)
	report, err := manifest.BuildUpdateDiscoveryReport(input.data, historical, productFile, catalogs, current)
	if err != nil {
		return writeManifestError(stderr, "DISCOVERY_REPORT_FAILED", "could not construct an auditable update-discovery report")
	}
	reportBytes, err := manifest.MarshalCanonical(report)
	if err != nil {
		return writeManifestError(stderr, "DISCOVERY_REPORT_FAILED", "could not serialize the update-discovery report")
	}
	if options.reportPath != "" {
		if err := writeDiscoveryReportFile(options.reportPath, reportBytes); err != nil {
			return writeManifestError(stderr, "DISCOVERY_REPORT_FILE_FAILED", "could not create the requested report file without replacing an existing path")
		}
	}
	if _, err := stdout.Write(append(reportBytes, '\n')); err != nil {
		return writeManifestError(stderr, "DISCOVERY_OUTPUT_WRITE_FAILED", "update-discovery report was constructed but could not be written to stdout")
	}
	if current.Graph == nil {
		return writeManifestError(stderr, "DISCOVERY_RESOLUTION_FAILED", "current Product Definition could not be resolved; inspect the emitted report for conflicts and candidate reason codes")
	}
	return exitOK
}

func parseUpdateDiscoveryOptions(args []string) (updateDiscoveryOptions, error) {
	options := updateDiscoveryOptions{}
	seen := make(map[string]bool)
	for i := 0; i < len(args); i++ {
		flag := args[i]
		switch flag {
		case "--input", "--sha256", "--product", "--catalog", "--allow-host", "--cache-dir", "--report":
		default:
			return options, fmt.Errorf("unknown option %q", flag)
		}
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			return options, fmt.Errorf("%s requires a value", flag)
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
			if seen[flag] { return options, fmt.Errorf("%s may be specified only once", flag) }
			seen[flag] = true
			options.expectedSHA256 = value
		case "--product":
			if seen[flag] { return options, fmt.Errorf("%s may be specified only once", flag) }
			seen[flag] = true
			options.productPath = value
		case "--catalog":
			options.catalogs = append(options.catalogs, value)
		case "--allow-host":
			for _, host := range strings.Split(value, ",") {
				host = strings.ToLower(strings.TrimSpace(host))
				if host == "" {
					return options, fmt.Errorf("--allow-host contains an empty host")
				}
				options.allowedHosts = append(options.allowedHosts, host)
			}
		case "--cache-dir":
			if seen[flag] { return options, fmt.Errorf("%s may be specified only once", flag) }
			seen[flag] = true
			options.cacheDir = value
		case "--report":
			if seen[flag] { return options, fmt.Errorf("%s may be specified only once", flag) }
			seen[flag] = true
			options.reportPath = value
		}
	}
	if options.inputPath == "" {
		return options, fmt.Errorf("--input is required")
	}
	if options.productPath == "" {
		return options, fmt.Errorf("--product is required")
	}
	if len(options.catalogs) == 0 {
		return options, fmt.Errorf("at least one --catalog is required")
	}
	if len(options.allowedHosts) == 0 {
		return options, fmt.Errorf("at least one --allow-host is required")
	}
	if options.reportPath == "-" {
		return options, fmt.Errorf("--report requires a file path; stdout already receives the report")
	}
	return options, nil
}

// writeDiscoveryReportFile creates a new canonical report file and refuses to
// replace any existing file or symlink. Failed writes remove only the file
// created by this invocation.
func writeDiscoveryReportFile(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("CLI: report path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return err
	}
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		return fmt.Errorf("CLI: report parent is not a directory")
	}
	absolute = filepath.Join(parent, filepath.Base(absolute))
	file, err := os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	created := true
	defer func() {
		if created {
			_ = os.Remove(absolute)
		}
	}()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	created = false
	return nil
}
