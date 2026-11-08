package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/cdin-editor/registry/internal/artifact"
	"github.com/cdin-editor/registry/internal/buildinfo"
	"github.com/cdin-editor/registry/internal/catalog"
	"github.com/cdin-editor/registry/internal/collector"
	"github.com/cdin-editor/registry/internal/manifest"
	"github.com/cdin-editor/registry/internal/resolver"
	"github.com/cdin-editor/registry/internal/schemas"
	"github.com/cdin-editor/registry/internal/validation"
	"github.com/cdin-editor/registry/internal/workspace"
)

const (
	exitOK      = 0
	exitInvalid = 1
	exitUsage   = 2
)

type inputFile struct {
	name string
	data []byte
}

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if err := ctx.Err(); err != nil {
		if len(args) > 0 && args[0] == "manifest" {
			return writeManifestError(stderr, "OPERATION_CANCELED", "operation canceled before it started")
		}
		fmt.Fprintf(stderr, "registry: %v\n", err)
		return exitInvalid
	}
	if len(args) == 0 {
		printUsage(stderr)
		return exitUsage
	}

	switch args[0] {
	case "--version", "-V", "version":
		if len(args) != 1 {
			return usageError(stderr, "version takes no arguments")
		}
		fmt.Fprintf(stdout, "registry %s (commit %s, built %s)\n",
			buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate)
		return exitOK

	case "help", "--help", "-h":
		if len(args) > 1 {
			return usageError(stderr, "help takes no arguments")
		}
		printUsage(stdout)
		return exitOK

	case "schema":
		return runSchema(args[1:], stdout, stderr)

	case "validate":
		return runValidate(args[1:], stdin, stdout, stderr)

	case "resolve":
		return runExecution(ctx, args[1:], false, stdout, stderr)

	case "prepare":
		return runExecution(ctx, args[1:], true, stdout, stderr)

	case "manifest":
		return runManifest(ctx, args[1:], stdin, stdout, stderr)

	case "cache":
		return runCache(ctx, args[1:], stdin, stdout, stderr)

	default:
		return usageError(stderr, "unknown command %q", args[0])
	}
}

func runSchema(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "list" {
		for _, kind := range validation.Kinds() {
			fmt.Fprintln(stdout, kind)
		}
		return exitOK
	}
	if len(args) == 2 && args[0] == "show" {
		kind, err := validation.ParseKind(args[1])
		if err != nil {
			return usageError(stderr, "%v", err)
		}
		data, err := schemas.FS.ReadFile("registry/v1/" + string(kind) + ".schema.json")
		if err != nil {
			fmt.Fprintf(stderr, "registry: read embedded schema: %v\n", err)
			return exitInvalid
		}
		if _, err := stdout.Write(data); err != nil {
			fmt.Fprintf(stderr, "registry: write schema: %v\n", err)
			return exitInvalid
		}
		return exitOK
	}
	printUsage(stderr)
	return exitUsage
}

func runValidate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		printUsage(stderr)
		return exitUsage
	}
	kind, err := validation.ParseKind(args[0])
	if err != nil {
		return usageError(stderr, "%v", err)
	}
	file, err := readInput(args[1], stdin)
	if err != nil {
		fmt.Fprintf(stderr, "registry: %v\n", err)
		return exitInvalid
	}
	validator, err := validation.New()
	if err != nil {
		fmt.Fprintf(stderr, "registry: initialize schema validator: %v\n", err)
		return exitInvalid
	}
	if err := validator.Validate(kind, file.data, file.name); err != nil {
		fmt.Fprintf(stderr, "registry: %v\n", err)
		return exitInvalid
	}
	fmt.Fprintf(stdout, "OK %s schema 1.0.0: %s\n", kind, file.name)
	return exitOK
}

// executionOptions is deliberately CLI-only. The core Resolver and Collector
// receive typed data and injected boundaries; they never inspect process args.
type executionOptions struct {
	productPath  string
	catalogs     []string
	allowedHosts []string
	outputPath   string
	cacheDir     string
	offline      bool
}

