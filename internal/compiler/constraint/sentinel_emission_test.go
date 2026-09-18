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
	"strconv"
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
// function bodies and the package-level value declarations — and never from a
// hand-maintained inventory: that inventory marks its own coordinates
// approximate and asks to be verified against the code, so it disclaims exactly
// the precision an assertion needs. It is a useful shape template for which fields matter and an unusable
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
// The no-emission-site bucket DOES block, gated by [unemittedSentinelsBlock],
// and its population is empty, so any sentinel arriving without an emission
// site fails outright. There is nothing for a grace period to protect: such a
// sentinel is a rule removed without its name, or a name minted for a check
// never written, and either is wrong at the moment it lands rather than
// scheduled to be fixed. Where a condition is deliberately declared ahead of
// the work that will raise it, that fact belongs on its own registration as a
// declared-ahead note, which is where the published reference reads it — not in
// a side list here, which would be a second home for a correspondence that
// travels with the identity.
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
// standing in for. Every other host is still read by its path: a plain declared
// function, and a package-level value that is not a check, belong to the
// dispatch when written inside this package and not when written anywhere else.
// So expressing a rule as a check is what moves it, and no rule is amnestied by
// where its file sits — least of all by being assembled into a value instead of
// written inline, which is the same rule at the same address wearing a
// different shape.
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
// # The emission side reads two spellings, because a raise has two
//
// The declaration side above matches [sentinelName], and the emission side
// cannot be read the same way. A raise names the identity, and inside the
// package that declares it the identity is the package-local binding the
// registration produces — the exported accessor yields a plain error, which the
// constructor a raise goes through does not accept, so the accessor is not
// written at a raise site in its own package at all. A census matching only the
// exported spelling therefore sees a declaration and no raise for every
// condition raised where it is declared, and reports it as a rule nothing
// raises.
//
// So the emission walk reads the exported name AND the package-local binding
// each accessor publishes, the binding being READ FROM THE ACCESSOR'S OWN
// DECLARATION by [accessorHandles] rather than inferred from a lowercase-twin
// spelling. This is not a widening of what counts as a raise: it is the same
// question asked of the shape the constructor now produces.
//
// Two occurrences of a binding are excluded, and each exclusion is what keeps
// the instrument able to report anything at all. An accessor's own declaration
// names the binding it publishes — crediting it empties the no-emission-site
// bucket permanently while every count still reads as clean. And a value
// specification's NAMES are never walked, only its values, because a
// registration binds the very name the accessor publishes and reading the name
// side would credit every registration with raising itself. Both are recorded at
// the walk in [sentinelCensus], beside the code that performs them.
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
//
// # The off-framework bucket is partitioned, and only one half is ratcheted
//
// The ratchet exists to refuse a REGRESSION: a rule that could sit inside the
// framework's dispatch escaping it, or a new rule written outside it that
// belongs in. A rule that could never have been inside it is not a regression,
// and counting it against the same bound makes the ceiling refuse the product's
// own growth — every legitimately elsewhere-evaluated rule added from then on
// trips it, and the pressure to raise the number arrives again each time with a
// weaker reason than the last.
//
// So the off-framework bucket answers two questions that read alike and are not
// the same: CANNOT be a check, and IS NOT YET one. Only the second is ratcheted.
//
// The distinguishing fact is DECLARED rather than derived, because no property
// of the declaring package can see it. A package that holds an identity while
// something else evaluates the rule — a plugin guest, or a verdict taken at
// publication — is the same kind of package as one holding a rule nobody has
// moved into the dispatch yet, and a discriminator over package structure sorts
// the two together whichever structure it reads. The fact is therefore declared
// where it is known: on the registration, as the errors package's RulePlace
// metadata, which names which of four places the rule belongs to and carries
// the reason where the place needs one. This census reads it off the
// declaration's own syntax.
//
// Criterion, so that a later reading measures the same thing: a declared
// sentinel is UNRATCHETED when the census places it in the off-framework
// population AND the registration binding the value its accessor publishes
// declares a RulePlace naming another evaluator, a moment no declared stage
// reaches, or no rule at all. That
// declaration is read by [readPlacementDeclarations], over the same file set
// and the same population every other count in this file is taken over. The
// ratcheted count is the off-framework population minus those. No other figure
// this file reports is changed by the partition.
//
// # The declaration is guarded in both directions, and what no guard can see
//
// [partitionOffFrameworkByDeclaredPlace] fails on a declaration the tree
// CONTRADICTS — a condition carrying one that the census nonetheless attributes
// to the framework's dispatch — and on one that DESCRIBES NOTHING: a
// declaration the census cannot place in the off-framework population at all.
// The second is what keeps this from being the recorded exception list the
// no-emission-site population refuses. Such a list outlives its subject
// silently; a declaration whose subject has been renamed, deleted or never
// reached the population reddens instead.
//
// What no guard here can see is a plausible declaration on a rule that could in
// fact have been written as a check today. That is a question about what the
// rule READS, and this census resolves no types by design — a type-resolving
// census is a different instrument with a different cost, which is the bound
// [checkTypeName] already records. What costs something instead is the
// declaration: it is prose a reviewer can hold against the code, a blank value
// is refused at registration, and the field cannot be acquired by neglect.
// Those are a cost, not a proof, and the difference is stated here rather than
// left to be inferred.
//
// # A second ceiling over the unratcheted half is REFUSED
//
// Recorded so that nobody adds one later as a hardening improvement. That half
// is EXPECTED to grow as the product adds legitimately elsewhere-evaluated
// rules, which is the whole reason the partition exists, so a bound on it would
// recreate one indirection later the pressure the partition removes.
//
// What the half gets instead is naming: every member is reported beside the
// declaration it was attributed from, on every run, for the same reason every
// other bucket here is named — a partition whose unratcheted half is invisible
// has retired the guard while reading as a repair. That naming carries the same
// weak channel every log line in this file carries, since a passing package
// prints nothing, so it informs a reader running this package verbosely and is
// not what holds the partition honest. The two guards are.
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
// Criterion, so that a later reading measures the same thing: the RATCHETED half
// of the off-framework population. That is the number of distinct sentinel names,
// matching [sentinelName] and bound by a package-level declaration in a non-test
// .go file under [scannedRoots], for which the census attributes at least one
// emission site outside the framework's dispatch — attribution exactly as
// [checkTypeName] and [frameworkPackageSuffix] decide it, over the host shapes
// and the inner criterion [sentinelCensus] records — LESS the ones whose
// registration declares that another evaluator owns the rule, that no declared
// stage of a compilation reaches it, or that the condition names no rule at
// all. [partitionOffFrameworkByDeclaredPlace] performs
// that subtraction and guards those declarations in both directions.
//
// Measured 2026-09-18, on the tree the revision carrying this edit records: 88,
// of 282 declared. Re-derive it by running this package verbosely — the census
// prints every term below, so the figure is checkable rather than remembered.
//
// Its composition, quoted from that run rather than recomputed, because a bare
// number cannot carry how the figure is read: 229 of the 282 declared conditions
// are emitted outside the dispatch, and 141 of those declare that their rule is
// not one of the framework's own — 32 owned by another evaluator, 36 reached by
// no declared stage, 73 naming no rule at all — leaving the 88 this ceiling
// bounds. What remains is the candidates, each of which could be expressed as a
// check, plus the conditions carrying nothing to declare a place ON: the 6
// forwarding declarations and the 5 conditions no registration binds, which
// [assertEveryConditionDeclaresItsPlace] names on every run and which together
// cap that second term at 11. Those are counted here rather than subtracted,
// because a condition with no declaration has an UNKNOWN place rather than a
// place outside the dispatch, and the safe direction for an unknown is to count
// it. Subtracting them would let a new unregistered condition raised outside the
// dispatch escape this bound entirely, which is the one movement the bound
// exists to refuse.
//
// # One recording here is not comparable with the rest
//
// 241 was taken over a DIFFERENT population and must never be read as a trend
// with any figure recorded since. No rule moved when it fell: the change that
// lowered it migrated nothing and altered no raise path. What changed is the
// population the number is taken over. That older one was MIXED — every condition
// raised outside the dispatch, counted together whether its rule could be
// expressed as a check or could never be one — and the present one is the
// conditions whose rules could be, plus the few that have not said.
//
// Every recording from 154 onward is taken over the present population under the
// criterion above, so those ARE comparable with one another. A fall among them is
// still not rules moving into the dispatch: it is conditions declaring a place
// the ratchet excludes — another evaluator owns the rule, no declared stage
// reaches it, or it is no rule at all — plus whatever slack the previous
// recording was carrying. Which of the two a particular fall was is answered by
// the census output at that revision, and is deliberately not narrated here: a
// running account of each fall is a second copy of what the census computes, and
// it would need an edit every time the number moves.
//
// The slack the old recording carried is reclaimed by this one rather than left
// standing, so the ratchet is exact again at the moment it is recorded. The bound
// that leaves, recorded so the ceiling is never read as the population: a FALL is
// deliberately not failed, so once the count drops this constant carries slack the
// ratchet does not reclaim, and a regression inside that slack is invisible.
// Reclaiming it is a one-line edit here, belonging to whatever change lowers the
// count rather than to a later tidy-up.
//
// # The denominator can now reach zero, which is the point of the re-scoping
//
// A meter is honest only if the thing it measures can reach zero, and the old
// population could not. A large part of it was conditions no work moves inside the
// dispatch, because there is no rule there for the dispatch to evaluate — a
// registry refusing a request, a signature that does not verify, a file that will
// not parse, a guest that trapped. The meter's own reversing condition was
// unreachable, and every later reading re-opened the same question about the same
// conditions.
//
// Those are now declared and counted apart, so nothing in this population is here
// by construction. The candidates reach zero by being expressed as checks. The
// conditions no registration binds reach zero by acquiring a registration that
// declares where their rule belongs, or by being deleted — a finite set named on
// every run with a remedy per member, which is a different thing from a
// structural remainder. The split between the two terms is deliberately not given
// as a pair of figures: the census prints the second term's members and the total
// above, and never the intersection of that term with this population, so a pair
// here would be derived rather than quoted and would be wrong in a way no run
// contradicts. So zero is a state this number can arrive at, and
// [offFrameworkEmissionBlocks] flips in the edit that takes it there.
const offFrameworkEmissionCeiling = 88

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
// What makes blocking possible without reddening the shipped tree is that the
// population is empty: every declared sentinel is raised somewhere. So this
// assertion fails on any member at all, with no recorded exception to narrow
// the corpus and nothing standing between a new unraised sentinel and a red
// build.
//
// No such exception list exists, and one must not be reintroduced as the way of
// admitting a new unraised sentinel. A recorded exception outlives its subject
// silently — once the sentinel it names acquires a raise, the entry reads as a
// known hole where there is none and goes on exempting that name for as long as
// it stands — so it needs guarding in both directions, and the moment it
// describes nothing it is worse than absent. A condition genuinely declared
// ahead of the work that will raise it says so on its own registration, where
// the correspondence travels with the identity.
const unemittedSentinelsBlock = true

