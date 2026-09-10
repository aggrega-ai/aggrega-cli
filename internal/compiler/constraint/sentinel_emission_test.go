// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The sentinel-emission assertion.
//
// What makes something a check is not what it reads but that its output can stop
// or warn a build. So the surface that makes an off-framework check detectable is
// the currency of halting: every sentinel's emission sites should be exactly the
// framework's dispatch. A check written outside the framework then has two
// options and both are visible. Reuse an existing sentinel, and the sentinel
// gains a second emission site. Mint a new one, and there is a sentinel no
// registered check emits.
//
// This is enforcement rather than documentation, which is why it lives with the
// framework. It derives its data from the code — the sentinel declarations, the
// function bodies and the check declarations — and never from a hand-maintained
// inventory: that inventory marks its own coordinates approximate and asks to be
// verified against the code, so it disclaims exactly the precision an assertion
// needs. It is a useful shape template for which fields matter and an unusable
// source for what their values are.
//
// # Enforcement is gated per population, because the two findings differ in kind
//
// This assertion answers the emitter question in four buckets, and two of them
// carry findings. Those two cannot share one tier, and one constant gating both
// gave the smaller finding the larger one's grace period. They are separate
// constants for that reason.
//
// The off-framework bucket CANNOT block on its population, and the reason is
// structural rather than a matter of confidence in it. Until the checks that
// raise these sentinels are written against the framework and the paths that
// raise them today are removed, most sentinels in the tree are emitted outside
// the framework's dispatch — so an assertion blocking on the population itself
// would fail on the shipped tree, every run, and would be switched off rather
// than fixed. It is gated by [offFrameworkEmissionBlocks], which is false, and
// the change that removes the last off-framework emission path flips it to true
// in the same edit that makes the flip pass. What blocks meanwhile is
// [offFrameworkEmissionCeiling]: the population may fall and may not rise.
//
// The no-emission-site bucket DOES block, gated by [unemittedSentinelsBlock].
// Its population is small, enumerable and not a backlog the tree is working
// through, so the names carrying it are recorded in [knownUnemittedSentinels]
// and anything outside that set fails. There is nothing for a grace period to
// protect: a sentinel arriving with no emission site is a rule removed without
// its name, or a name minted for a check never written, and either is wrong at
// the moment it lands rather than scheduled to be fixed.
//
// Reporting is weaker than it sounds, and saying so here is the point: nothing
// about a report-only assertion distinguishes one whose finding reaches a reader
// from one whose finding reaches nobody, because the code plainly writes the
// message either way. A Go package that passes prints nothing in a routine run —
// not the framework's log, not a write to standard error, not a write to
// standard output — so a count this assertion only logs reaches a reader only
// under a verbose run (`go test -v`) of this package. The channel measurement
// behind that claim is recorded once, beside [codeLiteralGuardBlocks], rather
// than restated here. That property is why each bucket above carries a channel
// that FAILS rather than a shared one that reports: a finding whose only channel
// is a log line on a passing test has no channel at all.
//
// What still blocks regardless of either constant is the instrument itself: a
// declared population of zero is fatal below, so a census that has stopped
// working is loud even while a population it reports is quiet.
//
// # What counts as the framework's dispatch, and why not by address
//
// A check in the framework's prescribed shape is a package-level value of
// [Check], and its predicate is a function literal rather than a declared
// function. Such a declaration belongs to the dispatch wherever it lives, and
// the reason is the framework's own contract rather than its address: a check
// returns a raise, a raise can only be produced from a subject the framework's
// traversal supplied, and the framework — not the check — builds whatever each
// caller needs from it. The halting currency at that site is framework-issued by
// construction, which is exactly what reading the declaring package's path was
// standing in for. A plain declared function is still read by its path: written
// inside this package it belongs to the dispatch, written anywhere else it does
// not. So expressing a rule as a check is what moves it, and no rule is
// amnestied by where its file sits.
//
// # The blind spot this admission carries
//
// The census reads declarations and cannot observe invocation, so a check that
// is declared and never invoked is attributed to the dispatch exactly as one
// that runs. The within-framework figure therefore answers "is this rule
// expressed as a check?" and never "does this rule run?" — and the distinction
// is live rather than theoretical, because a check's declaration and its wiring
// arrive separately. It is stated here, in the census output and in the failure
// message because an unstated heuristic that reads as a proof is the failure
// class this framework exists to remove, and an instrument is the most expensive
// place to leave one: every count downstream inherits its silence.
//
// # The census criterion, and what it deliberately excludes
//
// A sentinel is a distinct name matching [sentinelName] bound by a package-level
// declaration in a non-test .go file of this module: a function declaration, or
// a var or const specification. The asserted population is those declared under
// [scannedRoots]. Every count this file reports is a count of that population and
// of nothing else, so the criterion is written down here: a count whose criterion
// is implicit invites two readers to compare two different measurements and
// conclude the population moved.
//
// The exclusions, each a choice about what the population IS rather than a
// convenience, so that none of them is later "fixed" as an oversight:
//
//   - A _test.go file. A sentinel declared in a test is scaffolding rather than
//     a rule the product raises, and including them would additionally make
//     this assertion assert over its own instrument.
//   - A testdata directory. Fixture Go is example data, not specification.
//   - A directory carrying its own go.mod, and a vendor directory. Their
//     sentinels belong to another module, so reporting one would be a finding
//     nobody working in this module could act on.
//   - A directory whose name begins with "." or "_", which is what the Go tool
//     itself skips, so a file there is in no package this module builds.
//   - A var or const bound inside a function body. The census reads file-level
//     declarations only, because the convention a sentinel follows is a
//     package-level exported name. Both binding shapes are named because both
//     are admitted at file level, so a bullet naming one of them would read as
//     admitting the other inside a body.
//   - A method. A name bound to a receiver is reached through a value rather
//     than through the package, so it is not a name a consumer matches against.
//
// And three deliberate inclusions, recorded because they are the ones a later
// edit is most likely to undo.
//
// A function declaration counts, and it is the shape most sentinels take: the
// identity itself is a value no caller can construct, published by an exported
// accessor standing beside the registration that declares it. The accessor's
// name is the name a consumer matches against, so the accessor is the
// declaration this census counts.
//
// A const or var declaration counts. A sentinel is the distinct exported name a
// consumer matches against and the rule that name stands for, not the kind of
// declaration that binds it, so an identity bound to a value is a sentinel
// exactly as one published by an accessor is. Reading one shape alone would make
// this a count of that shape rather than of the rules this module raises, and a
// rule whose declaration changed shape would then read as a rule that
// disappeared — the one reading a population count must never produce, because
// it is indistinguishable from a rule that was genuinely dropped.
//
// A forwarding declaration, of either shape —
//
//	func ErrX() error { return otherpkg.ErrY() }
//	var  ErrX       = otherpkg.ErrY
//
// — counts. [sentinelName] is matched against the declared name and nothing
// constrains what the declaration yields, which is deliberate: the forwarding
// name is a distinct exported name a consumer can match against, so it is a
// sentinel of this module even though the identity it carries originates
// elsewhere. Constraining the right-hand side to a registration would read as
// tightening the criterion and would silently drop every re-export.
//
// # Why a forwarding declaration is reported in a bucket of its own
//
// That inclusion is right on the declaration side and it leaves a hole on the
// emitter side, because a forwarding declaration matches none of the site
// shapes above: the rule it carries is raised where the sentinel it names is
// raised, in a declaration that is not this one. Counted only as declared and
// never as emitted, it lands among the sentinels nothing emits — and the
// sentence that bucket carries, that such a name is either a rule removed
// without its name or a name minted for a check never written, is false of it.
// It is neither. It is a second name, deliberately published, for a rule that
// is written and does run.
//
// So the emitter question is answered in four buckets rather than three, and a
// forwarding declaration is reported as what it is, beside the sentinel whose
// identity it carries. The alternative — attributing the named sentinel's
// emission sites to the forwarding name — was not taken: it would move these
// into the off-framework count, and that count is the one measuring how much of
// the migration remains, so inflating it with names that raise nothing would
// corrupt the reading the gate above exists to give.
//
// The classification resolves no types, exactly as [checkTypeName] does not. It
// reads whether the declaration names another sentinel of this same population,
// which is a syntactic question about the declaration rather than a question
// about what any expression evaluates to.
const offFrameworkEmissionBlocks = false

