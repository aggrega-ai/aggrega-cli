// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// Two properties are pinned here together because each one is only meaningful
// while the other holds.
//
// The inject-delivery conversion accepts several payload shapes, and they do not
// all carry an order. An array of entries and an ordered mapping both record the
// order the author wrote; a plain Go map records no order at all, because the
// order is gone before the value is handed over. So the conversion can promise
// author-ordered output only for the shapes that carry an order, and the promise
// is worth having only if the shapes that do not carry one cannot arrive from an
// authored document.
//
// The first group pins the arrival side: a delivery authored as a mapping is
// refused against the entity's schema, on a core mechanism and on a custom one
// alike, so no authored document reaches the conversion with an orderless
// payload. The second group pins the output side: the shapes that do arrive
// serialise in the author's order, byte-identically on every run.
//
// Relaxing the schema to accept a mapping again would turn the first group red,
// which is the point: it names the conversion as the place that then owes an
// order rule, rather than leaving the relaxation to surface as output that
// changes between runs.

// shapeProbeRuns is the repeat count for the ordering pins. Go randomises map
// iteration order per range, so a single pass can coincidentally produce any one
// order; repeating makes a stable result evidence of order preservation rather
// than a coincidence.
const shapeProbeRuns = 500

// shapeProbeKeys are deliberately not in alphabetical order, so author order and
// sorted order are distinguishable in the serialised bytes. Eight of them, so a
// stable result is not an artefact of a key set too small to reorder.
var shapeProbeKeys = []string{
	"ZETA_KEY", "ALPHA_KEY", "MIKE_KEY", "BRAVO_KEY",
	"YANKEE_KEY", "CHARLIE_KEY", "TANGO_KEY", "DELTA_KEY",
}

// shapeProbeWant is the serialisation every order-bearing payload must produce:
// the probe keys in the order they were authored.
const shapeProbeWant = `{"env":{"ZETA_KEY":"value-of-ZETA_KEY","ALPHA_KEY":"value-of-ALPHA_KEY",` +
	`"MIKE_KEY":"value-of-MIKE_KEY","BRAVO_KEY":"value-of-BRAVO_KEY",` +
	`"YANKEE_KEY":"value-of-YANKEE_KEY","CHARLIE_KEY":"value-of-CHARLIE_KEY",` +
	`"TANGO_KEY":"value-of-TANGO_KEY","DELTA_KEY":"value-of-DELTA_KEY"}}`

// assertStableAuthorOrder converts the block shapeProbeRuns times and requires
// every run to serialise identically, in the authored key order.
func assertStableAuthorOrder(t *testing.T, block *types.InjectBlock) {
	t.Helper()
	seen := map[string]int{}
	for i := 0; i < shapeProbeRuns; i++ {
		raw, err := json.Marshal(convertInjectBlock(block))
		if err != nil {
			t.Fatalf("marshalling converted inject block: %v", err)
		}
		seen[string(raw)]++
	}
	if len(seen) != 1 {
		t.Errorf("conversion produced %d distinct serialisations over %d runs; want 1 (identical input must give identical output)", len(seen), shapeProbeRuns)
		shown := 0
		for s := range seen {
			t.Logf("  observed: %s", s)
			if shown++; shown == 4 {
				break
			}
		}
		return
	}
	for got := range seen {
		if got != shapeProbeWant {
			t.Errorf("serialisation is not the authored order\n got: %s\nwant: %s", got, shapeProbeWant)
		}
	}
}

// TestInjectDelivery_ArrayFormPreservesAuthorOrder pins the array form — the
// shape an author writes — as ordered on output, on every run.
func TestInjectDelivery_ArrayFormPreservesAuthorOrder(t *testing.T) {
	entries := make([]any, 0, len(shapeProbeKeys))
	for _, k := range shapeProbeKeys {
		entries = append(entries, map[string]any{"in": k, "value": "value-of-" + k})
	}
	assertStableAuthorOrder(t, &types.InjectBlock{Env: entries})
}

// TestInjectDelivery_OrderedMappingPreservesAuthorOrder pins the ordered-mapping
// shape, which an inject block acquires when it is decoded through a path that
// records key order, as ordered on output, on every run.
func TestInjectDelivery_OrderedMappingPreservesAuthorOrder(t *testing.T) {
	payload := types.NewOrderedMap[any]()
	for _, k := range shapeProbeKeys {
		payload.Set(k, "value-of-"+k)
	}
	assertStableAuthorOrder(t, &types.InjectBlock{Env: payload})
}

// injectShapeAggregate wraps one inject block body in the smallest Aggregate its
// schema accepts, so a finding reported below concerns the inject surface rather
// than a missing field elsewhere.
func injectShapeAggregate(injectBody string) []byte {
	return []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: inject-delivery-shape-probe
  version: 1.0.0
spec:
  components:
    api:
      type: core/service
      inject:
` + injectBody)
}

// TestInjectDelivery_MappingFormIsRefusedAtTheEnvelope pins the arrival side: a
// delivery authored as a mapping does not reach the typed decode, because the
// document is refused against its schema first. Both a core mechanism and a
// custom one are covered, since they are constrained by different parts of the
// same schema and a relaxation of one would not show up in the other.
//
// The array form is included as the calibration case. Without it, a refusal
// reported for every input would read as a pass here while actually meaning the
// probe document was malformed for some unrelated reason.
func TestInjectDelivery_MappingFormIsRefusedAtTheEnvelope(t *testing.T) {
	const mappingBody = "          ZETA_KEY: literal-z\n          ALPHA_KEY: literal-a\n"

	cases := []struct {
		name        string
		body        string
		wantRefused bool
	}{
		{
			name:        "mapping form on a core mechanism",
			body:        "        env:\n" + mappingBody,
			wantRefused: true,
		},
		{
			name:        "mapping form on a custom mechanism",
			body:        "        sidecar:\n" + mappingBody,
			wantRefused: true,
		},
		{
			name:        "array form on a core mechanism",
			body:        "        env:\n          - in: ZETA_KEY\n            value: literal-z\n          - in: ALPHA_KEY\n            value: literal-a\n",
			wantRefused: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := envelope.ValidateBytes(envelope.KindAggregate, injectShapeAggregate(tc.body), "probe.yaml")
			if !tc.wantRefused {
				if err != nil {
					t.Fatalf("array form was refused, so this probe cannot distinguish a real refusal from a malformed document: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("mapping form was accepted; the conversion now receives a payload carrying no author order, and owes an order rule for it")
			}
			// Naming the reason keeps the pin honest: a refusal for some other
			// reason would otherwise read as this one.
			if !strings.Contains(err.Error(), "inject_delivery") {
				t.Errorf("document was refused, but not for its inject delivery shape: %v", err)
			}
		})
	}
}
