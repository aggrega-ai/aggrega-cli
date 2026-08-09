// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package envelope

import (
	"encoding/json"
	"sort"
	"testing"
)

// rootRequiredKeys reads the root-level required list straight out of a
// kind's own embedded schema.
//
// Read rather than restated, because a restated list is what this test
// exists to catch one level down: it would agree with the schema on the day
// it was written and go on passing after the schema changed under it.
func rootRequiredKeys(t *testing.T, kind Kind) []string {
	t.Helper()
	raw, ok := schemaSource[kind]
	if !ok {
		t.Fatalf("kind %q carries no embedded schema", kind)
	}
	var root struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		t.Fatalf("decode the embedded %s schema: %v", kind, err)
	}
	out := append([]string{}, root.Required...)
	sort.Strings(out)
	return out
}

// TestEveryKindEnforcesTheRootKeysItsSchemaRequires walks the whole
// registry and pins that each kind's root constraints actually reach the
// instance root.
//
// The check is a document with no keys at all, because that is the one
// input that tells an enforced root from an unenforced one. A schema whose
// root constraints are declared and never applied accepts every malformed
// document, and it accepts them silently: nothing errors, nothing warns,
// and the schema reads as stricter than one that is genuinely enforced. No
// document expected to pass can distinguish the two, which is why the
// assertion is stated as a rejection.
//
// The registry is iterated rather than listed. A list beside the registry
// would agree with it on the day it was written, and a kind added later
// would be absent from the list — precisely the kind whose enforcement
// nobody had yet checked.
//
// Findings are matched by name, not counted. A count can be satisfied by
// arithmetic coincidence — one key enforced twice reads the same as two
// keys enforced once — and a count also breaks on kinds whose root carries
// constraints beyond the required list, which legitimately report more than
// one finding per missing key. Restricting the comparison to the findings
// that report a missing required key states exactly the property meant.
func TestEveryKindEnforcesTheRootKeysItsSchemaRequires(t *testing.T) {
	t.Parallel()

	kinds := Kinds()
	if len(kinds) == 0 {
		t.Fatal("the registry reported no kinds, so this test verified nothing")
	}

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			want := rootRequiredKeys(t, kind)

			findings, err := ValidateInstance(kind, map[string]any{})
			if err != nil {
				t.Fatalf("validate an empty %s document: %v", kind, err)
			}

			got := make([]string, 0, len(want))
			for _, f := range findings {
				if f.Keyword == "required" {
					got = append(got, f.FieldPath)
				}
			}
			sort.Strings(got)

			if len(want) == 0 {
				// A schema requiring nothing at its root accepts an empty
				// document, and that is the schema's own design rather than a
				// gap in enforcement. The case is asserted rather than skipped
				// so the zero is pinned as intended: read as a bare number
				// alongside the other kinds it looks exactly like the failure
				// this test hunts, and a future reader would otherwise be
				// right to try to "fix" it.
				if len(got) != 0 {
					t.Errorf("%s declares no required root keys, so an empty document is valid "+
						"for it; got findings for %v", kind, got)
				}
				return
			}

			if len(got) != len(want) {
				t.Fatalf("%s declares %d required root keys %v but an empty document produced "+
					"%d of them %v; a root whose required list is declared and never applied "+
					"accepts every malformed document of this kind",
					kind, len(want), want, len(got), got)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("%s: expected a missing-key finding for %q, got one for %q "+
						"(declared %v, reported %v)", kind, want[i], got[i], want, got)
				}
			}
		})
	}
}

// TestAKindDeclaringNoRequiredRootKeysAcceptsAnEmptyDocument names the one
// kind that is currently in that position, so the exemption above is a
// statement about a specific schema rather than an open-ended escape.
//
// The workspace configuration declares every one of its properties
// optional, so a configuration file with nothing in it is a valid
// configuration. If a required key is ever added to that schema, this test
// fails and says so — which is the intended signal, since the check above
// would silently move that kind from the exempt branch to the enforced one
// and stop pinning anything about it.
func TestAKindDeclaringNoRequiredRootKeysAcceptsAnEmptyDocument(t *testing.T) {
	t.Parallel()

	if keys := rootRequiredKeys(t, KindAggregaConfig); len(keys) != 0 {
		t.Fatalf("the workspace configuration schema now requires %v at its root; an empty "+
			"configuration file is no longer valid, and the exemption recorded for it is stale",
			keys)
	}

	findings, err := ValidateInstance(KindAggregaConfig, map[string]any{})
	if err != nil {
		t.Fatalf("validate an empty configuration document: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("an empty configuration document was rejected, though its schema requires "+
			"nothing at the root: %+v", findings)
	}
}
