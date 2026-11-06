// Package collector materializes only the immutable source units selected by
// the Registry Resolver. It does not resolve versions, inspect product identity,
// or execute component build logic.
//
// Spec traceability:
//   - REG-017: each selected release may declare one or more source units.
//   - REG-018: retrieve and verify the exact full Git commit object ID.
//   - REG-020: source subdirectories and destination paths must remain confined.
//   - REG-026: collected source is untrusted data; scripts/hooks are never run.
//   - Architecture: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry.md#5-source-collection-and-handoff
//   - Contracts: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#6-source-collection-and-workspace
//   - Local design: https://github.com/cdin-editor/registry/blob/main/docs/implementation/collector.md
//
// The Git runner and repository policy are injected. This keeps credentials,
// network policy and process execution outside the resolver and makes failure
// paths testable without granting metadata parsing arbitrary I/O authority.
package collector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cdin-editor/registry/internal/resolver"
)

// GitRunner runs Git with an argument vector (never a shell command string).
// Implementations must honor context cancellation and must not invoke a shell.
type GitRunner interface {
	Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error)
}

// GitCredentialProvider is an explicit, non-interactive credential boundary.
// It may return GIT_ASKPASS plus REGISTRY_GIT_CREDENTIAL_* variables only.
// The provider must not return tokens in Git arguments, URLs or diagnostics.
type GitCredentialProvider interface {
	GitEnvironment(ctx context.Context, repositoryURI string) ([]string, error)
}

// CommandGit invokes the configured Git executable with context cancellation.
// Command arguments are kept separate so repository URIs and revisions cannot
// become shell syntax (REG-018/026).
type CommandGit struct {
	Binary string
}

// Run executes Git with a deliberately small environment. Ambient credential
// helpers, askpass programs, SSH agents, proxy settings and Git config variables
// are not inherited. Private-repository authentication is not supported.
func (g CommandGit) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("collector: Git context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binary := g.Binary
	if binary == "" {
		binary = "git"
	}
	processEnv, err := gitProcessEnvironment(os.Environ(), env)
	if err != nil {
		return nil, fmt.Errorf("collector: invalid explicit credential environment")
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	configureGitProcess(cmd)
	cmd.Env = processEnv
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return nil, fmt.Errorf("collector: Git command failed with exit code %d", exitError.ExitCode())
		}
		return nil, fmt.Errorf("collector: Git command could not be started")
	}
	return output, nil
}

// RepositoryPolicy validates repository URIs before Git receives them.
// Production composition should require HTTPS plus an explicit host allow-list.
// Tests may inject a policy that accepts a temporary local Git repository.
type RepositoryPolicy interface {
	ValidateRepository(uri string) error
}

// HTTPSAllowList permits HTTPS repositories only when their hostname appears
// in the explicit allow-list. It prevents arbitrary hosts from being fetched;
// deployment network egress must also block private/link-local IP ranges and DNS
// rebinding because a hostname allow-list is not an egress firewall.
type HTTPSAllowList []string

// ValidateRepository enforces HTTPS, rejects embedded credentials and requires
// an exact hostname allow-list match. DNS/IP egress policy remains the
// responsibility of the deployment's network boundary.
func (p HTTPSAllowList) ValidateRepository(uri string) error {
	parsed, err := parseRepositoryURI(uri)
	if err != nil {
		return err
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("collector: repository URI must use https")
	}
	if parsed.User != nil {
		return fmt.Errorf("collector: repository URI must not contain embedded credentials")
	}
	// parseRepositoryURI stores url.URL.Hostname(), excluding an explicit
	// default :443 port so equivalent HTTPS spellings share one allow-list key.
	host := strings.ToLower(parsed.Host)
	if !containsHost(p, host) {
		return fmt.Errorf("collector: repository host %q is not allow-listed", host)
	}
	return nil
}

// Options configures source retrieval. Git and Policy are mandatory injected
// boundaries; credentials are optional and are never discovered from ambient
// environment, URLs, home-directory config or credential helpers.
type Options struct {
	Git         GitRunner
	Policy      RepositoryPolicy
	Credentials GitCredentialProvider
	GitCache    *GitObjectCache
	TempDir     string
}

// Source records a successfully materialized source unit and its exact commit.
type Source struct {
	ComponentID   string
	Variant       string
	SourceUnitID  string
	Revision      string
	WorkspacePath string
}

