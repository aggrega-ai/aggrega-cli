// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// componentIDKeySpace is the key space the compiler's own component-identity
// label value is declared into.
//
// It is named for the label the value lands on — aggrega.ai/component-id on
// every resource the declaring contributor's output carries — because a space
// name reaches the author inside the refusal message, and the label key is the
// one place that author has already seen this string. A space name they cannot
// reconduce to something they read sends them looking for a word that is in
// none of their files.
const componentIDKeySpace = "component-id"

// The two kinds of contributor a dispatch group has, in the author's own terms.
// The kind is what says where a contributor's name is written — a component's
// under the components the aggregate declares, a feature's under the features of
// whatever it is attached to — so it is the word the halt leads with.
const (
	producerKindComponent = "component"
	producerKindFeature   = "feature"
)

// derivedKeyProducer identifies one contributor to a dispatch group by every
// coordinate an author wrote for it.
//
// 🔴 A producer label must carry every coordinate the derived value carries,
// because the value is what gets compared and the producer is what decides
// whether two values are compared at all. A coordinate the label drops does not
// merely make a diagnostic vaguer: two contributors differing only in that
// coordinate arrive under one label, the grouping reads one declarer where there
// are two, and the two names it exists to compare are never compared. The check
// then reports nothing, which is exactly what a build with no collision reports.
//
// The coordinates are held apart rather than run together into one string, and
// that is the property rather than a matter of taste. A label built by joining
// folds the same way any joined name does — a feature named `a@b` attached at
// `c` and one named `a` attached at `b@c` reach one string — so a joined label
// would reintroduce, one layer up, precisely the defect the composed identity
// below it removes. Held apart, two producers are equal only where every
// coordinate is, which is injective by construction and needs no encoding.
//
// name is the authoring-level name the halt reports for this kind of
// contributor, and scope is the further coordinate that completes it: the
// attachment key a component carries, or the path of the boundary a feature is
// attached at. It is empty where the kind's own name identifies the contributor
// on its own, which is the case for a component's own render.
type derivedKeyProducer struct {
	kind  string
	name  string
	scope string
}

// derivedKeyProducerFor composes the label identifying one contributor from the
// coordinates an author wrote.
//
// It is the single home of that composition, and both sites that record a
// declaration reach it rather than assembling a label of their own. A second
// assembly would agree with this one on the day it was written and drift
// afterwards — and a drifted producer label does not fail loudly, it simply
// stops comparing two things that should have been compared, which is invisible
// from every green build.
//
// The coordinates are taken as arguments rather than recovered from a node
// identity the compiler built for its own routing. Reading them back out of such
// a marker is what dropped one of them: the marker joins its parts with
// characters that are also legal inside a part, so cutting it apart again cannot
// return what went in. Every caller holds the parts already, so composing from
// them is both exact and cheaper.
func derivedKeyProducerFor(kind, name, scope string) derivedKeyProducer {
	return derivedKeyProducer{kind: kind, name: name, scope: scope}
}

// derivedKeyContributor is one contributor's label together with where the
// render put that contributor's output.
//
// The two travel as one value because the comparison downstream needs both and
// neither is recoverable from the other. The label says who derived a name. The
// disposition says whether that name can reach another contributor's at all,
// which is what makes two equal names a collision rather than two names standing
// in two documents that never meet.
//
// 🔴 The disposition is NOT derivable from the label, and the case that proves it
// is the one a reader is most likely to assume away. An attachment's label looks
// identical whether the render handed its output to the group's assembler or
// wrote it beside the assembled document, and BOTH answers are reachable from one
// label: the same component-scope attachment hands its output over where its
// provider folds a group into one document and writes it beside the assembled
// document where its provider folds nothing. So a predicate keyed on the label's
// shape would answer the same for two contributions that went to different places.
// The routing is what decides, so the routing is what is carried.
type derivedKeyContributor struct {
	producer derivedKeyProducer

	// placedStandalone reports that the render wrote this contributor's output
	// as discrete files at its own position rather than handing it to its
	// dispatch group's assembler.
	//
	// It states where the output went and nothing that follows from it. In
	// particular it is true for every contributor to a group whose provider
	// folds nothing into one document, because such a group has no assembled
	// document for anything to be handed to — so a reader treating this alone as
	// a reason not to compare two names would stop comparing everywhere, which
	// is a removal rather than a narrowing. What the group's provider declared it
	// does with the group's output is a separate answer, and the two are read
	// together.
	placedStandalone bool
}

