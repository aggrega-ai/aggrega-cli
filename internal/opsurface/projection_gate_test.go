// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package opsurface

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/tools/projectionparity"
)

// projectionDir is this package's own directory, relative to the module root, so
// the surface constants can be read from the declaration rather than transcribed.
//
// It is spelled rather than derived, and a wrong value cannot pass quietly: the
// constant read refuses a directory declaring no surface constant, so the gate
// fails naming its own cause rather than deriving an empty corpus.
const projectionDir = "internal/opsurface"

// projectionCoreScope bounds which packages count as shared computation cores.
//
// It is the module's unimportable tree, and the bound is a real distinction
// rather than a naming convention: a package outside it is published vocabulary,
// whose types are shared with consumers this read knows nothing about. Two
// surfaces reading different parts of a loaded lockfile are reading one
// published structure for two purposes, which is not one surface dropping an
// answer the other was handed.
const projectionCoreScope = "internal/"

// surfaceProjectionDirs maps each surface to the package directories its
// projections live in, relative to the module root.
//
// It is a mapping rather than a derivation because nothing in the tree links a
// surface to the packages that project its results: the command line declares
// its availability in one package and renders every result in another, and no
// declaration connects the two. So the corpus is stated here — and, being
// stated, it is held from both ends by the test below. A surface constant this
// map does not name fails the gate, and so does a row naming a directory that
// holds no source. Neither direction can rot into a green.
var surfaceProjectionDirs = map[Surface][]string{
	SurfaceCLI:   {"internal/clisurface", "cmd/aggrega"},
	SurfaceServe: {"internal/serve"},
	SurfaceMCP:   {"internal/mcp"},
}

// projectionReadDir is the read's own package directory, relative to the module
// root, so the kinds it can emit are read from their declaration rather than
// transcribed beside them.
const projectionReadDir = "internal/tools/projectionparity"

