// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"text/template/parse"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// contextRootField is one root a render context exposes: the name a template
// reads it by, and the declared type behind that name.
type contextRootField struct {
	Name string
	Type reflect.Type
}

// contextRootNames returns the roots a render context exposes to a template, in
// declaration order. The names come from fieldJSONName — the same tag reading
// that names the keys toLowercaseMap delivers to the template — so the admitted
// set and the delivered set are one derivation rather than a hand-written list
// beside the type, and renaming a pillar cannot leave the two disagreeing.
//
// ok is false when the context is not a struct: its root set is not derivable
// from a type, and no root check applies.
func contextRootNames(data any) ([]string, bool) {
	fields, ok := contextRootFields(data)
	if !ok {
		return nil, false
	}
	return rootFieldNames(fields), true
}

// contextRootFields is contextRootNames carrying each root's declared type, which
// is what the depth-2 derivation reads. Both are the same walk so a root's name
// and the type behind it can never come from two readings of the struct.
func contextRootFields(data any) ([]contextRootField, bool) {
	t := reflect.TypeOf(data)
	for t != nil && (t.Kind() == reflect.Pointer || t.Kind() == reflect.Interface) {
		t = t.Elem()
	}
	return structRootFields(t)
}

func structRootFields(t reflect.Type) ([]contextRootField, bool) {
	if t == nil || t.Kind() != reflect.Struct {
		return nil, false
	}
	fields := make([]contextRootField, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, skip := fieldJSONName(f)
		if skip || name == "" {
			continue
		}
		fields = append(fields, contextRootField{Name: name, Type: f.Type})
	}
	return fields, true
}

func rootFieldNames(fields []contextRootField) []string {
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, f.Name)
	}
	return names
}

// orderedMapForTemplateType is the marker interface the context walk recognises an
// OrderedMap by, held as a reflect.Type so a root's openness is decidable from its
// declared type without instantiating a value.
var orderedMapForTemplateType = reflect.TypeOf((*orderedMapForTemplate)(nil)).Elem()

// closedRootKeys returns the keys a root's declared type defines one level down,
// and whether the root is closed at all.
//
// Closedness is decided the same way, and in the same order, walkForTemplate
// decides what it delivers. An OrderedMap is recognised FIRST and reported open
// even though it is a Go struct, because the walk hands it to the template through
// TemplateAlphabeticalMap as a keyed map whose keys are authored data, not a
// declared set — reading its Kind instead would report a struct with no exported
// fields and refuse every key the author wrote. Only after that does a struct
// count as closed; a map, slice, array, scalar or interface is open.
//
// Pointers are peeled; interfaces are not. reflect.Type.Elem is defined only for
// Array, Chan, Map, Pointer and Slice, so peeling an interface-typed field would
// panic — and a field declared as an interface carries no static key set to
// derive, since what it holds is decided at assembly time.
func closedRootKeys(ft reflect.Type) ([]string, bool) {
	for ft != nil && ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}
	if ft == nil {
		return nil, false
	}
	if ft.Implements(orderedMapForTemplateType) || reflect.PointerTo(ft).Implements(orderedMapForTemplateType) {
		return nil, false
	}
	fields, ok := structRootFields(ft)
	if !ok {
		return nil, false
	}
	return rootFieldNames(fields), true
}

// templateScope is the reference set one template surface admits. The universe of
// roots is derived from data's TYPE — nothing hand-lists a root — and a surface
// states only how it NARROWS that universe, so a renamed or retired pillar cannot
// leave a stale permitted set admitting or refusing the wrong thing.
//
// Depth 2 comes from the same derivation: a root whose declared type is a closed
// struct admits exactly that struct's own roots, while a root the context walk
// delivers as an open keyed map admits any key. Whether a root is open or closed
// is read off the type and never declared, which is what closes the typo hole
// under a closed pillar while leaving every optional-key idiom on values, infra,
// locals and provides working untouched.
type templateScope struct {
	// data is a value of the surface's context type. Only the type is read, so a
	// zero value is enough — a scope never has to carry the data being rendered,
	// which is what lets a pre-check that runs before a template is evaluated
	// share one derivation with the evaluation it guards.
	data any
	// exclude names roots the context type defines but this surface refuses.
	// Every entry must be a root of the type; validate reports one that is not,
	// so a narrowing cannot outlive the pillar it names.
	exclude []string
	// subGate names, per root, the depth-2 keys that root's own type defines but
	// this surface refuses. A root absent from the map keeps the full set its type
	// declares; a root the type reports open has no depth-2 set to narrow.
	subGate map[string][]string
}

