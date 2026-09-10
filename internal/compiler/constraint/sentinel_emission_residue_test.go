// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The inline-check residue, and why this one list is declared by hand.
//
// A check does not have to be a function. Some of the rules the renderer raises
// are written as an inline block or a closure inside a function whose job is
// something else — building a projection, grouping files for dispatch,
// delivering inject keys. Such a site is a check by nature: it computes a
// predicate over state and its verdict can stop or warn a build. What it lacks
// is a function to migrate. There is nothing whose signature can be read, no
// name to move, and nothing declaring its purpose.
//
// That last absence is why the set below cannot be computed. The census in
// [TestSentinelEmissionSitesAreTheFrameworkDispatch] attributes a raise to a
// declared function or to a check value, and it has no representation for "a
// raise site INSIDE a function" — so an inline block in a render orchestrator is
// indistinguishable, in its data model, from the orchestrator itself raising.
// Every syntactic proxy for "this inline site is a check" was measured against
// this set and every one failed in both directions at once: raise-inside-a
// -function-literal misses the sites that are plain conditional blocks and
// catches a closure that only reshapes a message; a name test over collision
// vocabulary misses the mismatch and cardinality rules and catches a named check
// that is already a function. What separates a check from a helper that happens
// to raise is PURPOSE, and purpose is exactly what a check declaration is for. A
// site with no check declaration has, by construction, nothing for a census to
// read.
//
// So this is a declared inventory, and the standing objection to one is real:
// the assertion beside it refuses to depend on a hand-maintained inventory
// precisely because such a list marks its own coordinates approximate and rots
// unobserved. What answers that objection here is [assertInlineCheckResidue],
// which is mechanical. Every entry is keyed on symbols alone — a vehicle name, a
// host function name, a file — and each is checked against the code on every
// run. No entry carries a line number, deliberately: a line number in a
// committed inventory is a staleness generator, and it is the field that decays
// first while everything around it still reads as current.
//
// # What the listing is FOR, which is not documentation
//
// The census reports how many sentinels are raised outside the framework's
// dispatch, and that count cannot distinguish two very different things: a rule
// somebody looked at, judged, and left inline for a stated reason, and a rule
// nobody has ever examined. Printing this inventory beside the count is what
// makes the difference readable. An entry here is a site already seen and
// dispositioned; a sentinel in the off-framework bucket with no entry here is a
// site nothing has judged.
//
// The listing is deliberately NOT capped the way the census buckets are. A
// bucket is a population whose size is the finding, so naming twelve of it and
// stating the remainder is honest. This is an enumeration whose entire purpose
// is that the residue can be counted off against the code, and a capped
// enumeration cannot do that job at all.
//
// # The reasons are judgements, and three of them are settled rulings
//
// Each reason states why its site was judged liftable or left, in the words of
// the reading that judged it. Three entries — the rendered-document collision,
// and both arms of the governing-service cardinality pair — are settled: each
// site stays in the provider's own guest core because what its predicate reads
// is provider knowledge, and the Go host carries none. Only two placements are
// consistent with that, a provider-neutral fact in the shared registry or the
// provider's own code, and each of the three was read on its own before landing
// on the second — the cardinality pair's arms are separate entries precisely so
// they could have parted.
//
// What each reason keeps is what its predicate reads, because that is what
// makes the ruling checkable. A placement stated without its input is a claim a
// reader has to take on trust, and the input is the whole of the argument.
//
// # Why a sentinel named here does not pollute the census
//
// Every entry below whose verdict travels on a sentinel names that sentinel, and
// a Go file naming a sentinel is what the census counts as an emission site. None
// of them is counted, because the census reads [scannedRoots] under
// [excludeTestFiles] and this is a _test.go file. The property is worth stating
// rather than assuming: were it to change, this inventory would silently add an
// emission site per such entry to the population it exists to describe.
//
// The number of them is deliberately not written here. It falls by one every time
// a site is lifted into a named check, and a written one would keep reading as
// current after it stopped being true — which is exactly what a reader checking
// this inventory against the code is relying on it not to do.

// residueVehicle is how a site's raise reaches a caller. The distinction is not
// cosmetic: it decides which mechanical check the staleness guard can apply, and
// one of the two is invisible to the sentinel census entirely.
type residueVehicle int

