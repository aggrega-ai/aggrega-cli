// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginwire

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestEncodeForExportBinaryWire proves the encode arm produces the bytes the
// guest reads, byte for byte, rather than merely producing something.
//
// The expected bytes come from the payload shape's own encoder rather than from a
// literal, because a literal would only restate whatever the encoder emits; what
// this pins is that the command's path reaches that encoder at all, which is the
// half that was missing.
func TestEncodeForExportBinaryWire(t *testing.T) {
	t.Parallel()

	want, err := topology.MutatorInput{}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the empty payload shape: %v", err)
	}
	got, err := EncodeForExport("mutate", []byte(`{}`))
	if err != nil {
		t.Fatalf("encoding for a binary-wire export should succeed; got %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("encoded payload = %x; want %x", got, want)
	}
	if bytes.Equal(got, []byte(`{}`)) {
		t.Fatalf("the payload reached the guest unchanged, so nothing encoded it")
	}
}

// TestEncodeForExportConvertsAnImporterInput covers an export whose name is fixed
// by its plugin type rather than defaulted by a governance role: the importer's
// export. The authored payload is the JSON form of the import input — its source
// bytes base64-encoded, its settings an ordinary JSON object — and what reaches
// the guest must be byte for byte what the host itself hands an importer for the
// same input, so a guest driven directly reads exactly what a real import gives
// it.
//
// The export name is read from the plugin-type registry rather than spelled here.
func TestEncodeForExportConvertsAnImporterInput(t *testing.T) {
	t.Parallel()

	importer, ok := plugintype.Lookup(plugintype.Importer)
	if !ok || importer.MinExport.Name == "" || importer.MinExport.IsPrefix {
		t.Fatal("the importer type has no exact required export")
	}
	want, err := abi.ImportInput{
		Format:   "demo",
		Bytes:    []byte("hello world"),
		Settings: json.RawMessage(`{"target_namespace":"acme"}`),
	}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the import input: %v", err)
	}
	authored := []byte(`{"format":"demo","bytes":"aGVsbG8gd29ybGQ=","settings":{"target_namespace":"acme"}}`)
	got, err := EncodeForExport(importer.MinExport.Name, authored)
	if err != nil {
		t.Fatalf("encoding the importer's authored input should succeed; got %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("encoded payload = %x; want %x", got, want)
	}
}

// TestEncodeForExportConvertsAParserInput covers the parser's export, which is
// fixed by its plugin type like the importer's. The authored payload is the JSON
// form of the parse input, its source bytes base64-encoded, and what reaches the
// guest must be byte for byte what the host itself hands a parser for the same
// input. The source is deliberately not valid UTF-8, so the bytes the guest reads
// can only be right if they crossed as a raw byte string: base64 text in their
// place would be a different, longer byte run.
//
// The export name is read from the plugin-type registry rather than spelled here.
func TestEncodeForExportConvertsAParserInput(t *testing.T) {
	t.Parallel()

	parser, ok := plugintype.Lookup(plugintype.Parser)
	if !ok || parser.MinExport.Name == "" || parser.MinExport.IsPrefix {
		t.Fatal("the parser type has no exact required export")
	}
	source := []byte{0xff, 0xfe, 'k', ' ', '=', ' ', 0xc3, 0x28, '\n'}
	want, err := abi.ParseInput{
		Format:  "demo",
		Bytes:   source,
		Options: json.RawMessage(`{"strict":true}`),
	}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the parse input: %v", err)
	}
	authored := []byte(`{"format":"demo","bytes":"//5rID0gwygK","options":{"strict":true}}`)
	got, err := EncodeForExport(parser.MinExport.Name, authored)
	if err != nil {
		t.Fatalf("encoding the parser's authored input should succeed; got %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("encoded payload = %x; want %x", got, want)
	}
	if !bytes.Contains(got, source) {
		t.Fatalf("encoded payload %x does not carry the source bytes %x as a raw run", got, source)
	}
}