// with returns the scope carrying the value actually being rendered. A scope
// declared at package level holds a zero value of its context type, because only
// the type is read to derive what the surface admits; substituting the value at
// the call site is what lets the surface's declared narrowing and the data the
// template renders against be the same scope rather than two arguments a caller
// could pair wrongly.
func (s templateScope) with(data any) templateScope {
	s.data = data
	return s
}

// templateScopeLevel names the nesting a refused reference failed at.
type templateScopeLevel int

const (
	scopeLevelRoot templateScopeLevel = 1
	scopeLevelKey  templateScopeLevel = 2
)

// templateScopeViolation is one refused reference: the reference itself, the set
// that was consulted, and the nesting the refusal happened at. The scope reports a
// violation and never formats it — each surface names the halt with its own
// published diagnostic, so one shared rule does not collapse several documented
// diagnostic codes into one.
type templateScopeViolation struct {
	Ref      gotmpl.RootRef
	Admitted []string
	Level    templateScopeLevel
}

// check returns the first reference the scope refuses, or nil when every
// reference is admitted. References are walked in first-appearance order, so the
// reference a diagnostic names is the same one on every run.
//
// A caller that already holds the reference set — the evaluator, which walks the
// tree once and shares that one walk between this admission check and the data it
// prepares for the execution — calls checkRefs instead. Both are the same rule
// over the same walk; only who performed the walk differs.
//
// The comparison is over NAMES taken from the context type, never over the
// context's runtime contents, because a root can be legitimately defined and
// still absent from the delivered data: a pillar declared omitempty is dropped
// when the node carries nothing for it, so a node with no attachments delivers no
// attachment pillar at all. Runtime absence therefore cannot tell a retired root
// from an empty one, and only the type can.
//
// Two nestings are checked, and the difference between them is read off the type
// rather than declared. A root the type reports OPEN — an ordered-map pillar such
// as values, infra, locals or provides — is checked at its name only, so a
// genuinely optional key under it still resolves through the surface's missing-key
// policy and a defensive wrapper around it still renders. A root whose type is a
// CLOSED struct — the identity, metadata, delivery, workload and boundary pillars
// — additionally has its keys checked against the struct's own fields, so a
// mistyped key halts instead of reaching the emitted artifact as an empty value.
// What the check removes at both nestings is the ability of a wrapper to launder a
// name the context does not define into an empty value: a wrapper can supply a
// default for an absent key, never for a name that does not exist.
//
// A bare whole-root reference is admitted whenever its root is, because refusing a
// bare root would break every defensive wrapper over a pillar. That admission covers
// the reference itself and not whatever is read through it: a read the template makes
// through an alias for a root — a variable bound to it, or the dot a with body
// rebinds to it — resolves to the path it aliases and is checked on exactly the terms
// the direct spelling gets. So aliasing a root cannot launder a key the root's type
// does not define; that is the same laundering the wrapper case above closes, reached
// by a different spelling.
//
// The comparison looks only at names and never at what any value means.
func (s templateScope) check(tree parse.Node) *templateScopeViolation {
	return s.checkRefs(gotmpl.CollectRootRefs(tree))
}

func (s templateScope) checkRefs(refs []gotmpl.RootRef) *templateScopeViolation {
	roots, ok := s.admittedRoots()
	if !ok {
		return nil
	}
	for _, ref := range refs {
		if !slices.Contains(roots, ref.Segs[0]) {
			return &templateScopeViolation{Ref: ref, Admitted: roots, Level: scopeLevelRoot}
		}
		if len(ref.Segs) < 2 {
			continue
		}
		keys, gated := s.admittedKeys(ref.Segs[0])
		if gated && !slices.Contains(keys, ref.Segs[1]) {
			return &templateScopeViolation{Ref: ref, Admitted: keys, Level: scopeLevelKey}
		}
	}
	return nil
}

// admittedRoots is the type's root universe less this surface's exclusions. ok is
// false when the context is not a struct, in which case no root set is derivable
// and no check applies.
func (s templateScope) admittedRoots() ([]string, bool) {
	roots, ok := contextRootNames(s.data)
	if !ok {
		return nil, false
	}
	return withoutNames(roots, s.exclude), true
}

