// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// SensitiveLeaves records which leaves an entity's schema declares sensitive,
// one set per pillar, keyed by the flat-dotted property path from the pillar
// root. The path spelling matches the one the per-leaf provenance maps use, so a
// caller holding both can join them by key.
//
// The three pillars are kept apart because a path is unique only within its
// pillar: an entity may declare `host` in `values` and again in `provides` and
// mean different data.
type SensitiveLeaves struct {
	Values   map[string]bool
	Infra    map[string]bool
	Provides map[string]bool
}

// Any reports whether the entity declares at least one sensitive leaf in any
// pillar. It is the one home for "does this entity declare sensitivity at all",
// so no caller has to decide which pillars that question spans.
func (s SensitiveLeaves) Any() bool {
	return len(s.Values) > 0 || len(s.Infra) > 0 || len(s.Provides) > 0
}

// Covers reports whether a pillar-leaf coordinate — the "values.<path>" /
// "infra.<path>" spelling a display prints and the graph carries — names data
// this entity's schema declares sensitive. A declaration on the coordinate
// itself covers it, and so does a declaration on any object containing it.
//
// It is the one home for "is this authored datum withheld", so every surface
// that shows a values-or-infra leaf asks the same question of the same
// declarations and cannot come to a different answer. The two halves it
// composes each have a home of their own: the coordinate spelling and the
// covering rule are both read from the shared leaf-coordinate primitives, so
// neither is restated here.
//
// A coordinate under any other pillar reports false rather than guessing which
// set was meant. That is not a narrowing of the axis: a provides key is
// governed by the declarations of the entity that PUBLISHES it, so it is asked
// through the edge that carries it rather than against the consumer's own
// declarations.
func (s SensitiveLeaves) Covers(coordinate string) bool {
	pillar, path, ok := types.SplitPillarCoordinate(coordinate)
	if !ok {
		return false
	}
	return types.PathCoveredBy(path, s.withheldPillar(pillar))
}

// holdsBeneath reports whether a declared path lies strictly beneath a position
// in one pillar: the position is an object holding, somewhere inside it, data
// this entity's schema declares sensitive. path is the flat-dotted path from the
// pillar root, and the empty path is the pillar root itself, which holds every
// declaration the pillar makes.
//
// It is the downward half of what Covers answers upward, and a surface needs it
// wherever something reports a whole position at once. A report at a position
// may reproduce the value written there, and that value contains the values
// beneath it, so a declaration below the position reaches the report even though
// it does not cover the position itself. Matching is segment-wise, as in Covers:
// a declaration at dbPasswordExtra does not lie beneath db.
//
// Only values and infra are read, for the reason Covers gives.
func (s SensitiveLeaves) holdsBeneath(pillar, path string) bool {
	for declared := range s.withheldPillar(pillar) {
		if path == "" || strings.HasPrefix(declared, path+".") {
			return true
		}
	}
	return false
}

// withheldPillar returns the declared set of one pillar whose authored data a
// surface withholds, and nil for any other pillar name. It is the one statement
// of which pillars those are, read by both directions of the covering question.
func (s SensitiveLeaves) withheldPillar(pillar string) map[string]bool {
	switch pillar {
	case "values":
		return s.Values
	case "infra":
		return s.Infra
	}
	return nil
}

