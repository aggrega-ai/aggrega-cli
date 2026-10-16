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

// The evaluated-elsewhere reason is written as a string literal rather than
// through a constant, because the census that reads it reads it off this
// declaration's own syntax: an identifier naming a constant is a value it
// cannot resolve, and the declaration would go unread.
var errImportHalted = cerrors.Register("import-halting-diagnostic", cerrors.Meta{
	ShortCode: "E0287",
	Message: "the importer reported a diagnostic that stops the import, so the source was not " +
		"fully turned into draft authoring files",
	RulePlace: cerrors.RuleEvaluatedElsewhere("The importer guest the catalog advertises for the source format evaluates what it can and " +
		"cannot translate, and [Result.Refusal] raises this condition when any diagnostic it returned " +
		"is of halt severity. The rule runs inside that guest, over a foreign source description " +
		"rather than over a compilation, so the rule dispatch neither invokes it nor holds a view of " +
		"what it reads."),
})

// ErrAmbiguousImportFormat fires when an import names no format and the
// signatures of more than one format recognise its source.
//
// It is a registered condition because it is a refusal an author acts on and may
// need to match: the import would otherwise have to choose between formats the
// importers each claim, and choosing is a guess. Its occurrence names
// every format the source was recognised as, so the reader can name the one they
// meant.
//
// It is distinct from more than one importer advertising one format, which the
// catalog reports once a format is settled: here the formats themselves differ,
// so no attachment can settle it and only naming the format does.
func ErrAmbiguousImportFormat() error { return errAmbiguousImportFormat }

var errAmbiguousImportFormat = cerrors.Register("ambiguous-import-format", cerrors.Meta{
	ShortCode: "E0339",
	Message:   "more than one importer recognises the source",
	RulePlace: cerrors.RuleNoStageReaches("An import naming no format evaluates the rule, when the signatures the importers " +
		"it resolves among record are matched against the source and those of more than one format match. " +
		"The import translates a foreign source description into entities before there is anything to compile, so " +
		"no declared stage of a compilation holds the signatures or the source the rule reads."),
})

// ErrImporterDeclaresNoFilePatterns fires when an import names a directory and the
// importer serving the source's format declares no file patterns.
//
// It is a registered condition because it is a refusal an author acts on and may
// need to match. A directory import hands its importer exactly the files the
// importer's declared file patterns select, and an importer declaring none has
// said nothing about which files of a directory it reads — so the import stops
// before opening any of them rather than handing it every file the directory holds.
// Its occurrence names the format and the importer, and the fix is the importer
// author's: declaring the patterns its files are named by.
func ErrImporterDeclaresNoFilePatterns() error { return errImporterDeclaresNoFilePatterns }

var errImporterDeclaresNoFilePatterns = cerrors.Register("importer-declares-no-file-patterns", cerrors.Meta{
	ShortCode: "E0341",
	Message: "the importer serving the source's format declares no file patterns, so it reads one file and " +
		"cannot read a directory",
	RulePlace: cerrors.RuleNoStageReaches("An import naming a directory evaluates the rule, once the importer " +
		"serving the source's format is resolved and before any file of the directory is opened. The import " +
		"translates a foreign source description into entities before there is anything to compile, so no " +
		"declared stage of a compilation holds the importer or the directory the rule reads."),
})

// ErrImportDirectoryMatchedNothing fires when an import names a directory and no
// file in it matches the file patterns the importer serving the source's format
// declares.
//
// It is a registered condition because it is a refusal an author acts on and may
// need to match: handing the importer no source at all would reach it as one empty
// source, and it would fail there in words about content rather than about which
// files were read. Its occurrence names the format, the importer and the patterns,
// so the reader can tell whether the directory or the format is the one to change.
func ErrImportDirectoryMatchedNothing() error { return errImportDirectoryMatchedNothing }

var errImportDirectoryMatchedNothing = cerrors.Register("import-directory-matched-nothing", cerrors.Meta{
	ShortCode: "E0342",
	Message:   "no file in the directory matches the file patterns the importer serving the source's format declares",
	RulePlace: cerrors.RuleNoStageReaches("An import naming a directory evaluates the rule, when the file patterns " +
		"the resolved importer declares are matched against the files the directory holds and none matches. The " +
		"import translates a foreign source description into entities before there is anything to compile, so no " +
		"declared stage of a compilation holds the patterns or the directory the rule reads."),
})

// ErrImportSourceNameCollision fires when an import names a directory and a file
// read from it and a file named in the importer's with.sources have the same name.
//
// It is a registered condition because it is a refusal an author acts on and may
// need to match. Both arrive at the importer as named sources, and a name is how an
// importer tells one source from another, so two of one name would leave one of
// them shadowing the other with nothing to say which. Its occurrence names the
// name and the with.sources file.
func ErrImportSourceNameCollision() error { return errImportSourceNameCollision }

var errImportSourceNameCollision = cerrors.Register("import-source-name-collision", cerrors.Meta{
	ShortCode: "E0343",
	Message:   "a file read from the directory and a file named in the importer's `with.sources` have the same name",
	RulePlace: cerrors.RuleNoStageReaches("An import naming a directory evaluates the rule, when the files read " +
		"from the directory and the files the attachment configuring the importer names in its with.sources are " +
		"gathered into the one list of named sources the importer is handed. The import translates a foreign " +
		"source description into entities before there is anything to compile, so no declared stage of a " +
		"compilation holds the sources the rule reads."),
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
