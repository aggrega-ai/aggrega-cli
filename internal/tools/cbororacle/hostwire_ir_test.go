// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cbororacle_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The IR graph is the one payload whose compiler-side codec is generated without
// its declarations: the graph types are hand-written where they are declared, and
// only the codec is generated against the same schema the plugin-side types are
// generated from. Nothing in a build encodes an IR graph in this form yet, so the
// codec is held by nothing but the tests below — which is exactly why they exist.
// A specification is not a consumer count: an unexercised codec drifts silently,
// and a schema edit or a generator change would break it with every other gate
// staying green.
//
// Four claims, each proved separately because each fails differently:
//
//   - Encoding is byte-stable — the same graph encodes to the same bytes twice.
//   - The bytes match two independent references: the frozen cross-language vector
//     corpus, and the plugin-side codec generated from the same schema. Two codecs
//     can agree on a shared mistake; a frozen vector the codec had no hand in
//     producing cannot.
//   - The bytes decode back through the compiler-side decoder, and the decode
//     re-encodes identically.
//   - Author order survives the wire in the payloads that carry it verbatim, and
//     does not survive in the ones the canonical encoding sorts.
//
// The fixture inserts keys in an order that is neither the canonical order nor the
// alphabetical one, so an encoder that sorted where it must not — or carried
// insertion order where it must not — produces different bytes here rather than
// agreeing by accident. TestHostWireIR_FixtureOrderIsDiscriminating pins that
// property of the fixture itself.

