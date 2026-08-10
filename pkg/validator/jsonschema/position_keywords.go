// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"
)

// positionKeywords carries the pre-indexed state for the keyword families
// the walk evaluates at one position beyond the declared-property families
// branchIndex already carries: the array keywords, the key-string and
// key-count constraints, the conditional applicator, closedness, and the
// two constraints that apply to whatever the other keywords did not reach.
//
// It is one structure rather than a field per keyword because the adapter
// and branchIndex carry the same per-position state at two scopes, and a
// keyword wired into one but forgotten in the other is enforced at the top
// level and silently unenforced inside every combinator branch. One
// structure, built by one function and evaluated by one function, cannot be
// half-wired — and the reduced copy of the position reads this same
// structure to decide what it must no longer check, so what the walk
// evaluates and what the library is left to evaluate cannot disagree.
type positionKeywords struct {
	// prefixItems constrains the elements at the head of an array, one
	// branch per position; items constrains every element after them, or
	// every element when no prefixItems is declared.
	prefixItems []*branchIndex
	items       *branchIndex

	// contains and its bounds constrain how many of an array's elements
	// match one shape. minContains is nil when the author wrote none, where
	// the keyword's own default of one applies.
	contains    *branchIndex
	minContains *int
	maxContains *int

	// propertyNames constrains an object's key strings rather than its
	// values, so the walk validates each key as a string in its own right.
	propertyNames *branchIndex

	minProperties *int
	maxProperties *int

	// closed reports that this position accepts only the property names it
	// declares — the form written additionalProperties: false. The walk
	// reports each key that reached no declared property at that key's own
	// path, which is why the reduced copy of the position must not also
	// report them.
	closed bool

	// conditional carries an if/then/else applicator, and is nil both when
	// the position declares none and when the position carries a when:
	// predicate. A predicate replaces the applicator rather than joining
	// it, so the two are never evaluated together.
	conditional *conditionalBranches

	// unevaluatedProperties and unevaluatedItems constrain what no other
	// keyword at this position reached. Each is populated only where that
	// set is computable from this position alone; see
	// evaluatedSetIsLocalToPosition for when it is not, and what is lost.
	unevaluatedProperties *tailConstraint
	unevaluatedItems      *tailConstraint

	// definition names the reusable definition this position was built from,
	// its own or an enclosing one's, and is empty where none was. Every
	// message emitted here carries it, so a rule expressed as a key count or
	// a closed key set names itself exactly as one expressed as a type does.
	definition string
}

// conditionalBranches carries an if/then/else applicator's three branches.
// The if: branch selects which of the other two applies and asserts
// nothing itself.
type conditionalBranches struct {
	ifBranch   *branchIndex
	thenBranch *branchIndex
	elseBranch *branchIndex
}

// tailConstraint is a constraint on whatever a position's other keywords
// did not reach — the keys no declared property or pattern claimed, or an
// array's elements past the declared positions. An author writes it either
// as a refusal (the closed form, `false`) or as a schema the leftover
// values must satisfy, and the two shapes are carried together here so the
// three keywords that both take them cannot drift apart.
type tailConstraint struct {
	closed bool
	branch *branchIndex
}

