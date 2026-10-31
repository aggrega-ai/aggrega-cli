// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// contradictionDiags returns the wave/edge contradiction advisories among diags:
// the ones naming syncWave that carry no class code. The start-only advisory also
// names syncWave but carries the unsupported-field code, so it stays out.
func contradictionDiags(diags []renderctx.Diagnostic) []renderctx.Diagnostic {
	var out []renderctx.Diagnostic
	for _, d := range diags {
		if d.SourceRef == "syncWave" && d.Code == "" {
			out = append(out, d)
		}
	}
	return out
}

// assembleWaved assembles one healthchecked contributor per entry of waves into a
// single file with the given edges. Every contributor declares a healthcheck, so
// no start-only advisory is raised and the only advisories left are the ones under
// test.
func assembleWaved(t *testing.T, waves map[string]int, edges []renderer.IREdgeInfo) (string, []renderctx.Diagnostic) {
	t.Helper()
	intermediates := map[string][]renderer.EmittedFile{}
	annotations := map[string]map[string]string{}
	for name := range waves {
		intermediates[name] = makeFragment(t, name, waveWorkload(name+":1.0", 8080, true), nil)
		annotations[name] = map[string]string{}
	}
	files, diags, err := AssembleBoundaryCore(intermediates, annotations, waves, edges, "shop", 2)
	if err != nil {
		t.Fatalf("AssembleBoundaryCore: %v", err)
	}
	return string(files[AssembledOutputFile]), diags
}

// TestWaveEdgeContradiction_DirectPairReported proves a service in an earlier
// wave that uses a service in a later wave is reported at build time: one
// warn-severity advisory naming both services and both waves. It also proves
// emission is unchanged by the report — both the edge's entry and the wave's entry
// are still in the file, which is the cycle the advisory describes.
func TestWaveEdgeContradiction_DirectPairReported(t *testing.T) {
	t.Parallel()
	body, diags := assembleWaved(t,
		map[string]int{"frontend": 0, "store": 1},
		[]renderer.IREdgeInfo{{Source: "frontend", Target: "store"}})

	got := contradictionDiags(diags)
	if len(got) != 1 {
		t.Fatalf("got %d contradiction advisories, want 1 (all: %+v)", len(got), diags)
	}
	d := got[0]
	if d.Severity != abi.SeverityWarn {
		t.Errorf("severity = %q, want warn", d.Severity)
	}
	for _, want := range []string{`service "frontend" in wave 0`, `service "store" in wave 1`, "cycle"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("advisory does not say %q; got %q", want, d.Message)
		}
	}
	if len(diags) != 1 {
		t.Errorf("expected the contradiction to be the only advisory; got %+v", diags)
	}

	// Emission is faithful: the edge's entry and the wave's entry both stand.
	if !strings.Contains(serviceBlock(t, body, "frontend"), "- store") {
		t.Errorf("the edge-derived entry frontend -> store is missing:\n%s", body)
	}
	storeBlock := serviceBlock(t, body, "store")
	if !strings.Contains(storeBlock, "frontend:") || !strings.Contains(storeBlock, "condition: service_healthy") {
		t.Errorf("the wave-derived entry store -> frontend is missing:\n%s", body)
	}
}

// TestWaveEdgeContradiction_NotSuppressedWithTheUnsupportedFieldClass proves the
// advisory is outside the unsupported-field class: the target expresses both
// declarations, so the filter that silences fields a target drops must not
// silence a file that cannot start.
func TestWaveEdgeContradiction_NotSuppressedWithTheUnsupportedFieldClass(t *testing.T) {
	t.Parallel()
	_, diags := assembleWaved(t,
		map[string]int{"frontend": 0, "store": 1},
		[]renderer.IREdgeInfo{{Source: "frontend", Target: "store"}})
	var n int
	for _, d := range diags {
		if d.SourceRef != "syncWave" || !strings.Contains(d.Message, "cycle") {
			continue
		}
		n++
		if d.Code == rendercore.UnsupportedFieldCode {
			t.Errorf("the contradiction carries the suppressible class code: %+v", d)
		}
	}
	if n != 1 {
		t.Fatalf("got %d contradiction advisories, want 1 (all: %+v)", n, diags)
	}
}

