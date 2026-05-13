// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

// Validate implements the Validator interface. The algorithm:
//
//  1. Deterministic property walk. Recursively iterate the instance's
//     keys in sorted order; at each property, validate the sub-instance
//     against the pre-resolved sub-schema (from a.subResolved) and
//     emit a Finding on failure. Check the schema's Required slice at
//     each level and emit one Finding per missing required key in
//     sorted order. Recurse into nested objects so deep properties
//     walk deterministically rather than relying on the library's
//     randomised internal Properties iteration. Cross-property
//     constraints at the root (allOf, anyOf, oneOf, not at root) and
//     patternProperties / dependentSchemas iteration are documented
//     walker-completeness gaps — not covered by this walk.
//
//  2. Walk the pre-collected x-aggrega-when: nodes shallowest-first.
//     For each:
//     - Navigate to the node's instance position.
//     - Evaluate the pre-compiled template against the parent of the
//       when-node (the surrounding object scope where sibling fields
//       live; templates reference siblings via $.fieldname).
//     - On truthy: keep the walk's findings for this subtree (the walk
//       has already validated against the sub-schema rooted at the
//       subtree, which includes any then: constraints in the
//       compact-form-emitted shape).
//     - On falsy + else: present: drop the walk's findings for this
//       subtree and validate the sub-instance against the pre-resolved
//       else: branch (findings rebased to the sub-instance's path).
//     - On falsy + no else: present: drop the walk's findings for this
//       subtree (no constraint applies when falsy and no else: was
//       authored — D-001b-2 semantic).
//     Subsequent x-aggrega-when: nodes nested under a falsy outer
//     branch are skipped (consumed by the outer's filter).
//
//  3. Walk the pre-collected x-aggrega-validate: nodes. For each:
//     - Navigate to the node's instance position.
//     - Evaluate the pre-compiled template against the field value.
//     - On falsy: append a Finding carrying the x-aggrega-validateMessage:
//       content.
//
//  4. Sort the findings (FieldPath -> Keyword -> Message) per the
//     deterministic-output invariant.
func (a *googleAdapter) Validate(instance any, ctx *Context) (*Report, error) {
	rpt := &Report{}

	// Step 1: deterministic property walk. Validates each property's
	// sub-instance against its pre-resolved sub-schema in sorted-key
	// order; checks required at each level. Sidesteps the library's
	// fail-fast-with-randomised-iteration semantic so multi-violation
	// Reports are byte-identical across builds.
	//
	// Cross-property constraints at the root (allOf, anyOf, oneOf,
	// not, patternProperties, dependentSchemas) are NOT validated by
	// this walk. The library's own root validation would catch them
	// but reintroduces non-determinism because the library's root
	// Validate uses randomised map iteration internally. These
	// constraints surface as a documented walker-completeness gap;
	// the typical compact-form output uses object-with-properties
	// patterns that the walk covers fully.
	a.deterministicWalk(instance, "", rpt)

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

		truthy, err := evaluateTemplate(wn.tpl, parentInstance)
		if err != nil {
			return nil, &AdapterError{
				Path:    whenPathStr,
				Keyword: "x-aggrega-when",
				Message: "template evaluation: " + err.Error(),
				Err:     ErrExtensionEval,
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
				a.libraryErrorToFindingsWithPathPrefix(rpt, err, whenPathStr)
			}
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

		truthy, err := evaluateTemplate(vn.tpl, fieldValue)
		if err != nil {
			return nil, &AdapterError{
				Path:    vnPathStr,
				Keyword: "x-aggrega-validate",
				Message: "template evaluation: " + err.Error(),
				Err:     ErrExtensionEval,
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

	// Step 4: deterministic sort.
	rpt.sort()
	return rpt, nil
}

// deterministicWalk recursively validates each property in the
// instance against its pre-resolved sub-schema (from a.subResolved)
// in sorted-key order, plus checks required-property presence at each
// level. Sidesteps the library's randomised property iteration so
// multi-violation Reports are byte-identical across builds.
//
// The walk emits findings with FieldPath at the property's instance
// path. Library sub-schema validation produces deterministic findings
// for leaf constraints (type, pattern, range, enum, etc.) per call.
// For sub-schemas that themselves contain Properties, the recursion
// descends into each child rather than letting the library iterate
// internally.
//
// Per-key dispatch follows the WI 05b precedence rule: Properties >
// PatternProperties > AdditionalProperties. A key declared in
// Properties validates against the Property's sub-schema. A key
// matching one or more patternProperties validates against each
// matching pattern's sub-schema via walkBranch. A key in neither falls
// through to additionalProperties (if declared as a schema; the
// boolean form is enforced via Properties shape alone).
//
// Cross-property constraints (allOf, anyOf, oneOf, not, dependentSchemas)
// evaluate at the current path after the per-key dispatch via Option α
// recursive walkBranch. The library's instance-root Validate is never
// invoked for these constructs — the walker delivers deterministic
// finding emission across the full JSON Schema 2020-12 surface the
// LDR enumerates.
func (a *googleAdapter) deterministicWalk(instance any, path string, rpt *Report) {
	// Required-property check at this level: emit one finding per
	// missing key, in sorted order, BEFORE recursing into present
	// children.
	if req, ok := a.required[path]; ok {
		if m, ok := instance.(map[string]any); ok {
			for _, key := range req {
				if _, present := m[key]; !present {
					missingPath := key
					if path != "" {
						missingPath = path + "." + key
					}
					rpt.append(Finding{
						FieldPath: missingPath,
						Keyword:   "required",
						Message:   "missing required property: " + key,
					})
				}
			}
		}
	}

	if m, ok := instance.(map[string]any); ok {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		// Per-key dispatch with WI 05b precedence rule:
		// Properties > PatternProperties > AdditionalProperties.
		for _, k := range keys {
			subPath := k
			if path != "" {
				subPath = path + "." + k
			}

			// Arm 1: declared in Properties.
			if subResolved, hasSub := a.subResolved[subPath]; hasSub {
				if err := subResolved.Validate(m[k]); err != nil {
					rpt.append(Finding{
						FieldPath: subPath,
						Keyword:   "validation",
						Message:   err.Error(),
					})
				}
				a.deterministicWalk(m[k], subPath, rpt)
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
			}
		}
	}

	// Combinator evaluation at this path via Option α recursive walkBranch.
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
					rpt.append(Finding{
						FieldPath: f.FieldPath,
						Keyword:   "dependentSchemas/" + dep.triggerKey + "/" + f.Keyword,
						Message:   f.Message,
					})
				}
			}
		}
	}
}

