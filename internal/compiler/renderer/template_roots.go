// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"text/template/parse"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// templateRootRef is one field access a template makes against its context
// root. Segs holds the path segments with the root first; Path holds that same
// path spelled the way a diagnostic quotes it back to the author.
//
// Path is the EFFECTIVE path rather than the author's keystrokes. A read a template
// makes through an alias for a root — a declared variable bound to it, or the dot a
// with body rebinds to it — is recorded under the path it resolves to, not under the
// alias. That is what lets the aliased and the direct spelling of one reference
// share a single entry, so the iteration flag below merges across them and a
// diagnostic names the reference the check actually refused.
//
// Iterated marks a reference the template iterates over — the operand of a range
// pipeline — rather than reads as a value. Iteration needs a container where a
// value read needs a scalar, so the two positions cannot be treated alike by
// anything that supplies something for an absent reference. It is set when ANY
// occurrence of the path is iterated, not just the first, so the flag does not
// depend on which occurrence the template happens to write first.
type templateRootRef struct {
	Segs     []string
	Path     string
	Iterated bool
}

// collectTemplateRootRefs returns every field access a parsed template makes
// against its context root, deduplicated by path and in first-appearance order
// so the reference a diagnostic names is the same one on every run.
//
// Only root-anchored accesses are collected, which is what makes the result
// usable as a root-name check rather than as a general field census:
//
//   - .values.port is anchored at the context root and contributes root
//     "values".
//   - (default dict .values.tls).ca is anchored at the parenthesised
//     expression's result, not at the context root, so the trailing .ca
//     contributes nothing while the inner .values.tls still contributes
//     "values". This is what keeps a defensively-written optional key from
//     reading as a reference to a root named "ca".
//   - inside a range body the dot is the element, so .name there names a field of
//     that element and contributes nothing; a range's declared key and element
//     variables name that same unnameable value and contribute nothing either. A
//     with body differs, because its dot is the value the pipeline named: when that
//     pipeline is a single root-anchored reference, the body's reads resolve through
//     it, so {{ with .metadata }}{{ .release_name }}{{ end }} contributes
//     ".metadata.release_name" exactly as the direct spelling does. The range or
//     with pipeline itself is evaluated against the surrounding dot and does
//     contribute. An else branch runs with the dot unchanged and so contributes
//     exactly as its surrounding scope does.
//   - $ names the context root under any rebinding, so $.values.x contributes
//     "values" even inside a rebound body. A variable the template declared
//     contributes through whatever it was bound to, so {{ $m := .metadata }} makes
//     $m.release_name contribute ".metadata.release_name". A variable holding a
//     value no static reading can name contributes nothing, and a variable's own
//     name never reads as a root.
//   - a dot the walk cannot name contributes nothing beneath it. A with or an
//     assignment whose pipeline applies a function or chains commands —
//     {{ with default dict .metadata }}, {{ with index .context.uses "main" }} —
//     leaves the dot unnameable, because what such a pipeline yields is decided at
//     evaluation rather than by its text. The pipeline's own root-anchored arguments
//     still contribute.
//
// A template invoked by name receives whatever the invocation passes as its
// dot, so the invocation's pipeline contributes and the invoked body does not.
//
// This is the only walk any reference check uses, and every surface that checks
// references shares it through templateScope, differing only in the scope it
// compares against. The field-access census in package gotmpl is a separate walk
// with separate purposes and the opposite polarity, and it deliberately
// over-collects; see the polarity note below for which questions each direction
// answers, and why a caller must never be able to choose.
//
// Polarity is why there is one walk rather than two, and it is worth stating
// because the two directions are not interchangeable. Under-collecting is the
// fail-safe direction for an ALLOW-list: a reference this walk does not attribute
// to the context root is simply not checked, whereas attributing one wrongly
// refuses legitimate authoring — a field read inside a range or with body, or the
// trailing key of a chained fallback, is not a root reference and must not be
// treated as one. Over-collecting is fail-safe in the opposite direction, for a
// DENY-list, where a forbidden name must not slip past by hiding inside a rebound
// body. Every reference check that ADMITS OR REFUSES AUTHORING is an allow-list
// over what a context type defines, and that class has no use for the
// over-collecting direction; this walk is the only one behind it.
//
// The scope of that statement is worth being exact about, because an earlier and
// wider version of it — that NO reference check has any use for the
// over-collecting direction — is false, and reading it as a universal would send a
// consumer to the wrong walk. A reading whose job is to WITHHOLD something is also
// a check over references, and it needs the opposite direction: it must catch every
// read, including one hidden inside a rebound body or behind a chained defensive
// wrapper, because a read it fails to attribute prints a value an author asked to
// have hidden. Which side a consumer sits on is decided by what a MISSED reference
// costs — refused authoring on one side, a printed secret on the other — not by
// whether the consumer is described as a check.
//
// The census walk in package gotmpl over-collects, correctly, and each of its
// questions is on the other side of that line. A read-set census must report a
// key hidden inside a rebound body as read, because a read-set that under-reported
// would call a consumed key unread. A check that must WITHHOLD a value, or that
// refuses a reference to a pillar the Recipe never declared, is a deny-list, and a
// name it failed to attribute is a name that slipped past. So the two walks are not
// one admitting walk and one merely-reporting walk: they are an allow-list walk and
// a deny-list walk, and what keeps their directions from meeting is that each
// consumer sits on exactly one side. Neither walk may grow a polarity parameter,
// for the same reason in both places — a caller could then pass the wrong
// direction, and the failure would be silent in whichever direction it went.
//
// Resolving an alias to the path it aliases NARROWS the under-collection without
// reversing the polarity, which is the property that keeps the paragraphs above
// true. The walk attributes a read through an alias only where the alias's own path
// is decidable from the text, and records nothing wherever it is not, so every
// reference the walk newly attributes is one whose direct spelling it already
// attributed. Nothing the surface admitted becomes refused; what stops being
// possible is spelling a reference through an alias to get a verdict the direct
// spelling would not have given.
func collectTemplateRootRefs(root parse.Node) []templateRootRef {
	out := &templateRootRefs{seen: map[string]int{}}
	walkTemplateRoots(root, templateDot{known: true}, templateVars{}, out)
	return out.refs
}