// buildPositionKeywords indexes the keyword families listed on
// positionKeywords for one schema node, or reports nil when the node
// declares none of them.
//
// Nil is the signal the rest of the adapter keys off: a branch the walk has
// nothing extra to do at stays classified as a leaf and is checked in one
// call, which is both cheaper and — having no map behind it — already
// stable.
func (a *googleAdapter) buildPositionKeywords(s *jsonschema.Schema, ambientDefs map[string]*jsonschema.Schema, enclosing string) *positionKeywords {
	if s == nil {
		return nil
	}

	pk := &positionKeywords{
		minProperties: s.MinProperties,
		maxProperties: s.MaxProperties,
		minContains:   s.MinContains,
		maxContains:   s.MaxContains,
		closed:        isClosedForm(s.AdditionalProperties),
		definition:    enclosing,
		propertyNames: a.buildBranchIndex(s.PropertyNames, ambientDefs, enclosing),
		contains:      a.buildBranchIndex(s.Contains, ambientDefs, enclosing),
	}

	// 🔴 The element keywords are modelled together or not at all, and the
	// reason is that a branch can fail to build — a reference that does not
	// resolve on its own leaves one nil.
	//
	// Two ways a partial model goes wrong, both silent. A positional list
	// assembled by skipping the entries that failed would slide the surviving
	// branches down and check element one against element two's constraint.
	// And modelling either keyword of the pair marks both as walked, so the
	// reduced copy of this position drops them — leaving whichever one failed
	// to build checked by nothing at all, which is the permissive direction no
	// passing document reveals.
	//
	// Leaving the whole family to the library instead costs the per-element
	// path and the complete reporting, which is the loss this position was
	// going to have anyway.
	elementBranches := make([]*branchIndex, 0, len(s.PrefixItems))
	elementsComplete := true
	for _, prefix := range s.PrefixItems {
		branch := a.buildBranchIndex(prefix, ambientDefs, enclosing)
		if branch == nil {
			elementsComplete = false
			break
		}
		elementBranches = append(elementBranches, branch)
	}
	var uniformElements *branchIndex
	if s.Items != nil {
		if uniformElements = a.buildBranchIndex(s.Items, ambientDefs, enclosing); uniformElements == nil {
			elementsComplete = false
		}
	}
	if elementsComplete {
		pk.prefixItems = elementBranches
		pk.items = uniformElements
	}

	// A when: predicate governs the node instead of if/then/else, so the
	// applicator is left to the predicate's own pass and is neither indexed
	// nor pruned here. Evaluating both would apply a constraint the author
	// gated away.
	//
	// Modelled only when every branch the author wrote was built, for the
	// reason the element keywords are: a selector that failed to build would
	// read as "matched" and apply the then: branch unconditionally, and a
	// branch that failed to build would be pruned from the reduced copy and
	// then checked by nothing.
	if s.If != nil && !carriesWhenPredicate(s) {
		selector := a.buildBranchIndex(s.If, ambientDefs, enclosing)
		whenMatched := a.buildBranchIndex(s.Then, ambientDefs, enclosing)
		otherwise := a.buildBranchIndex(s.Else, ambientDefs, enclosing)
		if selector != nil &&
			(s.Then == nil) == (whenMatched == nil) &&
			(s.Else == nil) == (otherwise == nil) {
			pk.conditional = &conditionalBranches{
				ifBranch:   selector,
				thenBranch: whenMatched,
				elseBranch: otherwise,
			}
		}
	}

	if evaluatedSetIsLocalToPosition(s) {
		pk.unevaluatedProperties = a.buildTailConstraint(s.UnevaluatedProperties, ambientDefs, enclosing)
		// An element matching contains counts as reached, so a position
		// carrying both would need the match set to decide the leftover set.
		// Skipping the keyword there under-reports; computing it without the
		// match set would refuse elements contains had accepted.
		if s.Contains == nil {
			pk.unevaluatedItems = a.buildTailConstraint(s.UnevaluatedItems, ambientDefs, enclosing)
		}
	}

	if pk.isEmpty() {
		return nil
	}
	return pk
}

// buildTailConstraint indexes a constraint written either as a refusal or
// as a schema, or reports nil when the author wrote neither.
func (a *googleAdapter) buildTailConstraint(s *jsonschema.Schema, ambientDefs map[string]*jsonschema.Schema, enclosing string) *tailConstraint {
	if s == nil {
		return nil
	}
	if isClosedForm(s) {
		return &tailConstraint{closed: true}
	}
	branch := a.buildBranchIndex(s, ambientDefs, enclosing)
	if branch == nil {
		return nil
	}
	return &tailConstraint{branch: branch}
}

// isEmpty reports whether the walk has nothing to evaluate at this
// position beyond what branchIndex already covers.
func (pk *positionKeywords) isEmpty() bool {
	return len(pk.prefixItems) == 0 &&
		pk.items == nil &&
		pk.contains == nil &&
		pk.propertyNames == nil &&
		pk.minProperties == nil &&
		pk.maxProperties == nil &&
		!pk.closed &&
		pk.conditional == nil &&
		pk.unevaluatedProperties == nil &&
		pk.unevaluatedItems == nil
}

