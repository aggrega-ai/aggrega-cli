// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// Placement parity for the structured edit: the command line must carry where
// the entry landed and which container levels the edit brought into being, both
// of which the shared editing layer computes and a session already reports.
//
// The expectation is taken by running the shared operation over one workspace
// and the command over a second, identically seeded one. That is what keeps the
// two halves independent: a comparison drawn from the command's own output would
// move with the command, so a surface that stopped naming the created containers
// would keep passing while the assertion followed it down. Here the facts come
// from internal/editops, which no edit to this package can change.
//
// Every fixture is laid down in a temp directory, so nothing here reads or
// writes a document anyone else on the machine owns.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/editops"
)

// editPlacementParityCase is one edit in two halves that must agree: the shared
// computation whose result IS the expectation, and the command that renders it.
//
// createdLevels and writes are controls ON THE ROW rather than the assertion.
// The facts asserted come from the shared result; these only keep a row from
// silently ceasing to cover the case it was written for — a fixture edited so
// that the two-level row created nothing would otherwise still pass, over an
// empty expectation.
type editPlacementParityCase struct {
	name          string
	edit          string
	node          []string
	socket        string
	entry         string
	value         string
	createdLevels int
	writes        bool
	// repeat applies the row's own edit once before either half runs it, so the
	// row covers the idempotent outcome: the entry already there, carrying what
	// was asked, which writes nothing and still has a placement to report.
	//
	// It is seeded by applying the edit rather than by adding an entry to the
	// fixture, because an entry an author hand-wrote carries more than an edit
	// writes — a component with a description and values is not "already
	// carrying what was asked", and such a row is refused rather than being a
	// no-op.
	repeat bool
}

// editPlacementParityCases is the grid, and its axis is how many container
// levels the edit has to create: two, one, and none. The none row is the
// control that stops the created-containers sentence from being printed
// unconditionally — without it a surface that named created containers on every
// edit would satisfy every other row.
var editPlacementParityCases = []editPlacementParityCase{
	{
		name:          "wiring inside a socket under a node carrying no connections at all",
		edit:          "wire-edge",
		node:          []string{"spec", "components", "db"},
		socket:        "storage",
		entry:         "volume",
		value:         "web",
		createdLevels: 2,
		writes:        true,
	},
	{
		name:          "attaching a Feature to a node carrying no attachments",
		edit:          "attach-feature",
		node:          []string{"spec", "components", "db"},
		entry:         "tls",
		value:         "core/tls:1.0.0",
		createdLevels: 1,
		writes:        true,
	},
	{
		name:          "adding a component to a container the document already carries",
		edit:          "add-component",
		node:          []string{"spec", "components"},
		entry:         "checkout",
		value:         "core/api-server:1.0.0",
		createdLevels: 0,
		writes:        true,
	},
	{
		name:          "an entry already there, which writes nothing",
		edit:          "add-component",
		node:          []string{"spec", "components"},
		entry:         "checkout",
		value:         "core/api-server:1.0.0",
		createdLevels: 0,
		writes:        false,
		repeat:        true,
	},
}

// editPlacementDocument names the document every row is applied to.
const editPlacementDocument = "storefront.aggregate.yaml"

// options is the row's request, spelled once so the two halves ask for the same
// edit rather than each spelling its own.
func (tc editPlacementParityCase) options(rel string) editops.Options {
	return editops.Options{
		File:   rel,
		Kind:   editops.Kind(tc.edit),
		Node:   tc.node,
		Socket: tc.socket,
		Name:   tc.entry,
		Value:  tc.value,
	}
}

// workspace lays the fixture down in a workspace of its own, seeding the row's
// edit first where the row covers the idempotent outcome.
func (tc editPlacementParityCase) workspace(t *testing.T) *workspaceForEdit {
	t.Helper()
	ws, _ := editDocumentWorkspace(t, editPlacementDocument, authoredAggregateForEdit)
	if tc.repeat {
		if _, err := editops.Apply(ws.root, tc.options(ws.rel)); err != nil {
			t.Fatalf("seeding the entry this row repeats failed, so the row cannot cover its case: %v", err)
		}
	}
	return ws
}

// shared runs the shared operation over its own workspace and returns the facts
// it computed. Its result is the whole expectation.
func (tc editPlacementParityCase) shared(t *testing.T) editops.Result {
	t.Helper()
	ws := tc.workspace(t)
	res, err := editops.Apply(ws.root, tc.options(ws.rel))
	if err != nil {
		t.Fatalf("the shared edit refused, so it produced no expectation to compare against: %v", err)
	}
	return res
}

// render runs the command over an identically seeded workspace and returns what
// it printed.
func (tc editPlacementParityCase) render(t *testing.T) string {
	t.Helper()
	ws := tc.workspace(t)
	var out bytes.Buffer
	if err := editDocumentRun(editDocumentConfig{
		Edit:       tc.edit,
		File:       ws.rel,
		Node:       tc.node,
		Socket:     tc.socket,
		Name:       tc.entry,
		Value:      tc.value,
		ValueGiven: true,
		ws:         resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws.root}),
		Out:        &out,
	}); err != nil {
		t.Fatalf("the command refused the edit: %v", err)
	}
	return out.String()
}

// expectedPlacementPath is the spelling a structural path is expected to travel
// under.
//
// The SEGMENTS come from the shared layer, which is the fact under test; the dot
// between them is spelled here rather than read from the production helper, and
// that near-duplication is the mechanism rather than an oversight. A reading
// that called the helper would compare the surface against itself: a helper
// changed to render nothing would satisfy such a comparison on every value,
// which is exactly the blindness this parity exists to remove. Spelled
// independently, a path that stops being printed fails here, and one
// deliberately respelled fails here too and is answered by a person.
func expectedPlacementPath(segments []string) string {
	return strings.Join(segments, ".")
}

