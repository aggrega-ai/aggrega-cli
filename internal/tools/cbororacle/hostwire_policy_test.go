// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cbororacle_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
)

// The policy payload family is the compiler-side codec whose graph field is a type
// declared in another package, so it is the one that proves a shared shape reaches
// the wire structurally rather than as a nested document. Its counterpart decodes
// the graph as a CBOR map; a codec that carried it as a byte-string of JSON bytes
// would compile, round-trip against itself, and pass any one-sided test while being
// unreadable by the plugin it exists to speak to. So the reference has to be
// something this codec had no hand in producing.
//
// Nothing in a build encodes these payloads in this form yet — the socket call
// sites still encode their inputs as JSON — so the codec is held by nothing but the
// tests below. A specification is not a consumer count.
//
// What anchors the bytes, stated plainly because the two payloads are anchored
// differently and the difference is a measured property rather than an omission:
//
//   - The graph-bearing payload is anchored by the FROZEN cross-language vector.
//     Its committed bytes are held by the plugin-side suite against an independent
//     reference encoder and by the Rust and TypeScript harnesses reading the same
//     corpus, so reproducing them is byte parity against an oracle. Two codecs can
//     agree on a shared mistake; a frozen committed document cannot move quietly.
//   - The render payload's frozen vector is NOT reproducible from the compiler side,
//     and that is a property of the compiler's own declaration rather than of this
//     codec: the settings document is held here as a decoded map, so it has no key
//     order to carry and rides in the order its own JSON encoding produces, while
//     the vector carries the order its author wrote. The vector still holds every
//     other field, asserted key by key, and the settings divergence is asserted as
//     exactly what it is — see
//     TestHostWirePolicy_FrozenRenderVectorDiffersOnlyInTheSettingsKeyOrder.
//   - Both payloads are additionally anchored by the published counterpart, which is
//     a different instrument and fails differently: it reads the compiler's bytes
//     and re-emits them, so a head that disagrees with its body, or a field one side
//     encodes structurally and the other opaquely, shows up there.
//
// The independent reference ENCODER is unavailable to these types, and the reason is
// structural rather than incidental: it dispatches on the CBOR struct tags, and the
// compiler-side declarations carry only the JSON and YAML ones. So it appears below
// only as a decoder, which is the half that is reachable.

// policyInputFixture builds a graph-bearing policy payload richer than the frozen
// vector: the graph is the one the IR exerciser builds, which carries every verbatim
// payload the graph schema declares, and the settings document is present, which the
// frozen vector omits.
func policyInputFixture(t *testing.T) analyzer.PolicyInput {
	t.Helper()
	return analyzer.PolicyInput{
		Graph: irFixture(t),
		PrecalculatedPaths: []analyzer.PrecalculatedPath{
			{
				PathID:           "path_01",
				Nodes:            []string{"zeta/web", "db"},
				AttachedFeatures: []string{"feature/observability"},
			},
		},
		Sources: []analyzer.PolicySource{
			{Path: "deny-latest.rego", Content: "package aggrega\n"},
			{Path: "require-labels.rego", Content: "package aggrega\n"},
		},
		Settings: map[string]any{"strict": true, "exempt": []any{"core/web"}},
	}
}

// renderPolicyFixture builds a render-stage policy payload whose artifact tree is
// authored in an order that is neither the canonical one nor the alphabetical one,
// so an assertion that the order survived distinguishes a verbatim carry from a
// re-encoded one. TestHostWirePolicy_FixtureOrderIsDiscriminating pins that property
// of the fixture itself.
func renderPolicyFixture(t *testing.T) analyzer.RenderPolicyInput {
	t.Helper()
	return analyzer.RenderPolicyInput{
		Artifacts: []analyzer.RenderedArtifact{
			{
				Path:    "base/deployment.yaml",
				Content: "apiVersion: apps/v1\nkind: Deployment\n",
				Tree:    json.RawMessage(`{"beta":"b-value","a":"a-value","middle":"m-value"}`),
				Format:  "yaml",
			},
			{Path: "base/service.yaml", Content: "apiVersion: v1\nkind: Service\n"},
		},
		Sources: []analyzer.PolicySource{
			{Path: "deny-latest.rego", Content: "package aggrega\n"},
			{Path: "require-labels.rego", Content: "package aggrega\n"},
		},
		Settings: map[string]any{"strict": true, "exempt": []any{"core/web"}},
	}
}

