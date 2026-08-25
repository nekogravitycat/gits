package output

import (
	"os"

	"github.com/nekogravitycat/gits/internal/app"
	"github.com/nekogravitycat/gits/internal/domain"
)

// Style decides how much decoration the human renderer may use. Color and Emoji degrade
// independently: a pipe handles UTF-8 but not colour; an old console renders colour but mangles
// glyphs.
type Style struct {
	Color bool
	Emoji bool
}

// NewStyle resolves the output style for a stream. plain forces the lowest common denominator;
// otherwise colour needs a terminal, and NO_COLOR is honoured per convention (spec §6.4).
func NewStyle(isTTY, plain bool) Style {
	if plain {
		return Style{}
	}
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		return Style{Emoji: isTTY}
	}
	return Style{Color: isTTY, Emoji: isTTY}
}

// ANSI colour codes, used only when Style.Color is set.
const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[34m"
	ansiCyan   = "\x1b[36m"
	ansiBold   = "\x1b[1m"
)

func (s Style) paint(code, text string) string {
	if !s.Color {
		return text
	}
	return code + text + ansiReset
}

// Dim renders de-emphasised text (e.g. clean repos: present but not competing for attention).
func (s Style) Dim(text string) string { return s.paint(ansiDim, text) }

// Bold renders a heading.
func (s Style) Bold(text string) string { return s.paint(ansiBold, text) }

// Symbol returns the marker for a repo state.
//
// CRITICAL: ASCII fallbacks are load-bearing -- Windows consoles without a UTF-8 code page render
// the glyphs as mojibake, and an unreadable status report is worse than a plain one (spec §6.4).
// Every fallback is exactly one char wide to match the glyphs; a wider marker would misalign the
// row's columns and break at-a-glance scanning.
func (s Style) Symbol(state domain.RepoState) string {
	switch state {
	case domain.StateClean:
		return s.paint(ansiGreen, s.pick("✓", "+"))
	case domain.StateDirty:
		return s.paint(ansiYellow, s.pick("●", "*"))
	case domain.StateAhead:
		return s.paint(ansiCyan, s.pick("↑", "^"))
	case domain.StateBehind:
		return s.paint(ansiBlue, s.pick("↓", "v"))
	case domain.StateDiverged, domain.StateDetached, domain.StateNoUpstream:
		return s.paint(ansiYellow, s.pick("⚠", "!"))
	case domain.StateMissing, domain.StateNotARepo, domain.StateError:
		return s.paint(ansiRed, s.pick("✗", "x"))
	default:
		return s.pick("?", "?")
	}
}

// VerdictSymbol returns the marker for a dependency verdict.
func (s Style) VerdictSymbol(v domain.PinVerdict) string {
	switch v {
	case domain.PinUpToDate:
		return s.paint(ansiGreen, s.pick("✓", "+"))
	case domain.PinBehind:
		return s.paint(ansiBlue, s.pick("↓", "v"))
	case domain.PinAhead:
		return s.paint(ansiCyan, s.pick("↑", "^"))
	case domain.PinDiverged:
		return s.paint(ansiYellow, s.pick("⚠", "!"))
	case domain.PinUnknown:
		return s.paint(ansiDim, "?")
	default:
		return "?"
	}
}

// ActionSymbol returns the marker for a write outcome.
func (s Style) ActionSymbol(a app.Action) string {
	switch a {
	case app.ActionFailed:
		return s.paint(ansiRed, s.pick("✗", "x"))
	case app.ActionSkipped:
		return s.paint(ansiYellow, "-")
	case app.ActionUpdated:
		return s.paint(ansiGreen, s.pick("✓", "+"))
	case app.ActionPlanned:
		return s.paint(ansiCyan, s.pick("→", ">"))
	case app.ActionUpToDate:
		return s.Dim(s.pick("·", "."))
	default:
		return s.Dim("?")
	}
}

func (s Style) pick(unicode, ascii string) string {
	if s.Emoji {
		return unicode
	}
	return ascii
}
