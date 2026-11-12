// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"encoding/json"
	"os"
	"testing"
)

// driftLineCasesPath is the case manifest the drift line is held to here and in
// the editor extension's own spec, relative to this package.
const driftLineCasesPath = "testdata/drift_line_cases.json"

// wantDriftLineCasesRevision is the revision of the case manifest THIS side was
// written against, and TestDriftLineRendersEveryCase refuses any other.
//
// 🔴 A DELIBERATE NEAR-DUPLICATE OF THE EDITOR SPEC'S OWN PINNED COPY, AND IT MUST
// NOT BE UNIFIED WITH IT — not read out of the manifest, not shared, not generated.
// The line is rendered twice across a language boundary on purpose, and the two
// pins are what make a green from each side evidence that the side read this
// revision rather than evidence that somebody's comparison agreed.
const wantDriftLineCasesRevision = 1

// driftLineCase is one manifest entry: a drift exactly as the session answers it,
// and the line it must render as.
type driftLineCase struct {
	Name  string `json:"name"`
	Drift struct {
		Change string   `json:"change"`
		File   string   `json:"file"`
		Key    []string `json:"key"`
	} `json:"drift"`
	Line string `json:"line"`
}

// TestDriftLineRendersEveryCase holds the one formatter of the drift line to every
// case of the shared manifest, the line each must render as written out in the
// manifest rather than composed here.
//
// 🔴 PLANTED DEFECTS: join the keys in DriftLine without RenderKey's quoting and
// every quoted case reddens; drop " in " and every keyed case reddens.
func TestDriftLineRendersEveryCase(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(driftLineCasesPath)
	if err != nil {
		t.Fatalf("read the drift-line cases at %s: %v", driftLineCasesPath, err)
	}
	var manifest struct {
		Revision int             `json:"revision"`
		Cases    []driftLineCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode the drift-line cases at %s: %v", driftLineCasesPath, err)
	}
	if manifest.Revision != wantDriftLineCasesRevision {
		t.Fatalf("the drift-line manifest declares revision %d; this side is pinned to %d. Move this pin and the "+
			"editor spec's own pin together, and run both", manifest.Revision, wantDriftLineCasesRevision)
	}
	if len(manifest.Cases) == 0 {
		t.Fatalf("the drift-line manifest at %s holds no case, so the comparison below covers nothing", driftLineCasesPath)
	}
	for i, tc := range manifest.Cases {
		got := DriftLine(Drift{Change: tc.Drift.Change, File: tc.Drift.File, Key: tc.Drift.Key})
		if got != tc.Line {
			t.Errorf("cases[%d] %s: DriftLine rendered\n  %q\nwant\n  %q", i, tc.Name, got, tc.Line)
		}
	}
}
