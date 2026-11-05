package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type gixBranch struct {
	Name               string   `yaml:"name"`
	Type               string   `yaml:"type"`
	Prefix             string   `yaml:"prefix"`
	Parent             string   `yaml:"parent"`
	MergeInto          []string `yaml:"merge_into"`
	UpstreamStrategy   string   `yaml:"upstream_strategy"`
	DownstreamStrategy string   `yaml:"downstream_strategy"`
	Tag                bool     `yaml:"tag"`
	AutoSync           bool     `yaml:"autosync"`
	DeleteOnFinish     bool     `yaml:"delete_on_finish"`
}

type gixFile struct {
	Gix struct {
		Version string `yaml:"version"`
	} `yaml:"gix"`
	Remote string               `yaml:"remote"`
	Branch map[string]gixBranch `yaml:"branch"`
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate the governance test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), ".."))
}

func repositoryFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(t), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func decodeYAMLMap(t *testing.T, name string) map[string]any {
	t.Helper()
	var document map[string]any
	if err := yaml.Unmarshal(repositoryFile(t, name), &document); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	if document == nil {
		t.Fatalf("%s decoded to an empty document", name)
	}
	return document
}

func asStringMap(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)
	return result, ok
}

func asSlice(value any) ([]any, bool) {
	result, ok := value.([]any)
	return result, ok
}

func validIssueFieldID(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
			return false
		}
	}
	return true
}

func TestRepositoryGovernanceGIXTopology(t *testing.T) {
	var config gixFile
	if err := yaml.Unmarshal(repositoryFile(t, ".gix/config"), &config); err != nil {
		t.Fatalf("parse .gix/config: %v", err)
	}

	if config.Gix.Version != "1" {
		t.Fatalf("gix.version = %q, want 1", config.Gix.Version)
	}
	if config.Remote != "origin" {
		t.Fatalf("remote = %q, want origin", config.Remote)
	}

	wantKinds := []string{"main", "develop", "feature", "bugfix", "hotfix", "release"}
	if len(config.Branch) != len(wantKinds) {
		t.Fatalf("configured branch kinds = %v, want exactly %v", config.Branch, wantKinds)
	}
	for _, kind := range wantKinds {
		if _, ok := config.Branch[kind]; !ok {
			t.Errorf("missing GIX branch kind %q", kind)
		}
	}

	main := config.Branch["main"]
	if main.Type != "base" || main.Name != "main" || main.DeleteOnFinish {
		t.Errorf("main branch config = %+v, want permanent main base branch", main)
	}
	develop := config.Branch["develop"]
	if develop.Type != "base" || develop.Name != "develop" || develop.Parent != "main" || !develop.AutoSync || develop.DeleteOnFinish {
		t.Errorf("develop branch config = %+v, want permanent develop base branch parented to main with autosync", develop)
	}

	assertTopic := func(kind, prefix, parent string, mergeInto []string, tag bool) {
		t.Helper()
		got, ok := config.Branch[kind]
		if !ok {
			t.Fatalf("missing topic branch kind %q", kind)
		}
		if got.Type != "topic" || got.Prefix != prefix || got.Parent != parent {
			t.Errorf("%s topology = %+v, want type=topic prefix=%q parent=%q", kind, got, prefix, parent)
		}
		if got.UpstreamStrategy != "merge" || got.DownstreamStrategy != "merge" {
			t.Errorf("%s strategies = %q/%q, want merge/merge", kind, got.UpstreamStrategy, got.DownstreamStrategy)
		}
		if got.DeleteOnFinish != true {
			t.Errorf("%s delete_on_finish = false, want true", kind)
		}
		if got.Tag != tag {
			t.Errorf("%s tag = %t, want %t", kind, got.Tag, tag)
		}
		if len(got.MergeInto) != len(mergeInto) {
			t.Errorf("%s merge_into = %v, want %v", kind, got.MergeInto, mergeInto)
			return
		}
		for i := range mergeInto {
			if got.MergeInto[i] != mergeInto[i] {
				t.Errorf("%s merge_into = %v, want %v", kind, got.MergeInto, mergeInto)
				break
			}
		}
	}

	assertTopic("feature", "feature/", "develop", nil, false)
	assertTopic("bugfix", "bugfix/", "develop", nil, false)
	assertTopic("hotfix", "hotfix/", "main", []string{"main", "develop"}, true)
	assertTopic("release", "release/", "develop", []string{"main", "develop"}, true)

	baseBranches := map[string]bool{"main": true, "develop": true}
	for kind, spec := range config.Branch {
		if spec.Type != "topic" {
			continue
		}
		if !baseBranches[spec.Parent] {
			t.Errorf("%s parent %q is not a configured base branch", kind, spec.Parent)
		}
		targets := spec.MergeInto
		if len(targets) == 0 {
			targets = []string{spec.Parent}
		}
		seen := make(map[string]bool, len(targets))
		for _, target := range targets {
			if !baseBranches[target] {
				t.Errorf("%s merge target %q is not a configured base branch", kind, target)
			}
			if seen[target] {
				t.Errorf("%s has duplicate merge target %q", kind, target)
			}
			seen[target] = true
		}
	}
}