// offFrameworkEmissionCeiling is the ratchet that gives the off-framework
// population a channel that fails while [offFrameworkEmissionBlocks] is false.
//
// That population is a MIGRATION METER and not a defect list: it measures how
// much of the migration remains, so blocking on the population itself would
// redden the shipped tree on every run. Blocking on an INCREASE costs nothing
// while the number holds or falls, and it makes the one movement that matters —
// the meter going backwards — loud instead of silent. Nothing weaker will do
// that job here, because a passing Go package prints nothing: a rise that is
// reported and not failed is a rise nobody learns about.
//
// Criterion, so that a later reading measures the same thing: the number of
// distinct sentinel names, matching [sentinelName] and bound by a package-level
// declaration in a non-test .go file under [scannedRoots], for which the census
// attributes at least one emission site outside the framework's dispatch —
// attribution exactly as [checkTypeName] and [frameworkPackageSuffix] decide it.
// Measured 2026-09-10 at aggrega-cli 5ebaf069: 238, of 280 declared.
//
// The bound this leaves, recorded so the ceiling is never read as the
// population: a FALL is deliberately not failed, so once the count drops this
// constant carries slack the ratchet does not reclaim, and a regression inside
// that slack is invisible. Reclaiming it is a one-line edit here, belonging to
// whatever change lowers the count rather than to a later tidy-up.
const offFrameworkEmissionCeiling = 238

// unemittedSentinelsBlock decides whether a sentinel with no emission site at
// all fails this test or is only reported. It is true.
//
// This population shared a constant with the off-framework meter until the two
// were split, and the split is the substance rather than the bookkeeping: the
// meter cannot block because its population is a backlog the tree is working
// through, while this one is neither a backlog nor expected to grow. A sentinel
// nothing emits is a rule removed without its name, or a name minted for a check
// never written; both are decidable from the census alone, and both are wrong
// when they land rather than scheduled. So the grace period the meter needs is
// exactly what this population must not inherit.
//
// What makes blocking possible without reddening the shipped tree is
// [knownUnemittedSentinels], which records the names carrying the population
// today. This assertion fails on anything outside that set. That is a narrowing
// of the corpus rather than an amnesty, and the set is asserted current in both
// directions — see its own comment.
const unemittedSentinelsBlock = true

