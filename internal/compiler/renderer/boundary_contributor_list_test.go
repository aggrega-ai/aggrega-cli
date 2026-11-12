// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer_test

import (
	"context"
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// contributorCaptureEngine renders a fixed file per node and records the
// BoundaryInfo its dispatch group hands its assembler, so a test can read what
// an assembler is actually given rather than what the map is named. mode is
// the boundary-assembly claim the engine reports, which is what decides whether
// per-node output travels as fragments to the assembler or is placed by the
// orchestrator.
type contributorCaptureEngine struct {
	collisionEngine
	mode string
	got  renderer.BoundaryInfo
}

func (e *contributorCaptureEngine) BoundaryAssembly() string { return e.mode }

// NestSegment names the directory this engine's contributors sit under. It is
// declared on the engine rather than returned from AssembleBoundary because the
// orchestrator reads a group's layout from the provider's own self-description.
func (e *contributorCaptureEngine) NestSegment() string { return "units" }

func (e *contributorCaptureEngine) AssembleBoundary(_ context.Context, info renderer.BoundaryInfo) (map[string]renderer.AssembledFile, error) {
	e.got = info
	return map[string]renderer.AssembledFile{
		"assembled.txt": {Content: []byte("assembled\n")},
	}, nil
}

// filesFor returns the recorded per-node file names for one node, drawn from
// whichever map the group's mode routed them through, so a test asserts on
// the contributor list an assembler sees rather than on one map.
func contributorFiles(info renderer.BoundaryInfo, node string) []string {
	var out []string
	for _, f := range info.IntermediatesByNode[node] {
		out = append(out, f.Name)
	}
	for _, f := range info.PlacedByNode[node] {
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}

// TestBoundaryInfoNamesEveryContributor is the property a nesting assembler
// cannot work without: it must be able to see the components it is assembling
// for. A nesting provider's per-node output is placed by the orchestrator rather
// than merged, so it never travels as a fragment — and an assembler handed only
// the fragments is handed nothing at all, which is exactly the state that makes a
// root-composes-children layout impossible to author. Both modes are asserted
// because the two maps are complements and a fix to one must not empty the
// other.
func TestBoundaryInfoNamesEveryContributor(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"nest", "merge"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			engine := &contributorCaptureEngine{
				collisionEngine: collisionEngine{emit: []renderer.RenderedFile{
					{Path: "unit.txt", Content: []byte("u\n")},
				}},
				mode: mode,
			}
			if _, err := runCollisionRender([]string{"alpha", "beta"}, engine); err != nil {
				t.Fatalf("render: %v", err)
			}

			names := map[string]bool{}
			for id := range engine.got.IntermediatesByNode {
				names[id] = true
			}
			for id := range engine.got.PlacedByNode {
				names[id] = true
			}
			for _, want := range []string{"alpha", "beta"} {
				if !names[want] {
					t.Errorf("assembler was not told about contributor %q; it saw %v", want, names)
				}
				if files := contributorFiles(engine.got, want); len(files) == 0 {
					t.Errorf("contributor %q reached the assembler with no files", want)
				} else if files[0] != "unit.txt" {
					t.Errorf("contributor %q carried %v, want unit.txt", want, files)
				}
			}
			if n := len(names); n != 2 {
				t.Errorf("expected exactly the 2 contributors, got %d: %v", n, names)
			}
		})
	}
}

// TestPlacedAndIntermediateMapsAreComplements pins the split the two maps
// mean, so neither grows into the other. A fragment-merging provider's per-node
// output is intermediate and reaches the assembler for merging; a nesting
// provider's is not intermediate and is placed by the orchestrator. A node
// appearing in both maps would mean one file was routed twice.
func TestPlacedAndIntermediateMapsAreComplements(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ intermediates, placed bool }{
		"nest":  {intermediates: false, placed: true},
		"merge": {intermediates: true, placed: false},
	}
	for mode, want := range cases {
		engine := &contributorCaptureEngine{
			collisionEngine: collisionEngine{emit: []renderer.RenderedFile{
				{Path: "unit.txt", Content: []byte("u\n")},
			}},
			mode: mode,
		}
		if _, err := runCollisionRender([]string{"alpha"}, engine); err != nil {
			t.Fatalf("%s: render: %v", mode, err)
		}
		gotIntermediates := len(engine.got.IntermediatesByNode["alpha"]) > 0
		gotPlaced := len(engine.got.PlacedByNode["alpha"]) > 0
		if gotIntermediates != want.intermediates {
			t.Errorf("%s: intermediates carried = %v, want %v", mode, gotIntermediates, want.intermediates)
		}
		if gotPlaced != want.placed {
			t.Errorf("%s: placed carried = %v, want %v", mode, gotPlaced, want.placed)
		}
	}
}

// TestNestedContributorsLandUnderTheSegmentTheAssemblerNames closes the loop from
// the contributor list back to the emitted tree: an assembler that nests names
// the directory its contributors sit under, and each contributor's files land at
// that segment joined with its own identity. This is what makes two components
// that write identically-named files coexist in one boundary.
func TestNestedContributorsLandUnderTheSegmentTheAssemblerNames(t *testing.T) {
	t.Parallel()

	engine := &contributorCaptureEngine{
		collisionEngine: collisionEngine{emit: []renderer.RenderedFile{
			{Path: "unit.txt", Content: []byte("u\n")},
		}},
		mode: "nest",
	}
	res, err := runCollisionRender([]string{"alpha", "beta"}, engine)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	got := sortedKeys(res.Files)
	for _, want := range []string{"units/alpha/unit.txt", "units/beta/unit.txt", "assembled.txt"} {
		found := false
		for _, k := range got {
			if k == want || len(k) > len(want) && k[len(k)-len(want)-1:] == "/"+want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected an output at %q; got %v", want, got)
		}
	}
}
