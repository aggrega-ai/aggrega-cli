// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
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

// onContext is what a template reads of this caller's facts: keyed by name, the
// caller's own render calls first and then each of its attachments in the order
// they rendered, so a name more than one of them reported holds one list joining
// theirs in that order.
func (c callerRenderFacts) onContext() (*types.OrderedMap[[]any], error) {
	reports := make([][]RenderFact, 0, 1+c.Attachments.Len())
	reports = append(reports, c.Own)
	for _, facts := range c.Attachments.All() {
		reports = append(reports, facts)
	}
	return renderFactsOnContext(reports...)
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

// renderFactsOnContext keys reported render facts by name for a template to read.
//
// The reports are taken in the order given, and each report's facts in the order
// its guest listed them; a name met again joins its list to the one already held,
// so the lists one name gets from several renders read as one list in the order
// those renders ran. The names keep the order they were first met in. Each value
// is the guest's JSON list, decoded so the template can range over it, with every
// object inside it keeping its keys in the order the guest wrote them — the same
// decode an ordered map gives a document's values, so nothing here is a second
// reading of JSON.
//
// The result is never nil: a context that promises the facts carries them even
// when nothing was reported, so a template reading a name nobody reported reads
// an empty list rather than failing on a missing parent.
//
// A value that is not a list is an error. The socket refuses such a value when a
// guest returns it, so reaching one here means a report arrived by some other
// path, and joining it would hand a template something no provider promised.
func renderFactsOnContext(reports ...[]RenderFact) (*types.OrderedMap[[]any], error) {
	facts := types.NewOrderedMap[[]any]()
	for _, report := range reports {
		for _, fact := range report {
			decoded, err := types.DecodeJSONValue(fact.Value)
			if err != nil {
				return nil, fmt.Errorf("render fact %q: decoding its value: %w", fact.Name, err)
			}
			entries, isList := decoded.([]any)
			if !isList {
				return nil, fmt.Errorf("render fact %q: its value is not a list, and every render fact's value is one", fact.Name)
			}
			// A name met for the first time starts from an empty list rather than a
			// nil one: appending nothing to nil stays nil, so a name whose every
			// report was an empty list would reach a template as null, not as the
			// list every render fact is.
			held, seen := facts.Get(fact.Name)
			if !seen {
				held = make([]any, 0, len(entries))
			}
			facts.Set(fact.Name, append(held, entries...))
		}
	}
	return facts, nil
}

// usedByWithCallerFacts returns the connections made to a node with each
// caller's render facts on its entry, for the context of an attachment whose
// FeatureRecipe declares it reads them.
//
// The connections are copied rather than written in place: the node's own list
// is the one every other attachment of the node is handed, and an attachment that
// does not declare the read must never meet the facts on it. Every entry carries
// the facts, empty for a caller whose providers reported nothing, so a template
// ranging the callers reads each one's facts the same way.
//
// callers is what gatherCallerRenderFacts read for the same connections, one
// entry per connection in the same order; a list that does not line up with the
// connections is refused rather than paired by position with the wrong caller.
func usedByWithCallerFacts(inbound []types.InboundEdge, callers []callerRenderFacts) ([]types.InboundEdge, error) {
	if len(callers) != len(inbound) {
		return nil, fmt.Errorf("internal error: %d connection(s) made to the node but %d caller report(s) gathered for them",
			len(inbound), len(callers))
	}
	out := make([]types.InboundEdge, len(inbound))
	for i, edge := range inbound {
		if callers[i].ComponentID != edge.ComponentID {
			return nil, fmt.Errorf("internal error: connection %d is made by %q but the report gathered for it is %q's",
				i, edge.ComponentID, callers[i].ComponentID)
		}
		facts, err := callers[i].onContext()
		if err != nil {
			return nil, fmt.Errorf("caller %q: %w", edge.ComponentID, err)
		}
		edge.RenderFacts = facts
		out[i] = edge
	}
	return out, nil
}