func TestRepositoryGovernancePullRequestTemplate(t *testing.T) {
	content := string(repositoryFile(t, ".github/PULL_REQUEST_TEMPLATE.md"))
	required := []string{
		"## Summary",
		"## Why is this needed?",
		"## Registry area",
		"## Specification references",
		"## Design and compatibility",
		"## Verification",
		"pipeline run check --json",
		"psx check",
		"gix config list",
		"Schema mirror check",
		"Positive and negative tests",
		"Failure cleanup and path-safety",
		"CHANGELOG.md",
		"I reviewed the complete diff.",
	}
	for _, fragment := range required {
		if !strings.Contains(content, fragment) {
			t.Errorf("pull request template is missing required review item %q", fragment)
		}
	}
}

func TestRepositoryGovernanceIssueForms(t *testing.T) {
	forms := []string{
		".github/ISSUE_TEMPLATE/bug_report.yml",
		".github/ISSUE_TEMPLATE/feature_request.yml",
		".github/ISSUE_TEMPLATE/spec_contract.yml",
	}
	for _, path := range forms {
		t.Run(filepath.Base(path), func(t *testing.T) {
			form := decodeYAMLMap(t, path)
			for _, key := range []string{"name", "description", "title"} {
				value, ok := form[key].(string)
				if !ok || strings.TrimSpace(value) == "" {
					t.Errorf("%s must be a non-empty string", key)
				}
			}
			body, ok := asSlice(form["body"])
			if !ok || len(body) == 0 {
				t.Fatal("body must be a non-empty YAML sequence")
			}

			seenIDs := make(map[string]bool)
			requiredFields := 0
			allowedTypes := map[string]bool{
				"markdown":   true,
				"input":      true,
				"dropdown":   true,
				"textarea":   true,
				"checkboxes": true,
			}
			for index, rawField := range body {
				field, ok := asStringMap(rawField)
				if !ok {
					t.Errorf("body[%d] must be a mapping", index)
					continue
				}
				fieldType, ok := field["type"].(string)
				if !ok || !allowedTypes[fieldType] {
					t.Errorf("body[%d] has unsupported field type %v", index, field["type"])
					continue
				}
				attributes, ok := asStringMap(field["attributes"])
				if !ok {
					t.Errorf("body[%d].attributes must be a mapping", index)
					continue
				}
				if fieldType == "markdown" {
					value, ok := attributes["value"].(string)
					if !ok || strings.TrimSpace(value) == "" {
						t.Errorf("body[%d] markdown value must be non-empty", index)
					}
					continue
				}

				id, ok := field["id"].(string)
				if !ok || !validIssueFieldID(id) {
					t.Errorf("body[%d] has invalid or missing field id %q", index, id)
				} else if seenIDs[id] {
					t.Errorf("duplicate issue field id %q", id)
				} else {
					seenIDs[id] = true
				}
				label, ok := attributes["label"].(string)
				if !ok || strings.TrimSpace(label) == "" {
					t.Errorf("body[%d] requires a non-empty label", index)
				}

				if validations, exists := field["validations"]; exists {
					validationMap, ok := asStringMap(validations)
					if !ok {
						t.Errorf("body[%d].validations must be a mapping", index)
					} else if required, exists := validationMap["required"]; exists {
						if _, ok := required.(bool); !ok {
							t.Errorf("body[%d].validations.required must be boolean", index)
						} else if required == true {
							requiredFields++
						}
					}
				}

				switch fieldType {
				case "dropdown":
					options, ok := asSlice(attributes["options"])
					if !ok || len(options) == 0 {
						t.Errorf("body[%d] dropdown requires non-empty options", index)
						continue
					}
					seenOptions := make(map[string]bool)
					for _, option := range options {
						value, ok := option.(string)
						if !ok || strings.TrimSpace(value) == "" {
							t.Errorf("body[%d] dropdown options must be non-empty strings", index)
							continue
						}
						if seenOptions[value] {
							t.Errorf("body[%d] has duplicate dropdown option %q", index, value)
						}
						seenOptions[value] = true
					}
				case "checkboxes":
					options, ok := asSlice(attributes["options"])
					if !ok || len(options) == 0 {
						t.Errorf("body[%d] checkboxes require non-empty options", index)
						continue
					}
					for optionIndex, rawOption := range options {
						option, ok := asStringMap(rawOption)
						if !ok {
							t.Errorf("body[%d] checkbox option %d must be a mapping", index, optionIndex)
							continue
						}
						optionLabel, labelOK := option["label"].(string)
						required, requiredOK := option["required"].(bool)
						if !labelOK || strings.TrimSpace(optionLabel) == "" || !requiredOK {
							t.Errorf("body[%d] checkbox option %d needs a label and boolean required flag", index, optionIndex)
						}
						if required {
							requiredFields++
						}
					}
				}
			}
			if len(seenIDs) == 0 {
				t.Error("issue form must contain at least one interactive field")
			}
			if requiredFields == 0 {
				t.Error("issue form must require at least one response")
			}
		})
	}
}

