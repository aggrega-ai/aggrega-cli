// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/google/jsonschema-go/jsonschema"
)

// Validator is the swap boundary between the compiler core and a
// concrete JSON Schema 2020-12 validator implementation. The package
// supplies googleAdapter as the current implementation; a future
// Wasm-validator socket extraction would supply an alternative that
// honors the same contract.
//
// A Validator instance is constructed for one specific schema (eager
// pre-resolution at construction time). Validate is called many times
// for many instances against that same schema; the per-call cost is
// the library Validate plus template evaluation, with no per-call
// Resolve.
//
// Adapter is intended for short-lived process use. Long-lived consumers
// should refresh the validator periodically or wrap with a bounded LRU
// cache to avoid unbounded memory growth from the resolved-branch cache.
type Validator interface {
	Validate(instance any, ctx *Context) (*Report, error)
}

// Context carries the runtime scope template predicates evaluate
// against. The standard JSON Schema "schema validates instance" model
// has two arguments; this validator's three-argument signature is the
// explicit override surface for x-aggrega-when: and x-aggrega-validate:
// template predicates.
type Context struct {
	// Parameters is the resolved Aggregate parameter map (post the
	// compiler's parameter-interpolation step, pre-validation).
	// Templates reference parameters via `.parameters.X`.
	Parameters map[string]any

	// Surround is the surrounding object scope template predicates
	// reference via `$`. For a when: predicate at a property of a
	// parent object, Surround is the parent object's instance value.
	// At the root, Surround is the instance itself.
	Surround any
}

// New constructs a Validator for the given schema. Eager pre-resolution:
// the schema is resolved once at construction; each x-aggrega-when: and
// x-aggrega-validate: node has its template pre-compiled; then: / else:
// branches are pre-resolved and cached. Resolution errors surface at
// construction time (fail-fast for the calling pipeline).
//
// The schema must not be mutated after New returns; the adapter holds
// references into it.
func New(schema *jsonschema.Schema) (Validator, error) {
	if schema == nil {
		return nil, &AdapterError{
			Message: "schema is nil",
			Err:     ErrSchemaResolve,
		}
	}

	a := &googleAdapter{
		subResolved: map[string]*jsonschema.Resolved{},
		required:    map[string][]string{},
		rootDefs:    schema.Defs,
	}

	// First pass: walk the schema to find x-aggrega-when: and
	// x-aggrega-validate: nodes; pre-compile templates; pre-resolve
	// then: / else: branches.
	if err := a.walk(schema, nil); err != nil {
		return nil, err
	}

	// Second pass: index sub-schemas by instance path. The
	// deterministic walk at Validate time looks up the sub-schema
	// for each instance position from this index, validates the
	// sub-instance against the sub-resolved, and emits findings in
	// sorted-key order. This pre-resolves once per property; per-call
	// validation is library Validate against cached sub-resolveds.
	//
	// The root path (path == "") is the schema's own Resolve. A root
	// resolution failure propagates as ErrSchemaResolve — fail-fast
	// for the calling pipeline. Sub-schema resolution failures (e.g.
	// $ref to root-level $defs from a sub-context) are tolerated by
	// indexSubSchemas and surface as a documented walker-
	// completeness gap rather than a construction error.
	if err := a.indexSubSchemas(schema, "", a.rootDefs); err != nil {
		return nil, err
	}

	return a, nil
}

// googleAdapter is the Validator implementation wrapping
// google/jsonschema-go. Holds pre-resolved state from New; reused
// across many Validate calls.
type googleAdapter struct {
	// subResolved maps an instance path to the library-resolved
	// sub-schema rooted at that path. Populated at construction by
	// walking schema.Properties recursively; the keys are
	// dot-delimited instance paths (e.g. "tlsConfig.certPath"). Used
	// by Validate's deterministic walk to validate each property's
	// sub-instance against its sub-schema in sorted-key order,
	// sidestepping the library's randomised property iteration so
	// multi-violation Reports are byte-identical across builds.
	subResolved map[string]*jsonschema.Resolved

	// required maps an instance path to the sorted list of required
	// property names declared on the schema node at that path. Used
	// by the deterministic walk to emit one finding per missing
	// required key per level — separate from the library's own
	// required check so that "missing X and missing Y" produces two
	// findings in alphabetical order rather than one library finding
	// chosen non-deterministically.
	required map[string][]string

	// whenNodes carries the pre-walked x-aggrega-when: positions with
	// their pre-compiled templates and pre-resolved then: / else:
	// branches. Sorted shallowest-first by instance path length so
	// outer predicates process before nested predicates.
	whenNodes []whenNode

	// validateNodes carries the pre-walked x-aggrega-validate:
	// positions with their pre-compiled templates and validateMessage
	// content.
	validateNodes []validateNode

	// rootDefs holds direct (un-cloned) references to the input
	// schema's $defs entries. New() passes this map as the initial
	// ambient $defs into indexSubSchemas; the walker threads the
	// ambient set through recursion (parent's ambient unioned with
	// each level's local $defs, local overriding) so any non-root
	// sub-schema's $ref → #/$defs/X resolves against the correct
	// enclosing scope. Each graft point in indexSubSchemas clones
	// the ambient entries via CloneSchemas() before installing them
	// into the cloned sub-schema's merged $defs map, so every
	// per-level Resolve() operates on its own independent clone
	// graph and the library's tree-uniqueness invariant (each
	// *Schema appears at most once in a resolved graph) is
	// preserved across the construction sweep. The un-cloned-handle
	// posture matches the adapter's "schema must not be mutated
	// after New returns" precondition: rootDefs is a read-only
	// pointer into the input schema.
	rootDefs map[string]*jsonschema.Schema
}

// whenNode is a single x-aggrega-when: position in the schema with
// its evaluation state pre-prepared.
type whenNode struct {
	// instancePath is the path from the root instance to the position
	// where this x-aggrega-when: applies. Used both to navigate the
	// instance at Validate time and to filter library findings whose
	// FieldPath sits under this subtree.
	instancePath []string

	// tpl is the pre-compiled x-aggrega-when: template.
	tpl *template.Template

	// thenResolved is the pre-resolved then: branch. nil when the
	// schema authored no then: clause (rare in compact-form output).
	thenResolved *jsonschema.Resolved

	// elseResolved is the pre-resolved else: branch. nil when the
	// author omitted else: (D-001b-2 semantic — no constraint when
	// falsy).
	elseResolved *jsonschema.Resolved
}

// validateNode is a single x-aggrega-validate: position with its
// pre-compiled template and the accompanying message.
type validateNode struct {
	// instancePath is the path from the root instance to the field
	// the validate: predicate constrains.
	instancePath []string

	// tpl is the pre-compiled x-aggrega-validate: template.
	tpl *template.Template

	// message is the x-aggrega-validateMessage: content; emitted as
	// the Finding's Message when the predicate evaluates falsy.
	message string
}

// sprigFuncs is the function map registered with every template the
// adapter compiles. Matches the convention Recipe valuesMapping: uses
// so authors get the same surface across schema constraints and
// rendering templates.
var sprigFuncs = sprig.HermeticTxtFuncMap()