// TestHostWirePolicy_MatchesFrozenVector holds the graph-bearing payload's
// compiler-side codec to the frozen shared vector, which is the strongest reference
// available to it.
//
// This is the assertion the whole cross-package dispatch rests on. The vector's
// graph is a CBOR map, because the plugin-side codec that produced it encodes the
// graph structurally. A compiler-side codec that fell back to carrying the graph as
// a byte-string of JSON bytes produces different bytes here and fails, which is the
// one failure mode that no self-consistent test can see.
func TestHostWirePolicy_MatchesFrozenVector(t *testing.T) {
	t.Parallel()

	var in analyzer.PolicyInput
	if err := json.Unmarshal(sharedVector(t, "PolicyInput.golden.json"), &in); err != nil {
		t.Fatalf("compiler-side decode of the frozen document: %v", err)
	}
	got, err := in.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}

	want := string(sharedVector(t, "PolicyInput.cbor.hex"))
	if h := hex.EncodeToString(got); h != want {
		t.Errorf("the compiler-side bytes are not the frozen canonical bytes\n frozen: %s\n    got: %s", want, h)
	}

	// Read through the independent decoder as well: it consumes exactly the pair
	// count each map head declares, so it catches a head disagreeing with its body in
	// either direction — which re-decoding with this codec's own counterpart cannot
	// see. It also establishes that the graph arrived as a MAP rather than as a
	// byte-string, which is the property the dispatch exists for and the one a byte
	// compare alone does not name.
	top := refDecode(t, "PolicyInput", got)
	if len(top) != 2 {
		t.Errorf("the independent decoder read %d top-level keys, want 2 (graph, precalculated_paths)", len(top))
	}
	if _, isBytes := top["graph"].([]byte); isBytes {
		t.Error("the graph arrived as a byte-string: the compiler side carried it opaquely where the " +
			"plugin side decodes a structural map, so neither end can read the other")
	}

	again, err := in.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side re-encode: %v", err)
	}
	if !bytes.Equal(got, again) {
		t.Errorf("encoding the same payload twice produced different bytes\nfirst:  %s\nsecond: %s",
			hex.EncodeToString(got), hex.EncodeToString(again))
	}
}