// knownUnemittedSentinels are the sentinels the census finds no emission site
// for today. It is the recorded baseline [unemittedSentinelsBlock] blocks
// against, and it is sorted so the failure messages below read in a stable
// order.
//
// Criterion: a sentinel of the asserted population, as [scannedRoots] and
// [sentinelName] define it, for which the census finds no emission site and
// whose declaration names no other sentinel of that population — the same
// partition the assertion below computes, so the two cannot disagree about what
// membership means. Measured 2026-09-10 at aggrega-cli 5ebaf069: these four and
// no others.
//
// 🔴 THIS LIST IS A BRIDGE AND NOT THE HOME OF WHAT IT RECORDS, so it is worth
// knowing what replaces it before adding anything to it.
//
// The fact each entry stands for — nothing raises this condition yet, and here
// is the work that will — already has a declared single home: the DeclaredAhead
// note on the condition's own registration, documented in pkg/errors. That
// declaration argues in writing against exactly the shape below, on the ground
// that a side list is a second home for the correspondence and a second home is
// what drifts when a condition is renamed, moved between packages or removed.
// The same file records why nothing inside one registration can perform this
// check: whether an unnoted condition is in fact raised is a property of the
// whole module. That is the gap this assertion closes, and closing it does not
// license storing the correspondence here.
//
// All four ARE registered identities, so DeclaredAhead can reach every one of
// them; measured 2026-09-10, exactly one of the four carries such a note. The
// list exists because the other three do not, and the note is a published
// sentence naming the work that will raise the condition — content this
// assertion cannot invent, since the census establishes only that nothing emits
// them and says nothing about why. Until then their absence from the published
// reference is reported by a WARN in the reference generator and by nothing that
// fails, which is the channel this assertion exists to replace.
//
// So an entry leaves this list when its condition gains a DeclaredAhead note or
// a raise, and the list is deleted outright once none of the four needs it. Do
// not grow it as the way of admitting a new unraised sentinel: that is what
// DeclaredAhead is for, and an entry added here instead takes the correspondence
// out of the one place it travels with the identity.
//
// An entry is a standing permission, so it is guarded in both directions. A
// sentinel absent from this list and missing an emission site fails. An entry
// naming a sentinel that has since acquired an emission site, been renamed, or
// been removed fails too, for the reason recorded at the view-accessor
// exceptions in rawdocument_guard_test.go: a stale exception is worse than none,
// because it reads as a known hole where there is none and goes on exempting its
// name for as long as it stands.
var knownUnemittedSentinels = []string{
	"ErrAmbiguousImporterPlugin",
	"ErrCatalogLookup",
	"ErrProviderBindingUnsatisfied",
	"ErrProviderVersionUnsatisfiable",
}

// sentinelName matches the declaration convention for a sentinel: an exported
// package-level error variable named for the rule it stands for.
var sentinelName = regexp.MustCompile(`^Err[A-Z][A-Za-z0-9]*$`)

// scannedRoots are the subtrees whose declarations form the asserted
// population, relative to this package: every tree in this module that holds
// Go.
//
// The set is asserted to cover the module rather than assumed to —
// [TestAssertedSentinelPopulationCoversTheModule] reads the whole module with
// the identical criterion and fails on anything these roots do not reach. A
// root that declares no sentinel today is still listed, because a tree absent
// from this set is precisely where a later sentinel arrives unobserved: the
// census would report a clean population while saying nothing about the file it
// never opened.
var scannedRoots = []string{
	"../../../pkg",
	"../../../internal",
	"../../../cmd",
	"../../../plugins",
}

// moduleRoot is this module's root, relative to this package. It is read for one
// purpose only — establishing that [scannedRoots] covers it — and never as the
// population itself: an explicit root list is what makes the asserted scope
// legible, and widening the scope to the module root would trade that legibility
// for a scope that silently absorbs whatever tree appears next.
const moduleRoot = "../../.."

// frameworkPackageSuffix locates the framework's own package, which is how a
// plain declared function's membership is read: a function declared here is part
// of the dispatch, and every other function naming a sentinel is not. A check
// declaration's membership is not read this way — see [checkTypeName].
const frameworkPackageSuffix = "internal/compiler/constraint"

// checkTypeName is the type whose package-level values are the framework's
// dispatch by construction. The census matches it as syntax — the name a
// composite literal constructs, with any generic instantiation and package
// qualifier unwrapped — and deliberately resolves no types: a type-resolving
// census is a different instrument with a different cost, and the name is
// unambiguous here because the framework declares the only type that bears it.
const checkTypeName = "Check"

