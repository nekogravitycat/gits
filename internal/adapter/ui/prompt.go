// Package ui implements the interaction and logging ports: terminal prompts and stderr output.
//
// Architecture Note:
//   - No TUI framework by design (spec §10): a plain read-a-line loop behaves identically across
//     Windows Terminal, Git Bash, SSH and CI logs, with no added dependency.
//   - NOTE: every prompt method must check IsInteractive first; a prompt on a non-terminal hangs
//     forever with no output and no exit (spec §6.7).
//   - Editor selection follows git's own precedence so gits never disagrees with `git commit`.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nekogravitycat/gits/internal/app"
)

// Prompter implements app.Prompter over plain stdin and stderr.
type Prompter struct {
	in          io.Reader
	out         io.Writer
	interactive bool
	reader      *bufio.Reader
}

// NewPrompter builds a prompter. interactive must be false whenever stdin is not a terminal.
func NewPrompter(in io.Reader, out io.Writer, interactive bool) *Prompter {
	return &Prompter{in: in, out: out, interactive: interactive, reader: bufio.NewReader(in)}
}

var _ app.Prompter = (*Prompter)(nil)

// IsInteractive reports whether prompting is possible at all.
func (p *Prompter) IsInteractive() bool { return p.interactive }

// Confirm asks a yes/no question, defaulting to no.
//
// CRITICAL: default must stay "no" -- an empty line, stray pipe newline, or enter-to-dismiss must
// never approve a push.
func (p *Prompter) Confirm(question string) (bool, error) {
	if !p.interactive {
		return false, app.ErrNeedsYes("")
	}
	fmt.Fprintf(p.out, "%s [y/N] ", question)

	line, err := p.reader.ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// Line reads one line of input.
func (p *Prompter) Line(prompt string) (string, error) {
	if !p.interactive {
		return "", app.ErrNeedsYes("")
	}
	fmt.Fprint(p.out, prompt)

	line, err := p.reader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// Editor opens the user's configured editor for a multi-line commit message.
func (p *Prompter) Editor(initial string) (string, error) {
	if !p.interactive {
		return "", app.ErrNeedsYes("")
	}

	file, err := os.CreateTemp("", "gits-commit-*.txt")
	if err != nil {
		return "", err
	}
	name := file.Name()
	defer os.Remove(name)

	if _, err := file.WriteString(initial); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}

	editor := resolveEditor()
	args := append(splitEditorCommand(editor), name)

	//nolint:noctx // NOTE: no timeout -- the editor is a foreground program the user is typing in;
	// a timeout would kill their message mid-sentence. Bounded by the user closing it.
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = p.in
	cmd.Stdout = p.out
	cmd.Stderr = p.out
	if err := cmd.Run(); err != nil {
		return "", err
	}

	content, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	return stripCommentLines(string(content)), nil
}

// resolveEditor follows git's precedence: GIT_EDITOR, VISUAL, EDITOR, then a platform default.
func resolveEditor() string {
	for _, key := range []string{"GIT_EDITOR", "VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	if isWindows() {
		return "notepad"
	}
	return "vi"
}

// splitEditorCommand splits an editor setting that carries arguments, e.g. "code --wait".
func splitEditorCommand(editor string) []string {
	fields := strings.Fields(editor)
	if len(fields) == 0 {
		return []string{"vi"}
	}
	return fields
}

// stripCommentLines removes '#' lines, matching what git does to a commit message buffer.
func stripCommentLines(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, strings.TrimRight(line, "\r"))
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func isWindows() bool { return filepath.Separator == '\\' }