const (
	// vehicleSentinel is a site raising a declared sentinel. The census sees it,
	// so the guard can ask the census where that sentinel is raised.
	vehicleSentinel residueVehicle = iota

	// vehicleAdvisoryConstructor is a site whose verdict travels as an advisory
	// message built by a constructor rather than as a declared sentinel. The
	// census cannot see it — the name matches no sentinel, so the population it
	// walks does not contain it — and that blindness is the reason this kind
	// exists rather than being folded into the one above.
	vehicleAdvisoryConstructor
)

// residueDisposition is the judgement recorded for a site: whether it can be
// lifted into a named check, or is left inline, and on what footing.
type residueDisposition int

const (
	// dispositionLiftable means the site can be expressed as a named check
	// without restructuring the function that hosts it.
	dispositionLiftable residueDisposition = iota

	// dispositionLeftInline means the site stays where it is, for the reason its
	// entry records. The reason is specific to the entry and is not a blanket.
	dispositionLeftInline

	// dispositionProviderOwned means the site belongs to the provider's own
	// guest core and is therefore outside the framework's reach for good: what
	// its predicate reads is provider knowledge, and the Go host carries none.
	//
	// It is a ruling with a reason rather than a deferral. Two placements are
	// consistent with the architecture — a provider-neutral fact in the shared
	// registry, or the provider's own code — so a predicate that reads a
	// platform-native resource shape, or bytes that exist only inside a render,
	// takes the second. 🔴 It must not be "completed" by declaring the fact it
	// argues against: a fact naming what these predicates read would carry
	// provider vocabulary into the registry every provider shares, which is the
	// thing the ruling refuses.
	dispositionProviderOwned
)

// String renders a disposition for the report. Each one names the placement it
// records and nothing about how firmly it is held: a disposition here is a
// judgement already made, and the reason beside it in the report is where a
// reader checks it.
func (d residueDisposition) String() string {
	switch d {
	case dispositionLiftable:
		return "liftable into a named check"
	case dispositionLeftInline:
		return "left inline"
	case dispositionProviderOwned:
		return "owned by the provider's guest core, out of the framework's reach"
	}
	return "unrecorded disposition"
}

// inlineResidueEntry is one check embedded as an inline block or closure. It is
// keyed on symbols only: the vehicle its verdict travels on, the function
// hosting it, and the file that function is declared in.
type inlineResidueEntry struct {
	vehicle     string
	vehicleKind residueVehicle
	host        string
	file        string
	disposition residueDisposition
	reason      string
}

