package collector

import (
	"strings"
	"testing"
)

func FuzzValidateCredentialEnvironment(f *testing.F) {
	for _, seed := range []string{
		"GIT_ASKPASS=/tmp/askpass\nGIT_ASKPASS_REQUIRE=force\nREGISTRY_GIT_CREDENTIAL_TOKEN=opaque",
		"PATH=/tmp/evil",
		"SSH_ASKPASS=/tmp/askpass",
		"GIT_ASKPASS=relative\nGIT_ASKPASS_REQUIRE=force",
		"REGISTRY_GIT_CREDENTIAL_TOKEN=secret",
		"GIT_ASKPASS=/tmp/askpass\nGIT_ASKPASS_REQUIRE=force\nGIT_ASKPASS=/tmp/other",
		"GIT_ASKPASS=/tmp/askpass\nGIT_ASKPASS_REQUIRE=force\nREGISTRY_GIT_CREDENTIAL_=empty",
		"GIT_ASKPASS=/tmp/askpass\nGIT_ASKPASS_REQUIRE=force\nREGISTRY_GIT_CREDENTIAL_TOKEN=value\nHTTPS_PROXY=https://proxy.invalid",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 64*1024 {
			t.Skip()
		}
		validated, err := validateCredentialEnvironment(strings.Split(input, "\n"))
		if err != nil {
			return
		}
		previous := ""
		hasAskPass := false
		hasAskPassRequire := false
		for _, item := range validated {
			index := strings.IndexByte(item, '=')
			if index <= 0 {
				t.Fatalf("validated entry has no key/value separator: %q", item)
			}
			key := item[:index]
			if key <= previous {
				t.Fatalf("validated environment is not uniquely sorted: %q after %q", key, previous)
			}
			previous = key
			switch {
			case key == "GIT_ASKPASS":
				hasAskPass = true
			case key == "GIT_ASKPASS_REQUIRE":
				if item[index+1:] != "force" {
					t.Fatal("validated askpass mode is not forced")
				}
				hasAskPassRequire = true
			case strings.HasPrefix(key, "REGISTRY_GIT_CREDENTIAL_"):
			default:
				t.Fatalf("unsupported validated environment key %q", key)
			}
		}
		if hasAskPass != hasAskPassRequire {
			t.Fatal("validated environment has an incomplete askpass pair")
		}
	})
}

func FuzzValidateCredentialEnvironmentDoesNotEchoSecret(f *testing.F) {
	for _, seed := range []string{"opaque", "short", "with=equals", "line\nbreak", "\x00", strings.Repeat("x", 128)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if len(value) > 4096 {
			t.Skip()
		}
		secret := "registry-fuzz-secret-marker-" + value
		_, err := validateCredentialEnvironment([]string{
			"REGISTRY_GIT_CREDENTIAL_TOKEN=" + secret,
		})
		if err == nil {
			t.Fatal("credential secret without forced askpass was accepted")
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatal("credential validation error echoed a secret value")
		}
	})
}