// DeclaredSensitiveLeaves reads the sensitivity declarations out of an entity's
// compiled schema. The input is the whole-entity compact-form compile — the same
// schema the pillar-conformance and declared-defaults readers consume — so an
// entity that inherits a declaration through `extends` reports it exactly as it
// reports an inherited constraint.
//
// A leaf declares sensitivity by carrying the `x-aggrega-sensitive` extension
// keyword with the boolean value true, which is what compact-form `sensitive:
// true` emits. A collected path is always the dotted property chain from the
// pillar root — the path the authored datum occupies — and a declaration is
// collected from any of four positions:
//
//   - On a property, at any `properties` nesting depth.
//   - BESIDE a `$ref` or a composition keyword, where the marker sits on the
//     referring or composing property itself. That property's own path is
//     collected.
//   - INSIDE a `$ref` target. The reference is resolved and the declaration is
//     collected at the REFERRING property's path plus its own chain below that,
//     never at a path inside the target's definition — no authored datum lives
//     there.
//   - INSIDE a composition branch's own `properties`. A branch composes onto the
//     same object as the property carrying it, so the declaration is collected at
//     that property's path plus its own chain.
//
// Beside and inside are distinct positions, and both are collected. Reading only
// the beside case leaves every inside declaration silently uncollected while the
// reader looks complete.
//
// A pillar is entered at the property maps it declares, which pillarPropertyMaps
// enumerates: the pillar node's own `properties`, and those of the node an
// in-document `$ref` written on the pillar node resolves to. So a pillar declared
// wholly through a reference is read through its target, and one that writes a
// property map beside the reference is read through both. A marker on the pillar
// node itself declares nothing, since no single datum sits there.
//
// Every branch keyword the compiled vocabulary admits is descended, because
// under-collecting prints a datum an author asked to be withheld while
// over-collecting only withholds one an author left readable. `allOf` composes
// unconditionally. `anyOf` and `oneOf` compose whichever branch an instance
// satisfies, which a schema alone does not fix. `if`, `then` and `else` compose
// on a condition evaluated against an instance, likewise unknown here. `not`
// describes a shape an instance must avoid, so collecting through it can only
// withhold a datum that could never validate — it can never leave one readable.
//
// A `$ref` is resolved as a pointer into the compiled document: each enclosing
// node from the referring property outward to the document root is tried, nearest
// first, so a definitions block emitted beside the properties that use it
// resolves. A pointer no enclosing node resolves collects the referring
// property's own path instead of being skipped: what the target declares is
// unknown, so naming the referring property is the most precise declaration
// available, and the only one that cannot under-report. That is enough on its
// own, because a declared path COVERS everything beneath it wherever this
// reader's output is consumed — Covers is the one home for that question — so
// naming the referring property withholds the whole subtree it stands for. A pointer
// that revisits a target already in flight stops rather than recursing forever,
// and collects the referring path only when that target declares sensitivity
// somewhere — a recursive schema declaring none stays readable, because a path is
// sensitive only when an author said so.
//
// Only a schema is read: no value is inspected, and a property name is used as a
// path segment rather than matched against anything, so nothing here infers
// sensitivity from what a datum is called. A path is sensitive because an author
// said so.
func DeclaredSensitiveLeaves(compiled compactform.Schema) SensitiveLeaves {
	out := SensitiveLeaves{
		Values:   map[string]bool{},
		Infra:    map[string]bool{},
		Provides: map[string]bool{},
	}
	for _, pillar := range []struct {
		name string
		dest map[string]bool
	}{
		{"values", out.Values},
		{"infra", out.Infra},
		{"provides", out.Provides},
	} {
		pillarNode, ok := pillarSubSchema(compiled, pillar.name)
		if !ok {
			continue
		}
		for _, declared := range pillarPropertyMaps(pillarNode, []map[string]any{compiled}) {
			collectSensitiveLeafPaths(declared.properties, "", declared.scopes, declared.refs, pillar.dest)
		}
	}
	return out
}

