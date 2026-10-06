// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"slices"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// renderFactsRecord is what one node's renders reported about what they emitted,
// as the node's own turn of the walk left it: the facts its own render reported,
// and those each attachment rendered in that turn reported, by instance name.
//
// It is a copy rather than a view of the node's state, and the copy is the point.
// An attachment held for the pass after the node walk renders onto its host's
// state too, so a view taken during the walk would come to include what a held
// attachment reported once that attachment had rendered — and a held attachment
// reading another's output is the dependency the pass is shaped to rule out.
type renderFactsRecord struct {
	own         []RenderFact
	attachments types.OrderedMap[[]RenderFact]
}

// renderFactsAsItStands copies what the node's renders have reported so far.
//
// Each fact list is copied, and the attachment record is rebuilt rather than
// shared: an ordered map shares its storage with every copy of the value, so a
// later write to the state's record would reach this one through it.
func (s *PhaseBState) renderFactsAsItStands() renderFactsRecord {
	rec := renderFactsRecord{own: slices.Clone(s.RenderFacts)}
	for instance, facts := range s.AttachmentRenderFacts.All() {
		rec.attachments.Set(instance, slices.Clone(facts))
	}
	return rec
}

// callerRenderFacts is what one connection's caller reported about its renders,
// as a held attachment on the node it connects to is handed it.
//
// The caller's own render and the attachments rendered in its own turn are kept
// apart, as the caller's state keeps them, so a fact one of its attachments
// reported never reads as one the caller itself rendered. What a held attachment
// of the caller reported is absent: a held attachment never reads another's
// output.
type callerRenderFacts struct {
	// ComponentID is the caller, as the connection names it.
	ComponentID string

	// Own are the facts the caller's own render reported, in the order they
	// arrived.
	Own []RenderFact

	// Attachments are the facts each attachment rendered in the caller's own turn
	// reported, by the instance name it was attached under, in the order the
	// attachments rendered.
	Attachments types.OrderedMap[[]RenderFact]
}

// gatherCallerRenderFacts reads, for each connection made to a node, what that
// connection's caller reported, from the record each node's turn left.
//
// One entry per connection, in the order the connections are held, so the
// entries line up one for one with the node's inbound connections — a caller
// connecting twice appears twice. A caller with no record reports nothing, which
// is the same answer as a caller whose provider reports nothing.
func gatherCallerRenderFacts(
	inbound []types.InboundEdge,
	recorded map[string]renderFactsRecord,
) []callerRenderFacts {
	if len(inbound) == 0 {
		return nil
	}
	out := make([]callerRenderFacts, 0, len(inbound))
	for _, edge := range inbound {
		rec := recorded[edge.ComponentID]
		out = append(out, callerRenderFacts{
			ComponentID: edge.ComponentID,
			Own:         rec.own,
			Attachments: rec.attachments,
		})
	}
	return out
}

// attachmentRenderRecord is what one component-scope attachment's render was
// handed: which attachment, whether it rendered in the pass after every node had
// rendered or in its host's turn, and what it was handed of its host's callers'
// renders.
type attachmentRenderRecord struct {
	Host          string
	Instance      string
	AfterTheNodes bool
	CallerFacts   []callerRenderFacts
}

// attachmentRenderLog is the render's record of every component-scope
// attachment's render, in the order they rendered.
//
// It is kept beside the render's output because which pass an attachment
// rendered in, and what it was handed there, leave no mark on an output that
// reads none of it — so they are recorded where they are decided rather than
// inferred from what the attachment emitted. Its only reader is this package's
// tests. A nil log records nothing.
type attachmentRenderLog struct {
	records []attachmentRenderRecord
}

// record appends one attachment render to the log.
func (l *attachmentRenderLog) record(id attachmentIdentity, afterTheNodes bool, callers []callerRenderFacts) {
	if l == nil {
		return
	}
	l.records = append(l.records, attachmentRenderRecord{
		Host:          id.host,
		Instance:      id.instance,
		AfterTheNodes: afterTheNodes,
		CallerFacts:   callers,
	})
}
