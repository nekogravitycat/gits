// Command gits keeps a workspace of many git repos in step.
//
// Composition root: wires signal handling to a context and hands argv to the CLI layer.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/cli"
)

func main() {
	// CRITICAL: os.Exit only here, after run returns so its deferred signal cleanup runs first.
	os.Exit(run())
}

func run() int {
	// NOTE: cancel on interrupt so in-flight git subprocesses are killed with their process tree,
	// not orphaned past gits exit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	code := cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr)

	// An interrupt outranks the command's own outcome: report 130 (spec §6.10).
	if ctx.Err() != nil && code == int(app.ExitOK) {
		return int(app.ExitInterrupted)
	}
	return code
}