// Collect downloads only source units declared by a successfully resolved graph
// and materializes them at the canonical workspace-relative layout. It writes
// into the supplied staging directory; the workspace package owns publication.
// See REG-017/018/020/026 and the local collector design note.
func Collect(ctx context.Context, graph *resolver.Graph, destination string, options Options) ([]Source, error) {
	if ctx == nil {
		return nil, fmt.Errorf("collector: context is required")
	}
	if graph == nil {
		return nil, fmt.Errorf("collector: resolved graph is required")
	}
	if options.Git == nil {
		return nil, fmt.Errorf("collector: GitRunner is required")
	}
	if options.Policy == nil {
		return nil, fmt.Errorf("collector: repository policy is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if destination == "" {
		return nil, fmt.Errorf("collector: destination is required")
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return nil, fmt.Errorf("collector: create destination: %w", err)
	}
	root, err := filepath.Abs(destination)
	if err != nil {
		return nil, fmt.Errorf("collector: resolve destination: %w", err)
	}
	// Canonicalize platform aliases such as macOS /tmp -> /private/tmp so
	// containment checks apply to the real destination path.
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("collector: canonicalize destination: %w", err)
	}
	if err := ensureNoSymlinkAncestors(root); err != nil {
		return nil, fmt.Errorf("collector: unsafe destination root: %w", err)
	}
	hooksDir, err := os.MkdirTemp(options.TempDir, "registry-disabled-hooks-*")
	if err != nil {
		return nil, fmt.Errorf("collector: create disabled hook directory: %w", err)
	}
	defer os.RemoveAll(hooksDir)

	sources := make([]Source, 0)
	seen := make(map[string]bool)
	createdTargets := make([]string, 0)
	collectionComplete := false
	defer func() {
		if collectionComplete {
			return
		}
		for _, target := range createdTargets {
			_ = os.RemoveAll(target)
			removeEmptyParents(root, target)
		}
	}()
	for _, component := range graph.Components {
		if !validID(component.ComponentID, 128) || !validID(component.Variant, 64) {
			return nil, fmt.Errorf("collector: invalid component identity %q/%q", component.ComponentID, component.Variant)
		}
		for _, unit := range component.SourceUnits {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !validID(unit.ID, 64) {
				return nil, fmt.Errorf("collector: invalid source_unit_id %q", unit.ID)
			}
			if !isFullObjectID(unit.Revision) {
				return nil, fmt.Errorf("collector: source %s/%s has a non-full Git object ID", component.ComponentID, unit.ID)
			}
			if err := options.Policy.ValidateRepository(unit.RepositoryURI); err != nil {
				return nil, fmt.Errorf("collector: source %s/%s repository rejected: %w", component.ComponentID, unit.ID, err)
			}
			var credentialEnv []string
			if options.GitCache == nil {
				credentialEnv, err = credentialEnvironmentFor(ctx, options.Credentials, unit.RepositoryURI)
				if err != nil {
					return nil, err
				}
			}
			relative := filepath.Join("components", component.ComponentID, component.Variant, "sources", unit.ID)
			key := filepath.ToSlash(relative)
			if seen[key] {
				return nil, fmt.Errorf("collector: duplicate workspace source path %q", key)
			}
			seen[key] = true
			target := filepath.Join(root, relative)
			if err := ensureContained(root, target); err != nil {
				return nil, err
			}
			if _, err := os.Lstat(target); err == nil {
				return nil, fmt.Errorf("collector: source destination already exists: %s", key)
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("collector: inspect source destination: %w", err)
			}
			createdTargets = append(createdTargets, target)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return nil, fmt.Errorf("collector: create source parent: %w", err)
			}
			if err := ensureNoSymlinkAncestors(filepath.Dir(target)); err != nil {
				return nil, fmt.Errorf("collector: unsafe source parent: %w", err)
			}
			materialized, err := collectOne(ctx, options, hooksDir, credentialEnv, unit.RepositoryURI, unit.Revision, unit.Subdirectory, target)
			if err != nil {
				return nil, fmt.Errorf("collector: %s/%s: %w", component.ComponentID, unit.ID, err)
			}
			sources = append(sources, Source{
				ComponentID:   component.ComponentID,
				Variant:       component.Variant,
				SourceUnitID:  unit.ID,
				Revision:      materialized,
				WorkspacePath: key,
			})
		}
	}
	collectionComplete = true
	return sources, nil
}