// DeclaredSocketSensitiveLeaves reads which leaves of a connection's
// configuration a socket's per-edge contract declares sensitive: the contract the
// CONSUMING component type declares under spec.usesSchema.<socket>, valueSchema
// for the values pillar and infraSchema for the infra pillar. Provides is always
// empty, because a socket's contract describes the configuration the consumer
// writes for a connection and never what the target publishes.
//
// Each pillar is compiled through the one home for compiling a socket's
// contract — the compile the per-edge conformance gate and the declared-defaults
// materialisation read — and the declarations are read out of it by
// DeclaredSensitiveLeaves, the reader every entity's declarations come from. So a
// leaf is declared on a connection on exactly the reading it would be declared on
// a component, and this function adds nothing but the choice of contract.
//
// A component type the catalog does not hold, a socket it does not declare, a
// pillar the socket declares no contract for, and a contract that does not
// compile each contribute nothing to that pillar — the last is reported where
// contracts are resolved, not here, exactly as it is for the defaults the same
// compile materialises.
func DeclaredSocketSensitiveLeaves(catalog CatalogLookup, compType, socket string) SensitiveLeaves {
	out := SensitiveLeaves{}
	if compiled, ok := CompileSocketPillarSchema(catalog, compType, socket, "values"); ok {
		out.Values = DeclaredSensitiveLeaves(compiled).Values
	}
	if compiled, ok := CompileSocketPillarSchema(catalog, compType, socket, "infra"); ok {
		out.Infra = DeclaredSensitiveLeaves(compiled).Infra
	}
	return out
}

// pillarPropertyMap is one property map a pillar declares, with the reference
// scopes in force where it was declared and the pointers followed to reach it.
type pillarPropertyMap struct {
	properties map[string]any
	scopes     []map[string]any
	refs       []string
}

// pillarPropertyMaps returns every property map one compiled pillar node
// declares: the node's own `properties`, then — when the node carries an
// in-document `$ref` — the `properties` of the node that reference resolves to,
// and so on along the references each target carries in turn. It is the one
// answer to "what does this pillar declare", read by the sensitivity reader and
// by the reader of the key names a pillar publishes, so the two cannot come to
// different answers about the same pillar.
//
// A reference on the pillar node composes with a property map written beside it:
// a value under the pillar must satisfy both, so both declare. Each map is
// returned on its own rather than merged into one, because the two may declare
// the same name differently, and a reader asking what is declared must see both
// declarations rather than whichever was written last.
//
// The pointer resolves against scopes nearest first, with the pillar node itself
// nearest: a definitions block authored on the pillar node is the one its pointer
// was written against. A caller holding a whole-entity compile passes the
// document root as the outer scope; a caller holding a compile of the pillar
// alone passes none, and the pillar node is then the whole document.
//
// A pointer no scope resolves contributes nothing beyond the maps already found.
// There is no path to collect in its place, as an unresolvable reference on a
// property collects the property: the pillar is the root, not a datum. Such a
// pillar builds no validator, so it is refused as the catalog loads rather than
// read here. A pointer revisiting one already followed ends the chain. Only
// `properties` and `$ref` are read on the pillar node and on each target: a
// composition keyword there is not entered.
func pillarPropertyMaps(pillarNode map[string]any, outer []map[string]any) []pillarPropertyMap {
	var out []pillarPropertyMap
	var refs []string
	node := pillarNode
	scopes := pushScope(outer, node)
	for {
		if props, ok := node["properties"].(map[string]any); ok {
			out = append(out, pillarPropertyMap{properties: props, scopes: scopes, refs: refs})
		}
		ref, present := node["$ref"].(string)
		if !present {
			return out
		}
		if _, cyclic := refcycle.Check(refs, ref); cyclic {
			return out
		}
		target, resolved := resolvePointer(ref, scopes)
		if !resolved {
			return out
		}
		refs = refcycle.Push(refs, ref)
		node = target
		scopes = pushScope(scopes, target)
	}
}

// branchListKeywords are the composition keywords whose value is an array of
// schemas, and branchNodeKeywords the ones whose value is a single schema. Both
// sets compose onto the object carrying them, so a declaration inside either
// contributes at the carrying property's own path.
var (
	branchListKeywords = []string{"allOf", "anyOf", "oneOf"}
	branchNodeKeywords = []string{"if", "then", "else", "not"}
)

// pushScope returns a new scope stack with node appended, copying rather than
// extending in place. The copy is load-bearing: sibling branches of one node each
// extend the same stack, and an in-place append would let one branch's entry land
// in the slot another branch is about to write.
func pushScope(scopes []map[string]any, node map[string]any) []map[string]any {
	return append(append([]map[string]any{}, scopes...), node)
}

