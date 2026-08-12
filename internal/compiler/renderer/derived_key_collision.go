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

// derivedKeyDeclaration is one contributor's declaration that it derived one
// name into one of its own key spaces: the space, the name, and the
// authoring-level name of whatever derived it.
type derivedKeyDeclaration struct {
	space    string
	key      string
	producer string
}

// derivedKeySet accumulates every derived name the contributors to one assembled
// output declare, then answers a single question about them: within each
// declared space, is the set of declared names injective?
//
// The direction is inverted from the collision guards that read output. A
// provider guest derives the names its output addresses resources by, and it
// puts them inside the file bytes the host writes verbatim — so the host cannot
// see them, and reading them back would mean parsing the provider's format in
// the host, which is the domain knowledge the Wasm boundary keeps out. The guest
// declares them instead. What arrives here is therefore already the answer to
// every provider-specific question; nothing below interprets it.
//
// Blindness is the property that makes one guard enough for every provider. A
// space is an opaque string the guest chose, a key is an opaque string the guest
// derived, and this compares keys for equality within a space and does nothing
// else — no format is recognised, no provider is named, no key's meaning is
// learned, and no key is ever derived here. A second provider declaring an
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
}

// put records that producer declared key in space. Whether that declaration
// collides with another is not decided here — see resolve.
func (s *derivedKeySet) put(space, key, producer string) {
	s.entries = append(s.entries, derivedKeyDeclaration{space: space, key: key, producer: producer})
}

// putAll records every name a contributor declared across every space it
// declared, reading the declaration in the guest's own order. Iterating the
// ordered declaration rather than a plain map is what keeps the recorded order a
// function of the input; resolve orders the diagnostic itself, so this ordering
// affects nothing downstream, but it keeps the accumulator's own contents stable
// for anything that later reads them.
func (s *derivedKeySet) putAll(declared types.OrderedMap[[]string], producer string) {
	for space, keys := range declared.All() {
		for _, key := range keys {
			s.put(space, key, producer)
		}
	}
}

// mergeDerivedKeys folds one render crossing's declaration into a node's
// accumulating set, appending each space's names to whatever that space already
// carries rather than replacing them.
//
// Merging rather than replacing is load-bearing: a node renders across more than
// one crossing — a source render, a workload synthesis, an artifacts placement —
// and each returns its own declaration. Replacing would keep only the last
// crossing's names and silently drop the rest, which is exactly the kind of quiet
// loss this whole check exists to refuse. Each space's names are copied into a
// fresh slice so no accumulated space aliases a caller's backing array.
func mergeDerivedKeys(into *types.OrderedMap[[]string], declared types.OrderedMap[[]string]) {
	for space, keys := range declared.All() {
		existing, _ := into.Get(space)
		merged := make([]string, 0, len(existing)+len(keys))
		merged = append(merged, existing...)
		merged = append(merged, keys...)
		into.Set(space, merged)
	}
}

// resolve reports nothing when every declared space carries each of its names
// once, and halts naming every collision otherwise.
//
// Every collision is reported rather than the first, so one build tells the
// author the whole picture instead of one collision at a time.
// Producers are collected as a SET rather than a list, which is what makes one
// contributor repeating a name across its several render crossings — a source
// render, a workload synthesis, an artifacts placement — read as one declarer
// rather than as a contributor colliding with itself. The set's size is then the
// collision test directly.
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

	var collided []spaceKey
	for sk, producers := range declarers {
		if len(producers) > 1 {
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
		sort.Strings(names)
		clauses = append(clauses, fmt.Sprintf("in key space %q the name %q is derived by %s",
			sk.space, sk.key, joinWithAnd(names)))
	}
	inner := fmt.Errorf("%w: assembly path %q provider %q: %s; change what one of them derives so each name is derived once within its space",
		ErrDerivedKeyCollision(), groupPath, provider, strings.Join(clauses, "; "))
	// The location is left empty rather than set to the group path, the way the
	// other group-scoped halts here do it: the message already names the assembly
	// path and the provider, and setting the location too prefixes the rendered
	// error with a path it goes on to repeat a few words later.
	return cerrors.Wrap(inner, cerrors.TranslationError, cerrors.SourceLocation{}, inner.Error())
}
