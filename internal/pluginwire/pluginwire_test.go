// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
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

// TestEncodeForExportConvertsAStepInput covers the pipeline-step type's export.
// The authored payload is the JSON form of the step input, and what reaches the
// guest must be byte for byte what the walk itself hands a step for the same input,
// so a step driven directly reads exactly what a build gives it. The documents and
// the `with` keys are authored in an order no sort produces, an integer-like key
// among them: the walk holds them in the order they were rendered, so a conversion
// that sorted them on the way would produce different bytes and fail here.
//
// The export name is read from the plugin-type registry rather than spelled here.
func TestEncodeForExportConvertsAStepInput(t *testing.T) {
	t.Parallel()

	step, ok := plugintype.Lookup(plugintype.PipelineStep)
	if !ok || len(step.HostExports) != 1 {
		t.Fatal("the pipeline-step type lists no single host export")
	}
	var own types.OrderedMap[frp.Unit]
	own.Set("z.yaml", frp.Unit{Token: "z.yaml", MediaType: "application/yaml", Tree: json.RawMessage(`{"kind":"Service"}`)})
	own.Set("a.yaml", frp.Unit{Token: "a.yaml", MediaType: "application/yaml", Tree: json.RawMessage(`{"kind":"Deployment"}`)})
	var with types.OrderedMap[any]
	with.Set("zeta", "z")
	with.Set("10", "ten")
	with.Set("alpha", "a")
	want, err := frp.FRPStepInput{Documents: frp.DocumentSet{Own: own}, StepInput: with, StepIndex: 2}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the step input: %v", err)
	}
	authored := []byte(`{"documents":{"own":{` +
		`"z.yaml":{"token":"z.yaml","media_type":"application/yaml","tree":{"kind":"Service"}},` +
		`"a.yaml":{"token":"a.yaml","media_type":"application/yaml","tree":{"kind":"Deployment"}}}},` +
		`"step_input":{"zeta":"z","10":"ten","alpha":"a"},"step_index":2}`)
	got, err := EncodeForExport(step.HostExports[0], authored)
	if err != nil {
		t.Fatalf("encoding the step's authored input should succeed; got %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("encoded payload = %x; want %x", got, want)
	}
}

