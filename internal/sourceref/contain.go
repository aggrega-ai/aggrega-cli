// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourceref

import (
	"fmt"
	"path/filepath"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// ErrContentOutsideEntityDirectory is the loud halt raised over a path an entity
// declares for content it carries — a local source, or a file an `$import:`
// names — that does not stay inside the directory holding the entity's own
// document.
//
// Everything a publication carries for the entity is taken from that directory,
// so it is the one place content the entity carries can be read from and still
// travel with it. A path that
// climbs out of it, or names an absolute location, points at a file that would
// not be there for anybody who received the entity, and a digest recorded over
// such a file vouches for bytes nobody else can obtain.
//
// It is raised wherever such a path is joined to that directory, which is every
// moment the path is read: as the entity's document is parsed, which every
// catalog read, pull, build and publication of it goes through, and at render
// for a source written as a template, once the template has produced the path
// it names. The verdict is lexical and is the one the containment predicate
// gives: an interior climb that stays inside ("charts/../values.yaml") is
// accepted, and a climb that leaves the directory is refused even where it names
// the directory again by name on the way back, because that name is not part of
// what travels.
//
// A path that names another entity rather than content the entity carries is not
// judged here: a Release's references to its Aggregate, its Manifest and the
// Release it extends, and an Aggregate's local type reference, are resolved on
// their own terms. Neither is a Release's `parameters` import, which reads a file
// for a document that is never published.
//
// Category: CATALOG_ERROR — the fault is in a path the entity's author wrote.
// Match with errors.Is(err, ErrContentOutsideEntityDirectory()).
func ErrContentOutsideEntityDirectory() error { return errContentOutsideEntityDirectory }

var errContentOutsideEntityDirectory = cerrors.Register("content-outside-entity-directory", cerrors.Meta{
	ShortCode: "E0304",
	Message:   "a path names content outside the entity's own directory",
	RulePlace: cerrors.RuleNoStageReaches("The rule is evaluated where an entity's content path is joined to the " +
		"directory holding its document: as the document is parsed on a catalog read, a pull, a publication or " +
		"the parse that opens a build, none of which is a declared stage of a compilation, and, for a source " +
		"written as a template, at the moment the render has produced the path. The first moment holds every " +
		"path but a templated source's, and no declared stage holds the entity's document path the rule is " +
		"judged against."),
})

// contentRemediation is what the refusal tells its reader, in the words the
// product's documentation teaches the rule in. It names the directory and what
// travels with it, because that is the whole reason for the rule and the one
// thing an author needs in order to see that the fix is to move the file rather
// than to adjust the path.
const contentRemediation = "remediation: move the file into the directory holding the entity, or a directory " +
	"beneath it, and name it with a path relative to that directory. An entity's content lives in the entity's " +
	"own directory because every file a publication of it carries is taken from there; a path that leaves it " +
	"points at a file that would not travel with the entity"

// AnchorContent resolves a path an entity declares for content it carries against
// the directory of the document that declared it, and refuses one that does not
// stay inside that directory.
//
// It is the one statement of both halves of that rule — what such a path is
// relative to, and that it may not leave it — for every surface that joins one:
// a local source, and a file an `$import:` names. The containment verdict is the
// containment predicate's own, reached rather than restated, so a path this
// accepts is one that predicate calls inside and a path it refuses is one that
// predicate calls outside. What this adds is the condition and the sentence the
// refusal is reported in, which the predicate deliberately does not carry.
//
// On success the path comes back absolute, so it reads the same whatever
// directory the build runs from. An empty path names nothing and comes back as
// it is. An absolute path is refused: it names a location of its own, on the
// machine it was written on, rather than a place inside the entity.
//
// An empty documentPath has no directory of its own, and the path is judged
// against the directory the process runs in. The verdict does not depend on
// which directory that is — an absolute path and a climb out are refused, and
// anything else is accepted, whatever root is named — so such a caller is held
// to the same rule as every other.
//
// The refusal is positioned at the declaring document. Callers wrap it with the
// field it was read from, which the condition's own sentence cannot name.
func AnchorContent(documentPath, authored string) (string, error) {
	if authored == "" {
		return authored, nil
	}
	joined, err := containInDocumentDirectory(documentPath, authored)
	if err != nil {
		return "", err
	}
	return filepath.Abs(joined)
}

// containInDocumentDirectory is the verdict both joins share: the cleaned join of
// the document's directory and the authored path, or the registered refusal.
func containInDocumentDirectory(documentPath, authored string) (string, error) {
	joined, ok := pathscope.Resolve(filepath.Dir(documentPath), authored)
	if !ok {
		return "", contentOutsideEntityDirectory(documentPath, authored)
	}
	return joined, nil
}

// contentOutsideEntityDirectory composes the refusal: the registered sentence,
// the path the author wrote, and the remediation.
func contentOutsideEntityDirectory(documentPath, authored string) error {
	condition := ErrContentOutsideEntityDirectory()
	return cerrors.Raise(errContentOutsideEntityDirectory, cerrors.CatalogError,
		cerrors.SourceLocation{File: documentPath},
		cerrors.ComposeMessage(condition, []string{fmt.Sprintf("%q", authored)}, contentRemediation), nil)
}
