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
			Err:     ErrExtensionEval(),
		}
	}
	tpl, err := template.New("when").Funcs(sprigFuncs).Parse(tplStr)
	if err != nil {
		return &AdapterError{
			Path:    joinPath(instancePath),
			Keyword: "x-aggrega-when",
			Message: "template parse: " + err.Error(),
			Err:     ErrExtensionEval(),
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
				Err:     ErrSchemaResolve(),
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
				Err:     ErrSchemaResolve(),
			}
		}
		wn.elseResolved = resolved
		wn.elseSchema = s.Else
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
			Err:     ErrExtensionEval(),
		}
	}
	tpl, err := template.New("validate").Funcs(sprigFuncs).Parse(tplStr)
	if err != nil {
		return &AdapterError{
			Path:    joinPath(instancePath),
			Keyword: "x-aggrega-validate",
			Message: "template parse: " + err.Error(),
			Err:     ErrExtensionEval(),
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

// childPath names a property's position beneath path. At the root the
// property name stands alone.
func childPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// sortedKeys returns an object's keys in ascending order, which is the
// order every walk over an instance uses. Iterating the map directly is
// what makes a report name a different one of several failing keys on each
// run.
func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
// PatternProperties, DependentSchemas and AdditionalProperties are
// indexed here too, each into its own branch structure, so the walk
// reaches them rather than leaving the library to iterate the matching
// instance keys.
//
// Alongside each position's full schema, the index carries that
// position reduced to its own keywords, with its declared property
// values pruned away. The per-call walk checks a position against the
// reduced copy: one call over a whole subtree reports whichever of
// several equally-failing children the library's map iteration reached
// first, and the walk reaches each child itself. A position carrying
// only a reference — the root included — is indexed as the schema it
// names, so the recursion descends into that schema's properties
// instead of stopping at a node that declares none.
//
// A position whose reference names something the graft cannot supply — a
// declared property rather than a definition, another document, or a
// definition whose own reference leaves the definitions behind — cannot be
// stood up on its own and is recorded instead, with its location in the
// schema document. The per-call check reaches those positions through the
// whole document, where such a reference does resolve; see
// unresolved_positions.go. Recording is what keeps the tolerance from being
// silent: a position skipped and not recorded is not merely reported less
// precisely, it is checked by nothing, and so is everything beneath it.
//
// pointer is the position's own location in the schema document, threaded
// alongside path for exactly that purpose. It is composed from the property
// names as authored, before any reference is substituted, because that is
// the location a failure's chain passes through on its way to whatever the
// reference named.
func (a *googleAdapter) indexSubSchemas(s *jsonschema.Schema, path string, ambientDefs map[string]*jsonschema.Schema, enclosing, pointer string) error {
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

	// A position that carries only a reference is indexed as the schema it
	// names, so this level's own recursion reaches that schema's
	// properties. Without the substitution the position would index as a
	// node with no properties of its own, its children would have no entry
	// in the index, and pruning its own call — which the next step does —
	// would leave them unchecked.
	//
	// The root is included, and it is the position where leaving the
	// reference in place costs the most. The index is what the walk
	// enforces from, so a root that indexes nothing enforces nothing: every
	// document validates clean, however malformed, because there is no
	// entry against which to find it wanting. That failure is silent and in
	// the permissive direction, which is the direction no positive test can
	// see — a schema in this shape reads as stricter than one that inlines
	// the same constraints while enforcing none of them.
	declaredSensitive := DeclaresSensitive(s.Extra)
	definition := ""
	if name, substituted, ok := substituteRefOnly(s); ok && !a.expanding[name] {
		s = substituted
		// The definition becomes the root of the library call made at this
		// position, and a library names its own call root as the root rather
		// than by name — so without recording the name here, a diagnostic
		// would no longer say which declared shape was violated.
		definition = name
		a.expanding[name] = true
		defer delete(a.expanding, name)
	}

	// Recorded before the position is resolved, so a position that cannot be
	// stood up on its own is recorded too, and noted before anything beneath it
	// is built, so every position inside it inherits the declaration.
	defer a.enterSensitive(declaredSensitive, s)()
	if a.sensitiveDepth > 0 {
		a.sensitive[path] = s
	}

	// The name a diagnostic at this position cites.
	//
	// The instance root is the exception, and it is deliberate rather than an
	// oversight. A reference at the root and the same constraints written out
	// at the root are two authorings of one document, so they must fail
	// identically; and
	// there the definition IS the document's root, so the library's own label
	// is already accurate. Citing the name would make one set of constraints
	// report differently according to how the author chose to factor it.
	//
	// Nor is the name inherited by the positions below, for the same reason:
	// every one of them is reached by the same descent under either
	// authoring. What does inherit is a definition substituted for an
	// in-place applicator's branch, which shares its instance position with
	// its siblings and so cannot be told apart by path alone. That
	// inheritance lives in buildBranchIndex, and enclosing carries it.
	cited := definition
	if path == "" {
		cited = ""
	}
	if cited != "" {
		a.subDefinition[path] = cited
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
				Err:     ErrSchemaResolve(),
			}
		}
		a.recordUnresolvedPosition(path, pointer)
		a.unresolvedNodes[path] = s
		return nil
	}
	a.subResolved[path] = resolved

	// The library ignores format, so the walk checks it at each position beside
	// the constraints the library checks there, and sweeps for it any region the
	// library evaluates whole at this position.
	if s.Format != "" {
		a.formats[path] = s.Format
	}
	if regions := a.libraryRegions(s); len(regions) > 0 {
		a.regions[path] = regions
	}

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
			if err := a.indexSubSchemas(s.Properties[name], subPath, nextAmbient, "", childPointer(pointer, name)); err != nil {
				return err
			}
		}
	}

	// Branch-index population for the seven composition / extension
	// keywords the walker covers: additionalProperties, oneOf, allOf,
	// anyOf, not, patternProperties, dependentSchemas. Each populates a
	// branchIndex via buildBranchIndex, which recursively constructs the
	// branch-local validation state for deterministic walkBranch
	// evaluation.

	// additionalProperties: <schema> — promoted to branchIndex, so
	// nested-Properties additionalProperties walk deterministically.
	//
	// The closed form is deliberately excluded. It states which keys the
	// object may carry rather than constraining any value, so routing an
	// undeclared key's value through it produced a finding at the key's own
	// path phrased as a failure to avoid matching an inverted schema —
	// wording that names neither the key nor anything the author wrote. The
	// keyword index carries the closed form instead, and the walk names
	// every unaccepted key once, together, in order.
	if s.AdditionalProperties != nil && !isClosedForm(s.AdditionalProperties) {
		if bi := a.buildBranchIndex(s.AdditionalProperties, nextAmbient, ""); bi != nil {
			a.additionalPropsBranch[path] = bi
		}
	}

	if len(s.OneOf) > 0 {
		branches := make([]*branchIndex, 0, len(s.OneOf))
		for _, b := range s.OneOf {
			if bi := a.buildBranchIndex(b, nextAmbient, ""); bi != nil {
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
			if bi := a.buildBranchIndex(b, nextAmbient, ""); bi != nil {
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
			if bi := a.buildBranchIndex(b, nextAmbient, ""); bi != nil {
				branches = append(branches, bi)
			}
		}
		if len(branches) > 0 {
			a.anyOfBranches[path] = branches
		}
	}

	if s.Not != nil {
		if bi := a.buildBranchIndex(s.Not, nextAmbient, ""); bi != nil {
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
					Err:     ErrSchemaResolve(),
				}
			}
			if bi := a.buildBranchIndex(s.PatternProperties[p], nextAmbient, ""); bi != nil {
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
			if bi := a.buildBranchIndex(s.DependentSchemas[k], nextAmbient, ""); bi != nil {
				entries = append(entries, &dependentEntry{triggerKey: k, branch: bi})
			}
		}
		if len(entries) > 0 {
			a.dependentSchemas[path] = entries
		}
	}

	if pk := a.buildPositionKeywords(s, nextAmbient, cited); pk != nil {
		a.keywords[path] = pk
	}

	// The reduced schema for this position, built last so it can be told
	// exactly which families the walk will evaluate here and therefore
	// which it must not repeat. Keyed on what was indexed above rather than
	// on what the schema declares, so a family the index could not build
	// stays in the reduced copy and keeps being checked.
	_, hasRequired := a.required[path]
	_, hasAllOf := a.allOfBranches[path]
	_, hasAnyOf := a.anyOfBranches[path]
	_, hasOneOf := a.oneOfBranches[path]
	_, hasNot := a.notIndexes[path]
	_, hasDependent := a.dependentSchemas[path]
	reduced := pruneOwnKeywords(s, evaluatedByWalk{
		required:         hasRequired,
		allOf:            hasAllOf,
		anyOf:            hasAnyOf,
		oneOf:            hasOneOf,
		not:              hasNot,
		dependentSchemas: hasDependent,
	}, a.keywords[path])
	if reducedResolved, reducedErr := reduced.Resolve(nil); reducedErr == nil {
		a.subPruned[path] = reducedResolved
	}

	return nil
}