// templateRootRefs accumulates the walk's result: the references in
// first-appearance order, and the index each recorded path sits at so a path met
// twice merges into its existing entry rather than appearing twice.
type templateRootRefs struct {
	refs []templateRootRef
	seen map[string]int
}

// templateDot is what the dot names at one point in the walk. A known dot carries
// the root-anchored path it resolves to, and an empty path on a known dot means the
// dot is the context root itself. An unknown dot is one no static reading can name —
// a range element, or the result of a function the walk must not reason about — and
// nothing read beneath it is attributed to the context root.
type templateDot struct {
	segs  []string
	known bool
}

// resolve returns the root-anchored path a field access off this dot names. ok is
// false for an unknown dot, which is what keeps a read beneath an unnameable value
// from being attributed to a root the author never wrote.
func (d templateDot) resolve(ident []string) ([]string, bool) {
	if !d.known {
		return nil, false
	}
	out := make([]string, 0, len(d.segs)+len(ident))
	out = append(out, d.segs...)
	return append(out, ident...), true
}

// templateVars maps a variable the template declared to the root-anchored path it
// holds. A variable ABSENT from the map is one whose value the walk cannot name, and
// absence is the fail-safe state: a read through it contributes nothing, exactly as
// a read beneath an unknown dot does. Names are keyed as the template spells them,
// with the leading sigil.
type templateVars map[string][]string