// TestWaveEdgeContradiction_ReachesAcrossAPopulatedWave proves the report does not
// depend on the two waves being adjacent. With a populated wave between them, no
// wave-derived entry names the consumer directly — store waits on middle, which
// waits on frontend — yet the file is just as cyclic, so the edge is still named.
func TestWaveEdgeContradiction_ReachesAcrossAPopulatedWave(t *testing.T) {
	t.Parallel()
	body, diags := assembleWaved(t,
		map[string]int{"frontend": 0, "middle": 1, "store": 2},
		[]renderer.IREdgeInfo{{Source: "frontend", Target: "store"}})

	got := contradictionDiags(diags)
	if len(got) != 1 {
		t.Fatalf("got %d contradiction advisories, want 1 (all: %+v)", len(got), diags)
	}
	for _, want := range []string{`service "frontend" in wave 0`, `service "store" in wave 2`} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("advisory does not say %q; got %q", want, got[0].Message)
		}
	}
	if strings.Contains(serviceBlock(t, body, "store"), "frontend") {
		t.Errorf("fixture premise broken: store names frontend directly, so this case no longer spans a wave:\n%s", body)
	}
}

// TestWaveEdgeContradiction_ConsistentEdgesSilent is the negative table: an edge
// pointing at an earlier or the same wave agrees with the waves, an edge into a
// service this file does not hold is not projected at all, and a single-wave file
// orders nothing — none of them raises the advisory.
func TestWaveEdgeContradiction_ConsistentEdgesSilent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		waves map[string]int
		edges []renderer.IREdgeInfo
	}{
		{"consumer in a later wave", map[string]int{"app": 1, "db": 0},
			[]renderer.IREdgeInfo{{Source: "app", Target: "db"}}},
		{"consumer in the same wave", map[string]int{"app": 0, "db": 0, "batch": 1},
			[]renderer.IREdgeInfo{{Source: "app", Target: "db"}}},
		{"target outside this file", map[string]int{"app": 0, "db": 1},
			[]renderer.IREdgeInfo{{Source: "app", Target: "elsewhere"}}},
		{"one wave only", map[string]int{"app": 3, "db": 3},
			[]renderer.IREdgeInfo{{Source: "app", Target: "db"}}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, diags := assembleWaved(t, tc.waves, tc.edges)
			if got := contradictionDiags(diags); len(got) != 0 {
				t.Errorf("a consistent edge raised %d contradiction advisories: %+v", len(got), got)
			}
		})
	}
}

// TestWaveEdgeContradiction_OnePerPairInStableOrder proves each contradicting pair
// is named once however often the edge list carries it, and that the advisories
// come out sorted by consumer and then target whatever order the edges arrived in.
func TestWaveEdgeContradiction_OnePerPairInStableOrder(t *testing.T) {
	t.Parallel()
	_, diags := assembleWaved(t,
		map[string]int{"web": 0, "admin": 0, "store": 1, "cache": 1},
		[]renderer.IREdgeInfo{
			{Source: "web", Target: "store"},
			{Source: "admin", Target: "store"},
			{Source: "web", Target: "cache"},
			{Source: "web", Target: "store"},
		})
	got := contradictionDiags(diags)
	want := [][2]string{{"admin", "store"}, {"web", "cache"}, {"web", "store"}}
	if len(got) != len(want) {
		t.Fatalf("got %d contradiction advisories, want %d (all: %+v)", len(got), len(want), diags)
	}
	for i, pair := range want {
		prefix := `service "` + pair[0] + `" in wave 0 uses service "` + pair[1] + `" in wave 1`
		if !strings.HasPrefix(got[i].Message, prefix) {
			t.Errorf("advisory %d = %q, want it to open %q", i, got[i].Message, prefix)
		}
	}
}
