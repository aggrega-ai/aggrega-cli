// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Zero-value conformance for the IR edge shape: which of the edge's
// schema-required properties reject the Go zero value of the field carrying
// them, and which accept it.
//
// That distinction is the whole subject, and it is not the same question as
// "which properties are required". A required property declared as an
// unconstrained string accepts "", so a construction site that forgets it still
// produces a document the schema accepts — the omission is silent, and harmless
// to conformance. A required property whose value space is CLOSED, by a const or
// an enum, rejects "". And because these fields carry no omitempty, forgetting
// one does not produce an ABSENT property that the required keyword would
// report; it produces a PRESENT property holding "", which the closed value
// space rejects. Any consumer that serialises the shape reproduces that, in
// whatever language its codec is written in, because the emptiness is in the
// value rather than in the key set.
//
// So the hazard is a joint property of the schema and the Go tag, and it is
// enumerable. The register below records the verdict for every required edge
// property; the census refuses to certify a register that does not cover the
// schema's whole required set for the shape. A property added to the edge whose
// zero value the schema rejects therefore lands here as a failure, at the point
// where sweeping construction sites is still cheap, rather than as a
// serialisation failure in a published binding.
//
// Scope is the edge shape, which is the IR shape constructed by hand most often.
// This is a different check from validating emitted IR: an emission check reads a
// real pipeline result, where the field is always populated, so no emission
// fixture can observe what an unpopulated field does. The bidirectional
// schema-to-struct alignment suite in this package holds the IR out for an
// unrelated reason — it reads yaml tags and the IR types are json-tagged — and
// this file does not change that: it walks the schema's required sets and
// validates real instances, so no tag reflection is involved.
package types_test

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// irEdgeZeroCase is one required edge property, the mutation returning the field
// that carries it to its Go zero value, and the recorded verdict.
//
// why is not decoration: it is the reason the verdict is what it is, and a
// reader who disagrees with a verdict needs it to know whether the schema
// changed or the reasoning was wrong.
type irEdgeZeroCase struct {
	property string
	zero     func(*types.IREdge)
	rejected bool
	why      string
}

// irEdgeShapeDiscriminator names the required properties by which a schema
// position is recognised as declaring the edge shape.
//
// The shape is recognised structurally, by properties it has always required,
// rather than by the one property this file is about: recognising it by
// socket_declaration would blind the walk in exactly the case where the walk
// matters, since renaming that property would leave zero positions matched and a
// zero match is indistinguishable from a clean result unless it is refused. The
// census refuses it explicitly for that reason.
var irEdgeShapeDiscriminator = []string{"source", "target"}

// irEdgeRequiredZeroCases is the register. Every required property of the edge
// shape appears exactly once, and the census below fails if the schema declares
// a required property this list does not carry.
var irEdgeRequiredZeroCases = []irEdgeZeroCase{
	{
		property: "source",
		zero:     func(e *types.IREdge) { e.Source = "" },
		rejected: false,
		why: "declared as an unconstrained string, so the schema accepts the empty value: " +
			"a forgotten source produces a conforming document and conformance cannot see it",
	},
	{
		property: "target",
		zero:     func(e *types.IREdge) { e.Target = "" },
		rejected: false,
		why: "declared as an unconstrained string, so the schema accepts the empty value: " +
			"a forgotten target produces a conforming document and conformance cannot see it",
	},
	{
		property: "socket_declaration",
		zero:     func(e *types.IREdge) { e.SocketDeclaration = "" },
		rejected: true,
		why: "declared with a closed two-value enum, so the empty value is outside the value space: " +
			"the field is always serialised, so forgetting it emits the empty string rather than " +
			"omitting the key, and the enum rejects it",
	},
}