// TestHostGuestWireParity_PolicyPayloads asserts the two sides of the wire speak the
// same bytes for both payloads in the family, on fixtures richer than the frozen
// vectors. The sides do not hold the same Go types — the graph is the compiler's own
// declaration here and the plugin's generated one there, and the settings document is
// a decoded map here and an opaque document there — so agreement is a property to
// assert rather than one the shared schema delivers by itself.
//
// The round trip through the compiler-side decoder is asserted alongside, because it
// is the only path that exercises the decode side at all, and the re-encode is what
// makes a lossy decode observable rather than silent.
func TestHostGuestWireParity_PolicyPayloads(t *testing.T) {
	t.Parallel()

	t.Run("PolicyInput", func(t *testing.T) {
		t.Parallel()

		in := policyInputFixture(t)
		got, err := in.EncodeCBOR()
		if err != nil {
			t.Fatalf("compiler-side encode: %v", err)
		}
		again, err := in.EncodeCBOR()
		if err != nil {
			t.Fatalf("compiler-side re-encode: %v", err)
		}
		if !bytes.Equal(got, again) {
			t.Errorf("encoding the same payload twice produced different bytes\nfirst:  %s\nsecond: %s",
				hex.EncodeToString(got), hex.EncodeToString(again))
		}

		crossSide(t, "PolicyInput", got, &abi.PolicyInput{})

		var rt analyzer.PolicyInput
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

		// Spot checks on values the round trip must carry through the cross-package
		// dispatch, asserted directly rather than inferred from the byte compare, so a
		// failure names the property. The graph is the one that matters: it is decoded
		// by another package's codec entirely.
		node, ok := rt.Graph.Spec.Nodes.Get("zeta/web")
		if !ok {
			t.Fatal("the decoded payload's graph has no zeta/web node")
		}
		if node.SyncWave != 3 {
			t.Errorf("decoded sync wave = %d, want 3", node.SyncWave)
		}
		if rt.Graph.Metadata.AggregateName != "shop" {
			t.Errorf("decoded aggregate name = %q, want %q", rt.Graph.Metadata.AggregateName, "shop")
		}
		if len(rt.PrecalculatedPaths) != 1 || rt.PrecalculatedPaths[0].PathID != "path_01" {
			t.Errorf("decoded precalculated paths = %+v, want the one path carried", rt.PrecalculatedPaths)
		}
		if len(rt.Sources) != 2 {
			t.Errorf("decoded sources = %d, want 2", len(rt.Sources))
		}
	})

	t.Run("RenderPolicyInput", func(t *testing.T) {
		t.Parallel()

		in := renderPolicyFixture(t)
		got, err := in.EncodeCBOR()
		if err != nil {
			t.Fatalf("compiler-side encode: %v", err)
		}
		again, err := in.EncodeCBOR()
		if err != nil {
			t.Fatalf("compiler-side re-encode: %v", err)
		}
		if !bytes.Equal(got, again) {
			t.Errorf("encoding the same payload twice produced different bytes\nfirst:  %s\nsecond: %s",
				hex.EncodeToString(got), hex.EncodeToString(again))
		}

		crossSide(t, "RenderPolicyInput", got, &abi.RenderPolicyInput{})

		var rt analyzer.RenderPolicyInput
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

		if len(rt.Artifacts) != 2 {
			t.Fatalf("decoded artifacts = %d, want 2", len(rt.Artifacts))
		}
		if rt.Artifacts[0].Format != "yaml" {
			t.Errorf("decoded format = %q, want %q", rt.Artifacts[0].Format, "yaml")
		}
		if rt.Artifacts[1].Tree != nil {
			t.Errorf("the second artifact carries no tree, yet one decoded: %s", rt.Artifacts[1].Tree)
		}
	})
}