// irFixture builds an IR graph exercising every field whose value is carried
// verbatim as opaque bytes — the node's self-inject block and uses block, an
// edge's per-connection values, infra and inject, an attachment's values, infra,
// inject and sensitive-inject, and a boundary attachment's infra — alongside the
// structurally encoded order-preserving containers beside them. The two groups
// behave differently on the wire, which is the subject of
// TestHostWireIR_AuthorOrderSurvivesOnlyInTheVerbatimPayloads.
func irFixture(t *testing.T) types.IRGraph {
	t.Helper()

	entry := types.IRFeatureEntry{
		Type:            "feature/observability",
		ResolvedVersion: "2.0.1",
		Origin:          types.OriginExplicit,
		Binding:         types.AttachmentBindingFree,
		// Verbatim payloads: insertion order is preserved inside the bytes.
		Values: om[any](t, "zulu", "z-value", "b", "b-value", "mid", "m-value"),
		Infra:  om[any](t, "size", "large"),
		Inject: om[types.OrderedMap[string]](t,
			"ZED", om[string](t, "TOKEN", "api_token"),
			"AYE", om[string](t, "HOST", "host"),
		),
		SensitiveInject: om[types.OrderedMap[string]](t,
			"ZED", om[string](t, "TOKEN", "api_token"),
		),
		// Structurally encoded: the canonical encoding sorts these.
		Labels:                 om[string](t, "zeta", "z", "b", "1"),
		CatalogAnnotations:     om[string](t, "zzz", "3", "a", "1"),
		ValuesProvenance:       om[types.FieldProvenance](t, "zulu", types.FieldProvenance{Origin: types.OriginExplicit}, "b", types.FieldProvenance{Origin: types.OriginExplicit}),
		InfraProvenance:        om[types.FieldProvenance](t, "size", types.FieldProvenance{Origin: types.OriginAnnotation, Side: "release", AnnotationKey: "aggrega.ai/size"}),
		Sensitive:              true,
		SensitivePayloadLeaves: []string{"values.zulu", "infra.size"},
	}

	node := types.IRNode{
		Description:     "the consuming node",
		Component:       "core/web",
		ResolvedVersion: "1.2.3",
		SyncWave:        3,
		BoundaryPath:    "prod/eks",
		SubsystemPath:   "checkout",
		SourceFile:      "aggregate.yaml",
		CatalogMetadata: &types.CatalogMetadata{
			License:  "Apache-2.0",
			Keywords: []string{"web", "http"},
		},
		// Verbatim payloads.
		SelfInject: &types.InjectBlock{Env: map[string]any{"DB_HOST": "db.local"}},
		Uses: om[types.UsesSocketValue](t,
			"tools", types.UsesSocketValue{Verbose: om[types.VerboseUsesEntry](t,
				"cache", types.VerboseUsesEntry{Target: "db", Values: om[any](t, "pool", "5")},
			)},
			"db", types.UsesSocketValue{Compact: []string{"db"}},
		),
		// Structurally encoded.
		Labels:               om[string](t, "zeta", "frontend", "b", "checkout"),
		CatalogAnnotations:   om[string](t, "zzz", "3", "a", "1"),
		ReleaseAnnotations:   om[string](t, "region", "eu-west-1"),
		AggregateAnnotations: om[string](t, "owner", "payments"),
	}

	edge := types.IREdge{
		Source:            "web",
		Target:            "db",
		Socket:            "databases",
		Alias:             "primary",
		SocketDeclaration: types.SocketDeclarationDeclared,
		// Verbatim payloads.
		Values: om[any](t, "zulu", "z-value", "b", "b-value", "mid", "m-value"),
		Infra:  om[any](t, "instance_class", "db.r6g.large"),
		Inject: om[types.OrderedMap[string]](t,
			"ZED", om[string](t, "PGPORT", "port"),
			"AYE", om[string](t, "PGHOST", "host"),
		),
	}

	return types.IRGraph{
		APIVersion: types.IRAPIVersion,
		Kind:       types.IRKind,
		Metadata:   types.IRMetadata{AggregateName: "shop"},
		Spec: types.IRSpec{
			// Structurally encoded containers, keyed out of canonical order.
			Nodes:         om[types.IRNode](t, "zeta/web", node, "db", types.IRNode{Component: "core/postgres", ResolvedVersion: "15.1.0", Labels: om[string](t, "tier", "data")}),
			Edges:         []types.IREdge{edge},
			ValuesMapping: om[json.RawMessage](t, "zeta/web", json.RawMessage(`{"replicas":2}`), "db", json.RawMessage(`{}`)),
			InfraMapping:  om[json.RawMessage](t, "zeta/web", json.RawMessage(`{"cpu":"500m"}`)),
			FeatureValuesMapping: om[types.OrderedMap[types.IRFeatureEntry]](t,
				"zeta/web", om[types.IRFeatureEntry](t, "obs", entry),
			),
			BoundaryFeatures: om[types.OrderedMap[types.IRBoundaryFeature]](t,
				"prod/eks", om[types.IRBoundaryFeature](t,
					"mesh", types.IRBoundaryFeature{
						Type: "feature/mesh",
						// Verbatim payload.
						Infra: om[any](t, "zulu", "z-value", "b", "b-value", "mid", "m-value"),
					},
				),
			),
			Provenance: types.IRProvenance{
				SchemaVersion:     "v1alpha1",
				CompilerVersion:   "test",
				AggregateVersion:  "1.0.0",
				ManifestVersion:   "1.0.0",
				ReleaseVersion:    "1.0.0",
				SourceIdentifiers: []string{"aggregate.yaml"},
			},
		},
	}
}