// TestEncodeForExportPassesAnUnlistedExportThrough covers the exports that get
// their authored bytes back because the generated table does not list them: the
// describe export, which takes no payload; the CLI type's host export, whose
// payload crosses as JSON; and an entry function an attachment names, which no
// socket calls by default. A guest reads its own input and reports its own
// failure, so an unlisted name is passed through rather than refused.
//
// Each goes through the generated table, and each is first required to be absent
// from it, so a row cannot pass because an export it names was listed and its
// encoder happened to return the bytes it was handed.
func TestEncodeForExportPassesAnUnlistedExportThrough(t *testing.T) {
	t.Parallel()

	cli, ok := plugintype.Lookup(plugintype.CLI)
	if !ok || len(cli.HostExports) != 1 {
		t.Fatal("the CLI type lists no single host export")
	}
	text := []byte(`{"k":1}`)
	for _, export := range []string{"describe", cli.HostExports[0], "verdict_an_attachment_names"} {
		t.Run(export, func(t *testing.T) {
			t.Parallel()
			for _, e := range entries {
				if e.export == export {
					t.Fatalf("the generated table lists %q, so this row does not drive the pass-through", export)
				}
			}
			got, err := EncodeForExport(export, text)
			if err != nil {
				t.Fatalf("an export the table does not list should pass its payload through; got %v", err)
			}
			if !bytes.Equal(got, text) {
				t.Fatalf("payload = %q; want it unchanged at %q", got, text)
			}
		})
	}
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
	var annotations types.OrderedMap[any]
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
// table cannot reach: one export naming two payload shapes. The refusal is the
// point — encoding as one of the two would hand the guest bytes it silently
// misreads, and an export name cannot say which shape was meant.
func TestEncodeForExportRefusesAnAmbiguousBinaryExport(t *testing.T) {
	t.Parallel()

	table := []entry{
		{export: "verdict_ir", shape: "PolicyInput", encode: failEncode(t)},
		{export: "verdict_ir", shape: "RenderPolicyInput", encode: failEncode(t)},
	}
	_, err := encodeForExport(table, "verdict_ir", []byte(`{}`))
	if err == nil {
		t.Fatal("an export naming two payload shapes should refuse rather than guess")
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
		{export: "verdict_ir", shape: "PolicyInput", encode: func(text []byte) ([]byte, error) {
			return append([]byte("encoded:"), text...), nil
		}},
	}
	got, err := encodeForExport(table, "verdict_ir", []byte(`{}`))
	if err != nil {
		t.Fatalf("one payload shape should encode; got %v", err)
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
		t.Error("an ambiguous export must refuse before any encoder runs")
		return nil, nil
	}
}

// TestDecodeForExportPrintsTheGovernanceRoleAnswers drives the answer side of
// every governance role: a role's answer, written in the canonical binary
// encoding as a guest serving that role writes it, comes back as the JSON text of
// the same value. The expected text is the value's own JSON, written the way the
// conversion writes it, so this pins that the conversion decoded the answer into
// the role's output shape rather than producing some text.
//
// A message carrying markup characters is part of the verdict, because a policy
// message is prose a person reads and the text must carry it as written.
//
// The export names are read from the role registry rather than spelled here.
func TestDecodeForExportPrintsTheGovernanceRoleAnswers(t *testing.T) {
	t.Parallel()

	verdicts := abi.PolicyOutput{Results: []abi.Diagnostic{
		{Severity: abi.SeverityHalt, Code: "example-rule", Message: "the <web> component must carry an owner label"},
	}}
	mutation := topology.MutatorOutput{Patches: []json.RawMessage{
		json.RawMessage(`{"op":"add","path":"/spec/nodes/web/labels/team","value":"payments"}`),
	}}

	cases := []struct {
		role  plugintype.Role
		value interface{ EncodeCBOR() ([]byte, error) }
	}{
		{plugintype.RoleIRVerdict, verdicts},
		{plugintype.RoleRenderVerdict, verdicts},
		{plugintype.RoleLabelTransform, mutation},
		{plugintype.RoleAnnotationTransform, pipeline.AnnotationTransformOutput{}},
	}
	for _, tc := range cases {
		export, ok := plugintype.GovernanceRoleExport(tc.role)
		if !ok {
			t.Fatalf("the %s role has no default export", tc.role)
		}
		t.Run(export, func(t *testing.T) {
			t.Parallel()
			answer, err := tc.value.EncodeCBOR()
			if err != nil {
				t.Fatalf("encode the %s answer: %v", tc.role, err)
			}
			var want bytes.Buffer
			enc := json.NewEncoder(&want)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			if err := enc.Encode(tc.value); err != nil {
				t.Fatalf("write the expected text: %v", err)
			}
			got, err := DecodeForExport(export, answer)
			if err != nil {
				t.Fatalf("decoding the %s role's answer should succeed; got %v", tc.role, err)
			}
			if string(got) != want.String() {
				t.Fatalf("printed answer =\n%s\nwant\n%s", got, want.String())
			}
		})
	}
}

// TestDecodeForExportRefusesAnAnswerThatIsNotTheShape hands a listed export an
// answer that is not its output shape — JSON text where the binary encoding is
// read — and requires the refusal the host's own socket call raises, naming the
// export, rather than a printed half-read answer.
func TestDecodeForExportRefusesAnAnswerThatIsNotTheShape(t *testing.T) {
	t.Parallel()

	export, ok := plugintype.GovernanceRoleExport(plugintype.RoleIRVerdict)
	if !ok {
		t.Fatal("the ir-verdict role has no default export")
	}
	got, err := DecodeForExport(export, []byte(`{"results":[]}`))
	if err == nil {
		t.Fatalf("an answer that is not the export's shape was printed as %q instead of refused", got)
	}
	if !errors.Is(err, wasm.ErrWasmOutputShape()) {
		t.Errorf("the refusal is not the output-shape condition: %v", err)
	}
	if !strings.Contains(err.Error(), strconv.Quote(export)) {
		t.Errorf("the refusal does not name the export %q: %v", export, err)
	}
}

// TestDecodeForExportPassesAnUnlistedExportThrough is the mirror of the encode
// side's pass-through: an export the table does not list gets its answer back as
// the guest wrote it.
func TestDecodeForExportPassesAnUnlistedExportThrough(t *testing.T) {
	t.Parallel()

	const answer = "an answer in whatever form this entry function writes\n"
	got, err := DecodeForExport("an_entry_function_of_the_plugins_own", []byte(answer))
	if err != nil {
		t.Fatalf("an unlisted export's answer should pass through; got %v", err)
	}
	if string(got) != answer {
		t.Fatalf("answer = %q; want it unchanged", got)
	}
}

// TestDecodeForExportRefusesAnAmbiguousBinaryExport drives the arm the generated
// table cannot reach, as the encode side's refusal test does, and requires that
// no output constructor runs before the refusal.
func TestDecodeForExportRefusesAnAmbiguousBinaryExport(t *testing.T) {
	t.Parallel()

	newOutput := func() wasm.GuestOutputCBOR {
		t.Error("an ambiguous export must refuse before any answer is decoded")
		return new(abi.PolicyOutput)
	}
	table := []entry{
		{export: "verdict_ir", shape: "PolicyInput", output: "PolicyOutput", newOutput: newOutput},
		{export: "verdict_ir", shape: "RenderPolicyInput", output: "PolicyOutput", newOutput: newOutput},
	}
	if _, err := decodeForExport(table, "verdict_ir", nil); err == nil {
		t.Fatal("an export naming two payload shapes should refuse rather than guess")
	}
}

// TestEveryRegisteredOutputDecodes exercises every row's output side, for the
// reason TestEveryRegisteredEncoderRuns exercises every encoder. The answer is
// the row's own shape left empty, written in the canonical binary encoding — an
// answer a guest can return, since the decode reads which keys an answer holds
// and not whether a required one is filled.
//
// Most shapes print such an answer as a JSON object. A shape that carries a
// required JSON document cannot: the empty document is not JSON, so the answer is
// refused, naming the export, the shape and the field. Which rows are which is
// read from the standard library's own verdict on the empty value rather than
// listed here, so a shape added later is asserted on its own terms; the test then
// requires at least one row of each kind, because a run in which every row took
// one branch would not show the other branch firing.
func TestEveryRegisteredOutputDecodes(t *testing.T) {
	t.Parallel()

	if len(entries) == 0 {
		t.Fatal("the generated registry is empty, so this test asserts nothing")
	}
	var printed, refused int
	for _, e := range entries {
		if e.newOutput == nil {
			t.Fatalf("row %s carries no output constructor", e.export)
		}
		empty := e.newOutput()
		encoder, ok := empty.(interface{ EncodeCBOR() ([]byte, error) })
		if !ok {
			t.Fatalf("row %s's output shape %s carries no encoder to write an answer with", e.export, e.output)
		}
		answer, err := encoder.EncodeCBOR()
		if err != nil {
			t.Fatalf("encode an empty %s: %v", e.output, err)
		}
		_, marshalErr := json.Marshal(empty)
		got, err := DecodeForExport(e.export, answer)
		if marshalErr == nil {
			printed++
			if err != nil {
				t.Errorf("%s: decoding an empty %s answer should print; got %v", e.export, e.output, err)
				continue
			}
			if !json.Valid(got) || !bytes.HasPrefix(got, []byte("{")) {
				t.Errorf("%s: printed answer is not a JSON object: %q", e.export, got)
			}
			continue
		}
		refused++
		if err == nil {
			t.Errorf("%s: an empty %s cannot be written as JSON, yet it printed as %q", e.export, e.output, got)
			continue
		}
		for _, want := range []string{e.export, e.output, "cannot be printed as JSON"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the refusal does not name %q: %v", e.export, want, err)
			}
		}
	}
	if printed == 0 || refused == 0 {
		t.Fatalf("%d row(s) printed and %d were refused; each branch needs a row for this test to show it firing",
			printed, refused)
	}
}