func TestSentinelEmissionSitesAreTheFrameworkDispatch(t *testing.T) {
	t.Parallel()

	declared, emitters, forwards := sentinelCensus(t)
	if len(declared) == 0 {
		t.Fatalf("no sentinel declaration was found under %s, so a clean result certifies nothing",
			strings.Join(scannedRoots, ", "))
	}

	var offFramework, unemitted, withinFramework, forwarding []string
	for _, name := range declared {
		sites := emitters[name]
		if len(sites) == 0 {
			// A sentinel with no site of its own whose declaration names
			// another sentinel is a forwarding declaration, not a rule nothing
			// raises. Only the no-site set is read this way: one that does have
			// sites is already answered truthfully, and re-reading it here would
			// move a name out of a count that describes it correctly.
			if len(forwards[name]) > 0 {
				forwarding = append(forwarding, name)
			} else {
				unemitted = append(unemitted, name)
			}
			continue
		}
		outside := false
		for _, site := range sites {
			if !site.framework {
				outside = true
			}
		}
		if outside {
			offFramework = append(offFramework, name)
		} else {
			withinFramework = append(withinFramework, name)
		}
	}

	reportOffFramework := t.Logf
	if offFrameworkEmissionBlocks {
		reportOffFramework = t.Errorf
	}

	t.Logf("sentinel census: %d declared; %d emitted only by the framework's dispatch; "+
		"%d emitted outside it; %d forwarding to another sentinel; %d with no emission site at all",
		len(declared), len(withinFramework), len(offFramework), len(forwarding), len(unemitted))
	t.Logf("the within-framework figure is counted from declarations: a rule expressed as a %s value "+
		"counts there wherever it is declared, and it counts whether or not anything invokes it yet. "+
		"It measures how many rules are expressed as checks, never how many run.", checkTypeName)
	if len(withinFramework) > 0 {
		t.Logf("emitted only by the framework's dispatch, each named beside the declaration it was "+
			"attributed from, so the attribution can be checked rather than taken: %s",
			strings.Join(namedList(namedWithSites(withinFramework, emitters), 12), "; "))
	}

	if len(offFramework) > 0 {
		reportOffFramework("%d sentinel(s) are emitted outside the framework's dispatch.\n"+
			"INVARIANT: a sentinel's emission sites are exactly the framework's dispatch, because that is "+
			"what makes a check written outside the framework detectable — reusing a sentinel shows up as "+
			"a second emission site, and minting one shows up as a sentinel no registered check emits.\n"+
			"ATTRIBUTION: a sentinel named in a package-level check declaration belongs to the dispatch "+
			"wherever that declaration lives, because the halting currency there is framework-issued by "+
			"construction; a plain declared function is read by its path instead. That reads declarations "+
			"and cannot observe invocation, so a check declared and never invoked is attributed to the "+
			"dispatch too.\n"+
			"CONSEQUENCE while this population reports rather than blocks: an off-framework check gets no "+
			"computed placement, no caller-coverage guarantee and no framework-supplied provenance, and "+
			"nothing halts on its absence. The population is not free to grow, though — see the ratchet "+
			"below.\n"+
			"Alphabetically: %s", len(offFramework), strings.Join(namedList(offFramework, 12), ", "))
	}

	assertOffFrameworkCeiling(t, len(offFramework), len(declared))

	// The off-framework count above cannot tell a rule somebody examined and left
	// inline from a rule nobody has looked at, and the listing is what separates
	// them. It runs unconditionally, including when nothing is off-framework: an
	// inventory describing sites that have all moved is exactly the state its
	// staleness guard exists to fail on.
	reportInlineCheckResidue(t, emitters)

	if len(forwarding) > 0 {
		t.Logf("%d sentinel(s) have no emission site of their own because their declaration names "+
			"another sentinel, each shown beside the sentinel whose identity it carries so the "+
			"attribution can be checked rather than taken: %s.\n"+
			"A forwarding declaration publishes a second name a consumer can match against for a rule "+
			"raised elsewhere, so the sites that raise it are the sites of the sentinel it names. It is "+
			"neither a rule that was removed nor a name minted for a check that was never written, "+
			"which is why it is reported apart from those.",
			len(forwarding), strings.Join(namedList(namedWithForwards(forwarding, forwards), 12), "; "))
	}
	assertUnemittedSentinels(t, unemitted)

	if !offFrameworkEmissionBlocks {
		t.Logf("the off-framework population reports and does not block on its size. It becomes blocking "+
			"when the last emission path outside the framework's dispatch is removed; until then it would "+
			"fail on the shipped tree on every run. What does block is the ceiling above, which refuses an "+
			"increase, and the no-emission-site population, which blocks against its recorded baseline. "+
			"Off-framework population at this run: %d of %d sentinels.",
			len(offFramework), len(declared))
	}
}

// assertOffFrameworkCeiling is the increase-only ratchet over the off-framework
// population, and it is the channel that fails while
// [offFrameworkEmissionBlocks] is false.
//
// It is separate from the report above because the two answer different
// questions. The report says how large the migration backlog is, which is a
// figure a reader wants and no build should halt on. This says whether the
// backlog GREW, which is the one movement that is a defect rather than a status.
//
// A fall is not failed, deliberately, and the slack that leaves is recorded at
// [offFrameworkEmissionCeiling] rather than here, so the bound sits with the
// number it bounds.
func assertOffFrameworkCeiling(t *testing.T, offFramework, declared int) {
	t.Helper()

	switch {
	case offFramework > offFrameworkEmissionCeiling:
		t.Errorf("%d sentinel(s) are emitted outside the framework's dispatch, above the recorded ceiling "+
			"of %d.\n"+
			"INVARIANT: this population is a migration meter, so it may fall and may not rise.\n"+
			"CONSEQUENCE: a new off-framework emission path has been added, or a check was expressed "+
			"outside the framework. Either way the migration this figure measures moved backwards, and "+
			"the ceiling is what makes that loud — the population itself cannot block, so without this "+
			"the increase would reach a reader only under a verbose run.\n"+
			"REMEDY: express the rule as a %s value so it is attributed to the dispatch. Raising the "+
			"ceiling is not the remedy and is the one edit here that removes the guarantee: the number "+
			"is only meaningful as a bound the tree has never exceeded.",
			offFramework, offFrameworkEmissionCeiling, checkTypeName)
	case offFramework < offFrameworkEmissionCeiling:
		t.Logf("off-framework population is %d of %d declared, below the recorded ceiling of %d. The "+
			"ratchet refuses an increase and does not refuse a fall, so this slack of %d is not reclaimed "+
			"until offFrameworkEmissionCeiling is lowered — a one-line edit belonging to whatever change "+
			"lowered the count. Within that slack a regression is not detected.",
			offFramework, declared, offFrameworkEmissionCeiling, offFrameworkEmissionCeiling-offFramework)
	default:
		t.Logf("off-framework population is %d of %d declared, exactly at the recorded ceiling. The "+
			"ratchet refuses an increase.", offFramework, declared)
	}
}

