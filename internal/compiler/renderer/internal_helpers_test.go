// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestNodeKindName_EveryDocumentedKind pins the diagnostic-formatter
// helper used in CATALOG_ERROR messages. Every yaml.Kind plus the
// zero-value fall-through must produce a stable name string.
func TestNodeKindName_EveryDocumentedKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind yaml.Kind
		want string
	}{
		{yaml.DocumentNode, "document"},
		{yaml.SequenceNode, "sequence"},
		{yaml.MappingNode, "mapping"},
		{yaml.ScalarNode, "scalar"},
		{yaml.AliasNode, "alias"},
	}
	for _, c := range cases {
		if got := nodeKindName(c.kind); got != c.want {
			t.Errorf("nodeKindName(%v) = %q; want %q", c.kind, got, c.want)
		}
	}
	// Unknown kind (zero value or out-of-range) falls through.
	if got := nodeKindName(yaml.Kind(99)); got == "" {
		t.Errorf("unknown kind: expected non-empty diagnostic; got empty")
	}
}
