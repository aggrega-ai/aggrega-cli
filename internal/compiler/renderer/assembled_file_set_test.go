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

// TestNodeProducerLabel holds the attribution a finding names a contributor by to
// every coordinate that contributor's label carries.
//
// The two-boundary row is the load-bearing one, and it is an equality between two
// labels rather than a spelling: the attribution is compared for equality by the
// surface that reports the producers meeting at one path, so two contributors
// arriving under one label are reported as one producer and a collision between
// them is reported as nothing at all.
func TestNodeProducerLabel(t *testing.T) {
	t.Parallel()

	component := derivedKeyProducerFor(producerKindComponent, "api", "")
	if got, want := nodeProducerLabel(component), "component api"; got != want {
		t.Errorf("nodeProducerLabel(component) = %q, want %q", got, want)
	}
	attachment := derivedKeyProducerFor(producerKindComponent, "api", "logging")
	if got, want := nodeProducerLabel(attachment), "component api carrying attachment logging"; got != want {
		t.Errorf("nodeProducerLabel(component attachment) = %q, want %q", got, want)
	}
	zoneA := derivedKeyProducerFor(producerKindFeature, "logging", "zone-a")
	if got, want := nodeProducerLabel(zoneA), "feature logging attached at boundary zone-a"; got != want {
		t.Errorf("nodeProducerLabel(boundary feature) = %q, want %q", got, want)
	}
	zoneB := derivedKeyProducerFor(producerKindFeature, "logging", "zone-b")
	if nodeProducerLabel(zoneA) == nodeProducerLabel(zoneB) {
		t.Errorf("one instance name at two boundaries reaches one label %q, so the two are "+
			"one producer to every surface that compares attributions", nodeProducerLabel(zoneA))
	}
}

// TestNodeOutputSegment holds the directory a nesting assembler places a
// contributor's files under to the coordinates that contributor carries in the
// author's input.
//
// Two rows are load-bearing for different reasons. The boundary-feature row: the
// per-node key that contributor is held under opens with an angle bracket and
// joins with a colon and an at sign, and a directory named for that key would be
// refused on some platforms and would have to be escaped in a URL. The
// component-scope attachment row: its further coordinate is the instance name,
// which the enclosing directory does NOT state, so a segment dropping it hands
// the attachment the segment its host already has — and the two then compose one
// directory, each holding the other's files, with nothing reporting it because
// the coordinate that told them apart was discarded before any comparison.
func TestNodeOutputSegment(t *testing.T) {
	t.Parallel()

	nodeOutputSegment := func(p derivedKeyProducer) string {
		t.Helper()
		segment, err := nodeOutputSegment("release", p)
		if err != nil {
			t.Fatalf("nodeOutputSegment(%+v) refused an ordinary contributor: %v", p, err)
		}
		return segment
	}

	if got, want := nodeOutputSegment(derivedKeyProducerFor(producerKindComponent, "api", "")), "api"; got != want {
		t.Errorf("nodeOutputSegment(component) = %q, want %q", got, want)
	}
	// A component producer with a NON-EMPTY scope is a component-scope
	// attachment, and it must not reach its host's segment.
	host := derivedKeyProducerFor(producerKindComponent, "api", "")
	attachment := derivedKeyProducerFor(producerKindComponent, "api", "logging")
	if got, want := nodeOutputSegment(attachment), "api--logging"; got != want {
		t.Errorf("nodeOutputSegment(component-scope attachment) = %q, want %q", got, want)
	}
	if nodeOutputSegment(attachment) == nodeOutputSegment(host) {
		t.Errorf("an attachment on %q reaches its host's own segment %q, so both would be placed at one "+
			"directory and each would hold the other's files",
			host.name, nodeOutputSegment(host))
	}
	feature := derivedKeyProducerFor(producerKindFeature, "logging", "zone-a")
	if got, want := nodeOutputSegment(feature), "logging"; got != want {
		t.Errorf("nodeOutputSegment(boundary feature) = %q, want %q", got, want)
	}
	// A boundary-scope feature keeps its instance alone: its further coordinate
	// is the boundary, which IS the position this segment sits inside, so
	// carrying it would state that boundary twice.
	if got := nodeOutputSegment(feature); strings.Contains(got, "zone-a") {
		t.Errorf("the segment for a boundary feature is %q, which repeats the boundary the position "+
			"already names", got)
	}
	for _, bad := range []string{"<", ">", ":", "@"} {
		if strings.Contains(nodeOutputSegment(feature), bad) {
			t.Errorf("the segment for a boundary feature carries %q, which a path refuses on some platforms", bad)
		}
	}
}

// TestABoundaryFeatureNamedDotOrDotDotIsNotAContributorDirectory drives the one
// composition of a contributor's directory with a boundary-level Feature named
// "." and "..", each refused with the condition's own identity and with the
// Release, the boundary and the instance named, and with a dotted ordinary name
// admitted as itself.
//
// The single-coordinate case is the one that can reach these strings: the
// composition carries an admitted name through unchanged, and a full stop is
// admitted, so "." composes to "." — which as a directory is the position holding
// every contributor rather than a position of the Feature's own. The two-
// coordinate rows are the control that the guard is about the string and not the
// kind: a component-scope attachment named "." always carries the separator, so
// it composes to a directory name of its own and is admitted.
func TestABoundaryFeatureNamedDotOrDotDotIsNotAContributorDirectory(t *testing.T) {
	t.Parallel()

	for _, name := range []string{".", ".."} {
		name := name
		t.Run("refused "+name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("nodeOutputSegment panicked on the instance name %q: %v", name, r)
				}
			}()
			segment, err := nodeOutputSegment("shop", derivedKeyProducerFor(producerKindFeature, name, "prod"))
			if !errors.Is(err, ErrBoundaryFeatureNameNotADirectory()) {
				t.Fatalf("a boundary Feature named %q composed the directory %q (err %v); want "+
					"ErrBoundaryFeatureNameNotADirectory", name, segment, err)
			}
			for _, want := range []string{`Release "shop"`, `boundary "prod"`, `feature "` + name + `"`, "rename"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not carry %q:\n%s", want, err)
				}
			}
		})
	}

	for _, tc := range []struct {
		name     string
		producer derivedKeyProducer
		want     string
	}{
		{"a dotted boundary Feature name", derivedKeyProducerFor(producerKindFeature, "a.b", "prod"), "a.b"},
		{"a name opening with two dots", derivedKeyProducerFor(producerKindFeature, "..a", "prod"), "..a"},
		{"a component-scope attachment named dot", derivedKeyProducerFor(producerKindComponent, "api", "."), ""},
	} {
		tc := tc
		t.Run("admitted "+tc.name, func(t *testing.T) {
			t.Parallel()
			segment, err := nodeOutputSegment("shop", tc.producer)
			if err != nil {
				t.Fatalf("%s was refused: %v", tc.name, err)
			}
			if tc.want != "" && segment != tc.want {
				t.Errorf("%s composed %q; want %q", tc.name, segment, tc.want)
			}
			if segment == "." || segment == ".." {
				t.Errorf("%s composed %q, which is not a directory name", tc.name, segment)
			}
		})
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