// placementComplaints reads printed output against the placement facts the
// shared operation computed and returns one complaint per fact the output does
// not carry.
//
// It returns complaints rather than failing, so the same reading can be run
// over deliberately damaged output. That is what makes its failure mode
// demonstrated rather than assumed: an assertion nobody has watched reject
// anything is a comment with a test's name on it.
//
// 🔴 The created path is a SUFFIX of the container path, so a containment check
// for it is satisfied by a line naming the container alone — the naive reading
// here is blind to a surface that dropped the created-containers fact entirely.
// So the created fact is required to appear somewhere BEYOND every mention of
// the container: the occurrences a container mention can account for are
// subtracted, and what must be left over is at least one. That arithmetic is
// what makes the check independent of how the two facts are laid out, which a
// line-shape assertion would not be.
func placementComplaints(printed string, res editops.Result) []string {
	var complaints []string

	container := expectedPlacementPath(res.Container)
	inContainer := strings.Count(printed, container)
	if len(res.Container) != 0 && inContainer == 0 {
		complaints = append(complaints, "nothing names the container "+container+
			", which the shared operation computed as where the entry landed and this surface dropped it")
	}

	if len(res.CreatedContainers) != 0 {
		created := expectedPlacementPath(res.CreatedContainers)
		attributable := strings.Count(container, created) * inContainer
		if strings.Count(printed, created) <= attributable {
			complaints = append(complaints, "nothing names "+created+
				" beyond the container path it sits inside, and the shared operation computed those as the "+
				"container levels this edit created, so this surface dropped it")
		}
	}
	return complaints
}

// TestTheCommandLineCarriesEveryPlacementFactTheSharedEditComputed is the
// parity proof for the structured edit.
//
// Two workspaces are built per row from one helper, so the two halves see the
// same document without either half having seen the other's. The shared half
// runs first and its result is the whole expectation; the command then edits the
// other workspace and its output is read back against that expectation.
func TestTheCommandLineCarriesEveryPlacementFactTheSharedEditComputed(t *testing.T) {
	for _, tc := range editPlacementParityCases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.shared(t)

			// Row controls. Without them a fixture that stopped exercising the
			// case would satisfy every comparison below over an empty
			// expectation.
			if len(res.Container) == 0 {
				t.Fatal("the shared edit computed no container, so this row asserts over nothing")
			}
			if got := len(res.CreatedContainers); got != tc.createdLevels {
				t.Fatalf("the shared edit created %d container level(s) and this row was written for %d, so it no longer covers its case",
					got, tc.createdLevels)
			}
			if wrote := len(res.Written) != 0; wrote != tc.writes {
				t.Fatalf("the shared edit wrote=%v and this row was written for wrote=%v, so it no longer covers its case",
					wrote, tc.writes)
			}

			printed := tc.render(t)
			t.Logf("%s printed:\n%s", tc.name, printed)

			for _, complaint := range placementComplaints(printed, res) {
				t.Errorf("%s: %s", tc.name, complaint)
			}
		})
	}
}

// TestThePlacementReadingRejectsOutputWithAPlacementFactRemoved is the negative
// control for the reading above, and it is the whole reason that reading is
// worth running.
//
// Each row takes the command's real output, removes one of the two facts, and
// requires the reading to complain about exactly that one. A reading that stayed
// silent here would be silent over a surface that had genuinely stopped
// reporting the fact, and a green from it would mean nothing.
//
// The surgery is keyed on the FACTS rather than on the sentences carrying them —
// lines are dropped by the path they name — so the control does not have to be
// re-taught every time the wording moves.
func TestThePlacementReadingRejectsOutputWithAPlacementFactRemoved(t *testing.T) {
	for _, tc := range editPlacementParityCases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.shared(t)
			printed := tc.render(t)
			if got := placementComplaints(printed, res); len(got) != 0 {
				t.Fatalf("the undamaged output already draws complaints, so nothing below is attributable: %v", got)
			}

			container := expectedPlacementPath(res.Container)
			withoutContainer := dropLinesNaming(printed, container)
			if got := placementComplaints(withoutContainer, res); len(got) != 1 {
				t.Errorf("with the container path removed the reading raised %d complaint(s), want 1: %v", len(got), got)
			} else if !strings.Contains(got[0], "dropped it") || !strings.Contains(got[0], container) {
				t.Errorf("the complaint does not name the container it lost: %q", got[0])
			}

			if len(res.CreatedContainers) == 0 {
				return
			}
			created := expectedPlacementPath(res.CreatedContainers)
			withoutCreated := dropLinesNaming(printed, created, container)
			if got := placementComplaints(withoutCreated, res); len(got) != 1 {
				t.Errorf("with the created containers removed the reading raised %d complaint(s), want 1: %v", len(got), got)
			} else if !strings.Contains(got[0], "dropped it") || !strings.Contains(got[0], created) {
				t.Errorf("the complaint does not name the created containers it lost: %q", got[0])
			}
		})
	}
}

// dropLinesNaming returns printed without the lines that name want, keeping any
// line that also names one of the keep strings.
//
// The keep list is what lets one fact be removed while the other stays: the
// created path sits inside the container path, so removing every line naming it
// would take the container line with it and the reading would then complain
// about both.
func dropLinesNaming(printed, want string, keep ...string) string {
	var kept []string
	for _, line := range strings.Split(printed, "\n") {
		if strings.Contains(line, want) && !containsAny(line, keep) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// containsAny reports whether line carries any of the needles.
func containsAny(line string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}