// TestHostWireIR_MatchesFrozenVector holds the compiler-side codec to the frozen
// shared vector, which is the strongest reference available to it: the vector's
// canonical bytes are anchored by the plugin-side suite against an independent
// reference encoder and by the Rust and TypeScript harnesses reading the same
// corpus, so they are an oracle this codec had no hand in producing. A codec
// re-checked only by its own counterpart proves that the two agree, including
// where they agree on a mistake.
//
// The comparison runs through the shared document rather than a fixture built
// here, so a divergence between the two sides of the wire shows up as a byte
// difference against a committed artifact rather than as two codecs quietly
// moving together.
func TestHostWireIR_MatchesFrozenVector(t *testing.T) {
	t.Parallel()

	var g types.IRGraph
	if err := json.Unmarshal(sharedVector(t, "IRGraph.golden.json"), &g); err != nil {
		t.Fatalf("compiler-side decode of the frozen document: %v", err)
	}
	got, err := g.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}

	want := string(sharedVector(t, "IRGraph.cbor.hex"))
	if h := hex.EncodeToString(got); h != want {
		t.Errorf("the compiler-side bytes are not the frozen canonical bytes\n frozen: %s\n    got: %s", want, h)
	}

	// Read through the independent decoder as well: it consumes exactly the pair
	// count each map head declares, so it catches a head that disagrees with its
	// body in either direction — which re-decoding with this codec's own
	// counterpart cannot see.
	if m := refDecode(t, "IRGraph", got); len(m) != 4 {
		t.Errorf("the independent decoder read %d top-level keys, want 4 (apiVersion, kind, metadata, spec)", len(m))
	}

	again, err := g.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side re-encode: %v", err)
	}
	if !bytes.Equal(got, again) {
		t.Errorf("encoding the same graph twice produced different bytes\nfirst:  %s\nsecond: %s",
			hex.EncodeToString(got), hex.EncodeToString(again))
	}
}

// TestHostGuestWireParity_IRGraph asserts the two sides of the wire speak the
// same bytes for a graph carrying every verbatim payload the schema declares.
// The sides do not hold the same Go types — an order-preserving container here is
// a plain string-keyed map there, and each verbatim payload here is an opaque
// document there — so agreement is a property to assert rather than one the
// shared schema delivers by itself.
//
// The fixture is richer than the frozen vector on purpose: the vector carries the
// shape a build produces, and this carries every field at once, including the
// ones a build populates rarely.
func TestHostGuestWireParity_IRGraph(t *testing.T) {
	t.Parallel()

	g := irFixture(t)
	got, err := g.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}
	again, err := g.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side re-encode: %v", err)
	}
	if !bytes.Equal(got, again) {
		t.Errorf("encoding the same graph twice produced different bytes\nfirst:  %s\nsecond: %s",
			hex.EncodeToString(got), hex.EncodeToString(again))
	}

	crossSide(t, "IRGraph", got, &abi.IRGraph{})

	// The compiler side reads its own bytes back and re-encodes identically. This
	// is the only path that exercises the decoder over a full graph, and the
	// re-encode is what makes a lossy decode observable rather than silent.
	var rt types.IRGraph
	if err := rt.DecodeCBOR(got); err != nil {
		t.Fatalf("compiler-side decode: %v", err)
	}
	back, err := rt.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side re-encode after decode: %v", err)
	}
	if !bytes.Equal(got, back) {
		t.Errorf("the round trip is not byte-stable\n in: %s\nout: %s",
			hex.EncodeToString(got), hex.EncodeToString(back))
	}

	// A spot check on scalars the round trip must carry, asserted directly rather
	// than inferred from the byte compare above, so a failure names the property.
	node, ok := rt.Spec.Nodes.Get("zeta/web")
	if !ok {
		t.Fatal("the decoded graph has no zeta/web node")
	}
	if node.SyncWave != 3 {
		t.Errorf("decoded sync wave = %d, want 3", node.SyncWave)
	}
	if node.SelfInject == nil {
		t.Error("the decoded node lost its self-inject block")
	}
	if node.CatalogMetadata == nil || node.CatalogMetadata.License != "Apache-2.0" {
		t.Errorf("decoded catalog metadata = %+v, want the licence carried", node.CatalogMetadata)
	}
	entry, ok := rt.Spec.FeatureValuesMapping.Get("zeta/web")
	if !ok {
		t.Fatal("the decoded graph has no attachments on zeta/web")
	}
	obs, ok := entry.Get("obs")
	if !ok {
		t.Fatal("the decoded graph lost the obs attachment")
	}
	if obs.Origin != types.OriginExplicit {
		t.Errorf("decoded attachment origin = %q, want %q", obs.Origin, types.OriginExplicit)
	}
	if !obs.Sensitive {
		t.Error("the decoded attachment lost its sensitivity marking")
	}
}

