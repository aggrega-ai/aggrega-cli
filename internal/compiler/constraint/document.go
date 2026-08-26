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
// This is the push entry for the document itself, and it is push because the
// compiler reads documents one at a time and reads the catalog lazily: no point
// in the compilation holds a materialised document set, so there is nothing for
// the framework to walk. The caller reading a document presents it as it
// arrives; the check runs exactly once, against that document.
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
//
// It takes no [Cancellation], and the asymmetry follows from the push model
// rather than being an omission in it. Every pull traversal reads a cancellation
// signal in its loop because a walk over an enumeration is an interval a caller
// may want to cut short. This runs the check once, against one document, so
// there is no interval to cut: a caller that wants to stop stops before
// presenting the next document. A parameter here would be one this function
// never reads.
//
// No production path invokes it, and it is nonetheless load-bearing, because it
// is the framework's minimal entry and this package's own controls rest on that.
// The programs under testdata that must compile and must not compile differ from
// one another in the subject kind their check declares and in nothing else, which
// is what makes a compile failure there attributable to the kinds refusing to
// substitute rather than to anything around them. Reaching those controls through
// an entry that also takes a cancellation signal and an enumerator would put that
// one difference among several.
func RunOnDocument[V View](sourceFile string, view V, check Check[V, DocumentSubject]) []Objection {
	return check.Run(view, newDocumentSubject(sourceFile))
}

// RunOnDocumentParameters invokes check once per parameter the presented
// document's contract declares, in the order parameters are given.
//
// It is a push entry like [RunOnDocument], and for the same reason: the
// coordinate it is rooted at is the document, which no point in the compilation
// holds a set of. What differs is the coordinate inside it. A document's declared
// parameter names are a set once the document is in hand, so this entry walks
// them — the outer coordinate is presented and the inner one is enumerated,
// which is why the caller hands over the names alongside the file rather than
// implementing an enumerator the framework would key by document.
//
// parameters are the declared parameter names, and the caller presents them in a
// deterministic order it declares — the order the contract declares them in,
// wherever the authored document has one. That obligation is the same one every
// enumerator interface carries, and it lands here because this side of the
// framework has no enumerator to put it on: a check raises in the order it is
// invoked, so this order is the order diagnostics appear in. Nothing here sorts,
// and nothing here derives a name — what arrives is what the check is invoked
// for.
//
// A locator names .spec.parameters.<name>, the site the contract is authored at.
// It is assembled here through the same helper [RunOnNodeParameters] uses, so
// one authored shape has one spelling.
//
// It takes no [Cancellation] and returns no error, which follows from the model
// rather than being an omission in it. A signal is read once per outermost
// coordinate and never at an inner one, so a walk stops within one outer
// iteration; the outermost coordinate here is the document, and the loop over
// documents is the caller's. A caller that wants to stop stops before presenting
// the next document, exactly as it does with [RunOnDocument]. One document's
// declared contract is bounded by that one file.
func RunOnDocumentParameters[V View](
	sourceFile string,
	parameters []string,
	view V,
	check Check[V, DocumentParameterSubject],
) []Objection {
	var out []Objection
	for _, parameter := range parameters {
		subject := newDocumentParameterSubject(
			sourceFile, parameter,
			documentSpecSegments(authoredKeyParameters, parameter)...,
		)
		out = append(out, check.Run(view, subject)...)
	}
	return out
}