// projectionKindBlocks says, for each kind the read can emit, whether a finding
// of that kind fails the gate below.
//
// The line between the two is accusation against declination. A kind that
// accuses the tree blocks: it reports something a surface did — a field dropped
// that another surface carries, a whole result thrown away, a stated exemption
// the site does not bear out. Each is a fact about the code and each has a
// repair in the code. A kind that declines to certify is excluded: it reports
// how far this read reached — a projection it could not follow, a producer whose
// field set it could not enumerate. Neither says anything about whether a
// surface carries what it was handed, in either direction, so failing on one
// would fail the gate for this read's own limits rather than for a divergence.
//
// The distinction is readable in the sentences the read prints, which is what
// keeps it from being a hand-picked list. A blocking kind names what a surface
// dropped and what another carries. An excluded kind ends in "so no parity
// verdict is drawn for it here", or "so what its callers are given cannot be
// compared".
//
// A stated exemption that does not bear out is the one that reads like a reach
// claim and is not. The read saw everything it needed: the marker an author
// wrote, and the site it sits on, compared. Every way such a marker goes
// unsupported is authored text being wrong — it names a field the surface does
// read, or one no result at the site carries, or it states no reason, or it sits
// away from any projection of the result it names, or its name reaches more than
// one result, or its shape names no form at all. One of those emits no other
// finding: a marker naming a field the surface has since started carrying is
// stale, and with the field now carried there is no dropped field left to fail,
// so this kind is the only report of it. A marker that is refused must be a
// refusal something reads, for the same reason a marker that is prescribed must
// be one the read accepts.
//
// An unreadable core is excluded on that same principle rather than because it
// is currently silent. It is raised when a producer reaches no top-level
// declaration of that name and so is a function value whose results cannot be
// enumerated, when a package declares no such type, when a result struct embeds
// a type from another package whose promoted fields this read declines to
// approximate, or when a package could not be parsed. Every one of those ends in
// "the field set is unknown, so nothing may be concluded", which is the same
// declination an unreadable site makes one level down — that one could not
// follow a consumer's use, this one could not enumerate a producer's field set.
// Failing on it would let an ordinary authoring act — a shared result embedding
// a struct from another package — fail the battery for a limit this read
// documents as deliberate.
//
// 🔴 The coverage bound this scoping creates, stated here rather than left to be
// inferred from a green. This gate answers: of the projections this read could
// follow, does any surface drop a fact another surface carries. It does not
// answer whether the read could follow every projection. A surface whose use of
// a shared result escapes the reading — into a closure, a second variable, a
// call this reading does not resolve — is reported as an unreadable site and
// passed over, so a divergence hiding behind that escape is unchecked; and where
// a producer's field set is not enumerated, no surface's projection of that
// result is compared at all.
//
// Neither exclusion is a tolerated divergence, and the difference decides how a
// green should be read. A tolerated divergence would be a defect this read saw
// and the gate let through. These are projections it drew no verdict about in
// either direction, so there is no finding about the tree to have suppressed —
// but the unchecked ground is real, and it is measurable rather than notional:
// the live half prints every excluded finding with its site, so the size of what
// this gate cannot see is readable from the same run that reports the green.
//
// One bound is narrower than the exclusions and sits inside the kinds that DO
// block, so it is stated here rather than left implied by them. The reading is
// syntactic: a field counts as carried when the surface names it, whatever the
// enclosing expression then does with the value. So a green means every field
// is READ at its projection site, not that every fact arrives in the surface's
// output — reading a field and discarding it is indistinguishable here from
// reading it and answering with it. That is what keeps this read sound in the
// one direction it claims, since it can only ever be quieter than the truth and
// never accuse a surface that carries everything; and it is why a repair that
// clears a finding still owes an assertion derived independently of this read.
//
// What is bounded and what deliberately is not, since the two sit one edit
// apart and a reader has to be able to tell them apart. The number of findings
// this classification EXCLUDES is bounded, by the increase-only ratchet at
// [projectionExclusionCeiling]. That population is exactly the ground this gate
// declines to certify, so its growth is a loss of coverage and nothing else,
// and a loss of coverage that arrives as a green is the one failure this
// scoping would otherwise hide. What is NOT bounded is a floor under the
// census, a minimum number of projections the read must reach. Such a number
// would be a second copy of what the corpus derivation already computes, it
// would go stale in the direction of silence the first time a surface was
// legitimately restructured, and choosing it is a policy call rather than an
// engineering one. The census is asserted non-empty and logged in full instead.
//
// Those two are not one figure inverted, which is what makes one of them
// bindable and the other not: the corpus may legitimately grow or shrink, which
// moves the number of projections followed without moving the number this read
// could not follow.
var projectionKindBlocks = map[projectionparity.Kind]bool{
	projectionparity.DroppedField:         true,
	projectionparity.ResultDiscarded:      true,
	projectionparity.ExemptionUnsupported: true,
	projectionparity.UnreadableSite:       false,
	projectionparity.UnreadableCore:       false,
}

// projectionFindingBlocks says whether a finding of this kind fails the gate.
//
// A kind the classification does not name blocks. The map lookup's zero value is
// "does not block", so a kind added to the read later would otherwise default to
// excluded — silently, and in the one direction nothing notices, because an
// excluded finding leaves the gate green. Turning the miss into a refusal makes
// the default fail closed, and the completeness assertion below turns what would
// be a puzzling red into a named one.
func projectionFindingBlocks(kind projectionparity.Kind) bool {
	blocks, classified := projectionKindBlocks[kind]
	return !classified || blocks
}

