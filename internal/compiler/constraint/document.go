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

// RunOnDocument runs a check against the single document the caller is holding,
// and returns whatever the check raised.
//
// This is the push-model entry, and it is push because the compiler reads
// documents one at a time and reads the catalog lazily: no point in the
// compilation holds a materialised document set, so there is nothing for the
// framework to walk. The caller reading a document presents it as it arrives;
// the check runs exactly once, against that document.
//
// view carries the facts the check declared. sourceFile is the document's
// workspace-relative path, and is the whole of the document subject's
// provenance.
func RunOnDocument[V any](sourceFile string, view V, run func(V, DocumentSubject) []Objection) []Objection {
	return run(view, docSubject{provenance{
		file:    sourceFile,
		locator: authoredLocator(),
	}})
}
