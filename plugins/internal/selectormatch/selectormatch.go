// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package selectormatch reads a catalog selector against a set of labels held
// as a plain map, for the importer guests built on the plugin kit: the kit hands
// a platform's matcher the labels of a Component as a map and carries no
// selector grammar of its own, and this package is the one place the importers
// read a selector in the compiler's own label-selector grammar, the grammar a
// catalog Component writes its sockets' selectors in.
package selectormatch

import (
	"maps"
	"slices"

	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Match reports whether labels satisfy sel, read in the compiler's
// label-selector grammar, and returns the grammar's refusal of a selector that
// does not read. The labels are handed to the grammar in key order, so the
// answer depends on the labels alone and never on the order a map yields them
// in.
func Match(sel string, labels map[string]string) (bool, error) {
	ordered := types.NewOrderedMap[string]()
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		ordered.Set(k, labels[k])
	}
	return selector.MatchString(sel, *ordered)
}
