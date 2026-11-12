// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package credentialfailure says that the container-registry credential store
// could not be read, without saying anything the store read.
//
// The store's own text is not safe to print. Reading a malformed entry, it quotes
// the entry's decoded contents — the shape of a pasted token — and reading a file
// that is not valid JSON, it quotes a character of the file. Either would reach a
// terminal, and an editor's output panel, that never asked to see the credential.
// So every site that opens the store or reads from it reports a failure through
// Unreadable, and the sentence the reader sees has this one home.
package credentialfailure

import (
	"errors"
	"fmt"
)

// locationRule names the credential file by where the store looks for it, for a
// failure that happened before the store could say which file it had opened.
const locationRule = "the credential file (config.json in the directory DOCKER_CONFIG names, or " +
	"~/.docker/config.json when that variable is unset)"

// Unreadable returns the failure to read the credential for registry from the
// credential store, in a fixed sentence naming the registry and the credential
// file. cause is the store's own error: it decides what the returned error
// matches and never what it says.
//
// file is the configuration file the store opened, where it opened one. It is
// empty where opening the store is what failed — no file was read far enough to
// be named — and the sentence then names the file by where the store looks for
// it instead.
func Unreadable(registry, file string, cause error) error {
	source := locationRule
	if file != "" {
		source = file
	}
	return &unreadable{
		message: fmt.Sprintf("credential store: the credential for %q cannot be read from %s or the "+
			"credential helper it names; what the file holds is not shown. Check that the file can be read "+
			"and is valid JSON, and that its entry for that registry is well formed — `aggrega login %s` "+
			"replaces the entry", registry, source, registry),
		cause: cause,
	}
}

// unreadable is the failure Unreadable returns.
//
// The store's failure is kept for matching rather than for printing, and that
// decides the methods. Is delegates to it, so a condition the store raised — a
// context already cancelled or expired when a credential helper was to be run, a
// credential helper that is not installed, a file the process may not open —
// matches as it did before the sentence replaced the text. There is deliberately
// no Unwrap: every walk that renders a chain one frame at a time prints each
// frame's own text, and the frames beneath this one are the store's. There is
// deliberately no As either: a match would hand the caller one of those frames,
// whose text is exactly what this type withholds.
type unreadable struct {
	message string
	cause   error
}

// Error returns the fixed sentence. It never includes the store's text.
func (e *unreadable) Error() string { return e.message }

// Is reports whether the store's own failure is, or wraps, target.
func (e *unreadable) Is(target error) bool { return errors.Is(e.cause, target) }
