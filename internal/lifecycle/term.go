package lifecycle

import (
	"fmt"
	"io"
	"os"
	"strconv"
)

// This is the whole of the terminal handling, and it is hand-written on
// purpose: a lifecycle verb prints a few lines, paints two of them, and redraws
// one. A terminal toolkit would be a new dependency, which this repository
// signs off on deliberately, for a job that is eighty lines.

// Lookup reads an environment variable and says whether it was set at all.
// os.LookupEnv satisfies it; a test hands over a map instead.
type Lookup func(key string) (value string, ok bool)

// Color is a foreground colour, as the SGR parameter that selects it.
type Color string

const (
	Red    Color = "31"
	Green  Color = "32"
	Yellow Color = "33"
	Dim    Color = "90"
)

// Terminal is what this output stream will bear.
//
// Two decisions, taken once and carried as values, so every caller renders the
// same way and a test can put either answer in without a terminal: whether an
// escape code will be read as colour, and whether a line can be redrawn.
type Terminal struct {
	Out io.Writer
	// Color allows the escape codes Paint writes.
	Color bool
	// Redraw allows a progress line to overwrite itself. Off, each step is its
	// own finished line.
	Redraw bool
}

// Detect works out what f will bear. TERM and NO_COLOR are read through env so
// nothing here reaches for the process environment on its own.
func Detect(f *os.File, env Lookup) Terminal {
	return detect(f, isTerminal(f), env)
}

func detect(out io.Writer, terminal bool, env Lookup) Terminal {
	term, _ := env("TERM")
	_, noColor := env("NO_COLOR")
	_, accessible := env("ACCESSIBLE")

	// ACCESSIBLE selects the same plain path a pipe selects. A redrawn region
	// is re-announced in full by a screen reader on every change, so the line
	// that reads best on a terminal reads worst here.
	plain := !terminal || term == "dumb" || accessible
	return Terminal{
		Out:    out,
		Color:  !plain && !noColor,
		Redraw: !plain,
	}
}

// isTerminal reports whether f is a character device — which a pipe, a file and
// a captured stream are not. Stat is enough for the one decision made here and
// costs no dependency.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Paint wraps s in a colour, or returns it untouched where colour is off. It is
// the only place in this package that writes an escape code.
func (t Terminal) Paint(c Color, s string) string {
	if !t.Color {
		return s
	}
	return "\x1b[" + string(c) + "m" + s + "\x1b[0m"
}

// Step reports a stage and how far through it is. done and total are counted in
// whatever unit the stage is measured in; a total of zero means the size is not
// known, and then no proportion is printed rather than a made-up one.
//
// A proportion rather than a spinner because the failure being reported is an
// operator who cannot tell working from stuck. It goes to the stream this
// Terminal holds, which for a verb whose output is parsed is standard error, so
// a progress line never lands in the middle of `status --json`.
func (t Terminal) Step(stage string, done, total int) {
	line := stage
	if total > 0 {
		// Clamped, because a caller that counts in bytes or in files can
		// overrun its own estimate, and a progress line reading 140% tells the
		// person watching it that the thing they are waiting on is broken.
		percent := done * 100 / total
		if percent > 100 {
			percent = 100
		}
		if percent < 0 {
			percent = 0
		}
		line += "  " + strconv.Itoa(percent) + "%"
	}
	if t.Redraw {
		// Carriage return to the margin, then clear what the last, possibly
		// longer, stage left on the line.
		fmt.Fprint(t.Out, "\r"+line+"\x1b[K")
		return
	}
	fmt.Fprintln(t.Out, line)
}

// Finish closes a redrawn progress line so the next thing printed starts at the
// margin. Off a terminal every step already ended its own line and this writes
// nothing.
func (t Terminal) Finish() {
	if t.Redraw {
		fmt.Fprintln(t.Out)
	}
}
