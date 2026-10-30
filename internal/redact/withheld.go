// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package redact

import (
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The template roots a set's coordinates are spelled in. Each is the name a
// template reads the pillar by — the json tag of the render context field that
// carries it — and TestCoordinateRootsAreTheTemplateContextTags holds every one
// of them to that tag, so a renamed pillar cannot leave a coordinate naming a
// root no context defines.
const (
	rootValues   = "values"
	rootInfra    = "infra"
	rootLocals   = "locals"
	rootSource   = "source"
	rootProvides = "provides"
	rootFeatures = "features"
	rootContext  = "context"
	keyInject    = "inject"
	keyUses      = "uses"
	keyAll       = "all"
)

// coordinates returns every datum the set names, spelled as the dotted path a
// template reads it by: values.<leaf>, infra.<leaf>, locals.<name>, source or
// source.<field>, context.inject.<mechanism>.<key>, context.uses.<alias>.provides.<key>,
// context.uses.<alias>.values.<leaf> and context.uses.<alias>.infra.<leaf>,
// context.uses.<alias>.features.<slot>.<pillar>… — and, where the set names the
// connection's socket, the same connection's values, infra and features beneath
// uses.<socket>.<alias> — features.<name>.<pillar>… and provides.<key>.
//
// It is the ONE statement of where in a template's data each kind of entry in the
// set sits, and both questions asked of a set in those terms read it: whether a
// template's references reach a withheld datum (ReachedBy), and which positions of
// a template's data are substituted before it runs again (SubstituteTree). The two
// therefore cannot disagree about a root — the reading cannot call a reference
// harmless while the substitution replaces the datum it reads, or the reverse.
//
// The delivery pipe's merged `all` view is covered by the union of every
// mechanism's marking, for the reason InjectPipe states: which mechanism won a
// key authored under two is the merge's own rule, and withholding the key under
// every view is the side of that trade that cannot leak.
//
// Every context type a template is evaluated against spells its pillars with these
// names — the render context, the assertion context, the locals, provides and
// self-inject contexts — so one set of coordinates serves each of them, and a
// coordinate naming a pillar a given context does not carry matches nothing in it.
// The one spelling two contexts do not share is a connection's: the render context
// reads it at context.uses.<alias> and the assertion context under its socket, at
// uses.<socket>.<alias>, so a connection the set places under a socket is named at
// both.
func (k Keys) coordinates() map[string]bool {
	out := map[string]bool{}
	addPaths(out, rootValues, k.Values)
	addPaths(out, rootInfra, k.Infra)
	addPaths(out, rootLocals, k.Locals)
	for field := range k.Source {
		if field == WholeSourceKey {
			out[rootSource] = true
			continue
		}
		out[rootSource+"."+field] = true
	}
	addPaths(out, rootProvides, k.Provides)
	for name, declared := range k.Features {
		addAttachment(out, rootFeatures+"."+name, declared)
	}
	pipe := rootContext + "." + keyInject
	for mechanism, deliveries := range k.inject {
		addPaths(out, pipe+"."+mechanism, deliveries)
	}
	addPaths(out, pipe+"."+keyAll, unionMarked(k.inject))
	uses := rootContext + "." + keyUses
	for alias, declared := range k.UsesProvides {
		addPaths(out, uses+"."+alias+"."+rootProvides, declared)
	}
	for alias, marked := range k.UsesConnections {
		addConnection(out, uses+"."+alias, marked)
		if socket := k.ConnectionSockets[alias]; socket != "" {
			addConnection(out, keyUses+"."+socket+"."+alias, marked)
		}
	}
	for alias, slots := range k.UsesAttachments {
		socket := k.ConnectionSockets[alias]
		for slot, declared := range slots {
			addAttachment(out, uses+"."+alias+"."+rootFeatures+"."+slot, declared)
			if socket != "" {
				addAttachment(out, keyUses+"."+socket+"."+alias+"."+rootFeatures+"."+slot, declared)
			}
		}
	}
	return out
}

// addPaths adds one coordinate per entry of paths, each beneath prefix.
func addPaths(out map[string]bool, prefix string, paths map[string]bool) {
	for path := range paths {
		out[prefix+"."+path] = true
	}
}

// addConnection adds the coordinates one connection's marking names in its own
// values and infra, beneath the position the connection is read at.
func addConnection(out map[string]bool, at string, marked ConnectionKeys) {
	addPaths(out, at+"."+rootValues, marked.Values)
	addPaths(out, at+"."+rootInfra, marked.Infra)
}

// addAttachment adds the coordinates one attachment's Feature declares, beneath
// the position the attachment is read at.
func addAttachment(out map[string]bool, at string, declared AttachmentKeys) {
	addPaths(out, at+"."+rootValues, declared.Values)
	addPaths(out, at+"."+rootInfra, declared.Infra)
	addPaths(out, at+"."+rootProvides, declared.Provides)
}

// ReachedBy reports whether any of a template's references reaches a datum the
// set names. refs are spelled as the over-collecting reference census spells them
// — a leading dot, then the dotted path — so a read hidden inside a rebound range
// or with body, or behind a chained defensive wrapper, has been recorded and is
// answered here.
//
// A reference reaches a withheld datum when its path is EQUAL to a coordinate, a
// DESCENDANT of one (a leaf beneath a declared object), or an ANCESTOR of one (a
// read of a whole object reaches every withheld datum beneath it — which is what
// makes `range .values.creds` and `index .values $k` conservative rather than
// invisible). The covered half is asked of the one home for that question; the
// ancestor half is this reading's own, because a display shows one leaf at a time
// and never needs it. A reference spelled as the data root alone — the census's
// entry for a template handing on the whole of its data — is the ancestor of every
// coordinate, so it reaches whatever the set names.
//
// A set naming nothing reaches nothing, which is what lets a mode that empties the
// set turn every reading of it off without a second switch.
func (k Keys) ReachedBy(refs []string) bool {
	if !k.AnyWithheld() || len(refs) == 0 {
		return false
	}
	coords := k.coordinates()
	if len(coords) == 0 {
		return false
	}
	ancestors := coordinateAncestors(coords)
	for _, ref := range refs {
		path := strings.TrimPrefix(ref, ".")
		if path == "" {
			return true
		}
		if types.PathCoveredBy(path, coords) || ancestors[path] {
			return true
		}
	}
	return false
}

// coordinateAncestors returns every proper prefix of every coordinate, on segment
// boundaries — the objects a read reaches a withheld datum through.
func coordinateAncestors(coords map[string]bool) map[string]bool {
	out := map[string]bool{}
	for coord := range coords {
		for i := strings.Index(coord, "."); i >= 0; {
			out[coord[:i]] = true
			next := strings.Index(coord[i+1:], ".")
			if next < 0 {
				break
			}
			i += 1 + next
		}
	}
	return out
}

// SubstituteTree replaces, in place, every datum the set names in a template's
// data tree with the placeholder, leaving the tree's shape and every datum the set
// does not name exactly as they were.
//
// It is the ONE substitution of a template's data in the module, and it is the
// answer wherever what a template produced cannot be addressed by key — a rendered
// document whose fields carry no link back to the declarations the template read,
// or the text of a failure the template raised. The data the template read IS
// addressable, so evaluating the same template again over a substituted tree
// yields output of the same shape with the placeholder standing where each datum
// stood. Removing the data instead would change which branches the template takes
// and the two outputs would diverge structurally; substituting keeps a string
// where a value was.
//
// tree is the plain-map tree a template is executed over — maps keyed by the
// names a template reads, slices and scalars — and it must be the caller's own
// private copy, because the substitution writes into it. Only a position that is
// PRESENT is replaced: a declared datum the author left unset stays absent, so a
// template testing whether it was supplied takes the same branch it took before.
// A declaration on an object replaces that whole object, and nothing is descended
// into but a map, so a coordinate reaches exactly the positions the display's
// pillar appliers reach; a parity test holds the two to one answer over one
// fixture.
//
// Two ways output built this way still differs from the original, and a surface
// showing it has to say so rather than paper over it: a template that branches on
// a value's CONTENT — an equality test, a length, a hash — takes a different branch
// against the placeholder, and a datum reaching the template through a position
// the set does not name is carried through unchanged.
func (k Keys) SubstituteTree(tree any) {
	root, ok := tree.(map[string]any)
	if !ok || !k.AnyWithheld() {
		return
	}
	coords := k.coordinates()
	if len(coords) == 0 {
		return
	}
	substituteMap(root, "", coords, coordinateAncestors(coords))
}

// substituteMap walks one map of a template's data tree. The keys are visited in
// sorted order so the walk is the same on every run, although the result does not
// depend on it: each step writes only the key it is visiting.
func substituteMap(m map[string]any, prefix string, coords, ancestors map[string]bool) {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if coords[path] {
			m[key] = placeholder
			continue
		}
		if !ancestors[path] {
			continue
		}
		if child, ok := m[key].(map[string]any); ok {
			substituteMap(child, path, coords, ancestors)
		}
	}
}