// derivedKeyContributorFor pairs one contributor's label with where the render
// put its output.
//
// Both coordinates are taken as arguments so every site that records a
// declaration states the disposition it observed rather than inheriting a
// default. A default would be the quiet failure this whole check exists to
// refuse: a site that forgot to answer would have its answer supplied for it,
// and whichever way the default fell it would be wrong for some contributor with
// nothing failing to say so.
func derivedKeyContributorFor(producer derivedKeyProducer, placedStandalone bool) derivedKeyContributor {
	return derivedKeyContributor{producer: producer, placedStandalone: placedStandalone}
}

// declareDerivedKey records that the compiler itself derived one name into one
// key space, folding it into an accumulator through the same merge a guest's
// declaration takes.
//
// It exists so there is one accumulation mechanic rather than two. The
// declarations a provider guest makes arrive over the node context's sink and
// are folded in by mergeDerivedKeys; a name the compiler derives has no
// boundary to cross and needs no sink, but it must accumulate identically or
// the two kinds of declaration would answer to different rules inside one space.
//
// The repeat report travels back to the caller rather than being dropped here,
// for the reason putAll keeps its two arguments together: a site that recorded a
// declaration while discarding what it repeated would compile, pass, and
// silently stop checking half the property. One name in one space cannot repeat
// inside its own declaration today, so the report is empty in practice — that is
// a property of this call's argument, not of the merge, and reading the merge's
// answer rather than assuming it is what keeps the two from diverging.
func declareDerivedKey(into *types.OrderedMap[[]string], space, key string) []derivedKeyRepeat {
	declared := types.OrderedMap[[]string]{}
	declared.Set(space, []string{key})
	return mergeDerivedKeys(into, declared)
}

// derivedKeyDeclaration is one contributor's declaration that it derived one
// name into one of its own key spaces: the space, the name, and the label
// identifying whatever derived it.
//
// The producer's coordinates stay inside the label rather than being flattened
// here. A reader spelling them into a sentence needs each one as a value it
// quotes itself, and a coordinate carrying its own quotes would be quoted again;
// the comparison upstream needs them apart for the sharper reason given on
// [derivedKeyProducer].
type derivedKeyDeclaration struct {
	space       string
	key         string
	contributor derivedKeyContributor
}

// derivedKeyRepeat is one name a single declaration carried more than once
// inside one of its own key spaces. It records only the space and the name: the
// contributor is whoever made that declaration, and it is attached where the
// declaration itself is recorded, so the two can never disagree about who
// derived what.
type derivedKeyRepeat struct {
	space string
	key   string
}

