// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package editops

import (
	"strings"
	"testing"
)

// wideIndentAggregate is the same project authored at a four-space nesting step,
// which is the fixture that separates a MEASURED step from an imposed one. A
// conventional two-space default would land the entry at the wrong column here,
// and the document would no longer parse the entry as a child.
const wideIndentAggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
    name: storefront
    version: 1.4.2
spec:
    components:
        web:
            type: core/api-server:1.0.0
            values:
                replicas: 3
`

// TestTheNestingStepIsReadFromTheDocument pins that an entry lands in the style
// the author is already writing in.
//
// The assertion is on the exact leading whitespace rather than on the entry's
// presence, because an entry written at the wrong column is still present — it
// is simply a child of something else, or of nothing.
func TestTheNestingStepIsReadFromTheDocument(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		want []string
	}{
		{
			name: "a two-space document",
			doc:  authoredAggregate,
			want: []string{"      features:", "        scrape:", "          type: core/o11y/scrape:1.0.0"},
		},
		{
			name: "a four-space document",
			doc:  wideIndentAggregate,
			want: []string{"            features:", "                scrape:", "                    type: core/o11y/scrape:1.0.0"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, rel := writeWorkspace(t, tc.doc)

			if _, err := Apply(t.Context(), root, Options{
				File:  rel,
				Kind:  KindAttachFeature,
				Node:  []string{"spec", "components", "web"},
				Name:  "scrape",
				Value: "core/o11y/scrape:1.0.0",
			}); err != nil {
				t.Fatalf("Apply: %v", err)
			}

			on := readBack(t, root, rel)
			for _, line := range tc.want {
				if !strings.Contains(on, "\n"+line+"\n") {
					t.Errorf("want the line %q at that exact column\n--- document ---\n%s", line, on)
				}
			}
		})
	}
}

// TestTheEntryLandsUnderTheNodeTheRequestNames pins that the node is addressed
// by its structural path rather than searched for by name.
//
// The fixture carries the SAME component name at two levels, which is the case a
// name search cannot resolve and a path resolves exactly. Each row edits one of
// them and asserts the other is untouched.
func TestTheEntryLandsUnderTheNodeTheRequestNames(t *testing.T) {
	const twoLevels = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: storefront
  version: 1.4.2
spec:
  subsystems:
    backend:
      components:
        web:
          type: core/api-server:2.0.0
          values:
            replicas: 1
    frontend:
      components:
        web:
          type: core/static-site:1.0.0
          values:
            replicas: 2
`

	for _, tc := range []struct {
		name     string
		node     []string
		wantNear string
	}{
		{
			name:     "the component inside the backend subsystem",
			node:     []string{"spec", "subsystems", "backend", "components", "web"},
			wantNear: "core/api-server:2.0.0",
		},
		{
			name:     "the component inside the frontend subsystem",
			node:     []string{"spec", "subsystems", "frontend", "components", "web"},
			wantNear: "core/static-site:1.0.0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, rel := writeWorkspace(t, twoLevels)

			if _, err := Apply(t.Context(), root, Options{
				File:  rel,
				Kind:  KindSetValue,
				Node:  tc.node,
				Name:  "timeout_seconds",
				Value: 30,
			}); err != nil {
				t.Fatalf("Apply: %v", err)
			}

			on := readBack(t, root, rel)
			if got := strings.Count(on, "timeout_seconds: 30"); got != 1 {
				t.Fatalf("the value was written %d times, want exactly 1\n--- document ---\n%s", got, on)
			}
			// The value must sit in the block belonging to the node named, which
			// is the one carrying that node's own type.
			idx := strings.Index(on, tc.wantNear)
			at := strings.Index(on, "timeout_seconds: 30")
			if idx < 0 || at < idx {
				t.Errorf("the value did not land under the node the request named\n--- document ---\n%s", on)
			}
			// The sibling of the same name keeps its own values untouched.
			if !strings.Contains(on, "            replicas: 1") || !strings.Contains(on, "            replicas: 2") {
				t.Errorf("a sibling node's values were disturbed\n--- document ---\n%s", on)
			}
		})
	}
}

// TestAValueKeepsTheTypeItWasGiven pins that a scalar is written back as the
// kind of thing it came in as.
//
// The string rows are the ones that matter. A version like 1.0, or the word
// "true", written unquoted would read back as a number or a boolean — this
// package changing the type of what an author supplied. Each row asserts the
// exact rendered line, since a wrong type is still a present key.
func TestAValueKeepsTheTypeItWasGiven(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{name: "a plain string", value: "eu-west-1", want: `        region: eu-west-1`},
		{name: "a string that looks like a number", value: "1.0", want: `        pinned: "1.0"`},
		{name: "a string that looks like a boolean", value: "true", want: `        flagged: "true"`},
		{name: "a genuine boolean", value: true, want: `        enabled: true`},
		{name: "a genuine integer", value: 3, want: `        replicas_min: 3`},
		{name: "nothing at all", value: nil, want: `        cleared: null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, rel := writeWorkspace(t, authoredAggregate)
			key := strings.TrimSuffix(strings.TrimSpace(strings.Split(tc.want, ":")[0]), ":")

			if _, err := Apply(t.Context(), root, Options{
				File:  rel,
				Kind:  KindSetValue,
				Node:  []string{"spec", "components", "web"},
				Name:  key,
				Value: tc.value,
			}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if on := readBack(t, root, rel); !strings.Contains(on, "\n"+tc.want+"\n") {
				t.Errorf("want the line %q\n--- document ---\n%s", tc.want, on)
			}
		})
	}
}

// TestTheEntryLandsAfterEverythingTheAuthorWrote pins the placement rule: a new
// entry follows the block's existing content in the order the author wrote,
// rather than landing among it.
//
// The blank line and the comment before the last component are what make this
// worth asserting — a scan that stopped at the first blank line would place the
// entry in the middle of the author's block, and a scan that ran past the block
// would place it inside the following one.
func TestTheEntryLandsAfterEverythingTheAuthorWrote(t *testing.T) {
	root, rel := writeWorkspace(t, authoredAggregate)

	if _, err := Apply(t.Context(), root, Options{
		File:  rel,
		Kind:  KindAddComponent,
		Node:  []string{"spec", "components"},
		Name:  "cache",
		Value: "core/redis:6.2.0",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	on := readBack(t, root, rel)
	dbAt := strings.Index(on, "    db:")
	cacheAt := strings.Index(on, "    cache:")
	if dbAt < 0 || cacheAt < 0 {
		t.Fatalf("both components must be present\n--- document ---\n%s", on)
	}
	if cacheAt < dbAt {
		t.Errorf("the new entry landed before an entry the author wrote\n--- document ---\n%s", on)
	}
	// The comment the author attached to the last component stays attached to
	// it rather than being separated by the new entry.
	if !strings.Contains(on, "    # The data tier is intentionally last.\n    db:") {
		t.Errorf("the new entry split a comment from the entry it belongs to\n--- document ---\n%s", on)
	}
}