// parseExecutionOptions accepts repeated --catalog and --allow-host flags.
// Explicit network hosts are required because catalog metadata URIs are
// untrusted data (REG-026); no ambient trust store or wildcard host is inferred.
func parseExecutionOptions(args []string, preparing bool) (executionOptions, error) {
	options := executionOptions{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--offline" {
			options.offline = true
			continue
		}
		if i+1 >= len(args) {
			return options, fmt.Errorf("flag %s requires a value", args[i])
		}
		value := args[i+1]
		switch args[i] {
		case "--product":
			if options.productPath != "" {
				return options, fmt.Errorf("--product may be specified only once")
			}
			options.productPath = value
		case "--catalog":
			options.catalogs = append(options.catalogs, value)
		case "--allow-host":
			for _, host := range strings.Split(value, ",") {
				if strings.TrimSpace(host) != "" {
					options.allowedHosts = append(options.allowedHosts, strings.TrimSpace(host))
				}
			}
		case "--output":
			if options.outputPath != "" {
				return options, fmt.Errorf("--output may be specified only once")
			}
			options.outputPath = value
		case "--cache-dir":
			if options.cacheDir != "" {
				return options, fmt.Errorf("--cache-dir may be specified only once")
			}
			options.cacheDir = value
		default:
			return options, fmt.Errorf("unknown option %q", args[i])
		}
		i++
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
	if preparing && options.outputPath == "" {
		return options, fmt.Errorf("--output is required for prepare")
	}
	if !preparing && options.outputPath != "" {
		return options, fmt.Errorf("--output is supported only by prepare")
	}
	if options.offline && options.cacheDir == "" {
		return options, fmt.Errorf("--offline requires --cache-dir with previously verified metadata and artifacts")
	}
	return options, nil
}

// runExecution composes public adapters in contract order: Product Definition
// validation/normalization → Catalog validation and digest verification →
// Resolver. The prepare variant then adds immutable source collection, Manifest/
// Report validation and atomic workspace publication. REG-001/005/019/022-026.
func runExecution(ctx context.Context, args []string, preparing bool, stdout, stderr io.Writer) int {
	return runExecutionWithFetcherFactory(ctx, args, preparing, stdout, stderr,
		func(options catalog.HTTPFetcherOptions) (catalog.Fetcher, error) {
			return catalog.NewHTTPFetcher(options)
		})
}

// catalogFetcherFactory is injected at the CLI composition boundary so local
// fixture catalogs can exercise the same validation → load → resolve path
// without network access. Production always supplies NewHTTPFetcher.
type catalogFetcherFactory func(catalog.HTTPFetcherOptions) (catalog.Fetcher, error)