// derivedKeySet accumulates every derived name the contributors to one assembled
// output declare, and presents them as the declarations they are. Whether the
// declared names are injective within each space is asked by the rule declared
// over the grouping, which reads this answer; nothing here decides it.
//
// A name fails injectivity two ways, and both are the same defect — the name is
// not unique in its space — so one condition covers both. Two contributors can
// arrive at one name, which the accumulated declarations show directly. Or one
// contributor's single declaration can carry one name twice in one space, which
// the accumulated declarations cannot show: a contributor legitimately restates
// a name across its several render crossings, and by the time the declarations
// accumulate here the two cases are the same pair of entries. That case is
// therefore recognised where it is still visible — at the moment one
// declaration arrives, before it merges with the rest — and carried here on the
// declaration it belongs to.
//
// A declaration reaches here from either of two directions, and the difference
// between them ends at the sink: below this line a declaration's origin is
// unreadable and nothing reads it.
//
// From a provider guest, the direction is inverted from the collision guards
// that read output. A guest derives the names its output addresses resources by,
// and it puts them inside the file bytes the host writes verbatim — so the host
// cannot see them, and reading them back would mean parsing the provider's
// format in the host, which is the domain knowledge the Wasm boundary keeps out.
// The guest declares them instead, and what arrives is already the answer to
// every provider-specific question.
//
// From the compiler itself, the name is one the compiler derived and then handed
// a guest to stamp — a label value composed from an identity the author wrote.
// The host can see that one, because it made it; it declares it for the same
// reason a guest does, so that one space is checked once however the names in it
// were arrived at. Declaring at the deriving site is what keeps a compiler
// derivation from being the one kind of name nobody reports.
//
// Blindness is the property that makes one rule enough for every provider. A
// space is an opaque string its declarer chose, a key is an opaque string its
// declarer derived, and neither this nor the rule reading it does anything but
// compare keys for equality within a space — no format is recognised, no
// provider is named, no key's meaning is learned, and no key is ever derived
// here. A second provider declaring an entirely different kind of name in an
// entirely different kind of space is covered by the same code, unchanged.
//
// Nothing is answered at put, for the same reason the assembled file set defers
// its own: an answer given at the moment of the second declaration would name
// whichever contributor happened to be walked first, and the walks upstream are
// over plain Go maps whose iteration order is randomised per run, so identical
// input would report a different message on different runs. The whole set is
// presented at once, in an order derived from the declared strings — making the
// answer a function of the input alone.
//
// The scope is one dispatch group's assembled output: the set of contributors
// whose output merges, which is where one contributor's name can displace
// another's. Each declaration therefore carries where the render put that
// contributor's output, so a contributor the render placed beside the assembled
// document rather than inside it can be told from one that reaches it — the
// distinction the accumulated names cannot show, since two contributors' names
// are the same pair of strings whichever files they landed in.
//
// 🔴 The bound on that, stated because an unstated bound reads as full coverage.
// The host knows its own routing — which of its outputs it folded into one
// document — and never learns what a key space's uniqueness MEANS. So a space
// whose names must be unique across something wider than one document, a cluster
// namespace or an account, has its comparison narrowed to less than that space
// requires wherever its declarer also folds its output into one document. The
// remedy for a guest-declared space is the one this type's contract already names
// and needs no host change: a guest needing uniqueness across a narrower or wider
// scope expresses that scope in the space name it declares, which keeps the
// choice in the guest and out of the host.
//
// 🔴 One space in the bound is the compiler's OWN and cannot use that remedy:
// componentIDKeySpace, whose value the compiler stamps as a provenance label on
// every resource a contributor emits. Its uniqueness requirement is across
// contributors rather than across one document — two contributors reaching one
// value hand one provenance to two different sets of resources — so on a provider
// that folds its group's output, a contributor whose own output is placed
// standalone has that value compared only within itself. Reached only where a
// composed identity had to be shortened, since the composition is injective
// wherever it fits its target's bound.
type derivedKeySet struct {
	entries []derivedKeyDeclaration
	repeats []derivedKeyDeclaration
}

// put records that contributor declared key in space. Whether that declaration
// collides with another is not decided here: the set answers what was declared
// and where the declarer's output went, and the rule declared over the grouping
// decides.
func (s *derivedKeySet) put(space, key string, c derivedKeyContributor) {
	s.entries = append(s.entries, derivedKeyDeclaration{
		space: space, key: key, contributor: c,
	})
}

// putAll records everything one contributor's rendering established: every name
// it declared across every space it declared, and every name one of its
// declarations carried more than once within a single space.
//
// The two travel together through one entry point rather than two, so a caller
// cannot record what a contributor declared while dropping what it repeated. A
// site that recorded only the declarations would compile, pass, and silently
// stop checking half the property.
//
// The declaration is read in the guest's own order. Iterating the ordered
// declaration rather than a plain map is what keeps the recorded order a
// function of the input; resolve orders the diagnostic itself, so this ordering
// affects nothing downstream, but it keeps the accumulator's own contents stable
// for anything that later reads them.
func (s *derivedKeySet) putAll(
	declared types.OrderedMap[[]string],
	repeated []derivedKeyRepeat,
	c derivedKeyContributor,
) {
	for space, keys := range declared.All() {
		for _, key := range keys {
			s.put(space, key, c)
		}
	}
	for _, r := range repeated {
		s.repeats = append(s.repeats, derivedKeyDeclaration{
			space: r.space, key: r.key, contributor: c,
		})
	}
}

