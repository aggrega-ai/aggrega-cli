// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// The keys a component's own injection block delivers are read once per delivery
// rule asked about that component, and each finding is reported in the order
// those keys came back. So the sequence is not an internal detail: it is the
// sequence a reader meets the resulting diagnostics in, and one unchanged
// document has to answer with one sequence however often it is read.
//
// An author may write a mechanism's payload as a mapping or as an array, and the
// two carry different amounts of information — an array states an order, a
// mapping states none. Both are read here from one document, because the claim is
// about what the reading makes of each shape rather than about either shape
// alone.

// deliveryKeyReadCount is how many times each payload is read below.
//
// Reading once cannot see this property at all: a reading that varies between
// calls still answers with SOME order every call, and any single one of those
// answers looks settled on its own. With five keys, the chance that a varying
// reading answers with one sequence this many times running is vanishing — which
// is what makes a single observed sequence evidence rather than luck.
const deliveryKeyReadCount = 200

// deliveryKeyOrderDocument declares the same five delivery keys twice on one
// Aggregate: once as a mapping and once as an array.
//
// Both are written in the same deliberately non-alphabetical order, so an answer
// reporting the author's order and an answer reporting a sorted one cannot be
// mistaken for each other. A fixture written ALPHA, BRAVO, CHARLIE would read
// identically either way and would prove nothing about either.
const deliveryKeyOrderDocument = `
apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: delivery-key-order
  version: "1.0.0"
spec:
  components:
    mapping-payload:
      type: core/server:1.0.0
      inject:
        env:
          DELTA: {from: delta}
          ALPHA: {from: alpha}
          ECHO: {from: echo}
          BRAVO: {from: bravo}
          CHARLIE: {from: charlie}
    array-payload:
      type: core/server:1.0.0
      inject:
        env:
          - {in: DELTA, from: delta}
          - {in: ALPHA, from: alpha}
          - {in: ECHO, from: echo}
          - {in: BRAVO, from: bravo}
          - {in: CHARLIE, from: charlie}
`

func TestRereadingOneInjectionBlockAnswersWithOneDeliveryKeySequence(t *testing.T) {
	t.Parallel()

	agg := loadAggregate(t, deliveryKeyOrderDocument)

	// The reading under test answers from the Aggregate's own instances and
	// consults no catalog, so none is supplied and nothing here rests on how a
	// catalog-less resolve behaves.
	view := newNormalizedView(normalized(t, agg), nil, CatalogLookup{}, nil)

	for _, tc := range []struct {
		name      string
		component string
		want      []string
	}{
		{
			name:      "a payload written as a mapping answers alphabetically, the one sequence a mapping can be read in repeatably",
			component: "mapping-payload",
			want:      []string{"ALPHA", "BRAVO", "CHARLIE", "DELTA", "ECHO"},
		},
		{
			name:      "a payload written as an array answers in the order it was written, which is the order that shape carries",
			component: "array-payload",
			want:      []string{"DELTA", "ALPHA", "ECHO", "BRAVO", "CHARLIE"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			seen := map[string]int{}
			for range deliveryKeyReadCount {
				seen[strings.Join(view.AuthoredInjectDeliveryKeys(tc.component, "env"), ",")]++
			}

			if len(seen) != 1 {
				t.Fatalf("reading %q %d times answered with %d different key sequences, want 1:\n%s",
					tc.component, deliveryKeyReadCount, len(seen), sequenceTally(seen))
			}

			want := strings.Join(tc.want, ",")
			for got := range seen {
				if got != want {
					t.Errorf("delivery keys for %q came back as %q, want %q", tc.component, got, want)
				}
			}
		})
	}
}

// TestAnInjectionPayloadArrivesInTheShapeItWasWritten establishes that the two
// rows above measure two different readings rather than one.
//
// The mapping row is a measurement only if a mapping payload actually arrives as
// a plain map — the shape carrying no order of its own. Were it to arrive already
// ordered, that row would be reporting the ordered reading a second time and
// would pass whatever the plain-map reading did.
func TestAnInjectionPayloadArrivesInTheShapeItWasWritten(t *testing.T) {
	t.Parallel()

	agg := loadAggregate(t, deliveryKeyOrderDocument)

	mapping, ok := agg.Spec.Components.Get("mapping-payload")
	if !ok || mapping.Inject == nil {
		t.Fatal("the document declares no component named mapping-payload carrying an injection block")
	}
	if _, plain := mapping.Inject.Env.(map[string]any); !plain {
		t.Errorf("a mechanism written as a mapping arrived as %T, want map[string]interface{}; "+
			"the sequence measured for that shape is measuring some other reading", mapping.Inject.Env)
	}

	array, ok := agg.Spec.Components.Get("array-payload")
	if !ok || array.Inject == nil {
		t.Fatal("the document declares no component named array-payload carrying an injection block")
	}
	if _, ordered := array.Inject.Env.([]any); !ordered {
		t.Errorf("a mechanism written as an array arrived as %T, want []interface{}", array.Inject.Env)
	}
}

// sequenceTally renders each distinct sequence beside how often it was seen, so a
// failure names what varied rather than only reporting that something did.
func sequenceTally(seen map[string]int) string {
	lines := make([]string, 0, len(seen))
	for sequence, count := range seen {
		lines = append(lines, fmt.Sprintf("  %s  x%d", sequence, count))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
