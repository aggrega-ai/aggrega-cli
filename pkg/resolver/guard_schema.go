// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"encoding/json"
	"fmt"
	"strings"

	libschema "github.com/google/jsonschema-go/jsonschema"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// EdgeValues is the per-edge developer `values` block one edge of a socket
// carries: the alias identifying the edge, and the block itself, which is empty
// when the author wrote none.
type EdgeValues struct {
	Alias  string
	Values types.OrderedMap[any]
}

// CompactItemNamesTarget reports whether one item of a socket's compact `uses`
// list names a target. An item the author left blank names none.
//
// This is the home of that rule, so a reader deciding what a compact list
// contributes asks it rather than testing the item against the empty string in
// place. A blank item is a list entry the author opened and wrote nothing in: it
// identifies no connection, so it populates no socket and it carries no per-edge
// configuration. Those two readings have to agree. A reader that counts a blank
// item as a connection while another reads it as nothing reports against an
// edge the rest of the build agrees does not exist, and locates it by an empty
// alias — which tells the author about a connection instead of about the list
// entry they left empty.
func CompactItemNamesTarget(item string) bool {
	return item != ""
}

// EdgeValuesOf projects one socket's authored `uses` value into one entry per
// edge, in author order, whichever form the author used: a compact list yields
// an entry per target it names, with the target as its alias, and a verbose map
// yields an entry per alias.
//
// An edge that authors no per-edge values yields an entry carrying an empty
// block rather than being omitted. That is the whole point of the projection: a
// socket's valueSchema may declare a key as required, and an edge that writes
// nothing is the maximal violation of such a key rather than a case with nothing
// to check. Omitting it would enforce a required key against every edge that
// authors something and against no edge that authors nothing.
//
// A compact item that names no target is the one case with genuinely nothing to
// check, and it is distinct from the empty edge above: there is no edge to
// measure a contract against, so the item yields no entry. [CompactItemNamesTarget]
// carries that rule, and a reader that re-derives per-edge judgement from a
// socket's authored entries reaches it through the same call — a second
// emptiness test in another reader is how the two answers drift apart.
//
// Both authoring forms are projected here so a socket contract is enforced the
// same way whichever the author chose. A compact edge can never satisfy a
// required per-edge key — the form has nowhere to write one — which is a fact
// about the contract the author declared, not a reason to exempt the form.
func EdgeValuesOf(sv types.UsesSocketValue) []EdgeValues {
	if len(sv.Compact) > 0 {
		out := make([]EdgeValues, 0, len(sv.Compact))
		for _, target := range sv.Compact {
			if !CompactItemNamesTarget(target) {
				continue
			}
			out = append(out, EdgeValues{Alias: target})
		}
		return out
	}
	if sv.Verbose.Len() == 0 {
		return nil
	}
	out := make([]EdgeValues, 0, sv.Verbose.Len())
	for alias, e := range sv.Verbose.All() {
		out = append(out, EdgeValues{Alias: alias, Values: e.Values})
	}
	return out
}

// CompileSocketPillarSchema compiles a socket's per-edge contract for one
// pillar so it can be fed to the shared [ValidatePillarConformance] entry,
// which expects a pillar-keyed schema (properties.<pillar>). The socket's
// valueSchema / infraSchema is authored as bare pillar content (a field →
// constraint map), so it is wrapped under a synthetic <pillar> key before
// compilation. Returns false when the component type is unknown, the socket is
// undeclared, the socket declares no schema for the pillar (Kind == 0), or the
// content fails to compile.
func CompileSocketPillarSchema(catalog CatalogLookup, compType, socket, pillar string) (compactform.Schema, bool) {
	compiled, declared, err := compileSocketPillarSchema(catalog, compType, socket, pillar)
	if !declared || err != nil {
		return nil, false
	}
	return compiled, true
}

// compileSocketPillarSchema is the classifying form of the socket-contract
// compile, and the home of it: it separates a socket that declares no contract
// for the pillar from one whose declared contract failed to compile, which the
// bool-returning entry above cannot express.
//
// The distinction exists because the two require opposite treatment and only one
// of them is correct to pass over silently. A socket declaring no contract has
// nothing to measure an edge against, and skipping is right. A declared contract
// that fails to compile has something to measure against that nobody can read,
// and skipping it retires a diagnostic without saying so — which is precisely
// how a check comes to stop firing with nothing to show that it did.
//
// The returned declared flag reports whether a contract was authored at all; err
// is why an authored one did not compile. A schema and a nil error together are
// the only combination that means there is a contract to read, and a contract
// that compiles to nothing is a non-nil empty schema rather than the nil one an
// absence returns.
func compileSocketPillarSchema(
	catalog CatalogLookup, compType, socket, pillar string,
) (compactform.Schema, bool, error) {
	catComp, found := lookupComponent(catalog, compType)
	if !found {
		return nil, false, nil
	}
	sock, ok := catComp.Spec.UsesSchema.Get(socket)
	if !ok {
		return nil, false, nil
	}
	var node *yaml.Node
	switch pillar {
	case "values":
		node = &sock.ValueSchema
	case "infra":
		node = &sock.InfraSchema
	default:
		return nil, false, nil
	}
	if node.Kind == 0 {
		return nil, false, nil
	}
	wrapper := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: pillar},
			node,
		},
	}
	compiled, err := compactform.Compile(wrapper, schemaLibraryOptions(catalog)...)
	if err != nil {
		return nil, true, err
	}
	return compiled, true, nil
}