// mergeDerivedKeys folds one render crossing's declaration into a node's
// accumulating set, appending each space's names to whatever that space already
// carries rather than replacing them, and reports any name that declaration
// carried more than once within one of its own spaces.
//
// Merging rather than replacing is load-bearing: a node renders across more than
// one crossing — a source render, a workload synthesis, an artifacts placement —
// and each returns its own declaration. Replacing would keep only the last
// crossing's names and silently drop the rest, which is exactly the kind of quiet
// loss this whole check exists to refuse. Each space's names are copied into a
// fresh slice so no accumulated space aliases a caller's backing array.
//
// The repeat report is produced here because here is the only place it can be.
// One name declared twice inside one declaration and one name restated at two
// separate crossings both end up as the same pair of entries in the accumulated
// set, and only the first is a failure of injectivity — so the distinction has to
// be drawn while a single crossing's declaration is still whole, which is
// precisely the argument the merge below then destroys. The caller carries the
// report to the group check rather than halting on it, which keeps every failure
// of the same property reported together and in one order.
//
// Nothing here reads a name. A repeat is two equal strings in one space's list;
// no format is recognised and no name's meaning is learned, exactly as in the
// across-contributors comparison.
func mergeDerivedKeys(into *types.OrderedMap[[]string], declared types.OrderedMap[[]string]) []derivedKeyRepeat {
	var repeated []derivedKeyRepeat
	for space, keys := range declared.All() {
		seen := make(map[string]bool, len(keys))
		reported := make(map[string]bool)
		for _, key := range keys {
			// Reported once however many times the name recurs: a name declared
			// three times is one repeat, not two, and the diagnostic reads the
			// same either way.
			if seen[key] && !reported[key] {
				repeated = append(repeated, derivedKeyRepeat{space: space, key: key})
				reported[key] = true
			}
			seen[key] = true
		}
		existing, _ := into.Get(space)
		merged := make([]string, 0, len(existing)+len(keys))
		merged = append(merged, existing...)
		merged = append(merged, keys...)
		into.Set(space, merged)
	}
	return repeated
}

// derivedKeyLedger accumulates the derived names one node is answerable for,
// keeping each declaration under the producer within that node that made it.
//
// 🔴 A node is not one contributor. Its own render is one, and every feature
// attachment it carries is another: an attachment has its own FeatureRecipe, its
// own render crossings and its own derived names, and the author wrote it under a
// key of its own. Accumulating all of them under the node alone puts a coordinate
// the derived names carry outside the label they are compared by — and two
// attachments on one component then become indistinguishable from one contributor
// restating a name across two of its own crossings, which the group check rules
// legitimate and passes over in silence. The ledger exists so the producer is
// attached where its coordinates are still in hand rather than recovered later
// from a node identity that never held them.
//
// It is a slice rather than a map, for two reasons of which the second is the
// stronger. A node carries few contributors, so the scan costs nothing. And the
// ledger is drained in the order its producers first declared, which is fixed by
// the render order rather than by Go's map iteration — so what reaches the group
// is a function of the input.
type derivedKeyLedger struct {
	entries []derivedKeyLedgerEntry
}

// derivedKeyLedgerEntry is everything one producer within a node has declared:
// the names merged across that producer's several render crossings, and the
// names one of its single declarations carried more than once.
type derivedKeyLedgerEntry struct {
	contributor derivedKeyContributor
	declared    types.OrderedMap[[]string]
	repeats     []derivedKeyRepeat
}

// declare folds one render crossing's declaration into the accumulation for the
// producer that made it, and keeps whatever that declaration repeated inside one
// of its own spaces.
//
// Merging rather than replacing is the argument mergeDerivedKeys makes for a
// node, applied one level finer: a single producer renders across more than one
// crossing and each returns its own declaration.
func (l *derivedKeyLedger) declare(c derivedKeyContributor, declared types.OrderedMap[[]string]) {
	e := l.entryFor(c)
	e.repeats = append(e.repeats, mergeDerivedKeys(&e.declared, declared)...)
}

// declareDerived records one name the compiler itself derived for a producer,
// through the same merge a guest's declaration takes, so the two kinds of
// declaration answer to one rule inside one space.
func (l *derivedKeyLedger) declareDerived(c derivedKeyContributor, space, key string) {
	e := l.entryFor(c)
	e.repeats = append(e.repeats, declareDerivedKey(&e.declared, space, key)...)
}

// entryFor returns the accumulation for one producer, opening one the first time
// that producer declares. Two producers are the same producer here exactly when
// every coordinate of their labels agrees, which is the whole property this type
// exists to preserve.
//
// The accumulation is keyed by the label alone and never by the disposition
// beside it. Keying by both would open a second entry for a producer that
// declared under two dispositions, and the two entries would then present as two
// contributors — reintroducing, one layer down, exactly the fold the label's own
// structure exists to prevent.
//
// Where a producer declares more than once the dispositions combine by
// conjunction: a producer whose output reached the assembler on any one of its
// crossings has its names inside the assembled document, so standing standalone
// has to hold for every crossing before it holds at all. Nothing declares one
// producer under two dispositions today; combining rather than assuming is what
// keeps the answer from depending on that.
func (l *derivedKeyLedger) entryFor(c derivedKeyContributor) *derivedKeyLedgerEntry {
	for i := range l.entries {
		if l.entries[i].contributor.producer == c.producer {
			l.entries[i].contributor.placedStandalone =
				l.entries[i].contributor.placedStandalone && c.placedStandalone
			return &l.entries[i]
		}
	}
	l.entries = append(l.entries, derivedKeyLedgerEntry{contributor: c})
	return &l.entries[len(l.entries)-1]
}

