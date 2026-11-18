// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// maxNearest bounds the names a not-found halt offers.
const maxNearest = 5

// Select chooses the types to draft from refs, the types the sources hold, as
// reqs asks, in request order. With no request and one type it chooses that
// type; with no request and no type it chooses none and reports nothing. It
// halts with resource-type-not-named where several types are held and
// none is requested (countOnly gives the count and the setting to write in
// place of the list), with resource-type-not-found where a request names no
// held type or, for an apiVersion, one the file does not hold, and with what
// resolve reports. foldCase compares names without regard to case. resolve is
// called with every ref a requested name matches when more than one does; nil
// means refs are unique by name. Where any halt is raised nothing is selected.
func Select(refs []TypeRef, reqs []Request, foldCase, countOnly bool,
	resolve func(name string, refs []TypeRef) (TypeRef, []aggrega.Diagnostic)) ([]Selected, []aggrega.Diagnostic) {
	if len(reqs) == 0 {
		names := distinctNames(refs)
		switch {
		case len(names) == 0:
			return nil, nil
		case len(names) == 1:
			return pick(refs, names[0], "", foldCase, resolve)
		}
		return nil, []aggrega.Diagnostic{resourceTypeNotNamed(names, countOnly, commonSource(refs))}
	}
	var out []Selected
	var diags []aggrega.Diagnostic
	halted := false
	for _, req := range reqs {
		matches := matching(refs, req.Type, foldCase)
		if len(matches) == 0 {
			diags = append(diags, resourceTypeNotFound(req.Type, nearest(refs, req.Type), commonSource(refs)))
			halted = true
			continue
		}
		if req.APIVersion != "" {
			var kept []TypeRef
			for _, m := range matches {
				if m.APIVersion == req.APIVersion {
					kept = append(kept, m)
				}
			}
			if len(kept) == 0 {
				var held []string
				for _, m := range matches {
					if m.APIVersion != "" && !slices.Contains(held, m.APIVersion) {
						held = append(held, m.APIVersion)
					}
				}
				diags = append(diags, resourceTypeVersionNotFound(req.Type, req.APIVersion, held, commonSource(matches)))
				halted = true
				continue
			}
			matches = kept
		}
		sel, d := resolveOne(matches, req.Component, resolve)
		diags = append(diags, d...)
		if hasHalt(d) {
			halted = true
			continue
		}
		out = append(out, sel...)
	}
	if halted {
		return nil, diags
	}
	return out, diags
}

// pick selects the one type named name from refs.
func pick(refs []TypeRef, name, component string, foldCase bool,
	resolve func(string, []TypeRef) (TypeRef, []aggrega.Diagnostic)) ([]Selected, []aggrega.Diagnostic) {
	sel, d := resolveOne(matching(refs, name, foldCase), component, resolve)
	if hasHalt(d) {
		return nil, d
	}
	return sel, d
}

// resolveOne selects the one ref of matches: the only one, or what resolve picks.
func resolveOne(matches []TypeRef, component string,
	resolve func(string, []TypeRef) (TypeRef, []aggrega.Diagnostic)) ([]Selected, []aggrega.Diagnostic) {
	if len(matches) == 1 || resolve == nil {
		return []Selected{{Ref: matches[0], Component: component}}, nil
	}
	ref, d := resolve(matches[0].Name, matches)
	if hasHalt(d) {
		return nil, d
	}
	return []Selected{{Ref: ref, Component: component}}, d
}

func hasHalt(ds []aggrega.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == aggrega.SeverityHalt {
			return true
		}
	}
	return false
}

// matching returns the refs named name, in source order.
func matching(refs []TypeRef, name string, foldCase bool) []TypeRef {
	var out []TypeRef
	for _, r := range refs {
		if r.Name == name || (foldCase && strings.EqualFold(r.Name, name)) {
			out = append(out, r)
		}
	}
	return out
}

// commonSource returns the source every ref of refs was read from, empty where
// they were read from several or there are none.
func commonSource(refs []TypeRef) string {
	if len(refs) == 0 {
		return ""
	}
	for _, r := range refs[1:] {
		if r.Source != refs[0].Source {
			return ""
		}
	}
	return refs[0].Source
}

// distinctNames returns the names refs carry, each once, in source order.
func distinctNames(refs []TypeRef) []string {
	var out []string
	for _, r := range refs {
		if !slices.Contains(out, r.Name) {
			out = append(out, r.Name)
		}
	}
	return out
}

// nearest returns up to maxNearest names of refs closest to name by edit
// distance counted without regard to case, ties by name.
func nearest(refs []TypeRef, name string) []string {
	names := distinctNames(refs)
	folded := strings.ToLower(name)
	dist := func(n string) int { return importer.EditDistance(folded, strings.ToLower(n)) }
	slices.SortStableFunc(names, func(a, b string) int {
		if d := dist(a) - dist(b); d != 0 {
			return d
		}
		return strings.Compare(a, b)
	})
	if len(names) > maxNearest {
		names = names[:maxNearest]
	}
	return names
}
