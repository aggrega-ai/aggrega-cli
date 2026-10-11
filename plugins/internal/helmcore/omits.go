// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import "github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"

// omitSelection is one chart render's omit entries and how many documents each has
// matched so far. It is this provider's one home of what an omit entry selects:
// every rendered document of the chart passes through it — a templated document and
// a crds/ file alike, because both are emitted through one path — and a document
// any entry matches is left out of what the render returns.
//
// An entry matches a document by the document's own words, the ones its emitted
// file carries: its kind equals the document's kind, its name equals the document's
// metadata.name, and, only where the entry names a namespace, that namespace equals
// the one the document is in. That is the namespace the document writes, or the
// one the render writes into it from the node's declared namespace where the chart
// left it without one — which is what the emitted file then carries — and never the
// cluster default a render without a declared namespace falls back to: a document
// the render leaves without a namespace is in none, and an entry naming one does not
// match it.
//
// A document carrying no kind or no metadata.name has no identity an entry can
// name, so it matches no entry, whatever the entry says.
type omitSelection struct {
	entries []rendercore.ResolvedOmit
	counts  []int
}

// newOmitSelection prepares the selection for a render handed entries. A render
// handed none gets a selection that leaves nothing out and reports no counts.
func newOmitSelection(entries []rendercore.ResolvedOmit) *omitSelection {
	s := &omitSelection{entries: entries}
	if len(entries) > 0 {
		s.counts = make([]int, len(entries))
	}
	return s
}

// leaveOut reports whether a document is left out, counting it against every entry
// that matches it. A document two entries match is counted once for each of them,
// because each entry's count is the number of documents that entry matched, whether
// or not another entry matched them too.
func (s *omitSelection) leaveOut(d renderedDocument) bool {
	if len(s.entries) == 0 || d.kind == "" || d.name == "" {
		return false
	}
	matched := false
	for i, entry := range s.entries {
		if omitMatches(entry.Entry, d) {
			s.counts[i]++
			matched = true
		}
	}
	return matched
}

// omitted returns one count per entry the render was handed, in the order the
// entries arrived, and nil for a render handed none.
func (s *omitSelection) omitted() []int {
	return s.counts
}

// omitMatches reports whether one entry names one document.
func omitMatches(entry rendercore.OmitEntry, d renderedDocument) bool {
	if entry.Kind != d.kind || entry.Name != d.name {
		return false
	}
	return entry.Namespace == "" || entry.Namespace == d.namespace
}