// assertUnemittedSentinels blocks on a sentinel with no emission site that the
// recorded baseline does not name, and on a baseline entry that no longer
// describes anything.
//
// Both directions matter and only one of them is obvious. Failing on a NEW
// unemitted sentinel is the point of promoting this population. Failing on a
// STALE entry is what keeps the baseline from quietly becoming a permanent
// exemption list: an entry whose sentinel has since acquired an emission site
// reads as a known hole where there is none, and it goes on exempting that name
// for as long as it stands.
//
// The baseline is asserted to be duplicate-free before it is believed, on the
// reasoning the residue guard records: a name written twice makes the recorded
// set smaller than it reads, so a reader counting entries and a reader counting
// distinct names disagree, and the smaller set is the one that governs. That is
// refused rather than normalised, because silently collapsing it would let the
// list misdescribe its own size indefinitely.
func assertUnemittedSentinels(t *testing.T, unemitted []string) {
	t.Helper()

	baseline := make(map[string]bool, len(knownUnemittedSentinels))
	for _, name := range knownUnemittedSentinels {
		baseline[name] = true
	}
	if len(baseline) != len(knownUnemittedSentinels) {
		t.Fatalf("the recorded baseline of %d entries holds only %d distinct name(s), so one is written "+
			"twice and the set does not say what it appears to",
			len(knownUnemittedSentinels), len(baseline))
	}

	found := map[string]bool{}
	var fresh []string
	for _, name := range unemitted {
		found[name] = true
		if !baseline[name] {
			fresh = append(fresh, name)
		}
	}
	sort.Strings(fresh)

	var stale []string
	for _, name := range knownUnemittedSentinels {
		if !found[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)

	report := t.Logf
	if unemittedSentinelsBlock {
		report = t.Errorf
	}

	if len(fresh) > 0 {
		report("%d sentinel(s) have no emission site at all and are not in the recorded baseline: %s.\n"+
			"INVARIANT: every declared sentinel is raised somewhere, or is recorded as a known exception "+
			"with its criterion and date.\n"+
			"CONSEQUENCE: a sentinel nothing emits is either a rule that was removed without its name, or "+
			"a name minted for a check that was never written. A consumer can match against it forever "+
			"and never see it.\n"+
			"REMEDY: raise it from the check that owns the rule; or, if it is deliberately declared ahead "+
			"of the work that will raise it, say so on its own registration with a DeclaredAhead note, "+
			"which is where that fact belongs and where the published reference reads it; or delete the "+
			"declaration. Adding the name to knownUnemittedSentinels is none of those — that list is a "+
			"bridge for conditions registered before the note existed, and its own comment says so.",
			len(fresh), strings.Join(namedList(fresh, 12), ", "))
	}

	if len(stale) > 0 {
		report("the recorded baseline names %d sentinel(s) that DO have an emission site now, or that no "+
			"longer exist: %s.\n"+
			"A stale entry is worse than none: it reads as a known hole where there is none, and it "+
			"exempts that name from this assertion for as long as it stands.\n"+
			"REMEDY: remove the entry from knownUnemittedSentinels. That removal is part of the change "+
			"that wired or deleted the sentinel, not a follow-up to it.",
			len(stale), strings.Join(namedList(stale, 12), ", "))
	}

	tier := "reports and does not block"
	if unemittedSentinelsBlock {
		tier = "blocks"
	}
	t.Logf("no-emission-site population: %d sentinel(s) carry no emission site, %d recorded as a known "+
		"baseline, %d not in that baseline, %d baseline entry/entries no longer applying. This assertion "+
		"%s on either of the last two.",
		len(unemitted), len(baseline), len(fresh), len(stale), tier)
}

// TestAssertedSentinelPopulationCoversTheModule asserts that [scannedRoots]
// reaches every sentinel declaration in the module, so the population every other
// count in this file is taken over is the whole population rather than whatever
// the root list happens to name.
//
// This is the completeness half of the census, and it exists because the failure
// it catches is silent in both directions. A sentinel declared in a tree no root
// reaches is not reported as missing — it is not reported at all, and the census
// prints a clean population while saying nothing about the file it never opened.
// The reverse reading is worse: a reader comparing two counts taken under two
// root lists sees the population move and concludes sentinels were added or
// removed, when the criterion changed underneath them.
//
// # Why this one blocks on its whole population
//
// [offFrameworkEmissionBlocks] gates the off-framework population because that
// population fails on the shipped tree today: the migration it measures is under
// way and a blocking form would be switched off rather than fixed. Coverage
// carries no such backlog — the roots cover the module, so this assertion passes
// — and there is nothing for a grace period to protect. It therefore fails
// rather than reports, which matters more here than elsewhere in this file: a
// passing Go test's log output is discarded unless the run asks for verbose
// output, so an assertion that only logs is an assertion whose finding no build
// mode shows. That is the same reasoning behind
// [offFrameworkEmissionCeiling] and [unemittedSentinelsBlock]: where a
// population cannot block outright, something about it still has to.
//
// The remedy when it fails is one of two edits, and the failure names both: add
// the tree to [scannedRoots] if its sentinels belong to the asserted population,
// or move the declaration into a tree already listed. Neither is a suppression —
// both make the population say what it means.
func TestAssertedSentinelPopulationCoversTheModule(t *testing.T) {
	t.Parallel()

	assertedFiles, _ := collectGoFiles(t, scannedRoots, excludeTestFiles)
	asserted := declaredSentinels(assertedFiles)
	if len(asserted) == 0 {
		t.Fatalf("no sentinel declaration was found under %s, so a clean coverage result certifies nothing",
			strings.Join(scannedRoots, ", "))
	}

	moduleFiles, _ := collectGoFiles(t, []string{moduleRoot}, excludeTestFiles)
	moduleWide := declaredSentinels(moduleFiles)

	var unreadFiles []string
	for path := range moduleFiles {
		if _, ok := assertedFiles[path]; !ok {
			unreadFiles = append(unreadFiles, filepath.ToSlash(path))
		}
	}
	sort.Strings(unreadFiles)

	var escaped []string
	for name, path := range moduleWide {
		if _, ok := asserted[name]; !ok {
			escaped = append(escaped, name+" at "+filepath.ToSlash(path))
		}
	}
	sort.Strings(escaped)

	t.Logf("sentinel population coverage: %d sentinels over %d files under the asserted roots (%s); "+
		"%d sentinels over %d files read across the whole module (%s). This assertion blocks: unlike the "+
		"emission assertion in this file, coverage carries no backlog to grow out of.",
		len(asserted), len(assertedFiles), strings.Join(scannedRoots, ", "),
		len(moduleWide), len(moduleFiles), moduleRoot)

	if len(escaped) > 0 {
		t.Errorf("%d sentinel(s) are declared in this module but sit outside the asserted population.\n"+
			"CONSEQUENCE: no count in this file describes them. They are not reported as missing an "+
			"emission site, not reported as emitted outside the framework's dispatch, and not counted in "+
			"the declared total — the census reads a population that does not include them and prints a "+
			"clean result.\n"+
			"REMEDY: add the tree to scannedRoots if these sentinels belong to the asserted population, or "+
			"move the declaration into a tree already listed.\n"+
			"Alphabetically: %s", len(escaped), strings.Join(namedList(escaped, 12), "; "))
	}

	// The name-level finding above cannot see a declaration whose name is ALSO
	// declared inside the roots: the population is a set of names, so a duplicate
	// name is not absent from it while its file still goes unread. Asserting the
	// file set closes that hole and makes the escape impossible rather than merely
	// detected.
	if len(unreadFiles) > 0 {
		t.Errorf("%d non-test Go file(s) in this module sit under no asserted root, so any sentinel they "+
			"declare is outside every count this file reports.\n"+
			"This fails even when those files declare no sentinel today, deliberately: the tree is where "+
			"the next one lands, and a root added after the fact does not retroactively make the "+
			"intervening counts true.\n"+
			"REMEDY: add the tree to scannedRoots, or exclude it through the documented criterion if its "+
			"Go belongs to another module.\n"+
			"Alphabetically: %s", len(unreadFiles), strings.Join(namedList(unreadFiles, 12), ", "))
	}
}

// emissionSite is one place a sentinel is named, and whether that place is the
// framework's dispatch. Membership travels with the site rather than being
// recomputed from its name, because the two kinds of site establish it
// differently: a declared function by its package, a check declaration by what
// it constructs.
type emissionSite struct {
	name      string
	framework bool
}

// sentinelCensus returns the declared sentinel names, sorted, the sites naming
// each one, and, for each sentinel whose own declaration names another sentinel,
// the sentinels that declaration names.
//
// The third result answers a different question from the second and is kept
// apart from it deliberately. The sites say where a rule is raised; the
// forwarding relation says what a declaration IS. Folding a forwarding
// declaration into the site map would make a name that raises nothing count as
// emitted, which is the one thing the emission counts must not say.
//
// Two shapes count as a site. A non-test declared function whose body names a
// sentinel identifier — the criterion the covering measurement used — and a
// package-level [Check] declaration whose value expression names one. Both count
// the site rather than the occurrence, so naming one sentinel twice is one site,
// and both are an upper bound on checks: a function that wraps a sentinel while
// re-raising it counts as naming it.
//
// A check declaration is walked whole, from the composite literal down, rather
// than only at the function literal holding the predicate. A check names its
// sentinel on the value's own field as well as inside the predicate, so a walk
// scoped to the predicate would see the second and miss the first — and would
// break outright on a check that names its sentinel only on the field. Walking
// the value expression also needs no knowledge of which field holds the
// predicate, so the field set can grow without the census following it.
//
// The walk stops at the check shape and does not extend to package-level
// variables in general, which is a deliberate bound rather than an oversight. A
// sentinel is routinely named in the declaration of another sentinel that wraps
// it, and in diagnostic tables keyed by sentinel; counting those as emission
// sites would answer a different question than the one this assertion asks, and
// would reclassify sentinels on the strength of a declaration that raises
// nothing.
//
// The value walk below admits a var declaration and not a const one, which is
// where this reading is deliberately NARROWER than [declaredSentinels] beside it,
// and the difference is a property of the shape being looked for rather than an
// oversight to reconcile. This walk attributes a package-level check declaration
// to the framework's dispatch, and a check is a composite literal, which cannot
// be a Go constant. So a const specification can bind a sentinel — which is why
// the population walk reads both — and can never hold a check, which is why
// reading it here would only widen the walk to specifications that cannot match.
func sentinelCensus(t *testing.T) (declared []string, emitters map[string][]emissionSite, forwards map[string][]string) {
	t.Helper()

	files, _ := collectGoFiles(t, scannedRoots, excludeTestFiles)
	names := declaredSentinels(files)
	forwards = forwardingDeclarations(files, names)

	emitters = map[string][]emissionSite{}
	for path, file := range files {
		dir := filepath.ToSlash(filepath.Dir(path))
		inFrameworkPackage := strings.Contains(dir, frameworkPackageSuffix)
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body == nil {
					continue
				}
				recordNamed(emitters, names, emissionSite{
					name:      dir + "." + d.Name.Name,
					framework: inFrameworkPackage,
				}, d.Body)
			case *ast.GenDecl:
				if d.Tok != token.VAR {
					continue
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, value := range vs.Values {
						if i >= len(vs.Names) || !isCheckValue(value) {
							continue
						}
						recordNamed(emitters, names, emissionSite{
							name:      dir + "." + vs.Names[i].Name,
							framework: true,
						}, value)
					}
				}
			}
		}
	}

	declared = make([]string, 0, len(names))
	for name := range names {
		declared = append(declared, name)
	}
	sort.Strings(declared)
	for name := range emitters {
		sites := emitters[name]
		sort.Slice(sites, func(a, b int) bool { return sites[a].name < sites[b].name })
	}
	return declared, emitters, forwards
}

