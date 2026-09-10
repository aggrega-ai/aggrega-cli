// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package opsurface

import (
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/tools/projectionparity"
)

// deliveryVocabularyDir is the package declaring the delivery vocabulary,
// relative to the module root.
//
// It is spelled rather than derived, and a wrong value cannot pass quietly: the
// read refuses a directory that declares no mapping from a caller's yes-or-no to
// a delivery, so a stale spelling fails naming its own cause rather than
// deriving an empty capability census and reporting it clean.
const deliveryVocabularyDir = "internal/authoredcontent"

// deliveryRead assembles the surface corpus over the shipped tree, runs the
// delivery read over it, and returns what it found.
//
// The corpus comes from projectionCorpus, which is the same corpus the
// projection gate reads and is already held from both ends there — a surface
// constant the mapping does not name fails, and a row naming a directory that
// holds no source fails too. Restating the mapping here would be a second copy
// of it, and the copy is what drifts the day a surface is restructured. The two
// gates read one axis each of the same pair, so they read one corpus.
//
// Every condition under which a clean result would certify nothing fails inside
// the read: a capability census that found no request carrying a delivery, and a
// corpus in which no surface composes any such request. Both are refusals rather
// than empty results, because either state reports no finding and would read as
// a green.
//
// It asserts nothing about the findings. Which of them fail is the caller's
// decision, which is why this returns the result instead of judging it.
func deliveryRead(t *testing.T) projectionparity.DeliveryResult {
	t.Helper()

	root, modulePath, surfaces := projectionCorpus(t)

	result, err := projectionparity.AnalyzeDelivery(
		root, modulePath, projectionCoreScope, deliveryVocabularyDir, surfaces)
	if err != nil {
		t.Fatalf("run the delivery read over the shipped surfaces: %v", err)
	}

	capabilities := make([]string, 0, len(result.Capabilities))
	for _, c := range result.Capabilities {
		capabilities = append(capabilities, c.Qualified()+"."+c.Selector)
	}
	t.Logf("delivery read over %d surface(s): %d capability request(s) carrying a delivery — %s",
		len(surfaces), len(result.Capabilities), strings.Join(capabilities, ", "))
	t.Logf("derived from %d chokepoint site(s):\n%s",
		len(result.Chokepoints), strings.Join(result.Chokepoints, "\n"))
	if len(result.Delegated) > 0 {
		// Reported, not passed over in silence. No verdict is drawn about these in
		// either direction, so a green above must not be read as covering them —
		// and the capability each one carries FOR is in the census, one hop out.
		t.Logf("%d request(s) carrying a delivery are composed by another capability's package, so they are "+
			"carriers rather than requests a caller composes and nothing is asserted about them:\n%s",
			len(result.Delegated), strings.Join(result.Delegated, "\n"))
	}

	// The offered set per surface, reported rather than only counted. A verdict
	// that said which surfaces were complete without saying which capabilities it
	// read them against would be unfalsifiable by anyone who came later.
	for _, c := range result.Capabilities {
		offered := result.OfferedBy(c.Qualified())
		composed := map[string]bool{}
		for _, o := range result.Offers {
			if o.Capability == c.Qualified() {
				composed[o.Surface] = true
			}
		}
		names := make([]string, 0, len(composed))
		for name := range composed {
			names = append(names, name)
		}
		sort.Strings(names)
		t.Logf("%s.%s: composed by %s; the choice is offered by %s",
			c.Qualified(), c.Selector,
			strings.Join(names, " and "),
			deliveryOfferedPhrase(offered))
	}

	// A surface composing none of the census is the blind spot this gate has by
	// construction, so it is named rather than left to be inferred from the
	// absence of its name above. It is not a tolerated gap: whether a surface
	// carries a capability at all is a membership question, and the availability
	// check answers it from the declaration each surface makes. What this line
	// prevents is a reader taking this gate's green as covering a surface it drew
	// no verdict about in either direction.
	composing := map[string]bool{}
	for _, o := range result.Offers {
		composing[o.Surface] = true
	}
	var silent []string
	for _, surface := range surfaces {
		if !composing[surface.Name] {
			silent = append(silent, surface.Name)
		}
	}
	sort.Strings(silent)
	if len(silent) > 0 {
		t.Logf("this gate draws no delivery verdict, in either direction, about %d of the %d surface(s) — %s — "+
			"which compose none of the %d capability request(s) in this census. Whether a surface carries "+
			"these capabilities at all is the availability check's question, answered from that surface's "+
			"own declaration",
			len(silent), len(surfaces), strings.Join(silent, " and "), len(result.Capabilities))
	}

	return result
}

