//go:build unix

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// commandContext stops HTTP and Git work on Ctrl+C or SIGTERM.
func commandContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
