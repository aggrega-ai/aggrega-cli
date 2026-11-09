// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Structural guard on the IR schema's top-level frame, which is declared in two
// places: at the document root, and again in the IRGraph definition for
// consumers that address the graph by reference.
//
// The duplication is deliberate and it is shallow. Every BODY has exactly one
// home — the root reaches the metadata and spec shapes by reference, so those
// cannot drift. What remains doubled is the frame itself: the object type, the
// required key list, the closed key set, and the two version constants. Two
// readers need it in two places. Instance validation applies the constraints it
// finds AT the root, so the root must carry them directly; code generation
// walks the definitions and never reads the root, so the same frame must be
// reachable there too.
//
// The failure mode this guards is quiet in both directions. Reducing the root
// to a single reference would satisfy code generation and leave instance
// validation with no constraint to apply at the root, so every malformed
// document would be accepted while the schema read as though it were stricter —
// a schema cannot report that it is being ignored. Editing one side alone would
// leave the two readers disagreeing about the same shape, with nothing
// comparing them.
//
// At four keywords the comparison costs less than the drift it prevents, which
// is why the guard is here rather than a comment asking the next author to
// remember.
package types_test

import (
	"encoding/json"
	"sort"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// irGraphFrameKeywords names the keywords the root and the IRGraph definition
// both declare. It is asserted against IRGraph's own key set before any value
// is compared, so a keyword added to one side alone fails rather than escaping
// the comparison.
var irGraphFrameKeywords = []string{
	"additionalProperties",
	"properties",
	"required",
	"type",
}

// TestIRSchemaRootMirrorsGraphDefinition compares the root's frame against the
// IRGraph definition's, keyword by keyword.
//
// Values are compared as canonical JSON rather than by reflect equality, so the
// verdict does not depend on decode order and a failure prints both sides.
func TestIRSchemaRootMirrorsGraphDefinition(t *testing.T) {
	t.Parallel()

	root := irMirrorSchemaDocument(t)
	graph := irMirrorGraphDefinition(t, root)

	// Comparing only the named keywords would silently pass a keyword added to
	// one side alone, so the key set is checked before the values are.
	if got := irMirrorSortedKeys(graph); !irMirrorEqualStrings(got, irGraphFrameKeywords) {
		t.Fatalf("the IRGraph definition declares keywords %v, and the root is compared over %v: "+
			"a keyword present on one side only is drift this test's value comparison cannot see. "+
			"Either extend the comparison to cover it, or move the new keyword's body behind a "+
			"reference so that only one side declares it", got, irGraphFrameKeywords)
	}

	for _, keyword := range irGraphFrameKeywords {
		t.Run(keyword, func(t *testing.T) {
			t.Parallel()
			atRoot := irMirrorCanonical(t, root[keyword])
			atGraph := irMirrorCanonical(t, graph[keyword])
			if atRoot != atGraph {
				t.Errorf("the IR schema's root and its IRGraph definition disagree on %q.\n"+
					"  root:    %s\n"+
					"  IRGraph: %s\n"+
					"Both readers of this schema must see the same top-level shape: instance "+
					"validation applies the root's constraints, and code generation reads the "+
					"definition. Apply the change to both, or reduce the difference to a reference",
					keyword, atRoot, atGraph)
			}
		})
	}
}

// TestIRSchemaRootConstraintsReachTheInstanceRoot checks that the root's
// constraints are actually applied to a document, by handing the validator a
// document that satisfies none of them.
//
// The mirror comparison above cannot establish this. It reads the schema
// document and would pass just as happily on a root whose constraints are never
// applied, because the two questions are different: one asks what the schema
// declares, the other asks what the validator enforces. A schema that is being
// ignored looks stricter than one that is not, so only a document that OUGHT to
// be rejected can tell the two apart.
//
// Scope here is the IR root. The same property holds for every kind the
// validator carries an embedded schema for, and the whole-registry check now
// lives with the registry, in the envelope package, where it can iterate the
// kinds rather than name them.
//
// This narrower check is kept deliberately rather than folded into that one.
// It reaches the validator from the package that owns the emitted structures,
// so it fails on the side that would break first if the IR root and its schema
// ever parted company — and a check that only ever ran from the validator's own
// package could be satisfied by a change to the validator alone. Whoever
// consolidates these two later should keep one arriving from each side.
func TestIRSchemaRootConstraintsReachTheInstanceRoot(t *testing.T) {
	t.Parallel()

	// A document with no keys at all violates the root's required list four
	// times over, so a validator applying the root reports findings and one
	// that never reaches it reports none.
	findings, err := envelope.ValidateInstance(envelope.KindIR, map[string]any{})
	if err != nil {
		t.Fatalf("validate an empty IR document: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("the IR envelope accepted a document with no properties at all, so the root's " +
			"required list, closed key set and version constants are declared but never applied. " +
			"Every malformed IR document is accepted in this state, and no positive conformance " +
			"test can observe it")
	}
}

// irMirrorSchemaDocument decodes the embedded IR schema document.
func irMirrorSchemaDocument(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(schemas.IRSchemaV1beta1), &doc); err != nil {
		t.Fatalf("decode the embedded IR schema: %v", err)
	}
	return doc
}

// irMirrorGraphDefinition returns the IRGraph definition, refusing a document
// that does not carry one.
//
// The refusal is explicit because a missing definition would otherwise yield an
// empty map, and an empty map compares equal to nothing at all — a renamed
// definition would read as a clean result rather than as a broken guard.
func irMirrorGraphDefinition(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatal("the embedded IR schema declares no definitions, so the frame this test " +
			"compares has only one side and the comparison cannot run")
	}
	graph, ok := defs["IRGraph"].(map[string]any)
	if !ok {
		t.Fatal("the embedded IR schema declares no IRGraph definition, so the frame this " +
			"test compares has only one side. If the definition was renamed, update this " +
			"test rather than leaving it matching nothing")
	}
	return graph
}

// irMirrorCanonical renders a decoded schema fragment as canonical JSON. Object
// keys are emitted in sorted order, so the comparison is insensitive to decode
// order and stable across runs.
func irMirrorCanonical(t *testing.T, fragment any) string {
	t.Helper()
	encoded, err := json.Marshal(fragment)
	if err != nil {
		t.Fatalf("re-encode a schema fragment: %v", err)
	}
	return string(encoded)
}

func irMirrorSortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func irMirrorEqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
