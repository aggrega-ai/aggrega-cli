// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Resolve returns the row of t that maps n. The rows keyed by n's type,
// regardless of case, are tried most predicates first, rows of as many
// predicates in the order [Table.Rows] returns them, and the first whose When
// holds of n is n's row, returned with true. A predicate reads the value n
// writes at its path, and a parameter there as its literal default, through a
// default naming another parameter; a value the import cannot read — a
// parameter with no literal default, a reference, deploy-time text — leaves
// the predicate undecided, as does a path no equivalence of the row
// translates into the form n is written in. Where a row tried before the one
// that holds is undecided, or no row holds and one is undecided, which row
// maps n depends on a value set only when the source deploys: Resolve returns
// no row, and the rows n could take in the order tried, for the caller to
// report the discriminator unresolved. A type no row keys, or whose every
// row's When fails, returns no row and no candidate. A table whose rows Rows
// refuses resolves no node. The rows are returned as the table holds them,
// sharing its slices and maps; [Row.For] reads one in n's form.
//
// Types, property names and the values a predicate tests are matched
// regardless of case, as Azure Resource Manager compares them: a template may
// write Microsoft.DBForPostgreSQL/flexibleServers for a row keyed
// Microsoft.DBforPostgreSQL/flexibleServers, or FunctionApp for a predicate
// testing functionapp.
func (t Table) Resolve(n *Node, g *Graph) (Row, []Row, bool) {
	if n == nil {
		return Row{}, nil, false
	}
	rows, err := t.Rows()
	if err != nil {
		return Row{}, nil, false
	}
	var keyed []Row
	for _, r := range rows {
		if strings.EqualFold(r.Key, n.Type) {
			keyed = append(keyed, r)
		}
	}
	sort.SliceStable(keyed, func(i, j int) bool { return len(keyed[i].When) > len(keyed[j].When) })

	read := reader{g: g}
	var could []Row
	for _, r := range keyed {
		switch read.row(r, n) {
		case truthHolds:
			if could == nil {
				return r, nil, true
			}
			return Row{}, append(could, r), false
		case truthUndecided:
			could = append(could, r)
		}
	}
	return Row{}, could, false
}

// For returns r as it reads a node written in form: each path of its When,
// its placements, its Provides and its Env translated through its
// Equivalences, where form and r.Form are one raw and the other a module form,
// and Form set to form. A path is translated through the equivalence of the
// longest path it begins with, the rest of it kept; a predicate, placement or
// environment list whose path no equivalence covers is left out of the row
// returned. Where form and r.Form are both raw, or both module forms, r is
// returned as is, Form set to form. An empty Form is the raw form. The row r
// is left as it is, and the row returned shares with it the slices and maps
// For does not translate.
func (r Row) For(form Form) Row {
	out := r
	out.Form = form
	toModule := isModule(form)
	if toModule == isModule(r.Form) {
		return out
	}
	out.When = nil
	for _, p := range r.When {
		if path, ok := translate(p.Path, r.Equivalences, toModule); ok {
			out.When = append(out.When, Pred{Path: path, Op: p.Op, Value: p.Value})
		}
	}
	out.Values = translatePlacements(r.Values, r.Equivalences, toModule)
	out.Infra = translatePlacements(r.Infra, r.Equivalences, toModule)
	out.Overrides = translatePlacements(r.Overrides, r.Equivalences, toModule)
	out.Secrets = translatePlacements(r.Secrets, r.Equivalences, toModule)
	out.Provides = translatePlacements(r.Provides, r.Equivalences, toModule)
	out.Env = nil
	if r.Env != nil {
		if path, ok := translate(r.Env.Path, r.Equivalences, toModule); ok {
			out.Env = &EnvList{Path: path, Name: r.Env.Name, Value: r.Env.Value}
		}
	}
	return out
}

// isModule reports whether form is a module form.
func isModule(form Form) bool {
	return form == FormAVM || form == FormModule
}

// translatePlacements returns the placements whose paths translate, each with
// its path translated.
func translatePlacements(ps []Placement, eqs []Equivalence, toModule bool) []Placement {
	var out []Placement
	for _, p := range ps {
		if path, ok := translate(p.From, eqs, toModule); ok {
			out = append(out, Placement{From: path, Key: p.Key, Conv: p.Conv})
		}
	}
	return out
}