// collectSensitiveLeafPaths walks a compiled property map, recording the dotted
// path of every property whose node declares sensitivity. Segments join with "."
// at every nesting level, matching the flat-dotted leaf paths the per-leaf
// provenance maps are keyed by; a mismatch there would make a nested declaration
// unfindable by a consumer holding a leaf path. Insertion order is irrelevant —
// the output is a set, addressed by key and never iterated for emission.
func collectSensitiveLeafPaths(props map[string]any, prefix string, scopes []map[string]any, refs []string, out map[string]bool) {
	for name, raw := range props {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		collectSensitiveNode(node, path, scopes, refs, out)
	}
}

// collectSensitiveNode records path when node declares sensitivity, then follows
// every route by which a declaration can reach the datum at path: nested
// properties, each composition branch, and the target of a reference. Branch and
// reference descents keep the SAME path, because a branch composes onto the
// carrying object and a reference target's content is what the referring property
// holds; only a `properties` descent extends the path.
//
// The declared-defaults walk over the same compiled schema deliberately draws a
// NARROWER line — it follows `properties` only and contributes nothing through a
// reference or a composition branch. The two are not one walk that drifted into
// two, and unifying them would be a defect, for two independent reasons.
//
// They feed different sides of the build. This walk feeds what human-readable
// surfaces withhold, where collecting more never changes a rendered byte. The
// defaults walk materialises authored values into the render context, so widening
// it would put defaults into rendered artifacts that are not there today.
//
// They have opposite fail-safe directions. Reading sensitivity must catch
// everything, so a path collected that need not have been only withholds a datum
// an author left readable. Materialising a default must follow what an author
// wrote, so a default reached through a branch an instance does not satisfy would
// invent a value nobody declared.
func collectSensitiveNode(node map[string]any, path string, scopes []map[string]any, refs []string, out map[string]bool) {
	if declaresSensitive(node) {
		out[path] = true
	}
	// The node joins the scope stack before any descent so a definitions block it
	// carries is visible to the pointers written beneath it.
	inner := pushScope(scopes, node)
	if child, ok := node["properties"].(map[string]any); ok {
		collectSensitiveLeafPaths(child, path, inner, refs, out)
	}
	for _, keyword := range branchListKeywords {
		branches, ok := node[keyword].([]any)
		if !ok {
			continue
		}
		for _, raw := range branches {
			if branch, ok := raw.(map[string]any); ok {
				collectSensitiveNode(branch, path, inner, refs, out)
			}
		}
	}
	for _, keyword := range branchNodeKeywords {
		if branch, ok := node[keyword].(map[string]any); ok {
			collectSensitiveNode(branch, path, inner, refs, out)
		}
	}
	ref, present := node["$ref"].(string)
	if !present {
		return
	}
	// The in-flight key is the pointer as written. Two scopes resolving the same
	// spelling to different targets therefore read as one revisit, which stops a
	// descent that could have continued — the same direction as an unresolvable
	// pointer, so it withholds rather than leaks.
	if _, cyclic := refcycle.Check(refs, ref); cyclic {
		if refTargetDeclaresSensitive(ref, inner, nil) {
			out[path] = true
		}
		return
	}
	target, resolved := resolvePointer(ref, inner)
	if !resolved {
		out[path] = true
		return
	}
	collectSensitiveNode(target, path, inner, refcycle.Push(refs, ref), out)
}

