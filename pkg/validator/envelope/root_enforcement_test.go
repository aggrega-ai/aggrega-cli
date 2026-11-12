// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package envelope

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
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

// rootDeclaredType reads the root-level type keyword straight out of a
// kind's own embedded schema, for the same reason rootRequiredKeys reads
// the required list: a restated answer agrees with the schema on the day it
// is written and goes on passing after the schema changes under it.
//
// The keyword may be authored as a single name or as a list of them, and
// both decode into the same slice here so a caller asks one question.
func rootDeclaredType(t *testing.T, kind Kind) []string {
	t.Helper()
	raw, ok := schemaSource[kind]
	if !ok {
		t.Fatalf("kind %q carries no embedded schema", kind)
	}
	var root struct {
		Type any `json:"type"`
	}
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		t.Fatalf("decode the embedded %s schema: %v", kind, err)
	}
	switch declared := root.Type.(type) {
	case string:
		return []string{declared}
	case []any:
		out := make([]string, 0, len(declared))
		for _, entry := range declared {
			if name, ok := entry.(string); ok {
				out = append(out, name)
			}
		}
		sort.Strings(out)
		return out
	default:
		return nil
	}
}

// nonObjectDocuments are the shapes a document can take that are not an
// object: the three scalars a YAML document can be at its top level, and a
// sequence. Each carries the JSON type name a diagnostic should call it, so
// the assertion checks that the reader is told what was found and not only
// that something was wrong.
var nonObjectDocuments = []struct {
	name     string
	yaml     string
	jsonType string
}{
	{"a bare string", "just a string\n", "string"},
	{"a bare number", "42\n", "integer"},
	{"a bare boolean", "true\n", "boolean"},
	{"a sequence", "- first\n- second\n", "array"},
}

// TestEveryKindRefusesADocumentWhoseRootIsNotAnObject walks the whole
// registry and pins that a document whose root is not an object is refused
// by the kind's schema, for every kind declaring an object root.
//
// A schema that accepts a scalar where it declares an object is not
// checking shape at all: it is checking the properties of an object it
// never confirmed it has, and every property check passes vacuously because
// there are no properties to fail. Nothing errors and nothing warns, so the
// schema reads as stricter than one enforcing the same constraints — which
// is why the assertion is stated as a rejection. No document expected to
// pass can tell the two apart.
//
// The refusal is required to be a schema violation specifically, not merely
// an error. Some routes into validation refuse a scalar earlier, for their
// own reasons — a typed decode that will not accept one, say — and a
// protection that depends on which route the caller took is not a
// protection at all. Matching the schema-violation shape is what
// distinguishes the gate doing its job from something else doing it by
// accident.
//
// The registry is iterated rather than listed, and each kind's declaration
// is read rather than assumed, so a kind added later is covered without
// anyone remembering to add it and a kind whose schema legitimately admits
// another root shape is not held to a rule its schema never stated.
func TestEveryKindRefusesADocumentWhoseRootIsNotAnObject(t *testing.T) {
	t.Parallel()

	kinds := Kinds()
	if len(kinds) == 0 {
		t.Fatal("the registry reported no kinds, so this test verified nothing")
	}

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			declared := rootDeclaredType(t, kind)
			if len(declared) != 1 || declared[0] != "object" {
				// Reported rather than skipped. A schema admitting a root
				// shape other than an object may be entirely deliberate, but
				// it is not something to discover by noticing that a check
				// quietly stopped applying: whoever changes the schema states
				// the intent here, and the cases below are narrowed to the
				// shapes that schema actually excludes.
				t.Fatalf("%s declares its root type as %v rather than object, so the shapes this "+
					"test rejects are no longer the right ones for it; state what its root admits "+
					"here", kind, declared)
			}

			for _, doc := range nonObjectDocuments {
				t.Run(doc.name, func(t *testing.T) {
					_, err := ValidateBytes(kind, []byte(doc.yaml), "root-shape.yaml")
					if err == nil {
						t.Fatalf("%s accepted a document whose root is %s, though its schema "+
							"declares an object root; in this state every constraint below the "+
							"root passes vacuously and no malformed document of this kind is "+
							"rejected", kind, doc.name)
					}

					var violation *Error
					if !errors.As(err, &violation) {
						t.Fatalf("%s refused a document whose root is %s, but not as a schema "+
							"violation: %v. A refusal arriving from somewhere other than the "+
							"schema does not reach the callers that skip that route",
							kind, doc.name, err)
					}
					if len(violation.Findings) == 0 {
						t.Fatalf("%s reported a schema violation for a root that is %s but "+
							"carried no findings, so the message names nothing to correct",
							kind, doc.name)
					}
					if !strings.Contains(violation.Error(), doc.jsonType) {
						t.Errorf("%s rejected a root that is %s without naming what it found; "+
							"the reader is told the document is wrong and not what it is: %s",
							kind, doc.name, violation.Error())
					}
				})
			}
		})
	}
}

// TestEveryKindStillAcceptsAnObjectRoot is the other direction, and it is
// not redundant: a validator refusing every document satisfies the test
// above completely. It pins that the root check rejects the root shape and
// nothing else, so the coverage that check adds is not paid for by turning
// every document of every kind into a violation.
//
// An empty object is the right instance for it. It violates most of these
// schemas — nearly all require keys at their root — so the assertion is
// narrowed to the root's own shape rather than to a clean report: what must
// not appear is a complaint about the type of the root itself.
func TestEveryKindStillAcceptsAnObjectRoot(t *testing.T) {
	t.Parallel()

	kinds := Kinds()
	if len(kinds) == 0 {
		t.Fatal("the registry reported no kinds, so this test verified nothing")
	}

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			findings, err := ValidateInstance(kind, map[string]any{})
			if err != nil {
				t.Fatalf("validate an empty %s document: %v", kind, err)
			}
			for _, f := range findings {
				if f.FieldPath == "" && strings.Contains(f.Message, "want \"object\"") {
					t.Errorf("%s reported that an object root is not an object, so the root "+
						"shape check rejects the very shape its schema declares: %+v", kind, f)
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
