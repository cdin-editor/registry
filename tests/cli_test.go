package tests

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cdin-editor/registry/internal/cli"
)

func TestSchemaList(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"schema", "list"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run() code = %d, stderr = %s", code, stderr.String())
	}
	for _, kind := range []string{"catalog-index", "component-release", "product-definition", "resolution-report", "resolved-manifest"} {
		if !strings.Contains(stdout.String(), kind) {
			t.Errorf("schema list missing %q", kind)
		}
	}
}

func TestSchemaShowReturnsJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"schema", "show", "product-definition"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run() code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "\"$schema\": \"https://json-schema.org/draft/2020-12/schema\"") {
		t.Fatalf("schema output is missing the Draft 2020-12 declaration: %s", stdout.String())
	}
}

func TestValidateFromStdin(t *testing.T) {
	input := "{\"schema_version\":\"1.0.0\",\"components\":[{\"component_id\":\"backend\",\"version_constraint\":\">=1.0.0 <2.0.0\"}]}"
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"validate", "product-definition", "-"}, strings.NewReader(input), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run() code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "OK product-definition") {
		t.Fatalf("unexpected stdout: %s", stdout.String())
	}
}

func TestInvalidDocumentReturnsExitCodeOne(t *testing.T) {
	input := "{\"schema_version\":\"1.0.0\",\"components\":[]}"
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"validate", "product-definition", "-"}, strings.NewReader(input), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("Run() code = %d, want 1; stderr = %s", code, stderr.String())
	}
}

func TestUnknownDocumentKindReturnsExitCodeTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"validate", "unknown", "file.json"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
}

func TestCanceledContextReturnsExitCodeOne(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	code := cli.Run(ctx, []string{"schema", "list"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
}
