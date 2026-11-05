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

func TestUsageErrorHasExitCodeTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"unknown"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
}
