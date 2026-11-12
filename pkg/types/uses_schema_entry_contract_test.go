// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import "testing"

// TestUsesSchemaEntry_ContractNodesAreThePillarNodes holds the two readers of a
// socket's contract fields to the same fields: every node ContractNodes returns is
// the node PillarNode returns for exactly one pillar a socket constrains, and every
// such pillar's node is among them. A contract field one reader knows and the other
// does not would be skipped silently by whichever reader missed it.
func TestUsesSchemaEntry_ContractNodesAreThePillarNodes(t *testing.T) {
	t.Parallel()

	var entry UsesSchemaEntry
	pillars := []string{"values", "infra", "provides"}

	byPillar := map[string]bool{}
	for _, p := range pillars {
		if n := entry.PillarNode(p); n != nil {
			byPillar[p] = false
		}
	}
	for i, n := range entry.ContractNodes() {
		owner := ""
		for _, p := range pillars {
			if entry.PillarNode(p) == n {
				owner = p
			}
		}
		if owner == "" {
			t.Errorf("contract node %d is no pillar's node; PillarNode does not map the field it reads", i)
			continue
		}
		byPillar[owner] = true
	}
	for p, seen := range byPillar {
		if !seen {
			t.Errorf("pillar %q maps to a contract field ContractNodes does not return", p)
		}
	}
	if len(byPillar) == 0 {
		t.Fatal("PillarNode maps no pillar, so this comparison checks nothing")
	}
}
