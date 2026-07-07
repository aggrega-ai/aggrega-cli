// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"bytes"
	"testing"
)

// FuzzSplitRoundTrip asserts the round-trip property — join(split(x)) == x
// byte-for-byte — over arbitrary inputs. A parse-rejecting input is a valid
// outcome (the segmenter surfaces a typed error and returns no segments), not a
// round-trip failure; only a successful split is held to the property.
func FuzzSplitRoundTrip(f *testing.F) {
	for _, tc := range segmenterCorpus() {
		f.Add(tc.body)
	}
	// A few raw seeds beyond the corpus to widen the mutation surface.
	f.Add("a: 1\n---\nb: 2\n---\nc: 3\n")
	f.Add("a: |\n  ---\n  ...\nb: 2\n")
	f.Add("\n\n\n")
	f.Add("---")

	f.Fuzz(func(t *testing.T, body string) {
		segs, err := Split("fuzz.yaml", []byte(body))
		if err != nil {
			// Parse-rejected input: must yield no segments.
			if segs != nil {
				t.Fatalf("typed error returned alongside %d segments", len(segs))
			}
			return
		}
		raw := []byte(body)

		// Partition must tile the bytes exactly.
		if len(segs) > 0 {
			if segs[0].Start != 0 {
				t.Fatalf("first segment Start = %d, want 0; input %q", segs[0].Start, body)
			}
			for i := 0; i < len(segs); i++ {
				sg := segs[i]
				if sg.Start < 0 || sg.End > len(raw) || sg.Start > sg.End {
					t.Fatalf("seg[%d] range [%d,%d) out of bounds for len %d; input %q", i, sg.Start, sg.End, len(raw), body)
				}
				if sg.bodyStart < sg.Start || sg.bodyStart > sg.bodyEnd || sg.bodyEnd > sg.End {
					t.Fatalf("seg[%d] sub-ranges not nested (Start=%d bodyStart=%d bodyEnd=%d End=%d); input %q",
						i, sg.Start, sg.bodyStart, sg.bodyEnd, sg.End, body)
				}
				if i+1 < len(segs) && sg.End != segs[i+1].Start {
					t.Fatalf("seg[%d] End=%d != seg[%d] Start=%d; input %q", i, sg.End, i+1, segs[i+1].Start, body)
				}
			}
			if segs[len(segs)-1].End != len(raw) {
				t.Fatalf("last segment End = %d, want %d; input %q", segs[len(segs)-1].End, len(raw), body)
			}
		}

		// Round-trip: join of all ranges reproduces the input.
		got := ApplyBack(raw, segs, nil)
		if !bytes.Equal(got, raw) {
			t.Fatalf("round-trip mismatch\n orig=%q\n  got=%q", body, string(got))
		}
	})
}
