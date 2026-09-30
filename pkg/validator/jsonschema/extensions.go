// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/pkg/truthy"
)

// Validate implements the Validator interface. The algorithm:
//
//  1. The instance root's own constraints, then a deterministic property
//     walk. The root is checked first and separately, because the walk
//     covers a position from the level above it and the root has no level
//     above it; without that check a root's type, property-count bounds,
//     propertyNames, enum, const and dependentRequired apply to nothing,
//     and a scalar or sequence document satisfies an object schema in
//     full. Then recursively iterate the instance's
//     keys in sorted order; at each property, check the sub-instance
//     against the constraints belonging to that position — the schema
//     there reduced to its own keywords, with its declared property
//     values pruned away (see pruneOwnKeywords) — and emit a Finding on
//     failure. Check the schema's Required slice at each level and emit
//     one Finding per missing required key in sorted order. Recurse into
//     nested objects, into the patternProperties and additionalProperties
//     branches, into an array's elements by index, and into the branch a
//     conditional applicator selects, so every position is reached by the
//     walk rather than by the library's randomised iteration over a
//     schema's or an instance's map. The keyword families the walk reaches
//     are listed in this package's documentation, along with the ones it
//     leaves to the library and what that costs.
//
//  2. Walk the pre-collected x-aggrega-when: nodes shallowest-first.
//     For each:
//     - Navigate to the node's instance position.
//     - Evaluate the pre-compiled template against the parent of the
//     when-node (the surrounding object scope where sibling fields
//     live; templates reference siblings via $.fieldname).
//     - On truthy: keep the walk's findings for this subtree (the walk
//     has already validated against the sub-schema rooted at the
//     subtree, which includes any then: constraints in the
//     compact-form-emitted shape).
//     - On falsy + else: present: drop the walk's findings for this
//     subtree and validate the sub-instance against the pre-resolved
//     else: branch (findings rebased to the sub-instance's path).
//     - On falsy + no else: present: drop the walk's findings for this
//     subtree (no constraint applies when falsy and no else: was
//     authored).
//     Subsequent x-aggrega-when: nodes nested under a falsy outer
//     branch are skipped (consumed by the outer's filter).
//
//  3. Walk the pre-collected x-aggrega-validate: nodes. For each:
//     - Navigate to the node's instance position.
//     - Evaluate the pre-compiled template against the field value.
//     - On falsy: append a Finding carrying the x-aggrega-validateMessage:
//     content.
//
//  4. Where the validator withholds sensitive values, compose every finding
//     at or inside a position whose value the schema declares sensitive from
//     the schema rather than from the value (see WithholdSensitiveValues).
//     Then sort the findings (FieldPath -> Keyword -> Message) per the
//     deterministic-output invariant.
func (a *googleAdapter) Validate(instance any, ctx *Context) (*Report, error) {
	rpt := &Report{}

	// Resolved parameter map exposed to predicate templates via the param /
	// parameters functions (nil when no Context or no parameters — the
	// functions then return the zero value / empty map).
	var params map[string]any
	if ctx != nil {
		params = ctx.Parameters
	}

	// Step 1: the instance root's own constraints, then the deterministic
	// property walk.
	//
	// The root check is made here because the walk cannot make it. A
	// position's own keywords are checked by its PARENT's per-key dispatch,
	// which validates the child value before recursing into it — so every
	// position is covered by the level above it, and the root, having no
	// level above it, is covered by nothing. Its keywords are indexed like
	// any other position's and then read by no one.
	//
	// What that silently loses is the whole root keyword set — its type
	// above all, but equally its property-count bounds, its propertyNames,
	// its enum and const, its dependentRequired. A document whose root is a
	// scalar or a sequence therefore satisfies an object schema completely:
	// nothing errors and nothing warns, because every remaining check either
	// iterates an instance that is not a map or descends into properties
	// that do not exist. That failure is silent and in the permissive
	// direction, which is the direction no passing document can reveal.
	//
	// Two root families already survive without this, and they are the
	// reason the gap reads as narrower than it is: required is checked in
	// the walk's own body, and closedness is checked by the per-key dispatch
	// keyed on the parent path, which at the root is the root itself.
	//
	// The check is the same one every other position receives, rather than a
	// root-specific type test. A second implementation would agree with
	// pruneOwnKeywords on the day it was written and drift from it after.
	//
	// No definition name is passed, and that is the one place this check
	// differs from the per-property one. Naming the definition exists to
	// repair a label that is wrong at a sub-position: a schema library calls
	// whatever it was handed "root", so a definition substituted for a
	// reference deep in the document gets described as the root when it is
	// nothing of the kind. At the instance root the two coincide — the
	// library's "root" is the document's root — so the label is already
	// accurate, and rewriting it would make one set of constraints report
	// differently depending on whether the author wrote them at the root or
	// behind a reference to them. Those two documents say the same thing, so
	// they must fail the same way.
	if err := a.validateOwnKeywords("", instance); err != nil {
		rpt.append(a.libraryFinding(err, a.ownResolved(""), instance, "", ""))
	}
	a.checkFormatAt("", instance, rpt, "")

	// Checks each position against the constraints declared at that
	// position, in sorted-key order, and checks required at each level.
	// Sidesteps the library's fail-fast-with-randomised-iteration semantic
	// so a report naming several violations names the same ones, in the same
	// order, on every run.
	a.deterministicWalk(instance, "", rpt)

	// The positions the walk cannot reach, checked against the whole
	// document because that is the only scope their references resolve in.
	// Placed here so that a subtree switched off by a falsy predicate drops
	// these findings along with the walk's, rather than keeping a finding
	// for a constraint the author gated away.
	a.checkUnresolvedPositions(instance, rpt)

	// Predicate-inert mode: the caller lacks the correct parameter context for
	// the conditional extensions, so do not evaluate them. The deterministic
	// walk (Step 1) already validated the unconditional structural constraints;
	// here every when-gated subtree is dropped as not-applicable (the same
	// drop the falsy branch performs, but without evaluating the predicate —
	// so a conditional constraint neither fires unconditionally nor is checked
	// against a wrong context), and x-aggrega-validate is skipped entirely.
	if ctx != nil && ctx.SkipPredicates {
		for _, wn := range a.whenNodes {
			rpt.Findings = filterOutPath(rpt.Findings, joinPath(wn.instancePath))
		}
		a.withholdIndexed(rpt)
		rpt.sort()
		return rpt, nil
	}

	// Step 2: x-aggrega-when: filter pass.
	// Process shallowest-first so an outer falsy predicate consumes
	// any nested predicates inside its (no-longer-applicable) then:
	// subtree before they are evaluated.
	handled := []string{}
nextWhen:
	for _, wn := range a.whenNodes {
		whenPathStr := joinPath(wn.instancePath)
		for _, consumed := range handled {
			if isUnderPath(whenPathStr, consumed) {
				continue nextWhen
			}
		}

		subInstance, ok := navigate(instance, wn.instancePath)
		// If the instance does not extend to the when: node's position,
		// the predicate cannot evaluate against a meaningful sub-instance.
		// Skip the subtree: any walk-findings for this path were already
		// emitted by the deterministic walk and remain accurate (the
		// missing-required check handles "field not present").
		if !ok {
			continue
		}

		// For x-aggrega-when:, the template's data scope is the parent
		// of the when-node — the surrounding object where sibling
		// fields live; an `$.architecture` style predicate reads a
		// sibling of the when-bearing field. For a root-level
		// when-node (empty instancePath), parent = instance.
		parentInstance := instance
		if len(wn.instancePath) > 0 {
			parentPath := wn.instancePath[:len(wn.instancePath)-1]
			if p, ok := navigate(instance, parentPath); ok {
				parentInstance = p
			}
		}

		truthy, err := evaluateTemplate(wn.tpl, parentInstance, params)
		if err != nil {
			return nil, &AdapterError{
				Path:    whenPathStr,
				Keyword: "x-aggrega-when",
				Message: "template evaluation: " + err.Error(),
				Err:     ErrExtensionEval(),
			}
		}

		if truthy {
			// Truthy: keep the deterministic walk's findings for this
			// subtree — they correctly reflect the gated then:
			// constraints applying (the walk validated against the
			// sub-schema rooted at the subtree).
			continue
		}

		// Falsy: drop the walk's findings under this subtree.
		rpt.Findings = filterOutPath(rpt.Findings, whenPathStr)

		// If else: was authored, validate against it.
		if wn.elseResolved != nil {
			if err := wn.elseResolved.Validate(subInstance); err != nil {
				start := len(rpt.Findings)
				a.libraryErrorToFindingsWithPathPrefix(rpt, err, whenPathStr, subInstance)
				// The branch is checked whole, so no position inside it was built
				// to carry a declaration; the failure's own location chain is read
				// for one instead.
				if a.withholdSensitive {
					if node, sensitive := chainDeclaresSensitive(wn.elseResolved.Schema(), schemaLocations(err)); sensitive {
						for i := start; i < len(rpt.Findings); i++ {
							rpt.Findings[i] = withheldFinding(rpt.Findings[i], node, "")
						}
					}
				}
			}
			a.sweepSchema(wn.elseSchema, subInstance, whenPathStr, "", rpt)
		}

		// Record this path as consumed; nested when: nodes inside it
		// are no longer relevant.
		handled = append(handled, whenPathStr)
	}

	// Step 3: x-aggrega-validate: evaluation.
nextValidate:
	for _, vn := range a.validateNodes {
		vnPathStr := joinPath(vn.instancePath)
		for _, consumed := range handled {
			if isUnderPath(vnPathStr, consumed) {
				continue nextValidate
			}
		}

		fieldValue, ok := navigate(instance, vn.instancePath)
		if !ok {
			// Field not present in the instance. The deterministic
			// walk's required-key check (Step 1) reports missing
			// required fields; no separate x-aggrega-validate finding
			// here.
			continue
		}

		truthy, err := evaluateTemplate(vn.tpl, fieldValue, params)
		if err != nil {
			return nil, &AdapterError{
				Path:    vnPathStr,
				Keyword: "x-aggrega-validate",
				Message: "template evaluation: " + err.Error(),
				Err:     ErrExtensionEval(),
			}
		}

		if !truthy {
			rpt.append(Finding{
				FieldPath: vnPathStr,
				Keyword:   "x-aggrega-validate",
				Message:   vn.message,
			})
		}
	}

	// Step 4: a value the schema declares sensitive is withheld from every
	// finding at or inside its position, then the deterministic sort.
	a.withholdIndexed(rpt)
	rpt.sort()
	return rpt, nil
}