// inlineCheckResidue is the inventory. Its order is the order it is read in and
// is stable across runs, so the report names the same entries in the same
// sequence without a sort: entries sharing a host sit together, which is what a
// reader needs when judging whether a host's sites can move as a group.
//
// A site raising two sentinels is TWO entries. That is deliberate — it makes a
// partial extraction representable, which a single entry naming both would not.
var inlineCheckResidue = []inlineResidueEntry{
	{
		vehicle:     "ErrInstanceIDCollision",
		vehicleKind: vehicleSentinel,
		host:        "RenderAll",
		file:        "internal/compiler/renderer/orchestrator.go",
		disposition: dispositionLiftable,
		reason: "A pure duplicate test over the dispatch grouping, whose own inline comment already calls " +
			"itself a check. Its inputs are the flattened node set and the boundary paths, a topology-stage " +
			"read, and the grouping map is fully built at that point in the same scope — so lifting it to a " +
			"named check over the boundary, provider and node coordinates does not touch the render loop.",
	},
	{
		vehicle:     "ErrArtifactOutputCollision",
		vehicleKind: vehicleSentinel,
		host:        "buildArtifactsProjection",
		file:        "internal/compiler/renderer/artifacts.go",
		disposition: dispositionLeftInline,
		reason: "A clean duplicate test over a Recipe's declared artifact output names, normalized-stage. " +
			"The projection name set is a declared fact, keyed by node and artifact — but the emitted-file " +
			"origin this collision class needs is not declared, and the node-and-artifact key structurally " +
			"cannot see the class: the origin set spans five origins of which only two are authored " +
			"artifacts. Leaving is what the declared facts support, not a deferral of convenience.",
	},
	{
		vehicle:     "ErrRenderedFileCollision",
		vehicleKind: vehicleSentinel,
		host:        "RenderLoadedChart",
		file:        "plugins/internal/helmcore/render_core.go",
		disposition: dispositionProviderOwned,
		reason: "A duplicate test with a byte-equality escape, whose predicate reads Helm rendered-document " +
			"identity — the resource kind and name pair the output key derives from — against the output map " +
			"the render is still filling, comparing the document's post-injection bytes with the bytes " +
			"already at that key. Two things place it in the provider, and either alone would. A declared " +
			"fact naming that identity would put provider-specific vocabulary into the shared fact registry, " +
			"which is the bound that keeps the provider-internal rules outside the framework's reach. And no " +
			"declared fact could carry this predicate's input in any case: a view exposes facts complete at " +
			"one point in the compilation, while this map is mid-render in guest memory and the export hands " +
			"back one value per key — so the contention two sources had over a key is gone before the host " +
			"ever sees the output, and the rule can only be asked where the bytes still are.",
	},
	{
		vehicle:     "ErrStatefulSetNoHeadlessService",
		vehicleKind: vehicleSentinel,
		host:        "statefulSetGoverningService",
		file:        "plugins/internal/helmcore/synth_core.go",
		disposition: dispositionProviderOwned,
		reason: "A cardinality test over the headless services declared on the synthesized workload, " +
			"cleanly self-contained — the predicate reads Kubernetes Service shape, counting the " +
			"translated services whose clusterIP is None, and the site returns the single one's rendered " +
			"name for the StatefulSet's serviceName. The same provider-vocabulary bound applies as for the " +
			"rendered-document collision above, and two further things hold it here. The requirement exists " +
			"only because this provider synthesizes a StatefulSet, so a host-side check over the authored " +
			"facts — which are host-visible — would halt a workload that is valid for whichever provider it " +
			"is actually rendered through, which is the core policing a provider's convention. And the " +
			"rendered name this site hands back does not exist host-side at all, so a check there could " +
			"only duplicate the counting half and leave the resolution where it already is.",
	},
	{
		vehicle:     "ErrStatefulSetAmbiguousHeadlessService",
		vehicleKind: vehicleSentinel,
		host:        "statefulSetGoverningService",
		file:        "plugins/internal/helmcore/synth_core.go",
		disposition: dispositionProviderOwned,
		reason: "The ambiguous arm of the same cardinality test in the same host, recorded as its own entry " +
			"so that lifting one arm and leaving the other is representable. It reads the same count of the " +
			"same Kubernetes Service shape and was judged on its own rather than by inheritance, reaching " +
			"the same placement as the zero arm. The entry stays separate because that is what keeps the " +
			"agreement a finding: two entries that could have parted and did not say more than one entry " +
			"covering both, which could not have parted at all.",
	},
}

// reportInlineCheckResidue prints the inventory as part of the emission
// assertion's report, then runs the staleness guard over it.
//
// The listing and the guard are one call deliberately. A listing without the
// guard is the hand-maintained inventory this package refuses to depend on, and
// a guard whose findings nothing prints leaves the residue unenumerable — which
// is the whole reason the listing exists.
//
// No t.Helper() here or in the guard below, deliberately: there is one call site,
// so attributing a line to it conveys nothing, while attributing it to this file
// puts a reader chasing a message at the code that writes it.
func reportInlineCheckResidue(t *testing.T, emitters map[string][]emissionSite) {
	if len(inlineCheckResidue) == 0 {
		t.Fatalf("the inline-check residue inventory is empty, so this listing and its staleness guard " +
			"certify nothing. An empty inventory is indistinguishable from a fully migrated tree here, " +
			"which is the one reading it must never produce.")
	}

	hosts := map[string]bool{}
	lines := make([]string, 0, len(inlineCheckResidue))
	for _, e := range inlineCheckResidue {
		hosts[e.file+"."+e.host] = true
		lines = append(lines, fmt.Sprintf("%s in %s (%s) — %s: %s",
			e.vehicle, e.host, e.file, e.disposition, e.reason))
	}

	t.Logf("known inline-check residue: %d entries across %d host function(s). Each entry is one check "+
		"embedded as an inline block or closure rather than a named function, so it has no signature to "+
		"migrate and nothing declaring its purpose for the census above to read. A host raising two "+
		"sentinels contributes two entries, so this count is of entries and not of hosts or of files.\n"+
		"WHY THIS IS LISTED: the off-framework count above cannot distinguish a rule somebody examined and "+
		"left inline for a stated reason from a rule nobody has looked at. An entry here is a site already "+
		"seen and dispositioned; an off-framework sentinel with no entry here is a site nothing has judged. "+
		"The listing is uncapped, unlike the census buckets, because an enumeration that exists to be "+
		"counted off against the code cannot do that job truncated.\n"+
		"%s",
		len(inlineCheckResidue), len(hosts), strings.Join(lines, "\n"))

	t.Logf("WHAT THE GUARD BELOW DOES NOT CHECK, and the consequence is a real one: it establishes that " +
		"each named host still raises its vehicle, never that the site is still INLINE. Expressing one of " +
		"these as a named check while leaving its entry in place keeps the guard green and leaves this " +
		"listing overstating the residue. Removing the entry is therefore part of that change, not a " +
		"follow-up to it.")

	assertInlineCheckResidue(t, emitters)
}