// into moves everything this node's producers declared into the set its dispatch
// group is checked from, each under its own label.
func (l *derivedKeyLedger) into(s *derivedKeySet) {
	for _, e := range l.entries {
		s.putAll(e.declared, e.repeats, e.contributor)
	}
}

// declarations is every declaration this group's contributors made, one entry
// per contributor per name per space, each carrying whether one of that
// contributor's own declarations held the name more than once and where the
// render put that contributor's output.
//
// It answers what was declared and decides nothing. Whether a name collides is
// the question a rule declared over the grouping asks of this answer, and
// keeping the two apart is what leaves the rule something to decide: an answer
// that already said which names collided would BE the rule. The disposition is
// carried for the same reason the repeat is — it is a fact about the declaration
// that the accumulated entries cannot show — and it is carried unjoined to any
// verdict, because what follows from it depends on a second answer this set does
// not hold: what the group's own provider does with the group's output.
//
// A contributor appears once per name per space however many entries it
// accumulated for that pair. Deduplicating here rather than leaving the
// repetitions to be counted is the whole reason this answer is trustworthy: a
// contributor legitimately restates a name across its several render crossings —
// a source render, a workload synthesis, an artifacts placement — so by the time
// the declarations have accumulated, that restatement and one declaration
// carrying the name twice are the same pair of entries. The distinction was
// drawn where it was still visible, at the moment a single declaration arrived,
// and it is carried here on the entry it belongs to. A reader counting
// repetitions would report a legitimate restatement as a defect.
//
// The repeats are read as a UNION with the declarations rather than as a lookup
// into them, so a repeat whose declaration never reached the accumulator is
// still answered for. Both are recorded through one entry point, so the case
// does not arise today; reading the union rather than assuming it keeps the
// answer from depending on that.
//
// The order is space, then name, then the producer's coordinates in the order
// they are declared on the label — a total order derived from the declared
// strings alone. The walks that feed the accumulator are not all fixed by the
// input: a boundary-scope feature's declarations are reached through a plain Go
// map, whose iteration order is randomised per run, so answering in arrival
// order would give identical input a different sequence on different runs.
// Nothing here reaches rendered output, so the ordered-map type would buy
// nothing and sorted order is the meaning wanted.
//
// 🔴 Two contributors are one contributor here exactly when every coordinate of
// their labels agrees, which is what makes the grouping sound: the producer
// travels as a value with its coordinates apart, so a pair differing in any one
// of them cannot arrive as a single entry. A label flattened to one string on
// the way in would put that soundness at the mercy of the flattening, and a
// flattening that folded would silently merge two declarers into one — leaving
// the rule below reading one contributor for a name that two derived, and
// reporting nothing.
//
// Nothing here reads a name. A space is an opaque string its declarer chose, a
// name is an opaque string its declarer derived, and this copies them out
// unexamined — no format is recognised, no provider is named, and no name is
// ever derived here.
func (s *derivedKeySet) declarations() []constraint.DerivedNameDeclaration {
	type entryKey struct {
		space, key string
		producer   derivedKeyProducer
	}
	repeated := make(map[entryKey]bool, len(s.repeats))
	for _, e := range s.repeats {
		repeated[entryKey{space: e.space, key: e.key, producer: e.contributor.producer}] = true
	}

	seen := make(map[entryKey]bool, len(s.entries)+len(s.repeats))
	keys := make([]entryKey, 0, len(s.entries)+len(s.repeats))
	// The dispositions of the several entries one contributor accumulates for one
	// name combine by conjunction, for the reason the ledger's own accumulation
	// does: output that reached the assembler on any one crossing puts the name
	// inside the assembled document, so standing standalone has to hold for every
	// entry before it holds at all. A name's first entry sets the value and each
	// later one can only narrow it.
	standalone := make(map[entryKey]bool, len(s.entries)+len(s.repeats))
	record := func(k entryKey, placedStandalone bool) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
			standalone[k] = placedStandalone
			return
		}
		standalone[k] = standalone[k] && placedStandalone
	}
	for _, e := range s.entries {
		record(entryKey{space: e.space, key: e.key, producer: e.contributor.producer},
			e.contributor.placedStandalone)
	}
	for _, e := range s.repeats {
		record(entryKey{space: e.space, key: e.key, producer: e.contributor.producer},
			e.contributor.placedStandalone)
	}
	if len(keys) == 0 {
		return nil
	}

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].space != keys[j].space {
			return keys[i].space < keys[j].space
		}
		if keys[i].key != keys[j].key {
			return keys[i].key < keys[j].key
		}
		if keys[i].producer.kind != keys[j].producer.kind {
			return keys[i].producer.kind < keys[j].producer.kind
		}
		if keys[i].producer.name != keys[j].producer.name {
			return keys[i].producer.name < keys[j].producer.name
		}
		return keys[i].producer.scope < keys[j].producer.scope
	})

	out := make([]constraint.DerivedNameDeclaration, 0, len(keys))
	for _, k := range keys {
		out = append(out, constraint.DerivedNameDeclaration{
			KeySpace:                     k.space,
			DerivedName:                  k.key,
			Contributor:                  k.producer.name,
			ContributorKind:              k.producer.kind,
			ContributorScope:             k.producer.scope,
			RepeatedWithinOneDeclaration: repeated[k],
			OutputPlacedStandalone:       standalone[k],
		})
	}
	return out
}

