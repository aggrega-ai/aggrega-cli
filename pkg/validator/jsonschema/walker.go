// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/google/jsonschema-go/jsonschema"
)

// walk is the pre-walk phase of New. Walks the schema recursively,
// collecting x-aggrega-when: and x-aggrega-validate: node positions,
// pre-compiling their templates, and pre-resolving the then: / else:
// branches of each x-aggrega-when: node.
//
// The walk follows JSON Schema 2020-12's standard subtree structure:
// properties (recurse into each property's schema), items (recurse
// into element schema), additionalProperties (when a schema),
// allOf/anyOf/oneOf (recurse into each composing entry), not (recurse),
// if/then/else (recurse — note that when x-aggrega-when: is present at
// a node, then: / else: still recurse so nested when: nodes inside
// then/else are discovered).
//
// Instance-path tracking: at properties.X, the path appends X. Items
// recursion is currently skipped (when: inside array elements is a
// rare authoring pattern; first-pass adapter does not support).
// Composition keyword recursion (allOf/anyOf/oneOf) does not add path
// segments — the composing entries apply at the same instance position.
//
// The walk completes silently on success. Errors are *AdapterError
// wrapping ErrSchemaResolve (when a then: / else: branch fails to
// resolve) or ErrExtensionEval (when a template fails to parse).
func (a *googleAdapter) walk(s *jsonschema.Schema, instancePath []string) error {
	if s == nil {
		return nil
	}

	// Check Schema.Extra for x-aggrega-* extension keywords.
	if s.Extra != nil {
		if whenTpl, ok := s.Extra["x-aggrega-when"]; ok {
			if err := a.collectWhenNode(s, instancePath, whenTpl); err != nil {
				return err
			}
		}
		if validateTpl, ok := s.Extra["x-aggrega-validate"]; ok {
			if err := a.collectValidateNode(s, instancePath, validateTpl); err != nil {
				return err
			}
		}
	}

	// Recurse into sub-schemas. Recursion happens whether or not this
	// node carried x-aggrega-when: — nested predicates inside then: /
	// else: still need discovery.

	// Properties: each property's schema is a sub-schema at instance
	// path appended with the property name.
	if s.Properties != nil {
		propertyNames := make([]string, 0, len(s.Properties))
		for name := range s.Properties {
			propertyNames = append(propertyNames, name)
		}
		sort.Strings(propertyNames) // deterministic walk order
		for _, name := range propertyNames {
			subPath := append(append([]string{}, instancePath...), name)
			if err := a.walk(s.Properties[name], subPath); err != nil {
				return err
			}
		}
	}

	// AdditionalProperties: a *Schema applying to undeclared property
	// values. Instance path is unknowable (depends on the instance);
	// walk with the parent path so any nested predicates use the
	// nearest known position. First-pass simplification.
	if s.AdditionalProperties != nil {
		if err := a.walk(s.AdditionalProperties, instancePath); err != nil {
			return err
		}
	}

	// Composition keywords: each composing entry applies at the same
	// instance position as the parent node.
	for _, sub := range s.AllOf {
		if err := a.walk(sub, instancePath); err != nil {
			return err
		}
	}
	for _, sub := range s.AnyOf {
		if err := a.walk(sub, instancePath); err != nil {
			return err
		}
	}
	for _, sub := range s.OneOf {
		if err := a.walk(sub, instancePath); err != nil {
			return err
		}
	}
	if s.Not != nil {
		if err := a.walk(s.Not, instancePath); err != nil {
			return err
		}
	}

	// If/Then/Else: same instance position as parent.
	if s.If != nil {
		if err := a.walk(s.If, instancePath); err != nil {
			return err
		}
	}
	if s.Then != nil {
		if err := a.walk(s.Then, instancePath); err != nil {
			return err
		}
	}
	if s.Else != nil {
		if err := a.walk(s.Else, instancePath); err != nil {
			return err
		}
	}

	// $defs entries are reusable definitions, not instance positions.
	// Walk them so nested predicates (rare) are still discovered, but
	// keep the instance path at the parent's level.
	for _, sub := range s.Defs {
		if err := a.walk(sub, instancePath); err != nil {
			return err
		}
	}
	for _, sub := range s.Definitions {
		if err := a.walk(sub, instancePath); err != nil {
			return err
		}
	}

	// PatternProperties + DependentSchemas: map-of-schemas keywords
	// that may carry x-aggrega-* extensions in nested authoring.
	// Recursion uses the parent's instance path because the matched
	// pattern's instance positions are unknowable at construction
	// (depend on the instance's runtime keys); first-pass simplification
	// matching AdditionalProperties' treatment above.
	for _, sub := range s.PatternProperties {
		if err := a.walk(sub, instancePath); err != nil {
			return err
		}
	}
	for _, sub := range s.DependentSchemas {
		if err := a.walk(sub, instancePath); err != nil {
			return err
		}
	}

	return nil
}