// walkBranch validates an instance against a pre-indexed branch using
// the same deterministic property-walk discipline as the top-level
// walker. Reads from the branchIndex rather than googleAdapter's
// top-level maps. Recursion through nested combinators is bounded by
// schema depth.
//
// Leaf-vs-compound discipline (Option α-strict): bi.resolved is
// populated ONLY for leaf branches (no Properties, no nested
// combinators, no patternProperties, no dependentSchemas, no
// additionalProperties). For leaf branches the library Validate runs
// (deterministic at the library level for type/pattern/enum/range
// constraints). For compound branches, bi.resolved is nil and only
// the per-construct walks run — the library is never invoked at
// instance-root scope.
func (a *googleAdapter) walkBranch(bi *branchIndex, instance any, path string) *Report {
	rpt := &Report{}
	if bi == nil {
		return rpt
	}

	// Leaf-validation fallback (Option α-strict).
	if bi.resolved != nil {
		if err := bi.resolved.Validate(instance); err != nil {
			rpt.append(Finding{
				FieldPath: path,
				Keyword:   "validation",
				Message:   err.Error(),
			})
		}
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
	requiresObject := len(bi.subResolved) > 0 ||
		len(bi.patternPropsResolved) > 0 ||
		len(bi.additionalPropsBranch) > 0 ||
		len(bi.dependentSchemas) > 0
	if requiresObject {
		if _, ok := instance.(map[string]any); !ok {
			rpt.append(Finding{
				FieldPath: path,
				Keyword:   "type",
				Message:   fmt.Sprintf("expected object, got %T", instance),
			})
			return rpt
		}
	}

	// Compound branch: required check + per-key dispatch + combinators + dependentSchemas.
	if req, ok := bi.required[""]; ok {
		if m, ok := instance.(map[string]any); ok {
			for _, key := range req {
				if _, present := m[key]; !present {
					missingPath := key
					if path != "" {
						missingPath = path + "." + key
					}
					rpt.append(Finding{
						FieldPath: missingPath,
						Keyword:   "required",
						Message:   "missing required property: " + key,
					})
				}
			}
		}
	}

	if m, ok := instance.(map[string]any); ok {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			subPath := k
			if path != "" {
				subPath = path + "." + k
			}

			// Arm 1: declared in Properties (branch-local).
			if subResolved, hasSub := bi.subResolved[k]; hasSub {
				if err := subResolved.Validate(m[k]); err != nil {
					rpt.append(Finding{
						FieldPath: subPath,
						Keyword:   "validation",
						Message:   err.Error(),
					})
				}
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
			}
		}
	}

	// Combinators at this branch (Option α recursion).
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
					rpt.append(Finding{
						FieldPath: f.FieldPath,
						Keyword:   "dependentSchemas/" + dep.triggerKey + "/" + f.Keyword,
						Message:   f.Message,
					})
				}
			}
		}
	}

	return rpt
}