// projectionExclusionCeiling bounds the findings the gate below passes over:
// the population may fall and may not rise.
//
// Those findings report how far this read reached rather than anything about
// the tree, so failing on the population itself would redden the shipped tree
// for this read's own limits — which is why the classification above excludes
// them at all. What IS a defect is the population growing. A refactor that puts
// ten more projections beyond this read's reach shrinks what the gate checks by
// ten, and every one of those losses arrives as a green. Refusing an increase
// costs nothing while the number holds or falls, and it makes the one movement
// that matters loud instead of silent.
//
// Nothing weaker will do that job, and the medium is the reason rather than a
// preference: a passing Go package relays no output whatever — not a test log
// line, not standard output, not standard error — so on a pass the count
// reaches a reader only under a verbose run. A rise that is reported and not
// refused is a rise nobody learns about. This constant is therefore also the
// pass-visible statement of the count: it is readable here without running
// anything, the assertion below is what keeps it from drifting above the truth,
// and because a rise cannot be cleared without editing it, the movement lands
// in a diff a reviewer reads rather than in a log a pass discards.
//
// Criterion, so that a later reading measures the same thing: the number of
// findings the read reports over the shipped surfaces for which
// [projectionFindingBlocks] answers false — the total across those kinds. NOT
// the number of KINDS passed over, which the classification assertions below
// hold from both ends. And NOT the total finding count, which mixes two
// populations whose movements mean opposite things: a bound on that sum can be
// held while an exclusion replaces a divergence, so it would constrain neither.
// Measured 2026-09-19 at aggrega-cli eb31c58a: 6 findings, all of one kind
// (unreadable-site), which is the whole of the 6 the read reports, since no
// finding currently accuses a surface. That coincidence is why the criterion
// spells out which of the two is meant.
//
// The fall to this figure came from the read's reach growing rather than from
// any surface changing, which is the one way this constant may be lowered. The
// read follows a value handed to a function in another package of this module
// into that function's body rather than stopping at the call, so two surfaces
// handing one shared explanation to the writer that renders it — each one import
// away from the body that reads the fields — are followed to a full field set.
// Two findings went and the census of projections followed to a verdict rose by
// exactly two, which is the arithmetic that says the coverage was gained by
// reading bodies rather than by excusing sites.
//
// The bound this leaves, recorded so the ceiling is never read as the
// population: a FALL is deliberately not refused, so once the count drops this
// constant carries slack the ratchet does not reclaim, and a reach regression
// inside that slack is invisible. Reclaiming it is a one-line edit here,
// belonging to whatever change lowers the count rather than to a later
// tidy-up.
const projectionExclusionCeiling = 6

// assertProjectionExclusionCeiling is the increase-only ratchet over the
// findings the gate below passes over.
//
// It is separate from the tally that gate logs because the two answer different
// questions. That line says which kinds were passed over and how many of each,
// which is a figure a reader wants and no build should halt on. This says
// whether the population GREW, which is the one movement that is a defect
// rather than a status: the read's reach shrank, and the ground the gate no
// longer covers grew by the difference.
//
// A fall is not refused, deliberately, and the slack that leaves is recorded at
// [projectionExclusionCeiling] rather than here, so the bound sits with the
// number it bounds.
func assertProjectionExclusionCeiling(t *testing.T, excluded, reported int) {
	t.Helper()

	switch {
	case excluded > projectionExclusionCeiling:
		t.Errorf("the gate passes over %d finding(s) reporting this read's own reach, above the "+
			"recorded ceiling of %d.\n"+
			"INVARIANT: this population measures the ground the gate declines to certify, so it may "+
			"fall and may not rise.\n"+
			"CONSEQUENCE: %d more projection(s) sit beyond this read's reach than when the ceiling was "+
			"recorded, so the gate checks less than it did, and a loss of coverage arrives as a green. "+
			"The findings themselves, with their sites, are printed by the live half of this gate — run "+
			"this package verbosely to read them, since a passing package relays nothing.\n"+
			"REMEDY: make the named projections readable — bind the shared result to a variable and "+
			"read its fields in the same body, rather than handing the value whole to a call this read "+
			"cannot follow. Where a legitimately new surface brought them instead, raise the ceiling "+
			"here deliberately and say why in review. What must not happen is the raise being made to "+
			"clear a red without either: the number is only meaningful as a bound the reach has never "+
			"fallen below.",
			excluded, projectionExclusionCeiling, excluded-projectionExclusionCeiling)
	case excluded < projectionExclusionCeiling:
		t.Logf("the gate passes over %d of the %d finding(s) reported, below the recorded ceiling of "+
			"%d. The ratchet refuses an increase and does not refuse a fall, so this slack of %d is not "+
			"reclaimed until projectionExclusionCeiling is lowered — a one-line edit belonging to "+
			"whatever change lowered the count. Within that slack a reach regression is not detected.",
			excluded, reported, projectionExclusionCeiling, projectionExclusionCeiling-excluded)
	default:
		t.Logf("the gate passes over %d of the %d finding(s) reported, exactly at the recorded ceiling "+
			"of %d. The ratchet refuses an increase.", excluded, reported, projectionExclusionCeiling)
	}
}