// clone is how a block body gets its own environment. A declaration inside a body
// goes out of scope at the body's end, so binding it into the enclosing map would
// let it name the wrong value for the actions that follow.
func (v templateVars) clone() templateVars {
	out := make(templateVars, len(v))
	for name, segs := range v {
		out[name] = segs
	}
	return out
}

// resolveVariablePath returns the root-anchored path a variable read names, through
// whatever the variable was bound to.
func resolveVariablePath(v *parse.VariableNode, vars templateVars) ([]string, bool) {
	if len(v.Ident) == 0 {
		return nil, false
	}
	base, bound := vars[v.Ident[0]]
	if !bound {
		return nil, false
	}
	out := make([]string, 0, len(base)+len(v.Ident)-1)
	out = append(out, base...)
	return append(out, v.Ident[1:]...), true
}

// templatePipeDot is the dot a pipeline's value would rebind to, which is the path
// the pipeline names when the pipeline is a single reference and nothing more. A
// pipeline that applies a function or chains commands resolves to an unknown dot:
// what such a pipeline yields is decided at evaluation rather than by its text, so
// naming it would mean reasoning about what a function returns.
func templatePipeDot(p *parse.PipeNode, dot templateDot, vars templateVars) templateDot {
	if p == nil || len(p.Cmds) != 1 {
		return templateDot{}
	}
	cmd := p.Cmds[0]
	if cmd == nil || len(cmd.Args) != 1 {
		return templateDot{}
	}
	switch a := cmd.Args[0].(type) {
	case *parse.DotNode:
		return dot
	case *parse.FieldNode:
		if segs, ok := dot.resolve(a.Ident); ok {
			return templateDot{segs: segs, known: true}
		}
	case *parse.VariableNode:
		if len(a.Ident) == 0 {
			return templateDot{}
		}
		if a.Ident[0] == "$" {
			return templateDot{segs: append([]string(nil), a.Ident[1:]...), known: true}
		}
		if segs, ok := resolveVariablePath(a, vars); ok {
			return templateDot{segs: segs, known: true}
		}
	case *parse.PipeNode:
		return templatePipeDot(a, dot, vars)
	}
	return templateDot{}
}

// bindTemplatePipeDecls points a pipeline's own declarations at the value they hold.
// A declaration whose value is not nameable UNBINDS the variable instead of leaving
// a stale path behind, so a reassignment cannot make a later read resolve to the
// path the variable used to hold.
func bindTemplatePipeDecls(vars templateVars, p *parse.PipeNode, value templateDot) {
	if p == nil {
		return
	}
	for _, decl := range p.Decl {
		if decl == nil || len(decl.Ident) != 1 {
			continue
		}
		if !value.known {
			delete(vars, decl.Ident[0])
			continue
		}
		vars[decl.Ident[0]] = append([]string(nil), value.segs...)
	}
}

