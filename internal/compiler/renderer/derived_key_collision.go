// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"sort"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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
// authoring-level name of whatever derived it.
type derivedKeyDeclaration struct {
	space    string
	key      string
	producer string
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
// output declare, then answers a single question about them: within each
// declared space, is the set of declared names injective?
//
// A name fails that question two ways, and both are the same defect — the name
// is not unique in its space — so one halt covers both. Two contributors can
// arrive at one name, which the accumulated declarations show directly. Or one
// contributor's single declaration can carry one name twice in one space, which
// the accumulated declarations cannot show: a contributor legitimately restates
// a name across its several render crossings, and by the time the declarations
// accumulate here the two cases are the same pair of entries. That case is
// therefore recognised where it is still visible — at the moment one
// declaration arrives, before it merges with the rest — and carried here.
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
// Blindness is the property that makes one guard enough for every provider. A
// space is an opaque string its declarer chose, a key is an opaque string its
// declarer derived, and this compares keys for equality within a space and does
// nothing else — no format is recognised, no provider is named, no key's meaning
// is learned, and no key is ever derived here. A second provider declaring an
// entirely different kind of name in an entirely different kind of space is
// covered by the same code, unchanged.
//
// The check runs in resolve rather than in put, for the same reason the assembled
// file set defers its own: halting at the moment of the second declaration would
// name whichever contributor happened to be walked first, and the walks upstream
// are over plain Go maps whose iteration order is randomised per run, so
// identical input would fail with a different message on different runs. resolve
// sees every declaration at once and orders the diagnostic by space, then key,
// then producer — making the message a function of the input alone.
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

// put records that producer declared key in space. Whether that declaration
// collides with another is not decided here — see resolve.
func (s *derivedKeySet) put(space, key, producer string) {
	s.entries = append(s.entries, derivedKeyDeclaration{space: space, key: key, producer: producer})
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
func (s *derivedKeySet) putAll(declared types.OrderedMap[[]string], repeated []derivedKeyRepeat, producer string) {
	for space, keys := range declared.All() {
		for _, key := range keys {
			s.put(space, key, producer)
		}
	}
	for _, r := range repeated {
		s.repeats = append(s.repeats, derivedKeyDeclaration{space: r.space, key: r.key, producer: producer})
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

// resolve reports nothing when every declared space carries each of its names
// once, and halts naming every collision otherwise.
//
// Every collision is reported rather than the first, so one build tells the
// author the whole picture instead of one collision at a time. A name collides
// when more than one contributor derived it, or when one contributor's single
// declaration carried it more than once.
//
// Producers are collected as a SET rather than a list, which is what makes one
// contributor repeating a name across its several render crossings — a source
// render, a workload synthesis, an artifacts placement — read as one declarer
// rather than as a contributor colliding with itself. That restatement is
// legitimate and stays legitimate: the set's size answers only the
// across-contributors half. The within-one-declaration half is answered by the
// repeats recorded alongside, because a set built here cannot tell a restatement
// across crossings from a duplicate inside one of them.
//
// A contributor that repeated a name is named with the rest and marked, so a
// name that both collides across contributors and repeats within one is reported
// once with everything true about it rather than twice.
//
// The sets are plain maps and their names are sorted before they are reported.
// Sorting is not decoration: it is what makes the message a total order derived
// from the declared strings alone, rather than from the order the accumulating
// walks happened to take. Nothing here reaches rendered output — a diagnostic is
// the only consumer — so the ordered-map type would buy nothing, and the meaning
// wanted here is sorted order rather than declaration order anyway.
func (s *derivedKeySet) resolve(groupPath, provider string) error {
	type spaceKey struct{ space, key string }
	declarers := make(map[spaceKey]map[string]bool, len(s.entries))
	for _, e := range s.entries {
		sk := spaceKey{space: e.space, key: e.key}
		if declarers[sk] == nil {
			declarers[sk] = map[string]bool{}
		}
		declarers[sk][e.producer] = true
	}
	repeaters := make(map[spaceKey]map[string]bool, len(s.repeats))
	for _, e := range s.repeats {
		sk := spaceKey{space: e.space, key: e.key}
		if repeaters[sk] == nil {
			repeaters[sk] = map[string]bool{}
		}
		repeaters[sk][e.producer] = true
	}

	var collided []spaceKey
	for sk, producers := range declarers {
		if len(producers) > 1 || len(repeaters[sk]) > 0 {
			collided = append(collided, sk)
		}
	}
	// A repeat whose declaration never reached the accumulator would otherwise go
	// unreported. Both are recorded through one entry point so the case does not
	// arise today; reading the union rather than assuming it keeps the report from
	// depending on that.
	for sk := range repeaters {
		if _, declared := declarers[sk]; !declared {
			collided = append(collided, sk)
		}
	}
	if len(collided) == 0 {
		return nil
	}
	sort.Slice(collided, func(i, j int) bool {
		if collided[i].space != collided[j].space {
			return collided[i].space < collided[j].space
		}
		return collided[i].key < collided[j].key
	})

	clauses := make([]string, 0, len(collided))
	for _, sk := range collided {
		names := make([]string, 0, len(declarers[sk]))
		for producer := range declarers[sk] {
			names = append(names, producer)
		}
		for producer := range repeaters[sk] {
			if !declarers[sk][producer] {
				names = append(names, producer)
			}
		}
		sort.Strings(names)
		// Sorted first, marked second: the mark travels with the producer it
		// describes and never influences where that producer sorts.
		rendered := make([]string, 0, len(names))
		for _, producer := range names {
			if repeaters[sk][producer] {
				producer += " (more than once)"
			}
			rendered = append(rendered, producer)
		}
		clauses = append(clauses, fmt.Sprintf("in key space %q the name %q is derived by %s",
			sk.space, sk.key, joinWithAnd(rendered)))
	}
	inner := fmt.Errorf("%w: assembly path %q provider %q: %s; change what is derived so each name is derived once within its space",
		ErrDerivedKeyCollision(), groupPath, provider, strings.Join(clauses, "; "))
	// The location is left empty rather than set to the group path, the way the
	// other group-scoped halts here do it: the message already names the assembly
	// path and the provider, and setting the location too prefixes the rendered
	// error with a path it goes on to repeat a few words later.
	return cerrors.Wrap(inner, cerrors.TranslationError, cerrors.SourceLocation{}, inner.Error())
}