type repositoryURI struct {
	Scheme string
	Host   string
	User   *string
}

// parseRepositoryURI uses net/url without accepting SCP-like Git syntax. The
// contract says repository_uri is a URI; accepting an alternate SSH shorthand
// would create a second parser with different security behavior.
func parseRepositoryURI(value string) (repositoryURI, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return repositoryURI{}, fmt.Errorf("collector: invalid repository URI")
	}
	if parsed.Host == "" || parsed.Hostname() == "" || parsed.Opaque != "" {
		return repositoryURI{}, fmt.Errorf("collector: repository URI must be an absolute hierarchical URI")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return repositoryURI{}, fmt.Errorf("collector: repository URI must not contain query or fragment components")
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return repositoryURI{}, fmt.Errorf("collector: HTTPS repository URI must use the default port")
	}
	var user *string
	if parsed.User != nil {
		secret := parsed.User.String()
		user = &secret
	}
	return repositoryURI{Scheme: strings.ToLower(parsed.Scheme), Host: parsed.Hostname(), User: user}, nil
}

// collectOne performs a no-checkout fetch of the exact object, verifies it is
// a commit with the requested full object ID, then checks it out detached.
// Hook paths and prompting are disabled for every Git invocation (REG-018/026).
func collectOne(ctx context.Context, options Options, hooksDir string, credentialEnv []string, repository, revision, subdirectory, target string) (string, error) {
	if options.GitCache != nil {
		return collectOneWithGitCache(ctx, options, hooksDir, repository, revision, subdirectory, target)
	}
	targetComplete := false
	defer func() {
		if !targetComplete {
			_ = os.RemoveAll(target)
		}
	}()
	repoDir, err := os.MkdirTemp(options.TempDir, "registry-git-source-*")
	if err != nil {
		return "", fmt.Errorf("create temporary repository: %w", err)
	}
	defer os.RemoveAll(repoDir)
	run := func(args ...string) ([]byte, error) {
		withConfig := []string{"-c", "core.hooksPath=" + hooksDir, "-c", "filter.lfs.smudge=", "-c", "filter.lfs.process=", "-c", "filter.lfs.required=false"}
		output, runErr := options.Git.Run(ctx, repoDir, credentialEnv, append(withConfig, args...)...)
		if runErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// GitRunner is an injected trust boundary. Its errors may contain
			// remote stderr, repository URIs or credential values; do not wrap
			// arbitrary text into Collector diagnostics (REG-026).
			return nil, errors.New("collector: Git command failed")
		}
		return output, nil
	}
	initArgs := []string{"init", "--quiet"}
	if len(revision) == 64 {
		initArgs = append(initArgs, "--object-format=sha256")
	}
	if _, err := run(initArgs...); err != nil {
		return "", fmt.Errorf("initialize temporary repository: %w", err)
	}
	if _, err := run("remote", "add", "origin", repository); err != nil {
		return "", fmt.Errorf("add origin: %w", err)
	}
	if _, err := run("fetch", "--no-tags", "--no-recurse-submodules", "--depth=1", "origin", revision); err != nil {
		return "", fmt.Errorf("fetch declared commit: %w", err)
	}
	object, err := run("rev-parse", "--verify", revision+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("declared object is not a commit: %w", err)
	}
	actual := strings.ToLower(strings.TrimSpace(string(object)))
	if !isFullObjectID(actual) || actual != strings.ToLower(revision) {
		// The runner's stdout is an untrusted boundary too. Do not echo it in
		// identity diagnostics, even when a custom runner returns arbitrary text.
		return "", fmt.Errorf("collector: declared commit identity mismatch")
	}
	if _, err := run("checkout", "--quiet", "--detach", "--no-recurse-submodules", revision); err != nil {
		return "", fmt.Errorf("checkout immutable commit: %w", err)
	}
	head, err := run("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", fmt.Errorf("verify checked-out commit: %w", err)
	}
	if strings.ToLower(strings.TrimSpace(string(head))) != strings.ToLower(revision) {
		return "", fmt.Errorf("checked-out commit differs from requested revision")
	}
	sourceRoot, err := sourceDirectory(repoDir, subdirectory)
	if err != nil {
		return "", err
	}
	if err := copyTree(ctx, sourceRoot, target); err != nil {
		return "", fmt.Errorf("copy checked-out source: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	targetComplete = true
	return strings.ToLower(revision), nil
}


func collectOneWithGitCache(ctx context.Context, options Options, hooksDir string, repository, revision, subdirectory, target string) (string, error) {
	revision = strings.ToLower(revision)
	targetComplete := false
	defer func() {
		if !targetComplete {
			_ = os.RemoveAll(target)
		}
	}()
	checkoutDir, err := os.MkdirTemp(options.TempDir, "registry-git-checkout-*")
	if err != nil {
		return "", fmt.Errorf("collector: create cached checkout directory: %w", err)
	}
	defer os.RemoveAll(checkoutDir)

	withConfig := []string{
		"-c", "core.hooksPath=" + hooksDir,
		"-c", "filter.lfs.smudge=",
		"-c", "filter.lfs.process=",
		"-c", "filter.lfs.required=false",
	}
	runCached := func(gitDir string, args ...string) ([]byte, error) {
		command := append([]string{}, withConfig...)
		command = append(command, "--git-dir="+gitDir)
		command = append(command, args...)
		output, runErr := options.Git.Run(ctx, gitDir, nil, command...)
		if runErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("collector: Git-object cache operation failed")
		}
		return output, nil
	}
	verify := func(gitDir string) error {
		kind, err := runCached(gitDir, "cat-file", "-t", revision)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(kind)) != "commit" {
			return fmt.Errorf("collector: cached Git object is not a commit")
		}
		object, err := runCached(gitDir, "cat-file", "commit", revision)
		if err != nil {
			return err
		}
		if gitObjectHash(revision, object) != revision {
			return fmt.Errorf("collector: cached commit object ID mismatch")
		}
		if _, err := runCached(gitDir, "fsck", "--full", "--strict", "--no-reflogs", revision); err != nil {
			return fmt.Errorf("collector: cached Git object graph failed integrity verification")
		}
		return nil
	}
	populate := func(staging string) error {
		command := append([]string{}, withConfig...)
		initArgs := []string{"init", "--bare", "--quiet"}
		if len(revision) == 64 {
			initArgs = append(initArgs, "--object-format=sha256")
		}
		initArgs = append(initArgs, ".")
		command = append(command, initArgs...)
		if _, err := options.Git.Run(ctx, staging, credentialEnv, command...); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("collector: initialize Git-object cache staging failed")
		}
		credentialEnv, err := credentialEnvironmentFor(ctx, options.Credentials, repository)
		if err != nil {
			return err
		}
		command = append([]string{}, withConfig...)
		command = append(command, "fetch", "--no-tags", "--no-recurse-submodules", "--depth=1", repository, revision)
		if _, err := options.Git.Run(ctx, staging, credentialEnv, command...); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("collector: fetch declared Git commit into object cache failed")
		}
		if err := os.Remove(filepath.Join(staging, "FETCH_HEAD")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("collector: remove temporary Git fetch metadata: %w", err)
		}
		return nil
	}
	use := func(gitDir string) error {
		command := append([]string{}, withConfig...)
		command = append(command, "-c", "core.bare=false", "--git-dir="+gitDir, "--work-tree="+checkoutDir,
			"checkout", "--quiet", "--detach", "--no-recurse-submodules", revision)
		if _, err := options.Git.Run(ctx, checkoutDir, nil, command...); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("collector: checkout verified Git-object cache entry failed")
		}
		head, err := runCached(gitDir, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil || strings.ToLower(strings.TrimSpace(string(head))) != revision {
			return fmt.Errorf("collector: cached checkout commit identity mismatch")
		}
		sourceRoot, err := sourceDirectory(checkoutDir, subdirectory)
		if err != nil {
			return err
		}
		if err := copyTree(ctx, sourceRoot, target); err != nil {
			return fmt.Errorf("collector: copy cached source tree: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		targetComplete = true
		return nil
	}
	if _, err := options.GitCache.WithObject(ctx, revision, verify, populate, use); err != nil {
		return "", err
	}
	return strings.ToLower(revision), nil
}

// sourceDirectory validates the subdirectory as a clean relative POSIX path
// before translating it to the host filesystem. REG-020 forbids traversal.
func sourceDirectory(repository, subdirectory string) (string, error) {
	if subdirectory == "" {
		return repository, nil
	}
	drivePrefix := len(subdirectory) >= 2 &&
		((subdirectory[0] >= 'a' && subdirectory[0] <= 'z') || (subdirectory[0] >= 'A' && subdirectory[0] <= 'Z')) &&
		subdirectory[1] == ':'
	if drivePrefix || strings.Contains(subdirectory, "\\") || !fs.ValidPath(subdirectory) || path.Clean(subdirectory) != subdirectory || strings.HasPrefix(subdirectory, "../") {
		return "", fmt.Errorf("collector: unsafe source subdirectory %q", subdirectory)
	}
	candidate := repository
	parts := strings.Split(subdirectory, "/")
	for index, part := range parts {
		candidate = filepath.Join(candidate, part)
		if err := ensureContained(repository, candidate); err != nil {
			return "", err
		}
		info, err := os.Lstat(candidate)
		if err != nil {
			return "", fmt.Errorf("collector: source subdirectory %q does not exist: %w", subdirectory, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("collector: source subdirectory traverses symlink %q", part)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", fmt.Errorf("collector: source subdirectory component %q is not a directory", part)
		}
		if index == len(parts)-1 && !info.IsDir() {
			return "", fmt.Errorf("collector: source subdirectory must be a real directory")
		}
	}
	return candidate, nil
}

// copyTree copies regular files/directories only and refuses symlinks and special
// files. Rejecting all symlinks is conservative but makes the REG-020 root
// containment guarantee independent of platform-specific link resolution.
func copyTree(ctx context.Context, source, destination string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if relative == ".git" || strings.HasPrefix(relative, ".git"+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("source tree contains symlink %q; symlinks are not materialized", relative)
		}
		target := filepath.Join(destination, relative)
		if err := ensureContained(destination, target); err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("source tree contains unsupported special file %q", relative)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(current)
		if err != nil {
			return err
		}
		mode := info.Mode().Perm() & 0o755
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := copyWithContext(ctx, out, in)
		inputCloseErr := in.Close()
		outputCloseErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
		return outputCloseErr
	})
}

