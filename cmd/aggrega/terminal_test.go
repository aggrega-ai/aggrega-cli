// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"slices"
	"testing"
)

// fakeEscapesBit is the mode bit the fake console treats as "interprets escape
// sequences". The control carries its own bit, so any value serves.
const fakeEscapesBit uint32 = 0x4

// fakeConsole stands in for a console that has to be asked to interpret escape
// sequences: a mode per descriptor, a read and a write that can each be made to
// fail, and a record of every mode written.
type fakeConsole struct {
	modes   map[uintptr]uint32
	readErr error
	setErr  error
	writes  []uint32
}

func (c *fakeConsole) control() *consoleControl {
	return &consoleControl{
		getMode: func(fd uintptr) (uint32, error) {
			if c.readErr != nil {
				return 0, c.readErr
			}
			return c.modes[fd], nil
		},
		setMode: func(fd uintptr, mode uint32) error {
			if c.setErr != nil {
				return c.setErr
			}
			c.writes = append(c.writes, mode)
			c.modes[fd] = mode
			return nil
		},
		escapes: fakeEscapesBit,
	}
}

// withConsole installs control as this platform's console control for the
// duration of the test, with an empty record of prepared consoles, and puts both
// back afterwards. A nil control is a platform whose terminals interpret escape
// sequences unasked.
func withConsole(t *testing.T, control *consoleControl) {
	t.Helper()
	origControl, origPrepared := platformConsole, preparedConsoles
	t.Cleanup(func() { platformConsole, preparedConsoles = origControl, origPrepared })
	platformConsole = control
	preparedConsoles = &consoleRegistry{ready: map[uintptr]bool{}}
}

// withTerminals makes fileTerminal answer yes for exactly the given streams.
func withTerminals(t *testing.T, terminals ...*os.File) {
	t.Helper()
	orig := fileTerminal
	t.Cleanup(func() { fileTerminal = orig })
	fileTerminal = func(w io.Writer) bool {
		f, ok := w.(*os.File)
		return ok && slices.Contains(terminals, f)
	}
}

