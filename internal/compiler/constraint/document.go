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
// It is the framework's minimal entry, and this package's own controls rest on
// that rather than on anything a caller does with it.
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

// RunOnDocumentAssertions invokes check once per assertion the presented
// document's entity declares, in the order the assertions are given.
//
// It is a push entry like [RunOnDocumentParameters], and for the same reason: the
// coordinate it is rooted at is the document, which no point in the compilation
// holds a set of. What differs is the coordinate inside it. The assertions an
// entity declares are a set once the document and the entities it extends are in
// hand, so this entry walks them — the outer coordinate is presented and the
// inner one is enumerated.
//
// assertions are the assertion names the check is invoked for, and the caller
// presents them in a deterministic order it declares — the chain root's first,
// and within each entity the order its author wrote. That obligation is the same
// one every enumerator interface carries, and it lands here because the push side
// of the framework has no enumerator to put it on: a check raises in the order it
// is invoked, so this order is the order diagnostics appear in. Nothing here
// sorts and nothing here derives a name.
//
// A name is presented once however many entities along the chain declare it,
// because the subject is the assertion rather than one entity's declaration of
// it: a rule about a name shared by two entities has one finding to report and
// one site to send the author to. The multiplicity is a fact the view carries,
// which is where a rule reading it asks.
//
// A locator names .spec.assertions.<name>, the site the assertion is authored at,
// assembled through the same helper every other document-level site uses so one
// authored shape has one spelling.
//
// It takes no [Cancellation] and returns no error, for the reason
// [RunOnDocumentParameters] states: the signal is read once per outermost
// coordinate, the outermost coordinate here is the document, and the loop over
// documents is the caller's.
func RunOnDocumentAssertions[V View](
	sourceFile string,
	assertions []string,
	view V,
	check Check[V, DocumentAssertionSubject],
) []Objection {
	var out []Objection
	for _, assertion := range assertions {
		subject := newDocumentAssertionSubject(
			sourceFile, assertion,
			documentSpecSegments(authoredKeyAssertions, assertion)...,
		)
		out = append(out, check.Run(view, subject)...)
	}
	return out
}

// RunOnDocumentSchemaPillars invokes check once per top-level key the presented
// document's schema block declares, in the order the keys are given.
//
// It is a push entry like [RunOnDocumentParameters], and for the same reason: the
// coordinate it is rooted at is the document, which no point in the compilation
// holds a set of. What differs is the coordinate inside it. The keys a schema
// block declares are a set once the document is in hand, so this entry walks them.
//
// keys are the top-level keys the check is invoked for, and the caller presents
// them in a deterministic order it declares — the order the author wrote them in.
// That obligation is the same one every enumerator interface carries, and it lands
// here because the push side of the framework has no enumerator to put it on: a
// check raises in the order it is invoked, so this order is the order diagnostics
// appear in. Nothing here sorts and nothing here derives a key.
//
// It serves both authored kinds that carry the block, and takes neither as an
// argument. Which kind a key was read from is the document-kind axis, which the view
// carries: a caller presents the document it is holding, and the facts that
// presentation answers are the facts that kind carries. The coordinate is the same
// site in either.
//
// A locator names .spec.schema.<key>, the site the key is authored at, assembled
// through the same helper every other document-level site uses so one authored
// shape has one spelling.
//
// It takes no [Cancellation] and returns no error, for the reason
// [RunOnDocumentParameters] states: the signal is read once per outermost
// coordinate, the outermost coordinate here is the document, and the loop over
// documents is the caller's.
func RunOnDocumentSchemaPillars[V View](
	sourceFile string,
	keys []string,
	view V,
	check Check[V, DocumentSchemaPillarSubject],
) []Objection {
	var out []Objection
	for _, key := range keys {
		subject := newDocumentSchemaPillarSubject(
			sourceFile, key,
			documentSpecSegments(authoredKeySchema, key)...,
		)
		out = append(out, check.Run(view, subject)...)
	}
	return out
}