// buildBranchIndex constructs a branchIndex for a sub-schema. A leaf
// branch gets resolved populated and nothing else; a compound branch
// gets its own pruned constraints plus a recursively-built index per
// construct, and resolved stays nil.
//
// A branch carrying only a reference to one of the schema's own
// definitions is built from the definition it names. Without that
// substitution such a branch reads as a leaf — a reference declares no
// properties of its own — and the whole referenced object would be handed
// to the library in one call, which is exactly the parent-level call that
// reports an arbitrary one of several equally-failing children. The
// substitution is refused whenever the reference sits alongside any other
// constraint (see refOnly), so an unrecognised shape keeps the behaviour
// it had.
//
// Returns nil if Resolve fails after $defs graft (tolerate-skip per
// the existing walker-completeness gap pattern; the corresponding
// branch's validation surfaces as a documented gap rather than a
// construction error).
func (a *googleAdapter) buildBranchIndex(s *jsonschema.Schema, ambientDefs map[string]*jsonschema.Schema, enclosing string) *branchIndex {
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

	// Inherited from the enclosing position for the same reason as in
	// indexSubSchemas, and overridden where this branch names a definition
	// of its own: the nearest name is the one that describes the shape the
	// value was checked against.
	declaredSensitive := DeclaresSensitive(s.Extra)
	definition := enclosing
	if name, substituted, ok := substituteRefOnly(s); ok && !a.expanding[name] {
		s = substituted
		definition = name
		a.expanding[name] = true
		defer delete(a.expanding, name)
	}
	defer a.enterSensitive(declaredSensitive, s)()

	resolved, err := s.Resolve(nil)
	if err != nil {
		return nil
	}

	bi := &branchIndex{definition: definition, format: s.Format, regions: a.libraryRegions(s)}
	if a.sensitiveDepth > 0 {
		bi.sensitive = s
	}

	// Uses the same ambient-defs threading as indexSubSchemas;
	// nextAmbient = s.Defs post-graft.
	nextAmbient := s.Defs

	// Indexed before the leaf decision, because whether the walk has
	// anything to do at this branch is part of that decision: a branch
	// declaring only array keywords carries no properties and would read as
	// a leaf, and handing it to the library entire is exactly the one call
	// that reports an arbitrary one of an element's equally-failing fields.
	bi.keywords = a.buildPositionKeywords(s, nextAmbient, definition)

	// Leaf-vs-compound decision: a branch is a leaf iff it has no
	// Properties, no nested combinators, no patternProperties, no
	// dependentSchemas, no additionalProperties, and nothing the keyword
	// index picked up.
	isLeaf := s.Properties == nil &&
		len(s.OneOf) == 0 &&
		len(s.AllOf) == 0 &&
		len(s.AnyOf) == 0 &&
		s.Not == nil &&
		len(s.PatternProperties) == 0 &&
		len(s.DependentSchemas) == 0 &&
		s.AdditionalProperties == nil &&
		bi.keywords == nil

	if isLeaf {
		bi.resolved = resolved
		return bi
	}
	bi.subBranches = map[string]*branchIndex{}
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
			// Built through buildBranchIndex rather than resolved directly,
			// so the definitions in scope reach the property (a property
			// that is a reference used to fail resolution here and be
			// dropped) and so a property that is itself an object is walked
			// per-property instead of being handed to the library whole.
			if sub := a.buildBranchIndex(s.Properties[name], nextAmbient, definition); sub != nil {
				bi.subBranches[name] = sub
			}
		}
	}

	// The closed form is excluded for the same reason as at the top level:
	// it constrains no value, and the keyword index carries it.
	if s.AdditionalProperties != nil && !isClosedForm(s.AdditionalProperties) {
		if apBi := a.buildBranchIndex(s.AdditionalProperties, nextAmbient, definition); apBi != nil {
			bi.additionalPropsBranch = map[string]*branchIndex{"": apBi}
		}
	}

	if len(s.OneOf) > 0 {
		branches := make([]*branchIndex, 0, len(s.OneOf))
		for _, b := range s.OneOf {
			if sub := a.buildBranchIndex(b, nextAmbient, definition); sub != nil {
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
			if sub := a.buildBranchIndex(b, nextAmbient, definition); sub != nil {
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
			if sub := a.buildBranchIndex(b, nextAmbient, definition); sub != nil {
				branches = append(branches, sub)
			}
		}
		if len(branches) > 0 {
			bi.anyOfBranches = map[string][]*branchIndex{"": branches}
		}
	}

	if s.Not != nil {
		if notBi := a.buildBranchIndex(s.Not, nextAmbient, definition); notBi != nil {
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
			if sub := a.buildBranchIndex(s.PatternProperties[p], nextAmbient, definition); sub != nil {
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
			if sub := a.buildBranchIndex(s.DependentSchemas[k], nextAmbient, definition); sub != nil {
				entries = append(entries, &dependentEntry{triggerKey: k, branch: sub})
			}
		}
		if len(entries) > 0 {
			bi.dependentSchemas = map[string][]*dependentEntry{"": entries}
		}
	}

	// The branch position's own constraints, built last so it can be told
	// which families the walk evaluates at this branch and therefore which
	// it must not repeat. Keyed on what was indexed above rather than on
	// what the branch declares, so a family the index could not build stays
	// in the reduced copy and keeps being checked.
	_, hasRequired := bi.required[""]
	_, hasAllOf := bi.allOfBranches[""]
	_, hasAnyOf := bi.anyOfBranches[""]
	_, hasOneOf := bi.oneOfBranches[""]
	_, hasNot := bi.notIndexes[""]
	_, hasDependent := bi.dependentSchemas[""]
	reduced := pruneOwnKeywords(s, evaluatedByWalk{
		required:         hasRequired,
		allOf:            hasAllOf,
		anyOf:            hasAnyOf,
		oneOf:            hasOneOf,
		not:              hasNot,
		dependentSchemas: hasDependent,
	}, bi.keywords)
	if reducedResolved, reducedErr := reduced.Resolve(nil); reducedErr == nil {
		bi.ownPruned = reducedResolved
	}

	return bi
}