// translate returns path in the module form where toModule is true, and in
// the raw form otherwise, through the equivalence whose path in path's own
// form is the longest path begins with, its segments compared regardless of
// case; the first of equal length is taken.
func translate(path []string, eqs []Equivalence, toModule bool) ([]string, bool) {
	best, length := -1, 0
	for i, e := range eqs {
		from := e.Raw
		if !toModule {
			from = e.Module
		}
		if len(from) > length && len(from) <= len(path) && slices.EqualFunc(path[:len(from)], from, strings.EqualFold) {
			best, length = i, len(from)
		}
	}
	if best < 0 {
		return nil, false
	}
	to := eqs[best].Module
	if !toModule {
		to = eqs[best].Raw
	}
	out := make([]string, 0, len(to)+len(path)-length)
	return append(append(out, to...), path[length:]...), true
}

// truth is what a predicate or a row's When is of a node.
type truth int

const (
	truthFails truth = iota
	truthHolds
	truthUndecided
)

// state is what reading a value gives.
type state int

const (
	// stateNone is no value of the kind read: an absent path, or a value that
	// is not text.
	stateNone state = iota
	// stateRead is a value read.
	stateRead
	// stateUndecided is a value the import cannot read: it is set only when
	// the source deploys.
	stateUndecided
)

// reader reads the values of a node, a parameter through its default in g.
type reader struct {
	g *Graph
}

// row returns whether r's When holds of n: it fails where a predicate fails,
// is undecided where none fails and one is undecided, and holds otherwise.
func (rd reader) row(r Row, n *Node) truth {
	toModule := isModule(n.Form)
	translated := toModule != isModule(r.Form)
	result := truthHolds
	for _, p := range r.When {
		path := p.Path
		if translated {
			var ok bool
			if path, ok = translate(p.Path, r.Equivalences, toModule); !ok {
				result = truthUndecided
				continue
			}
		}
		switch rd.pred(Pred{Path: path, Op: p.Op, Value: p.Value}, n.Body) {
		case truthFails:
			return truthFails
		case truthUndecided:
			result = truthUndecided
		}
	}
	return result
}

// pred returns whether p holds of the value body writes at p's path, the text
// compared with p's Value regardless of case. An operator outside the
// vocabulary never holds.
func (rd reader) pred(p Pred, body []Prop) truth {
	st, v := rd.at(body, p.Path)
	if st == stateUndecided {
		return truthUndecided
	}
	present := st == stateRead && !isNull(v)
	switch p.Op {
	case OpPresent:
		return truthOf(present)
	case OpAbsent:
		return truthOf(!present)
	case OpLacks:
		switch t := rd.pred(Pred{Path: p.Path, Op: OpContains, Value: p.Value}, body); t {
		case truthUndecided:
			return t
		case truthHolds:
			return truthFails
		}
		return truthHolds
	case OpEquals, OpPrefix, OpContains:
		if st != stateRead || v == nil {
			return truthFails
		}
		text, read := rd.text(v, 0)
		switch read {
		case stateUndecided:
			return truthUndecided
		case stateNone:
			return truthFails
		}
		text, want := strings.ToLower(text), strings.ToLower(p.Value)
		switch p.Op {
		case OpEquals:
			return truthOf(text == want)
		case OpPrefix:
			return truthOf(strings.HasPrefix(text, want))
		default:
			return truthOf(strings.Contains(text, want))
		}
	}
	return truthFails
}

func truthOf(b bool) truth {
	if b {
		return truthHolds
	}
	return truthFails
}

// at returns what body writes at path: stateRead and the leaf value there, nil
// for an object or an array; stateNone where nothing is written there; and
// stateUndecided where the path runs through a value the import cannot read.
// A member is found by its name regardless of case, and an array's item by
// its index in decimal. A path running through a literal, or a parameter
// whose default is one, is read inside the literal's JSON.
func (rd reader) at(body []Prop, path []string) (state, Value) {
	if len(path) == 0 {
		return stateNone, nil
	}
	props, items := body, []Prop(nil)
	for i, seg := range path {
		p, ok := child(props, items, seg)
		if !ok {
			return stateNone, nil
		}
		if i == len(path)-1 {
			return stateRead, p.Value
		}
		if p.Value == nil {
			props, items = p.Props, p.Items
			continue
		}
		raw, st := rd.literal(p.Value, 0)
		if st != stateRead {
			return st, nil
		}
		if inner, ok := descend(raw, path[i+1:]); ok {
			return stateRead, Literal{JSON: inner}
		}
		return stateNone, nil
	}
	return stateNone, nil
}