// projectionCorpus reads the module root and assembles the surface corpus,
// asserting the mapping accounts for every declared surface.
func projectionCorpus(t *testing.T) (root string, modulePath string, surfaces []projectionparity.Surface) {
	t.Helper()

	root, modulePath, err := projectionparity.ModuleRoot(".")
	if err != nil {
		t.Fatalf("find the module root: %v", err)
	}

	declared, err := projectionparity.ConstantValues(root, projectionDir, "Surface")
	if err != nil {
		t.Fatalf("read the declared surfaces: %v", err)
	}

	mapped := map[string]bool{}
	for surface := range surfaceProjectionDirs {
		mapped[string(surface)] = true
	}
	var unmapped []string
	for _, name := range declared {
		if !mapped[name] {
			unmapped = append(unmapped, name)
		}
	}
	sort.Strings(unmapped)
	if len(unmapped) > 0 {
		t.Fatalf("the projection corpus names no directory for %s. A surface with no corpus is not "+
			"excused from parity — its projections simply go unread, which is the one failure this gate "+
			"must not have. Add the directories its results are rendered in.",
			strings.Join(unmapped, " and "))
	}
	if len(declared) != len(mapped) {
		t.Fatalf("the projection corpus names %d surface(s) and %s declares %d; a row naming a surface "+
			"that no longer exists is stale — delete it", len(mapped), projectionDir, len(declared))
	}

	names := make([]string, 0, len(surfaceProjectionDirs))
	for surface := range surfaceProjectionDirs {
		names = append(names, string(surface))
	}
	sort.Strings(names)
	for _, name := range names {
		surfaces = append(surfaces, projectionparity.Surface{
			Name: name,
			Dirs: surfaceProjectionDirs[Surface(name)],
		})
	}
	return root, modulePath, surfaces
}

// projectionRead assembles the surface corpus over the shipped tree, runs the
// projection read over it, and returns what it found.
//
// Every condition under which a clean result would certify nothing fails here: a
// corpus accounting for a surface the vocabulary does not declare or missing one
// it does, a read that could not run, and a corpus holding no shared computation
// at all. So this is the load-bearing half — it derives the corpus from the tree
// rather than from a list, and a list is what "answers nothing is wrong about
// code it never opened".
//
// It asserts nothing about the findings. Which of them fail is one caller's
// decision, taken by kind, which is why this returns the result instead of
// judging it.
func projectionRead(t *testing.T) projectionparity.Result {
	t.Helper()

	root, modulePath, surfaces := projectionCorpus(t)

	result, err := projectionparity.Analyze(root, modulePath, projectionCoreScope, surfaces)
	if err != nil {
		t.Fatalf("run the projection read over the shipped surfaces: %v", err)
	}

	// The corpus is asserted rather than assumed. The read over a tree in which
	// no computation is shared returns no finding and no error, so an empty
	// corpus is indistinguishable from a clean one from the outside: the census
	// is what makes the green mean something.
	if len(result.SharedComputations) == 0 || len(result.ResultTypes) == 0 {
		t.Fatalf("the corpus derivation found %d shared computation(s) and %d structured result type(s); "+
			"a clean result over an empty corpus certifies nothing",
			len(result.SharedComputations), len(result.ResultTypes))
	}
	t.Logf("projection read over %d surface(s): %d shared computation(s), %d structured result type(s), %d projection(s) followed to a verdict",
		len(surfaces), len(result.SharedComputations), len(result.ResultTypes), result.Projections)
	if len(result.UnreadResultTypes) > 0 {
		// Reported, not passed over. No parity claim is made about these in
		// either direction, so a green above must not be read as covering them.
		t.Logf("no surface projects these %d shared result(s) readably, so nothing is asserted about them: %s",
			len(result.UnreadResultTypes), strings.Join(result.UnreadResultTypes, ", "))
	}
	if len(result.Findings) > 0 {
		// Printed rather than asserted, and the distinction is this gate's whole
		// shape. The caller that judges these fails only on the kinds that
		// accuse the tree, so the kinds reporting the read's own reach would
		// otherwise have no channel at all — a passing package prints nothing.
		// Printing every finding here is what keeps the size of what that gate
		// passes over readable from the same run that reports its green.
		t.Logf("the read reports %d finding(s), which this half of the gate asserts nothing about:\n%s",
			len(result.Findings), projectionparity.Report(result.Findings))
	}
	return result
}