// deliveryOfferedPhrase renders an offered set for a log line, naming an empty
// one rather than printing nothing — an empty phrase would read as a formatting
// slip where it is the whole finding.
func deliveryOfferedPhrase(offered []string) string {
	if len(offered) == 0 {
		return "NO surface"
	}
	return strings.Join(offered, " and ")
}

// TestTheDeliveryReadRunsOverTheShippedSurfaces is the LIVE half of this gate:
// it runs on every ordinary battery run and it blocks.
//
// What its green covers is the machinery and not the verdict — the surface
// corpus held from both ends, the delivery vocabulary read from its declaration,
// the capability census derived from the chokepoint, and the offered set read per
// surface. What it deliberately does NOT cover is whether every surface offers
// every delivery: that is the assertion in the test below.
//
// It also prints the capability census, the chokepoint sites it was derived from,
// and the offered set per capability. That is the only channel any of those have:
// a passing Go package prints nothing at all, so a figure that is reported and
// not asserted reaches a reader only under a verbose run.
//
// It exists as its own test because the two fail for reasons that need different
// repairs, and one test cannot tell a reader which it was. A red here means this
// gate's own wiring is broken — a stale vocabulary directory, a corpus that
// reached no surface, a census that found no capability — and the repair is in
// this file or in the corpus. A red below means a surface decided a caller's
// delivery for it, and the repair is in that surface.
func TestTheDeliveryReadRunsOverTheShippedSurfaces(t *testing.T) {
	t.Parallel()

	deliveryRead(t)
}

// TestEverySurfaceOffersEveryDeliveryItsCapabilitySupports fails on every
// surface that composes a capability's request without the delivery selector.
//
// It answers a question neither the availability check nor the projection read
// reaches. The availability check walks membership — whether each surface carries
// each capability, and under what name. The projection read walks the result
// side — whether each surface carries the facts a shared computation handed it.
// This walks the REQUEST side: given that a capability can either write its
// product or hand back the content the write would have produced, does every
// surface that composes that request let the caller choose.
//
// 🔴 There is no exemption here and none may be added. A delivery is a request a
// caller makes, not a capability a surface provides, so a surface has no standing
// to decline one: an incomplete surface fails. The marker grammar the projection
// read accepts is deliberately NOT reused, and inventing a second one would be
// the same mistake in new words. A stated decline would make this gate green on
// exactly the state the rule forbids, and the rule's reasoning is specific: a
// transport constraint is a fact about a payload, so it belongs in the answer the
// capability gives rather than in a per-surface list of what may be asked.
//
// It blocks rather than reports, and the reason is mechanical rather than a
// matter of strictness: a Go test that gathers findings and prints them prints
// nothing at all on a passing package, so a reporting form of this gate would be
// a mechanism nothing ever reads. There is no reporting tier available here that
// is not inert.
//
// What it cannot see, stated so a green is not read as more than it is. The
// reading is syntactic and per-composition: a surface offers the choice at a site
// when the composition there names the selector, whatever the value then turns
// out to be — so a surface that names the selector and hands it a constant reads
// as offering the choice. That keeps the read sound in the one direction it
// claims, since it can only ever be quieter than the truth and never accuse a
// surface that lets the caller choose.
//
// And a surface that composes the request nowhere at all is invisible here by
// construction. That is not this gate's subject in either direction: whether a
// surface carries a capability at all is a membership question, which the
// availability check answers from the declaration each surface makes, and a
// capability absent from a surface has no composition for a delivery to be
// missing from.
func TestEverySurfaceOffersEveryDeliveryItsCapabilitySupports(t *testing.T) {
	t.Parallel()

	result := deliveryRead(t)

	if len(result.Findings) == 0 {
		t.Logf("every surface composing one of the %d capability request(s) carrying a delivery offers "+
			"the choice, across %d composition(s)", len(result.Capabilities), len(result.Offers))
		return
	}

	t.Errorf("%d surface composition(s) decide a caller's delivery for it:\n%s",
		len(result.Findings), projectionparity.FormatDeliveryFindings(result.Findings))
}
