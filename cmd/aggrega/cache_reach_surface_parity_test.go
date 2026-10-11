// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// Reach parity for the three reclamation operations, asserted against the
// shared layer's own result rather than against anything this surface produced.
//
// The expectation is taken by running the shared operation over one cache and
// the command over a second, identically seeded one. That is what keeps the two
// halves independent: a comparison drawn from the command's own output would
// move with the command, so a surface that stopped reporting a reach would keep
// passing while the assertion followed it down. Here the facts come from
// internal/cacheops, which no edit to this package can change.
//
// The fixtures address only temp directories — a temp workspace and temp
// compiled-module, plugin-describe and documentation-bundle roots — so nothing
// here reads or removes a cache anyone else on the machine depends on.

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
)

// reachedThing is one thing an operation reported acting on, reduced to the two
// facts this parity is about: what it was, and how far its class is shared.
//
// label is empty for a whole-subtree wipe, which names a class and no element.
type reachedThing struct {
	class string
	label string
	reach cacheops.Reach
}

// reachParityCase is one reclamation operation in two halves that must agree:
// the shared computation whose result IS the expectation, and the command that
// renders it.
type reachParityCase struct {
	name string
	// shared runs the shared operation and returns the facts it computed.
	shared func(t *testing.T, loc cacheops.Locations) (machineWide []string, reached []reachedThing)
	// render runs the command over an identically seeded cache.
	render func(t *testing.T, w io.Writer, loc cacheops.Locations)
}

// evictParityCap is a cap below anything the fixture's compiled-module cache
// holds, so the trim removes rather than reporting a no-op. A no-op would leave
// the per-element half of this parity holding over an empty set.
const evictParityCap = int64(1)

// reachParityCases is the grid: one row per reclamation operation, each row
// covering every element that operation reported acting on.
var reachParityCases = []reachParityCase{
	{
		name: "prune",
		shared: func(t *testing.T, loc cacheops.Locations) ([]string, []reachedThing) {
			t.Helper()
			res, err := cacheops.Prune(t.Context(), loc)
			if err != nil {
				t.Fatalf("the shared prune refused, so it produced no expectation to compare against: %v", err)
			}
			reached := make([]reachedThing, 0, len(res.Removed))
			for _, r := range res.Removed {
				reached = append(reached, reachedThing{class: r.Class, label: r.Label, reach: r.Reach})
			}
			return res.MachineWide, reached
		},
		render: func(t *testing.T, w io.Writer, loc cacheops.Locations) {
			t.Helper()
			if err := runCachePrune(t.Context(), w, loc); err != nil {
				t.Fatalf("the command refused the prune: %v", err)
			}
		},
	},
	{
		name: "evict",
		shared: func(t *testing.T, loc cacheops.Locations) ([]string, []reachedThing) {
			t.Helper()
			res, err := cacheops.Evict(t.Context(), loc, evictParityCap)
			if err != nil {
				t.Fatalf("the shared eviction refused, so it produced no expectation to compare against: %v", err)
			}
			if res.WithinCapBefore {
				t.Fatalf("a cap of %d byte(s) reported the cache already within it, so this row asserts over no removal",
					evictParityCap)
			}
			reached := make([]reachedThing, 0, len(res.Removed))
			for _, r := range res.Removed {
				reached = append(reached, reachedThing{class: r.Class, label: r.Label, reach: r.Reach})
			}
			return res.MachineWide, reached
		},
		render: func(t *testing.T, w io.Writer, loc cacheops.Locations) {
			t.Helper()
			if err := runCacheEvict(t.Context(), w, loc, evictParityCap); err != nil {
				t.Fatalf("the command refused the eviction: %v", err)
			}
		},
	},
	{
		name: "clear",
		shared: func(t *testing.T, loc cacheops.Locations) ([]string, []reachedThing) {
			t.Helper()
			res, err := cacheops.Clear(t.Context(), loc)
			if err != nil {
				t.Fatalf("the shared wipe refused, so it produced no expectation to compare against: %v", err)
			}
			reached := make([]reachedThing, 0, len(res.Cleared))
			for _, c := range res.Cleared {
				reached = append(reached, reachedThing{class: c.Class, reach: c.Reach})
			}
			return res.MachineWide, reached
		},
		render: func(t *testing.T, w io.Writer, loc cacheops.Locations) {
			t.Helper()
			if err := runCacheClear(t.Context(), w, loc); err != nil {
				t.Fatalf("the command refused the wipe: %v", err)
			}
		},
	},
}

// expectedReachMarker is the marker a reach is expected to travel under.
//
// The reach WORD comes from the shared layer, which is the fact under test; the
// two brackets around it are spelled here rather than read from the production
// helper, and that near-duplication is the mechanism rather than an oversight. A
// reading that called the helper would compare the surface against itself: a
// helper changed to render nothing would satisfy such a comparison on every
// value, which is exactly the blindness this parity exists to remove. Spelled
// independently, a marker that stops being printed fails here, and a marker
// deliberately respelled fails here too and is answered by a person.
func expectedReachMarker(r cacheops.Reach) string {
	return "[" + string(r) + "]"
}