// deterministicWalk recursively checks each property in the instance
// against the constraints declared at that property's position, in
// sorted-key order, plus checks required-property presence at each level.
// Sidesteps the library's randomised property iteration, so a report
// naming several violations names the same ones in the same order on
// every run.
//
// The walk emits findings with FieldPath at the property's instance path.
// Because the check at a position covers that position only, a failing
// value is named once, at its own path, instead of once at its own path
// and again — chosen arbitrarily among its siblings — at each ancestor.
//
// A position's own keywords are checked here from the level above it, as
// part of the per-key dispatch, and never for the position the walk is
// currently at. The instance root therefore falls outside this walk
// entirely and is checked by the caller before the walk begins. Do not add
// a self-check here to cover it: every non-root position already has one
// from its parent, so a self-check would report each of them twice.
//
// Per-key dispatch follows the precedence rule the walker enforces:
// Properties > PatternProperties > AdditionalProperties. A key declared
// in Properties validates against the Property's sub-schema. A key
// matching one or more patternProperties validates against each
// matching pattern's sub-schema via walkBranch. A key in neither falls
// through to additionalProperties (if declared as a schema; the
// boolean form is enforced via Properties shape alone).
//
// Cross-property constraints (allOf, anyOf, oneOf, not, dependentSchemas)
// evaluate at the current path after the per-key dispatch, each through
// its own recursive branch walk. The library's instance-root Validate is
// never invoked for these constructs, so their findings are emitted in a
// fixed order across the JSON Schema 2020-12 surface this adapter
// covers.
func (a *googleAdapter) deterministicWalk(instance any, path string, rpt *Report) {
	// Required-property check at this level: emit one finding per
	// missing key, in sorted order, BEFORE recursing into present
	// children.
	if req, ok := a.required[path]; ok {
		if m, ok := instance.(map[string]any); ok {
			for _, key := range req {
				if _, present := m[key]; !present {
					rpt.append(Finding{
						FieldPath: childPath(path, key),
						Keyword:   "required",
						Message:   nameViolatedDefinition("missing required property: "+key, a.subDefinition[path]),
					})
				}
			}
		}
	}

	if m, ok := instance.(map[string]any); ok {
		// Keys reaching no declared property and no pattern, in the sorted
		// order this loop visits them, so whatever names them names them the
		// same way on every run.
		var unmatched []string

		// Per-key dispatch with the walker's precedence rule:
		// Properties > PatternProperties > AdditionalProperties.
		for _, k := range sortedKeys(m) {
			subPath := childPath(path, k)

			// Arm 1: declared in Properties. Validated against the
			// position's own constraints, with its declared property values
			// pruned away — the recursion on the next line reaches those in
			// sorted key order, whereas one library call over the whole
			// subtree reports whichever equally-failing child its map
			// iteration happened to reach first.
			if _, hasSub := a.subResolved[subPath]; hasSub {
				if err := a.validateOwnKeywords(subPath, m[k]); err != nil {
					rpt.append(a.libraryFinding(err, a.ownResolved(subPath), m[k], subPath, a.subDefinition[subPath]))
				}
				a.checkFormatAt(subPath, m[k], rpt, a.subDefinition[subPath])
				a.deterministicWalk(m[k], subPath, rpt)
				continue
			}

			// Also declared, but as a position that could not be stood up on
			// its own. It is still a declared property, so it must not fall
			// through the arms below: a key the author was invited to write
			// would otherwise be refused by a closed parent as one it never
			// declared — a refusal naming the wrong problem, on a document
			// that may have nothing wrong with it at all. Its constraints are
			// checked against the whole document instead, which is the only
			// scope its reference resolves in. That check is the library's, which
			// ignores format, so the position's schema is swept for it here.
			if a.isUnresolvedPosition(subPath) {
				a.sweepSchema(a.unresolvedNodes[subPath], m[k], subPath, a.subDefinition[subPath], rpt)
				continue
			}

			// Arm 2: matches one or more patternProperties — validate against EACH match.
			if patterns, hasPP := a.patternPropsResolved[path]; hasPP {
				matched := false
				for _, pe := range patterns {
					if pe.pattern.MatchString(k) {
						matched = true
						branchRpt := a.walkBranch(pe.branch, m[k], subPath)
						rpt.Findings = append(rpt.Findings, branchRpt.Findings...)
					}
				}
				if matched {
					continue
				}
			}

			// Arm 3: undeclared key falls through to additionalProperties.
			if apBranch, ok := a.additionalPropsBranch[path]; ok {
				branchRpt := a.walkBranch(apBranch, m[k], subPath)
				rpt.Findings = append(rpt.Findings, branchRpt.Findings...)
				continue
			}

			unmatched = append(unmatched, k)
		}

		a.reportUnmatchedKeys(a.keywords[path], unmatched, m, path, rpt)
	}

	// The keyword families beyond the declared-property ones: the array
	// keywords, the key-string and key-count constraints, and the
	// conditional applicator.
	a.walkPositionKeywords(a.keywords[path], instance, path, rpt)

	// Combinator evaluation at this path, each through its own branch walk.
	if branches, ok := a.oneOfBranches[path]; ok {
		evaluateOneOf(a, branches, instance, path, rpt)
	}
	if branches, ok := a.allOfBranches[path]; ok {
		evaluateAllOf(a, branches, instance, path, rpt)
	}
	if branches, ok := a.anyOfBranches[path]; ok {
		evaluateAnyOf(a, branches, instance, path, rpt)
	}
	if notBranch, ok := a.notIndexes[path]; ok {
		evaluateNot(a, notBranch, instance, path, rpt)
	}

	// DependentSchemas evaluation at this path.
	if deps, ok := a.dependentSchemas[path]; ok {
		if m, ok := instance.(map[string]any); ok {
			for _, dep := range deps {
				if _, present := m[dep.triggerKey]; !present {
					continue
				}
				branchRpt := a.walkBranch(dep.branch, instance, path)
				for _, f := range branchRpt.Findings {
					// Re-keyed rather than rebuilt, so what the branch recorded about
					// the finding travels with it.
					f.Keyword = "dependentSchemas/" + dep.triggerKey + "/" + f.Keyword
					rpt.append(f)
				}
			}
		}
	}
}

