// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// The compiler half of the sensitivity-declaration parity check.
//
// Two readers answer "which leaves does this schema declare sensitive" and
// neither can call the other: the compiler reads the compiled schema, where the
// declaration has become an extension keyword, while the publishing-linter
// guest reads the authored entity because it cannot run schema compilation. The
// duplication is structural, so it is held by a shared expectation rather than
// by eye.
//
// This file is one of the two halves. Both read
// plugins/publishing-linter/tests/fixtures/sensitivity_parity.json — the ONE
// source of cases — so a case is checked on both sides or on neither. Add a case
// to that file rather than inline here or in the reader's own unit tests; a case
// added inline is invisible to the other side.
//
// Every case's schema is written as JSON, which is valid YAML, so the bytes the
// guest receives and the bytes compiled here are identical.

package lintops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// sensitivityParityFixture is the shared case file, held by the publishing
// linter's crate because the guest reaches it without leaving its own tree.
const sensitivityParityFixture = "../../plugins/publishing-linter/tests/fixtures/sensitivity_parity.json"

type sensitivityParityCases struct {
	LeafCases []struct {
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
		Expect struct {
			Values   []string `json:"values"`
			Infra    []string `json:"infra"`
			Provides []string `json:"provides"`
		} `json:"expect"`
	} `json:"leafCases"`
	SlotCases []struct {
		Name            string          `json:"name"`
		Slot            json.RawMessage `json:"slot"`
		ExpectSensitive bool            `json:"expectSensitive"`
		CompilerDecodes bool            `json:"compilerDecodes"`
	} `json:"slotCases"`
}

func loadSensitivityParityCases(t *testing.T) sensitivityParityCases {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(sensitivityParityFixture))
	if err != nil {
		t.Fatalf("read shared parity fixture %s: %v", sensitivityParityFixture, err)
	}
	var cases sensitivityParityCases
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("parse shared parity fixture %s: %v", sensitivityParityFixture, err)
	}
	return cases
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameSet(got map[string]bool, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, w := range want {
		if !got[w] {
			return false
		}
	}
	return true
}

// TestSensitivityLeafParityAgainstSharedFixture runs every shared case through
// the compiler's reader. The expectation in the fixture is the compiler's truth
// by definition, so this test is what establishes it — and what fails when a
// change to compilation or to the reader moves it out from under the guest.
func TestSensitivityLeafParityAgainstSharedFixture(t *testing.T) {
	t.Parallel()
	cases := loadSensitivityParityCases(t)
	if len(cases.LeafCases) < 30 {
		t.Fatalf("the shared fixture lost cases (%d left); it is the only source of cases for both sides", len(cases.LeafCases))
	}

	for _, c := range cases.LeafCases {
		var node yaml.Node
		if err := yaml.Unmarshal(c.Schema, &node); err != nil {
			t.Errorf("%s: schema does not parse: %v", c.Name, err)
			continue
		}
		compiled, err := compactform.Compile(&node)
		if err != nil {
			t.Errorf("%s: schema does not compile: %v", c.Name, err)
			continue
		}
		declared := resolver.DeclaredSensitiveLeaves(compiled)

		for _, pillar := range []struct {
			name string
			got  map[string]bool
			want []string
		}{
			{"values", declared.Values, c.Expect.Values},
			{"infra", declared.Infra, c.Expect.Infra},
			{"provides", declared.Provides, c.Expect.Provides},
		} {
			if !sameSet(pillar.got, pillar.want) {
				t.Errorf("%s / %s: compiler collected %v, shared expectation %v",
					c.Name, pillar.name, sortedKeys(pillar.got), pillar.want)
			}
		}

		wantAny := len(c.Expect.Values)+len(c.Expect.Infra)+len(c.Expect.Provides) > 0
		if declared.Any() != wantAny {
			t.Errorf("%s: Any() = %v, want %v", c.Name, declared.Any(), wantAny)
		}
	}
}