// TestHostWireIR_AuthorOrderSurvivesOnlyInTheVerbatimPayloads records what a
// decode of an IR graph does and does not recover, because the two halves of the
// graph behave differently and the difference is not visible from either half
// alone.
//
// A verbatim payload — a node's self-inject and uses blocks, an edge's values,
// infra and inject, an attachment's values, infra, inject and sensitive-inject, a
// boundary attachment's infra — is carried as one opaque document, so the order
// its author wrote lives inside those bytes and comes back out of them unchanged.
//
// Every other order-preserving container is encoded structurally, and the
// canonical encoding sorts a map's keys. That is the encoding's rule and the
// reason the bytes are identical across runs and across languages. It has a
// consequence, asserted here so it is measured rather than assumed: a decode
// rebuilds those containers in the wire's canonical order, so the author's order
// is not recoverable from the wire. The assertion below records that consequence.
// It is not a property to want, to rely on, or to preserve for its own sake — the
// canonical order is wanted, and this is what it costs.
//
// 🔴 Nothing in a build decodes an IR graph on the compiler side, which is why the
// consequence is inert today. It stops being inert the moment a compiler-side path
// decodes a graph and feeds the result to rendering: the rendered artifacts would
// then carry canonical order where the author's order was intended, which is a
// different output for the same input. Whoever wires such a path owes either a
// decode that does not reach rendered artifacts, or an order-preserving array form
// for these fields — a change both sides of the wire have to make together.
func TestHostWireIR_AuthorOrderSurvivesOnlyInTheVerbatimPayloads(t *testing.T) {
	t.Parallel()

	g := irFixture(t)
	got, err := g.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}
	var rt types.IRGraph
	if err := rt.DecodeCBOR(got); err != nil {
		t.Fatalf("compiler-side decode: %v", err)
	}

	node, ok := rt.Spec.Nodes.Get("zeta/web")
	if !ok {
		t.Fatal("the decoded graph has no zeta/web node")
	}
	attachments, ok := rt.Spec.FeatureValuesMapping.Get("zeta/web")
	if !ok {
		t.Fatal("the decoded graph has no attachments on zeta/web")
	}
	obs, ok := attachments.Get("obs")
	if !ok {
		t.Fatal("the decoded graph lost the obs attachment")
	}
	boundary, ok := rt.Spec.BoundaryFeatures.Get("prod/eks")
	if !ok {
		t.Fatal("the decoded graph lost its boundary attachments")
	}
	mesh, ok := boundary.Get("mesh")
	if !ok {
		t.Fatal("the decoded graph lost the mesh boundary attachment")
	}
	if len(rt.Spec.Edges) != 1 {
		t.Fatalf("decoded %d edges, want 1", len(rt.Spec.Edges))
	}
	edge := rt.Spec.Edges[0]

	// The verbatim payloads: author order, unchanged.
	authored := []struct {
		what string
		keys []string
		want []string
	}{
		{"attachment values", obs.Values.Keys(), []string{"zulu", "b", "mid"}},
		{"attachment inject", obs.Inject.Keys(), []string{"ZED", "AYE"}},
		{"attachment sensitive inject", obs.SensitiveInject.Keys(), []string{"ZED"}},
		{"edge values", edge.Values.Keys(), []string{"zulu", "b", "mid"}},
		{"edge inject", edge.Inject.Keys(), []string{"ZED", "AYE"}},
		{"node uses", node.Uses.Keys(), []string{"tools", "db"}},
		{"boundary attachment infra", mesh.Infra.Keys(), []string{"zulu", "b", "mid"}},
	}
	for _, c := range authored {
		if !sameKeys(c.keys, c.want) {
			t.Errorf("%s came back as %v, want the author's order %v — a verbatim payload carries its order inside its own bytes",
				c.what, c.keys, c.want)
		}
	}

	// The structurally encoded containers: the wire's canonical order, which is
	// length-then-bytewise and is not the order the fixture authored. Recorded, not
	// wanted; see this test's own documentation.
	structural := []struct {
		what string
		keys []string
		want []string
	}{
		{"node labels", node.Labels.Keys(), []string{"b", "zeta"}},
		{"node catalog annotations", node.CatalogAnnotations.Keys(), []string{"a", "zzz"}},
		{"attachment labels", obs.Labels.Keys(), []string{"b", "zeta"}},
		{"attachment values provenance", obs.ValuesProvenance.Keys(), []string{"b", "zulu"}},
		{"graph nodes", rt.Spec.Nodes.Keys(), []string{"db", "zeta/web"}},
		{"values mapping", rt.Spec.ValuesMapping.Keys(), []string{"db", "zeta/web"}},
	}
	for _, c := range structural {
		if !sameKeys(c.keys, c.want) {
			t.Errorf("%s came back as %v, want the wire's canonical order %v", c.what, c.keys, c.want)
		}
	}
}