// RunOnDocumentOutputLabels invokes check once per output-label key one block of
// the presented Recipe declares, in the order the keys are given.
//
// It is a push entry like [RunOnDocumentParameters], and for the same reason: the
// coordinate it is rooted at is the document, which no point in the compilation
// holds a set of. What differs is the coordinate inside it. The keys one block
// declares are a set once the document is in hand, so this entry walks them.
//
// block names which of the Recipe's two label blocks the keys were written in:
// one of [OutputLabelBlockLiteral] or [OutputLabelBlockPromoted]. It is taken
// from the caller rather than inferred, because a presentation answers both
// blocks and only the caller knows which of the two it is walking; it reaches the
// subject as a coordinate and the locator as a segment, from this one argument,
// so the block a rule names and the block a finding is positioned in are the same
// string by construction.
//
// A block is walked in one call rather than both in one, and that is what keeps a
// caller able to order two rules over the same keys: a rule settled across a whole
// block before another rule is asked anything is expressed by invoking each in
// turn, which a single entry walking both could not express.
//
// keys are the keys the check is invoked for, and the caller presents them in a
// deterministic order it declares — the order the fact enumerating that block
// declares, which is the author's where the block carries one and a sorted order
// where it does not. That obligation is the same one every enumerator interface
// carries, and it lands here because the push side of the framework has no
// enumerator to put it on: a check raises in the order it is invoked, so this
// order is the order diagnostics appear in. Nothing here sorts and nothing here
// derives a key.
//
// A locator names .spec.<block>.<key>, the site the key is authored at, assembled
// through the same helper every other document-level site uses so one authored
// shape has one spelling.
//
// 🔴 A label key routinely carries the locator dialect's own separator — a
// reserved key opens with a namespace spelled with dots — so the assembly stops
// at the block for exactly those keys, and the locator names the block rather
// than the entry. That is the dialect's own rule rather than a shortfall here,
// and it is why the key is carried as a coordinate as well: a rule naming the key
// reads it from the subject, where it is the author's text whatever it contains.
//
// It takes no [Cancellation] and returns no error, for the reason
// [RunOnDocumentParameters] states: the signal is read once per outermost
// coordinate, the outermost coordinate here is the document, and the loop over
// documents is the caller's.
func RunOnDocumentOutputLabels[V View](
	sourceFile string,
	block string,
	keys []string,
	view V,
	check Check[V, DocumentOutputLabelSubject],
) []Objection {
	var out []Objection
	for _, key := range keys {
		subject := newDocumentOutputLabelSubject(
			sourceFile, block, key,
			documentSpecSegments(block, key)...,
		)
		out = append(out, check.Run(view, subject)...)
	}
	return out
}

// RunOnDocumentRecipeSourceField invokes check once for the presented Recipe, at
// the position its source entry is written at.
//
// It is a push entry like [RunOnDocument], and for the same reason: the
// coordinate it is rooted at is the document, which no point in the compilation
// holds a set of. What differs is the coordinate inside it, and here nothing is
// enumerated: a Recipe has exactly one position a source entry is written at, so
// the entry is invoked once per document, and it is invoked whether or not the
// author wrote anything there. That is the axis rather than an oversight — a rule
// about an entry left behind with nothing in it has to be invoked where the entry
// would be, and an entry presenting the position only where something was written
// would be invoked no times for exactly the documents such a rule exists to
// refuse, which reports what a rule that ran and approved reports. Whether the
// entry was written is a declared fact over this coordinate, where a reader of the
// rule can see it.
//
// A locator names .spec.source, assembled through the same helper every other
// document-level site uses so one authored shape has one spelling. The key is the
// authoring surface's own and carries no separator, so the locator is exact.
//
// It takes no [Cancellation] and returns no error, for the reason
// [RunOnDocumentParameters] states: the signal is read once per outermost
// coordinate, the outermost coordinate here is the document, and the loop over
// documents is the caller's.
func RunOnDocumentRecipeSourceField[V View](
	sourceFile string,
	view V,
	check Check[V, DocumentRecipeSourceFieldSubject],
) []Objection {
	subject := newDocumentRecipeSourceFieldSubject(
		sourceFile, authoredKeySource,
		documentSpecSegments(authoredKeySource)...,
	)
	return check.Run(view, subject)
}