// TestSensitivitySlotParityAgainstSharedFixture runs the shared slot cases
// through the compiler's own decode of a slot. The compiler reads the marker as
// a boolean field, so a non-boolean is not merely "not a declaration" there — it
// is refused, and the fixture records which side of that line each case sits on.
func TestSensitivitySlotParityAgainstSharedFixture(t *testing.T) {
	t.Parallel()
	cases := loadSensitivityParityCases(t)
	if len(cases.SlotCases) < 5 {
		t.Fatalf("the shared fixture lost slot cases (%d left)", len(cases.SlotCases))
	}

	for _, c := range cases.SlotCases {
		var slot types.AttachmentSlot
		err := yaml.Unmarshal(c.Slot, &slot)
		if c.CompilerDecodes {
			if err != nil {
				t.Errorf("%s: the shared expectation says the compiler decodes this slot, but it does not: %v", c.Name, err)
				continue
			}
			if slot.Sensitive != c.ExpectSensitive {
				t.Errorf("%s: compiler read %v, shared expectation %v", c.Name, slot.Sensitive, c.ExpectSensitive)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: the shared expectation says the compiler refuses this slot, but it decoded to %+v", c.Name, slot)
		}
		if c.ExpectSensitive {
			t.Errorf("%s: a slot the compiler refuses cannot be expected to declare sensitivity", c.Name)
		}
	}
}

// TestSensitivityParityFixtureCoversEveryDivergenceShape pins the shape classes
// the two readers can genuinely disagree on. Each is a shape whose authored
// spelling and compiled spelling differ in a way a naive reader gets wrong, so
// dropping one from the fixture would silence the case that motivates the parity
// check in the first place.
func TestSensitivityParityFixtureCoversEveryDivergenceShape(t *testing.T) {
	t.Parallel()
	cases := loadSensitivityParityCases(t)
	present := map[string]bool{}
	for _, c := range cases.LeafCases {
		present[c.Name] = true
	}
	for _, required := range []string{
		// A marker without a type reads as a property named after the marker
		// unless the marker is known to be a keyword.
		"typeless-leaf-declares",
		// Three shapes that carry the marker without routing through the leaf
		// emitter, each with its own emit site.
		"marker-beside-ref",
		"marker-beside-allof",
		"marker-beside-anyof",
		"marker-beside-oneof",
		// The path spelling a nested declaration is keyed by.
		"nested-implicit-object",
		"nested-explicit-properties",
		// The boolean-only strictness, which a presence test would fail.
		"only-boolean-true-declares",
		// Authored JSON Schema, where the same key is content and not a marker.
		"explicit-jsonschema-mode-declares-nothing",
		"escape-mapping-body-declares-nothing-at-its-own-node",
		"escape-mapping-body-nested-leaf-declares",
		// The one declared divergence: a body only a YAML parser can open.
		"escape-text-body-is-unreadable-in-the-guest",
		// A declaration INSIDE a composing branch, which lands on the
		// composing node rather than below the branch — one case per branch
		// shape, because the list-valued and single-schema keywords are two
		// separate keyword sets and a reader can descend one and miss the
		// other.
		"composition-branch-leaf-declares-on-the-composing-node",
		"composition-branch-properties-declare-below-the-composing-node",
		"conditional-branch-properties-declare-below-the-carrying-node",
		"negated-branch-leaf-declares-on-the-carrying-node",
		// A declaration INSIDE a reference target, which lands at the
		// referring path — from a definitions block at each of the two scopes
		// a pointer can resolve against.
		"reference-target-declares-at-the-referring-path",
		"reference-target-at-the-document-root-resolves-too",
		// The two halves of the cycle rule, which are opposite failures: a
		// recursive target that declares withholds its tail, one that declares
		// nothing stays readable.
		"recursive-reference-declaring-terminates-and-withholds-the-tail",
		"recursive-reference-declaring-nothing-stays-readable",
		// A pointer nothing resolves, where withholding the referring path is
		// the only reading that cannot under-report.
		"unresolvable-reference-collects-the-referring-path",
		// The shapes the walk deliberately does not descend into.
		"composition-sibling-properties-are-not-walked",
		"array-items-are-not-walked",
	} {
		if !present[required] {
			t.Errorf("the shared fixture no longer carries the %q case; that shape is where the two readers can diverge", required)
		}
	}
}
