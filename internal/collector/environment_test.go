package collector

import (
	"context"
	"os"
	"strings"
	"testing"
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