func TestRepositoryGovernanceIssueTemplateConfig(t *testing.T) {
	config := decodeYAMLMap(t, ".github/ISSUE_TEMPLATE/config.yml")
	if enabled, ok := config["blank_issues_enabled"].(bool); !ok || enabled {
		t.Errorf("blank_issues_enabled = %v, want false", config["blank_issues_enabled"])
	}
	links, ok := asSlice(config["contact_links"])
	if !ok || len(links) == 0 {
		t.Fatal("contact_links must be a non-empty sequence")
	}
	for index, rawLink := range links {
		link, ok := asStringMap(rawLink)
		if !ok {
			t.Errorf("contact_links[%d] must be a mapping", index)
			continue
		}
		for _, key := range []string{"name", "url", "about"} {
			value, ok := link[key].(string)
			if !ok || strings.TrimSpace(value) == "" {
				t.Errorf("contact_links[%d].%s must be a non-empty string", index, key)
			}
		}
	}
}

func TestRepositoryGovernancePSXRequiresProjectAndReviewFiles(t *testing.T) {
	config := decodeYAMLMap(t, "psx.yml")
	project, ok := asStringMap(config["project"])
	if !ok || project["type"] != "go" || project["kind"] != "cli" {
		t.Fatalf("PSX project metadata = %v, want Go CLI", config["project"])
	}
	rules, ok := asStringMap(config["rules"])
	if !ok {
		t.Fatal("PSX rules must be a mapping")
	}
	for _, key := range []string{
		"readme",
		"license",
		"package_manager",
		"lockfile",
		"tests_folder",
		"contributing",
		"security",
		"pull_request_template",
		"issue_templates",
		"ci_config",
		"workflow_ci",
		"workflow_release",
	} {
		if rules[key] != "error" {
			t.Errorf("PSX rule %q = %v, want error", key, rules[key])
		}
	}
}