// consoleFile returns a real file for a test to treat as a console: what
// outputTerminal reads from it is its descriptor, exactly as from a real one.
func consoleFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "console")
	if err != nil {
		t.Fatalf("create console stand-in: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// TestConsoleControl_EnableEscapes drives the one place a console is asked to
// interpret escape sequences through every answer a console can give.
func TestConsoleControl_EnableEscapes(t *testing.T) {
	t.Parallel()
	const fd uintptr = 7
	const otherBits uint32 = 0x3
	cases := []struct {
		name           string
		start          uint32
		readErr        error
		setErr         error
		wantInterprets bool
		wantWrites     []uint32
		wantUndo       bool
	}{
		{
			name:           "a console whose mode cannot be read does not interpret them",
			readErr:        errors.New("not a console"),
			wantInterprets: false,
		},
		{
			name:           "a console already interpreting them is left as it was",
			start:          otherBits | fakeEscapesBit,
			wantInterprets: true,
		},
		{
			name:           "a console that refuses to turn them on does not interpret them",
			start:          otherBits,
			setErr:         errors.New("an older console refuses the mode"),
			wantInterprets: false,
		},
		{
			name:           "a console that turns them on keeps every other bit and can be put back",
			start:          otherBits,
			wantInterprets: true,
			wantWrites:     []uint32{otherBits | fakeEscapesBit},
			wantUndo:       true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			console := &fakeConsole{modes: map[uintptr]uint32{fd: tc.start}, readErr: tc.readErr, setErr: tc.setErr}
			interprets, undo := console.control().enableEscapes(fd)
			if interprets != tc.wantInterprets {
				t.Errorf("interprets = %v, want %v", interprets, tc.wantInterprets)
			}
			if !slices.Equal(console.writes, tc.wantWrites) {
				t.Errorf("modes written = %#x, want %#x", console.writes, tc.wantWrites)
			}
			if (undo != nil) != tc.wantUndo {
				t.Fatalf("undo returned = %v, want %v", undo != nil, tc.wantUndo)
			}
			if undo != nil {
				undo()
				if got := console.modes[fd]; got != tc.start {
					t.Errorf("mode after undo = %#x, want the mode it was found in, %#x", got, tc.start)
				}
			}
		})
	}
}

// TestOutputTerminal_Shapes pins the one terminal decision against every kind of
// stream it can be handed.
func TestOutputTerminal_Shapes(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal, platformConsole and
	// preparedConsoles seams other tests read.
	t.Run("a stream nobody reads at a terminal", func(t *testing.T) {
		f := consoleFile(t)
		withTerminals(t)
		withConsole(t, nil)
		if got := outputTerminal(f); got != notATerminal {
			t.Errorf("outputTerminal = %v, want notATerminal", got)
		}
	})
	t.Run("a terminal on a platform whose terminals interpret escape sequences unasked", func(t *testing.T) {
		f := consoleFile(t)
		withTerminals(t, f)
		withConsole(t, nil)
		if got := outputTerminal(f); got != drawingTerminal {
			t.Errorf("outputTerminal = %v, want drawingTerminal", got)
		}
	})
	t.Run("a console that turned them on when asked", func(t *testing.T) {
		f := consoleFile(t)
		withTerminals(t, f)
		withConsole(t, (&fakeConsole{modes: map[uintptr]uint32{}}).control())
		prepareTerminals(f)
		if got := outputTerminal(f); got != drawingTerminal {
			t.Errorf("outputTerminal = %v, want drawingTerminal", got)
		}
	})
	t.Run("a console that refused to turn them on", func(t *testing.T) {
		f := consoleFile(t)
		withTerminals(t, f)
		withConsole(t, (&fakeConsole{modes: map[uintptr]uint32{}, setErr: errors.New("refused")}).control())
		prepareTerminals(f)
		if got := outputTerminal(f); got != plainTerminal {
			t.Errorf("outputTerminal = %v, want plainTerminal", got)
		}
	})
	t.Run("a console nobody asked", func(t *testing.T) {
		f := consoleFile(t)
		withTerminals(t, f)
		withConsole(t, (&fakeConsole{modes: map[uintptr]uint32{}}).control())
		if got := outputTerminal(f); got != plainTerminal {
			t.Errorf("outputTerminal = %v, want plainTerminal: an unprepared console is written plain", got)
		}
	})
	t.Run("a writer with no console behind it", func(t *testing.T) {
		withTTY(t, true)
		withConsole(t, (&fakeConsole{modes: map[uintptr]uint32{}}).control())
		if got := outputTerminal(&bytes.Buffer{}); got != drawingTerminal {
			t.Errorf("outputTerminal = %v, want drawingTerminal", got)
		}
	})
}

// TestPrepareTerminals_PutsEachConsoleBack pins what preparation touches: every
// terminal stream's console is asked once, a redirected stream is left alone, a
// console already interpreting escape sequences is not written to, and restoring
// puts each console it changed back in the mode it was found in.
func TestPrepareTerminals_PutsEachConsoleBack(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal, platformConsole and
	// preparedConsoles seams other tests read.
	out, errOut, redirected := consoleFile(t), consoleFile(t), consoleFile(t)
	withTerminals(t, out, errOut)
	const otherBits uint32 = 0x3
	console := &fakeConsole{modes: map[uintptr]uint32{
		out.Fd():        otherBits,
		errOut.Fd():     otherBits | fakeEscapesBit,
		redirected.Fd(): otherBits,
	}}
	withConsole(t, console.control())

	restore := prepareTerminals(out, errOut, redirected)

	if got, want := console.writes, []uint32{otherBits | fakeEscapesBit}; !slices.Equal(got, want) {
		t.Errorf("modes written while preparing = %#x, want %#x (only the console that had them off)", got, want)
	}
	if got := console.modes[redirected.Fd()]; got != otherBits {
		t.Errorf("a redirected stream's mode = %#x, want it untouched at %#x", got, otherBits)
	}
	for _, f := range []*os.File{out, errOut} {
		if got := outputTerminal(f); got != drawingTerminal {
			t.Errorf("outputTerminal(%s) = %v, want drawingTerminal after preparation", f.Name(), got)
		}
	}
	if got := outputTerminal(redirected); got != notATerminal {
		t.Errorf("outputTerminal(redirected) = %v, want notATerminal", got)
	}

	restore()

	if got := console.modes[out.Fd()]; got != otherBits {
		t.Errorf("mode after restore = %#x, want the mode it was found in, %#x", got, otherBits)
	}
	if got := console.modes[errOut.Fd()]; got != otherBits|fakeEscapesBit {
		t.Errorf("a console found interpreting escape sequences = %#x after restore, want it untouched", got)
	}
}

// TestPrepareTerminals_NothingToAskWhereTerminalsDraw pins the platforms whose
// terminals interpret escape sequences unasked: preparation asks nothing,
// records nothing, and its restore does nothing.
func TestPrepareTerminals_NothingToAskWhereTerminalsDraw(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal, platformConsole and
	// preparedConsoles seams other tests read.
	f := consoleFile(t)
	withTerminals(t, f)
	withConsole(t, nil)

	restore := prepareTerminals(f)
	restore()

	if len(preparedConsoles.ready) != 0 {
		t.Errorf("preparation recorded %d consoles on a platform with none to ask", len(preparedConsoles.ready))
	}
	if got := outputTerminal(f); got != drawingTerminal {
		t.Errorf("outputTerminal = %v, want drawingTerminal", got)
	}
}