// forwardingDeclarations returns, for each declared sentinel whose own
// declaration names one or more other declared sentinels, the names it names.
//
// It reads the same file set and the same population as the census, so there is
// no second criterion for what a sentinel is: the population arrives as an
// argument and this only classifies its members. What it adds is a question
// about the declaration rather than about a rule — does this name stand for an
// identity declared elsewhere in this population?
//
// Both declaration shapes are read, the function that returns another sentinel
// and the value bound to one, because a re-export takes either form and reading
// one alone would leave the other landing in the bucket for rules nothing
// raises. A declaration naming only unexported values — the accessor shape that
// publishes an identity registered beside it — names no sentinel and is
// correctly not forwarding.
//
// It records no emission site and is deliberately kept out of that walk. A
// forwarding declaration raises nothing; treating the declaration as a site
// would inflate the emission counts with names that emit nothing, which is
// exactly the misreading this classification exists to remove.
func forwardingDeclarations(files map[string]*ast.File, names map[string]string) map[string][]string {
	forwards := map[string][]string{}
	record := func(declared string, subtree ast.Node) {
		if _, isSentinel := names[declared]; !isSentinel {
			return
		}
		var targets []string
		for name := range namesIn(names, subtree) {
			// A declaration naming itself is recursion or a doc reference, not
			// a forwarding relation, and admitting it would let any sentinel
			// forward to itself out of the bucket it belongs in.
			if name == declared {
				continue
			}
			targets = append(targets, name)
		}
		if len(targets) == 0 {
			return
		}
		sort.Strings(targets)
		forwards[declared] = targets
	}

	for _, file := range files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body == nil || d.Recv != nil {
					continue
				}
				record(d.Name.Name, d.Body)
			case *ast.GenDecl:
				if d.Tok != token.VAR && d.Tok != token.CONST {
					continue
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, value := range vs.Values {
						if i >= len(vs.Names) {
							continue
						}
						record(vs.Names[i].Name, value)
					}
				}
			}
		}
	}
	return forwards
}