// admittedKeys is one root's depth-2 set less this surface's narrowing. gated is
// false for a root the type reports open, which admits any key.
func (s templateScope) admittedKeys(root string) ([]string, bool) {
	declared, closed := s.declaredKeys(root)
	if !closed {
		return nil, false
	}
	return withoutNames(declared, s.subGate[root]), true
}

// declaredKeys is one root's depth-2 set as its own type declares it, before this
// surface's narrowing is applied.
func (s templateScope) declaredKeys(root string) ([]string, bool) {
	fields, ok := contextRootFields(s.data)
	if !ok {
		return nil, false
	}
	for _, f := range fields {
		if f.Name != root {
			continue
		}
		return closedRootKeys(f.Type)
	}
	return nil, false
}

// validate reports every narrowing this scope declares that its context type does
// not bear out: an exclude naming a root the type does not define, a subGate on a
// root the type does not define, a subGate on a root the type reports open (so
// there is no declared set to narrow), or a subGate entry naming a key the root's
// own type does not define. A stale narrowing silently changes what a surface
// admits, which is why it is held to the type by a guard rather than by review.
func (s templateScope) validate() []string {
	roots, ok := contextRootNames(s.data)
	if !ok {
		return []string{fmt.Sprintf("context type %T is not a struct, so no root set is derivable from it", s.data)}
	}
	var problems []string
	for _, name := range s.exclude {
		if !slices.Contains(roots, name) {
			problems = append(problems, fmt.Sprintf("exclude names %q, which %T does not define; it defines %s",
				name, s.data, strings.Join(roots, ", ")))
		}
	}
	gated := make([]string, 0, len(s.subGate))
	for root := range s.subGate {
		gated = append(gated, root)
	}
	slices.Sort(gated)
	for _, root := range gated {
		if !slices.Contains(roots, root) {
			problems = append(problems, fmt.Sprintf("subGate names root %q, which %T does not define; it defines %s",
				root, s.data, strings.Join(roots, ", ")))
			continue
		}
		// The declared set BEFORE this surface's narrowing is what a subGate entry
		// has to exist in; the narrowed set has already had the entry removed and
		// so cannot tell a live narrowing from a stale one.
		declared, closed := s.declaredKeys(root)
		if !closed {
			problems = append(problems, fmt.Sprintf("subGate narrows root %q, which %T reports open — an open root has no declared key set to narrow",
				root, s.data))
			continue
		}
		for _, key := range s.subGate[root] {
			if !slices.Contains(declared, key) {
				problems = append(problems, fmt.Sprintf("subGate[%q] names %q, which the root's own type does not define; it defines %s",
					root, key, strings.Join(declared, ", ")))
			}
		}
	}
	return problems
}

func withoutNames(all, drop []string) []string {
	if len(drop) == 0 {
		return all
	}
	out := make([]string, 0, len(all))
	for _, name := range all {
		if !slices.Contains(drop, name) {
			out = append(out, name)
		}
	}
	return out
}

// templateRootsPhrase renders a reference set as the prose list a diagnostic
// quotes, each entry spelled the way an author would write it. Derived from the
// set the check consults rather than restated, so what the build refuses and what
// the author is told cannot disagree.
func templateRootsPhrase(prefix string, names []string) string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, prefix+"."+name)
	}
	return strings.Join(out, ", ")
}

// templateUnknownRootErr states the unknown-reference halt every template surface
// shares, charged to the authoring domain the calling surface names. One wording
// and one sentinel across every surface; only the artifact the author is sent to
// differs.
func templateUnknownRootErr(category cerrors.Category, name string, v *templateScopeViolation) error {
	msg := fmt.Sprintf("template %q references %q — the render context defines %s",
		name, v.Ref.Path, templateRootsPhrase("", v.Admitted))
	if v.Level == scopeLevelKey {
		root := "." + v.Ref.Segs[0]
		msg = fmt.Sprintf("template %q references %q — %s defines %s",
			name, v.Ref.Path, root, templateRootsPhrase(root, v.Admitted))
	}
	return cerrors.Wrap(ErrTemplateUnknownRoot(), category, cerrors.SourceLocation{File: name}, msg)
}
