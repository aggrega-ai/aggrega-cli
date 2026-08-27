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

// declareDerivedKey records that the compiler itself derived one name into one
// key space, folding it into an accumulator through the same merge a guest's
// declaration takes.
//
// It exists so there is one accumulation mechanic rather than two. The
// declarations a provider guest makes arrive over the node context's sink and
// are folded in by mergeDerivedKeys; a name the compiler derives has no
// boundary to cross and needs no carrier, but it must accumulate identically or
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
// name into one of its own key spaces: the space, the name, and the
// authoring-level name of whatever derived it together with what kind of thing
// that is.
//
// The kind and the name are held apart rather than as one composed label. A
// reader spelling them into a sentence needs the kind as a word and the name as
// a value it quotes itself, and a label carrying its own quotes would be quoted
// again. Both come from the one classifier the render uses everywhere it names a
// contributor.
type derivedKeyDeclaration struct {
	space        string
	key          string
	producer     string
	producerKind string
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
// another's. A guest needing uniqueness across a narrower or wider scope
// expresses that scope in the space name it declares, which keeps the choice in
// the guest and out of the host.
type derivedKeySet struct {
	entries []derivedKeyDeclaration
	repeats []derivedKeyDeclaration
}

// put records that producer, a contributor of kind producerKind, declared key in
// space. Whether that declaration collides with another is not decided here: the
// set answers what was declared and the rule declared over the grouping decides.
func (s *derivedKeySet) put(space, key, producerKind, producer string) {
	s.entries = append(s.entries, derivedKeyDeclaration{
		space: space, key: key, producer: producer, producerKind: producerKind,
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
	producerKind, producer string,
) {
	for space, keys := range declared.All() {
		for _, key := range keys {
			s.put(space, key, producerKind, producer)
		}
	}
	for _, r := range repeated {
		s.repeats = append(s.repeats, derivedKeyDeclaration{
			space: r.space, key: r.key, producer: producer, producerKind: producerKind,
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

// declarations is every declaration this group's contributors made, one entry
// per contributor per name per space, each carrying whether one of that
// contributor's own declarations held the name more than once.
//
// It answers what was declared and decides nothing. Whether a name collides is
// the question a rule declared over the grouping asks of this answer, and
// keeping the two apart is what leaves the rule something to decide: an answer
// that already said which names collided would BE the rule.
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
// The order is space, then name, then contributor kind, then contributor — a
// total order derived from the declared strings alone. The walks that feed the
// accumulator are not all fixed by the input: a boundary-scope feature's
// declarations are reached through a plain Go map, whose iteration order is
// randomised per run, so answering in arrival order would give identical input a
// different sequence on different runs. Nothing here reaches rendered output, so
// the ordered-map type would buy nothing and sorted order is the meaning wanted.
//
// Nothing here reads a name. A space is an opaque string its declarer chose, a
// name is an opaque string its declarer derived, and this copies them out
// unexamined — no format is recognised, no provider is named, and no name is
// ever derived here.
func (s *derivedKeySet) declarations() []constraint.DerivedNameDeclaration {
	type entryKey struct{ space, key, kind, producer string }
	repeated := make(map[entryKey]bool, len(s.repeats))
	for _, e := range s.repeats {
		repeated[entryKey{space: e.space, key: e.key, kind: e.producerKind, producer: e.producer}] = true
	}

	seen := make(map[entryKey]bool, len(s.entries)+len(s.repeats))
	keys := make([]entryKey, 0, len(s.entries)+len(s.repeats))
	record := func(k entryKey) {
		if seen[k] {
			return
		}
		seen[k] = true
		keys = append(keys, k)
	}
	for _, e := range s.entries {
		record(entryKey{space: e.space, key: e.key, kind: e.producerKind, producer: e.producer})
	}
	for k := range repeated {
		record(k)
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
		if keys[i].kind != keys[j].kind {
			return keys[i].kind < keys[j].kind
		}
		return keys[i].producer < keys[j].producer
	})

	out := make([]constraint.DerivedNameDeclaration, 0, len(keys))
	for _, k := range keys {
		out = append(out, constraint.DerivedNameDeclaration{
			KeySpace:                     k.space,
			DerivedName:                  k.key,
			Contributor:                  k.producer,
			ContributorKind:              k.kind,
			RepeatedWithinOneDeclaration: repeated[k],
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