// TestHostWirePolicy_FrozenRenderVectorDiffersOnlyInTheSettingsKeyOrder measures the
// one place the compiler side cannot reproduce the frozen corpus, and bounds it
// exactly rather than leaving it as an untested caveat.
//
// The cause is in the compiler's own declaration and predates this codec: the
// settings document is declared as a decoded map, so by the time any encoder sees it
// the order its author wrote is already gone, and what goes on the wire is whatever
// the map's own JSON encoding produces. The frozen vector was produced by the plugin
// side, which holds the same document opaquely and therefore carries the author's
// order inside its bytes. Both ends read the document by key, so the two orders are
// the same document; the bytes are not the same bytes.
//
// Asserted positively, key by key, because "the bytes differ" on its own would also
// pass if the graph, the artifacts or the sources had gone wrong.
func TestHostWirePolicy_FrozenRenderVectorDiffersOnlyInTheSettingsKeyOrder(t *testing.T) {
	t.Parallel()

	var in analyzer.RenderPolicyInput
	if err := json.Unmarshal(sharedVector(t, "RenderPolicyInput.golden.json"), &in); err != nil {
		t.Fatalf("compiler-side decode of the frozen document: %v", err)
	}
	got, err := in.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}
	frozen, err := hex.DecodeString(string(sharedVector(t, "RenderPolicyInput.cbor.hex")))
	if err != nil {
		t.Fatalf("decode the frozen hex: %v", err)
	}

	hostSide := refDecode(t, "RenderPolicyInput (compiler side)", got)
	frozenSide := refDecode(t, "RenderPolicyInput (frozen)", frozen)

	if len(hostSide) != len(frozenSide) {
		t.Fatalf("the compiler side emitted %d top-level keys and the frozen document carries %d",
			len(hostSide), len(frozenSide))
	}
	for key := range frozenSide {
		if _, ok := hostSide[key]; !ok {
			t.Errorf("the compiler side omitted the key %q the frozen document carries", key)
		}
	}

	// Every field but the settings document must match the frozen bytes exactly, read
	// through the independent decoder so the comparison is of decoded values rather
	// than of a byte range this test chose.
	for key, want := range frozenSide {
		if key == "settings" {
			continue
		}
		if !reflect.DeepEqual(hostSide[key], want) {
			t.Errorf("%q does not match the frozen document\n frozen: %#v\n    got: %#v", key, want, hostSide[key])
		}
	}

	gotSettings, ok := hostSide["settings"].([]byte)
	if !ok {
		t.Fatalf("the compiler side's settings is %T, want the opaque byte-string the schema declares",
			hostSide["settings"])
	}
	frozenSettings, ok := frozenSide["settings"].([]byte)
	if !ok {
		t.Fatalf("the frozen settings is %T, want an opaque byte-string", frozenSide["settings"])
	}

	// The divergence, asserted rather than assumed: were these equal, the property
	// this test documents would have changed and the documentation above would be the
	// thing that is now wrong.
	if bytes.Equal(gotSettings, frozenSettings) {
		t.Errorf("the compiler side reproduced the frozen settings bytes exactly (%s); the declaration "+
			"that made that impossible has changed, so this test and the reasoning above it are stale",
			gotSettings)
	}

	// And the difference is key order and nothing else: the two byte-strings are the
	// same document.
	var gotDoc, frozenParsed map[string]any
	if err := json.Unmarshal(gotSettings, &gotDoc); err != nil {
		t.Fatalf("the compiler side's settings bytes are not a JSON document: %v", err)
	}
	if err := json.Unmarshal(frozenSettings, &frozenParsed); err != nil {
		t.Fatalf("the frozen settings bytes are not a JSON document: %v", err)
	}
	if !reflect.DeepEqual(gotDoc, frozenParsed) {
		t.Errorf("the two settings documents are not the same document\n frozen: %#v\n    got: %#v",
			frozenParsed, gotDoc)
	}

	// The order the compiler side puts them in is its map encoding's own, which is
	// what makes the divergence a property of the declaration rather than of a choice
	// this codec made.
	direct, err := json.Marshal(in.Settings)
	if err != nil {
		t.Fatalf("encode the settings map directly: %v", err)
	}
	if !bytes.Equal(gotSettings, direct) {
		t.Errorf("the settings on the wire are not the map's own encoding\n direct: %s\n   wire: %s",
			direct, gotSettings)
	}
}

// TestHostWirePolicy_AuthorOrderSurvivesInTheTreeAndNotInSettings records what the
// two opaque documents in this family do and do not carry, because they behave
// differently and the difference is not visible from either one alone.
//
// The artifact tree is declared as the opaque document it is on the wire, so its
// bytes ride verbatim and the order its author wrote comes back out of them
// unchanged. The settings document is declared as a decoded map, so there is no
// author order left by the time it is encoded, and the wire carries the order the
// map's own JSON encoding produces.
//
// Neither is a property to want or to rely on for its own sake. They are what the
// two declarations cost, asserted so the cost is measured; and the asymmetry is the
// reason a reader must not "align" the two by changing either declaration without
// moving both ends of the wire together.
func TestHostWirePolicy_AuthorOrderSurvivesInTheTreeAndNotInSettings(t *testing.T) {
	t.Parallel()

	in := renderPolicyFixture(t)
	authored := jsonObjectKeys(t, in.Artifacts[0].Tree)

	got, err := in.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}

	// Read the tree back through the PUBLISHED counterpart rather than through the
	// compiler's own decoder, so the property is asserted about what the other end of
	// the wire actually receives.
	var guest abi.RenderPolicyInput
	if err := guest.DecodeCBOR(got); err != nil {
		t.Fatalf("plugin-side decode of compiler-side bytes: %v", err)
	}
	if len(guest.Artifacts) == 0 {
		t.Fatal("the plugin side decoded no artifacts")
	}
	if carried := jsonObjectKeys(t, guest.Artifacts[0].Tree); !sameKeys(carried, authored) {
		t.Errorf("the artifact tree came back as %v, want the author's order %v", carried, authored)
	}

	// The settings document, at the same end of the wire: its keys are in the order
	// the map's encoding produces, which is bytewise, and NOT any order an author
	// could have chosen.
	settings := jsonObjectKeys(t, guest.Settings)
	bytewise := append([]string(nil), settings...)
	sort.Strings(bytewise)
	if !sameKeys(settings, bytewise) {
		t.Errorf("the settings document came back as %v, want the map encoding's own order %v — the "+
			"compiler holds this document as a decoded map, so no other order is reachable",
			settings, bytewise)
	}
}

