// Command gits keeps a workspace of many git repos in step.
//
// This file is the composition root and nothing more: it wires signal handling to a context and
// hands argv to the CLI layer, which builds the adapters and calls the use cases.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/cli"
)

func main() {
	// os.Exit is called only here, after run has returned and its deferred signal cleanup has
	// already happened.
	os.Exit(run())
}

func run() int {
	// Cancelling on interrupt lets in-flight git subprocesses be killed with their whole process
	// tree, rather than being orphaned to keep running after gits exits.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	code := cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr)

	// An interrupt outranks whatever the command was about to report: the run did not finish, and
	// 130 is the conventional way to say a signal ended it (spec §6.10).
	if ctx.Err() != nil && code == int(app.ExitOK) {
		return int(app.ExitInterrupted)
	}
	return code
}