// validateOwnKeywords checks the sub-instance at path against the
// constraints belonging to that position — its type, its bounds, its
// declared property names, its closedness, its array keywords — without
// descending into the declared property values, which the walk reaches
// itself.
//
// Falls back to the position's full schema when no reduced one was built,
// which keeps the coverage that would otherwise be lost at the cost of
// the ordering guarantee for that one position;
// TestEveryIndexedPathHasAPrunedSchema pins the fallback unreached for
// the shipped schemas.
func (a *googleAdapter) validateOwnKeywords(path string, instance any) error {
	if own := a.ownResolved(path); own != nil {
		return own.Validate(instance)
	}
	return nil
}

// ownResolved is the schema a position's own keywords are checked against — its
// reduced copy, or its full schema where no reduced one was built — or nil for a
// path the index holds neither for. It is the one statement of that choice, read
// by the check itself and by what reads a failure of the check back against the
// schema the check was made with.
func (a *googleAdapter) ownResolved(path string) *jsonschema.Resolved {
	if pruned, ok := a.subPruned[path]; ok {
		return pruned
	}
	if resolved, ok := a.subResolved[path]; ok {
		return resolved
	}
	return nil
}

// libraryFinding is the finding a schema library's failure produces at path,
// shaped as findingMessage shapes every library sentence.
//
// called is the schema the failing call was made with. Where the validator
// withholds sensitive values and the failure's location chain passes through a
// position called declares sensitive, the sentence is composed from the schema
// instead. The chain is read rather than the position the finding lands on
// because a call can check more than the position: the schema a reference names
// where the reference could not stand in for the position is checked whole, and a
// value it declares sensitive is reported at the position holding the reference.
func (a *googleAdapter) libraryFinding(err error, called *jsonschema.Resolved, instance any, path, definition string) Finding {
	f := Finding{
		FieldPath: path,
		Keyword:   "validation",
		Message:   findingMessage(err, instance, definition),
	}
	if a.withholdSensitive && called != nil {
		if node, sensitive := chainDeclaresSensitive(called.Schema(), schemaLocations(err)); sensitive {
			f = withheldFinding(f, node, definition)
		}
	}
	return f
}

