// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package yamlorder

import (
	"reflect"
	"testing"
)

// TestKeyOrderReadsTheOrderAFileWritesItsKeysIn reads the keys of a block mapping
// at a path in the order the text writes them, and leaves a mapping written in
// flow style, or a key the text does not write beneath the path, in sorted order
// after them. An empty path reads the top-level mapping, past a document marker.
func TestKeyOrderReadsTheOrderAFileWritesItsKeysIn(t *testing.T) {
	t.Parallel()
	text := []byte("# a comment\nname: x\nservices:\n  zeta:\n    image: a\n    environment:\n      B: 1\n      A: 2\n" +
		"  \"alpha\": &anchor\n    image: b\n  mid: {image: c}\nmodels: {m2: {model: x}, m1: {model: y}}\n")
	for _, row := range []struct {
		path        []string
		names, want []string
	}{
		{[]string{"services"}, []string{"alpha", "mid", "zeta"}, []string{"zeta", "alpha", "mid"}},
		{[]string{"services", "zeta", "environment"}, []string{"A", "B"}, []string{"B", "A"}},
		{[]string{"models"}, []string{"m1", "m2"}, []string{"m1", "m2"}},
		{[]string{"services"}, []string{"alpha", "extra", "zeta"}, []string{"zeta", "alpha", "extra"}},
		{nil, []string{"models", "name", "services"}, []string{"name", "services", "models"}},
	} {
		if got := KeyOrder(text, row.path, row.names); !reflect.DeepEqual(got, row.want) {
			t.Errorf("KeyOrder(%v, %v) = %v, want %v", row.path, row.names, got, row.want)
		}
	}
	marked := []byte("---\nzeta:\n  role: a\nalpha:\n  role: b\n")
	if got := KeyOrder(marked, nil, []string{"alpha", "zeta"}); !reflect.DeepEqual(got, []string{"zeta", "alpha"}) {
		t.Errorf("KeyOrder of a document opened by a marker = %v, want [zeta alpha]", got)
	}
}