// TestHostWireIR_FixtureOrderIsDiscriminating calibrates the order assertions
// above. Each of them compares a decoded key sequence against an expected one, and
// such a comparison proves nothing about where the order came from unless the
// author's order and the wire's canonical order actually differ: where the two
// coincide, an encoder that sorted a verbatim payload — or one that carried
// insertion order into a structural map — would pass unnoticed.
//
// So the fixture's own orders are checked here before the assertions that rest on
// them are read as evidence. The canonical order is recomputed independently rather
// than taken from the codec, for the same reason.
func TestHostWireIR_FixtureOrderIsDiscriminating(t *testing.T) {
	t.Parallel()

	g := irFixture(t)

	node, ok := g.Spec.Nodes.Get("zeta/web")
	if !ok {
		t.Fatal("the fixture has no zeta/web node")
	}
	attachments, ok := g.Spec.FeatureValuesMapping.Get("zeta/web")
	if !ok {
		t.Fatal("the fixture has no attachments on zeta/web")
	}
	obs, ok := attachments.Get("obs")
	if !ok {
		t.Fatal("the fixture has no obs attachment")
	}
	boundary, ok := g.Spec.BoundaryFeatures.Get("prod/eks")
	if !ok {
		t.Fatal("the fixture has no boundary attachments")
	}
	mesh, ok := boundary.Get("mesh")
	if !ok {
		t.Fatal("the fixture has no mesh boundary attachment")
	}
	if len(g.Spec.Edges) != 1 {
		t.Fatalf("the fixture carries %d edges, want 1", len(g.Spec.Edges))
	}

	cases := []struct {
		what string
		keys []string
	}{
		{"attachment values", obs.Values.Keys()},
		{"attachment inject", obs.Inject.Keys()},
		{"edge values", g.Spec.Edges[0].Values.Keys()},
		{"edge inject", g.Spec.Edges[0].Inject.Keys()},
		{"node uses", node.Uses.Keys()},
		{"boundary attachment infra", mesh.Infra.Keys()},
		{"node labels", node.Labels.Keys()},
		{"node catalog annotations", node.CatalogAnnotations.Keys()},
		{"attachment labels", obs.Labels.Keys()},
		{"attachment values provenance", obs.ValuesProvenance.Keys()},
		{"graph nodes", g.Spec.Nodes.Keys()},
		{"values mapping", g.Spec.ValuesMapping.Keys()},
	}
	for _, c := range cases {
		if len(c.keys) < 2 {
			t.Errorf("%s carries %d key(s); one key cannot distinguish an order from any other", c.what, len(c.keys))
			continue
		}
		if sameKeys(c.keys, canonicalOrder(c.keys)) {
			t.Errorf("%s is authored in %v, which is already the canonical order — an assertion on this container distinguishes nothing",
				c.what, c.keys)
		}
	}
}

// canonicalOrder returns the keys in the order the canonical encoding puts them:
// by encoded length first, then bytewise. Computed here from the rule rather than
// read from the codec under test, so the calibration does not depend on the thing
// it calibrates.
func canonicalOrder(keys []string) []string {
	out := append([]string(nil), keys...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && shorterThenBytewise(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func shorterThenBytewise(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

func sameKeys(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
