// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestAssembledFileSet_AttributionInvariantUnderPlacementOrder proves the
// property directly at the unit that owns it: the same placements, offered in
// any order, resolve to the same attribution and the same retained record. The
// contributing maps upstream iterate in randomised order, so any dependence on
// placement order here would surface as a build whose diagnostic named its
// contributors differently on different runs of identical input.
func TestAssembledFileSet_AttributionInvariantUnderPlacementOrder(t *testing.T) {
	t.Parallel()

	placements := []assembledFileEntry{
		{path: "zeta.yaml", file: AssembledFile{Content: []byte("z")}, producer: "component one"},
		{path: "alpha.yaml", file: AssembledFile{Content: []byte("a"), Format: "json"}, producer: "component two"},
		{path: "zeta.yaml", file: AssembledFile{Content: []byte("z2")}, producer: "component three"},
		{path: "alpha.yaml", file: AssembledFile{Content: []byte("a2")}, producer: "component four"},
		{path: "alpha.yaml", file: AssembledFile{Content: []byte("a3")}, producer: "component five"},
	}

	var wantProducers map[string][]string
	var wantFiles map[string]AssembledFile
	for _, order := range permutations(len(placements)) {
		var set assembledFileSet
		for _, i := range order {
			p := placements[i]
			set.put(p.path, p.file, p.producer)
		}
		files, producers := set.resolve()
		if wantProducers == nil {
			wantProducers, wantFiles = producers, files
			continue
		}
		if !reflect.DeepEqual(producers, wantProducers) {
			t.Fatalf("attribution depends on placement order\nwant: %v\ngot (order %v): %v", wantProducers, order, producers)
		}
		if !reflect.DeepEqual(files, wantFiles) {
			t.Fatalf("retained record depends on placement order\nwant: %v\ngot (order %v): %v", wantFiles, order, files)
		}
	}

	// Every contributor is named, in producer order, and the paths stay
	// group-relative: the disk-write prefix is reattached at the final flush.
	for path, want := range map[string][]string{
		"alpha.yaml": {"component five", "component four", "component two"},
		"zeta.yaml":  {"component one", "component three"},
	} {
		if got := wantProducers[path]; !reflect.DeepEqual(got, want) {
			t.Errorf("producers for %q = %v, want %v", path, got, want)
		}
	}
}

// TestAssembledFileSet_DistinctPathsResolve confirms the ordinary path: distinct
// placements resolve into the group's file map with their records intact, each
// attributed to the one contributor that placed it.
func TestAssembledFileSet_DistinctPathsResolve(t *testing.T) {
	t.Parallel()

	var set assembledFileSet
	set.put("a.yaml", AssembledFile{Content: []byte("a"), Format: "yaml"}, "component one")
	set.put("b.yaml", AssembledFile{Content: []byte("b")}, "component two")

	files, producers := set.resolve()
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %v", files)
	}
	// Keys stay group-relative — the prefix is reattached at the final flush.
	if got := files["a.yaml"]; string(got.Content) != "a" || got.Format != "yaml" {
		t.Errorf("record for a.yaml did not survive resolve: %+v", got)
	}
	if got, want := producers["a.yaml"], []string{"component one"}; !reflect.DeepEqual(got, want) {
		t.Errorf("producers for a.yaml = %v, want %v", got, want)
	}
	if len(producers) != len(files) {
		t.Errorf("the attribution and the file map must share one key set: %v vs %v", producers, files)
	}
}

func TestNodeProducerLabel(t *testing.T) {
	t.Parallel()

	if got, want := nodeProducerLabel("api"), "component api"; got != want {
		t.Errorf("nodeProducerLabel(api) = %q, want %q", got, want)
	}
	if got, want := nodeProducerLabel(boundaryFRNodePrefix+"logging"), "feature logging"; got != want {
		t.Errorf("nodeProducerLabel(boundary feature) = %q, want %q", got, want)
	}
}

// TestNodeOutputSegment holds the directory a nesting assembler places a
// contributor's files under to the name that contributor carries in the author's
// input. The boundary-feature row is the load-bearing one: the per-node key it
// is derived from opens with an angle bracket and joins with a colon, and a
// directory named for that key would be refused on some platforms and would have
// to be escaped in a URL.
func TestNodeOutputSegment(t *testing.T) {
	t.Parallel()

	if got, want := nodeOutputSegment("api"), "api"; got != want {
		t.Errorf("nodeOutputSegment(api) = %q, want %q", got, want)
	}
	if got, want := nodeOutputSegment(boundaryFRNodePrefix+"logging"), "logging"; got != want {
		t.Errorf("nodeOutputSegment(boundary feature) = %q, want %q", got, want)
	}
	for _, bad := range []string{"<", ">", ":"} {
		if strings.Contains(nodeOutputSegment(boundaryFRNodePrefix+"logging"), bad) {
			t.Errorf("the segment for a boundary feature carries %q, which a path refuses on some platforms", bad)
		}
	}
}

// TestAssembledFileSetIsTheOnlyPlacementPath keeps the attribution reachable from
// every producer. Two properties hold it in place:
//
//   - assembledFileSet carries no map, so placing a file cannot be spelled as an
//     index expression that skips the producer label.
//   - a dispatch group's files and its attribution come only from resolve, so a
//     producer added later cannot hand the group a map with nobody recorded
//     against it.
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
				"every group's files must arrive with their attribution (near offset %d)", at)
		}
	}
	if constructions == 0 {
		t.Error("no assembledGroup construction found in orchestrator.go; this guard has stopped guarding anything")
	}
	// The other way to hand a group unattributed files is to build it empty and
	// fill either field afterwards.
	for _, assignment := range []string{".files =", ".producers ="} {
		if strings.Contains(text, assignment) {
			t.Errorf("a group's %q is assigned outside its construction; both must come from assembledFileSet.resolve",
				strings.TrimSuffix(assignment, " ="))
		}
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