// child returns the member of props named seg — the first of exactly that
// name, else the first whose name differs from it only in case — or where
// props is empty the item of items at the index seg writes in decimal.
func child(props, items []Prop, seg string) (Prop, bool) {
	for _, p := range props {
		if p.Name == seg {
			return p, true
		}
	}
	for _, p := range props {
		if strings.EqualFold(p.Name, seg) {
			return p, true
		}
	}
	if i, ok := index(seg, len(items)); ok {
		return items[i], true
	}
	return Prop{}, false
}

// index returns the index seg writes in decimal, where it is one below n.
func index(seg string, n int) (int, bool) {
	i, err := strconv.Atoi(seg)
	if err != nil || i < 0 || i >= n || strconv.Itoa(i) != seg {
		return 0, false
	}
	return i, true
}

// descend returns the JSON at path inside raw, an object's member found as
// member finds it.
func descend(raw json.RawMessage, path []string) (json.RawMessage, bool) {
	for _, seg := range path {
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) == nil && object != nil {
			next, ok := member(object, seg)
			if !ok {
				return nil, false
			}
			raw = next
			continue
		}
		var array []json.RawMessage
		if json.Unmarshal(raw, &array) != nil {
			return nil, false
		}
		i, ok := index(seg, len(array))
		if !ok {
			return nil, false
		}
		raw = array[i]
	}
	return raw, true
}

// member returns the member of object named seg: the member of exactly that
// name, else the first in sorted order whose name differs from it only in
// case.
func member(object map[string]json.RawMessage, seg string) (json.RawMessage, bool) {
	if v, ok := object[seg]; ok {
		return v, true
	}
	for _, name := range slices.Sorted(maps.Keys(object)) {
		if strings.EqualFold(name, seg) {
			return object[name], true
		}
	}
	return nil, false
}

// literal returns the JSON v holds: a literal's, or a parameter's literal
// default's. A parameter with no default, one whose default the import cannot
// read, a reference, a credential and deploy-time text are undecided; a
// composed value is text, and holds no JSON to read inside. hops counts the
// parameters read on the way to v.
func (rd reader) literal(v Value, hops int) (json.RawMessage, state) {
	switch v := v.(type) {
	case Literal:
		return v.JSON, stateRead
	case Param:
		def, ok := rd.defaultOf(v.Name, hops)
		if !ok {
			return nil, stateUndecided
		}
		return rd.literal(def, hops+1)
	case Compose, nil:
		return nil, stateNone
	}
	return nil, stateUndecided
}

// text returns v read as text: a string literal's string, a number's or a
// boolean's JSON text, a parameter's default read as text, and a composed
// value's parts read as text and concatenated. A null, an object, an array
// and a composed value holding one have no text; a value the import cannot
// read is undecided. hops counts the parameters read on the way to v.
func (rd reader) text(v Value, hops int) (string, state) {
	switch v := v.(type) {
	case Literal:
		return literalText(v.JSON)
	case Param:
		def, ok := rd.defaultOf(v.Name, hops)
		if !ok {
			return "", stateUndecided
		}
		return rd.text(def, hops+1)
	case Compose:
		var b strings.Builder
		result := stateRead
		for _, part := range v.Parts {
			text, st := rd.text(part, hops)
			switch st {
			case stateNone:
				return "", stateNone
			case stateUndecided:
				result = stateUndecided
			}
			b.WriteString(text)
		}
		if result != stateRead {
			return "", result
		}
		return b.String(), stateRead
	case nil:
		return "", stateNone
	}
	return "", stateUndecided
}

// literalText returns a JSON scalar as text.
func literalText(raw json.RawMessage) (string, state) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return "", stateNone
	}
	switch v := v.(type) {
	case string:
		return v, stateRead
	case json.Number:
		return v.String(), stateRead
	case bool:
		return strconv.FormatBool(v), stateRead
	}
	return "", stateNone
}

// defaultOf returns the default of the first parameter of g named name, false
// where g declares no such parameter or it has no default. hops counts the
// parameters read on the way: a chain of defaults reading more parameters than
// g declares runs in a cycle, and reads none.
func (rd reader) defaultOf(name string, hops int) (Value, bool) {
	if rd.g == nil || hops >= len(rd.g.Params) {
		return nil, false
	}
	for _, p := range rd.g.Params {
		if p.Name == name {
			return p.Default, p.Default != nil
		}
	}
	return nil, false
}

// isNull reports whether v is the literal null.
func isNull(v Value) bool {
	l, ok := v.(Literal)
	return ok && bytes.Equal(bytes.TrimSpace(l.JSON), []byte("null"))
}
