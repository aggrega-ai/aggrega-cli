// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"io"
	"os"
	"sync"

	"golang.org/x/term"
)

// terminalShape is what an output stream is, as far as the surfaces that write
// escape sequences — colour and the in-place progress checklist — need to know.
type terminalShape int

const (
	// notATerminal is a stream nobody reads at a terminal: redirected to a file,
	// piped into another program, or captured by a continuous-integration runner.
	notATerminal terminalShape = iota
	// plainTerminal is a terminal whose console does not interpret escape
	// sequences — a Windows console that refused to turn their interpretation on.
	// A person is reading it, so it gets plain lines rather than nothing, and no
	// escape byte reaches it.
	plainTerminal
	// drawingTerminal is a terminal that interprets escape sequences, so colour
	// shows and the progress checklist redraws in place.
	drawingTerminal
)

// outputTerminal decides what w is. It is the one place the colour and progress
// surfaces ask, so the two can never disagree about a stream: a console that
// cannot draw the checklist does not receive colour either.
func outputTerminal(w io.Writer) terminalShape {
	if !fileTerminal(w) {
		return notATerminal
	}
	f, ok := w.(*os.File)
	if !ok {
		// Only a test reaches this branch, since the production fileTerminal
		// answers yes for an *os.File alone. A writer with no console behind it
		// has no console mode that could refuse escape sequences.
		return drawingTerminal
	}
	if !preparedConsoles.interpretsEscapes(f.Fd()) {
		return plainTerminal
	}
	return drawingTerminal
}

// fileTerminal reports whether w is an interactive terminal. It is a
// package-level seam so the auto-detect branch is unit-testable without a real
// TTY; tests replace it. A writer that is not an *os.File (a *bytes.Buffer in
// tests) is never a terminal, and neither is a file or a pipe a stream was
// redirected to.
var fileTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// consoleControl reads and writes a console's mode through the platform's own
// calls. It exists only on a platform whose console interprets escape sequences
// once a program asks it to; elsewhere a terminal interprets them without being
// asked and there is no control at all.
type consoleControl struct {
	getMode func(fd uintptr) (uint32, error)
	setMode func(fd uintptr, mode uint32) error
	// escapes is the mode bit under which the console interprets escape
	// sequences.
	escapes uint32
}

// platformConsole is this platform's console control, or nil where terminals
// interpret escape sequences unasked. It is a package-level seam so a test drives
// every branch of a console that has to be asked — one that refuses, one that
// accepts, one already interpreting them — on any platform.
var platformConsole = nativeConsoleControl()

// enableEscapes asks the console behind fd to interpret escape sequences. It
// reports whether the console now does, and, where it changed the console's
// mode to get there, the function that puts the mode back; a console already
// interpreting them is left as it was. A console whose mode cannot be read or
// written does not interpret them.
func (c *consoleControl) enableEscapes(fd uintptr) (bool, func()) {
	mode, err := c.getMode(fd)
	if err != nil {
		return false, nil
	}
	if mode&c.escapes != 0 {
		return true, nil
	}
	if err := c.setMode(fd, mode|c.escapes); err != nil {
		return false, nil
	}
	return true, func() { _ = c.setMode(fd, mode) }
}

// consoleRegistry records, for each console prepared before the command ran,
// whether it interprets escape sequences.
type consoleRegistry struct {
	mu    sync.Mutex
	ready map[uintptr]bool
}

// preparedConsoles is the record prepareTerminals writes and outputTerminal
// reads.
var preparedConsoles = &consoleRegistry{ready: map[uintptr]bool{}}

// interpretsEscapes reports whether the console behind fd interprets escape
// sequences: always where the platform has no console to ask, and otherwise only
// where preparation turned their interpretation on. A console that was never
// prepared is treated as one that refused, so it is written plain.
func (r *consoleRegistry) interpretsEscapes(fd uintptr) bool {
	if platformConsole == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ready[fd]
}

func (r *consoleRegistry) record(fd uintptr, interprets bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ready[fd] = interprets
}

// prepareTerminals asks the console behind each stream to interpret escape
// sequences before anything is written to it, and returns the function that
// puts every console it changed back in the mode it was found in. A stream that
// is not a terminal is left alone, and so is every stream on a platform whose
// terminals interpret escape sequences unasked. Where a console refuses,
// outputTerminal reports it as a plain terminal, and colour and progress both
// write plain text to it.
//
// The streams are prepared in order and put back in reverse, so two streams
// sharing one console end in the mode the first of them found it in.
func prepareTerminals(streams ...*os.File) (restore func()) {
	control := platformConsole
	if control == nil {
		return func() {}
	}
	var undo []func()
	for _, f := range streams {
		if !fileTerminal(f) {
			continue
		}
		fd := f.Fd()
		interprets, put := control.enableEscapes(fd)
		preparedConsoles.record(fd, interprets)
		if put != nil {
			undo = append(undo, put)
		}
	}
	return func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}
}