// TestIREdgeRequiredPropertyZeroValueVerdicts returns one edge field at a time
// to its Go zero value inside an otherwise-conforming document and checks the
// schema's verdict against the register.
//
// The verdict is read as the presence of any finding rather than by matching
// finding text, so the check does not depend on how the validation engine words
// or nests its messages.
func TestIREdgeRequiredPropertyZeroValueVerdicts(t *testing.T) {
	t.Parallel()

	// The scaffold is itself a hand-written IR fixture, and every verdict below
	// is the difference between its clean result and a mutated one. A scaffold
	// that does not conform turns every reading into noise, so it is checked
	// first and the failure is fatal rather than reported.
	if findings := irZeroValidate(t, irZeroScaffold()); len(findings) > 0 {
		t.Fatalf("the scaffold must conform before any verdict can be read from it; got %d finding(s): %s",
			len(findings), irZeroFormatFindings(findings))
	}

	for _, c := range irEdgeRequiredZeroCases {
		c := c
		t.Run(c.property, func(t *testing.T) {
			t.Parallel()
			g := irZeroScaffold()
			c.zero(&g.Spec.Edges[0])
			findings := irZeroValidate(t, g)
			rejected := len(findings) > 0
			if rejected == c.rejected {
				return
			}
			if c.rejected {
				t.Errorf("edge property %q: the register records that the schema REJECTS the Go zero "+
					"value of the field carrying it, and the schema accepted it. Either the property's "+
					"value space was opened — in which case a construction site that forgets the field "+
					"now produces a conforming document and nothing will report the omission — or the "+
					"register's reasoning is wrong. Registered reason: %s",
					c.property, c.why)
				return
			}
			t.Errorf("edge property %q: the register records that the schema ACCEPTS the Go zero value "+
				"of the field carrying it, and the schema rejected it. The property's value space has "+
				"closed, so every construction site that leaves the field unset now emits a "+
				"non-conforming edge — sweep the construction sites and update this register. "+
				"Registered reason: %s\nFindings: %s",
				c.property, c.why, irZeroFormatFindings(findings))
		})
	}
}

// TestIREdgeRequiredPropertyCensusIsFullyRegistered closes the hole that
// hand-listing the register opens: a required property the schema declares and
// the register omits would have its zero-value behaviour checked by nothing, and
// no test would say so.
//
// The schema declares the edge shape at more than one position. This walk does
// not nominate one of them: it collects every position whose required list names
// both source and target, requires those positions to agree, and reads the
// agreed set as the census. A divergence is reported rather than resolved,
// because which position governs is not a question a test may answer by picking.
func TestIREdgeRequiredPropertyCensusIsFullyRegistered(t *testing.T) {
	t.Parallel()

	positions := irSchemaRequiredSets(t)
	if len(positions) == 0 {
		t.Fatalf("the walk found no position in the IR schema declaring a required list; " +
			"that is a claim about the walk, not about the schema, so nothing below can be certified")
	}

	edges := map[string][]string{}
	for ptr, req := range positions {
		matched := true
		for _, name := range irEdgeShapeDiscriminator {
			if !irZeroHasString(req, name) {
				matched = false
				break
			}
		}
		if matched {
			edges[ptr] = req
		}
	}
	if len(edges) == 0 {
		t.Fatalf("no edge-shaped position found in the IR schema — no required list names all of %v "+
			"— while the walk did find %d position(s) carrying a required list. Either the edge shape's "+
			"required set changed, or this walk no longer reaches it; in both cases the zero-value "+
			"register below is certifying nothing. Positions walked: %v",
			irEdgeShapeDiscriminator, len(positions), irZeroSortedKeys(positions))
	}

	// Agreement across the positions, reported without nominating one.
	var pointers []string
	for ptr := range edges {
		pointers = append(pointers, ptr)
	}
	sort.Strings(pointers)
	reference := edges[pointers[0]]
	for _, ptr := range pointers[1:] {
		if strings.Join(edges[ptr], ",") != strings.Join(reference, ",") {
			t.Fatalf("the edge shape's required set differs between the positions declaring it, so no "+
				"single census exists to register against: %s declares %v, %s declares %v. Settle the "+
				"two declarations before recording a zero-value verdict against either.",
				pointers[0], reference, ptr, edges[ptr])
		}
	}

	registered := map[string]bool{}
	for _, c := range irEdgeRequiredZeroCases {
		if registered[c.property] {
			t.Errorf("edge property %q appears more than once in the zero-value register; "+
				"a property with two verdicts has none", c.property)
		}
		registered[c.property] = true
	}

	for _, property := range reference {
		if !registered[property] {
			t.Errorf("the IR schema requires edge property %q (declared at %v) and the zero-value "+
				"register does not carry it, so nothing checks whether the schema accepts or rejects "+
				"the Go zero value of the field carrying it. Add a case to "+
				"irEdgeRequiredZeroCases: if the schema rejects the zero value, every construction "+
				"site — including every hand-written fixture edge — must set the field.",
				property, pointers)
		}
	}
	for _, c := range irEdgeRequiredZeroCases {
		if !irZeroHasString(reference, c.property) {
			t.Errorf("the zero-value register carries edge property %q, which the IR schema no longer "+
				"requires (required set at %v is %v). Remove the case, or correct it: a register entry "+
				"for a property that is not required certifies a verdict nobody asked for.",
				c.property, pointers, reference)
		}
	}
}

