// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package gotmpl

import (
	"strings"
	"text/template/parse"
)

// RootRef is one field access a template makes against its context root. Segs
// holds the path segments with the root first; Path holds that same path spelled
// the way a diagnostic quotes it back to the author.
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
type RootRef struct {
	Segs     []string
	Path     string
	Iterated bool
}

// CollectRootRefs returns every field access a parsed template makes against its
// context root, deduplicated by path and in first-appearance order so the
// reference a diagnostic names is the same one on every run. The argument is the
// root node of a parsed template — either the tree a caller is about to execute,
// or one ParseTree produced from authored text.
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
// This is the only walk behind every reference check, behind SeedAbsentLeafKeys and
// behind RootKeysRead. A surface differs only in what it does with the result — which
// references it admits, which absent leaves it supplies for, or which keys it
// evaluates ahead of the template — never in how the references are derived, so no
// two surfaces can disagree about which references a template makes.
// CollectTemplateFieldAccesses in this package is a separate walk with separate
// purposes and the opposite polarity, and it deliberately over-collects; see the
// polarity note below for which questions each direction answers, and why a caller
// must never be able to choose.
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
// over-collecting direction; this walk is the only one behind it. Seeding sits on
// the same side for the same reason: it WRITES at the path it is handed, so a
// reference attributed wrongly would insert a key the template never reads.
//
// The scope of that statement is worth being exact about, because an earlier and
// wider version of it — that NO reference check has any use for the
// over-collecting direction — is false, and reading it as a universal would send a
// consumer to the wrong walk. A reading whose job is to WITHHOLD something is also
// a check over references, and it needs the opposite direction: it must catch every
// read, including one hidden inside a rebound body or behind a chained defensive
// wrapper, because a read it fails to attribute prints a value an author asked to
// have hidden. Which side a consumer sits on is decided by what a MISSED reference
// costs — refused authoring or a wrongly inserted key on one side, a printed secret
// on the other — not by whether the consumer is described as a check.
//
// The census walk in this package over-collects, correctly, and each of its
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
func CollectRootRefs(tree parse.Node) []RootRef {
	out := &rootRefs{seen: map[string]int{}}
	walkRoots(tree, dotPath{known: true}, varPaths{}, out)
	return out.refs
}

// SeedAbsentLeafKeys makes a read of an unset value render nothing, on the
// surfaces whose policy is to tolerate an unset value at all.
//
// text/template's missingkey=zero resolves an absent key to the zero value of the
// map's ELEMENT type, and a template context reaches evaluation as a tree of
// map[string]any whatever the declared element types on the way in were. The zero
// value is therefore a nil interface, which the printer writes as the literal text
// "<no value>" and printf "%s" writes as "%!s(<nil>)". Neither is anything the
// author wrote, and both land in the emitted artifact. Inserting an empty string at
// the key the template reads makes the read produce nothing instead: the author
// left the field unset on a surface that permits it, so nothing substituted for
// nothing is the unopinionated result, and a `default` pipe or an `if` guard over
// the read behaves exactly as it did before, because an empty string is as empty
// and as falsy as the unset value it replaces.
//
// The caller must OWN the map it passes. This writes into data in place, so a
// caller holding a map anything else can observe has to seed a copy instead —
// otherwise a key supplied for one evaluation becomes a key every later reader of
// that map sees, with an empty string where the author wrote nothing at all.
//
// Only a LEAF is seeded, and only where its parent chain ALREADY resolves to a
// writable map. A read whose parent is absent fails the execution today —
// text/template cannot descend through nothing — and that failure is load-bearing:
// creating the parent would trade a loud halt for silence. A reference whose chain
// does not already resolve is therefore left untouched, and so is a chain that
// resolves to something other than a map.
//
// A reference of one segment names a root rather than a key and is left alone. What
// a surface does about a root it does not define is that surface's own policy —
// refusing it outright, or letting it resolve to nothing — and seeding one here
// would silently pre-empt that decision. A root a surface does define is a container
// in every real use, and supplying an empty string for one is the parent-creation
// the paragraph above forbids.
//
// A reference the template ITERATES is left alone for the same reason from the
// other direction: range needs a container where a value read needs a scalar, so
// an empty string there fails the execution outright, while an absent reference
// already iterates as nothing — which is the outcome an author ranging over an
// optional list wants. Nothing to fix, so nothing is supplied.
//
// Two consequences are worth knowing before either looks like a defect.
//
// On a surface carrying the `optional` helper, that helper reports a key as present
// once it has been seeded, so a template that reads one key both through `optional`
// and as a dotted chain gets the empty string rather than `optional`'s fallback for
// that key. Reading a key one way per template keeps the two answers the same.
//
// And supplying a leaf makes its PARENT non-empty, so a guard over the parent —
// {{ if .values }} or {{ with .values }} — reads as truthy once anything in the
// template reads an absent key beneath it, where the same guard over an otherwise
// empty parent reads as falsy. The alternative is worse rather than better: keeping
// the parent empty would mean not supplying the leaf, which is the emitted
// "<no value>" this exists to remove. A guard that means "did the author fill this
// one field" belongs on the field, where the empty string is falsy as the unset
// value was.
func SeedAbsentLeafKeys(data any, refs []RootRef) {
	root, ok := data.(map[string]any)
	if !ok {
		return
	}
	for _, ref := range refs {
		if len(ref.Segs) < 2 || ref.Iterated {
			continue
		}
		parent := root
		for _, seg := range ref.Segs[:len(ref.Segs)-1] {
			next, ok := parent[seg].(map[string]any)
			if !ok || next == nil {
				parent = nil
				break
			}
			parent = next
		}
		if parent == nil {
			continue
		}
		leaf := ref.Segs[len(ref.Segs)-1]
		if _, present := parent[leaf]; !present {
			parent[leaf] = ""
		}
	}
}