// assertInlineCheckResidue is the staleness guard: the mechanical half that
// makes a hand-declared inventory honest.
//
// It checks three properties per entry, and the third is the reason the guard is
// worth more than its dispatch lookup alone.
//
//  1. The named host function is still declared in the named file. The
//     attribution in the census keys a site by package DIRECTORY, so a host
//     moving to another file in the same package would leave the entry's file
//     field silently wrong while the lookup below still passed — the field would
//     be decorative.
//  2. For a sentinel vehicle, the census still attributes that sentinel to that
//     host. This reads the census's own data rather than re-deriving it, so a
//     sentinel that was retired, renamed out of the declaration convention, or
//     whose raise moved elsewhere all fail here.
//  3. For an advisory vehicle, the host's body still names the constructor. The
//     census is structurally blind to this one, so it needs its own check; a
//     skipped entry is exactly the rot this guard exists to prevent.
//
// It fails rather than reports, and it is deliberately NOT gated on
// [offFrameworkEmissionBlocks]. That constant grants a grace period to a
// migration backlog that is expected to be non-empty. Inventory staleness has no
// backlog: an entry is either current or wrong.
//
// The count of inspected entries is asserted, not merely printed. A guard that
// walks a shape which has stopped existing inspects nothing, reports nothing and
// passes — so an entry reaching no arm below lands in the default arm, is named
// as skipped, and moves the count off the inventory's own length.
func assertInlineCheckResidue(t *testing.T, emitters map[string][]emissionSite) {
	// Parsed per named file rather than through the census's own walk: this asks
	// about specific files an entry names, which is a different question from
	// what the asserted population is, and reusing that walk here would tie a
	// file lookup to a population criterion it has no stake in.
	fset := token.NewFileSet()
	parsed := map[string]*ast.File{}
	for _, e := range inlineCheckResidue {
		if _, done := parsed[e.file]; done {
			continue
		}
		file, err := parser.ParseFile(fset, moduleRoot+"/"+e.file, nil, 0)
		if err != nil {
			t.Errorf("the inline-check residue inventory names %s, which cannot be read: %v.\n"+
				"REMEDY: point the entry at the file its host is declared in now, or remove the entry if "+
				"the site is gone.", e.file, err)
			continue
		}
		parsed[e.file] = file
	}

	inspected := 0
	for _, e := range inlineCheckResidue {
		if file, ok := parsed[e.file]; ok && !declaresFunc(file, e.host) {
			t.Errorf("the inline-check residue inventory attributes %s to %s in %s, but no function of that "+
				"name is declared there.\n"+
				"REMEDY: the host was renamed, moved to another file, or removed — update the entry to the "+
				"host raising it now, or remove the entry if the site is gone.", e.vehicle, e.host, e.file)
		}

		switch e.vehicleKind {
		case vehicleSentinel:
			inspected++
			want := moduleRoot + "/" + dirOf(e.file) + "." + e.host
			attributed := false
			var sites []string
			for _, site := range emitters[e.vehicle] {
				sites = append(sites, site.name)
				if site.name == want {
					attributed = true
				}
			}
			if !attributed {
				t.Errorf("the inline-check residue inventory records %s as raised by %s, and the census "+
					"does not attribute it there.\n"+
					"The census attributes it to: %s.\n"+
					"CONSEQUENCE: this entry describes a site that has moved or gone, so the listing "+
					"overstates the residue and understates what remains unexamined.\n"+
					"REMEDY: update the entry to where the sentinel is raised now; or, if it was expressed "+
					"as a check, remove the entry, because that removal is part of the change and not a "+
					"follow-up to it.",
					e.vehicle, want, describeSites(sites))
			}
		case vehicleAdvisoryConstructor:
			inspected++
			file, ok := parsed[e.file]
			if !ok {
				continue // already reported as unreadable above
			}
			if !funcBodyNames(file, e.host, e.vehicle) {
				t.Errorf("the inline-check residue inventory records %s as the advisory vehicle raised by %s "+
					"in %s, and that function's body no longer names it.\n"+
					"This entry is checked here rather than against the census because its vehicle is not a "+
					"sentinel: the census population does not contain it, so nothing above can see this "+
					"entry go stale.\n"+
					"REMEDY: update the entry, or remove it if the site is gone.", e.vehicle, e.host, e.file)
			}
		default:
			t.Errorf("the inline-check residue entry for %s in %s carries a vehicle kind this guard has no "+
				"arm for, so it was SKIPPED — no property of it was checked.\n"+
				"A silently skipped entry is the exact rot this guard exists to prevent, which is why it "+
				"fails here rather than passing over it.", e.vehicle, e.host)
		}
	}

	// The skipped figure is computed, never written as a constant. A summary line
	// asserting "none skipped" beside a loop that can skip is the same class of
	// false report this whole guard exists to prevent, and it reads as
	// reassurance precisely when it is wrong.
	skipped := len(inlineCheckResidue) - inspected
	if skipped != 0 {
		t.Errorf("the inline-check residue guard inspected %d of %d entries, leaving %d unchecked.\n"+
			"This count is asserted rather than printed because a guard walking a shape that has stopped "+
			"existing inspects nothing and passes, and a clean result over an uninspected inventory "+
			"certifies nothing at all.", inspected, len(inlineCheckResidue), skipped)
	}

	t.Logf("inline-check residue staleness guard: %d of %d entries inspected, %d skipped. Per entry: the "+
		"named host is still declared in the named file; a sentinel vehicle is still attributed to that "+
		"host by the census above; an advisory vehicle, which the census cannot see, is still named in that "+
		"host's body. The guard reads syntax and resolves no types, exactly as the census does.",
		inspected, len(inlineCheckResidue), skipped)
}

