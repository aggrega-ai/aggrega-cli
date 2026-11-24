// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"path"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// Unit is one source an import is handed that it could start from — a
// template, a stack, a root module — with the sources it links.
type Unit struct {
	// Path is the unit's slash-separated path inside the imported directory.
	Path string
	// Links are the paths of the sources the unit links, each relative to the
	// unit's own directory, as the source writes them.
	Links []string
}

// SelectRoot returns the unit the import starts from and the other candidates,
// in path order. A candidate is a unit no other unit links. root is the root
// setting: where it is set, the unit at that path is the one the import starts
// from, a candidate or not; where it is not, the one candidate is. A root
// naming no unit, or several candidates and no root, halts source-refused
// naming the candidates and the root setting.
func SelectRoot(units []Unit, root string) (Unit, []Unit, *aggrega.Diagnostic) {
	sorted := append([]Unit(nil), units...)
	sort.SliceStable(sorted, func(i, j int) bool { return path.Clean(sorted[i].Path) < path.Clean(sorted[j].Path) })

	linked := map[string]bool{}
	for _, u := range sorted {
		own := path.Clean(u.Path)
		for _, link := range u.Links {
			if target := path.Join(path.Dir(own), link); target != own {
				linked[target] = true
			}
		}
	}
	var candidates []Unit
	for _, u := range sorted {
		if !linked[path.Clean(u.Path)] {
			candidates = append(candidates, u)
		}
	}

	if root != "" {
		want := path.Clean(root)
		for _, u := range sorted {
			if path.Clean(u.Path) == want {
				return u, without(candidates, u), nil
			}
		}
		return Unit{}, nil, rootNamesNoSource(candidates, sorted)
	}
	if len(candidates) == 1 {
		return candidates[0], nil, nil
	}
	return Unit{}, nil, noRootChosen(candidates, sorted)
}

// without returns units less the one at u's path.
func without(units []Unit, u Unit) []Unit {
	var out []Unit
	for _, other := range units {
		if path.Clean(other.Path) != path.Clean(u.Path) {
			out = append(out, other)
		}
	}
	return out
}

// pathList returns the paths of the candidates, or of every unit where no unit
// is a candidate.
func pathList(candidates, units []Unit) string {
	if len(candidates) == 0 {
		candidates = units
	}
	paths := make([]string, len(candidates))
	for i, u := range candidates {
		paths[i] = u.Path
	}
	return strings.Join(paths, ", ")
}

// rootNamesNoSource halts on a root setting naming no unit.
func rootNamesNoSource(candidates, units []Unit) *aggrega.Diagnostic {
	if len(units) == 0 {
		d := Report(CodeSourceRefused, "the root setting names a source, and the import holds none it can start "+
			"from: import the directory holding the source, or leave the root setting out")
		return &d
	}
	d := Report(CodeSourceRefused, "the root setting names no source the import holds: set root to the path, "+
		"inside the imported directory, of one of "+pathList(candidates, units))
	return &d
}

// noRootChosen halts where the root setting is not set and the units hold no
// one candidate.
func noRootChosen(candidates, units []Unit) *aggrega.Diagnostic {
	var d aggrega.Diagnostic
	switch {
	case len(units) == 0:
		d = Report(CodeSourceRefused, "the import holds no source it can start from: import the directory "+
			"holding one")
	case len(candidates) == 0:
		d = Report(CodeSourceRefused, "every source the import holds is linked by another, so none is where it "+
			"starts: set root to the path of the one to start from, one of "+pathList(candidates, units))
	default:
		d = Report(CodeSourceRefused, "the sources "+pathList(candidates, units)+" are each linked by no other "+
			"source, and the root setting names none of them: set root to the path of the one to start from")
	}
	return &d
}
