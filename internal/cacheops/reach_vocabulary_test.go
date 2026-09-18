// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cacheops

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// TestTheCacheReachSpellingsAgreeWithTheOperationVocabulary holds the one thing
// this package's Reach and the operation set's share: the word each scope is
// spelled with.
//
// The two types stay separate — one classifies a cache class, the other an
// operation, and each says so in its own documentation — so nothing in the
// compiler makes them agree. What makes the agreement worth holding is the
// reader: a caller is told which scope an operation reaches, and is then shown
// the scope of every class a reclamation acted on, and the two answers should be
// the same word for the same scope rather than two words a reader has to learn
// are synonyms.
//
// The comparison is written against the constants on both sides rather than
// against a value either side computes. That near-duplication is the mechanism:
// a single shared constant cannot disagree with itself, and disagreement is the
// whole signal here.
func TestTheCacheReachSpellingsAgreeWithTheOperationVocabulary(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cache Reach
		op    opsurface.Reach
	}{
		{"workspace", ReachWorkspace, opsurface.ReachWorkspace},
		{"machine", ReachMachine, opsurface.ReachMachine},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A control first: two empty strings would satisfy the comparison
			// below while telling a reader nothing at all.
			if string(tc.cache) == "" {
				t.Fatal("this package spells the scope as the empty string, so the comparison below asserts nothing")
			}
			if string(tc.cache) != string(tc.op) {
				t.Errorf("a cache class reports this scope as %q and an operation declares it as %q; "+
					"a reader meets one scope under two words", string(tc.cache), string(tc.op))
			}
		})
	}
}