// TestEncodeForExportTextWirePassesThrough covers the two ways an export gets its
// authored bytes back: a text-wire export the table knows, and an export the
// table does not know at all. The second matters as much as the first — a guest
// reads its own input and reports its own failure, so an unknown name is passed
// through rather than refused.
//
// Every row the generated table carries is on the binary wire, so the text-wire
// arm is driven through a table built here, the way the refusal below is; the
// unknown export goes through the generated table, where it is unknown.
func TestEncodeForExportTextWirePassesThrough(t *testing.T) {
	t.Parallel()

	text := []byte(`{"k":1}`)
	t.Run("text-wire row", func(t *testing.T) {
		t.Parallel()
		table := []entry{
			{export: "verdict_ir", shape: "PolicyInput", binary: false, encode: failEncode(t)},
		}
		got, err := encodeForExport(table, "verdict_ir", text)
		if err != nil {
			t.Fatalf("a text-wire export should pass its payload through; got %v", err)
		}
		if !bytes.Equal(got, text) {
			t.Fatalf("payload = %q; want it unchanged at %q", got, text)
		}
	})
	t.Run("unknown export", func(t *testing.T) {
		t.Parallel()
		got, err := EncodeForExport("describe", text)
		if err != nil {
			t.Fatalf("an export the table does not know should pass its payload through; got %v", err)
		}
		if !bytes.Equal(got, text) {
			t.Fatalf("payload = %q; want it unchanged at %q", got, text)
		}
	})
}

// TestEncodeForExportConvertsTheGovernanceRoleInputs covers the governance roles
// whose payloads the generated table converts besides the label-transform one:
// the two verdict roles and the annotation-transform role. For each, the authored
// payload is the JSON form of the role's input, and what reaches the guest must be
// byte for byte what the host itself hands a guest serving that role for the same
// input — so a guest driven directly reads exactly what a build gives it.
//
// The graph's nodes, one node's labels and the target's annotations are authored
// in an order that is neither sorted nor reversed. The host holds them in the
// order they were written, so a conversion that sorted them on the way would
// produce different bytes and fail here.
//
// The export names are read from the role registry rather than spelled here.
func TestEncodeForExportConvertsTheGovernanceRoleInputs(t *testing.T) {
	t.Parallel()

	var labels types.OrderedMap[string]
	for _, k := range []string{"d", "8080", "bbbb", "aaaaaaaa", "cc"} {
		labels.Set(k, "v-"+k)
	}
	var graph types.IRGraph
	graph.APIVersion = types.IRAPIVersion
	graph.Kind = types.IRKind
	graph.Spec.Nodes.Set("web", types.IRNode{Labels: labels, Component: "example/web", ResolvedVersion: "1.0.0"})
	graph.Spec.Nodes.Set("api", types.IRNode{Component: "example/api", ResolvedVersion: "2.0.0"})
	graph.Spec.Edges = []types.IREdge{{Source: "web", Target: "api", SocketDeclaration: types.SocketDeclarationAdHoc}}
	policy := analyzer.PolicyInput{
		Graph:              graph,
		PrecalculatedPaths: []analyzer.PrecalculatedPath{{PathID: "path_01", Nodes: []string{"web", "api"}, AttachedFeatures: []string{}}},
		Sources:            []analyzer.PolicySource{{Path: "policy.rego", Content: "package p\n"}},
		Settings:           map[string]any{"limit": json.Number("3")},
	}
	render := analyzer.RenderPolicyInput{
		Artifacts: []analyzer.RenderedArtifact{{Path: "main.tf", Content: "a = 1\n", Tree: json.RawMessage(`{"a":1}`), Format: "hcl"}},
		Settings:  map[string]any{"limit": json.Number("3")},
	}
	var annotations types.OrderedMap[string]
	for _, k := range []string{"zz.example.com/last", "aa.example.com/first", "mm.example.com/mid"} {
		annotations.Set(k, "v")
	}
	annotation := pipeline.AnnotationTransformInput{
		Targets:     []pipeline.AnnotationTarget{{BoundaryPath: "main", ComponentName: "web", Annotations: annotations}},
		BindingKeys: []string{"backup.example.com/schedule"},
		Settings:    json.RawMessage(`{"limit":3}`),
	}

	cases := []struct {
		role  plugintype.Role
		value interface{ EncodeCBOR() ([]byte, error) }
	}{
		{plugintype.RoleIRVerdict, policy},
		{plugintype.RoleRenderVerdict, render},
		{plugintype.RoleAnnotationTransform, annotation},
	}
	for _, tc := range cases {
		export, ok := plugintype.GovernanceRoleExport(tc.role)
		if !ok {
			t.Fatalf("the %s role has no default export", tc.role)
		}
		t.Run(export, func(t *testing.T) {
			t.Parallel()
			want, err := tc.value.EncodeCBOR()
			if err != nil {
				t.Fatalf("encode the %s input: %v", tc.role, err)
			}
			authored, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("write the authored payload: %v", err)
			}
			got, err := EncodeForExport(export, authored)
			if err != nil {
				t.Fatalf("encoding the %s role's authored input should succeed; got %v", tc.role, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("encoded payload = %x; want %x", got, want)
			}
		})
	}
}

