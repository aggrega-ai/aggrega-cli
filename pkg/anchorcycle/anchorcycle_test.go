// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package anchorcycle

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// parse is the fixture helper: it produces the node tree a caller holds at the
// moment it must decide whether walking is safe. Parsing never expands an
// alias, so a recursive document parses cleanly here — which is precisely why
// the check has to exist.
func parse(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatalf("fixture did not parse: %v", err)
	}
	return &n
}

// TestParseAcceptsARecursiveDocument is the premise every other test here
// rests on. If parsing itself started rejecting these documents, the guard
// would be dead code and the tests below would pass for the wrong reason.
func TestParseAcceptsARecursiveDocument(t *testing.T) {
	t.Parallel()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte("a: &loop\n  b: *loop\n"), &n); err != nil {
		t.Fatalf("parse rejected a recursive document (%v); the guard's reason for existing has changed", err)
	}
}

func TestFindDetectsRecursion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		src        string
		wantAnchor string
	}{
		{
			name:       "anchor aliased directly inside itself",
			src:        "a: &loop\n  b: *loop\n",
			wantAnchor: "loop",
		},
		{
			name:       "anchor aliased below a nested mapping",
			src:        "a: &loop\n  b:\n    c:\n      d: *loop\n",
			wantAnchor: "loop",
		},
		{
			name:       "anchor aliased from inside a sequence",
			src:        "a: &loop\n  - x\n  - *loop\n",
			wantAnchor: "loop",
		},
		{
			name:       "recursion reached only through a second anchor",
			src:        "a: &outer\n  b: &inner\n    c: *outer\n",
			wantAnchor: "outer",
		},
		{
			name:       "recursive anchor sitting beside clean content",
			src:        "clean:\n  x: 1\nloopy: &loop\n  y: *loop\ntrailing: done\n",
			wantAnchor: "loop",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			anchor, found := Find(parse(t, tc.src))
			if !found {
				t.Fatal("expected recursion to be reported; got none — a walker downstream of this would not terminate")
			}
			if anchor != tc.wantAnchor {
				t.Errorf("anchor = %q, want %q (the name is what an author has to break)", anchor, tc.wantAnchor)
			}
		})
	}
}

// TestFindAcceptsNonRecursiveDocuments is the other half of the contract, and
// the more important half: a false positive refuses input an author may
// legitimately write.
func TestFindAcceptsNonRecursiveDocuments(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, src string }{
		{"no anchors at all", "a:\n  b:\n    c: 1\n"},
		{"anchor aliased outside its own content", "base: &b\n  x: 1\nuser:\n  y: *b\n"},
		{"one anchor aliased from many places", "base: &b\n  x: 1\na: *b\nb: *b\nc: *b\nd: *b\n"},
		{"anchor nested inside another anchor, no cycle", "outer: &o\n  inner: &i\n    x: 1\nuse: *i\nalso: *o\n"},
		{"empty document", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if anchor, found := Find(parse(t, tc.src)); found {
				t.Errorf("reported anchor %q on a document with no recursion; a false refusal rejects valid input", anchor)
			}
		})
	}
}

func TestFindOnNilReportsNothing(t *testing.T) {
	t.Parallel()
	if anchor, found := Find(nil); found {
		t.Errorf("nil root reported anchor %q", anchor)
	}
}

// TestFindRefusesRecursionNotDepth is the acceptance-level distinction. A
// depth cap large enough to be safe would still be large enough to exhaust the
// stack, and small enough to reject documents an author may legitimately write
// — so depth must not be what triggers the refusal.
func TestFindRefusesRecursionNotDepth(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	const depth = 5000
	for i := range depth {
		b.WriteString(strings.Repeat("  ", i))
		b.WriteString(fmt.Sprintf("level%d:\n", i))
	}
	b.WriteString(strings.Repeat("  ", depth))
	b.WriteString("leaf: done\n")

	if anchor, found := Find(parse(t, b.String())); found {
		t.Fatalf("a %d-deep non-recursive document was refused, naming %q; the check has become a depth limit", depth, anchor)
	}
}

// TestFindDoesNotWalkTheExpansion pins the property that makes the check
// affordable. The fixture's expanded form doubles at every level, so at 40
// levels it is on the order of 2^40 nodes while its text stays tiny. A check
// that walked the expanded form rather than the document's own nodes would
// exhaust exactly the resources it exists to protect, and it would do so on a
// document containing no recursion at all.
func TestFindDoesNotWalkTheExpansion(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	const levels = 40
	b.WriteString("a0: &a0 [leaf]\n")
	for i := 1; i <= levels; i++ {
		b.WriteString(fmt.Sprintf("a%d: &a%d [*a%d, *a%d]\n", i, i, i-1, i-1))
	}
	root := parse(t, b.String())

	done := make(chan struct{})
	var found bool
	var anchor string
	go func() {
		anchor, found = Find(root)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Find did not finish on a document with no recursion whose EXPANDED form is exponential; it is walking the expansion rather than the document")
	}
	if found {
		t.Errorf("reported anchor %q on a document with no recursion", anchor)
	}
}

// TestExplainNamesTheAnchorAndTheRemedy keeps the wording honest: an author
// meeting this refusal needs the anchor to change and something to change it
// to, and both must survive any later edit of the sentence.
func TestExplainNamesTheAnchorAndTheRemedy(t *testing.T) {
	t.Parallel()
	got := Explain("loop")
	for _, want := range []string{"&loop", "*loop", "expands without end", "write the content out in full"} {
		if !strings.Contains(got, want) {
			t.Errorf("Explain(%q) does not carry %q; got: %s", "loop", want, got)
		}
	}
}
