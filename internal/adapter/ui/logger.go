package ui

import (
	"fmt"
	"io"
	"os"

	"github.com/nekogravitycat/gits/internal/app"
)

// Logger implements app.Logger.
//
// Everything it writes goes to stderr, without exception. In --json mode stdout has to carry
// exactly one JSON object and nothing else, or `gits status --json | jq` breaks on the first
// stray progress line (spec §6.4).
type Logger struct {
	out     io.Writer
	verbose bool
}

// NewLogger builds a logger writing to out, which should always be stderr.
func NewLogger(out io.Writer, verbose bool) *Logger {
	return &Logger{out: out, verbose: verbose}
}

var _ app.Logger = (*Logger)(nil)

// Verbosef writes only under -v: the git commands being run and their output.
func (l *Logger) Verbosef(format string, args ...any) {
	if !l.verbose {
		return
	}
	fmt.Fprintf(l.out, format+"\n", args...)
}

// Warnf writes something the user should see regardless of verbosity.
func (l *Logger) Warnf(format string, args ...any) {
	fmt.Fprintf(l.out, format+"\n", args...)
}

// Stderr returns the process stderr, for wiring in main().
func Stderr() io.Writer { return os.Stderr }