// rootRefs accumulates the walk's result: the references in first-appearance
// order, and the index each recorded path sits at so a path met twice merges into
// its existing entry rather than appearing twice.
//
// wholeRoot records that the template handed on the context root itself — a bare
// dot while the dot is the root, a bare $, or a variable bound to the root. Such a
// read names no path, so it is not a reference and CollectRootRefs does not report
// it; it is kept beside the references for [RootKeysRead], whose answer is only
// complete when nothing could have reached a root's keys that way.
type rootRefs struct {
	refs      []RootRef
	seen      map[string]int
	wholeRoot bool
}

// dotPath is what the dot names at one point in the walk. A known dot carries the
// root-anchored path it resolves to, and an empty path on a known dot means the dot
// is the context root itself. An unknown dot is one no static reading can name — a
// range element, or the result of a function the walk must not reason about — and
// nothing read beneath it is attributed to the context root.
type dotPath struct {
	segs  []string
	known bool
}

// resolve returns the root-anchored path a field access off this dot names. ok is
// false for an unknown dot, which is what keeps a read beneath an unnameable value
// from being attributed to a root the author never wrote.
func (d dotPath) resolve(ident []string) ([]string, bool) {
	if !d.known {
		return nil, false
	}
	out := make([]string, 0, len(d.segs)+len(ident))
	out = append(out, d.segs...)
	return append(out, ident...), true
}

// varPaths maps a variable the template declared to the root-anchored path it
// holds. A variable ABSENT from the map is one whose value the walk cannot name, and
// absence is the fail-safe state: a read through it contributes nothing, exactly as
// a read beneath an unknown dot does. Names are keyed as the template spells them,
// with the leading sigil.
type varPaths map[string][]string

// clone is how a block body gets its own environment. A declaration inside a body
// goes out of scope at the body's end, so binding it into the enclosing map would
// let it name the wrong value for the actions that follow.
func (v varPaths) clone() varPaths {
	out := make(varPaths, len(v))
	for name, segs := range v {
		out[name] = segs
	}
	return out
}

