package lifecycle

import (
	"bytes"
	"strings"
	"testing"
)

// lookup builds an environment for Detect out of a map, so a test never reads
// the environment the suite is running in.
func lookup(env map[string]string) Lookup {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

// Colour is written only where it will be read as colour. Everything else —
// a pipe, a file, a terminal that says it cannot, an operator who asked for
// none — gets plain text, because an escape code in a pasted bug report is
// noise the reader has to strip.
func TestDetectDecidesColourAndRedraw(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
		env      map[string]string
		color    bool
		redraw   bool
	}{
		{
			name:     "a terminal with an ordinary TERM takes both",
			terminal: true,
			env:      map[string]string{"TERM": "xterm-256color"},
			color:    true,
			redraw:   true,
		},
		{
			name:     "a pipe takes neither",
			terminal: false,
			env:      map[string]string{"TERM": "xterm-256color"},
			color:    false,
			redraw:   false,
		},
		{
			name:     "TERM=dumb says the escape codes would not be read",
			terminal: true,
			env:      map[string]string{"TERM": "dumb"},
			color:    false,
			redraw:   false,
		},
		{
			name:     "NO_COLOR is honoured however it is set",
			terminal: true,
			env:      map[string]string{"TERM": "xterm-256color", "NO_COLOR": ""},
			color:    false,
			// A redrawn line is not colour, and an operator who turned colour
			// off did not ask for one line per step.
			redraw: true,
		},
		{
			name:     "ACCESSIBLE takes the same plain path a pipe takes",
			terminal: true,
			env:      map[string]string{"TERM": "xterm-256color", "ACCESSIBLE": "1"},
			color:    false,
			redraw:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := detect(&bytes.Buffer{}, tc.terminal, lookup(tc.env))
			if got.Color != tc.color {
				t.Errorf("Color = %v, want %v", got.Color, tc.color)
			}
			if got.Redraw != tc.redraw {
				t.Errorf("Redraw = %v, want %v", got.Redraw, tc.redraw)
			}
		})
	}
}

// Paint is the only place an escape code is written, and it writes none when
// the terminal said no.
func TestPaintWritesEscapesOnlyWhenColourIsOn(t *testing.T) {
	plain := Terminal{Out: &bytes.Buffer{}}
	if got := plain.Paint(Red, "refused"); got != "refused" {
		t.Errorf("Paint with colour off = %q, want the text unchanged", got)
	}
	colored := Terminal{Out: &bytes.Buffer{}, Color: true}
	got := colored.Paint(Red, "refused")
	if !strings.Contains(got, "refused") {
		t.Errorf("Paint = %q, which lost the text", got)
	}
	if !strings.HasPrefix(got, "\x1b[") || !strings.HasSuffix(got, "\x1b[0m") {
		t.Errorf("Paint = %q, want the text wrapped in an escape and a reset", got)
	}
}

// The progress line names a proportion. A spinner says only that something is
// happening; the failure this reports is an operator who cannot tell working
// from stuck, and a proportion is what tells them apart.
func TestProgressNamesAProportion(t *testing.T) {
	var buf bytes.Buffer
	term := Terminal{Out: &buf}
	term.Step("installing MLX", 2, 5)
	if got := buf.String(); !strings.Contains(got, "40%") {
		t.Errorf("Step = %q, which names no proportion", got)
	}
	if got := buf.String(); !strings.Contains(got, "installing MLX") {
		t.Errorf("Step = %q, which does not name the stage", got)
	}
}

// A stage whose size nothing knows says so by saying nothing, rather than by
// inventing a figure: no percentage at all, and still the stage's name.
func TestProgressOmitsTheProportionItDoesNotHave(t *testing.T) {
	var buf bytes.Buffer
	term := Terminal{Out: &buf}
	term.Step("installing uv", 0, 0)
	if got := buf.String(); strings.Contains(got, "%") {
		t.Errorf("Step = %q, want no percentage for a stage with no total", got)
	}
	if got := buf.String(); !strings.Contains(got, "installing uv") {
		t.Errorf("Step = %q, which does not name the stage", got)
	}
}

// Off a terminal — a pipe, a log file, a screen reader's plain path — each step
// is its own line and nothing is redrawn: a carriage return in a captured log
// leaves the reader with one mangled line, and a redrawn region is re-announced
// in full every time it changes.
func TestProgressWritesOneLinePerStepWhenItCannotRedraw(t *testing.T) {
	var buf bytes.Buffer
	term := Terminal{Out: &buf}
	term.Step("installing uv", 1, 3)
	term.Step("installing MLX", 2, 3)
	got := buf.String()
	if strings.Contains(got, "\r") {
		t.Errorf("Step = %q, which redrew a line on a stream that cannot be redrawn", got)
	}
	if n := strings.Count(got, "\n"); n != 2 {
		t.Errorf("Step wrote %d lines for two steps, want 2: %q", n, got)
	}
}

// On a terminal the same two steps occupy one line: the carriage return and the
// clear-to-end are what keep a multi-minute install to a single line.
func TestProgressRedrawsOneLineOnATerminal(t *testing.T) {
	var buf bytes.Buffer
	term := Terminal{Out: &buf, Redraw: true}
	term.Step("installing uv", 1, 3)
	term.Step("installing MLX", 2, 3)
	got := buf.String()
	if !strings.HasPrefix(got, "\r") {
		t.Errorf("Step = %q, want a redraw to start with a carriage return", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("Step = %q, want no newline until the line is finished", got)
	}
	if !strings.Contains(got, "\x1b[K") {
		t.Errorf("Step = %q, want the rest of the line cleared so a shorter stage does not leave the last one's tail behind", got)
	}
}

// Finish closes the progress line, so whatever is printed next starts at the
// left margin rather than after a half-drawn stage.
func TestFinishEndsTheRedrawnLine(t *testing.T) {
	var buf bytes.Buffer
	term := Terminal{Out: &buf, Redraw: true}
	term.Step("installing MLX", 2, 3)
	term.Finish()
	if got := buf.String(); !strings.HasSuffix(got, "\n") {
		t.Errorf("Finish left the line open: %q", got)
	}

	// Off a terminal every step already ended its own line, so Finish adds
	// nothing rather than an empty one.
	var plain bytes.Buffer
	term = Terminal{Out: &plain}
	term.Step("installing MLX", 2, 3)
	before := plain.Len()
	term.Finish()
	if plain.Len() != before {
		t.Errorf("Finish wrote %q on a stream that ends every step itself", plain.String()[before:])
	}
}

// A stage that reports more work done than it has is still a proportion of a
// whole: the line says 100% rather than a figure that reads as a bug, and a
// count below zero says nothing has been done rather than a negative.
func TestProgressClampsTheProportion(t *testing.T) {
	for _, tc := range []struct {
		done, total int
		want        string
	}{
		{done: 7, total: 5, want: "100%"},
		{done: 5, total: 5, want: "100%"},
		{done: -1, total: 5, want: "0%"},
		{done: 1, total: 4, want: "25%"},
	} {
		var buf bytes.Buffer
		Terminal{Out: &buf}.Step("installing MLX", tc.done, tc.total)
		if !strings.Contains(buf.String(), tc.want) {
			t.Errorf("Step(%d of %d) = %q, want %q", tc.done, tc.total, buf.String(), tc.want)
		}
	}
}
