// Package ui implements the interaction and logging ports: terminal prompts and stderr output.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/nekogravitycat/gits/internal/app"
)

// Logger implements app.Logger.
//
// Architecture Note:
//   - CRITICAL: everything written here goes to stderr only. In --json mode stdout must carry
//     exactly one JSON object; a stray progress line breaks `gits status --json | jq` (spec §6.4).
//   - live means the terminal can redraw a line in place (real TTY, not piped, not --plain/--json);
//     otherwise one plain line per event, matching the ASCII/no-colour fallback (spec §6.4).
//   - NOTE: mu serialises writes -- Progress runs from mapRepos worker goroutines, so without it a
//     redraw and another repo's completion could interleave mid-write.
type Logger struct {
	out       io.Writer
	verbose   bool
	live      bool
	mu        sync.Mutex
	lastWidth int
}

// NewLogger builds a logger writing to out (always stderr). live is the caller's own
// TTY/--plain/--json determination; Logger cannot derive it.
func NewLogger(out io.Writer, verbose, live bool) *Logger {
	return &Logger{out: out, verbose: verbose, live: live}
}

var _ app.Logger = (*Logger)(nil)

// Verbosef writes the git commands and their output, only under -v.
func (l *Logger) Verbosef(format string, args ...any) {
	if !l.verbose {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.out, format+"\n", args...)
}

// Warnf writes something the user should see regardless of verbosity.
func (l *Logger) Warnf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.out, format+"\n", args...)
}

// Progress reports a concurrent stage's completion count (calling convention: see app.Logger --
// one done==0 call announces the stage, one call per finished repo).
func (l *Logger) Progress(stage string, done, total int, name string) {
	if total <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.live {
		if name == "" {
			fmt.Fprintf(l.out, "%s: 0/%d\n", stage, total)
			return
		}
		fmt.Fprintf(l.out, "%s [%d/%d] %s\n", stage, done, total, name)
		return
	}

	line := fmt.Sprintf("%s [%d/%d]", stage, done, total)
	if name != "" {
		line += " " + name
	}
	pad := l.lastWidth - len(line)
	l.lastWidth = len(line)
	if pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	fmt.Fprintf(l.out, "\r%s", line)
}

// ProgressDone closes out the last live progress line so the next output starts fresh instead of
// overwriting it.
func (l *Logger) ProgressDone() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.live && l.lastWidth > 0 {
		fmt.Fprintln(l.out)
		l.lastWidth = 0
	}
}

// Stderr returns the process stderr, for wiring in main().
func Stderr() io.Writer { return os.Stderr }
