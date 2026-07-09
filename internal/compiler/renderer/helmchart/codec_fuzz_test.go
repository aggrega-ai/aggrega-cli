// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import "testing"

// FuzzHelmCodecRoundTrip asserts the two round-trip invariants of the
// structure-with-holes codec over arbitrary inputs:
//
//   - blank/restore identity: restore(blank(x)) == x for any tokenizable input
//     (the placeholder substitution reproduces the original bytes exactly).
//   - codec identity: Reassemble(Segment(x), {}) == x — segmenting then
//     reassembling with no patches reproduces the file byte-for-byte.
//
// A tokenizer error (an unterminated action/quote) or a blank collision is a
// valid outcome, not a failure: the codec surfaces a loud error and the input is
// not held to the property. Only a successful Segment is held to identity.
func FuzzHelmCodecRoundTrip(f *testing.F) {
	for _, sh := range realHelmShapes() {
		f.Add(sh.body)
	}
	for _, sh := range multidocAdversarialShapes() {
		f.Add(sh.body)
	}
	// Raw seeds widening the mutation surface around the tokenizer's edges.
	f.Add("{{ }}")
	f.Add("{{- -}}")
	f.Add("a: {{ \"}}\" }}\n")
	f.Add("a: {{ dict \"x\" 1 }}\n")
	f.Add("{{ if .x }}{{ end }}")
	f.Add("k: {{ printf `%s` .y }}\n")
	f.Add("no actions here: plain\n")
	f.Add("")

	codec := NewHelmChartCodec(2)

	f.Fuzz(func(t *testing.T, body string) {
		b := []byte(body)

		// blank/restore identity (only when tokenization succeeds and there is no
		// placeholder collision).
		if acts, err := findActions(b); err == nil {
			if scaffold, holes, berr := blank(b, acts); berr == nil {
				if got := restore(scaffold, holes); got != body {
					t.Fatalf("restore(blank(x)) != x\n--- got ---\n%q\n--- want ---\n%q", got, body)
				}
			}
		}

		// codec identity: Segment then Reassemble with no patches.
		units, err := codec.Segment("fuzz/templates/x.yaml", b)
		if err != nil {
			return // tokenizer error / collision: a valid loud-halt outcome
		}
		out, _, err := codec.Reassemble("fuzz/templates/x.yaml", b, units, nil)
		if err != nil {
			t.Fatalf("Reassemble(Segment(x), {}) errored: %v; input %q", err, body)
		}
		if string(out) != body {
			t.Fatalf("Reassemble(Segment(x), {}) != x\n--- got ---\n%q\n--- want ---\n%q", out, body)
		}
	})
}