// walkBranch validates an instance against a pre-indexed branch, as
// walkBranchPosition describes, and then — where the validator withholds
// sensitive values and the branch's position holds one — composes every
// finding at or inside the position from the schema instead of from the
// value. Every position a branch stands for is reached through here, the
// element of an array and a combinator's branch alike, so none of them can
// report a sensitive value by being reached some other way.
func (a *googleAdapter) walkBranch(bi *branchIndex, instance any, path string) *Report {
	rpt := a.walkBranchPosition(bi, instance, path)
	if a.withholdSensitive && bi != nil && bi.sensitive != nil {
		withhold(rpt.Findings, path, bi.sensitive, bi.definition)
	}
	return rpt
}

// walkBranchPosition validates an instance against a pre-indexed branch
// using the same deterministic property-walk discipline as the top-level
// walker. Reads from the branchIndex rather than googleAdapter's
// top-level maps. Recursion through nested combinators is bounded by
// schema depth.
//
// A leaf branch (no properties, no nested combinators, no
// patternProperties, no dependentSchemas, no additionalProperties) is
// validated directly: its constraints produce one stable message per
// call with no map iteration behind them. A compound branch is validated
// against its own constraints only — with its declared property values
// pruned away — and each property is then walked, so a branch with
// several equally-failing children reports all of them in sorted key
// order rather than one chosen by map-iteration order.
func (a *googleAdapter) walkBranchPosition(bi *branchIndex, instance any, path string) *Report {
	rpt := &Report{}
	if bi == nil {
		return rpt
	}

	if bi.resolved != nil {
		if err := bi.resolved.Validate(instance); err != nil {
			rpt.append(a.libraryFinding(err, bi.resolved, instance, path, bi.definition))
		}
		a.checkBranchFormat(bi, instance, path, rpt)
		return rpt
	}

	// Compound branch: implicit type gate. If the branch carries any
	// object-requiring constructs (Properties, patternProperties,
	// additionalProperties, dependentSchemas) AND the instance is not
	// a map, emit a deterministic type-mismatch finding and return —
	// the per-construct walks below would silently no-op on
	// wrong-typed instances (e.g., calling instance.(map[string]any)
	// returns ok=false), which would let a non-object instance pass
	// branch validation when the branch shape clearly requires an
	// object. The gate closes that hole without invoking the library's
	// non-deterministic root validation.
	requiresObject := len(bi.subBranches) > 0 ||
		len(bi.patternPropsResolved) > 0 ||
		len(bi.additionalPropsBranch) > 0 ||
		len(bi.dependentSchemas) > 0
	if requiresObject {
		if _, ok := instance.(map[string]any); !ok {
			rpt.append(Finding{
				FieldPath: path,
				Keyword:   "type",
				Message:   "expected object, got " + JSONTypeName(instance),
			})
			return rpt
		}
	}

	// The branch position's own constraints — its type, its bounds, its
	// closedness, its array keywords, its in-place applicators — checked
	// without descending into the declared property values the per-key
	// dispatch below covers.
	if bi.ownPruned != nil {
		if err := bi.ownPruned.Validate(instance); err != nil {
			rpt.append(a.libraryFinding(err, bi.ownPruned, instance, path, bi.definition))
		}
	}
	a.checkBranchFormat(bi, instance, path, rpt)

	// Compound branch: required check + per-key dispatch + combinators + dependentSchemas.
	if req, ok := bi.required[""]; ok {
		if m, ok := instance.(map[string]any); ok {
			for _, key := range req {
				if _, present := m[key]; !present {
					rpt.append(Finding{
						FieldPath: childPath(path, key),
						Keyword:   "required",
						Message:   nameViolatedDefinition("missing required property: "+key, bi.definition),
					})
				}
			}
		}
	}

	if m, ok := instance.(map[string]any); ok {
		var unmatched []string

		for _, k := range sortedKeys(m) {
			subPath := childPath(path, k)

			// Arm 1: declared in Properties (branch-local). Walked rather
			// than validated in one call, so a property that is itself an
			// object reports every failing child of its own.
			if sub, hasSub := bi.subBranches[k]; hasSub {
				branchRpt := a.walkBranch(sub, m[k], subPath)
				rpt.Findings = append(rpt.Findings, branchRpt.Findings...)
				continue
			}

			// Arm 2: patternProperties (branch-local).
			if patterns, hasPP := bi.patternPropsResolved[""]; hasPP {
				matched := false
				for _, pe := range patterns {
					if pe.pattern.MatchString(k) {
						matched = true
						branchRpt := a.walkBranch(pe.branch, m[k], subPath)
						rpt.Findings = append(rpt.Findings, branchRpt.Findings...)
					}
				}
				if matched {
					continue
				}
			}

			// Arm 3: additionalProperties (branch-local).
			if apBranch, ok := bi.additionalPropsBranch[""]; ok {
				branchRpt := a.walkBranch(apBranch, m[k], subPath)
				rpt.Findings = append(rpt.Findings, branchRpt.Findings...)
				continue
			}

			unmatched = append(unmatched, k)
		}

		a.reportUnmatchedKeys(bi.keywords, unmatched, m, path, rpt)
	}

	a.walkPositionKeywords(bi.keywords, instance, path, rpt)

	// Combinators at this branch, recursively.
	if branches, ok := bi.oneOfBranches[""]; ok {
		evaluateOneOf(a, branches, instance, path, rpt)
	}
	if branches, ok := bi.allOfBranches[""]; ok {
		evaluateAllOf(a, branches, instance, path, rpt)
	}
	if branches, ok := bi.anyOfBranches[""]; ok {
		evaluateAnyOf(a, branches, instance, path, rpt)
	}
	if notBranch, ok := bi.notIndexes[""]; ok {
		evaluateNot(a, notBranch, instance, path, rpt)
	}

	// dependentSchemas at this branch.
	if deps, ok := bi.dependentSchemas[""]; ok {
		if m, ok := instance.(map[string]any); ok {
			for _, dep := range deps {
				if _, present := m[dep.triggerKey]; !present {
					continue
				}
				branchRpt := a.walkBranch(dep.branch, instance, path)
				for _, f := range branchRpt.Findings {
					f.Keyword = "dependentSchemas/" + dep.triggerKey + "/" + f.Keyword
					rpt.append(f)
				}
			}
		}
	}

	return rpt
}