// carriesWhenPredicate reports whether the node's conditional extension
// keyword governs it, in which case its if/then/else is the vestigial
// wrapper emitted for standard consumers rather than a constraint this
// adapter evaluates.
func carriesWhenPredicate(s *jsonschema.Schema) bool {
	if s == nil || s.Extra == nil {
		return false
	}
	_, ok := s.Extra["x-aggrega-when"]
	return ok
}

// evaluatedSetIsLocalToPosition reports whether what counts as reached at
// this position is computable from the position's own keywords.
//
// The two leftover-value keywords are the only ones whose meaning is
// assembled from what the sibling keywords matched, so they are the only
// ones that need this question asked. An applicator that brings its own
// declared properties into the position — a reference, a combinator, a
// conditional, a dependent schema — widens the reached set beyond anything
// this position can see, and a set computed without it would be too small.
// Too small is the direction that invents a failure: values another
// applicator did in fact match would be refused. So where one is present
// the keyword is left unevaluated, which under-reports instead, and the
// coverage contract in this package's documentation states that.
func evaluatedSetIsLocalToPosition(s *jsonschema.Schema) bool {
	if s.UnevaluatedProperties == nil && s.UnevaluatedItems == nil {
		return false
	}
	return s.Ref == "" &&
		s.DynamicRef == "" &&
		len(s.AllOf) == 0 &&
		len(s.AnyOf) == 0 &&
		len(s.OneOf) == 0 &&
		s.Not == nil &&
		s.If == nil &&
		len(s.DependentSchemas) == 0 &&
		len(s.DependencySchemas) == 0
}

// walkPositionKeywords evaluates the indexed keywords against the value at
// path, appending one finding per violation at the path of the value that
// violates.
//
// The keys and elements the caller's own dispatch did not match are passed
// in rather than recomputed, because the precedence rule deciding what
// counts as matched — declared properties before patterns before the
// leftover constraint — lives in that dispatch, and a second implementation
// of it here would agree today and drift later.
func (a *googleAdapter) walkPositionKeywords(pk *positionKeywords, instance any, path string, rpt *Report) {
	if pk == nil {
		return
	}

	switch value := instance.(type) {
	case []any:
		a.walkArrayKeywords(pk, value, path, rpt)
	case map[string]any:
		a.walkKeyKeywords(pk, value, path, rpt)
	}

	if pk.conditional != nil {
		a.walkConditional(pk.conditional, instance, path, rpt)
	}
}

// walkArrayKeywords checks an array's elements against the positional and
// uniform element constraints, then the match-count constraints.
//
// Every element is reached by index, so a report names each failing element
// at its own path. A library walks the elements itself and stops at the
// first failure, which reports one element and leaves the rest of a bad
// list undescribed.
func (a *googleAdapter) walkArrayKeywords(pk *positionKeywords, elements []any, path string, rpt *Report) {
	for index, element := range elements {
		branch := pk.items
		if index < len(pk.prefixItems) {
			branch = pk.prefixItems[index]
		}
		if branch == nil {
			continue
		}
		rpt.Findings = append(rpt.Findings, a.walkBranch(branch, element, elementPath(path, index)).Findings...)
	}

	if pk.contains != nil {
		a.walkContains(pk, elements, path, rpt)
	}

	// An element past the declared positions is reached by nothing when no
	// uniform element constraint was authored.
	if pk.unevaluatedItems != nil && pk.items == nil {
		for index := len(pk.prefixItems); index < len(elements); index++ {
			elementAt := elementPath(path, index)
			if pk.unevaluatedItems.closed {
				rpt.append(Finding{
					FieldPath: elementAt,
					Keyword:   "unevaluatedItems",
					Message:   nameViolatedDefinition(unexpectedElementMessage(len(pk.prefixItems)), pk.definition),
				})
				continue
			}
			rpt.Findings = append(rpt.Findings,
				a.walkBranch(pk.unevaluatedItems.branch, elements[index], elementAt).Findings...)
		}
	}
}

