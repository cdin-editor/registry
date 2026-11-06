package main

import (
	"context"
	"os"

	"github.com/cdin-editor/registry/internal/cli"
)

func main() {
	ctx, cancel := commandContext()
	defer cancel()
	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