// undeclaredRulePlaceBlocks decides whether a condition that declares no place
// for its rule fails this test or is only reported. It is true.
//
// # What the declaration is for, and why the count above needs it
//
// The off-framework population is called a migration meter, and a meter is only
// honest if the thing it measures can reach zero. Part of that population never
// can: a registry refusing a request, a signature that does not verify, a file
// that will not parse, a guest that trapped, an invariant of this program's own
// construction. No work anyone does moves those inside the dispatch, because
// there is no rule there for the dispatch to evaluate. Counted together with the
// rules that are merely not expressed as checks yet, they make the meter's own
// reversing condition unreachable, and every later reading of it re-opens the
// same question about the same conditions.
//
// The separating fact cannot be derived — a package holding a condition that is
// no rule at all looks exactly like a package holding a rule nobody has moved
// yet — so it is declared at the registration, in the errors package's rule
// place, which says which of four places the rule belongs to and carries prose
// for the three that are a departure from the ordinary one.
//
// # What makes blocking possible without reddening the shipped tree
//
// The declared set is written: every condition carrying a registration declares
// a place, so the population this names is empty. That is what lets this fail on
// any member at all, with no recorded exception to narrow the corpus and nothing
// standing between a new condition declaring no place and a red build — the
// standing [unemittedSentinelsBlock] has over its own empty population, for the
// same reason.
//
// It is deliberately NOT the standing [offFrameworkEmissionBlocks] has. That
// population is a backlog the tree is working through, so a blocking form would
// fail on the shipped tree every run and be switched off rather than fixed. This
// one is not a backlog: a condition arriving without a place is wrong when it
// lands rather than scheduled to be fixed, because the answer is known to
// whoever registers it and to nobody else afterwards.
//
// # Why the gate rather than the compiler, which would be stronger
//
// The strongest available shape is a required positional argument on the errors
// package's Register — a condition omitting its place would then not compile and
// no assertion would be needed. That shape stays reachable and was not taken
// here, for a reason about sequencing rather than preference: it moves every
// registration call site in the module in one act, plus the fixture modules under
// testdata that compile on their own, and it would put that churn behind the same
// review as the classification of the conditions themselves. A disagreement about
// one would then block the other.
//
// What reaches it later is a mechanical edit with nothing left to decide, once
// every registration already carries a place — which is the state this gate now
// holds the tree in. The place moves from the metadata literal to an argument,
// the field goes away, and this assertion goes with it, because a population the
// compiler cannot express is a population nothing has to count. The two are not
// both needed: an argument that cannot be omitted makes this census's question
// unaskable rather than merely answered.
//
// # No exception list, and the one exclusion is DERIVED rather than recorded
//
// There is nothing here to record a permitted name in, and none should be added.
// A recorded exception outlives its subject silently: once the condition it names
// acquires a declaration the entry reads as a known hole where there is none.
//
// The population is narrower than every declared condition, and the narrowing is
// not such a list. A condition no registration binds carries no metadata literal
// to declare a place on, so there is nothing on it this gate could ask for — and
// that is read from the tree on every run by [readPlacementDeclarations] rather
// than written down anywhere. The difference is what makes it safe: the exclusion
// cannot come to describe nothing, because a condition acquiring a registration
// joins this population immediately and fails until it declares a place. Its
// members are named on every run by [assertEveryConditionDeclaresItsPlace], where
// the reasoning sits beside the code performing it.
//
// So the remedy a failure names is always to declare the place, never to record
// the name. Every condition IS in one of the four places; what a condition can
// lack is a registration to say so on, which is a different finding with a
// different remedy and is reported as one.
const undeclaredRulePlaceBlocks = true

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

// registrationFuncName is the function a condition's identity is declared by,
// and the metadata keys and constructor names below are the ones this census
// reads off that declaration.
//
// They are spelled here rather than at the walk so every string tying this file
// to the errors package's declaration shape sits in one place: a rename there is
// carried here once, and a reader can see at a glance how much of that shape
// this census depends on. It depends on no other part of it, and in particular
// on no other metadata key — an unrecognised key is skipped, so a field added
// beside these disturbs this walk not at all.
//
// The four constructor names are the errors package's four places, and they
// are read by NAME rather than resolved: the value a rule-place key is given is
// a call, and this census resolves no types, so what it reads is which function
// the call names and what literal it was handed.
const (
	registrationFuncName = "Register"
	declaredAheadMetaKey = "DeclaredAhead"
	rulePlaceMetaKey     = "RulePlace"

	placeOverAuthoredInputFunc  = "RuleOverAuthoredInput"
	placeEvaluatedElsewhereFunc = "RuleEvaluatedElsewhere"
	placeNoStageReachesFunc     = "RuleNoStageReaches"
	placeNotARuleFunc           = "NotARule"
)

