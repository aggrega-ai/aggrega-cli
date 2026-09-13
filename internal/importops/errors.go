// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrImportHalted fires when the importer reports a diagnostic that stops the
// import, so the source was not fully turned into draft authoring files.
//
// It is a registered condition because it is a refusal an author acts on and may
// need to match: the import produced a partial answer deliberately, and a caller
// deciding whether to keep what it produced has to be able to tell this refusal
// from the import having failed outright.
//
// It names the outcome rather than the cause. What the importer could not
// translate is in the diagnostics themselves, one per unresolved construct, and
// those are reported in the importer's own words — so this condition says the
// import stopped and the diagnostics beside it say why.
func ErrImportHalted() error { return errImportHalted }

var errImportHalted = cerrors.Register("import-halting-diagnostic", cerrors.Meta{
	ShortCode: "E0287",
	Message: "the importer reported a diagnostic that stops the import, so the source was not " +
		"fully turned into draft authoring files",
})

// Refusal returns the occurrence of [ErrImportHalted] when the import raised a
// halt-severity diagnostic, and nil when it did not.
//
// It is the outcome every surface reads, which is why it is here rather than at
// each of them: the command line's exit status and a protocol answer's field
// cannot come to disagree about whether one import completed. It is built on
// [Result.Halted] rather than re-scanning the severities, so the predicate has
// one home too.
//
// The occurrence carries no concern in its head, and that is a decision rather
// than an omission. A concern answers "which authoring surface do I fix", and
// what the reader fixes here is the foreign source they handed in — which is not
// one of the authoring surfaces the compiler files faults against. The condition
// alone names what happened, and the diagnostics reported beside it name what in
// the source could not be translated.
func (r Result) Refusal() error {
	if !r.Halted() {
		return nil
	}
	return cerrors.Raise(errImportHalted, "", cerrors.SourceLocation{},
		cerrors.ComposeMessage(errImportHalted, nil,
			"The diagnostics reported above name what the importer could not translate."),
		nil)
}
