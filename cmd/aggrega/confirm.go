// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// confirmation is the interactive yes/no gate every command guarding an
// irreversible action shares. It carries the three sentences such a gate speaks,
// because those sentences are the whole of what differs between one gate and the
// next: everything else — when consent is implied, what a missing terminal means,
// which answers count as yes — is the same question and has one answer.
//
// Keeping it in one place is not tidiness. A second implementation is free to
// diverge on the part nobody sees while testing by hand, which is what a caller
// with no terminal behind it gets: one that blocks on a prompt nobody can answer
// stalls a pipeline silently, reporting neither success nor failure.
type confirmation struct {
	// question is printed verbatim before the answer is read, and carries its own
	// trailing prompt punctuation so the whole line a user sees is legible at the
	// declaration rather than assembled here.
	question string
	// noTerminal is raised when standard input is not a terminal and consent was
	// not given in advance. It names the flag that gives consent in advance,
	// because that is the only remedy available to a caller that cannot be asked.
	noTerminal string
	// declined is raised when the answer is anything but an explicit yes.
	declined string
}

// ask runs the gate against w and r. With bypass set it consents immediately and
// reads nothing. On a non-interactive standard input without bypass it refuses,
// rather than blocking on a prompt nobody can answer. Otherwise it prints the
// question, reads one line, and consents only on an explicit yes — silence, a
// bare newline and anything unrecognised all decline, so the destructive path
// needs a deliberate answer and the safe path is what a mistake reaches.
func (c confirmation) ask(w io.Writer, r io.Reader, bypass bool) error {
	if bypass {
		return nil
	}
	if !isTerminal() {
		return errors.New(c.noTerminal)
	}
	fmt.Fprint(w, c.question)
	line, _ := bufio.NewReader(r).ReadString('\n')
	switch strings.TrimSpace(line) {
	case "y", "Y", "yes", "Yes", "YES":
		return nil
	default:
		return errors.New(c.declined)
	}
}

// confirmDestructiveRefresh gates a bare dependency refresh — the form that drops
// every recorded pin and re-resolves the whole closure from current registry
// state — behind the shared confirmation.
//
// Only the bare form is gated. A refresh that names its targets holds every pin
// it did not name, so gating it would put a prompt on the safe path, and a prompt
// a user meets when nothing is at stake teaches the habit of dismissing the one
// that guards the form where everything is. The narrowing form is the remedy this
// gate's own refusal points at, so confirming it would also make the remedy as
// expensive as the thing it remedies.
//
// commandLabel prefixes both refusals so a reader who reached this from a build
// is not told about some other command. The question itself is invariant across
// callers: the act being confirmed is one act however it was reached, and a
// reader deciding whether to answer yes is owed the same sentence either way.
func confirmDestructiveRefresh(w io.Writer, r io.Reader, commandLabel string, assumeYes bool) error {
	return confirmation{
		question: "This drops every recorded pin and re-resolves every dependency from current registry state, " +
			"floating each to the highest available version. Proceed? [y/N] ",
		noTerminal: commandLabel + ": refusing to drop every recorded pin on a non-interactive stdin without --yes",
		declined:   commandLabel + ": aborted",
	}.ask(w, r, assumeYes)
}