// TestEncodeForExportRefusesAnAmbiguousBinaryExport drives the arm the generated
// table cannot reach yet: one export naming two payload shapes, on a socket whose
// wire has moved to binary. The refusal is the point — encoding as one of the two
// would hand the guest bytes it silently misreads, and an export name cannot say
// which shape was meant.
func TestEncodeForExportRefusesAnAmbiguousBinaryExport(t *testing.T) {
	t.Parallel()

	table := []entry{
		{export: "verdict_ir", shape: "PolicyInput", binary: true, encode: failEncode(t)},
		{export: "verdict_ir", shape: "RenderPolicyInput", binary: true, encode: failEncode(t)},
	}
	_, err := encodeForExport(table, "verdict_ir", []byte(`{}`))
	if err == nil {
		t.Fatal("an export naming two payload shapes on a binary wire should refuse rather than guess")
	}
	for _, want := range []string{"verdict_ir", "PolicyInput", "RenderPolicyInput"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestEncodeForExportEncodesAnUnambiguousBinaryExport is the control for the
// refusal above: the same synthetic shape with one payload shape encodes.
func TestEncodeForExportEncodesAnUnambiguousBinaryExport(t *testing.T) {
	t.Parallel()

	table := []entry{
		{export: "verdict_ir", shape: "PolicyInput", binary: true, encode: func(text []byte) ([]byte, error) {
			return append([]byte("encoded:"), text...), nil
		}},
	}
	got, err := encodeForExport(table, "verdict_ir", []byte(`{}`))
	if err != nil {
		t.Fatalf("one payload shape on a binary wire should encode; got %v", err)
	}
	if string(got) != "encoded:{}" {
		t.Fatalf("payload = %q; want the table's own encoder to have run", got)
	}
}

// failEncode returns an encoder that fails the test if it is called, so a
// refusal that quietly encoded one of two shapes cannot read as a refusal.
func failEncode(t *testing.T) func([]byte) ([]byte, error) {
	t.Helper()
	return func([]byte) ([]byte, error) {
		t.Error("an ambiguous binary-wire export must refuse before any encoder runs")
		return nil, nil
	}
}

// TestEveryRegisteredEncoderRuns exercises every row's encoder, not only the
// rows whose socket has already moved.
//
// The encoders are emitted for every row on purpose, so that moving a socket to
// the binary wire is a one-field edit on its registration rather than new code.
// The cost of that is three encoders nothing calls yet, and an unexercised
// encoder is a mechanism whose first real use is also its first test — at the
// moment a wire moves, which is the worst moment to discover it. So each one is
// driven here.
//
// The assertion is the shape of the result rather than its bytes: a canonical
// encoding of a struct is a CBOR map, whose leading byte carries major type 5.
// That is a property of the encoding rather than of any one payload shape, so it
// holds for a row added later without this test being rewritten.
func TestEveryRegisteredEncoderRuns(t *testing.T) {
	t.Parallel()

	if len(entries) == 0 {
		t.Fatal("the generated registry is empty, so this test asserts nothing")
	}
	for _, e := range entries {
		t.Run(e.export+"/"+e.shape, func(t *testing.T) {
			t.Parallel()
			if e.encode == nil {
				t.Fatalf("row %s/%s carries no encoder", e.export, e.shape)
			}
			got, err := e.encode([]byte(`{}`))
			if err != nil {
				t.Fatalf("encoding the empty payload should succeed; got %v", err)
			}
			if len(got) == 0 {
				t.Fatal("the encoder produced no bytes")
			}
			if major := got[0] >> 5; major != 5 {
				t.Fatalf("encoded payload starts with major type %d (% x); a canonical struct "+
					"encoding is a map, which is major type 5", major, got)
			}
		})
	}
}
