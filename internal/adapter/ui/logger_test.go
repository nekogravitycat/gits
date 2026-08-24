package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestLogger_Progress_Live_RedrawsInPlace(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf, false, true)

	l.Progress("syncing", 0, 2, "")
	l.Progress("syncing", 1, 2, "alpha")
	l.Progress("syncing", 2, 2, "beta")
	l.ProgressDone()

	got := buf.String()
	// The final redraw can carry trailing padding left over from a longer earlier line, so trim
	// trailing spaces before the closing newline rather than pinning the exact byte count.
	got = strings.TrimRight(strings.TrimSuffix(got, "\n"), " ") + "\n"
	want := "\rsyncing [0/2]\rsyncing [1/2] alpha\rsyncing [2/2] beta\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLogger_Progress_Live_PadsOverAShorterPreviousLine(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf, false, true)

	l.Progress("cloning", 1, 10, "a-very-long-repo-name")
	l.Progress("cloning", 2, 10, "x")

	got := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("\rcloning [2/10] x")) {
		t.Fatalf("got %q, missing the second redraw", got)
	}
	// The second, shorter line must pad out the leftover tail of the first, or stale characters
	// stay on screen.
	secondLine := got[bytes.LastIndexByte(buf.Bytes(), '\r'):]
	if len(secondLine) < len("\rcloning [1/10] a-very-long-repo-name") {
		t.Fatalf("second redraw %q was not padded to cover the first", secondLine)
	}
}

func TestLogger_Progress_NonLive_WritesOneLinePerEvent(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf, false, false)

	l.Progress("checking", 0, 2, "")
	l.Progress("checking", 1, 2, "alpha")
	l.ProgressDone()

	want := "checking: 0/2\nchecking [1/2] alpha\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

func TestLogger_Progress_ZeroTotal_IsNoOp(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf, false, true)

	l.Progress("syncing", 0, 0, "")
	l.ProgressDone()

	if buf.Len() != 0 {
		t.Fatalf("expected no output for a zero-repo stage, got %q", buf.String())
	}
}

func TestLogger_ProgressDone_NonLive_WritesNothing(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf, false, false)

	l.Progress("checking", 1, 1, "alpha")
	buf.Reset()
	l.ProgressDone()

	if buf.Len() != 0 {
		t.Fatalf("expected ProgressDone to be a no-op in non-live mode, got %q", buf.String())
	}
}