// resolveVariablePath returns the root-anchored path a variable read names, through
// whatever the variable was bound to.
func resolveVariablePath(v *parse.VariableNode, vars varPaths) ([]string, bool) {
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

// pipeDot is the dot a pipeline's value would rebind to, which is the path the
// pipeline names when the pipeline is a single reference and nothing more. A
// pipeline that applies a function or chains commands resolves to an unknown dot:
// what such a pipeline yields is decided at evaluation rather than by its text, so
// naming it would mean reasoning about what a function returns.
func pipeDot(p *parse.PipeNode, dot dotPath, vars varPaths) dotPath {
	if p == nil || len(p.Cmds) != 1 {
		return dotPath{}
	}
	cmd := p.Cmds[0]
	if cmd == nil || len(cmd.Args) != 1 {
		return dotPath{}
	}
	switch a := cmd.Args[0].(type) {
	case *parse.DotNode:
		return dot
	case *parse.FieldNode:
		if segs, ok := dot.resolve(a.Ident); ok {
			return dotPath{segs: segs, known: true}
		}
	case *parse.VariableNode:
		if len(a.Ident) == 0 {
			return dotPath{}
		}
		if a.Ident[0] == "$" {
			return dotPath{segs: append([]string(nil), a.Ident[1:]...), known: true}
		}
		if segs, ok := resolveVariablePath(a, vars); ok {
			return dotPath{segs: segs, known: true}
		}
	case *parse.PipeNode:
		return pipeDot(a, dot, vars)
	}
	return dotPath{}
}

// bindPipeDecls points a pipeline's own declarations at the value they hold. A
// declaration whose value is not nameable UNBINDS the variable instead of leaving
// a stale path behind, so a reassignment cannot make a later read resolve to the
// path the variable used to hold.
func bindPipeDecls(vars varPaths, p *parse.PipeNode, value dotPath) {
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

func walkRoots(n parse.Node, dot dotPath, vars varPaths, out *rootRefs) {
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
			walkRoots(child, dot, vars, out)
		}
	case *parse.ActionNode:
		walkRootsPipe(v.Pipe, dot, vars, false, out)
		bindPipeDecls(vars, v.Pipe, pipeDot(v.Pipe, dot, vars))
	case *parse.IfNode:
		walkRootsPipe(v.Pipe, dot, vars, false, out)
		value := pipeDot(v.Pipe, dot, vars)
		body := vars.clone()
		bindPipeDecls(body, v.Pipe, value)
		walkRoots(v.List, dot, body, out)
		elseVars := vars.clone()
		bindPipeDecls(elseVars, v.Pipe, value)
		walkRoots(v.ElseList, dot, elseVars, out)
	case *parse.RangeNode:
		// Everything the range pipeline reaches is marked iterated. A reference
		// nested inside that pipeline — an argument to a function whose result is
		// what actually gets iterated — is marked too, which over-marks rather
		// than under-marks: the flag only ever withholds something from an absent
		// reference, so marking one reference too many costs nothing and missing
		// one costs a container where the template needed it.
		walkRootsPipe(v.Pipe, dot, vars, true, out)
		// The body's dot is one element of what the pipeline named, and so are the
		// key and element variables the range declares. An element is not nameable
		// from the text, so all three are unknown rather than the container's path.
		body := vars.clone()
		bindPipeDecls(body, v.Pipe, dotPath{})
		walkRoots(v.List, dotPath{}, body, out)
		walkRoots(v.ElseList, dot, vars.clone(), out)
	case *parse.WithNode:
		walkRootsPipe(v.Pipe, dot, vars, false, out)
		// A with body's dot IS the value the pipeline named, so a nameable pipeline
		// makes the body's reads resolve through it on the same terms the direct
		// spelling gets.
		value := pipeDot(v.Pipe, dot, vars)
		body := vars.clone()
		bindPipeDecls(body, v.Pipe, value)
		walkRoots(v.List, value, body, out)
		walkRoots(v.ElseList, dot, vars.clone(), out)
	case *parse.TemplateNode:
		walkRootsPipe(v.Pipe, dot, vars, false, out)
	}
}

func walkRootsPipe(p *parse.PipeNode, dot dotPath, vars varPaths, iterated bool, out *rootRefs) {
	if p == nil {
		return
	}
	for _, cmd := range p.Cmds {
		if cmd == nil {
			continue
		}
		for _, arg := range cmd.Args {
			walkRootsArg(arg, dot, vars, iterated, out)
		}
	}
}

func walkRootsArg(arg parse.Node, dot dotPath, vars varPaths, iterated bool, out *rootRefs) {
	switch a := arg.(type) {
	case *parse.DotNode:
		if dot.known && len(dot.segs) == 0 {
			out.wholeRoot = true
		}
	case *parse.FieldNode:
		if segs, ok := dot.resolve(a.Ident); ok {
			recordRootRef(out, segs, "."+strings.Join(segs, "."), iterated)
		}
	case *parse.VariableNode:
		if len(a.Ident) == 0 {
			return
		}
		if a.Ident[0] == "$" {
			if len(a.Ident) > 1 {
				recordRootRef(out, a.Ident[1:], "$."+strings.Join(a.Ident[1:], "."), iterated)
			} else {
				out.wholeRoot = true
			}
			return
		}
		if segs, ok := resolveVariablePath(a, vars); ok {
			if len(segs) == 0 {
				out.wholeRoot = true
				return
			}
			recordRootRef(out, segs, "."+strings.Join(segs, "."), iterated)
		}
	case *parse.ChainNode:
		// The chained fields hang off the receiver's value, not off the dot;
		// only the receiver can reach the context root.
		walkRootsArg(a.Node, dot, vars, iterated, out)
	case *parse.PipeNode:
		walkRootsPipe(a, dot, vars, iterated, out)
	}
}

func recordRootRef(out *rootRefs, segs []string, path string, iterated bool) {
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
	out.refs = append(out.refs, RootRef{Segs: segs, Path: path, Iterated: iterated})
}