func TestSentinelEmissionSitesAreTheFrameworkDispatch(t *testing.T) {
	t.Parallel()

	declared, declaredAt, emitters, forwards, placements := sentinelCensus(t)
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
			"construction; a plain declared function and any other package-level value are read by their "+
			"path instead. A function body is read for what it NAMES and a package-level value for what it "+
			"OBTAINS, so a rule raised from a diagnostic set assembled at package level is counted here "+
			"rather than reported as raised by nothing. Both are read under either spelling a raise takes: "+
			"the exported name, and the package-local binding the accessor publishes. That reads "+
			"declarations and cannot observe invocation, so a check declared and never invoked is "+
			"attributed to the dispatch too.\n"+
			"CONSEQUENCE while this population reports rather than blocks: an off-framework check gets no "+
			"computed placement, no caller-coverage guarantee and no framework-supplied provenance, and "+
			"nothing halts on its absence. The population is not free to grow, though — see the partition "+
			"and the ratchet below, which are read over the half of this population whose rules could be "+
			"expressed as %s values.\n"+
			"Alphabetically: %s", len(offFramework), checkTypeName,
			strings.Join(namedList(offFramework, 12), ", "))
	}

	ratcheted, unratcheted := partitionOffFrameworkByDeclaredPlace(t, offFramework, emitters, placements)
	t.Logf("off-framework partition: %d of the %d sentinels emitted outside the dispatch declare that "+
		"their rule is not one of the framework's own — another evaluator owns it, no declared "+
		"stage reaches it, or the condition names no rule at all — and are NOT ratcheted, "+
		"leaving %d ratcheted. The ceiling "+
		"below is read against the ratcheted count alone, so the off-framework total must never be "+
		"compared with it.", len(unratcheted), len(offFramework), len(ratcheted))
	if len(unratcheted) > 0 {
		t.Logf("not ratcheted, each named beside the declaration it was attributed from, so the "+
			"attribution can be checked rather than taken: %s.\n"+
			"Each of these carries prose on its own registration naming where its rule belongs and why "+
			"that place is not this dispatch. They are named on every run rather than counted, because "+
			"a partition whose unratcheted half is invisible has retired the guard while reading as a "+
			"repair.",
			strings.Join(namedList(namedWithPlacement(unratcheted, placements), 12), "; "))
	}

	assertOffFrameworkCeiling(t, len(ratcheted), len(declared))

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
	assertEveryConditionDeclaresItsPlace(t, declared, forwarding, declaredAt, placements)

	if !offFrameworkEmissionBlocks {
		t.Logf("the off-framework population reports and does not block on its size. It becomes blocking "+
			"when the last emission path outside the framework's dispatch is removed; until then it would "+
			"fail on the shipped tree on every run. What does block is the ceiling above, which refuses an "+
			"increase, and the no-emission-site population, which is empty and blocks on any member. "+
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
//
// What it is given is the RATCHETED half of the off-framework population, not
// the whole of it: a condition declaring that its rule is evaluated outside the
// dispatch by construction was never inside it, so its arrival is not the
// backwards movement this refuses. [partitionOffFrameworkByDeclaredPlace]
// performs that split and guards the declaration in both directions.
func assertOffFrameworkCeiling(t *testing.T, ratcheted, declared int) {
	t.Helper()

	switch {
	case ratcheted > offFrameworkEmissionCeiling:
		t.Errorf("%d ratcheted sentinel(s) are emitted outside the framework's dispatch, above the "+
			"recorded ceiling of %d.\n"+
			"INVARIANT: this population is a migration meter, so it may fall and may not rise.\n"+
			"CONSEQUENCE: a new off-framework emission path has been added, or a check was expressed "+
			"outside the framework. Either way the migration this figure measures moved backwards, and "+
			"the ceiling is what makes that loud — the population itself cannot block, so without this "+
			"the increase would reach a reader only under a verbose run.\n"+
			"REMEDY: express the rule as a %s value so it is attributed to the dispatch. Where the rule "+
			"is evaluated outside this dispatch by construction — inside a plugin guest, or over "+
			"artifacts at publication, so no view of a compilation exists for a %s to read — say so on "+
			"its own registration with a RulePlace value, which moves it out of this count and "+
			"is guarded in both directions rather than exempting it. Raising the ceiling is neither, and "+
			"is the one edit here that removes the guarantee: the number is only meaningful as a bound "+
			"the tree has never exceeded.",
			ratcheted, offFrameworkEmissionCeiling, checkTypeName, checkTypeName)
	case ratcheted < offFrameworkEmissionCeiling:
		t.Logf("ratcheted off-framework population is %d of %d declared, below the recorded ceiling of "+
			"%d. The ratchet refuses an increase and does not refuse a fall, so this slack of %d is not "+
			"reclaimed until offFrameworkEmissionCeiling is lowered — a one-line edit belonging to "+
			"whatever change lowered the count. Within that slack a regression is not detected.",
			ratcheted, declared, offFrameworkEmissionCeiling, offFrameworkEmissionCeiling-ratcheted)
	default:
		t.Logf("ratcheted off-framework population is %d of %d declared, exactly at the recorded ceiling. "+
			"The ratchet refuses an increase.", ratcheted, declared)
	}
}

// assertUnemittedSentinels blocks on any sentinel the census finds no emission
// site for.
//
// The population is empty, so there is no recorded set of permitted names and
// no narrowing of the corpus — the reasoning is at [unemittedSentinelsBlock].
// The remedy a failure names is therefore never "record it here": a condition
// deliberately declared ahead of the work that will raise it says so on its own
// registration, where the correspondence travels with the identity and the
// published reference reads it.
func assertUnemittedSentinels(t *testing.T, unemitted []string) {
	t.Helper()

	report := t.Logf
	if unemittedSentinelsBlock {
		report = t.Errorf
	}

	if len(unemitted) > 0 {
		report("%d sentinel(s) have no emission site at all: %s.\n"+
			"INVARIANT: every declared sentinel is raised somewhere, or says on its own registration that "+
			"it is declared ahead of the work that will raise it.\n"+
			"CONSEQUENCE: a sentinel nothing emits is either a rule that was removed without its name, or "+
			"a name minted for a check that was never written. A consumer can match against it forever "+
			"and never see it.\n"+
			"ATTRIBUTION: a raise is read from a declared function body that names the sentinel and from a "+
			"package-level value that obtains it, under either spelling a raise takes — the exported name, "+
			"and the package-local binding the accessor publishes, which is the only spelling available "+
			"inside the declaring package. An accessor's own declaration is excluded, or every "+
			"accessor-published sentinel would read as raised by the declaration that publishes it and this "+
			"list could never have a member. A sentinel reached only as a function VALUE — an entry in a "+
			"dispatch table something else calls — is not credited and lands here. That is the "+
			"under-attributing direction on purpose: it is a false alarm somebody investigates rather than "+
			"an unraised condition reported as raised.\n"+
			"REMEDY: raise it from the check that owns the rule; or, if it is deliberately declared ahead "+
			"of the work that will raise it, say so on its own registration with a DeclaredAhead note, "+
			"which is where that fact belongs and where the published reference reads it; or delete the "+
			"declaration. Recording the name in a list here is none of those, and no such list exists.",
			len(unemitted), strings.Join(namedList(unemitted, 12), ", "))
	}

	tier := "reports and does not block"
	if unemittedSentinelsBlock {
		tier = "blocks"
	}
	t.Logf("no-emission-site population: %d sentinel(s) carry no emission site, against a population "+
		"asserted to be empty with no recorded exception. This assertion %s on any member.",
		len(unemitted), tier)
}

// assertEveryConditionDeclaresItsPlace reports, and eventually fails on, a
// condition whose registration does not say where the rule it names belongs.
//
// The reasoning for the tier, for the flip, and for why no exception list exists
// is at [undeclaredRulePlaceBlocks].
//
// # The population, stated so a later reading measures the same thing
//
// Every declared sentinel THAT CARRIES A REGISTRATION. Two kinds of declaration
// are outside it, and both are outside for one reason: there is no registration
// on them to declare a place on.
//
//   - A forwarding declaration. It is a second exported name for a rule declared
//     elsewhere, and the place of the rule it names is declared at the
//     registration it forwards to.
//   - A declaration no registration binds at all — a sentinel built by another
//     package's constructor, or bound straight to a plain Go error value. There
//     is no metadata literal in it to carry a place.
//
// Counting either would put a permanent floor under a population that is
// asserted to reach zero, which is the reading a meter must never produce.
//
// # The second exclusion is DERIVED, which is what keeps it from being a list
//
// The population this file refuses to hold anywhere is a recorded set of
// permitted names, for the reason [unemittedSentinelsBlock] records: such a set
// outlives its subject silently. This exclusion is not one. It is read from the
// tree on every run by [readPlacementDeclarations], from the absence of a
// registration, so it is guarded in both directions without anything guarding
// it. A condition that acquires a registration joins the population on the next
// run and fails until it declares a place; a condition that is deleted leaves
// the population and the report together. Neither movement needs anybody to
// remember this report exists.
//
// What it costs is a coverage bound, and the bound is stated rather than
// implied: the members are NAMED on every run, beside the file declaring each,
// so a reader can open the declaration and confirm that no registration is there
// to carry a place. A population whose excluded half is invisible has narrowed
// the corpus while reading as full coverage.
//
// # What a declaration is, here
//
// A rule place naming one of the four constructors, read off the registration
// by [readPlacementDeclarations].
func assertEveryConditionDeclaresItsPlace(t *testing.T, declared, forwarding []string, declaredAt map[string]string, declarations placementDeclarations) {
	t.Helper()

	isForwarding := map[string]bool{}
	for _, name := range forwarding {
		isForwarding[name] = true
	}

	var undeclared, unregistered []string
	byPlace := map[string]int{}
	for _, name := range declared {
		if isForwarding[name] {
			continue
		}
		if !declarations.registered[name] {
			unregistered = append(unregistered, name+" declared in "+filepath.ToSlash(declaredAt[name]))
			continue
		}
		decl, found := declarations.bySentinel[name]
		if !found {
			undeclared = append(undeclared, name)
			continue
		}
		byPlace[decl.placeFunc]++
	}

	report := t.Logf
	if undeclaredRulePlaceBlocks {
		report = t.Errorf
	}

	if len(undeclared) > 0 {
		report("%d condition(s) declare no place for the rule they name.\n"+
			"INVARIANT: every condition's rule is in exactly one of four places — a rule over authored "+
			"input, a rule another evaluator owns, a rule no stage reaches, or no rule at all — and "+
			"which one is declared at the registration, because no property of the declaring package "+
			"can tell them apart.\n"+
			"CONSEQUENCE: a condition declaring none is counted with the rules not yet expressed as %s "+
			"values whether or not it could ever be one. The count that measures how much of that "+
			"migration remains then cannot reach zero, and every later reading re-opens the same question "+
			"about the same conditions — which is the cost this declaration exists to end, not a tidiness "+
			"concern.\n"+
			"REMEDY: declare the place on the registration. The three places that are a departure from a "+
			"rule over authored input carry prose saying why, and that prose is what makes a condition "+
			"settled: a later reader meets the reason instead of the question. Recording the name in a "+
			"list here is not a remedy, and no such list exists.\n"+
			"Alphabetically: %s", len(undeclared), checkTypeName,
			strings.Join(namedList(undeclared, 12), ", "))
	}

	if len(unregistered) > 0 {
		t.Logf("%d condition(s) sit outside this population because no registration in this module binds "+
			"the value their declaration publishes, each named beside the file declaring it so the "+
			"exclusion can be checked rather than taken: %s.\n"+
			"There is no metadata literal on such a declaration to carry a place — the same reason a "+
			"forwarding declaration is outside this population. The exclusion is DERIVED from the absence "+
			"of a registration on every run rather than recorded as a permitted name, so it cannot "+
			"outlive its subject: a condition acquiring a registration joins the population on the next "+
			"run and fails until it declares a place, and one that is deleted leaves the population and "+
			"this report together.\n"+
			"They are named on every run rather than counted, because a population whose excluded half is "+
			"invisible has narrowed the corpus while reading as full coverage. Each is also counted "+
			"against the off-framework ceiling wherever the census places it outside the dispatch, so "+
			"none of them is exempt from the meter as well as from this gate.",
			len(unregistered), strings.Join(namedList(unregistered, 12), "; "))
	}

	tier := "reports and does not block"
	if undeclaredRulePlaceBlocks {
		tier = "blocks"
	}
	t.Logf("declared-place population: %d condition(s) declare no place, against %d that do — %d a rule "+
		"over authored input, %d a rule another evaluator owns, %d a rule no stage reaches, %d no rule "+
		"at all. The population is every declared condition CARRYING A REGISTRATION: %d forwarding "+
		"declaration(s) and %d condition(s) no registration binds are outside it, because neither "+
		"carries a metadata literal to declare a place on. This assertion %s on any member.",
		len(undeclared),
		byPlace[placeOverAuthoredInputFunc]+byPlace[placeEvaluatedElsewhereFunc]+
			byPlace[placeNoStageReachesFunc]+byPlace[placeNotARuleFunc],
		byPlace[placeOverAuthoredInputFunc],
		byPlace[placeEvaluatedElsewhereFunc],
		byPlace[placeNoStageReachesFunc],
		byPlace[placeNotARuleFunc],
		len(forwarding),
		len(unregistered),
		tier)
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

// emissionSite is one place a sentinel is raised, and whether that place is the
// framework's dispatch. Membership travels with the site rather than being
// recomputed from its name, because the kinds of site establish it differently:
// a check declaration by what it constructs, and every other host — a declared
// function, any other package-level value — by its package.
type emissionSite struct {
	name      string
	framework bool
}

// sentinelCensus returns the declared sentinel names, sorted, one declaring path
// per name, the sites naming each one, and, for each sentinel whose own
// declaration names another sentinel, the sentinels that declaration names.
//
// The paths are returned because a report that excludes a condition from a
// population has to say where that condition is declared, so a reader can open
// the declaration and check the exclusion rather than take it — the same reason
// every other bucket in this file is named beside the declaration it was
// attributed from.
//
// The fourth result answers a different question from the third and is kept
// apart from it deliberately. The sites say where a rule is raised; the
// forwarding relation says what a declaration IS. Folding a forwarding
// declaration into the site map would make a name that raises nothing count as
// emitted, which is the one thing the emission counts must not say.
//
// Two host shapes count as a site. A non-test declared function whose body names
// a sentinel identifier — the criterion the covering measurement used — and a
// package-level var declaration whose value expression obtains one. Both count
// the site rather than the occurrence, so naming one sentinel twice is one site,
// and both are an upper bound on checks: a function that wraps a sentinel while
// re-raising it counts as naming it.
//
// A value host is walked whole, from the composite literal down, rather than
// only at a function literal inside it. A check names its sentinel on the
// value's own field as well as inside the predicate, so a walk scoped to the
// predicate would see the second and miss the first — and would break outright
// on a check that names its sentinel only on the field. Walking the whole value
// expression also needs no knowledge of which field holds a predicate, so the
// field set can grow without the census following it.
//
// # Why the value host is every package-level var and not only a check
//
// A rule raised from a diagnostic set assembled once at package level is raised
// as surely as one raised inline, and a walk that enters only the check shape
// cannot see it: the sentinel is obtained on a field of a package-level struct
// value, and the function that reads the field back and wraps it names no
// sentinel at all. Both halves of the raise are then outside the walk, and the
// sentinel reads as raised by nothing while the shipped build raises it. That is
// the one reading a census must never produce, because a name minted for a check
// never written and a rule wired through a value are indistinguishable from the
// outside and only the first is a defect. The host set is therefore every
// package-level var, so that assembling a rule into a value rather than writing
// it inline changes nothing about whether it is counted.
//
// # The inner criterion differs by host, and that is a property of the hosts
//
// A bare MENTION inside a package-level value establishes nothing: a sentinel is
// routinely named in the declaration of another sentinel that wraps it, and in
// diagnostic tables keyed by sentinel, and crediting those would reclassify a
// sentinel on the strength of a declaration that raises nothing. So a value host
// is read by the stronger question — does this expression OBTAIN the sentinel's
// value, per [obtainedIn] — rather than by the naming question [namesIn]
// answers.
//
// A declared function body is read by the naming question, and the asymmetry is
// deliberate at both ends. A function body is a host that RUNS, where the two
// questions coincide for every shape in this module and the naming reading is
// already recorded above as an upper bound on checks; reading it the other way
// would move the migration meter for reasons that have nothing to do with which
// hosts are entered. A package-level value is not a host that runs — it is data,
// and inside data a mention is routinely a key or a wrapped reference. The two
// hosts are read by the two questions each can answer soundly, and the direction
// of error at the value host is toward under-attribution: a value that merely
// mentions a sentinel is not credited with raising it.
//
// Both questions additionally read the package-local binding an accessor
// publishes, supplied per declaring package by [accessorHandles], because inside
// that package the binding is the only spelling a raise can take. It is a value
// rather than an accessor, so the value host obtains it by NAMING it — the same
// arm a value-bound sentinel takes, for the same reason: there is no call to
// look for, and requiring one would report every raise of it as no raise at all.
//
// The function-body reading of a binding therefore inherits that host's upper
// bound rather than narrowing it. A body asking `errors.Is(err, errX)` names the
// binding and is counted, exactly as a body asking `errors.Is(err, ErrX())`
// names the accessor and is counted today; the two spellings ask one question
// and it would be incoherent for the census to answer them differently. What
// this file reports is where a rule is EXPRESSED, and the bound is stated on the
// off-framework figure itself rather than repaired here.
//
// One shape is under-attributed by that choice and is named so it is not
// mistaken for a defect in the tree. A sentinel reached only as a function VALUE
// — an accessor bound into a dispatch table something else calls — is obtained
// by nothing this walk can see, so it lands among the sentinels nothing raises.
// That is the safe direction: a false alarm somebody investigates, rather than an
// unraised condition reported as raised.
//
// # Where the framework verdict comes from, per host
//
// A package-level [Check] declaration belongs to the dispatch wherever it is
// declared, because its halting currency is framework-issued by construction.
// Every other value host is read by its declaring package's path, exactly as a
// plain declared function is — so a diagnostic set assembled in a consuming
// package is attributed outside the dispatch, which is what it is.
//
// The value walk below admits a var declaration and not a const one, which is
// where this reading is deliberately NARROWER than [declaredSentinels] beside it,
// and the difference is a property of the shape being looked for rather than an
// oversight to reconcile. A Go constant is limited to constant expressions, so it
// can hold neither a composite literal, a function literal nor a call, and an
// error value is not a constant — so a const specification can never obtain a
// sentinel's value. A const can still BIND a sentinel, which is why the
// population walk reads both, and reading it here would only widen the walk to
// specifications that cannot match.
func sentinelCensus(t *testing.T) (declared []string, declaredAt map[string]string, emitters map[string][]emissionSite, forwards map[string][]string, placements placementDeclarations) {
	t.Helper()

	files, _ := collectGoFiles(t, scannedRoots, excludeTestFiles)
	names := declaredSentinels(files)
	forwards = forwardingDeclarations(files, names)
	accessorPublished := accessorPublishedSentinels(files, names)
	handles := accessorHandles(files, names)
	placements = readPlacementDeclarations(t, files, handles)

	emitters = map[string][]emissionSite{}
	for path, file := range files {
		dir := filepath.ToSlash(filepath.Dir(path))
		inFrameworkPackage := strings.Contains(dir, frameworkPackageSuffix)
		pkgHandles := handles[dir]
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body == nil {
					continue
				}
				// An accessor's own declaration is read WITHOUT the bindings,
				// and nothing here is more load-bearing than that. Its body
				// names the binding it publishes, so crediting it would make
				// every accessor-published sentinel raised by its own
				// declaration: the no-emission-site bucket would empty
				// permanently, this assertion would go on passing, and the one
				// finding it exists to report could never be reported again. The
				// body is still read for what it NAMES, so a forwarding accessor
				// still counts as a site for the sentinel it forwards to.
				bodyHandles := pkgHandles
				if _, isSentinel := names[d.Name.Name]; isSentinel {
					if _, isAccessor := accessorHandle(d); isAccessor {
						bodyHandles = nil
					}
				}
				recordNamed(emitters, names, bodyHandles, emissionSite{
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
					// Only the value expressions are walked, never the names the
					// specification binds. A registration binds the very name an
					// accessor publishes, so reading the name side would credit
					// every registration with raising itself — the same erasure
					// the accessor exclusion above prevents, arriving from the
					// other half of the declaration.
					for i, value := range vs.Values {
						if i >= len(vs.Names) {
							continue
						}
						recordObtained(emitters, names, accessorPublished, pkgHandles, emissionSite{
							name:      dir + "." + vs.Names[i].Name,
							framework: inFrameworkPackage || isCheckValue(value),
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
	return declared, names, emitters, forwards, placements
}

// placementDeclaration is what one registration says about where the rule its
// condition names belongs: where the statement was read from, which of the two
// spellings it is written in, what it says, and whether the same registration
// also carries a declared-ahead note.
//
// # Two spellings, one fact
//
// A registration declares its place as a rule-place value, which says which of
// four places the rule belongs to and carries prose for the three that take one.
//
// The prose is kept rather than discarded once the place is established, because
// the guard that fails on a declaration the tree contradicts has to show a reader
// the claim being contradicted. Nothing else reads it: the criterion is which
// place is declared, and the prose is for whoever opens the declaration.
type placementDeclaration struct {
	site string

	// placeFunc is the name of the constructor a rule-place key was given, and
	// placeReason the literal that constructor was handed. Both are empty when
	// that key is absent, and placeReason is empty for the place that takes no
	// reason.
	placeFunc   string
	placeReason string

	declaredAhead bool
}

// claimsOutsideTheDispatch reports whether this declaration says the rule could
// not be one of the framework's own — because another evaluator owns it, because
// no stage of a compilation reaches the moment it runs at, or because the
// condition names no rule at all.
//
// Those three are read together everywhere in this file, and deliberately: a
// condition in any of them was never inside the dispatch, so none of them is the
// backwards movement the ratchet refuses. What separates them is the question the
// migration asks — one is a rule somewhere else, one is a rule at a moment no
// stage holds the facts for, the last is no rule at all — and that separation is
// what a later reader meets instead of the question. The ratchet does not need
// it, so it is reported rather than partitioned on.
func (d placementDeclaration) claimsOutsideTheDispatch() bool {
	return d.placeFunc == placeEvaluatedElsewhereFunc ||
		d.placeFunc == placeNoStageReachesFunc ||
		d.placeFunc == placeNotARuleFunc
}

// claim is the prose the declaration carries, for a guard that has to show a
// reader what is being contradicted.
func (d placementDeclaration) claim() string { return d.placeReason }

// placementDeclarations is what [readPlacementDeclarations] reads out of the
// tree: the declarations it could attribute to a sentinel, the ones it could
// not, and the sentinels a registration was found for at all.
//
// The first two are separate because only the first can be partitioned. An
// orphan is not dropped, though — it is a declaration that describes no
// condition this census counts, which is exactly what the describes-nothing
// guard exists to fail on, so it is carried here rather than discarded at the
// walk.
type placementDeclarations struct {
	bySentinel map[string]placementDeclaration
	orphans    []string

	// registered is every sentinel this walk found a registration for, WHETHER
	// OR NOT that registration declares a place. It is read from the same walk
	// and the same population as bySentinel, so the two cannot drift into two
	// criteria that agree today.
	//
	// It answers a question bySentinel cannot: a sentinel absent from both has
	// no registration to declare a place on, while one present here and absent
	// there has a registration that declares none. Those are a coverage bound
	// and a defect respectively, and the gate over undeclared places has to tell
	// them apart — a walk recording only the declarations it found reads them
	// identically and would fail on the first.
	registered map[string]bool
}

// readPlacementDeclarations reads, for each declared sentinel, what its
// registration says about where the rule it names belongs — and, separately,
// WHETHER a registration binds it at all.
//
// The second fact is read here rather than by a walk of its own because it is
// the same walk: a registration is recognised before its metadata is examined,
// so asking whether one exists costs nothing and asking it anywhere else would
// be a second recogniser of the same shape, free to drift from this one. What
// consumes it is the gate over undeclared places, which has to separate a
// condition whose registration declares no place — a defect — from one with no
// registration to declare on, which is a bound on what this census can ask.
//
// The shape it looks for is the one a registration is written in: a package-
// level `var <binding> = <pkg>.Register(<code>, <pkg>.Meta{ … })`. The binding
// name comes from the value specification and is mapped to the sentinel that
// publishes it through [accessorHandles], which is keyed per directory for the
// reason recorded there — a package-local binding is unexported, so two packages
// may bind the same local name for two different sentinels.
//
// The qualifier before Register is deliberately not resolved to an import, and
// no type is resolved either. That admits a same-named Register from outside
// this module, which is the same admission [namesIn] and [obtainedIn] already
// make beside it rather than a new one, and it keeps this walk inside the
// no-types criterion the rest of the file is taken over.
//
// # This duplicates the generator's reader, deliberately
//
// A second recogniser of this same registration shape lives in the tool that
// composes the published reference, and the two are NOT unified — the same
// arrangement, and the same reason, as the declaration recogniser in
// [declaredSentinels]. Two independent readings of one population fail in
// different directions; one shared reading replaces them with a single point of
// blindness, and this census would then inherit whatever the generator cannot
// see. A criterion change is carried to both sites by hand, which is the cost
// this duplication knowingly pays.
//
// # An unreadable value fails rather than being skipped
//
// A value this walk cannot resolve — an identifier, a call, a value built
// anywhere but in the declaration — is reported as a failure naming the
// declaration, never passed over. A skipped declaration is indistinguishable
// here from an absent one, so its condition would stay in the ratcheted
// population and the ceiling would fail naming a regression that did not
// happen: a red attributed to the wrong thing is worse than the red it stands
// in for. A present-but-blank value is refused for the same reason and is
// additionally refused at registration, which this cannot rely on because it
// reads source rather than running it.
//
// # A rule-place value is read by the constructor it names
//
// The value is a call rather than a string, so it is read by which function the
// call names and what literal that function was handed — see
// [declaredRulePlace]. That is the same no-types criterion the rest of this file
// is taken over: nothing here resolves the call to a function, and a
// same-named constructor from outside this module would be read as this one,
// which is the admission [namesRegistration] already makes one line along.
func readPlacementDeclarations(t *testing.T, files map[string]*ast.File, handles map[string]map[string]string) placementDeclarations {
	t.Helper()

	out := placementDeclarations{
		bySentinel: map[string]placementDeclaration{},
		registered: map[string]bool{},
	}
	for path, file := range files {
		dir := filepath.ToSlash(filepath.Dir(path))
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				meta, ok := registrationMeta(vs.Values[0])
				if !ok {
					continue
				}
				site := dir + "." + vs.Names[0].Name
				// The sentinel is resolved BEFORE the place is read, and the
				// order is the whole of what makes the coverage bound derivable.
				// A registration is recorded here whether or not it declares a
				// place, so a condition absent from this set is one no
				// registration in this module binds at all — a fact about the
				// tree, read on every run, rather than a name somebody recorded.
				sentinel, published := handles[dir][vs.Names[0].Name]
				if published {
					out.registered[sentinel] = true
				}
				placeValue, aheadValue := declaredMetaValues(meta)
				if placeValue == nil {
					continue
				}
				declaration := placementDeclaration{site: site}
				fn, reason, readable := declaredRulePlace(placeValue)
				if !readable {
					reportUnreadableDeclaration(t, site, rulePlaceMetaKey)
					continue
				}
				declaration.placeFunc, declaration.placeReason = fn, reason
				if aheadValue != nil {
					aheadNote, aheadReadable := declaredString(aheadValue)
					if !aheadReadable {
						reportUnreadableDeclaration(t, site, declaredAheadMetaKey)
						continue
					}
					declaration.declaredAhead = strings.TrimSpace(aheadNote) != ""
				}
				if !published {
					out.orphans = append(out.orphans,
						site+" (no accessor in that package publishes this binding, so the census counts "+
							"no sentinel for it)")
					continue
				}
				// Where one package somehow registers a sentinel's binding
				// twice, the lexicographically first site wins, so a reported
				// attribution is the same on every run rather than whichever
				// the file map handed back first — the convention
				// [declaredSentinels] and [accessorHandles] already follow.
				if prior, seen := out.bySentinel[sentinel]; seen && prior.site <= site {
					continue
				}
				out.bySentinel[sentinel] = declaration
			}
		}
	}
	sort.Strings(out.orphans)
	return out
}

// declaredRulePlace reads a rule-place value: which of the errors package's four
// constructors the declaration names, and the literal it was handed.
//
// It is deliberately strict about the shape, and every arm that returns
// unreadable is a value the guard above then FAILS on rather than skips. The
// reason is the one recorded at [reportUnreadableDeclaration]: a value this
// census cannot read is indistinguishable here from an absent one, so a skip
// would leave the condition in the ratcheted population and the ceiling would
// fail naming a regression that did not happen.
//
// The reason is required for the three places that carry one, and refused as blank
// for the same reason registration refuses it: a place declared with nothing
// behind it moves a condition out of the ratcheted count while saying nothing
// about why it is out. The place that carries no reason is read as unreadable if
// handed one, because a declaration that does not compile is not a declaration
// this census should report as well-formed.
func declaredRulePlace(expr ast.Expr) (fn, reason string, ok bool) {
	call, isCall := expr.(*ast.CallExpr)
	if !isCall {
		return "", "", false
	}
	switch f := call.Fun.(type) {
	case *ast.Ident:
		fn = f.Name
	case *ast.SelectorExpr:
		fn = f.Sel.Name
	default:
		return "", "", false
	}
	switch fn {
	case placeOverAuthoredInputFunc:
		if len(call.Args) != 0 {
			return "", "", false
		}
		return fn, "", true
	case placeEvaluatedElsewhereFunc, placeNoStageReachesFunc, placeNotARuleFunc:
		if len(call.Args) != 1 {
			return "", "", false
		}
		text, readable := declaredString(call.Args[0])
		if !readable || strings.TrimSpace(text) == "" {
			return "", "", false
		}
		return fn, text, true
	}
	return "", "", false
}

// reportUnreadableDeclaration fails on a metadata value this census must read
// and cannot.
func reportUnreadableDeclaration(t *testing.T, site, key string) {
	t.Helper()

	t.Errorf("the %s value declared at %s is not a string literal carrying text that this census can "+
		"read.\n"+
		"INVARIANT: the value is read off the registration's own syntax, so it is a string literal, or "+
		"literals joined with +, and it says something. A blank value is refused at registration too, "+
		"and is refused here as well because this reads source rather than running it.\n"+
		"CONSEQUENCE: an unreadable value is indistinguishable here from an absent one, so the condition "+
		"stays in the ratcheted population and the ceiling fails naming a regression that did not "+
		"happen — a red attributed to the wrong thing, which costs more than the red it stands in for.\n"+
		"REMEDY: write the value as a string literal at the registration. A constant or a call moves the "+
		"text out of the declaration, where neither this census nor a reader of the source can see it.",
		key, site)
}

// registrationMeta returns the metadata literal of a registration call, and
// whether the expression is one at all.
//
// The test is the call's shape alone — the registration function's name, two
// arguments, the second a composite literal — and deliberately says nothing
// about the first argument. Requiring a string literal there would be the
// tighter reading and is refused: a registration declaring its code through a
// constant would then be unrecognised, and a note on it would go unread while
// reading exactly like a registration carrying none. That is the silent skip
// the loud failure below exists to prevent, reintroduced one argument to the
// left. Recognising too much costs nothing here, because anything recognised in
// error and carrying a note lands in the describes-nothing guard, which fails.
func registrationMeta(value ast.Expr) (*ast.CompositeLit, bool) {
	call, ok := value.(*ast.CallExpr)
	if !ok || len(call.Args) != 2 || !namesRegistration(call.Fun) {
		return nil, false
	}
	meta, ok := call.Args[1].(*ast.CompositeLit)
	return meta, ok
}

// namesRegistration reports whether an expression names the registration
// function, under either spelling a call to it takes: bare inside the declaring
// package, and qualified everywhere else.
func namesRegistration(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name == registrationFuncName
	case *ast.SelectorExpr:
		return f.Sel.Name == registrationFuncName
	}
	return false
}

// declaredMetaValues returns the expressions a registration's metadata literal
// binds to the two keys this census reads, and nil for a key it does not
// declare. Every other key is skipped, so a metadata field this walk knows
// nothing about disturbs it not at all.
//
// Each key is returned as its own value rather than folded into one, because
// the two are different questions with different readers: one is prose read by
// [declaredString], and the other is a call read by [declaredRulePlace].
func declaredMetaValues(meta *ast.CompositeLit) (place, ahead ast.Expr) {
	for _, el := range meta.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case rulePlaceMetaKey:
			place = kv.Value
		case declaredAheadMetaKey:
			ahead = kv.Value
		}
	}
	return place, ahead
}

// declaredString returns the text of a string-valued expression a registration
// declares, for the two shapes one can be written in: a single literal, and
// several joined with +.
//
// The joined form is read rather than refused because it is how a declaration
// too long for one line is written, and these notes are prose that legitimately
// runs long. Only a concatenation of literals folds: a value naming a constant,
// or built by a call, is not read, because reading it would mean evaluating the
// package — the thing this census exists to avoid — and because a declaration
// whose text is not in the declaration is one a reader of the source cannot see
// either.
func declaredString(expr ast.Expr) (string, bool) {
	switch v := expr.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		left, ok := declaredString(v.X)
		if !ok {
			return "", false
		}
		right, ok := declaredString(v.Y)
		if !ok {
			return "", false
		}
		return left + right, true
	}
	return "", false
}

// partitionOffFrameworkByDeclaredPlace splits the off-framework population
// between the conditions this ratchet counts and the ones it does not, and fails
// on a declaration the tree contradicts or one that describes nothing.
//
// # Which declarations it partitions on, and which it passes over
//
// A declaration is read here only when it claims the rule is not one of the
// framework's own — another evaluator owns it, no declared stage of a
// compilation reaches it, or the condition names no rule at all. A condition
// declaring a rule OVER AUTHORED INPUT makes no such
// claim: it says the rule belongs where the dispatch can evaluate it, which is
// true whether the census places the condition inside the dispatch or outside
// it, so there is nothing here for the tree to contradict and nothing to move
// out of the ratcheted count. Such a declaration is passed over by this
// partition and read by the gate over undeclared places instead.
//
// # The arms
//
// They are exhaustive over the claiming declarations and each is taken at most
// once per condition, which is what keeps the two counts a partition rather than
// two overlapping views. In order: a condition the census attributes to the
// framework's dispatch fails, because the tree shows the rule IS a check; one
// the census places off-framework moves out of the ratcheted count; one
// carrying a declared-ahead note is admitted and reported, because nothing
// raises it yet so it is in no emission bucket to be moved out of; and anything
// left describes no condition this census can place, which fails.
//
// The reasoning for each failure, and for what neither of them can see, is
// recorded beside [offFrameworkEmissionBlocks] rather than restated here.
func partitionOffFrameworkByDeclaredPlace(t *testing.T, offFramework []string, emitters map[string][]emissionSite, declarations placementDeclarations) (ratcheted, unratcheted []string) {
	t.Helper()

	inOffFramework := map[string]bool{}
	for _, name := range offFramework {
		inOffFramework[name] = true
	}

	declared := make([]string, 0, len(declarations.bySentinel))
	for name := range declarations.bySentinel {
		declared = append(declared, name)
	}
	sort.Strings(declared)

	moved := map[string]bool{}
	var contradicted, describesNothing, notYetRaised []string
	for _, name := range declared {
		decl := declarations.bySentinel[name]
		if !decl.claimsOutsideTheDispatch() {
			continue
		}
		switch {
		case attributedToDispatch(emitters[name]):
			contradicted = append(contradicted,
				name+" at "+decl.site+", declaring: "+decl.claim())
		case inOffFramework[name]:
			moved[name] = true
			unratcheted = append(unratcheted, name)
		case decl.declaredAhead:
			notYetRaised = append(notYetRaised, name+" at "+decl.site)
		default:
			describesNothing = append(describesNothing, name+" at "+decl.site)
		}
	}
	describesNothing = append(describesNothing, declarations.orphans...)

	for _, name := range offFramework {
		if !moved[name] {
			ratcheted = append(ratcheted, name)
		}
	}

	if len(contradicted) > 0 {
		t.Errorf("%d condition(s) declare that their rule is not one of the framework's own — "+
			"another evaluator owns it, no declared stage reaches it, or the condition names no "+
			"rule at all — while the census attributes a dispatch emission site to them.\n"+
			"INVARIANT: a condition carrying any of those declarations names a rule that is not "+
			"expressed as a %s value, so no emission site of it is the framework's dispatch.\n"+
			"CONSEQUENCE: the declaration is contradicted by the tree it describes. Left standing it "+
			"holds the condition outside the ratcheted population on a fact the tree already falsifies, "+
			"so every later reading of the ceiling is taken over a population one member too small and "+
			"the ratchet quietly carries one more unit of slack.\n"+
			"REMEDY: declare the place the condition is actually in. The tree shows the rule IS expressed "+
			"as a %s, which is what the declaration says it is not, so the declaration is the "+
			"wrong half rather than the check.\n"+
			"Alphabetically: %s", len(contradicted), checkTypeName, checkTypeName,
			strings.Join(namedList(contradicted, 12), "; "))
	}

	if len(describesNothing) > 0 {
		t.Errorf("%d declaration(s) placing a rule outside the framework's dispatch describe no condition "+
			"this census can place in the off-framework population.\n"+
			"INVARIANT: such a declaration says why a condition is counted apart from the rules that "+
			"could be expressed as %s values, so it describes a condition the census places among "+
			"them.\n"+
			"CONSEQUENCE: a declaration describing nothing is the recorded exception this partition "+
			"exists not to be. It goes on reading as a measured fact about a condition that has since "+
			"been renamed, deleted, or never reached the population at all, and nothing else in the tree "+
			"reports it.\n"+
			"REMEDY: delete the declaration, because it describes nothing; or, where the condition is "+
			"still meant to exist, repair the registration so the census reaches it — a binding no "+
			"accessor publishes declares no sentinel this census counts. Recording the name in a list "+
			"here is neither of those, and no such list exists.\n"+
			"Alphabetically: %s", len(describesNothing), checkTypeName,
			strings.Join(namedList(describesNothing, 12), "; "))
	}

	if len(notYetRaised) > 0 {
		t.Logf("%d condition(s) place their rule outside the framework's dispatch AND carry a "+
			"declared-ahead note, and have no emission site yet: %s.\n"+
			"Such a condition is admitted rather than failed: the declared-ahead note is the "+
			"registration's own statement that nothing raises it, so there is no emission bucket to move "+
			"it out of. It is reported on every run rather than passing quietly, because the placement "+
			"stays undischarged until the work the note names lands and the condition arrives in the "+
			"off-framework population.",
			len(notYetRaised), strings.Join(namedList(notYetRaised, 12), "; "))
	}

	return ratcheted, unratcheted
}

// attributedToDispatch reports whether any of a sentinel's emission sites is
// the framework's dispatch.
//
// It reads the sites rather than the bucket a sentinel landed in, and the
// difference is the whole point: a sentinel with one dispatch site and one
// outside it is in the off-framework bucket, and a declaration saying its rule
// could never be a check is contradicted by that dispatch site all the same.
func attributedToDispatch(sites []emissionSite) bool {
	for _, site := range sites {
		if site.framework {
			return true
		}
	}
	return false
}

// namedWithPlacement renders each unratcheted sentinel beside the registration
// its declaration was read from, so a reader can open that declaration and check
// the attribution rather than take it. It is the unratcheted bucket's
// counterpart to [namedWithSites] and [namedWithForwards].
func namedWithPlacement(sentinels []string, declarations placementDeclarations) []string {
	out := make([]string, 0, len(sentinels))
	for _, sentinel := range sentinels {
		out = append(out, sentinel+" declared at "+declarations.bySentinel[sentinel].site)
	}
	return out
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
		// Read without the package-local bindings, deliberately. A forwarding
		// relation holds between two PUBLISHED names; an accessor's body naming
		// the binding it publishes is the publication itself, so reading the
		// bindings here would move every ordinary accessor into the forwarding
		// bucket and out of the population its own raises are counted against.
		for name := range namesIn(names, nil, subtree) {
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
//
// handles maps the package-local bindings an accessor publishes, for the
// declaring package only, and is nil where the question being asked is about
// exported names alone — [forwardingDeclarations] passes nil, because a
// forwarding declaration is a relation between two PUBLISHED names and an
// accessor's body naming its own binding is the publication rather than a
// forward. Where it is supplied, naming the binding names the sentinel: inside
// the declaring package that is the only spelling a raise can take.
//
// A name after a dot is never read as a binding. The binding is unexported and
// package-scoped, so a selected name is a field or a method, and reading one as
// a binding would credit a rule to a struct field that happens to share the
// name. The selected identifier is marked on the way down and skipped on the way
// back into the subtree, which is how the exported reading below already reaches
// a qualified spelling exactly once.
func namesIn(names map[string]string, handles map[string]string, subtree ast.Node) map[string]bool {
	named := map[string]bool{}
	selected := map[*ast.Ident]bool{}
	ast.Inspect(subtree, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.SelectorExpr:
			selected[x.Sel] = true
			if _, ok := names[x.Sel.Name]; ok {
				named[x.Sel.Name] = true
			}
		case *ast.Ident:
			if selected[x] {
				return true
			}
			if _, ok := names[x.Name]; ok {
				named[x.Name] = true
				return true
			}
			if sentinel, ok := handles[x.Name]; ok {
				named[sentinel] = true
			}
		}
		return true
	})
	return named
}

// recordNamed attributes site to every declared sentinel the subtree names.
func recordNamed(emitters map[string][]emissionSite, names map[string]string, handles map[string]string, site emissionSite, subtree ast.Node) {
	for name := range namesIn(names, handles, subtree) {
		emitters[name] = append(emitters[name], site)
	}
}

// obtainedIn returns the declared sentinels a subtree OBTAINS the value of, each
// once however often it appears.
//
// It is the stronger sibling of [namesIn] and the criterion the value hosts are
// read by, for the reason recorded at [sentinelCensus]: inside data a mention is
// routinely a key or a wrapped reference rather than a raise, so crediting one
// would reclassify a sentinel on the strength of a declaration that raises
// nothing.
//
// The shape an obtain takes follows from how the sentinel's own declaration binds
// it, which is why [accessorPublishedSentinels] is an argument rather than
// something guessed at here.
//
//   - A sentinel published by an accessor is obtained by INVOKING it — `ErrX()`
//     or `pkg.ErrX()`. The value cannot be reached any other way, so the empty
//     argument list is the whole test: a same-prefixed helper taking arguments
//     names no identity, and a bare mention of the accessor passes the function
//     itself rather than the condition it publishes.
//   - A sentinel bound to a value is obtained by NAMING it. There is no call to
//     look for, so requiring one would drop every value-bound sentinel's sites
//     and report a rule the tree raises as raised by nothing — the failure this
//     criterion exists to remove, reintroduced from the other side.
//
// A qualified spelling is read for both shapes, and the qualifier is deliberately
// not resolved to an import: the population is a set of bare names, so resolving
// it would ask a question no count in this file is taken over. That admits a
// same-named value from outside this module, which is the same admission
// [namesIn] already makes beside it rather than a new one.
//
// A third shape joins those two and is read like the second. A package-local
// binding an accessor publishes is a VALUE, so it is obtained by being named and
// there is no call to look for — the accessor is the thing that cannot be
// written at the raise, because it yields the wrong type. handles carries those
// bindings for the declaring package only, and a name after a dot is never read
// as one, for the reason [namesIn] records beside its own marking.
func obtainedIn(names map[string]string, accessorPublished map[string]bool, handles map[string]string, subtree ast.Node) map[string]bool {
	obtained := map[string]bool{}
	record := func(name string, invoked bool) {
		if _, ok := names[name]; !ok {
			return
		}
		// The two shapes each admit exactly one occurrence form, so the test is
		// that the form matches the declaration: an accessor obtained only by a
		// call, a bound value only by a mention. Reading a call to a bound value
		// or a mention of an accessor as an obtain is the over-attribution this
		// refuses.
		if accessorPublished[name] == invoked {
			obtained[name] = true
		}
	}
	selected := map[*ast.Ident]bool{}
	ast.Inspect(subtree, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.CallExpr:
			if len(x.Args) != 0 {
				return true
			}
			switch fun := x.Fun.(type) {
			case *ast.Ident:
				record(fun.Name, true)
			case *ast.SelectorExpr:
				record(fun.Sel.Name, true)
			}
		case *ast.SelectorExpr:
			selected[x.Sel] = true
			record(x.Sel.Name, false)
		case *ast.Ident:
			if selected[x] {
				return true
			}
			if _, isSentinel := names[x.Name]; isSentinel {
				record(x.Name, false)
				return true
			}
			if sentinel, ok := handles[x.Name]; ok {
				obtained[sentinel] = true
			}
		}
		return true
	})
	return obtained
}

// recordObtained attributes site to every declared sentinel the subtree obtains
// the value of.
func recordObtained(emitters map[string][]emissionSite, names map[string]string, accessorPublished map[string]bool, handles map[string]string, site emissionSite, subtree ast.Node) {
	for name := range obtainedIn(names, accessorPublished, handles, subtree) {
		emitters[name] = append(emitters[name], site)
	}
}

// accessorHandle returns the package-local name an accessor declaration
// publishes — the identifier in `func ErrX() error { return errX }`.
//
// It is READ FROM THE DECLARATION and never derived from the spelling. A
// lowercase-twin rule would agree with this tree today and would silently stop
// agreeing the day an accessor is written over a differently-named binding, at
// which point every raise through that binding would go uncounted while the
// census reported a clean population.
//
// The shape is the whole test: no receiver, and a body that is one return of a
// bare identifier. A body returning a call is the forwarding shape, which names
// another sentinel rather than a package-local binding and is already read as
// what it is by [forwardingDeclarations].
func accessorHandle(fd *ast.FuncDecl) (string, bool) {
	if fd.Recv != nil || fd.Body == nil || len(fd.Body.List) != 1 {
		return "", false
	}
	ret, ok := fd.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return "", false
	}
	id, ok := ret.Results[0].(*ast.Ident)
	if !ok {
		return "", false
	}
	return id.Name, true
}