// ValidatePillarConformance validates a resolved or authored pillar instance
// (values or infra) against the named pillar of an already-compiled
// Component-or-Feature schema, closed. It is the shared schema-conformance
// entry point: the component values gate at resolution and the Feature/Secret
// attachment gate at graph finalization both route through it, so an authored
// schema is interpreted identically wherever the value originates.
//
// The pillar sub-schema is extracted from compiled; schema-declared defaults for
// that pillar are merged into the instance (default-aware) before validation;
// the validator carries the compiled schema's own definitional context so a
// single-document #/$defs $ref or allOf inside the pillar resolves rather than
// being skipped; findings are deduped to the leaf-most. Each violation is a
// *ResolverError wrapping ErrSchemaConformance, its Field rooted at
// fieldPrefix + "." + <pillar>.<path>, its Message naming schemaLabel.
//
// A violation on a leaf the schema declares sensitive carries a fixed
// placeholder where the constraint detail would go, because that detail quotes
// the offending value and a diagnostic is read by anyone watching the build.
// The Entity, the Field, the pillar, the contract name and the failing keyword
// are all retained, so a reader still learns which leaf failed — only the text
// that would reproduce the withheld value is dropped.
//
// Returns nil when compiled declares no such pillar (nothing to validate), when
// the instance does not marshal, or when the instance conforms. The returned
// slice is NOT sorted — callers that aggregate across multiple instances
// canonicalise the combined output.
func ValidatePillarConformance(
	compiled compactform.Schema,
	pillar string,
	instance types.OrderedMap[any],
	parameters map[string]any,
	entity, fieldPrefix, schemaLabel string,
) []*ResolverError {
	return validatePillarConformance(compiled, pillar, instance, parameters, entity, fieldPrefix, schemaLabel)
}

// CompileComponentSchema compiles a Component type's declared schema for
// conformance validation. Returns (nil, false) when the catalog has no such
// component, the component declares no schema, or compilation fails. Uncached —
// callers that validate many instances of the same type should memoise.
//
// A Component's authored schema is the only schema there is to compile, on an
// extending Component as much as a standalone one: extending copies the base's
// pillar block down verbatim, so an inherited constraint is already present in
// the authored node. Do not add a second, composed view for the extending case
// — two views of one Component's contract is how an inherited constraint came
// to be enforced on one path and not another.
func CompileComponentSchema(catalog CatalogLookup, compType string) (compactform.Schema, bool) {
	compiled, declared, err := compileComponentSchema(catalog, compType)
	if !declared || err != nil {
		return nil, false
	}
	return compiled, true
}

// compileComponentSchema is the classifying form of the component-contract
// compile, and the home of it. It separates a type declaring no contract from
// one whose declared contract failed to compile, for the reason its socket-side
// sibling records: only one of those two is correct to pass over silently, and
// the bool-returning entry above reports them identically.
//
// The library set is not an option here. A caller that omitted it would compile
// a contract whose cross-document references cannot resolve, and the failure
// would arrive as the same not-ok an absent contract returns — so every
// component whose contract reaches another document would quietly stop being
// measured. The libraries come from the catalog this reads through, which is the
// one place they can be missing from without the omission being a choice a
// caller made.
func compileComponentSchema(
	catalog CatalogLookup, compType string,
) (compactform.Schema, bool, error) {
	catComp, found := lookupComponent(catalog, compType)
	if !found || catComp.Spec.Schema.Kind == 0 {
		return nil, false, nil
	}
	compiled, err := compactform.Compile(&catComp.Spec.Schema, schemaLibraryOptions(catalog)...)
	if err != nil {
		return nil, true, err
	}
	return compiled, true, nil
}

// schemaLibraryOptions is how a compile in this package reaches the catalog's
// resolved library set, stated once so the two compiles cannot come to disagree
// about whether cross-document references resolve.
func schemaLibraryOptions(catalog CatalogLookup) []compactform.CompileOption {
	if len(catalog.SchemaLibraries) == 0 {
		return nil
	}
	return []compactform.CompileOption{compactform.WithSchemaLibraries(catalog.SchemaLibraries)}
}