// resolvePointer resolves an in-document reference against a stack of enclosing
// scopes, nearest first, and reports false when no scope holds the pointed-to
// node. A reference naming another document is never resolvable here: a
// cross-document reference is merged into the consumer's own schema before this
// reader sees it, so one that survived is a reference this reader cannot follow.
//
// Nearest-first matters because a definitions block is emitted on the node whose
// properties use it, not hoisted to the document root, so the innermost scope
// holding the pointer's own first segment is the one an author wrote it against.
func resolvePointer(ref string, scopes []map[string]any) (map[string]any, bool) {
	if !strings.HasPrefix(ref, "#") {
		return nil, false
	}
	pointer := strings.TrimPrefix(ref, "#")
	segments := []string{}
	if pointer != "" {
		if !strings.HasPrefix(pointer, "/") {
			return nil, false
		}
		for _, segment := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
			// Unescape in the order the JSON Pointer grammar requires: "~1"
			// first, so an escaped tilde cannot be re-read as a separator.
			segment = strings.ReplaceAll(segment, "~1", "/")
			segments = append(segments, strings.ReplaceAll(segment, "~0", "~"))
		}
	}
	for i := len(scopes) - 1; i >= 0; i-- {
		if node, found := walkPointer(scopes[i], segments); found {
			return node, true
		}
	}
	return nil, false
}

func walkPointer(scope map[string]any, segments []string) (map[string]any, bool) {
	node := scope
	for _, segment := range segments {
		next, ok := node[segment].(map[string]any)
		if !ok {
			return nil, false
		}
		node = next
	}
	return node, true
}

// refTargetDeclaresSensitive answers one narrower question than the path walk
// above: does anything reachable from this reference declare sensitivity at all,
// regardless of where. It exists because a reference that revisits a target
// already in flight cannot be enumerated further, and the two answers available
// there are opposite failures — stopping silently leaves a recursive tail
// readable, while withholding unconditionally invents sensitivity on a recursive
// schema whose author declared none. This predicate separates the two cases, so
// the tail is withheld exactly when the recursion carries a declaration.
//
// It is deliberately a second, simpler walk rather than a reuse of the path walk:
// it collects no paths and never withholds on a revisit, which is sound for a
// question about mere reachability — a marker anywhere in the reachable graph sits
// on some node reached by a non-repeating chain. Folding the two together would
// make the path walk's revisit rule depend on its own result.
func refTargetDeclaresSensitive(ref string, scopes []map[string]any, refs []string) bool {
	if _, cyclic := refcycle.Check(refs, ref); cyclic {
		return false
	}
	target, resolved := resolvePointer(ref, scopes)
	if !resolved {
		return true
	}
	return nodeDeclaresSensitive(target, pushScope(scopes, target), refcycle.Push(refs, ref))
}

func nodeDeclaresSensitive(node map[string]any, scopes []map[string]any, refs []string) bool {
	if declaresSensitive(node) {
		return true
	}
	if child, ok := node["properties"].(map[string]any); ok {
		for _, raw := range child {
			if property, ok := raw.(map[string]any); ok && nodeDeclaresSensitive(property, pushScope(scopes, property), refs) {
				return true
			}
		}
	}
	for _, keyword := range branchListKeywords {
		branches, ok := node[keyword].([]any)
		if !ok {
			continue
		}
		for _, raw := range branches {
			if branch, ok := raw.(map[string]any); ok && nodeDeclaresSensitive(branch, pushScope(scopes, branch), refs) {
				return true
			}
		}
	}
	for _, keyword := range branchNodeKeywords {
		if branch, ok := node[keyword].(map[string]any); ok && nodeDeclaresSensitive(branch, pushScope(scopes, branch), refs) {
			return true
		}
	}
	if ref, present := node["$ref"].(string); present {
		return refTargetDeclaresSensitive(ref, scopes, refs)
	}
	return false
}

// declaresSensitive reports whether one compiled schema node carries the
// sensitivity marker — the shape compact-form `sensitive: true` emits.
//
// The decision is the validator's own predicate, not a reading of it made here:
// this reader decides which authored data every display withholds, and the
// validator decides which refused values its findings withhold, and one answer
// to "does this node declare it" is what keeps the two from disagreeing about
// the same schema.
func declaresSensitive(node map[string]any) bool {
	return jsonvalidator.DeclaresSensitive(node)
}
