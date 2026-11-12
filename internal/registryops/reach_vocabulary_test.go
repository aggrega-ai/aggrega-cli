// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package registryops

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// TestTheWriteTargetScopeSpellingsAgreeWithTheOperationVocabulary holds the one
// thing this package's Scope and the operation set's reach vocabulary share: the
// word each scope is spelled with.
//
// The two types stay separate — one classifies the file a configuration write
// resolved to, the other an operation — and this package's own documentation says
// so in terms: the reach "is not a property of the operation — it is a property of
// the file the write resolved to". So nothing in the compiler makes them agree.
// What makes the agreement worth holding is the reader: a caller is told which
// scope an operation reaches before it runs, and is then told the scope of the
// file the write landed in, and the two answers should be the same word for the
// same scope rather than two words a reader has to learn are synonyms.
//
// The comparison is written against the constants on both sides rather than
// against a value either side computes. That near-duplication is the mechanism: a
// single shared constant cannot disagree with itself, and disagreement is the
// whole signal here.
//
// It is the third of these, and the third is what makes the pair before it a set.
// Two of three vocabularies held in step reads exactly like all of them being
// held, because the one nobody pinned fails silently — a rename on this side
// would have left a caller meeting one scope under two words with every gate
// green.
func TestTheWriteTargetScopeSpellingsAgreeWithTheOperationVocabulary(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write Scope
		op    opsurface.Reach
	}{
		{"machine", ScopeMachine, opsurface.ReachMachine},
		{"workspace", ScopeWorkspace, opsurface.ReachWorkspace},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A control first: two empty strings would satisfy the comparison
			// below while telling a reader nothing at all.
			if string(tc.write) == "" {
				t.Fatal("this package spells the scope as the empty string, so the comparison below asserts nothing")
			}
			if string(tc.write) != string(tc.op) {
				t.Errorf("a configuration write reports this scope as %q and an operation declares it as %q; "+
					"a reader meets one scope under two words", string(tc.write), string(tc.op))
			}
		})
	}
}