// evaluateOneOf walks each branch via walkBranch; exactly-one
// clean-match accepts the instance; zero matches or multi-match emit
// a single deterministic Finding. A multi-match diagnostic names the
// matched-branch indices, so an author can see which branches collided.
func evaluateOneOf(a *googleAdapter, branches []*branchIndex, instance any, path string, rpt *Report) {
	var matchedIndices []int
	var failures []string
	for i, branch := range branches {
		branchRpt := a.walkBranch(branch, instance, path)
		if len(branchRpt.Findings) == 0 {
			matchedIndices = append(matchedIndices, i)
		} else {
			failures = append(failures, fmt.Sprintf("branch[%d]: %s", i, summarizeBranchFindings(branchRpt)))
		}
	}
	if len(matchedIndices) != 1 {
		rpt.append(Finding{
			FieldPath: path,
			Keyword:   "oneOf",
			Message:   formatOneOfFailure(matchedIndices, len(branches), failures),
		})
	}
}

// evaluateAllOf walks every branch; emits one Finding per failing
// branch (union of failing-branch findings).
func evaluateAllOf(a *googleAdapter, branches []*branchIndex, instance any, path string, rpt *Report) {
	for i, branch := range branches {
		branchRpt := a.walkBranch(branch, instance, path)
		if len(branchRpt.Findings) == 0 {
			continue
		}
		for _, f := range branchRpt.Findings {
			f.Keyword = fmt.Sprintf("allOf/branch[%d]/%s", i, f.Keyword)
			rpt.append(f)
		}
	}
}