// collectWhenNode records an x-aggrega-when: position in
// a.whenNodes after pre-compiling its template and pre-resolving the
// then: and else: branches.
func (a *googleAdapter) collectWhenNode(s *jsonschema.Schema, instancePath []string, rawTpl any) error {
	tplStr, ok := rawTpl.(string)
	if !ok {
		return &AdapterError{
			Path:    joinPath(instancePath),
			Keyword: "x-aggrega-when",
			Message: fmt.Sprintf("expected string template; got %T", rawTpl),
			Err:     ErrExtensionEval,
		}
	}
	tpl, err := template.New("when").Funcs(sprigFuncs).Parse(tplStr)
	if err != nil {
		return &AdapterError{
			Path:    joinPath(instancePath),
			Keyword: "x-aggrega-when",
			Message: "template parse: " + err.Error(),
			Err:     ErrExtensionEval,
		}
	}

	wn := whenNode{
		instancePath: append([]string{}, instancePath...),
		tpl:          tpl,
	}

	if s.Then != nil {
		resolved, err := s.Then.Resolve(nil)
		if err != nil {
			return &AdapterError{
				Path:    joinPath(instancePath),
				Keyword: "then",
				Message: "then: branch Resolve: " + err.Error(),
				Err:     ErrSchemaResolve,
			}
		}
		wn.thenResolved = resolved
	}
	if s.Else != nil {
		resolved, err := s.Else.Resolve(nil)
		if err != nil {
			return &AdapterError{
				Path:    joinPath(instancePath),
				Keyword: "else",
				Message: "else: branch Resolve: " + err.Error(),
				Err:     ErrSchemaResolve,
			}
		}
		wn.elseResolved = resolved
	}

	a.whenNodes = append(a.whenNodes, wn)
	return nil
}

// collectValidateNode records an x-aggrega-validate: position in
// a.validateNodes after pre-compiling its template and capturing the
// x-aggrega-validateMessage: content.
func (a *googleAdapter) collectValidateNode(s *jsonschema.Schema, instancePath []string, rawTpl any) error {
	tplStr, ok := rawTpl.(string)
	if !ok {
		return &AdapterError{
			Path:    joinPath(instancePath),
			Keyword: "x-aggrega-validate",
			Message: fmt.Sprintf("expected string template; got %T", rawTpl),
			Err:     ErrExtensionEval,
		}
	}
	tpl, err := template.New("validate").Funcs(sprigFuncs).Parse(tplStr)
	if err != nil {
		return &AdapterError{
			Path:    joinPath(instancePath),
			Keyword: "x-aggrega-validate",
			Message: "template parse: " + err.Error(),
			Err:     ErrExtensionEval,
		}
	}

	message := ""
	if rawMsg, ok := s.Extra["x-aggrega-validateMessage"]; ok {
		if msgStr, ok := rawMsg.(string); ok {
			message = msgStr
		}
	}
	if message == "" {
		message = "x-aggrega-validate predicate failed"
	}

	a.validateNodes = append(a.validateNodes, validateNode{
		instancePath: append([]string{}, instancePath...),
		tpl:          tpl,
		message:      message,
	})
	return nil
}

// joinPath formats a path slice as a slash-delimited string for error
// reporting and Finding.FieldPath. An empty path renders as "".
func joinPath(path []string) string {
	if len(path) == 0 {
		return ""
	}
	return strings.Join(path, ".")
}

