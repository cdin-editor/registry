package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cdin-editor/registry/internal/buildinfo"
	"github.com/cdin-editor/registry/internal/schemas"
	"github.com/cdin-editor/registry/internal/validation"
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
	fmt.Fprintf(stderr, "registry: "+format+"\n", args...)
	fmt.Fprintln(stderr)
	printUsage(stderr)
	return exitUsage
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, strings.TrimSpace("Registry — generic component delivery and orchestration

Usage:
  registry --version
  registry help
  registry schema list
  registry schema show <kind>
  registry validate <kind> <file|->

Document kinds:
  product-definition
  catalog-index
  component-release
  resolved-manifest
  resolution-report

Notes:
  YAML (.yaml/.yml) is accepted only for Product Definitions.
  Standard input (-) is read as JSON."))
}