// evaluateAnyOf walks branches in declared order; accepts on the
// first clean match; emits a single Finding listing per-branch
// summaries on no-match.
func evaluateAnyOf(a *googleAdapter, branches []*branchIndex, instance any, path string, rpt *Report) {
	var failures []string
	for i, branch := range branches {
		branchRpt := a.walkBranch(branch, instance, path)
		if len(branchRpt.Findings) == 0 {
			return // anyOf short-circuits on first clean match
		}
		failures = append(failures, fmt.Sprintf("branch[%d]: %s", i, summarizeBranchFindings(branchRpt)))
	}
	rpt.append(Finding{
		FieldPath: path,
		Keyword:   "anyOf",
		Message:   "no anyOf branch matched: " + strings.Join(failures, "; "),
	})
}

// evaluateNot walks the excluded schema; if the instance matches it (zero
// findings), the exclusion is violated and a single "not" Finding is
// emitted.
//
// The message names the excluded shape where the author gave it a name by
// referencing one of the schema's own definitions, because that is the one
// word telling the reader which shape their value is not allowed to be.
// Without a name there is nothing to name, so the message says what the
// reader can still act on: the value at this position is one the schema
// rules out.
func evaluateNot(a *googleAdapter, notBranch *branchIndex, instance any, path string, rpt *Report) {
	branchRpt := a.walkBranch(notBranch, instance, path)
	if len(branchRpt.Findings) != 0 {
		return
	}
	message := "value matches a shape that is not allowed here"
	if notBranch.definition != "" {
		message = "value matches " + strconv.Quote(notBranch.definition) + ", which is not allowed here"
	}
	rpt.append(Finding{
		FieldPath: path,
		Keyword:   "not",
		Message:   message,
	})
}