// copyWithContext bounds cancellation latency while copying large regular
// files. Checking only between directory entries can leave one large file copy
// running long after the caller cancels (REG-018/026).
func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	if ctx == nil {
		return 0, fmt.Errorf("collector: copy context is required")
	}
	buffer := make([]byte, 32*1024)
	var total int64
	emptyReads := 0
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		count, readErr := source.Read(buffer)
		if err := ctx.Err(); err != nil {
			return total, err
		}
		if count > 0 {
			written, writeErr := destination.Write(buffer[:count])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != count {
				return total, io.ErrShortWrite
			}
			emptyReads = 0
		} else if readErr == nil {
			emptyReads++
			if emptyReads >= 100 {
				return total, io.ErrNoProgress
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, readErr
		}
	}
}

// removeEmptyParents removes only newly-created empty source layout directories.
// It stops at the Collector-owned destination root and never removes non-empty
// directories that may contain unrelated caller data.
func removeEmptyParents(root, target string) {
	root = filepath.Clean(root)
	current := filepath.Dir(target)
	for current != root && current != filepath.Dir(current) {
		if err := os.Remove(current); err != nil {
			return
		}
		current = filepath.Dir(current)
	}
}

// ensureNoSymlinkAncestors checks every component from the filesystem root to
// the target. This also catches a symlink in an intermediate subdirectory,
// which a final-component Lstat alone would follow (REG-020).
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
			return fmt.Errorf("collector: path contains symlink ancestor %q", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

// credentialEnvironmentFor asks the optional provider only after repository URI
// policy has accepted the target. Provider failures are deliberately redacted.
func credentialEnvironmentFor(ctx context.Context, provider GitCredentialProvider, repositoryURI string) ([]string, error) {
	if provider == nil {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	provided, err := provider.GitEnvironment(ctx, repositoryURI)
	if err != nil {
		return nil, fmt.Errorf("collector: explicit Git credential provider failed")
	}
	validated, err := validateCredentialEnvironment(provided)
	if err != nil {
		return nil, fmt.Errorf("collector: explicit Git credential provider returned unsupported settings")
	}
	return validated, nil
}

// validateCredentialEnvironment permits only an explicit askpass executable,
// forced non-interactive askpass mode, and provider-private credential values.
// Arbitrary Git configuration, proxy, PATH, SSH and terminal controls are never
// accepted through this boundary.
func validateCredentialEnvironment(input []string) ([]string, error) {
	values := make(map[string]string, len(input))
	hasAskPass := false
	hasAskPassRequire := false
	for _, item := range input {
		index := strings.IndexByte(item, '=')
		if index <= 0 {
			return nil, errors.New("invalid credential environment")
		}
		key := strings.ToUpper(item[:index])
		value := item[index+1:]
		if len(key) > 128 || len(value) > 16*1024 || strings.ContainsAny(key+value, "\x00\r\n") {
			return nil, errors.New("invalid credential environment")
		}
		if _, exists := values[key]; exists {
			return nil, errors.New("duplicate credential environment key")
		}
		switch {
		case key == "GIT_ASKPASS":
			if value == "" || !filepath.IsAbs(value) {
				return nil, errors.New("askpass must be an absolute executable path")
			}
			hasAskPass = true
		case key == "GIT_ASKPASS_REQUIRE":
			if value != "force" {
				return nil, errors.New("askpass must be forced")
			}
			hasAskPassRequire = true
		case strings.HasPrefix(key, "REGISTRY_GIT_CREDENTIAL_"):
			suffix := strings.TrimPrefix(key, "REGISTRY_GIT_CREDENTIAL_")
			if suffix == "" {
				return nil, errors.New("credential environment key has no name")
			}
			for _, char := range suffix {
				if !(char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_') {
					return nil, errors.New("invalid credential environment key")
				}
			}
		default:
			return nil, errors.New("unsupported credential environment key")
		}
		values[key] = value
	}
	if hasAskPass != hasAskPassRequire {
		return nil, errors.New("askpass and forced askpass mode must be supplied together")
	}
	if len(values) > 0 && !hasAskPass {
		return nil, errors.New("credential values require a forced askpass helper")
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result, nil
}

// gitProcessEnvironment combines a minimal OS environment with only the
// validated credentials supplied for this exact repository operation.
func gitProcessEnvironment(base, injected []string) ([]string, error) {
	values := make(map[string]string)
	for _, item := range isolatedGitEnvironment(base) {
		index := strings.IndexByte(item, '=')
		if index > 0 {
			values[item[:index]] = item[index+1:]
		}
	}
	validated, err := validateCredentialEnvironment(injected)
	if err != nil {
		return nil, err
	}
	for _, item := range validated {
		index := strings.IndexByte(item, '=')
		values[item[:index]] = item[index+1:]
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result, nil
}

// isolatedGitEnvironment retains only operating-system variables needed to find
// and start Git. In particular it excludes HOME/USERPROFILE, all ambient GIT_*
// configuration, askpass helpers, SSH agent state, credential helpers and proxy
// variables. The explicit controls below force non-interactive, config-isolated
// public retrieval (REG-026). An optional injected askpass provider can add only
// the narrowly allow-listed credential environment described above.
func isolatedGitEnvironment(base []string) []string {
	allowed := map[string]bool{
		"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "WINDIR": true,
		"SYSTEMDRIVE": true, "COMSPEC": true, "TMP": true, "TEMP": true,
		"TMPDIR": true, "LANG": true, "LC_ALL": true,
	}
	values := make(map[string]string)
	for _, item := range base {
		index := strings.IndexByte(item, '=')
		if index <= 0 {
			continue
		}
		key := strings.ToUpper(item[:index])
		if allowed[key] {
			values[key] = item[index+1:]
		}
	}
	values["GIT_TERMINAL_PROMPT"] = "0"
	values["GIT_CONFIG_NOSYSTEM"] = "1"
	values["GIT_CONFIG_GLOBAL"] = os.DevNull
	values["GIT_ASKPASS"] = ""
	values["SSH_ASKPASS"] = ""
	values["GCM_INTERACTIVE"] = "Never"

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

// ensureContained compares absolute, cleaned paths and requires target to be a
// strict descendant of root. It does not follow symlinks; callers reject them.
func ensureContained(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return err
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("collector: path %q escapes or equals root %q", target, root)
	}
	return nil
}

func isFullObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

func validID(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	first := value[0]
	if !((first >= 'a' && first <= 'z') || (first >= '0' && first <= '9')) {
		return false
	}
	lastSeparator := false
	for _, r := range value {
		alnum := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		separator := r == '.' || r == '-'
		if !alnum && !separator || separator && lastSeparator {
			return false
		}
		lastSeparator = separator
	}
	return !lastSeparator
}

func containsHost(hosts []string, target string) bool {
	for _, host := range hosts {
		if strings.EqualFold(strings.TrimSpace(host), target) {
			return true
		}
	}
	return false
}