// testFileScope states whether a walk admits _test.go files.
//
// It is a named type rather than a bare bool so the choice reads at the call
// site. The populations this package walks differ in exactly this one respect,
// and a literal true or false in the argument list would say which population is
// meant only to a reader who first opens [collectGoFiles].
type testFileScope bool

const (
	// excludeTestFiles is the scope of every guard whose subject is what this
	// module SHIPS: the sentinel census, the severity-literal guard and the
	// code-literal guard. A sentinel declared in a test is scaffolding rather
	// than a rule this module raises, which is the criterion recorded beside
	// [offFrameworkEmissionBlocks]; the code-literal guard's own reason for
	// excluding a test is different and is recorded beside
	// [codeLiteralGuardBlocks].
	excludeTestFiles testFileScope = false

	// includeTestFiles is the rule-ordinal guard's scope. That guard's subject is
	// a bare ordinal written into a Rule field, which is equally wrong in a test
	// as in production: the field is the same field, and a test writing one is
	// pinning the ordinal it happens to hold today.
	includeTestFiles testFileScope = true
)

// collectGoFiles parses every file the criterion admits under roots, keyed by
// path, and returns the file set those positions are recorded in so a caller can
// report a finding by line.
//
// It is one half of the criterion's single home — the scope half. Both the
// asserted-population read and the module-wide coverage read pass the identical
// scope, so neither of them can acquire a scope rule the other lacks; a caller
// whose population is genuinely different states that difference in the
// argument, where it is visible, rather than by walking the tree a second time.
//
// A file that will not parse is skipped rather than fatal, because a census is
// not a compiler and a tree mid-edit should not turn an unrelated assertion red.
func collectGoFiles(t *testing.T, roots []string, tests testFileScope) (map[string]*ast.File, *token.FileSet) {
	t.Helper()

	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.IsDir():
				if skipDir(path, root, d.Name()) {
					return fs.SkipDir
				}
				return nil
			case !strings.HasSuffix(path, ".go"):
				return nil
			case tests == excludeTestFiles && strings.HasSuffix(path, "_test.go"):
				return nil
			}
			parsed, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return nil // a file this census cannot parse is skipped, not fatal
			}
			files[path] = parsed
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if len(files) == 0 {
		t.Fatalf("no Go file was read under %s; the census instrument is not working",
			strings.Join(roots, ", "))
	}
	return files, fset
}

// skipDir reports whether the walk stops at a directory. The exclusions are the
// scope half of the census criterion and each is documented with its reason
// beside [offFrameworkEmissionBlocks]; a root is never skipped for its own sake,
// which is what keeps a root spelled with a leading ".." from matching the
// dot-prefix rule and emptying the walk.
func skipDir(path, root, name string) bool {
	if path == root {
		return false
	}
	if name == "testdata" || name == "vendor" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}
	_, err := os.Stat(filepath.Join(path, "go.mod"))
	return err == nil
}