// evaluateOneOf walks each branch via walkBranch; exactly-one
// clean-match accepts the instance; zero matches or multi-match emit
// a single deterministic Finding. Multi-match diagnostics include the
// matched-branch indices (revision-2 enhancement for operator
// debugging).
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
			rpt.append(Finding{
				FieldPath: f.FieldPath,
				Keyword:   fmt.Sprintf("allOf/branch[%d]/%s", i, f.Keyword),
				Message:   f.Message,
			})
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

// evaluateNot walks the inverse schema; if instance MATCHES the
// inverse (zero findings), emit a single "not" Finding.
func evaluateNot(a *googleAdapter, notBranch *branchIndex, instance any, path string, rpt *Report) {
	branchRpt := a.walkBranch(notBranch, instance, path)
	if len(branchRpt.Findings) == 0 {
		rpt.append(Finding{
			FieldPath: path,
			Keyword:   "not",
			Message:   "instance must NOT match the inverse schema",
		})
	}
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
// Parameter context (Context.Parameters) is not yet plumbed into the
// template scope; current canonical predicate examples use only the
// field value + surrounding scope. Parameter access through
// `{{ .parameters.X }}` is a follow-up enhancement once an authoring
// pattern surfaces that requires it.
func evaluateTemplate(tpl *template.Template, data any) (bool, error) {
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return false, err
	}
	out := strings.TrimSpace(buf.String())
	switch out {
	case "true", "True", "TRUE":
		return true, nil
	case "false", "False", "FALSE", "":
		return false, nil
	}
	// Numeric: nonzero is truthy.
	if n, err := strconv.ParseFloat(out, 64); err == nil {
		return n != 0, nil
	}
	// Any other non-empty string is truthy (matches Go template
	// convention for boolean-like outputs).
	return true, nil
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
// of path. Both arguments are dot-delimited strings produced by
// joinPath. The check uses a strict prefix match on path + "." to
// avoid matching siblings whose paths share a prefix (e.g.,
// "foo.bar" is NOT under "foo.b").
func isUnderPath(child, path string) bool {
	if child == path {
		return true
	}
	return strings.HasPrefix(child, path+".")
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
func (a *googleAdapter) libraryErrorToFindings(rpt *Report, err error) {
	if err == nil {
		return
	}
	rpt.append(Finding{
		FieldPath: extractPath(err),
		Keyword:   "validation",
		Message:   err.Error(),
	})
}

// libraryErrorToFindingsWithPathPrefix is libraryErrorToFindings with
// the resulting Findings' FieldPath prefixed by the given path. Used
// when an else: branch validation produces findings whose paths are
// relative to the sub-instance; the prefix rebases them to the root
// instance's path space.
func (a *googleAdapter) libraryErrorToFindingsWithPathPrefix(rpt *Report, err error, prefix string) {
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
		Message:   err.Error(),
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
	// Walk the slash-delimited segments. Capture each `properties/<name>`
	// pair; skip composition / branch / numeric keywords.
	segments := strings.Split(pathPart, "/")
	out := make([]string, 0, len(segments)/2)
	for i := 0; i < len(segments); i++ {
		if segments[i] == "properties" && i+1 < len(segments) {
			out = append(out, segments[i+1])
			i++ // skip the captured property name
		}
		// All other segments (empty, "then", "else", "if",
		// "allOf", "anyOf", "oneOf", "not", numeric indices,
		// "items", "patternProperties", "$defs") do not advance the
		// instance position and are skipped.
	}
	return strings.Join(out, ".")
}
