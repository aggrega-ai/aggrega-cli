// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonschema

import (
	"maps"
	"regexp"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
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
	// Predicates reach it through the param and parameters template
	// functions — param "X" returns one parameter's value, parameters
	// the whole map. It is not reachable from the template's data root,
	// which stays the value being validated.
	Parameters map[string]any

	// Surround is the surrounding object scope template predicates
	// reference via `$`. For a when: predicate at a property of a
	// parent object, Surround is the parent object's instance value.
	// At the root, Surround is the instance itself.
	Surround any

	// SkipPredicates requests predicate-inert validation: the conditional
	// extension keywords (x-aggrega-when: / x-aggrega-validate:) are NOT
	// evaluated, and every when-gated constraint is treated as not-applicable
	// (its findings are dropped without evaluating the predicate). Only the
	// unconditional structural constraints (type, range, enum, required,
	// closed-ness) produce findings. This is for callers that lack the correct
	// parameter context for the predicates and must validate structure only
	// rather than evaluate predicates against a wrong (or empty) context.
	SkipPredicates bool
}

// New constructs a Validator for the given schema. Eager pre-resolution:
// the schema is resolved once at construction; each x-aggrega-when: and
// x-aggrega-validate: node has its template pre-compiled; then: / else:
// branches are pre-resolved and cached. Resolution errors surface at
// construction time (fail-fast for the calling pipeline).
//
// The schema must not be mutated after New returns; the adapter holds
// references into it.
//
// Each Option adjusts how the Validator reports what it finds; with none it
// reports as it always has.
func New(schema *jsonschema.Schema, opts ...Option) (Validator, error) {
	if schema == nil {
		return nil, &AdapterError{
			Message: "schema is nil",
			Err:     ErrSchemaResolve(),
		}
	}
	var chosen options
	for _, opt := range opts {
		opt(&chosen)
	}

	// A format name outside the supported set is refused before anything is
	// built, wherever in the schema it sits, so a misspelt or unsupported name is
	// a diagnostic when the schema loads rather than a constraint that checks
	// nothing.
	if err := refuseUnsupportedFormats(schema, false); err != nil {
		return nil, err
	}

	a := &googleAdapter{
		root:                  schema,
		formats:               map[string]string{},
		regions:               map[string][]*jsonschema.Schema{},
		unresolvedNodes:       map[string]*jsonschema.Schema{},
		subResolved:           map[string]*jsonschema.Resolved{},
		subPruned:             map[string]*jsonschema.Resolved{},
		subDefinition:         map[string]string{},
		required:              map[string][]string{},
		rootDefs:              schema.Defs,
		additionalPropsBranch: map[string]*branchIndex{},
		oneOfBranches:         map[string][]*branchIndex{},
		allOfBranches:         map[string][]*branchIndex{},
		anyOfBranches:         map[string][]*branchIndex{},
		notIndexes:            map[string]*branchIndex{},
		patternPropsResolved:  map[string][]*patternEntry{},
		dependentSchemas:      map[string][]*dependentEntry{},
		keywords:              map[string]*positionKeywords{},
		expanding:             map[string]bool{},
		withholdSensitive:     chosen.withholdSensitive,
		withholdEvery:         chosen.withholdEvery,
		sensitive:             map[string]*jsonschema.Schema{},
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
	if err := a.indexSubSchemas(schema, "", a.rootDefs, "", ""); err != nil {
		return nil, err
	}

	// Positions the sweep could not stand up on their own are checked
	// against the whole document per call, and ordering them by document
	// location here is what keeps that check's attribution independent of
	// the order the sweep happened to visit them in.
	a.sortUnresolvedPositions()

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

	// subPruned mirrors subResolved with the schema at each path reduced
	// to the constraints that apply to that position only — its declared
	// property names and every local keyword intact, the property values
	// replaced by schemas constraining nothing (see pruneOwnKeywords).
	// The deterministic walk validates against this rather than against
	// subResolved, because a library call over a whole subtree reports
	// whichever equally-failing child its map iteration reached first and
	// so varies between runs. Each child is reached by the walk itself, in
	// sorted key order, so nothing goes unchecked.
	//
	// A path is absent only when the reduced schema fails to resolve,
	// which cannot happen for a path whose full schema resolved; the walk
	// falls back to subResolved in that case, keeping the coverage it
	// would otherwise lose. TestEveryIndexedPathHasAPrunedSchema pins the
	// absence to zero for the shipped schemas.
	subPruned map[string]*jsonschema.Resolved

	// subDefinition maps an instance path to the name of the reusable
	// definition that was substituted for a reference at that position.
	// Absent for a position that referenced nothing.
	//
	// The name is what a diagnostic at that position says was violated. It
	// has to be recorded because the substituted definition becomes the
	// root of the library call, and a library names its own call root as
	// the root — so the definition's name appears in the message only if
	// the adapter puts it there.
	subDefinition map[string]string

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

	// unresolved names the positions that could not be stood up as
	// schemas in their own right, each with its location in the schema
	// document. A position lands here when its own reference — or one
	// below it — names something a standalone position does not carry,
	// which is every reference shape but a name in the definitions in
	// scope. Empty for a schema whose references all name definitions,
	// which is what makes the per-call check over the whole document cost
	// nothing at all for such a schema.
	//
	// The entries are what turns a blind spot into a checked position. A
	// position that cannot be stood up is not descended into either, so
	// its whole subtree is invisible to the walk; recording it lets the
	// per-call check reach that subtree through the document, where the
	// reference does resolve. Sorted by document location at construction,
	// so attribution does not depend on visit order.
	unresolved []unresolvedPosition

	// additionalPropsBranch maps a parent path to its
	// AdditionalProperties-as-schema branchIndex. Populated only when
	// the schema node at the path carries AdditionalProperties as a
	// non-nil *Schema (vs the boolean-shorthand case). Carries the
	// recursive branchIndex shape so nested-Properties
	// additionalProperties (e.g., the parameter-contract's
	// {oneOf: [scalar, typed-form]}) walk deterministically through
	// walkBranch.
	additionalPropsBranch map[string]*branchIndex

	// oneOf/allOf/anyOf/notIndexes map a path to the ordered list (or
	// single, for not) of branchIndex sub-structures. The recursive
	// branch walk reads these shapes via walkBranch rather than calling
	// the library's randomised root validation.
	oneOfBranches map[string][]*branchIndex
	allOfBranches map[string][]*branchIndex
	anyOfBranches map[string][]*branchIndex
	notIndexes    map[string]*branchIndex

	// patternPropsResolved maps a parent path to its patternProperties
	// entries — each is a compiled regex + the sub-schema's
	// branchIndex. Pattern entries are sorted alphabetically by
	// pattern string at index time so iteration at walk time is
	// deterministic (the library's PatternProperties iteration is
	// randomised).
	patternPropsResolved map[string][]*patternEntry

	// expanding names the reusable definitions currently being substituted
	// for a reference during index construction. A schema may define a
	// structure that contains itself — a subsystem holding subsystems, a
	// provenance record pointing at a provenance record — and substituting
	// such a definition for its own reference would never terminate. A
	// definition already being expanded is left as the reference it was,
	// which is the behaviour before substitution existed.
	//
	// Construction is single-threaded (New builds the index before
	// returning the adapter), so a plain map needs no guarding.
	expanding map[string]bool

	// dependentSchemas maps a parent path to its trigger-key-keyed
	// dependent sub-schemas. Each entry is (triggerKey,
	// dependentBranchIndex). Sorted alphabetically by triggerKey at
	// index time for deterministic walk-time iteration.
	dependentSchemas map[string][]*dependentEntry

	// keywords maps an instance path to the remaining keyword families the
	// walk evaluates there — the array keywords, the key-string and
	// key-count constraints, the conditional applicator, closedness, and the
	// leftover-value constraints. Absent for a position declaring none of
	// them, which is the signal that nothing beyond the declared-property
	// families needs walking there.
	keywords map[string]*positionKeywords

	// root is the schema document New was handed, read to resolve an
	// in-document reference inside a region the format sweep covers.
	root *jsonschema.Schema

	// formats maps an instance path to the format its position declares, for
	// the positions that declare one. The library ignores the keyword, so the
	// walk checks it beside the constraints the library checks there.
	formats map[string]string

	// regions maps an instance path to the sub-schemas the library evaluates
	// whole at that position on the walk's behalf, each swept for format; see
	// libraryRegions.
	regions map[string][]*jsonschema.Schema

	// unresolvedNodes maps the instance path of each position that could not be
	// stood up on its own to its schema, so the format sweep reaches the subtree
	// the whole-document check covers for everything else.
	unresolvedNodes map[string]*jsonschema.Schema

	// withholdSensitive reports that a finding at a position whose value is
	// sensitive carries no part of that value; see WithholdSensitiveValues.
	withholdSensitive bool

	// withholdEvery, when not empty, is the notice closing every finding's
	// sentence, none of which carries any part of the value it found; see
	// WithholdEveryValue.
	withholdEvery string

	// sensitive maps the instance path of each position the index built whose
	// value is sensitive — declared there, or inside a position that declares it
	// — to that position's schema. Recorded whether or not withholdSensitive is
	// set, and read only where it is.
	sensitive map[string]*jsonschema.Schema

	// sensitiveDepth counts the positions being built, from the root down to the
	// one in hand, whose schemas declare their value sensitive, so a position
	// inside one inherits the declaration. Construction only; see enterSensitive.
	sensitiveDepth int
}

// branchIndex carries a single combinator branch's (or
// AdditionalProperties / patternProperties value's / dependentSchemas
// value's) pre-indexed validation state. Mirrors googleAdapter's
// top-level maps at the branch scope so walkBranch can validate
// deterministically without invoking the library's randomised root
// validation.
//
// A branch is a leaf when it declares no properties, no nested
// combinators (oneOf/allOf/anyOf/not), no patternProperties, no
// dependentSchemas and no additionalProperties — the resolved field is
// populated for exactly those, and nil otherwise. A leaf's constraints
// (type, pattern, enum, range) produce one stable message per call with
// no map iteration behind them, so validating against it directly is
// deterministic. A compound branch is walked construct by construct
// instead, and the constraints belonging to the branch position itself
// come from ownPruned.
//
// A branch that carries only a reference to one of the schema's own
// definitions is classified on the definition it names, not on the
// reference: a reference is the shape of a leaf while what it names may
// declare a whole object, and reading the reference alone would hand that
// object to the library entire.
type branchIndex struct {
	// resolved carries a leaf branch's constraints. nil for a compound
	// branch.
	resolved *jsonschema.Resolved

	// definition names the reusable definition this branch was built from,
	// when the branch carried only a reference to one, and is empty
	// otherwise. It is what a diagnostic for this branch says was
	// violated: the definition is the root of the library call made here,
	// and a library names its own call root as the root.
	definition string

	// ownPruned carries a compound branch's own constraints — the
	// declared property names, closedness, the property-count bounds, the
	// array keywords, the in-place applicators — with the property values
	// pruned away, so validating against it cannot descend into a child.
	// nil for a leaf branch, whose resolved covers the same ground.
	ownPruned *jsonschema.Resolved

	// subBranches indexes a compound branch's declared properties by
	// property name. Each entry is a full branchIndex rather than a bare
	// resolved schema, so a property that is itself an object is walked
	// property by property instead of being handed to the library whole,
	// and a property naming one of the schema's own definitions resolves
	// against the definitions in scope rather than being dropped.
	subBranches map[string]*branchIndex

	required              map[string][]string
	additionalPropsBranch map[string]*branchIndex
	oneOfBranches         map[string][]*branchIndex
	allOfBranches         map[string][]*branchIndex
	anyOfBranches         map[string][]*branchIndex
	notIndexes            map[string]*branchIndex
	patternPropsResolved  map[string][]*patternEntry
	dependentSchemas      map[string][]*dependentEntry

	// keywords mirrors the adapter's per-path keyword index at this branch's
	// own position, so a keyword is evaluated inside a combinator branch
	// exactly as it is at the top level.
	keywords *positionKeywords

	// format and regions mirror the adapter's per-path format index at this
	// branch's own position, so a format inside a combinator branch takes part
	// in deciding whether the branch matches, as every other constraint does.
	format  string
	regions []*jsonschema.Schema

	// sensitive is this branch's schema when the value at its position is
	// sensitive — declared on the branch, or on a position enclosing it — and
	// nil otherwise.
	sensitive *jsonschema.Schema
}

// patternEntry pairs a compiled regex with its sub-schema's
// branchIndex. Stored in alphabetically-sorted slices keyed by parent
// path so walk-time iteration is deterministic.
type patternEntry struct {
	pattern *regexp.Regexp
	branch  *branchIndex
}

// dependentEntry pairs a trigger key with its dependent sub-schema's
// branchIndex. Stored in alphabetically-sorted slices keyed by parent
// path so walk-time iteration is deterministic.
type dependentEntry struct {
	triggerKey string
	branch     *branchIndex
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
	// author omitted else:, in which case no constraint applies when the
	// predicate is falsy.
	elseResolved *jsonschema.Resolved

	// elseSchema is the else: branch as authored, swept for format when the
	// predicate is falsy, since the library checking elseResolved ignores it.
	elseSchema *jsonschema.Schema
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
//
// `param` and `parameters` give x-aggrega-when / x-aggrega-validate predicates
// access to the resolved Aggregate parameter map: `param "X"` returns one
// parameter's value, or nil where no such parameter was supplied, and
// `parameters` returns the whole map. They are exposed as functions rather than a
// data-root field so the dot stays the field value / surrounding object exactly
// as before — predicates that do not call them render identically. Registered
// here as placeholders (so templates referencing them parse); the real
// closures over the per-validation Context.Parameters are bound per evaluation
// in evaluateTemplate via a template clone, since the parameter map is known
// only at Validate time, not at compile time.
//
// `index` is the shared template layer's replacement for Go's own, merged over
// the Sprig set so a predicate reads a map exactly as every other template
// surface does: indexing a map with a key it does not hold halts naming the key.
// Reading `(parameters)` through `index` therefore halts on a parameter nobody
// supplied, where `param` answers nil.
var sprigFuncs = func() template.FuncMap {
	m := sprig.HermeticTxtFuncMap()
	m["param"] = func(string) any { return nil }
	m["parameters"] = func() map[string]any { return nil }
	// Iterating a map is safe here where it usually would not be: the loop only
	// assigns distinct keys into another map, so the merged set is the same
	// whatever order the keys arrive in.
	maps.Copy(m, gotmpl.BuiltinReplacements())
	return m
}()