func validatePillarConformance(
	compiled compactform.Schema,
	pillar string,
	instance types.OrderedMap[any],
	parameters map[string]any,
	entity, fieldPrefix, schemaLabel string,
) []*ResolverError {
	findings := pillarConformanceFindings(compiled, pillar, instance, parameters)
	errs := make([]*ResolverError, 0, len(findings))
	for _, f := range findings {
		field := fieldPrefix
		if f.path != "" {
			field = fieldPrefix + "." + f.path
		}
		msg := f.reason
		if f.keyword != "" {
			msg = fmt.Sprintf("%s (%s)", msg, f.keyword)
		}
		errs = append(errs, &ResolverError{
			Entity:  entity,
			Field:   field,
			Message: fmt.Sprintf("%s pillar does not conform to %s: %s", pillar, schemaLabel, msg),
			Err:     ErrSchemaConformance(),
		})
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

// pillarFinding is one way a pillar instance fails the contract declared over
// it: the path the failure landed on, which leads with the pillar name, the
// constraint keyword that failed, and why — with the reason withheld when the
// leaf is one the contract declares sensitive.
//
// It is the conformance answer before anything is done with it. Two callers
// need that answer in two shapes — one builds the finding shape a resolve
// reports across, the other raises it as an objection carrying provenance
// supplied to it — and both read this, so neither can hold a different idea of
// what conforms.
type pillarFinding struct {
	path    string
	keyword string
	reason  string
}

// pillarConformanceFindings validates a pillar instance against the named
// pillar of an already-compiled schema, closed and default-aware, and returns
// one finding per independent violation.
//
// The pillar instance is wrapped one level deep ({<pillar>: instance} against
// {properties: {<pillar>: schema}}) so the findings' paths lead with the pillar
// name (values.x / infra.x), and the compiled schema's root definitional
// context ($defs / definitions) is carried onto the wrapper so a
// single-document $ref/allOf inside the pillar schema resolves. Only the
// leaf-most findings are kept — the validator reports a failure at every level
// along a path — so one violation yields one finding while independent
// violations stay distinct.
//
// This is the one site holding both a finding's value-bearing text and the
// schema that says whether the leaf it failed on is sensitive; downstream every
// consumer sees an opaque string with that fact already gone. So the
// substitution happens here, and the authored declarations are read through
// their single home rather than recomputed.
//
// Returns nil when the schema declares no such pillar, when the instance does
// not marshal, when the wrapper does not compile, or when the instance
// conforms.
func pillarConformanceFindings(
	compiled compactform.Schema,
	pillar string,
	instance types.OrderedMap[any],
	parameters map[string]any,
) []pillarFinding {
	pillarSchema, ok := pillarSubSchema(compiled, pillar)
	if !ok {
		return nil // no such pillar declared → nothing to validate
	}

	effective := compactform.MergeSchemaDefaults(instance, compactform.PillarDefaults(compiled, pillar))
	normalised, err := normalisePillarInstance(effective)
	if err != nil {
		return nil // a non-marshalable instance is not a conformance violation
	}

	wrapper := map[string]any{
		"type":       "object",
		"properties": map[string]any{pillar: pillarSchema},
	}
	// Carry the root definitional context so a single-document $ref/allOf in the
	// pillar schema resolves within the wrapper document (2020-12 $defs and the
	// draft-07 definitions alias, whichever the compiled schema carries).
	if defs, present := compiled["$defs"]; present {
		wrapper["$defs"] = defs
	}
	if defs, present := compiled["definitions"]; present {
		wrapper["definitions"] = defs
	}
	wrapped := map[string]any{pillar: normalised}

	v, err := newPillarValidator(wrapper)
	if err != nil {
		return nil // a genuinely uncompilable schema is surfaced at catalog load, not here
	}

	rpt, err := v.Validate(wrapped, &jsonvalidator.Context{Parameters: parameters, Surround: wrapped})
	if err != nil || rpt == nil || len(rpt.Findings) == 0 {
		return nil
	}

	// The validator emits a finding at every level along a failure path (the
	// failing leaf plus each ancestor object). Keep only the leaf-most findings
	// — drop any whose FieldPath is a strict segment-wise prefix of another's —
	// so one violation maps to one finding while independent violations stay
	// distinct.
	leaves := leafmostFindings(rpt.Findings)

	declared := DeclaredSensitiveLeaves(compiled)

	out := make([]pillarFinding, 0, len(leaves))
	for _, f := range leaves {
		reason := f.Message
		if findingOnSensitiveLeaf(declared, pillar, f.FieldPath) {
			reason = redact.Placeholder()
		}
		out = append(out, pillarFinding{path: f.FieldPath, keyword: f.Keyword, reason: reason})
	}
	return out
}

// findingOnSensitiveLeaf reports whether a validator finding landed on a leaf
// the entity's schema declares sensitive, or anywhere inside an object it
// declares sensitive. fieldPath is the instance path the finding carries, which
// leads with the pillar name; a path naming no leaf inside the pillar — the
// pillar root, or the instance root — reports false. Only values and infra are
// validated through this entry, and an unrecognised pillar reports false rather
// than guessing.
//
// The walk up the containing objects matters as much as the leaf itself: a
// declaration on an object withholds everything beneath it, so without the walk
// a constraint failing two levels down would print material an author asked to
// be withheld. Nothing about the value is examined — a path is sensitive
// because the schema says so.
func findingOnSensitiveLeaf(declared SensitiveLeaves, pillar, fieldPath string) bool {
	var paths map[string]bool
	switch pillar {
	case "values":
		paths = declared.Values
	case "infra":
		paths = declared.Infra
	}
	if len(paths) == 0 {
		return false
	}
	prefix := pillar + "."
	if !strings.HasPrefix(fieldPath, prefix) {
		return false
	}
	for leaf := strings.TrimPrefix(fieldPath, prefix); leaf != ""; leaf = containingLeafPath(leaf) {
		if paths[leaf] {
			return true
		}
	}
	return false
}

// containingLeafPath returns the dotted path of the object containing the named
// leaf, and the empty string when the leaf sits at the pillar root.
func containingLeafPath(path string) string {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return ""
	}
	return path[:i]
}

// leafmostFindings returns the findings whose FieldPath is NOT a strict
// segment-wise prefix of another finding's FieldPath. The validator reports a
// failure at every level along the path (the leaf plus each containing object),
// so keeping only the most-specific entries yields one error per independent
// violation while preserving multiple distinct violations. Comparison is
// segment-wise (split on ".") so "values.d" is not treated as a prefix of
// "values.db".
func leafmostFindings(findings []jsonvalidator.Finding) []jsonvalidator.Finding {
	out := make([]jsonvalidator.Finding, 0, len(findings))
	for i, f := range findings {
		prefixOfAnother := false
		for j, g := range findings {
			if i == j {
				continue
			}
			if isStrictSegmentPrefix(f.FieldPath, g.FieldPath) {
				prefixOfAnother = true
				break
			}
		}
		if !prefixOfAnother {
			out = append(out, f)
		}
	}
	return out
}

// isStrictSegmentPrefix reports whether a is a strict segment-wise prefix of b
// (a != b, and a's "."-split segments are an initial run of b's). The empty path
// (the instance root) is a strict prefix of any non-empty path.
func isStrictSegmentPrefix(a, b string) bool {
	if a == b || b == "" {
		return false
	}
	if a == "" {
		return true
	}
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	if len(as) >= len(bs) {
		return false
	}
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// pillarSubSchema returns compiled.properties.<pillar> as a JSON Schema map.
func pillarSubSchema(compiled compactform.Schema, pillar string) (map[string]any, bool) {
	props, ok := compiled["properties"].(map[string]any)
	if !ok {
		return nil, false
	}
	sub, ok := props[pillar].(map[string]any)
	return sub, ok
}

// pillarProperties returns the compiled property map of one named pillar inside
// a whole-entity compiled schema, and false when the schema declares no such
// pillar or the pillar declares no properties.
//
// A whole-entity compile nests a pillar one level deeper than a compile of the
// pillar node alone — properties -> <pillar> -> properties — so a reader holding
// a whole-entity schema takes one hop more than a reader holding the pillar
// alone. It layers on pillarSubSchema rather than repeating its navigation, so
// the two cannot disagree about where a pillar sits.
func pillarProperties(compiled compactform.Schema, pillar string) (map[string]any, bool) {
	sub, ok := pillarSubSchema(compiled, pillar)
	if !ok {
		return nil, false
	}
	props, ok := sub["properties"].(map[string]any)
	if !ok {
		return nil, false
	}
	return props, true
}

// newPillarValidator round-trips a compiled schema into the library schema type
// and constructs the validator.
func newPillarValidator(schema map[string]any) (jsonvalidator.Validator, error) {
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	libSchema := &libschema.Schema{}
	if err := json.Unmarshal(schemaJSON, libSchema); err != nil {
		return nil, err
	}
	return jsonvalidator.New(libSchema)
}

// normalisePillarInstance converts an authored OrderedMap pillar into the
// canonical generic value the validator consumes (JSON round-trip).
func normalisePillarInstance(pillar types.OrderedMap[any]) (any, error) {
	raw, err := json.Marshal(pillar)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