// accessorHandles returns, per package directory, the package-local name each
// accessor publishes mapped to the sentinel that publishes it.
//
// This is what lets the census see a raise at all now that the identity a raise
// names is the binding rather than the accessor. An identity a caller can only
// obtain by calling the accessor is reached, inside the declaring package, by
// naming the binding directly — the accessor is not written there, because it
// would yield the wrong type — so a census matching [sentinelName] alone sees
// nothing at the site where the rule is actually raised.
//
// It is keyed by directory rather than module-wide, unlike every other map here.
// A package-local binding is unexported and package-scoped, so two packages may
// bind the same local name for two different sentinels; a module-wide key would
// let one package's raise credit the other's sentinel, which is the merge
// [namesIn]'s bare-name population can afford for exported names and cannot
// afford for these.
//
// Where one directory publishes one binding under two accessor names the
// lexicographically first wins, so a reported attribution is the same on every
// run rather than whichever the file map handed back first.
func accessorHandles(files map[string]*ast.File, names map[string]string) map[string]map[string]string {
	handles := map[string]map[string]string{}
	for path, file := range files {
		dir := filepath.ToSlash(filepath.Dir(path))
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if _, isSentinel := names[fd.Name.Name]; !isSentinel {
				continue
			}
			local, isAccessor := accessorHandle(fd)
			if !isAccessor {
				continue
			}
			if handles[dir] == nil {
				handles[dir] = map[string]string{}
			}
			if prior, seen := handles[dir][local]; !seen || fd.Name.Name < prior {
				handles[dir][local] = fd.Name.Name
			}
		}
	}
	return handles
}

// accessorPublishedSentinels reports, for each declared sentinel, whether its
// declaration publishes it through an accessor rather than binding it to a value.
//
// It reads the same file set and the same population as the census, and the
// population arrives as an argument, so there is no second criterion for what a
// sentinel is — the same arrangement [forwardingDeclarations] beside it uses, and
// for the same reason: this asks a question about the declaration rather than
// about a rule, and a second walk free to recognise a shape the other does not
// would move a sentinel between buckets for no reason a reader could find.
//
// A name declared in both shapes anywhere in the module counts as
// accessor-published, because the accessor is the shape whose value can only be
// reached by a call: reading such a name as value-bound would credit a mention of
// the accessor as an obtain, which is the over-attribution [obtainedIn] refuses.
func accessorPublishedSentinels(files map[string]*ast.File, names map[string]string) map[string]bool {
	published := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil {
				continue
			}
			if _, isSentinel := names[fd.Name.Name]; isSentinel {
				published[fd.Name.Name] = true
			}
		}
	}
	return published
}

// namedWithSites renders each sentinel beside the sites raising it, so a reader
// can reach the declaration an attribution came from. A value host's site
// carries the variable's own name and a declared function's carries the function
// name, so either one is a name the reader can look up directly.
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
