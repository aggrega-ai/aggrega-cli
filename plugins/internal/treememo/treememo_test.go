// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package treememo

import (
	"encoding/json"
	"testing"
)

// TestOfBuildsOncePerTreeAndNeverServesAnotherTreesValue asks one Memo about a
// sequence of trees, as the sources of one import and of the next import a
// guest instance serves would ask it, and holds it to building a tree's value
// only where the tree before was not of the same bytes: the same slice again and
// a copy of equal bytes are served the value held; a tree of other bytes —
// another length, or the same length with one byte changed — is built afresh,
// and so is a tree equal to one served two trees earlier, since a Memo holds one
// tree's value. Every value served is the one built from that tree's bytes.
//
// 🔴 PLANTED DEFECT: in sameBytes, replace `return bytes.Equal(a, b)` with
// `return bytes.Equal(a, b) || true`: the row of the same length with one byte
// changed fails, served the value of the tree before it.
func TestOfBuildsOncePerTreeAndNeverServesAnotherTreesValue(t *testing.T) {
	t.Parallel()
	first := json.RawMessage(`{"nodes":[{"id":"a"}]}`)
	other := json.RawMessage(`{"nodes":[{"id":"b"},{"id":"c"}]}`)
	sameLength := json.RawMessage(`{"nodes":[{"id":"z"}]}`)
	var m Memo[string]
	builds := 0
	build := func(tree json.RawMessage) string {
		builds++
		return string(tree)
	}
	for _, tc := range []struct {
		name  string
		tree  json.RawMessage
		built bool
	}{
		{name: "the first tree", tree: first, built: true},
		{name: "the same slice again", tree: first},
		{name: "a copy of equal bytes", tree: append(json.RawMessage(nil), first...)},
		{name: "another length", tree: other, built: true},
		{name: "the first tree after another", tree: first, built: true},
		{name: "the same length, one byte changed", tree: sameLength, built: true},
		{name: "no tree", tree: nil, built: true},
		{name: "an empty tree after no tree", tree: json.RawMessage{}},
	} {
		before := builds
		got := m.Of(tc.tree, build)
		if built := builds > before; built != tc.built {
			t.Errorf("%s: built = %v, want %v", tc.name, built, tc.built)
		}
		if got != string(tc.tree) {
			t.Errorf("%s: served %q, want the value built from %q", tc.name, got, tc.tree)
		}
	}
}
