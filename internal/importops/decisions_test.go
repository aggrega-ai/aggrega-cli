// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
)

// decisionLineCasesPath is the case manifest the mapping-decision line is held to
// here and in the editor extension's own spec, relative to this package.
const decisionLineCasesPath = "testdata/decision_line_cases.json"

// wantDecisionLineCasesRevision is the revision of the case manifest THIS side
// was written against, and TestDecisionLineRendersEveryCase refuses any other.
//
// 🔴 A DELIBERATE NEAR-DUPLICATE OF THE EDITOR SPEC'S OWN PINNED COPY, AND IT MUST
// NOT BE UNIFIED WITH IT — not read out of the manifest, not shared, not generated.
// The line is rendered twice across a language boundary on purpose, and the two
// pins are what make a green from each side evidence that the side read this
// revision rather than evidence that somebody's comparison agreed. One constant
// cannot disagree with itself, and disagreement is the whole signal.
const wantDecisionLineCasesRevision = 1

// decisionLineCase is one manifest entry: a decision exactly as the session
// answers it, and the line it must render as.
type decisionLineCase struct {
	Name     string             `json:"name"`
	Decision abi.ImportDecision `json:"decision"`
	Line     string             `json:"line"`
}

// decisionLineCases is the manifest as a whole.
type decisionLineCases struct {
	Revision int                `json:"revision"`
	Langflow []decisionLineCase `json:"langflow"`
	Shapes   []decisionLineCase `json:"shapes"`
}

// readDecisionLineCases reads the manifest at path, failing the test where it
// cannot be read or holds no case of either kind — a manifest read as empty would
// let every assertion over it pass without comparing anything.
func readDecisionLineCases(t *testing.T, path string) decisionLineCases {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the decision-line cases at %s: %v", path, err)
	}
	var cases decisionLineCases
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("decode the decision-line cases at %s: %v", path, err)
	}
	if len(cases.Langflow) == 0 || len(cases.Shapes) == 0 {
		t.Fatalf("the decision-line cases at %s hold %d Langflow decision(s) and %d shape(s); both kinds are "+
			"needed, or the comparison below covers nothing of that kind", path, len(cases.Langflow), len(cases.Shapes))
	}
	return cases
}

// TestDecisionLineRendersEveryCase holds the one formatter of the mapping-decision
// line to every case of the shared manifest, the line each must render as written
// out in the manifest rather than composed here.
//
// The Langflow cases are the decisions a real import of the committed fixture
// returns under the node-by-node layout; the command's end-to-end spec requires
// the live answer to equal them. The shapes are those the reference importers do
// not produce from that fixture under that layout, and each pins one rule of the
// line: a reference, the top score taken whatever order the candidates are listed
// in, a whole number, a score whose shortest form runs past four places, no
// candidate, and no choice — the line every node a platform's flow keeps reads as.
//
// 🔴 PLANTED DEFECTS: format the score with %.4f in DecisionLine and the whole and
// long-score shapes redden; take the first candidate's score rather than the
// highest and the shape listing the lowest first reddens; write "at 0" for a
// decision with no candidate and that shape and the third Langflow case redden.
func TestDecisionLineRendersEveryCase(t *testing.T) {
	t.Parallel()
	cases := readDecisionLineCases(t, decisionLineCasesPath)
	if cases.Revision != wantDecisionLineCasesRevision {
		t.Fatalf("the decision-line manifest declares revision %d; this side is pinned to %d. Move this pin and "+
			"the editor spec's own pin together, and run both: a pair whose sides name different revisions has "+
			"not compared anything", cases.Revision, wantDecisionLineCasesRevision)
	}
	for _, kind := range []struct {
		name  string
		cases []decisionLineCase
	}{{"langflow", cases.Langflow}, {"shapes", cases.Shapes}} {
		for i, tc := range kind.cases {
			if got := DecisionLine(tc.Decision); got != tc.Line {
				t.Errorf("%s[%d] %s: DecisionLine rendered\n  %q\nwant\n  %q", kind.name, i, tc.Name, got, tc.Line)
			}
		}
	}
}
