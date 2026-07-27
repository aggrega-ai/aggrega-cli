// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import "github.com/aggrega-ai/aggrega-cli/pkg/compactform"

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

// DeclaredSensitiveLeaves reads the sensitivity declarations out of an entity's
// compiled schema. The input is the whole-entity compact-form compile — the same
// schema the pillar-conformance and declared-defaults readers consume — so an
// entity that inherits a declaration through `extends` reports it exactly as it
// reports an inherited constraint.
//
// A leaf declares sensitivity by carrying the `x-aggrega-sensitive` extension
// keyword with the boolean value true, which is what compact-form `sensitive:
// true` emits. The keyword is read on the node it sits on, and the walk descends
// only through `properties`, so a collected path is the dotted property chain
// from the pillar root. Two shapes are therefore not collected: a declaration
// that lives inside a reference target rather than beside the reference, and one
// that lives inside a composition branch's own `properties` rather than beside
// the composition keyword. The declared-defaults walk over the same schema draws
// the same line.
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
		collectSensitiveLeafPaths(props, "", pillar.dest)
	}
	return out
}

// collectSensitiveLeafPaths walks a compiled property map, recording the dotted
// path of every node carrying the sensitivity marker and recursing into nested
// property maps. Segments join with "." at every nesting level, matching the
// flat-dotted leaf paths the per-leaf provenance maps are keyed by; a mismatch
// there would make a nested declaration unfindable by a consumer holding a leaf
// path. Insertion order is irrelevant — the output is a set, addressed by key
// and never iterated for emission.
func collectSensitiveLeafPaths(props map[string]any, prefix string, out map[string]bool) {
	for name, raw := range props {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		if declaresSensitive(node) {
			out[path] = true
		}
		if child, ok := node["properties"].(map[string]any); ok {
			collectSensitiveLeafPaths(child, path, out)
		}
	}
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