// summarizeBranchFindings produces a single-line summary of a
// branch's findings for inclusion in aggregated combinator error
// messages. Format: "<keyword>: <message>" per finding, comma-joined.
func summarizeBranchFindings(rpt *Report) string {
	parts := make([]string, 0, len(rpt.Findings))
	for _, f := range rpt.Findings {
		parts = append(parts, fmt.Sprintf("%s: %s", f.Keyword, f.Message))
	}
	return strings.Join(parts, ", ")
}

// formatOneOfFailure formats the oneOf failure Finding message.
// No-match case lists per-branch failures; multi-match case names
// the matched-branch indices for operator debugging.
func formatOneOfFailure(matchedIndices []int, total int, failures []string) string {
	if len(matchedIndices) == 0 {
		return fmt.Sprintf("no oneOf branch matched (%d branches tried): %s",
			total, strings.Join(failures, "; "))
	}
	indices := make([]string, len(matchedIndices))
	for i, idx := range matchedIndices {
		indices[i] = strconv.Itoa(idx)
	}
	return fmt.Sprintf("multiple oneOf branches matched (branches [%s] of %d): expected exactly one",
		strings.Join(indices, ", "), total)
}

// evaluateTemplate runs a pre-compiled template against a data value,
// returning a boolean result. The data scope is passed directly so the
// template's `.` accesses it naturally:
//
//   - For x-aggrega-validate: callers pass the field value; templates
//     reference it via `.` (e.g., `{{ regexMatch "Gi$" . }}` checks
//     the field's string value against a pattern).
//   - For x-aggrega-when: callers pass the parent of the when-node;
//     templates reference sibling fields via `.fieldname` or `$.fieldname`
//     (e.g., `{{ eq $.tlsEnabled true }}` reads a sibling at the
//     parent's scope).
//
// Templates emit "true" / "false" textually; the result is parsed as
// a Go boolean. Numeric outputs are truthy if nonzero. Any non-empty
// non-falsy string is truthy (standard Go template boolean convention).
//
// The resolved Aggregate parameter map (params) is reachable through the
// `param` and `parameters` functions — `param "X"` returns one parameter's
// value, `parameters` the whole map — bound here per evaluation over a clone of
// the pre-compiled template, since the map is known only at validation time.
// The data root is untouched, so a predicate that calls neither function
// renders exactly as before. params is nil when no Context or no parameters was
// supplied (param then returns nil, parameters an empty map). The clone keeps
// the shared pre-compiled template free of per-evaluation state.
func evaluateTemplate(tpl *template.Template, data any, params map[string]any) (bool, error) {
	clone, err := tpl.Clone()
	if err != nil {
		return false, err
	}
	clone.Funcs(template.FuncMap{
		"param": func(key string) any {
			if params == nil {
				return nil
			}
			return params[key]
		},
		"parameters": func() map[string]any {
			if params == nil {
				return map[string]any{}
			}
			return params
		},
	})
	var buf bytes.Buffer
	if err := clone.Execute(&buf, data); err != nil {
		return false, err
	}
	return truthy.Bool(buf.String()), nil
}