func runExecutionWithFetcherFactory(ctx context.Context, args []string, preparing bool, stdout, stderr io.Writer, newFetcher catalogFetcherFactory) int {
	options, err := parseExecutionOptions(args, preparing)
	if err != nil {
		return usageError(stderr, "%v", err)
	}
	if err := ctx.Err(); err != nil {
		fmt.Fprintf(stderr, "registry: %v\n", err)
		return exitInvalid
	}
	validator, err := validation.New()
	if err != nil {
		fmt.Fprintf(stderr, "registry: initialize schema validator: %v\n", err)
		return exitInvalid
	}
	productFile, err := os.ReadFile(options.productPath)
	if err != nil {
		fmt.Fprintf(stderr, "registry: read Product Definition: %v\n", err)
		return exitInvalid
	}
	normalized, err := validator.Normalize(validation.ProductDefinition, productFile, options.productPath)
	if err != nil {
		fmt.Fprintf(stderr, "registry: Product Definition: %v\n", err)
		return exitInvalid
	}
	var definition resolver.ProductDefinition
	if err := json.Unmarshal(normalized, &definition); err != nil {
		fmt.Fprintf(stderr, "registry: decode normalized Product Definition: %v\n", err)
		return exitInvalid
	}
	if newFetcher == nil {
		fmt.Fprintln(stderr, "registry: Catalog fetcher factory is required")
		return exitInvalid
	}
	fetcher, err := newFetcher(catalog.HTTPFetcherOptions{
		AllowedHosts: options.allowedHosts,
		CacheDir:     options.cacheDir,
		Offline:      options.offline,
	})
	if err != nil {
		fmt.Fprintf(stderr, "registry: configure Catalog transport: %v\n", err)
		return exitInvalid
	}
	catalogs := make([]*catalog.Catalog, 0, len(options.catalogs))
	for catalogIndex, catalogInput := range options.catalogs {
		if err := ctx.Err(); err != nil {
			fmt.Fprintf(stderr, "registry: %v\n", err)
			return exitInvalid
		}
		indexBytes, indexURI, err := readCatalogInput(ctx, catalogInput, fetcher)
		if err != nil {
			fmt.Fprintf(stderr, "registry: Catalog input %d: %v\n", catalogIndex+1, err)
			return exitInvalid
		}
		loaded, err := catalog.LoadFromSource(ctx, indexBytes, indexURI, fetcher, validator)
		if err != nil {
			fmt.Fprintf(stderr, "registry: Catalog input %d: %v\n", catalogIndex+1, err)
			return exitInvalid
		}
		catalogs = append(catalogs, loaded)
	}
	result := resolver.Resolve(definition, catalogs)
	if !preparing {
		report, err := manifest.MarshalReport(result.Report)
		if err != nil {
			fmt.Fprintf(stderr, "registry: serialize Resolution Report: %v\n", err)
			return exitInvalid
		}
		if _, err := stdout.Write(append(report, '\n')); err != nil {
			fmt.Fprintf(stderr, "registry: write Resolution Report: %v\n", err)
			return exitInvalid
		}
		if result.Graph == nil || result.Report.Result != "success" {
			return exitInvalid
		}
		return exitOK
	}
	if result.Graph == nil || result.Report.Result != "success" {
		report, marshalErr := manifest.MarshalReport(result.Report)
		if marshalErr == nil {
			_, _ = stderr.Write(append(report, '\n'))
		}
		return exitInvalid
	}
	if options.offline {
		for _, component := range result.Graph.Components {
			if component.Delivery != "artifact" {
				fmt.Fprintln(stderr, "registry: error[OFFLINE_SOURCE_UNAVAILABLE]: offline prepare requires every selected component to use an immutable artifact; source delivery requires Git retrieval")
				return exitInvalid
			}
		}
	}
	var artifactFetcher artifact.DigestFetcher
	for _, component := range result.Graph.Components {
		if component.Delivery == "artifact" {
			packageFetcher, fetchErr := catalog.NewHTTPFetcher(catalog.HTTPFetcherOptions{
				AllowedHosts: options.allowedHosts,
				CacheDir:     options.cacheDir,
				MaxBytes:     artifact.DefaultMaxCompressedBytes,
				Offline:      options.offline,
			})
			if fetchErr != nil {
				fmt.Fprintf(stderr, "registry: configure artifact transport: %v\n", fetchErr)
				return exitInvalid
			}
			artifactFetcher = packageFetcher
			break
		}
	}
	prepared, err := workspace.Prepare(ctx, result, workspace.Options{
		Destination:      options.outputPath,
		RegistryVersion:  buildinfo.Version,
		Git:              collector.CommandGit{},
		RepositoryPolicy: catalogRepositoryPolicy{hosts: options.allowedHosts},
		ArtifactFetcher:  artifactFetcher,
		ArtifactLimits:   artifact.Limits{},
		Validator:        validator,
	})
	if err != nil {
		fmt.Fprintf(stderr, "registry: prepare workspace: %v\n", err)
		return exitInvalid
	}
	receipt := struct {
		WorkspacePath  string `json:"workspace_path"`
		ManifestPath   string `json:"manifest_path"`
		ReportPath     string `json:"report_path"`
		ManifestSHA256 string `json:"manifest_sha256"`
		ReportSHA256   string `json:"report_sha256"`
	}{
		WorkspacePath:  prepared.WorkspacePath,
		ManifestPath:   prepared.ManifestPath,
		ReportPath:     prepared.ReportPath,
		ManifestSHA256: prepared.ManifestSHA256,
		ReportSHA256:   prepared.ReportSHA256,
	}
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "registry: encode prepare receipt: %v\n", err)
		return exitInvalid
	}
	if _, err := stdout.Write(append(encoded, '\n')); err != nil {
		fmt.Fprintf(stderr, "registry: write prepare receipt: %v\n", err)
		return exitInvalid
	}
	return exitOK
}

// readCatalogInput accepts a local Catalog Index file or an HTTPS URI. The file
// URI recorded in the Manifest is absolute and stable for the current host;
// callers that require portable provenance should publish Catalog Indexes at an
// immutable HTTPS URI.
func readCatalogInput(ctx context.Context, input string, fetcher catalog.Fetcher) ([]byte, string, error) {
	if strings.HasPrefix(strings.ToLower(input), "https://") {
		data, err := fetcher.Fetch(ctx, input)
		if err != nil {
			return nil, "", err
		}
		return data, input, nil
	}
	if strings.HasPrefix(strings.ToLower(input), "file://") {
		parsed, err := url.Parse(input)
		if err != nil || parsed.Host != "" || parsed.Path == "" {
			return nil, "", fmt.Errorf("Catalog Index file URI must be local and absolute")
		}
		path := fileURIPathToLocalPath(parsed.Path, os.PathSeparator == '\\')
		if !filepath.IsAbs(path) {
			return nil, "", fmt.Errorf("Catalog Index file URI must contain an absolute local path")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, "", fmt.Errorf("resolve Catalog Index path: %w", err)
		}
		data, err := os.ReadFile(absolute)
		if err != nil {
			return nil, "", err
		}
		indexURI, err := localFileURI(absolute)
		if err != nil {
			return nil, "", err
		}
		return data, indexURI, nil
	}
	// Treat the input as a path by default. This deliberately avoids parsing
	// Windows drive letters (for example C:\\repo\\catalog.json) as URI schemes.
	absolute, err := filepath.Abs(input)
	if err != nil {
		return nil, "", fmt.Errorf("resolve Catalog Index path: %w", err)
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return nil, "", err
	}
	indexURI, err := localFileURI(absolute)
	if err != nil {
		return nil, "", err
	}
	return data, indexURI, nil
}

