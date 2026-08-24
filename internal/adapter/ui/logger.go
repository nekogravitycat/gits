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
// Everything it writes goes to stderr, without exception. In --json mode stdout has to carry
// exactly one JSON object and nothing else, or `gits status --json | jq` breaks on the first
// stray progress line (spec §6.4).
type Logger struct {
	out     io.Writer
	verbose bool
	// live is whether the terminal can redraw a line in place: a real TTY, not piped, and not
	// downgraded by --plain or --json. Everything else gets one plain line per event instead,
	// same as the ASCII/no-colour fallback the human renderer already applies (spec §6.4).
	live bool

	// mu serialises writes: Progress is called from mapRepos's worker goroutines, so without a
	// lock a redrawn line and a completion from another repo could interleave mid-write.
	mu        sync.Mutex
	lastWidth int
}

// NewLogger builds a logger writing to out, which should always be stderr. live enables in-place
// progress redraw and should be the caller's own TTY/--plain/--json determination -- Logger has
// no way to know that on its own.
func NewLogger(out io.Writer, verbose, live bool) *Logger {
	return &Logger{out: out, verbose: verbose, live: live}
}

var _ app.Logger = (*Logger)(nil)

// Verbosef writes only under -v: the git commands being run and their output.
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

// Progress reports a concurrent stage's completion count. See app.Logger for the calling
// convention (one done==0 call to announce the stage, one call per finished repo).
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

// ProgressDone closes out whatever the last Progress call left on screen, so the next line of
// output -- report or warning -- starts on a fresh line instead of overwriting the progress line.
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
