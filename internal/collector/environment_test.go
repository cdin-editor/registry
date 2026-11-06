package collector

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestREG026GitEnvironmentExcludesAmbientCredentialState verifies Git receives
// only the minimal OS environment and explicit non-interactive policy controls.
func TestREG026GitEnvironmentExcludesAmbientCredentialState(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"PATHEXT=.EXE",
		"SYSTEMROOT=C:\\Windows",
		"HOME=/home/user",
		"USERPROFILE=C:\\Users\\user",
		"GIT_ASKPASS=/tmp/askpass-secret",
		"SSH_ASKPASS=/tmp/ssh-askpass-secret",
		"SSH_AUTH_SOCK=/tmp/agent-secret",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=store",
		"GIT_TERMINAL_PROMPT=1",
		"GIT_CONFIG_NOSYSTEM=0",
		"GIT_CONFIG_GLOBAL=/home/user/.gitconfig",
		"HTTPS_PROXY=https://user:proxy-secret@proxy.example.invalid",
		"HTTP_PROXY=http://proxy.example.invalid",
		"GIT_TRACE=1",
	}
	got := strings.Join(isolatedGitEnvironment(base), "\n")
	for _, expected := range []string{
		"PATH=/usr/bin",
		"PATHEXT=.EXE",
		"SYSTEMROOT=C:\\Windows",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"GCM_INTERACTIVE=Never",
	} {
		if !strings.Contains(got, expected) {
			t.Errorf("isolated environment omitted %q: %s", expected, got)
		}
	}
	for _, forbidden := range []string{
		"HOME=", "USERPROFILE=", "SSH_AUTH_SOCK", "GIT_CONFIG_COUNT",
		"GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "HTTPS_PROXY", "HTTP_PROXY",
		"GIT_TRACE", "askpass-secret", "proxy-secret",
	} {
		if strings.Contains(got, forbidden) {
			t.Errorf("isolated environment retained forbidden value %q: %s", forbidden, got)
		}
	}
}

// TestREG026GitCommandFailureDoesNotEchoArgumentsOrRemoteOutput ensures a
// failed Git process cannot expose untrusted arguments or stderr in diagnostics.
func TestREG026GitCommandFailureDoesNotEchoArgumentsOrRemoteOutput(t *testing.T) {
	const marker = "registry-secret-marker-9f83"
	_, err := (CommandGit{}).Run(context.Background(), t.TempDir(), []string{"GIT_ASKPASS=" + marker},
		"not-a-real-registry-subcommand-"+marker)
	if err == nil {
		t.Fatal("expected the deliberately invalid Git command to fail")
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("Git failure diagnostic leaked command/environment material: %v", err)
	}
}

func TestGitProcessEnvironmentAcceptsOnlyExplicitCredentialBoundary(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"HOME=/home/ambient-user",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=store",
		"HTTPS_PROXY=https://proxy.invalid",
	}
	askpass := filepath.Join(t.TempDir(), "askpass")
	injected := []string{
		"GIT_ASKPASS=" + askpass,
		"GIT_ASKPASS_REQUIRE=force",
		"REGISTRY_GIT_CREDENTIAL_TOKEN=opaque-secret",
	}
	got, err := gitProcessEnvironment(base, injected)
	if err != nil {
		t.Fatalf("valid explicit credential environment rejected: %v", err)
	}
	joined := strings.Join(got, "\n")
	expectedValues := []string{
		"PATH=/usr/bin", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_ASKPASS=" + askpass, "GIT_ASKPASS_REQUIRE=force",
		"REGISTRY_GIT_CREDENTIAL_TOKEN=opaque-secret", "GIT_CONFIG_GLOBAL=" + os.DevNull,
	}
	for _, expected := range expectedValues {
		if !strings.Contains(joined, expected) {
			t.Errorf("process environment omitted %q: %s", expected, joined)
		}
	}
	for _, forbidden := range []string{"HOME=", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "HTTPS_PROXY"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("ambient setting %q leaked into process environment: %s", forbidden, joined)
		}
	}
}

