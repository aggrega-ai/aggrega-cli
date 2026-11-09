// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// A delivery entry on a connection is authored in one of two kinds, and the two
// are opposites: it names a key the target publishes, or it carries a literal
// string. The conversion an edge's delivery block goes through on its way into
// the graph writes both kinds into one slot, so which kind an entry was is not
// recoverable from the converted carrier — only what string it holds.
//
// That is a property of the carrier rather than a defect in it. What the carrier
// exists to serve is delivery, and delivery resolves each entry against the
// values the target published and delivers whatever it finds — one reading over
// the whole block, asking of no entry which kind it was. Nothing downstream of
// the conversion needs the distinction, so nothing downstream retains it.
//
// The two tests below pin the consequence for anything that WOULD need it. A rule
// measuring the key an entry names against the keys a target publishes has to
// read the authored block, because the converted carrier answers a literal
// exactly as it answers a named key — so such a rule read over the carrier would
// refuse every literal that does not happen to name a published key, and a
// literal is a shape the entity contract accepts.
//
// Each test pins one half and fails on its own change. The first fails if the
// conversion starts carrying the kind, which is the signal that a rule over the
// carrier has become expressible; the second fails if the entity contract stops
// admitting a literal on a connection, which would make the first test's pair
// unreachable from any document and so leave it pinning nothing.

// referenceShapeAggregate wraps a connection's delivery block in the smallest
// document its entity contract accepts, so a refusal reported below concerns the
// delivery entry rather than a missing field elsewhere.
func referenceShapeAggregate(deliveryEntry string) []byte {
	return []byte(`apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: inject-reference-shape-probe
  version: 1.0.0
spec:
  components:
    api:
      type: core/service
      uses:
        peer:
          link:
            target: db
            inject:
              env:
                - in: ENDPOINT
` + deliveryEntry + `
    db:
      type: core/db
`)
}

// TestInjectReference_ConvertedCarrierAnswersALiteralAsAReference pins that the
// converted carrier holds no trace of which kind an entry was.
//
// The two blocks are authored to differ in exactly one way — one entry reads a
// published key, the other carries a literal of the same text — so a difference
// in the converted bytes could only come from the distinction being carried. The
// third entry is the one that makes the consequence concrete: a literal whose
// text names nothing a target could publish, which a rule reading this carrier as
// a set of references would refuse.
func TestInjectReference_ConvertedCarrierAnswersALiteralAsAReference(t *testing.T) {
	reference := &types.InjectBlock{
		Env: []any{
			map[string]any{"in": "ENDPOINT", "from": "endpoint"},
		},
	}
	literal := &types.InjectBlock{
		Env: []any{
			map[string]any{"in": "ENDPOINT", "value": "endpoint"},
		},
	}

	asReference := convertedBytes(t, reference)
	asLiteral := convertedBytes(t, literal)
	if asReference != asLiteral {
		t.Fatalf("the conversion distinguishes a reference from a literal:\n"+
			"  reference -> %s\n  literal   -> %s\n"+
			"the carrier keeps the kind, so a rule measuring an entry against the keys a target "+
			"publishes is expressible over it", asReference, asLiteral)
	}

	// The consequence, spelled out rather than left to be inferred from the
	// equality above: a literal naming nothing publishable is in the carrier as
	// an ordinary string, so a reader treating every string here as a reference
	// has no way to leave it alone.
	unpublishable := convertedBytes(t, &types.InjectBlock{
		Env: []any{
			map[string]any{"in": "ENDPOINT", "value": "a literal, not a key"},
		},
	})
	const want = `{"env":{"ENDPOINT":"a literal, not a key"}}`
	if unpublishable != want {
		t.Errorf("converted literal = %s; want %s", unpublishable, want)
	}
}

// TestInjectReference_ALiteralOnAConnectionIsAcceptedAuthoring pins the arrival
// side: the entity contract admits a literal on a connection's delivery block, so
// the indistinguishable pair above is a pair a document can actually write.
//
// The reference form is included as the calibration case. Without it, a refusal
// reported for both would read as this pin holding while actually meaning the
// probe document was malformed for an unrelated reason.
func TestInjectReference_ALiteralOnAConnectionIsAcceptedAuthoring(t *testing.T) {
	cases := []struct {
		name  string
		entry string
	}{
		{name: "the entry reads a published key", entry: "                  from: endpoint"},
		{name: "the entry carries a literal", entry: "                  value: a literal, not a key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := envelope.ValidateBytes(
				envelope.KindAggregate, referenceShapeAggregate(tc.entry), "probe.yaml")
			if err != nil {
				t.Fatalf("a connection's delivery block is refused for this entry, so the "+
					"indistinguishable pair beside this test is unreachable from an authored "+
					"document: %v", err)
			}
		})
	}
}

// convertedBytes is one delivery block as the graph's edge carries it,
// serialised. The serialisation is the comparison surface because the carrier is
// an ordered map, and comparing bytes compares the keys, the values and their
// order in one reading.
//
// The graph's own accessor for this fact returns the edge's carrier unchanged, so
// what is compared here is what a rule at that stage would be handed.
func convertedBytes(t *testing.T, block *types.InjectBlock) string {
	t.Helper()
	raw, err := json.Marshal(convertInjectBlock(block))
	if err != nil {
		t.Fatalf("marshalling converted delivery block: %v", err)
	}
	return string(raw)
}