// localFileURI returns a canonical local file URI for an absolute filesystem
// path. Windows drive paths need a leading slash in the URI path (file:///C:/...);
// omitting it makes net/url interpret the drive letter as an authority/host.
func localFileURI(absolutePath string) (string, error) {
	slashPath := filepath.ToSlash(absolutePath)
	if strings.HasPrefix(slashPath, "//") {
		return "", fmt.Errorf("Catalog Index UNC paths are not supported as local file URIs")
	}
	if isWindowsDriveAbsolutePath(slashPath) {
		slashPath = "/" + slashPath
	}
	if !strings.HasPrefix(slashPath, "/") {
		return "", fmt.Errorf("Catalog Index path must be absolute")
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String(), nil
}

// fileURIPathToLocalPath restores the drive-letter form used by Windows from
// the leading slash required by a canonical file:///C:/... URI.
func fileURIPathToLocalPath(uriPath string, windows bool) string {
	if windows && strings.HasPrefix(uriPath, "/") && isWindowsDriveAbsolutePath(uriPath[1:]) {
		uriPath = uriPath[1:]
	}
	return filepath.FromSlash(uriPath)
}

func isWindowsDriveAbsolutePath(path string) bool {
	return len(path) >= 3 &&
		((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) &&
		path[1] == ':' && path[2] == '/'
}

// catalogRepositoryPolicy applies the same exact allow-list to source repository
// URIs. Git retrieval never silently trusts a host simply because it appeared in
// a release metadata document.
type catalogRepositoryPolicy struct {
	hosts []string
}

func (p catalogRepositoryPolicy) ValidateRepository(uri string) error {
	return (collector.HTTPSAllowList(p.hosts)).ValidateRepository(uri)
}

func readInput(path string, stdin io.Reader) (inputFile, error) {
	if path == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return inputFile{}, fmt.Errorf("read standard input: %w", err)
		}
		return inputFile{name: "stdin", data: data}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return inputFile{}, fmt.Errorf("read %q: %w", path, err)
	}
	return inputFile{name: path, data: data}, nil
}

func usageError(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintln(stderr, "registry: "+fmt.Sprintf(format, args...))
	fmt.Fprintln(stderr)
	printUsage(stderr)
	return exitUsage
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, strings.TrimSpace(`Registry — generic component delivery and orchestration

Usage:
  registry --version
  registry help
  registry schema list
  registry schema show <kind>
  registry validate <kind> <file|->
  registry resolve --product <file> --catalog <file|https-uri>... --allow-host <host>... [--cache-dir <dir>] [--offline]
  registry prepare --product <file> --catalog <file|https-uri>... --allow-host <host>... --output <dir> [--cache-dir <dir>] [--offline]
  registry manifest verify --input <file|-> [--sha256 <hex>]
  registry manifest replay --input <file|-> --output <dir> --allow-host <host>... [--sha256 <hex>] [--verify-remote-metadata] [--cache-dir <dir>] [--offline]
  registry manifest discover --input <file|-> --product <file> --catalog <file|https-uri>... --allow-host <host>... [--sha256 <hex>] [--cache-dir <dir>] [--report <file>]
  registry cache pin --manifest <file|-> --cache-dir <dir>
  registry cache unpin --manifest-sha256 <hex> --cache-dir <dir>
  registry cache gc --cache-dir <dir>

Document kinds:
  product-definition
  catalog-index
  component-release
  resolved-manifest
  resolution-report
  update-discovery-report

Notes:
  YAML (.yaml/.yml) is accepted only for Product Definitions.
  Standard input (-) is read as JSON.
  resolve emits a Resolution Report and never collects component source.
  prepare publishes a complete workspace only after all checks succeed.
  manifest verify checks canonical bytes, schema, provenance and an optional detached digest.
  manifest replay recollects only the recorded full Git commits; it never runs version selection.
  manifest discover compares a saved Manifest with current Catalog resolution and emits an immutable audit report.
  manifest discover never collects source or modifies the historical Manifest/Workspace.
  --report writes the canonical update-discovery report to a new file and never replaces an existing file.
  Replay makes no Catalog metadata requests by default; --verify-remote-metadata enables strict checks.
  Replay requires explicit source/metadata host policy through --allow-host.
  Network Catalog and source hosts must be explicitly allow-listed.
  --cache-dir enables integrity-checked metadata and content-addressed Git-object caches.\n  --offline forbids unpinned metadata fetches and requires verified digest-addressed entries.\n  Offline prepare/replay is artifact-only; source components require Git retrieval.
  cache pin/unpin retains or releases metadata and source commit identities from a verified Manifest.
  cache gc evicts only entries outside retained Manifest pins and active replay leases.`))
}
