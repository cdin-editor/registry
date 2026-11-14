package cli

import "testing"

func TestGitHubAPITokenFromEnvironmentSelection(t *testing.T) {
	tests := []struct {
		name     string
		actions  string
		registry string
		gh       string
		github   string
		want     string
	}{
		{
			name:     "explicit Registry token wins",
			actions:  "true",
			registry: " registry-read-token ",
			gh:       "gh-token",
			github:   "repo-scoped-token",
			want:     "registry-read-token",
		},
		{
			name:    "GH_TOKEN is accepted in Actions",
			actions: "true",
			gh:      " cross-repository-read-token ",
			github:  "repo-scoped-token",
			want:    "cross-repository-read-token",
		},
		{
			name:    "automatic Actions GITHUB_TOKEN is ignored",
			actions: "true",
			github:  "repo-scoped-token",
			want:    "",
		},
		{
			name:   "local GITHUB_TOKEN remains supported",
			github: " local-personal-token ",
			want:   "local-personal-token",
		},
		{
			name:   "GH_TOKEN works outside Actions",
			gh:     "local-gh-token",
			github: "local-github-token",
			want:   "local-gh-token",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GITHUB_ACTIONS", test.actions)
			t.Setenv("REGISTRY_GITHUB_API_TOKEN", test.registry)
			t.Setenv("GH_TOKEN", test.gh)
			t.Setenv("GITHUB_TOKEN", test.github)

			if got := githubAPITokenFromEnvironment(); got != test.want {
				t.Fatalf("githubAPITokenFromEnvironment() = %q, want %q", got, test.want)
			}
		})
	}
}