// indexSubSchemas populates a.subResolved and a.required by walking
// schema.Properties recursively. Each sub-schema at an
// instance-reachable position gets pre-resolved via the library's
// Resolve so the per-call Validate path does not pay resolution cost.
// The sorted instance keys at Validate time + this index together
// give deterministic multi-violation Reports.
//
// $defs graft for non-root sub-schemas: a sub-schema using
// `$ref → #/$defs/X` cannot standalone-resolve because its own
// $defs scope is empty (the entries live on the root or on an
// enclosing parent). At every non-root level, the walker clones the
// sub-schema via (*Schema).CloneSchemas() and grafts a merged $defs
// map onto the clone — ambient entries (each deep-cloned to keep
// the library's tree-uniqueness invariant intact across the
// construction sweep) plus any local entries the sub-schema
// declared. Local entries take precedence on name collision,
// matching JSON Schema 2020-12's nearest-enclosing-$defs semantic.
//
// The ambient set threads through recursion: the initial call from
// New() passes the root schema's $defs as the initial ambient; each
// level's graft computes the next ambient (parent's ambient unioned
// with the sub-schema's local entries) and passes it to descendants.
// So `$ref → #/$defs/X` resolves at any depth and against any
// enclosing scope, not only against the document root.
//
// PatternProperties / DependentSchemas / AdditionalProperties are
// recursed for x-aggrega-* discovery in walk() but are not indexed
// for deterministic sub-schema validation here — the library's own
// iteration over those map-typed fields remains non-deterministic
// and is a documented limitation routed to a follow-up.
//
// Residual walker-completeness gaps for `$ref` resolution: (a)
// cross-property refs (`$ref → #/properties/foo` — the graft covers
// $defs only, not /properties), (b) `$defs/X` entries whose own
// refs leave $defs scope (e.g., `$defs/X` referencing
// `/properties/foo`). Either pattern would require root-validate +
// finding-filter and accept determinism degradation for the affected
// paths only.
func (a *googleAdapter) indexSubSchemas(s *jsonschema.Schema, path string, ambientDefs map[string]*jsonschema.Schema) error {
	if s == nil {
		return nil
	}

	if path != "" && len(ambientDefs) > 0 {
		s = s.CloneSchemas()
		merged := make(map[string]*jsonschema.Schema, len(ambientDefs)+len(s.Defs))
		for k, v := range ambientDefs {
			merged[k] = v.CloneSchemas()
		}
		for k, v := range s.Defs {
			merged[k] = v
		}
		s.Defs = merged
	}

	// Resolve this sub-schema as a standalone root. The root schema
	// (path == "") MUST resolve — propagating the error gives the
	// calling pipeline a fail-fast on a malformed top-level schema.
	// Sub-schemas (path != "") whose $ref targets remain unresolvable
	// after the graft (cross-property refs; $defs chains escaping
	// $defs scope) tolerate-skip from the deterministic-walk index
	// rather than fail construction — those subtrees are the
	// documented residual walker-completeness gap.
	resolved, err := s.Resolve(nil)
	if err != nil {
		if path == "" {
			return &AdapterError{
				Path:    "",
				Message: "schema.Resolve: " + err.Error(),
				Err:     ErrSchemaResolve,
			}
		}
		return nil
	}
	a.subResolved[path] = resolved

	if len(s.Required) > 0 {
		req := append([]string{}, s.Required...)
		sort.Strings(req)
		a.required[path] = req
	}

	// nextAmbient = the $defs in scope for descendants — the post-graft
	// s.Defs is exactly this (ambient unioned with local overrides).
	// At the root, s.Defs is the root's original $defs (no graft
	// occurred at path == "").
	nextAmbient := s.Defs

	if s.Properties != nil {
		propertyNames := make([]string, 0, len(s.Properties))
		for name := range s.Properties {
			propertyNames = append(propertyNames, name)
		}
		sort.Strings(propertyNames)
		for _, name := range propertyNames {
			subPath := name
			if path != "" {
				subPath = path + "." + name
			}
			if err := a.indexSubSchemas(s.Properties[name], subPath, nextAmbient); err != nil {
				return err
			}
		}
	}

	// WI 05b indexing for the seven walker-completeness gap constructs.
	// AdditionalProperties / oneOf / allOf / anyOf / not / patternProperties /
	// dependentSchemas all populate branchIndex structures via
	// buildBranchIndex which recursively constructs the branch-local
	// validation state for deterministic walkBranch evaluation.

	// additionalProperties: <schema> — promoted to branchIndex per
	// Decision 3 so nested-Properties additionalProperties walk
	// deterministically.
	if s.AdditionalProperties != nil {
		if bi := a.buildBranchIndex(s.AdditionalProperties, nextAmbient); bi != nil {
			a.additionalPropsBranch[path] = bi
		}
	}

	if len(s.OneOf) > 0 {
		branches := make([]*branchIndex, 0, len(s.OneOf))
		for _, b := range s.OneOf {
			if bi := a.buildBranchIndex(b, nextAmbient); bi != nil {
				branches = append(branches, bi)
			}
		}
		if len(branches) > 0 {
			a.oneOfBranches[path] = branches
		}
	}

	if len(s.AllOf) > 0 {
		branches := make([]*branchIndex, 0, len(s.AllOf))
		for _, b := range s.AllOf {
			if bi := a.buildBranchIndex(b, nextAmbient); bi != nil {
				branches = append(branches, bi)
			}
		}
		if len(branches) > 0 {
			a.allOfBranches[path] = branches
		}
	}

	if len(s.AnyOf) > 0 {
		branches := make([]*branchIndex, 0, len(s.AnyOf))
		for _, b := range s.AnyOf {
			if bi := a.buildBranchIndex(b, nextAmbient); bi != nil {
				branches = append(branches, bi)
			}
		}
		if len(branches) > 0 {
			a.anyOfBranches[path] = branches
		}
	}

	if s.Not != nil {
		if bi := a.buildBranchIndex(s.Not, nextAmbient); bi != nil {
			a.notIndexes[path] = bi
		}
	}

	if len(s.PatternProperties) > 0 {
		patterns := make([]string, 0, len(s.PatternProperties))
		for p := range s.PatternProperties {
			patterns = append(patterns, p)
		}
		sort.Strings(patterns)
		entries := make([]*patternEntry, 0, len(patterns))
		for _, p := range patterns {
			re, perr := regexp.Compile(p)
			if perr != nil {
				return &AdapterError{
					Path:    path,
					Message: "patternProperties regex compile: " + perr.Error(),
					Err:     ErrSchemaResolve,
				}
			}
			if bi := a.buildBranchIndex(s.PatternProperties[p], nextAmbient); bi != nil {
				entries = append(entries, &patternEntry{pattern: re, branch: bi})
			}
		}
		if len(entries) > 0 {
			a.patternPropsResolved[path] = entries
		}
	}

	if len(s.DependentSchemas) > 0 {
		keys := make([]string, 0, len(s.DependentSchemas))
		for k := range s.DependentSchemas {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		entries := make([]*dependentEntry, 0, len(keys))
		for _, k := range keys {
			if bi := a.buildBranchIndex(s.DependentSchemas[k], nextAmbient); bi != nil {
				entries = append(entries, &dependentEntry{triggerKey: k, branch: bi})
			}
		}
		if len(entries) > 0 {
			a.dependentSchemas[path] = entries
		}
	}

	return nil
}

// buildBranchIndex constructs a branchIndex for a sub-schema. Applies
// the Option α-strict leaf-vs-compound discipline: leaf branches get
// resolved populated only; compound branches recursively populate the
// per-construct maps and leave resolved nil.
//
// Returns nil if Resolve fails after $defs graft (tolerate-skip per
// the existing walker-completeness gap pattern; the corresponding
// branch's validation surfaces as a documented gap rather than a
// construction error).
func (a *googleAdapter) buildBranchIndex(s *jsonschema.Schema, ambientDefs map[string]*jsonschema.Schema) *branchIndex {
	if s == nil {
		return nil
	}

	if len(ambientDefs) > 0 {
		s = s.CloneSchemas()
		merged := make(map[string]*jsonschema.Schema, len(ambientDefs)+len(s.Defs))
		for k, v := range ambientDefs {
			merged[k] = v.CloneSchemas()
		}
		for k, v := range s.Defs {
			merged[k] = v
		}
		s.Defs = merged
	}

	resolved, err := s.Resolve(nil)
	if err != nil {
		return nil
	}

	bi := &branchIndex{}

	// Leaf-vs-compound decision: a branch is a leaf iff it has no
	// Properties, no nested combinators, no patternProperties, no
	// dependentSchemas, and no additionalProperties.
	isLeaf := s.Properties == nil &&
		len(s.OneOf) == 0 &&
		len(s.AllOf) == 0 &&
		len(s.AnyOf) == 0 &&
		s.Not == nil &&
		len(s.PatternProperties) == 0 &&
		len(s.DependentSchemas) == 0 &&
		s.AdditionalProperties == nil

	if isLeaf {
		bi.resolved = resolved
		return bi
	}

	// Compound branch: populate per-construct maps. Uses the same
	// ambient-defs threading as indexSubSchemas; nextAmbient = s.Defs
	// post-graft.
	nextAmbient := s.Defs
	bi.subResolved = map[string]*jsonschema.Resolved{}
	bi.required = map[string][]string{}

	if len(s.Required) > 0 {
		req := append([]string{}, s.Required...)
		sort.Strings(req)
		bi.required[""] = req
	}

	if s.Properties != nil {
		propertyNames := make([]string, 0, len(s.Properties))
		for name := range s.Properties {
			propertyNames = append(propertyNames, name)
		}
		sort.Strings(propertyNames)
		for _, name := range propertyNames {
			propResolved, perr := s.Properties[name].Resolve(nil)
			if perr == nil {
				bi.subResolved[name] = propResolved
			}
		}
	}

	if s.AdditionalProperties != nil {
		if apBi := a.buildBranchIndex(s.AdditionalProperties, nextAmbient); apBi != nil {
			bi.additionalPropsBranch = map[string]*branchIndex{"": apBi}
		}
	}

	if len(s.OneOf) > 0 {
		branches := make([]*branchIndex, 0, len(s.OneOf))
		for _, b := range s.OneOf {
			if sub := a.buildBranchIndex(b, nextAmbient); sub != nil {
				branches = append(branches, sub)
			}
		}
		if len(branches) > 0 {
			bi.oneOfBranches = map[string][]*branchIndex{"": branches}
		}
	}

	if len(s.AllOf) > 0 {
		branches := make([]*branchIndex, 0, len(s.AllOf))
		for _, b := range s.AllOf {
			if sub := a.buildBranchIndex(b, nextAmbient); sub != nil {
				branches = append(branches, sub)
			}
		}
		if len(branches) > 0 {
			bi.allOfBranches = map[string][]*branchIndex{"": branches}
		}
	}

	if len(s.AnyOf) > 0 {
		branches := make([]*branchIndex, 0, len(s.AnyOf))
		for _, b := range s.AnyOf {
			if sub := a.buildBranchIndex(b, nextAmbient); sub != nil {
				branches = append(branches, sub)
			}
		}
		if len(branches) > 0 {
			bi.anyOfBranches = map[string][]*branchIndex{"": branches}
		}
	}

	if s.Not != nil {
		if notBi := a.buildBranchIndex(s.Not, nextAmbient); notBi != nil {
			bi.notIndexes = map[string]*branchIndex{"": notBi}
		}
	}

	if len(s.PatternProperties) > 0 {
		patterns := make([]string, 0, len(s.PatternProperties))
		for p := range s.PatternProperties {
			patterns = append(patterns, p)
		}
		sort.Strings(patterns)
		entries := make([]*patternEntry, 0, len(patterns))
		for _, p := range patterns {
			re, perr := regexp.Compile(p)
			if perr != nil {
				continue
			}
			if sub := a.buildBranchIndex(s.PatternProperties[p], nextAmbient); sub != nil {
				entries = append(entries, &patternEntry{pattern: re, branch: sub})
			}
		}
		if len(entries) > 0 {
			bi.patternPropsResolved = map[string][]*patternEntry{"": entries}
		}
	}

	if len(s.DependentSchemas) > 0 {
		keys := make([]string, 0, len(s.DependentSchemas))
		for k := range s.DependentSchemas {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		entries := make([]*dependentEntry, 0, len(keys))
		for _, k := range keys {
			if sub := a.buildBranchIndex(s.DependentSchemas[k], nextAmbient); sub != nil {
				entries = append(entries, &dependentEntry{triggerKey: k, branch: sub})
			}
		}
		if len(entries) > 0 {
			bi.dependentSchemas = map[string][]*dependentEntry{"": entries}
		}
	}

	return bi
}
