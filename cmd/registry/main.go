package main

import (
	"os"

	"github.com/cdin-editor/registry/internal/cli"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := commandContext()
	defer cancel()
	return cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}
