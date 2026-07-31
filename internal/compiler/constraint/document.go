// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

// DocumentSubject is the subject of a check invoked once per document: the one
// document being presented, not a member of a document set.
//
// Its locator is empty. A document is the whole file, so there is no authored
// element inside it for the subject to name — a check raising about a
// particular field inside a document names that field in its detail, and the
// consumer positions at file level.
type DocumentSubject interface {
	Subject
	document()
}

type docSubject struct{ provenance }

func (docSubject) document() {}

// newDocumentSubject takes no segments, unlike every other kind's constructor:
// a document has no authored element inside it for a locator to name, so its
// provenance is the file and nothing else.
func newDocumentSubject(sourceFile string) DocumentSubject {
	return docSubject{newProvenance(sourceFile)}
}

// RunOnDocument runs a check against the single document the caller is holding,
// and returns whatever the check raised.
//
// This is the push-model entry, and it is push because the compiler reads
// documents one at a time and reads the catalog lazily: no point in the
// compilation holds a materialised document set, so there is nothing for the
// framework to walk. The caller reading a document presents it as it arrives;
// the check runs exactly once, against that document.
//
// It takes a [Check] value rather than a bare predicate, exactly as every
// pull-model traversal does. A predicate reaching this entry has therefore
// declared the rule it enforces and what a violation does to the build, and V
// is constrained to [View] so the facts it reads are a declared view rather
// than any type at all. Both halves matter for one reason: the invocation site
// admits declarations only, so there is no route through the framework for a
// check carrying no sentinel and no severity, and the push side holds that
// property on the same terms as the pull side rather than nearly.
//
// view carries the facts the check declared. sourceFile is the document's
// workspace-relative path, and is the whole of the document subject's
// provenance.
func RunOnDocument[V View, C any](sourceFile string, view V, check Check[V, C, DocumentSubject]) []Objection {
	return check.Run(view, newDocumentSubject(sourceFile))
}