// walkContains counts the elements matching the required shape and reports
// a shortfall or an excess against the authored bounds. A match is an
// element the shape produces no findings for, which is the same test the
// rest of the walk uses.
func (a *googleAdapter) walkContains(pk *positionKeywords, elements []any, path string, rpt *Report) {
	matches := 0
	for index, element := range elements {
		if len(a.walkBranch(pk.contains, element, elementPath(path, index)).Findings) == 0 {
			matches++
		}
	}

	atLeast := 1
	if pk.minContains != nil {
		atLeast = *pk.minContains
	}
	if matches < atLeast {
		rpt.append(Finding{
			FieldPath: path,
			Keyword:   "contains",
			Message:   nameViolatedDefinition(containsShortfallMessage(matches, atLeast, pk.contains.definition), pk.definition),
		})
	}
	if pk.maxContains != nil && matches > *pk.maxContains {
		rpt.append(Finding{
			FieldPath: path,
			Keyword:   "maxContains",
			Message:   nameViolatedDefinition(containsExcessMessage(matches, *pk.maxContains, pk.contains.definition), pk.definition),
		})
	}
}

// walkKeyKeywords checks an object's key count and its key strings. The
// keys no declared property or pattern claimed are handled by
// reportUnmatchedKeys, which the caller's dispatch feeds.
func (a *googleAdapter) walkKeyKeywords(pk *positionKeywords, object map[string]any, path string, rpt *Report) {
	if pk.minProperties != nil && len(object) < *pk.minProperties {
		rpt.append(Finding{
			FieldPath: path,
			Keyword:   "minProperties",
			Message:   nameViolatedDefinition(propertyShortfallMessage(len(object), *pk.minProperties), pk.definition),
		})
	}
	if pk.maxProperties != nil && len(object) > *pk.maxProperties {
		rpt.append(Finding{
			FieldPath: path,
			Keyword:   "maxProperties",
			Message:   nameViolatedDefinition(propertyExcessMessage(len(object), *pk.maxProperties), pk.definition),
		})
	}

	if pk.propertyNames != nil {
		// Keys in ascending order, so an object with two unacceptable names
		// reports both and reports them the same way on every run. A library
		// collects them by iterating the object and names one of them.
		for _, key := range sortedKeys(object) {
			// The key string is what is constrained, not the value it holds,
			// so the key is what the branch is handed.
			//
			// The finding stays at the position rather than moving to the
			// key, and the key is named in the message instead. A path
			// pointing at the key would read as a claim about the value
			// there, which is not what failed — the value may be perfectly
			// good and the name still unacceptable.
			for _, finding := range a.walkBranch(pk.propertyNames, key, path).Findings {
				rpt.append(Finding{
					FieldPath: path,
					Keyword:   "propertyNames",
					Message:   nameViolatedDefinition("property name "+strconv.Quote(key)+" is not allowed here: "+finding.Message, pk.definition),
				})
			}
		}
	}
}

// reportUnmatchedKeys names the keys that reached no declared property and
// no pattern at this position, against whichever leftover constraint the
// author wrote there.
//
// One finding per key, at that key's own path. Two properties follow from
// that and neither is available from a single finding naming them all at
// the enclosing object's path:
//
// The key each finding points at is the one the author typed, so a surface
// resolving a path back to a source position — a terminal naming a line and
// column, an editor drawing a squiggle — lands on the offending key rather
// than on the block containing it. A refused key is also its own mistake:
// two of them are two mistakes, so two findings, not one mistake reported
// twice.
//
// The keys arrive in the order the caller's dispatch produced them, which is
// sorted, so the findings are produced in the same order on every run — the
// ordering a library cannot offer, because it collects the keys by iterating
// the instance.
func (a *googleAdapter) reportUnmatchedKeys(pk *positionKeywords, unmatched []string, object map[string]any, path string, rpt *Report) {
	if pk == nil || len(unmatched) == 0 {
		return
	}

	if pk.closed {
		a.reportRefusedKeys(pk, unmatched, "additionalProperties", path, rpt)
		return
	}

	tail := pk.unevaluatedProperties
	if tail == nil {
		return
	}
	if tail.closed {
		a.reportRefusedKeys(pk, unmatched, "unevaluatedProperties", path, rpt)
		return
	}
	for _, key := range unmatched {
		rpt.Findings = append(rpt.Findings,
			a.walkBranch(tail.branch, object[key], childPath(path, key)).Findings...)
	}
}