// TestTheProjectionReadRunsOverTheShippedSurfaces is the LIVE half of this gate:
// it runs on every ordinary battery run and it blocks.
//
// What its green covers is the machinery and not the verdict — the surface
// mapping held from both ends, the corpus derived from the module read, the read
// itself, and the census that keeps an empty corpus from reading as a clean one.
// What it deliberately does NOT cover is whether the surfaces agree: that is the
// assertion in the test below. Reading this test's pass as parity would be
// reading it as the opposite of what it says.
//
// It also prints every finding the read reports, including the ones the gate
// below passes over, which is the only channel those have: a passing Go package
// prints nothing.
//
// It exists as its own test because the two fail for reasons that need different
// repairs, and one test cannot tell a reader which it was. A red here means this
// gate's own wiring is broken — a surface with no corpus, a stale row, a
// derivation that reached nothing — and the repair is in this file. A red below
// means two surfaces disagree, and the repair is in a surface. Merging them
// would give one name to both, and which of the two it was is the first question
// asked after a red.
func TestTheProjectionReadRunsOverTheShippedSurfaces(t *testing.T) {
	t.Parallel()

	projectionRead(t)
}

// TestEverySurfaceCarriesWhatTheSharedComputationProduced fails on every finding
// the projection read makes about the shipped surfaces, and passes over every
// finding it makes about its own reach.
//
// It answers a question the availability check structurally cannot. That check
// walks membership — whether each surface carries each capability, and under
// what name — and says nothing about two surfaces behaving differently under one
// name. This reads the second axis: given that two surfaces call one shared
// computation, does each one carry the structured facts that computation handed
// it. A count, a verdict, a classification or a hint is owed on every surface
// that can carry structure; only human-formatted presentation may differ.
//
// It blocks rather than reports, and the reason is mechanical rather than a
// matter of strictness: a Go test that gathers findings and prints them prints
// nothing at all on a passing package, so a reporting form of this gate would be
// a mechanism nothing ever reads. There is no reporting tier available here that
// is not inert.
//
// Which kinds fail here is decided by the kind classification above, where that
// reasoning and the coverage bound it creates are written. In short: a dropped
// field, a discarded result and an exemption the site does not bear out each
// accuse the tree, and each fails. An unreadable site and an unreadable core
// each report how far this read reached, and a gate failing on those would be
// refusing the read's own limits rather than a divergence, so they are passed
// over here and printed in full by the live half above, where their sites stay
// visible.
//
// That two of the five kinds are excluded is not a tolerated divergence, and
// being exact about the difference matters, because a gate that excuses defects
// and a gate that declines to certify ground it never read both report a green.
// A tolerated divergence would be a defect this read saw and this gate let
// through. What is excluded here is ground on which the read drew no verdict in
// either direction, so there is no finding about the tree to have suppressed.
//
// The excluded ground is bounded rather than merely named, by
// [projectionExclusionCeiling]. Passing a finding over is a decision about what
// this gate certifies, and how much it passes over is a number that moves
// whenever the surfaces are restructured — upward when a projection stops being
// followable, which is a loss of coverage that would otherwise arrive as a
// green and reach nobody. So the count may fall and may not rise. That is the
// only reading of a green here that also covers the gate's own reach; the
// verdict itself still covers only the projections the read could follow.
//
// What it cannot see, stated so a green is not read as more than it is. A
// surface that answers a request with a second implementation of its own, taking
// different input and calling nothing shared, has no shared result to project
// and is invisible to this read by construction. And a field no surface carries
// is not reported, because a computation producing something nobody projects is
// a different defect with a different repair.
func TestEverySurfaceCarriesWhatTheSharedComputationProduced(t *testing.T) {
	t.Parallel()

	result := projectionRead(t)

	var blocking []projectionparity.Finding
	passedOver := map[projectionparity.Kind]int{}
	excluded := 0
	for _, finding := range result.Findings {
		if projectionFindingBlocks(finding.Kind) {
			blocking = append(blocking, finding)
			continue
		}
		passedOver[finding.Kind]++
		excluded++
	}

	// Bounded rather than only named. The tally below says how much ground this
	// gate passed over; the ratchet says whether that ground GREW, which is the
	// movement no other assertion here would notice, because it leaves the gate
	// green. It runs unconditionally, since a fall to zero is a reading the
	// ceiling must be able to record.
	assertProjectionExclusionCeiling(t, excluded, len(result.Findings))

	if len(passedOver) > 0 {
		// Named on the way past, so a pass reads as a pass OVER something rather
		// than as a read that found nothing. The findings themselves, with their
		// sites, are printed by the live half above.
		counted := make([]string, 0, len(passedOver))
		for kind, count := range passedOver {
			counted = append(counted, fmt.Sprintf("%d %s", count, kind))
		}
		sort.Strings(counted)
		t.Logf("passed over %s, which report this read's reach rather than the tree",
			strings.Join(counted, " and "))
	}

	if len(blocking) > 0 {
		t.Fatalf("the projection read reports %d finding(s) about the shipped surfaces:\n%s",
			len(blocking), projectionparity.Report(blocking))
	}
}