// TestDecodeForExportRefusesAParseAnswerWithoutATree drives the case a parser
// guest can actually produce: an answer whose tree, the one field a parse answer
// requires, is missing or empty. The strict decode accepts both, because it reads
// which keys are present and not whether a required one is filled — so the
// refusal has to come from the printing, and it has to say what is wrong in the
// author's terms: the export, the shape, the field, and that it is empty. The
// control beside it is the same shape with a tree, which prints.
func TestDecodeForExportRefusesAParseAnswerWithoutATree(t *testing.T) {
	t.Parallel()

	parser, ok := plugintype.Lookup(plugintype.Parser)
	if !ok || len(parser.HostExports) != 1 {
		t.Fatal("the parser type lists no single host export")
	}
	export := parser.HostExports[0]

	emptyTree, err := abi.ParseOutput{}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode a parse answer with an empty tree: %v", err)
	}
	cases := map[string][]byte{
		"tree key absent": {0xa0},
		"tree empty":      emptyTree,
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := DecodeForExport(export, answer)
			if err == nil {
				t.Fatalf("a parse answer with no tree printed as %q instead of being refused", got)
			}
			if errors.Is(err, wasm.ErrWasmOutputShape()) {
				t.Errorf("the refusal reads as a wrong-shape answer, which this is not: %v", err)
			}
			for _, want := range []string{"the " + export + " export", "ParseOutput", `"tree"`, "is empty", "cannot be printed as JSON"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %s: %v", want, err)
				}
			}
		})
	}

	withTree, err := abi.ParseOutput{Tree: json.RawMessage(`{"a":1}`)}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode a parse answer with a tree: %v", err)
	}
	got, err := DecodeForExport(export, withTree)
	if err != nil {
		t.Fatalf("a parse answer carrying its tree should print; got %v", err)
	}
	if !bytes.Contains(got, []byte(`"a": 1`)) {
		t.Fatalf("the printed answer does not carry the tree: %q", got)
	}
}

// TestEveryRegisteredEncoderRuns exercises every row's encoder, not only the
// rows a dedicated test above converts a real payload for.
//
// An encoder no test drives is a mechanism whose first real use is also its first
// test — at the moment an author runs a guest directly, which is the worst moment
// to discover it. So each one is driven here.
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