// derivedNameSets is the render's retained record of the names each dispatch
// group's contributors declared they derived.
//
// It is a retention rather than a read of the render's working state because that
// state is a loop-local: the accumulator each group fills as its contributors
// render is dropped when the render returns, and the presentation of the grouping
// outlives it. A reader asking what a group's contributors declared has this and
// nothing else.
//
// It is keyed by the group rather than by a node, because the group is the scope
// the question is asked at: a name displaces another only where the two outputs
// merge, and what merges is a group.
type derivedNameSets struct {
	// byGroup is looked up by group and never walked, so it holds no order to
	// keep: every reader arrives with a path and a provider in hand. The
	// declarations inside each entry carry their own order.
	byGroup map[dispatchKey][]constraint.DerivedNameDeclaration
}

// newDerivedNameSets records what each group's contributors declared they
// derived, at the point every contributor has been sorted into its group.
//
// It is taken here because this is the first point the record is complete and the
// last point at which acting on it still saves work: every contributor has
// rendered, so every declaration a group will ever hold is already in it, and a
// duplicate is settled entirely by the declarations in hand — no assembly output
// is needed to see one, and stopping here stops the build before an assembler
// merges output whose names were already known to collide.
//
// A group whose contributors declared nothing records nothing, which is the same
// answer a group no reader ever asks about gets: there is no name to compare.
func newDerivedNameSets(
	groups map[dispatchKey]*pendingDispatch,
	keys []dispatchKey,
) *derivedNameSets {
	out := &derivedNameSets{
		byGroup: make(map[dispatchKey][]constraint.DerivedNameDeclaration, len(keys)),
	}
	for _, key := range keys {
		group := groups[key]
		if group == nil {
			continue
		}
		if declared := group.derivedKeys.declarations(); len(declared) > 0 {
			out.byGroup[key] = declared
		}
	}
	return out
}

// declarations returns what one group's contributors declared they derived, and
// nothing for a group nothing was recorded for.
func (d *derivedNameSets) declarations(
	assemblyPath, provider string,
) []constraint.DerivedNameDeclaration {
	return d.byGroup[dispatchKey{Path: assemblyPath, Provider: provider}]
}

// groupDerivedNames returns the names the contributors to one dispatch group
// declared they derived inside their own output.
//
// It is unexported for the reason the group's other readers are: its only readers
// are in this package — the presentation of what the grouping holds — and one walk
// is what keeps a coordinate a rule runs over and the value it reads from being
// derived twice. A render that recorded nothing answers nothing, which is the same
// answer a group whose contributors declared nothing gets.
func (r *RenderResult) groupDerivedNames(
	assemblyPath, provider string,
) []constraint.DerivedNameDeclaration {
	if r.derivedNames == nil {
		return nil
	}
	return r.derivedNames.declarations(assemblyPath, provider)
}