// TestTheGateClassifiesEveryKindTheReadCanEmit holds the classification above
// from both ends, so the scoping cannot rot into a green.
//
// The kinds are read from their own declarations rather than from a list beside
// them, which is what lets this fail in three distinguishable ways instead of
// passing on a stale classification: a kind the read declares that nothing
// classifies, a classification row naming a kind the read no longer emits, and a
// classification that has degenerated to one answer.
//
// The third is the one worth spelling out, because neither of the others would
// notice it. An all-blocking classification fails the battery for the read's own
// limits; an all-excluding one is a gate that cannot fail whatever it is handed.
// Both are a plausible result of one careless bulk edit.
func TestTheGateClassifiesEveryKindTheReadCanEmit(t *testing.T) {
	t.Parallel()

	root, _, err := projectionparity.ModuleRoot(".")
	if err != nil {
		t.Fatalf("find the module root: %v", err)
	}
	declared, err := projectionparity.ConstantValues(root, projectionReadDir, "Kind")
	if err != nil {
		t.Fatalf("read the kinds the projection read declares: %v", err)
	}

	classified := map[string]bool{}
	for kind := range projectionKindBlocks {
		classified[string(kind)] = true
	}

	var unclassified []string
	for _, name := range declared {
		if !classified[name] {
			unclassified = append(unclassified, name)
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Fatalf("the read can emit %s and the gate classifies it neither as blocking nor as passed "+
			"over. A kind nobody classified is one a gate would let through, which is the direction "+
			"nothing notices: decide whether it accuses the tree or reports this read's reach, and "+
			"say which where the classification is stated.", strings.Join(unclassified, " and "))
	}
	if len(declared) != len(classified) {
		t.Fatalf("the gate classifies %d kind(s) and the read declares %d; a row naming a kind the "+
			"read no longer emits is stale — delete it", len(classified), len(declared))
	}

	var blocking, passedOver int
	for _, blocks := range projectionKindBlocks {
		if blocks {
			blocking++
			continue
		}
		passedOver++
	}
	if blocking == 0 {
		t.Fatal("no kind blocks, so the gate above cannot fail whatever the read reports")
	}
	if passedOver == 0 {
		t.Fatal("every kind blocks, so the gate above fails for this read's own limits as readily " +
			"as for a divergence, and a projection it could not follow is not a defect in the tree")
	}
}

// TestAKindTheGateDoesNotClassifyBlocks pins the fail-closed default.
//
// It is the one property the classification cannot state about itself. The map
// lookup's zero value is "does not block", so without this, a kind added to the
// read later would be passed over — and passed over silently, since a green says
// nothing about what it declined to read. A synthetic kind stands in for that
// future one, because no real kind can be unclassified while the completeness
// assertion above passes.
func TestAKindTheGateDoesNotClassifyBlocks(t *testing.T) {
	t.Parallel()

	if !projectionFindingBlocks(projectionparity.Kind("a-kind-added-later")) {
		t.Fatal("a finding whose kind the gate does not classify passes the gate. The default must " +
			"refuse: an unclassified kind is one nobody decided about, and deciding by omission " +
			"decides it in the direction that leaves the gate green.")
	}
}

// TestTheProjectionReadFiresOnAPlantedDrop is the calibration for the gate
// above: it plants a divergence the read must report and asserts it is reported.
//
// Without it, the gate's green says only that the read returned nothing, which
// is also what a read reaching no file, or one wired to the wrong directories,
// returns. The planted drop is the difference between a gate and a decoration.
//
// It deliberately repeats a little of what the read's own fixtures establish,
// and the repetition is the point rather than an oversight: those calibrate the
// read against sources of their own, while this calibrates the entry the gate
// actually calls. Unifying the two would blind whichever one was removed.
func TestTheProjectionReadFiresOnAPlantedDrop(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(root+"/go.mod", []byte("module planted\n\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatalf("write the planted module file: %v", err)
	}
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.MkdirAll(root+"/"+dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(root+"/"+dir+"/"+name, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s/%s: %v", dir, name, err)
		}
	}

	write("internal/core", "core.go", `package core

type Answer struct {
	Verdict bool
	Hint    string
}

func Compute() (Answer, error) { return Answer{}, nil }
`)
	write("internal/one", "one.go", `package one

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
	_ = answer.Hint
}
`)
	write("internal/two", "two.go", `package two

import "planted/internal/core"

func project() {
	answer, _ := core.Compute()
	_ = answer.Verdict
}
`)

	result, err := projectionparity.Analyze(root, "planted", projectionCoreScope, []projectionparity.Surface{
		{Name: "the first surface", Dirs: []string{"internal/one"}},
		{Name: "the second surface", Dirs: []string{"internal/two"}},
	})
	if err != nil {
		t.Fatalf("run the projection read over the planted sources: %v", err)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("the read reports %d finding(s) for one planted drop, want 1:\n%s",
			len(result.Findings), projectionparity.Report(result.Findings))
	}
	finding := result.Findings[0]
	if finding.Kind != projectionparity.DroppedField {
		t.Fatalf("the read calls the planted drop %q, want %q", finding.Kind, projectionparity.DroppedField)
	}
	if !projectionFindingBlocks(finding.Kind) {
		// The link between "the read fires" and "the gate acts on what it
		// fired". Without it this calibration would pass unchanged while the
		// classification excluded the very kind it plants, leaving a calibrated
		// read wired to a gate that ignores it.
		t.Fatalf("the read reports the planted drop as %q and the gate passes that kind over, so a "+
			"divergence this read demonstrably catches would not fail the gate", finding.Kind)
	}
	if finding.Field != "Hint" || finding.Surface != "the second surface" {
		t.Fatalf("the read names %s.%s on %q; want Hint on the second surface",
			finding.Result, finding.Field, finding.Surface)
	}
}
