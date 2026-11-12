// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package treememo holds what an importer guest built on the plugin kit reads
// once off the tree of a source and is asked about many times: the kit hands
// each question a format answers about one node of a source — how the node
// reaches its part, how the flow reaches it, which model it selects — the whole
// tree of that source, so a format that reads its node list off the tree for
// every question reads the tree once per node and per question. A Memo reads it
// once per tree and serves every later question about that tree from what it
// read.
package treememo

import (
	"bytes"
	"encoding/json"
	"sync"
)

// Memo holds the value a function last built from a tree, and the tree it was
// built from. The function must build its value from the tree's bytes alone,
// and its value must not be changed by those it is handed to: Of serves the
// value again for a tree of the same bytes, and only for one.
//
// The zero Memo holds nothing. A Memo is safe for concurrent use.
type Memo[T any] struct {
	mu    sync.Mutex
	held  bool
	tree  json.RawMessage
	value T
}

// Of returns build(tree): the value the Memo holds where it was built from a
// tree of the same bytes as tree, and otherwise the value build returns, which
// the Memo then holds in place of the one before. So two sources of one import,
// and the sources of two imports one guest instance serves, are each read
// afresh, unless their trees are the same bytes, from which build builds the
// same value.
//
// A tree is the one the Memo was built from where it is the same slice — the
// Memo keeps that slice, so no later tree can be given its memory while the
// Memo holds it — and otherwise where its bytes are equal.
func (m *Memo[T]) Of(tree json.RawMessage, build func(json.RawMessage) T) T {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.held || !sameBytes(m.tree, tree) {
		m.value, m.tree, m.held = build(tree), tree, true
	}
	return m.value
}

// sameBytes reports whether a and b hold the same bytes: at once where they are
// the same slice, and otherwise by comparing them.
func sameBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 || &a[0] == &b[0] {
		return true
	}
	return bytes.Equal(a, b)
}