// dirOf returns the slash-separated directory of a file path recorded in the
// inventory. The inventory spells its paths module-relative with forward
// slashes, which is the spelling the census's site names carry, so this is a
// string operation rather than a filesystem one.
func dirOf(file string) string {
	if cut := strings.LastIndex(file, "/"); cut >= 0 {
		return file[:cut]
	}
	return "."
}

// describeSites renders the sites a census attributed a sentinel to, naming the
// empty case rather than printing an empty string: "no site at all" and "a
// different site" are different findings and a bare empty list reads as neither.
func describeSites(sites []string) string {
	if len(sites) == 0 {
		return "no site at all, so the sentinel is declared and nothing raises it, or it is no longer declared"
	}
	return strings.Join(sortedNames(sites), " and ")
}

// declaresFunc reports whether file declares a function or method named name. A
// method counts, because the census's emitter walk records one: a raise inside a
// method is attributed to the method's own name.
func declaresFunc(file *ast.File, name string) bool {
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Body != nil && fd.Name.Name == name {
			return true
		}
	}
	return false
}

// funcBodyNames reports whether the body of the function named host in file
// names the identifier ident.
//
// It reads through [namesIn] with a single-member population rather than
// re-walking the syntax itself, so "which names does this piece of syntax name"
// keeps one home however the question is asked.
func funcBodyNames(file *ast.File, host, ident string) bool {
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil || fd.Name.Name != host {
			continue
		}
		if namesIn(map[string]string{ident: ""}, fd.Body)[ident] {
			return true
		}
	}
	return false
}
