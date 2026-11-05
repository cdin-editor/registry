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
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/cdin-editor/registry/internal/resolver"
)

// GitRunner runs Git with an argument vector (never a shell command string).
// Implementations must honor context cancellation and must not invoke a shell.
type GitRunner interface {
	Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error)
}

// CommandGit invokes the configured Git executable with context cancellation.
// Command arguments are kept separate so repository URIs and revisions cannot
// become shell syntax (REG-018/026).
type CommandGit struct {
	Binary string
}

// Run executes Git and returns combined output for actionable diagnostics.
// The caller supplies an isolated environment that disables prompts and hooks.
func (g CommandGit) Run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	binary := g.Binary
	if binary == "" {
		binary = "git"
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = mergeEnvironment(os.Environ(), env)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
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
	host := strings.ToLower(parsed.Host)
	if !containsHost(p, host) {
		return fmt.Errorf("collector: repository host %q is not allow-listed", host)
	}
	return nil
}

// Options configures source retrieval. Git and Policy are mandatory injected
// boundaries; an empty/implicit repository policy is intentionally not allowed.
type Options struct {
	Git     GitRunner
	Policy  RepositoryPolicy
	TempDir string
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
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return nil, fmt.Errorf("collector: create source parent: %w", err)
			}
			if err := ensureNoSymlinkAncestors(filepath.Dir(target)); err != nil {
				return nil, fmt.Errorf("collector: unsafe source parent: %w", err)
			}
			materialized, err := collectOne(ctx, options, hooksDir, unit.RepositoryURI, unit.Revision, unit.Subdirectory, target)
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
		return repositoryURI{}, fmt.Errorf("collector: invalid repository URI: %w", err)
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
func collectOne(ctx context.Context, options Options, hooksDir, repository, revision, subdirectory, target string) (string, error) {
	repoDir, err := os.MkdirTemp(options.TempDir, "registry-git-source-*")
	if err != nil {
		return "", fmt.Errorf("create temporary repository: %w", err)
	}
	defer os.RemoveAll(repoDir)
	env := []string{"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
	run := func(args ...string) ([]byte, error) {
		withConfig := []string{"-c", "core.hooksPath=" + hooksDir, "-c", "filter.lfs.smudge=", "-c", "filter.lfs.process=", "-c", "filter.lfs.required=false"}
		return options.Git.Run(ctx, repoDir, env, append(withConfig, args...)...)
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
	if actual != strings.ToLower(revision) {
		return "", fmt.Errorf("commit identity mismatch: requested %s, resolved %s", revision, actual)
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
	return strings.ToLower(revision), nil
}

// sourceDirectory validates the subdirectory as a clean relative POSIX path
// before translating it to the host filesystem. REG-020 forbids traversal.
func sourceDirectory(repository, subdirectory string) (string, error) {
	if subdirectory == "" {
		return repository, nil
	}
	if strings.Contains(subdirectory, "\\") || !fs.ValidPath(subdirectory) || path.Clean(subdirectory) != subdirectory || strings.HasPrefix(subdirectory, "../") {
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
		_, copyErr := io.Copy(out, in)
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

// mergeEnvironment applies Git-specific overrides without duplicate environment
// keys. Duplicate keys have platform-dependent precedence and could otherwise
// re-enable prompts or global/system configuration (REG-026).
func mergeEnvironment(base, overrides []string) []string {
	replaced := make(map[string]bool, len(overrides))
	for _, value := range overrides {
		if index := strings.IndexByte(value, '='); index > 0 {
			replaced[value[:index]] = true
		}
	}
	result := make([]string, 0, len(base)+len(overrides))
	for _, value := range base {
		index := strings.IndexByte(value, '=')
		if index > 0 && replaced[value[:index]] {
			continue
		}
		result = append(result, value)
	}
	return append(result, overrides...)
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
