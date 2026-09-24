// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Holds two derivations of one set against each other, from the only side that
// can reach both.
//
// The authored structured-source keys this package reads a source's fetch
// coordinates out of, and the keys the source-reference leaf reads a source's
// IDENTITY out of, are the same set asked about from two packages that share no
// symbol. That leaf imports nothing of this package's and cannot import it, so the
// correspondence has no structural direction available to it and nothing but this
// compares the two.
//
// The failure it exists for is silent by construction: a seventh coordinate
// taught to one side alone changes no behaviour anybody asserts, so every other
// test in this package stays green while the identity reading answers about a
// source it no longer fully reads.

package closure

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
)

// TestSourceSpecFieldsAndTheIdentityReadingAreOneSet fails, naming the key, when
// either side carries an authored key the other does not.
//
// The two directions are different faults and are reported apart. A key this
// package fetches on but the identity reading ignores would call a source's
// identity knowable to its author while a template still decided which artifact
// it named. A key the identity reading consults but no coordinate here reads
// would call it unknowable over text that decides nothing.
//
// A disagreement is a decision to take rather than a line to delete. Either the
// new key belongs in both sets, or it is a coordinate that genuinely does not fix
// identity — and that second case is the identity reading learning to separate
// them, not this guard going away.
func TestSourceSpecFieldsAndTheIdentityReadingAreOneSet(t *testing.T) {
	t.Parallel()

	fetched := make(map[string]bool, len(sourceSpecFields))
	for _, field := range sourceSpecFields {
		fetched[field.key] = true
	}
	identity := map[string]bool{}
	for _, key := range sourceref.IdentityFields() {
		identity[key] = true
	}

	for _, field := range sourceSpecFields {
		if !identity[field.key] {
			t.Errorf("spec.source key %q is read into a fetch coordinate here, and the identity reading does not consult it: "+
				"a template in it would leave the identity reported knowable", field.key)
		}
	}
	for _, key := range sourceref.IdentityFields() {
		if !fetched[key] {
			t.Errorf("spec.source key %q is consulted by the identity reading, and no fetch coordinate here reads it: "+
				"a template in it would report an identity unknowable over text that decides nothing", key)
		}
	}
}

// TestEveryDeclaredSourceSpecFieldReachesItsCoordinate drives each declared key
// through the spec builder and reads the coordinate it claims to fill.
//
// It is the half the set comparison above cannot do: a table entry naming a key
// and assigning nothing, or assigning to the wrong coordinate, agrees with the
// identity reading perfectly and fetches the wrong artifact. The value written is
// the key's own name, so a row that fills its neighbour's coordinate reports which
// one it took.
func TestEveryDeclaredSourceSpecFieldReachesItsCoordinate(t *testing.T) {
	t.Parallel()

	read := map[string]func(*SourceSpec) string{
		"kind":          func(s *SourceSpec) string { return s.Kind },
		"url":           func(s *SourceSpec) string { return s.URL },
		"version":       func(s *SourceSpec) string { return s.Version },
		"chart":         func(s *SourceSpec) string { return s.Chart },
		"ref":           func(s *SourceSpec) string { return s.Ref },
		"module_subdir": func(s *SourceSpec) string { return s.ModuleSubdir },
	}

	for _, field := range sourceSpecFields {
		coordinate, known := read[field.key]
		if !known {
			t.Errorf("declared key %q has no coordinate this test knows how to read; teach it one here rather than dropping the row", field.key)
			continue
		}
		spec := &SourceSpec{}
		field.assign(spec, field.key)
		if got := coordinate(spec); got != field.key {
			t.Errorf("key %q assigns nothing to its own coordinate: read %q back", field.key, got)
		}
		for other, otherCoordinate := range read {
			if other == field.key {
				continue
			}
			if got := otherCoordinate(spec); got != "" {
				t.Errorf("key %q wrote %q into the %q coordinate", field.key, got, other)
			}
		}
	}
}