// reportRefusedKeys emits one finding per key a position does not accept.
// The two keywords that refuse a key outright — the closed form of
// additionalProperties, and the closed form of the leftover-property
// keyword — differ only in which one the author wrote, so they report
// through here rather than each carrying its own copy of the shape.
func (a *googleAdapter) reportRefusedKeys(pk *positionKeywords, keys []string, keyword, path string, rpt *Report) {
	for _, key := range keys {
		rpt.append(Finding{
			FieldPath: childPath(path, key),
			Keyword:   keyword,
			Message:   nameViolatedDefinition(unexpectedPropertyMessage(key), pk.definition),
		})
	}
}

// walkConditional applies whichever of the then: / else: branches the if:
// branch selects.
//
// The if: branch's own findings are discarded, because it asserts nothing:
// a report naming what the selector rejected would name a constraint the
// author never asked the value to satisfy.
// The selector is never nil here — an applicator whose selector failed to
// build is not indexed at all, precisely so this cannot read a build failure
// as a match.
func (a *googleAdapter) walkConditional(conditional *conditionalBranches, instance any, path string, rpt *Report) {
	selected := conditional.elseBranch
	if len(a.walkBranch(conditional.ifBranch, instance, path).Findings) == 0 {
		selected = conditional.thenBranch
	}
	if selected == nil {
		// The author wrote no branch for this outcome, so nothing constrains
		// the value here.
		return
	}
	rpt.Findings = append(rpt.Findings, a.walkBranch(selected, instance, path).Findings...)
}

// unexpectedPropertyMessage names a key a position does not accept.
//
// The key is named in the message as well as carried in the finding's path,
// which reads as a repetition on a surface that renders both. It is the same
// shape the missing-property message uses, and it is what makes the message
// stand on its own: a surface may render the message without the path — an
// editor diagnostic does, because the squiggle it draws is the path — and a
// message that named no key would leave such a reader with nothing to fix.
func unexpectedPropertyMessage(key string) string {
	return "unexpected property: " + strconv.Quote(key)
}

// unexpectedElementMessage describes an array entry past the last position
// the schema declares a constraint for.
func unexpectedElementMessage(declared int) string {
	if declared == 1 {
		return "unexpected element: only 1 element position is declared here"
	}
	return "unexpected element: only " + strconv.Itoa(declared) + " element positions are declared here"
}

// propertyShortfallMessage and propertyExcessMessage describe an object whose
// key count falls outside an authored bound.
//
// The two are written out rather than shared behind a "bound kind" argument
// because the floor states a requirement and the ceiling states a permission,
// and one phrasing covering both reads as the wrong one at one end: "at least
// two properties are allowed" tells an author they may write two when it
// means they must.
func propertyShortfallMessage(actual, atLeast int) string {
	return "object has " + countOf(actual, "property", "properties") +
		"; at least " + strconv.Itoa(atLeast) + " required"
}

func propertyExcessMessage(actual, atMost int) string {
	return "object has " + countOf(actual, "property", "properties") +
		"; at most " + strconv.Itoa(atMost) + " allowed"
}

// containsShortfallMessage describes an array with too few matching
// elements, naming the required shape where it has a name to give.
func containsShortfallMessage(matches, atLeast int, definition string) string {
	shape := "the required shape"
	if definition != "" {
		shape = strconv.Quote(definition)
	}
	if atLeast == 1 {
		return "no element matches " + shape
	}
	return "only " + countOf(matches, "element", "elements") + " " + matchesVerb(matches) + " " + shape +
		"; at least " + strconv.Itoa(atLeast) + " must"
}

// containsExcessMessage describes an array with too many matching elements.
func containsExcessMessage(matches, atMost int, definition string) string {
	shape := "the restricted shape"
	if definition != "" {
		shape = strconv.Quote(definition)
	}
	return countOf(matches, "element", "elements") + " " + matchesVerb(matches) + " " + shape +
		"; at most " + strconv.Itoa(atMost) + " may"
}

// countOf renders a count with the correct form of its noun.
func countOf(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(n) + " " + plural
}

// matchesVerb gives the form of "match" agreeing with a count.
func matchesVerb(n int) string {
	if n == 1 {
		return "matches"
	}
	return "match"
}

// elementPath names an array element's position beneath path, in the
// bracketed form this package's finding paths already use for array
// indices.
func elementPath(path string, index int) string {
	return path + "[" + strconv.Itoa(index) + "]"
}