func TestGitProcessEnvironmentRejectsUnscopedInjectedValues(t *testing.T) {
	askpass := filepath.Join(t.TempDir(), "askpass")
	cases := []struct {
		name string
		env  []string
	}{
		{name: "ambient git configuration", env: []string{"GIT_CONFIG_COUNT=1"}},
		{name: "ssh askpass", env: []string{"SSH_ASKPASS=/tmp/askpass"}},
		{name: "proxy", env: []string{"HTTPS_PROXY=https://proxy.invalid"}},
		{name: "path override", env: []string{"PATH=/tmp/evil"}},
		{name: "relative askpass", env: []string{"GIT_ASKPASS=askpass", "GIT_ASKPASS_REQUIRE=force"}},
		{name: "missing forced askpass", env: []string{"GIT_ASKPASS=" + askpass}},
		{name: "duplicate key", env: []string{"GIT_ASKPASS=" + askpass, "GIT_ASKPASS=" + askpass, "GIT_ASKPASS_REQUIRE=force"}},
		{name: "secret without helper", env: []string{"REGISTRY_GIT_CREDENTIAL_TOKEN=secret"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := gitProcessEnvironment([]string{"PATH=/usr/bin"}, testCase.env); err == nil {
				t.Fatal("unscoped credential environment unexpectedly accepted")
			}
		})
	}
}

func TestCommandGitRedactsChildStderrAndCredentialValues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper executable is Unix-specific")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "fake-git")
	script := "#!/bin/sh\nprintf '%s\\n' \"$REGISTRY_GIT_CREDENTIAL_TOKEN\" \"$@\" >&2\nexit 19\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	const marker = "credential-stderr-secret-7a93"
	_, err := (CommandGit{Binary: binary}).Run(context.Background(), root, []string{
		"GIT_ASKPASS=" + binary,
		"GIT_ASKPASS_REQUIRE=force",
		"REGISTRY_GIT_CREDENTIAL_TOKEN=" + marker,
	}, "argument-marker")
	if err == nil || !strings.Contains(err.Error(), "exit code 19") {
		t.Fatalf("expected safe exit classification, got %v", err)
	}
	if strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), "argument-marker") {
		t.Fatalf("child stderr/arguments leaked into Git diagnostic: %v", err)
	}
}

func TestCommandGitCancellationTerminatesChildProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep child fixture is Unix-specific")
	}
	binary, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("sleep executable unavailable: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	directory := t.TempDir()
	finished := make(chan error, 1)
	go func() {
		_, runErr := (CommandGit{Binary: binary}).Run(ctx, directory, nil, "30")
		finished <- runErr
	}()
	select {
	case runErr := <-finished:
		t.Fatalf("child process exited before cancellation: %v", runErr)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case runErr := <-finished:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("CommandGit error = %v, want context.Canceled", runErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CommandGit did not terminate the child process after cancellation")
	}
}

// blockingCopyReader pauses on its second read so the test can cancel between
// chunks and prove copyWithContext checks the context before writing more bytes.
type blockingCopyReader struct {
	started chan struct{}
	release chan struct{}
	reads   int
}

func (r *blockingCopyReader) Read(buffer []byte) (int, error) {
	r.reads++
	if r.reads == 1 {
		return copy(buffer, "first"), nil
	}
	if r.reads == 2 {
		close(r.started)
		<-r.release
		return copy(buffer, "second"), nil
	}
	return 0, errors.New("unexpected extra read")
}

func TestCopyWithContextStopsBetweenChunks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &blockingCopyReader{started: make(chan struct{}), release: make(chan struct{})}
	var output strings.Builder
	finished := make(chan error, 1)
	go func() {
		_, err := copyWithContext(ctx, &output, reader)
		finished <- err
	}()
	<-reader.started
	cancel()
	close(reader.release)
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("copyWithContext error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("copyWithContext did not return after cancellation")
	}
	if output.String() != "first" {
		t.Fatalf("copy wrote bytes after cancellation: %q", output.String())
	}
}