// reachComplaints reads printed output against the facts the shared layer
// computed and returns one complaint per fact the output does not carry.
//
// It returns complaints rather than failing, so the same reading can be run over
// deliberately damaged output. That is what makes its failure mode demonstrated
// rather than assumed: an assertion nobody has watched reject anything is a
// comment with a test's name on it.
func reachComplaints(printed string, machineWide []string, reached []reachedThing) []string {
	var complaints []string
	lines := strings.Split(printed, "\n")

	for _, thing := range reached {
		subject := thing.label
		if subject == "" {
			subject = thing.class
		}
		marker := expectedReachMarker(thing.reach)
		var carried bool
		for _, line := range lines {
			if strings.Contains(line, subject) && strings.Contains(line, marker) {
				carried = true
				break
			}
		}
		if !carried {
			complaints = append(complaints, "no line carries "+subject+" together with its reach "+marker+
				"; the shared operation computed that reach and this surface dropped it")
		}
	}

	for _, class := range machineWide {
		var named bool
		for _, line := range lines {
			if strings.HasPrefix(line, "machine-wide: ") && strings.Contains(line, class) {
				named = true
				break
			}
		}
		if !named {
			complaints = append(complaints, "no machine-wide statement names "+class+
				", which the shared operation computed as shared across every project on the machine")
		}
	}
	return complaints
}

// TestTheCommandLineCarriesEveryReachTheSharedOperationComputed is the parity
// proof for the three reclamation verbs.
//
// Two fixtures are built per row from one helper, so the two halves see the same
// cache state without either half having seen the other's. The shared half runs
// first and its result is the whole expectation; the command then renders the
// other fixture and its output is read back against that expectation.
func TestTheCommandLineCarriesEveryReachTheSharedOperationComputed(t *testing.T) {
	for _, tc := range reachParityCases {
		t.Run(tc.name, func(t *testing.T) {
			machineWide, reached := tc.shared(t, newCacheFixture(t).loc)

			// Controls. Without them a surface reporting nothing at all would
			// satisfy every comparison below over an empty expectation.
			if len(reached) == 0 {
				t.Fatalf("the shared %s reported acting on nothing, so this row asserts over an empty set", tc.name)
			}
			if len(machineWide) == 0 {
				t.Fatalf("the shared %s computed no machine-wide class, so the reach statement is asserted over nothing", tc.name)
			}

			var out bytes.Buffer
			tc.render(t, &out, newCacheFixture(t).loc)
			t.Logf("%s printed:\n%s", tc.name, out.String())

			for _, complaint := range reachComplaints(out.String(), machineWide, reached) {
				t.Errorf("%s: %s", tc.name, complaint)
			}
		})
	}
}

// TestTheReachReadingRejectsOutputWithTheReachRemoved is the negative control
// for the reading above, and it is the whole reason that reading is worth
// running.
//
// Each row takes the command's real output, removes one half of the answer, and
// requires the reading to complain about exactly that half. A reading that
// stayed silent here would be silent over a surface that had genuinely stopped
// reporting the reach, and a green from it would mean nothing.
func TestTheReachReadingRejectsOutputWithTheReachRemoved(t *testing.T) {
	for _, tc := range reachParityCases {
		t.Run(tc.name, func(t *testing.T) {
			machineWide, reached := tc.shared(t, newCacheFixture(t).loc)
			var out bytes.Buffer
			tc.render(t, &out, newCacheFixture(t).loc)
			printed := out.String()

			if got := reachComplaints(printed, machineWide, reached); len(got) != 0 {
				t.Fatalf("the undamaged output already draws complaints, so nothing below is attributable: %v", got)
			}

			// Every per-element reach marker removed, the machine-wide statement
			// left in place: the reading must object once per element.
			stripped := printed
			for _, r := range []cacheops.Reach{cacheops.ReachWorkspace, cacheops.ReachMachine} {
				stripped = strings.ReplaceAll(stripped, expectedReachMarker(r), "")
			}
			perElement := reachComplaints(stripped, machineWide, reached)
			if len(perElement) != len(reached) {
				t.Errorf("with every reach marker removed the reading raised %d complaint(s) for %d element(s): %v",
					len(perElement), len(reached), perElement)
			}
			for _, complaint := range perElement {
				if !strings.Contains(complaint, "dropped it") {
					t.Errorf("a complaint about a removed marker does not say what was dropped: %q", complaint)
				}
			}

			// The machine-wide statement removed, the markers left in place: the
			// reading must object once per machine-wide class.
			var kept []string
			for _, line := range strings.Split(printed, "\n") {
				if strings.HasPrefix(line, "machine-wide: ") {
					continue
				}
				kept = append(kept, line)
			}
			perClass := reachComplaints(strings.Join(kept, "\n"), machineWide, reached)
			if len(perClass) != len(machineWide) {
				t.Errorf("with the machine-wide statement removed the reading raised %d complaint(s) for %d class(es): %v",
					len(perClass), len(machineWide), perClass)
			}
		})
	}
}