// TestHostWirePolicy_FixtureOrderIsDiscriminating calibrates the order assertions
// above. Each compares a key sequence against an expected one, and such a comparison
// proves nothing about where the order came from unless the orders being told apart
// actually differ: where they coincide, an encoder that re-sorted a verbatim
// document would pass unnoticed.
//
// So the fixture's own orders are checked here before the assertions resting on them
// are read as evidence. The canonical order is recomputed from the rule rather than
// read from the codec under test, for the same reason.
func TestHostWirePolicy_FixtureOrderIsDiscriminating(t *testing.T) {
	t.Parallel()

	in := renderPolicyFixture(t)
	authored := jsonObjectKeys(t, in.Artifacts[0].Tree)
	if len(authored) < 2 {
		t.Fatalf("the artifact tree carries %d key(s); one key cannot distinguish an order from any other",
			len(authored))
	}
	if sameKeys(authored, canonicalOrder(authored)) {
		t.Errorf("the artifact tree is authored in %v, which is already the canonical order — an "+
			"assertion on this document distinguishes nothing", authored)
	}
	bytewise := append([]string(nil), authored...)
	sort.Strings(bytewise)
	if sameKeys(authored, bytewise) {
		t.Errorf("the artifact tree is authored in %v, which is already the bytewise order a map "+
			"encoding produces — an assertion on this document cannot tell a verbatim carry from a "+
			"re-encoded one", authored)
	}

	// The settings assertion is calibrated against the frozen corpus rather than
	// against this fixture, because a Go map has no authored order to compare with:
	// the frozen document is where an author's order for this field exists, and the
	// divergence test is only meaningful if that order differs from the one a map
	// encoding produces.
	frozen, err := hex.DecodeString(string(sharedVector(t, "RenderPolicyInput.cbor.hex")))
	if err != nil {
		t.Fatalf("decode the frozen hex: %v", err)
	}
	frozenSettings, ok := refDecode(t, "RenderPolicyInput (frozen)", frozen)["settings"].([]byte)
	if !ok {
		t.Fatal("the frozen render payload carries no opaque settings document, so the divergence " +
			"assertion has nothing to be calibrated against")
	}
	frozenKeys := jsonObjectKeys(t, frozenSettings)
	if len(frozenKeys) < 2 {
		t.Fatalf("the frozen settings document carries %d key(s); one key cannot distinguish an order "+
			"from any other", len(frozenKeys))
	}
	frozenBytewise := append([]string(nil), frozenKeys...)
	sort.Strings(frozenBytewise)
	if sameKeys(frozenKeys, frozenBytewise) {
		t.Errorf("the frozen settings document is authored in %v, which is already the bytewise order "+
			"a map encoding produces — the divergence assertion would pass whether the order was "+
			"carried or lost", frozenKeys)
	}
}

// jsonObjectKeys returns a JSON object's keys in document order, read with a token
// walk because decoding into a map is exactly what loses the order this is for.
func jsonObjectKeys(t *testing.T, doc []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(doc))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("read the document's opening token: %v", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		t.Fatalf("the document opens with %v, want a JSON object", tok)
	}
	var keys []string
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			t.Fatalf("read a key: %v", err)
		}
		name, ok := key.(string)
		if !ok {
			t.Fatalf("read %v where a key was expected", key)
		}
		keys = append(keys, name)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("skip the value of %q: %v", name, err)
		}
	}
	return keys
}
