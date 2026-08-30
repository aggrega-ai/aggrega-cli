// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// sensitiveExtensionKeyword is the compiled-schema spelling a schema leaf
// declared `sensitive: true` carries.
const sensitiveExtensionKeyword = "x-aggrega-sensitive"

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
	switch pillar {
	case "values":
		return types.PathCoveredBy(path, s.Values)
	case "infra":
		return types.PathCoveredBy(path, s.Infra)
	}
	return false
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
		props, ok := pillarProperties(compiled, pillar.name)
		if !ok {
			continue
		}
		// The pillar node joins the document root as a reference scope: a
		// definitions block authored inside a pillar is emitted on that node
		// rather than at the root, so a pointer written beside it resolves only
		// when the node it sits on is in scope.
		scopes := []map[string]any{compiled}
		if pillarNode, found := pillarSubSchema(compiled, pillar.name); found {
			scopes = pushScope(scopes, pillarNode)
		}
		collectSensitiveLeafPaths(props, "", scopes, nil, pillar.dest)
	}
	return out
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
// sensitivity marker. Only the boolean true counts — the shape compact-form
// `sensitive: true` emits. Presence of the key alone is deliberately not enough:
// a composed schema records, in a sibling map, which entity in an `extends` chain
// declared each keyword, so the same keyword spelling also appears there mapped
// to an entity name rather than to a boolean.
func declaresSensitive(node map[string]any) bool {
	v, has := node[sensitiveExtensionKeyword]
	if !has {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}