// declaredSentinels returns each declared sentinel name and one path declaring
// it. It is the other half of the criterion's single home — the name half — so
// the asserted population and the module-wide coverage read cannot drift into two
// criteria that happen to agree today.
//
// Both declaration shapes are read: the exported accessor that publishes an
// identity, and a var or const bound to one. A walk reading only the second
// would report a population of the handful of sentinels still declared as values
// and say nothing about the rest — and it would report it as a clean count
// rather than as the blindness it is, which is exactly the reading a population
// count must never produce.
//
// Where a name is declared more than once the lexicographically first path wins,
// so a reported path is the same on every run rather than whichever the map
// handed back first.
//
// A const specification is admitted here and is NOT admitted by the emission walk
// in [sentinelCensus], and the asymmetry is deliberate at both ends rather than
// one of the two drifting. This reading asks which names exist, and a sentinel is
// the distinct exported name a consumer matches against however the declaration
// binds it — so excluding a const would make this a count of one declaration
// shape rather than of the rules this module raises. That reading asks which
// declarations construct a check, and a check is a composite literal, which no Go
// constant can hold. Narrowing this to match it would drop every const-bound
// sentinel from the population; widening that one to match this would add
// specifications it can never attribute anything from.
//
// A second recogniser of the same declaration shapes lives in the published-pages
// parity assertion under cmd/aggrega, and the two are deliberately NOT unified.
// This one parses the syntax tree; that one scans source text. They fail in
// different directions, which is the whole value: a text scan reads a shape this
// walk cannot express, and this walk reads a shape no pattern can bound. Unifying
// them would replace two independent readings of one population with a single
// point of blindness, so a criterion change must be carried to both sites by hand
// — which is the cost this duplication is knowingly paying.
func declaredSentinels(files map[string]*ast.File) map[string]string {
	names := map[string]string{}
	record := func(name, path string) {
		if !sentinelName.MatchString(name) {
			return
		}
		if prior, seen := names[name]; !seen || path < prior {
			names[name] = path
		}
	}
	for path, file := range files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				// A method is reached through a value rather than through the
				// package, so its name is not one a consumer matches against.
				if d.Recv != nil {
					continue
				}
				record(d.Name.Name, path)
			case *ast.GenDecl:
				if d.Tok != token.VAR && d.Tok != token.CONST {
					continue
				}
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, id := range vs.Names {
						record(id.Name, path)
					}
				}
			}
		}
	}
	return names
}

// namesIn returns the declared sentinels a subtree names, each once however
// often it appears.
//
// It is the single home of "which sentinels does this piece of syntax name",
// asked by the two readings that need it — where a rule is raised, and what a
// declaration forwards to. Those are different questions over the same scan, and
// a second copy of the scan would be free to drift into recognising a spelling
// the other does not: a qualified name read by one and not the other would move
// a sentinel between buckets for no reason a reader could find.
func namesIn(names map[string]string, subtree ast.Node) map[string]bool {
	named := map[string]bool{}
	ast.Inspect(subtree, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.Ident:
			if _, ok := names[x.Name]; ok {
				named[x.Name] = true
			}
		case *ast.SelectorExpr:
			if _, ok := names[x.Sel.Name]; ok {
				named[x.Sel.Name] = true
			}
		}
		return true
	})
	return named
}

// recordNamed attributes site to every declared sentinel the subtree names.
func recordNamed(emitters map[string][]emissionSite, names map[string]string, site emissionSite, subtree ast.Node) {
	for name := range namesIn(names, subtree) {
		emitters[name] = append(emitters[name], site)
	}
}

// namedWithSites renders each sentinel beside the sites naming it, so a reader
// can reach the declaration an attribution came from. A check declaration's site
// carries the variable's own name, which is what a reader greps for; a declared
// function's carries the function name.
func namedWithSites(sentinels []string, emitters map[string][]emissionSite) []string {
	out := make([]string, 0, len(sentinels))
	for _, sentinel := range sentinels {
		sites := make([]string, 0, len(emitters[sentinel]))
		for _, site := range emitters[sentinel] {
			sites = append(sites, site.name)
		}
		out = append(out, sentinel+" at "+strings.Join(sites, " and "))
	}
	return out
}

// namedWithForwards renders each sentinel beside the sentinel or sentinels its
// declaration names, so a reader can reach the rule the forwarding name carries
// and confirm the attribution instead of taking it. It is the forwarding bucket's
// counterpart to [namedWithSites], which does the same for an emission site.
func namedWithForwards(sentinels []string, forwards map[string][]string) []string {
	out := make([]string, 0, len(sentinels))
	for _, sentinel := range sentinels {
		out = append(out, sentinel+" carries "+strings.Join(forwards[sentinel], " and "))
	}
	return out
}

// isCheckValue reports whether expr constructs a [Check].
func isCheckValue(expr ast.Expr) bool {
	lit, ok := expr.(*ast.CompositeLit)
	return ok && constructedTypeName(lit.Type) == checkTypeName
}

// constructedTypeName names the type a composite literal constructs, unwrapping
// a generic instantiation and a package qualifier. It reads syntax alone, so it
// answers for the framework's own unqualified spelling and for a consuming
// package's qualified one without either being enumerated.
func constructedTypeName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.IndexExpr: // instantiated with one type argument
		return constructedTypeName(x.X)
	case *ast.IndexListExpr: // instantiated with two or more
		return constructedTypeName(x.X)
	case *ast.SelectorExpr:
		return x.Sel.Name
	case *ast.Ident:
		return x.Name
	}
	return ""
}

// sortedNames returns ss in alphabetical order, leaving ss itself untouched, so
// a report names the same entries in the same order on every run.
func sortedNames(ss []string) []string {
	sorted := append([]string(nil), ss...)
	sort.Strings(sorted)
	return sorted
}

// namedList renders a bucket for a report: every entry alphabetically where the
// bucket fits within n, and otherwise the first n followed by a statement of how
// many were withheld.
//
// The withheld count is the point, and it is not presentation. A report that
// names a category and then silently drops a member of it cannot be checked
// against the tree at all: a reader comparing the named entries with the code
// finds them all accounted for and concludes the category is understood, while
// the member that was cut is the one nothing else will ever mention. Stating the
// remainder makes the difference between a complete list and a capped one
// readable from the message alone, without the reader knowing that a cap exists
// or what it is set to — so a bucket that grows past the cap announces the
// change rather than absorbing it.
//
// A count alone in the introducing sentence is not enough on its own, because it
// leaves the reader to notice the arithmetic; this states the remainder where
// the list ends, which is where a reader stops.
func namedList(ss []string, n int) []string {
	sorted := sortedNames(ss)
	if len(sorted) <= n {
		return sorted
	}
	return append(sorted[:n:n], fmt.Sprintf("and %d more not named", len(sorted)-n))
}