func walkTemplateRoots(n parse.Node, dot templateDot, vars templateVars, out *templateRootRefs) {
	if n == nil {
		return
	}
	switch v := n.(type) {
	case *parse.ListNode:
		if v == nil {
			return
		}
		// One environment threads through the list, because a declaration in one
		// action is in scope for the actions that follow it.
		for _, child := range v.Nodes {
			walkTemplateRoots(child, dot, vars, out)
		}
	case *parse.ActionNode:
		walkTemplateRootsPipe(v.Pipe, dot, vars, false, out)
		bindTemplatePipeDecls(vars, v.Pipe, templatePipeDot(v.Pipe, dot, vars))
	case *parse.IfNode:
		walkTemplateRootsPipe(v.Pipe, dot, vars, false, out)
		value := templatePipeDot(v.Pipe, dot, vars)
		body := vars.clone()
		bindTemplatePipeDecls(body, v.Pipe, value)
		walkTemplateRoots(v.List, dot, body, out)
		elseVars := vars.clone()
		bindTemplatePipeDecls(elseVars, v.Pipe, value)
		walkTemplateRoots(v.ElseList, dot, elseVars, out)
	case *parse.RangeNode:
		// Everything the range pipeline reaches is marked iterated. A reference
		// nested inside that pipeline — an argument to a function whose result is
		// what actually gets iterated — is marked too, which over-marks rather
		// than under-marks: the flag only ever withholds something from an absent
		// reference, so marking one reference too many costs nothing and missing
		// one costs a container where the template needed it.
		walkTemplateRootsPipe(v.Pipe, dot, vars, true, out)
		// The body's dot is one element of what the pipeline named, and so are the
		// key and element variables the range declares. An element is not nameable
		// from the text, so all three are unknown rather than the container's path.
		body := vars.clone()
		bindTemplatePipeDecls(body, v.Pipe, templateDot{})
		walkTemplateRoots(v.List, templateDot{}, body, out)
		walkTemplateRoots(v.ElseList, dot, vars.clone(), out)
	case *parse.WithNode:
		walkTemplateRootsPipe(v.Pipe, dot, vars, false, out)
		// A with body's dot IS the value the pipeline named, so a nameable pipeline
		// makes the body's reads resolve through it on the same terms the direct
		// spelling gets.
		value := templatePipeDot(v.Pipe, dot, vars)
		body := vars.clone()
		bindTemplatePipeDecls(body, v.Pipe, value)
		walkTemplateRoots(v.List, value, body, out)
		walkTemplateRoots(v.ElseList, dot, vars.clone(), out)
	case *parse.TemplateNode:
		walkTemplateRootsPipe(v.Pipe, dot, vars, false, out)
	}
}

func walkTemplateRootsPipe(p *parse.PipeNode, dot templateDot, vars templateVars, iterated bool, out *templateRootRefs) {
	if p == nil {
		return
	}
	for _, cmd := range p.Cmds {
		if cmd == nil {
			continue
		}
		for _, arg := range cmd.Args {
			walkTemplateRootsArg(arg, dot, vars, iterated, out)
		}
	}
}

func walkTemplateRootsArg(arg parse.Node, dot templateDot, vars templateVars, iterated bool, out *templateRootRefs) {
	switch a := arg.(type) {
	case *parse.FieldNode:
		if segs, ok := dot.resolve(a.Ident); ok {
			recordTemplateRootRef(out, segs, "."+strings.Join(segs, "."), iterated)
		}
	case *parse.VariableNode:
		if len(a.Ident) == 0 {
			return
		}
		if a.Ident[0] == "$" {
			if len(a.Ident) > 1 {
				recordTemplateRootRef(out, a.Ident[1:], "$."+strings.Join(a.Ident[1:], "."), iterated)
			}
			return
		}
		if segs, ok := resolveVariablePath(a, vars); ok {
			recordTemplateRootRef(out, segs, "."+strings.Join(segs, "."), iterated)
		}
	case *parse.ChainNode:
		// The chained fields hang off the receiver's value, not off the dot;
		// only the receiver can reach the context root.
		walkTemplateRootsArg(a.Node, dot, vars, iterated, out)
	case *parse.PipeNode:
		walkTemplateRootsPipe(a, dot, vars, iterated, out)
	}
}

func recordTemplateRootRef(out *templateRootRefs, segs []string, path string, iterated bool) {
	if len(segs) == 0 || segs[0] == "" {
		return
	}
	if at, ok := out.seen[path]; ok {
		// A path read as a value in one place and iterated in another is iterated:
		// the position that needs a container is the binding one.
		out.refs[at].Iterated = out.refs[at].Iterated || iterated
		return
	}
	out.seen[path] = len(out.refs)
	out.refs = append(out.refs, templateRootRef{Segs: segs, Path: path, Iterated: iterated})
}

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
	Ref      templateRootRef
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
	return s.checkRefs(collectTemplateRootRefs(tree))
}

func (s templateScope) checkRefs(refs []templateRootRef) *templateScopeViolation {
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