// RunOnDocumentAnnotationBindings invokes check once per annotation binding the
// presented Manifest's registry declares, in the order the bindings are given.
//
// It is a push entry for the same reason [RunOnDocumentParameters] is, and the
// reason is the outer coordinate rather than anything about a Manifest: the kind
// is rooted at a document, and no point in the compilation holds a materialised
// document set to walk. A Manifest is read one file at a time, so the caller
// holding one presents it. What differs is the coordinate inside it — a
// Manifest's declared binding keys are a set once the document is in hand, so
// this entry walks them.
//
// bindings are the declared annotation keys, and the caller presents them in a
// deterministic order it declares — the order the registry declares them in,
// which is the order the author wrote. That obligation is the same one every
// enumerator interface carries, and it lands here because the push side of the
// framework has no enumerator to put it on: a check raises in the order it is
// invoked, so this order is the order diagnostics appear in. Nothing here sorts
// and nothing here derives a key.
//
// A locator names .spec.mappings.features.annotations.<key>, the site the
// binding is authored at, assembled through the same helper every other
// document-level site uses so one authored shape has one spelling.
//
// 🔴 The locator names the registry's site, which is the one site a Manifest
// declares an annotation binding at, so every presented key is locatable at the
// path this entry assembles. The fact this entry is enumerated from is declared
// over that same site and says so in its own declaration, so the two agree by
// construction rather than by a caller remembering.
//
// It takes no [Cancellation] and returns no error, for the reason
// [RunOnDocumentParameters] states: the signal is read once per outermost
// coordinate, the outermost coordinate here is the document, and the loop over
// documents is the caller's.
func RunOnDocumentAnnotationBindings[V View](
	sourceFile string,
	bindings []string,
	view V,
	check Check[V, DocumentAnnotationBindingSubject],
) []Objection {
	var out []Objection
	for _, binding := range bindings {
		subject := newDocumentAnnotationBindingSubject(
			sourceFile, binding,
			documentSpecSegments(
				authoredKeyMappings, authoredKeyFeatures, authoredKeyAnnotations, binding,
			)...,
		)
		out = append(out, check.Run(view, subject)...)
	}
	return out
}

// RunOnDocumentMappingEntries invokes check once per entry of the presented
// Manifest's recipe mapping table, in the order the entries are given.
//
// It is a push entry for the reason [RunOnDocumentAnnotationBindings] is, and the
// reason is the outer coordinate rather than anything about a Manifest: the kind
// is rooted at a document, and no point in the compilation holds a materialised
// document set to walk. What differs is the coordinate inside it — a Manifest's
// mapping entries are a set once the document is in hand, so this entry walks
// them.
//
// entries are the declared mapping entries, and the caller presents them in the
// deterministic order the fact that enumerates them declares. That obligation is
// the same one every enumerator interface carries, and it lands here because the
// push side of the framework has no enumerator to put it on: a check raises in
// the order it is invoked, so this order is the order diagnostics appear in.
// Nothing here sorts and nothing here derives an entry.
//
// It takes the record the enumerating fact returns rather than three parallel
// lists, and that is single-sourcing rather than convenience. An entry's identity
// is its tier, its axis and its key taken together, so a signature splitting them
// would hand the framework three lists to re-pair by position — the same
// re-derivation the composite fact exists to close, arrived at through the
// traversal instead of through the view.
//
// A locator names .spec.mappings.<tier>.<axis>.<key>, the site the entry is
// authored at, assembled through the same helper every other document-level site
// uses so one authored shape has one spelling. The tier and axis reach the
// locator as the author's own words because that is what they are — the entry's
// coordinates and the file's keys are the same two strings, which is why the
// vocabulary a rule branches on is declared from the authoring keys rather than
// beside them.
//
// It takes no [Cancellation] and returns no error, for the reason
// [RunOnDocumentParameters] states: the signal is read once per outermost
// coordinate, the outermost coordinate here is the document, and the loop over
// documents is the caller's.
func RunOnDocumentMappingEntries[V View](
	sourceFile string,
	entries []ManifestRecipeMapping,
	view V,
	check Check[V, DocumentMappingEntrySubject],
) []Objection {
	var out []Objection
	for _, entry := range entries {
		subject := newDocumentMappingEntrySubject(
			sourceFile, entry.Tier, entry.Axis, entry.Key,
			documentSpecSegments(
				authoredKeyMappings, entry.Tier, entry.Axis, entry.Key,
			)...,
		)
		out = append(out, check.Run(view, subject)...)
	}
	return out
}
