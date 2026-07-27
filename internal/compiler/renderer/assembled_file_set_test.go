// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestAssembledFileSet_DiagnosticInvariantUnderPlacementOrder proves the
// property directly at the unit that owns it: the same placements, offered in
// any order, produce the same diagnostic. The contributing maps upstream iterate
// in randomised order, so any dependence on placement order here would surface
// as a build that fails with a different message on different runs of identical
// input.
func TestAssembledFileSet_DiagnosticInvariantUnderPlacementOrder(t *testing.T) {
	t.Parallel()

	placements := []assembledFileEntry{
		{path: "zeta.yaml", file: AssembledFile{Content: []byte("z")}, producer: `component "one"`},
		{path: "alpha.yaml", file: AssembledFile{Content: []byte("a"), Format: "json"}, producer: `component "two"`},
		{path: "zeta.yaml", file: AssembledFile{Content: []byte("z2")}, producer: `component "three"`},
		{path: "alpha.yaml", file: AssembledFile{Content: []byte("a2")}, producer: `component "four"`},
		{path: "alpha.yaml", file: AssembledFile{Content: []byte("a3")}, producer: `component "five"`},
	}

	var want string
	for _, order := range permutations(len(placements)) {
		var set assembledFileSet
		for _, i := range order {
			p := placements[i]
			set.put(p.path, p.file, p.producer)
		}
		_, err := set.resolve("out/")
		if err == nil {
			t.Fatalf("order %v: expected a collision", order)
		}
		if !errors.Is(err, ErrAssembledOutputCollision) {
			t.Fatalf("order %v: expected ErrAssembledOutputCollision, got %v", order, err)
		}
		if want == "" {
			want = err.Error()
			continue
		}
		if err.Error() != want {
			t.Fatalf("diagnostic depends on placement order\nwant: %s\ngot (order %v): %s", want, order, err.Error())
		}
	}

	// The colliding paths carry the group's disk-write prefix, are reported in
	// path order, and every contributor is named — with the declared format
	// spelled out only where the contributors disagree about it.
	for _, fragment := range []string{
		`"out/alpha.yaml" is written by component "five" (no declared format), component "four" (no declared format) and component "two" (declared format "json")`,
		`"out/zeta.yaml" is written by component "one" and component "three"`,
	} {
		if !strings.Contains(want, fragment) {
			t.Errorf("diagnostic missing %q\ngot: %s", fragment, want)
		}
	}
	if strings.Index(want, "out/alpha.yaml") > strings.Index(want, "out/zeta.yaml") {
		t.Errorf("colliding paths must be reported in path order, got: %s", want)
	}
}

// TestAssembledFileSet_DistinctPathsResolve confirms the ordinary path: distinct
// placements resolve into the group's file map with their records intact.
func TestAssembledFileSet_DistinctPathsResolve(t *testing.T) {
	t.Parallel()

	var set assembledFileSet
	set.put("a.yaml", AssembledFile{Content: []byte("a"), Format: "yaml"}, `component "one"`)
	set.put("b.yaml", AssembledFile{Content: []byte("b")}, `component "two"`)

	files, err := set.resolve("out/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %v", files)
	}
	// Keys stay group-relative — the prefix is reattached at the final flush and
	// is used only to report a collision by the name it would have on disk.
	if got := files["a.yaml"]; string(got.Content) != "a" || got.Format != "yaml" {
		t.Errorf("record for a.yaml did not survive resolve: %+v", got)
	}
}

func TestNodeProducerLabel(t *testing.T) {
	t.Parallel()

	if got, want := nodeProducerLabel("api"), `component "api"`; got != want {
		t.Errorf("nodeProducerLabel(api) = %q, want %q", got, want)
	}
	if got, want := nodeProducerLabel(boundaryFRNodePrefix+"logging"), `feature "logging"`; got != want {
		t.Errorf("nodeProducerLabel(boundary feature) = %q, want %q", got, want)
	}
}

// TestAssembledFileSetIsTheOnlyPlacementPath keeps the guard reachable from
// every producer. Two properties hold it in place:
//
//   - assembledFileSet carries no map, so placing a file cannot be spelled as an
//     index expression that skips the producer label.
//   - a dispatch group's files come only from resolve, so a producer added later
//     cannot hand the group a map the collision check never saw.
func TestAssembledFileSetIsTheOnlyPlacementPath(t *testing.T) {
	t.Parallel()

	setType := reflect.TypeOf(assembledFileSet{})
	for i := 0; i < setType.NumField(); i++ {
		if setType.Field(i).Type.Kind() == reflect.Map {
			t.Errorf("assembledFileSet field %q is a map; a file must be placed through put, not by indexing",
				setType.Field(i).Name)
		}
	}

	src, err := os.ReadFile("orchestrator.go")
	if err != nil {
		t.Fatalf("read orchestrator.go: %v", err)
	}
	text := string(src)
	const marker = "assembledGroup{"
	constructions := 0
	for offset := 0; ; {
		i := strings.Index(text[offset:], marker)
		if i < 0 {
			break
		}
		at := offset + i
		offset = at + len(marker)
		// An empty composite literal is the container the groups are collected
		// in, not a group being built.
		if strings.HasPrefix(text[offset:], "}") {
			continue
		}
		constructions++
		window := text[max(0, at-400):at]
		if !strings.Contains(window, ".resolve(") {
			t.Errorf("an assembledGroup is constructed without its files coming from assembledFileSet.resolve; "+
				"every group's files must pass the collision check (near offset %d)", at)
		}
	}
	if constructions == 0 {
		t.Error("no assembledGroup construction found in orchestrator.go; this guard has stopped guarding anything")
	}
	// The other way to hand a group unchecked files is to build it empty and
	// fill the field afterwards.
	if strings.Contains(text, ".files =") {
		t.Error("a group's files field is assigned outside its construction; the files must come from assembledFileSet.resolve")
	}
}

// permutations returns every ordering of the indices 0..n-1.
func permutations(n int) [][]int {
	if n == 0 {
		return [][]int{{}}
	}
	var out [][]int
	for _, rest := range permutations(n - 1) {
		for pos := 0; pos <= len(rest); pos++ {
			next := make([]int, 0, n)
			next = append(next, rest[:pos]...)
			next = append(next, n-1)
			next = append(next, rest[pos:]...)
			out = append(out, next)
		}
	}
	return out
}
