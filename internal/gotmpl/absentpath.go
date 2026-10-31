// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gotmpl

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// ErrAbsentPathReadAndIterated is the Loud Guardrail halt raised when a template
// prints a path as a value, also iterates it, and the data holds nothing at that
// path.
//
// An unset value read on a surface that tolerates one is supplied an empty string,
// so the read prints nothing; an iterated one is left absent, so it iterates as
// nothing. A path the template both prints and iterates can have neither: the empty
// string fails the iteration, and leaving the path absent prints the template
// engine's placeholder text "<no value>" into the artifact while the build
// succeeds. No single value serves both reads, so the build halts naming the path,
// spelled the way the template first wrote it.
//
// It is raised only where the data holds nothing at the path — a path holding a
// value prints and iterates as it always did — and only for a print the template
// reaches every time it runs: an action whose whole pipeline is the reference,
// outside every if, with and range body and every else branch. A print inside such
// a body may sit behind a test of the very path it prints, which no reading of the
// text can decide, so it renders as it does on its own. Category: whichever the
// surface evaluating the template charges. Match with
// errors.Is(err, ErrAbsentPathReadAndIterated()).
func ErrAbsentPathReadAndIterated() error { return errAbsentPathReadAndIterated }

var errAbsentPathReadAndIterated = cerrors.Register("absent-path-read-and-iterated", cerrors.Meta{
	ShortCode: "E0369",
	Message:   "template reads a path as a value and iterates it, and the data holds nothing at that path",
	RulePlace: cerrors.RuleNoStageReaches("A template's evaluation evaluates the rule, at the moment it is about to run, on " +
		"every surface that supplies an unset value for a template — a render and a parameter interpolation. Whether " +
		"the path holds anything is a fact of the data that one evaluation is handed, which is assembled for it alone, " +
		"so no declared stage of a compilation holds it as a fact."),
})

// absentPathReadAndIteratedAdvice is what a halted template is told to do instead.
const absentPathReadAndIteratedAdvice = "supply a value at the path, guard the printed read with an if or with " +
	"over the path, or read it one way only"

// absentPathReadAndIteratedError is one reference the seeding refused to leave
// unsupplied. Its text is the condition's sentence, the path as the template
// spelled it first and the advice; it unwraps to the condition, so the condition's
// codes reach every frame that wraps it.
//
// The path is authored template text and never data, so the error carries nothing
// a value an author declared sensitive could reach.
type absentPathReadAndIteratedError struct {
	path string
}

func (e *absentPathReadAndIteratedError) Error() string {
	return cerrors.ComposeMessage(errAbsentPathReadAndIterated, []string{"path " + e.path}, absentPathReadAndIteratedAdvice)
}

func (e *absentPathReadAndIteratedError) Unwrap() error { return errAbsentPathReadAndIterated }