// irZeroScaffold returns an IR document that conforms to its schema, carrying
// exactly one edge for a case to mutate. It is written out in full rather than
// built by the pipeline: the point is to exercise what a hand-written shape
// produces, and a pipeline result never leaves a field unset.
func irZeroScaffold() *types.IRGraph {
	labels := types.OrderedMap[string]{}
	labels.Set("tier", "data")

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("consumer", types.IRNode{Labels: labels, Component: "core/consumer", ResolvedVersion: "1.0.0"})
	nodes.Set("producer", types.IRNode{Labels: labels, Component: "core/producer", ResolvedVersion: "1.0.0"})

	return &types.IRGraph{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "IR",
		Metadata:   types.IRMetadata{AggregateName: "zero-value-scaffold"},
		Spec: types.IRSpec{
			Nodes: nodes,
			Edges: []types.IREdge{{
				Source:            "consumer",
				Target:            "producer",
				Socket:            "databases",
				Alias:             "main",
				SocketDeclaration: types.SocketDeclarationDeclared,
			}},
			FeatureMarkers:       []string{},
			ValuesMapping:        types.OrderedMap[json.RawMessage]{},
			InfraMapping:         types.OrderedMap[json.RawMessage]{},
			FeatureValuesMapping: types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{},
			Provenance: types.IRProvenance{
				SchemaVersion:     "v1alpha1",
				CompilerVersion:   "scaffold",
				AggregateVersion:  "1.0.0",
				ManifestVersion:   "1.0.0",
				ReleaseVersion:    "1.0.0",
				SourceIdentifiers: []string{"scaffold"},
			},
		},
	}
}

// irZeroValidate marshals a graph and validates it through the same entry point
// the emission-side conformance checks use, so this file observes exactly what
// they observe and adds no second reading of the schema.
func irZeroValidate(t *testing.T, g *types.IRGraph) []jsonvalidator.Finding {
	t.Helper()
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal IR document: %v", err)
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("re-decode IR document: %v", err)
	}
	findings, err := envelope.ValidateInstance(envelope.KindIR, instance)
	if err != nil {
		t.Fatalf("IR schema engine error: %v", err)
	}
	return findings
}

// irSchemaRequiredSets walks the embedded IR schema document and returns the
// sorted required list declared at every position that carries one, keyed by a
// pointer to that position.
//
// The walk is over the raw document and does not resolve $ref, because the
// question is which positions DECLARE a requirement and a reference declares
// none of its own.
func irSchemaRequiredSets(t *testing.T) map[string][]string {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(schemas.IRSchemaV1alpha1), &doc); err != nil {
		t.Fatalf("decode embedded IR schema: %v", err)
	}
	out := map[string][]string{}
	var walk func(node any, pointer string)
	walk = func(node any, pointer string) {
		switch n := node.(type) {
		case map[string]any:
			if raw, ok := n["required"].([]any); ok {
				var names []string
				for _, item := range raw {
					if s, ok := item.(string); ok {
						names = append(names, s)
					}
				}
				if len(names) > 0 {
					sort.Strings(names)
					at := pointer
					if at == "" {
						at = "(root)"
					}
					out[at] = names
				}
			}
			keys := make([]string, 0, len(n))
			for k := range n {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(n[k], pointer+"/"+k)
			}
		case []any:
			for i, item := range n {
				walk(item, fmt.Sprintf("%s/%d", pointer, i))
			}
		}
	}
	walk(doc, "")
	return out
}

func irZeroHasString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func irZeroSortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func irZeroFormatFindings(findings []jsonvalidator.Finding) string {
	var b strings.Builder
	for i, f := range findings {
		if i > 0 {
			b.WriteString("; ")
		}
		path := f.FieldPath
		if path == "" {
			path = "(root)"
		}
		fmt.Fprintf(&b, "%s: %s", path, f.Message)
	}
	return b.String()
}
