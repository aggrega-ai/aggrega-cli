// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"strconv"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
)

// DecisionLine renders one mapping decision as the sentence a person reads:
//
//	mapped <node id> (<node type>) to <target> as a <mode>, <tier> at <score>
//
// Every value is the importer's own, unaltered: the node's identifier and type,
// the target it chose, how it emitted it, the tier of the choice, and the score
// of its top candidate — the highest score among the candidates it weighed, so
// the line does not depend on the order the importer listed them in.
//
// It is the one home of that sentence. The command line writes it on its error
// stream and the assistant's import tool writes it in its text answer, so the two
// cannot come to word one decision two ways; a protocol answer carries the
// decision itself rather than this sentence, and a client renders it from there.
//
// The score is written exactly as the importer returned it — the shortest decimal
// that reads back as the same number — so the line adds no digit the importer
// did not compute and drops none it did. For any score from a millionth upward
// those are also the digits a protocol answer encodes the number in.
//
// Two decisions the importer may return carry less than the sentence names, and
// each is rendered with the part it lacks left out rather than filled in. One the
// importer weighed no candidate for has no score to report, so the line ends at
// its tier. One that records no choice has no target, mode or tier, so it reads
// "made no choice for <node id> (<node type>)".
func DecisionLine(d abi.ImportDecision) string {
	node := d.SourceNode.ID + " (" + d.SourceNode.Type + ")"
	if d.Chosen == nil {
		return "made no choice for " + node
	}
	line := "mapped " + node + " to " + d.Chosen.Target + " as a " + d.Chosen.Mode + ", " + d.Chosen.Tier
	if len(d.Candidates) == 0 {
		return line
	}
	top := d.Candidates[0].Score
	for _, c := range d.Candidates[1:] {
		if c.Score > top {
			top = c.Score
		}
	}
	return line + " at " + strconv.FormatFloat(top, 'f', -1, 64)
}