// navigate walks the instance to the position indicated by path.
// Returns (sub-instance, true) on success; (nil, false) when the path
// does not exist or traverses through a non-map value.
//
// Currently supports object-property navigation only (matching the
// walker's instance-path collection — array-internal positions are
// not tracked at adapter construction). Map-typed instances may be
// map[string]any (the standard JSON-unmarshalled shape).
func navigate(instance any, path []string) (any, bool) {
	current := instance
	for _, segment := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[segment]
		if !ok {
			return nil, false
		}
		current = v
	}
	return current, true
}

// filterOutPath returns the findings whose FieldPath is NOT under the
// given path (the path itself plus any descendants). Used to drop the
// library's findings for a subtree whose x-aggrega-when: evaluated
// falsy.
func filterOutPath(findings []Finding, path string) []Finding {
	if path == "" {
		// An empty path matches everything; this would drop all
		// findings. Defensive — should not happen since x-aggrega-when:
		// at the root has an empty instance path, but the dropping
		// makes the falsy-root semantic correct.
		return nil
	}
	out := findings[:0]
	for _, f := range findings {
		if isUnderPath(f.FieldPath, path) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// isUnderPath reports whether child is path itself or a descendant
// of path. The check uses a strict prefix match on the delimiter that
// begins a descendant segment, so a sibling sharing a prefix does not match
// ("foo.bar" is NOT under "foo.b").
//
// A descendant begins either with the property delimiter or with the
// bracket that opens an array index, and both have to be recognised: an
// element's path is the array's path plus a bracketed index, so a check
// knowing only the property delimiter reads a finding inside an array as
// belonging to a different subtree — which would leave a gated subtree's
// array findings behind when the gate turns the subtree off.
func isUnderPath(child, path string) bool {
	if child == path {
		return true
	}
	return strings.HasPrefix(child, path+".") || strings.HasPrefix(child, path+"[")
}

// libraryErrorToFindings translates a library validation error into
// structured Finding entries appended to the report. The library's
// validation error includes the schema's keyword location in its
// message text; extractPath parses the deepest `/properties/<name>`
// chain and maps it to the dot-delimited instance path the adapter
// uses for findings.
//
// First-pass: one Finding per library error envelope. Splitting a
// single library error into multiple per-keyword findings is a
// follow-up enhancement.
func (a *googleAdapter) libraryErrorToFindings(rpt *Report, err error, instance any) {
	if err == nil {
		return
	}
	rpt.append(Finding{
		FieldPath: extractPath(err),
		Keyword:   "validation",
		Message:   findingMessage(err, instance, ""),
	})
}

// libraryErrorToFindingsWithPathPrefix is libraryErrorToFindings with
// the resulting Findings' FieldPath prefixed by the given path. Used
// when an else: branch validation produces findings whose paths are
// relative to the sub-instance; the prefix rebases them to the root
// instance's path space.
func (a *googleAdapter) libraryErrorToFindingsWithPathPrefix(rpt *Report, err error, prefix string, instance any) {
	if err == nil {
		return
	}
	subPath := extractPath(err)
	full := prefix
	if subPath != "" {
		full = prefix + "." + subPath
	}
	rpt.append(Finding{
		FieldPath: full,
		Keyword:   "validation",
		Message:   findingMessage(err, instance, ""),
	})
}

// extractPath parses the library error's message text to find the
// schema's keyword-location chain (e.g. "/properties/tlsConfig/then"),
// extracts each `properties/<name>` segment, and returns a dot-delimited
// instance path ("tlsConfig"). Keywords that do not advance instance
// position (`then`, `else`, `if`, `allOf`, `anyOf`, `oneOf`,
// `not`, numeric indices) are skipped.
//
// The library's error format may shift across versions; this extractor
// targets the format observed at v0.4.3. If the format changes, the
// extractor needs an update.
func extractPath(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	// Find the LAST `validating ` occurrence in the message — that
	// segment's path is the deepest keyword location.
	idx := strings.LastIndex(msg, "validating ")
	if idx < 0 {
		return ""
	}
	tail := msg[idx+len("validating "):]
	// Trim to the first `:` or whitespace separator.
	end := len(tail)
	for i, c := range tail {
		if c == ':' || c == '\n' || c == ' ' {
			end = i
			break
		}
	}
	pathPart := tail[:end]
	if pathPart == "root" {
		return ""
	}
	// Which segments move the instance position, and which merely narrow
	// the schema, is one question with one answer; it is read here from the
	// same reader the whole-document check reads it from, so the two cannot
	// come to disagree about a keyword.
	return instancePathFromPointer(pathPart)
}
