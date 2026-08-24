// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/codecconformance"
	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestStockYAMLCodecConformance holds the core's stock YAML codec to the shared
// codec contract — determinism, round-trip, patch-isolation, and op-apply — over
// three corpus groups:
//
//   - the adversarial segmenter corpus (segmenterCorpus): block scalars
//     (literal | and folded >), CRLF line endings, BOM prefix, %TAG/%YAML
//     directives, empty and null documents, leading/inter-document/trailing
//     comments, no-trailing-newline, bare separators, inline flow documents.
//     Inputs the YAML parser rejects are fed as halt cases (Segment must error).
//   - the RFC-6902 op-apply cases (codecconformance.RFC6902Cases): replace of a
//     scalar value, add of a mapping member, remove of a mapping member, and an
//     absent-path halt.
//   - the JSON / flow-style corpus (jsonFlowCorpus): compact and pretty-printed
//     JSON bodies, at a nested depth and at the top level, where a scalar's
//     terminator is a flow delimiter rather than the end of the line. A replace of
//     a comma-adjacent scalar and a
//     replace of a last (brace-adjacent) member both edit only the addressed
//     value's span; an add into a non-empty flow mapping splices a new member
//     after the last existing value; a replace of a scalar with a structured
//     value splices inline compact JSON over the scalar's span; a remove against
//     a flow object halts; a patch against an opaque (non-JSON) body halts.
//   - the multi-line-construct corpus (multiLineConstructCorpus): every YAML
//     construct whose source spans more than the line the parser reports — block
//     literal (|), folded (>), the chomping variants (|-, |+, >-, >+), multi-line
//     double- and single-quoted scalars, and multi-line flow collections whose
//     closing }/] sits on its own line — crossed with replace (scalar→scalar and
//     scalar→object), add (last-member and empty-parent), and remove (last-member
//     and mid). Each mutation case pins the whole output byte-for-byte, so a span
//     that stops at the first newline (leaving a block body behind) or a
//     last-member end computed from the header line (splicing a new member inside
//     the block, or orphaning it on remove) fails here. Sibling members and their
//     comments are pinned verbatim, so a patch that reaches beyond its addressed
//     span trips the exact assertion.
//   - the absent-token guardrail (CheckAbsentTokenHalts): a Reassemble naming a
//     token the segmentation never produced must be rejected.
func TestStockYAMLCodecConformance(t *testing.T) {
	t.Parallel()
	codec := newStockYAMLCodec(2)

	corpus := stockYAMLCorpus()
	corpus = append(corpus, jsonFlowCorpus()...)
	corpus = append(corpus, multiLineConstructCorpus()...)
	corpus = append(corpus, codecconformance.RFC6902Cases()...)
	codecconformance.Run(t, codec, corpus)

	if err := codecconformance.CheckAbsentTokenHalts(codec); err != nil {
		t.Error(err)
	}
}

// stockYAMLCorpus maps the adversarial segmenter corpus onto conformance cases.
// A parse-rejecting input becomes a halt case (Segment must error); every other
// case asserts round-trip identity. The empty-body case (zero segments) is
// dropped — there is no unit to address and round-trip identity over empty
// bytes is trivial.
func stockYAMLCorpus() []codecconformance.Case {
	const name = "manifest.yaml"
	var out []codecconformance.Case
	for _, tc := range segmenterCorpus() {
		if tc.body == "" {
			continue
		}
		out = append(out, codecconformance.Case{
			Name:     tc.name,
			Filename: name,
			Input:    []byte(tc.body),
			WantHalt: tc.parseErr,
		})
	}
	// A two-document case carrying ops on the first document — exercises
	// patch-isolation (the sibling document survives verbatim) through the
	// shared harness against the stock codec.
	out = append(out, codecconformance.Case{
		Name:      "patch-isolation-two-docs",
		Filename:  name,
		Input:     []byte("a: 1\n---\nb: 2\n"),
		TargetSel: firstStructured,
		Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: float64(9)}},
		WantBytes: []byte("a: 9\n---\nb: 2\n"),
	})
	return out
}

// firstStructured selects the first structured unit — the explicit form of the
// harness default, used where a case names a TargetSel for clarity.
func firstStructured(units []frp.Unit) (int, bool) {
	for i, u := range units {
		if u.Structured() {
			return i, true
		}
	}
	return 0, false
}

// jsonFlowCorpus returns cases over JSON-shaped bodies, which parse as
// flow-style collections where a scalar's terminator is a flow delimiter
// (`,`, `}`, `]`) rather than the end of the line. It covers a JSON-authored
// Terraform module body in both encodings it reaches the codec in — compact
// single-line and 2-space pretty — a flat document whose addressed members sit
// at the top level, and a native-HCL body that is opaque to the codec.
//
// The two document depths are both needed and neither substitutes for the other:
// the nested body's addressed scalars sit four levels down, where a span is
// computed from a deeply-indented child, while the flat document's sit directly
// inside the document's own mapping, at the outermost indent. The filenames
// here are labels: this corpus drives one codec instance directly, and that codec
// decides a body is opaque from its content rather than from its suffix — which is
// why the native-HCL case halts.
//
// Each mutation case asserts byte-fidelity exactly (WantBytes is the input with
// only the addressed span changed), so an over-reaching span that swallowed a
// trailing comma or ran to the end of the line would fail here. Both flow
// terminators are exercised: a comma-adjacent value (a member with a sibling
// after it) and a brace-adjacent value (the last member). The add cases splice
// a new JSON-quoted member into a non-empty flow mapping — on its own indented
// line for a pretty body, inline after `, ` for a compact body — leaving the
// existing members verbatim. The scalar-to-object cases replace a scalar with
// inline compact JSON. The remove case asserts the flow-member-removal halt,
// and the native-HCL case asserts an opaque body is not a patch target.
func jsonFlowCorpus() []codecconformance.Case {
	const tfName = "main.tf.json"
	const flatName = "variables.json"

	compact := []byte(`{"resource":{"aws_db_instance":{"main":{"instance_class":"db.t3.micro","allocated_storage":20}}}}` + "\n")
	pretty := jsonIndent2(compact)
	flat := flatJSONDocument()

	sub := func(src []byte, old, replacement string) []byte {
		return bytes.Replace(src, []byte(old), []byte(replacement), 1)
	}

	return []codecconformance.Case{
		{
			Name:      "tfjson-compact-replace-sibling",
			Filename:  tfName,
			Input:     compact,
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/resource/aws_db_instance/main/instance_class", Value: "db.t3.small"}},
			WantBytes: sub(compact, `"db.t3.micro"`, `"db.t3.small"`),
		},
		{
			Name:      "tfjson-compact-replace-last-member",
			Filename:  tfName,
			Input:     compact,
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/resource/aws_db_instance/main/allocated_storage", Value: float64(25)}},
			WantBytes: sub(compact, "20", "25"),
		},
		{
			Name:      "tfjson-pretty-replace-sibling",
			Filename:  tfName,
			Input:     pretty,
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/resource/aws_db_instance/main/instance_class", Value: "db.t3.small"}},
			WantBytes: sub(pretty, `"db.t3.micro"`, `"db.t3.small"`),
		},
		{
			Name:      "tfjson-pretty-replace-last-member-number",
			Filename:  tfName,
			Input:     pretty,
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/resource/aws_db_instance/main/allocated_storage", Value: float64(25)}},
			WantBytes: sub(pretty, "20", "25"),
		},
		{
			Name:      "flat-replace-comma-adjacent",
			Filename:  flatName,
			Input:     flat,
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/db_name", Value: "appdb2"}},
			WantBytes: sub(flat, `"appdb"`, `"appdb2"`),
		},
		{
			Name:      "tfjson-pretty-flow-add-into-non-empty",
			Filename:  tfName,
			Input:     pretty,
			Ops:       []frp.JSONPatchOp{{Op: "add", Path: "/resource/aws_db_instance/main/multi_az", Value: true}},
			WantBytes: sub(pretty, `"allocated_storage": 20`, "\"allocated_storage\": 20,\n        \"multi_az\": true"),
		},
		{
			Name:      "tfjson-compact-flow-add-into-non-empty",
			Filename:  tfName,
			Input:     compact,
			Ops:       []frp.JSONPatchOp{{Op: "add", Path: "/resource/aws_db_instance/main/multi_az", Value: true}},
			WantBytes: sub(compact, `"allocated_storage":20`, `"allocated_storage":20, "multi_az": true`),
		},
		{
			Name:      "tfjson-pretty-scalar-to-object-replace",
			Filename:  tfName,
			Input:     pretty,
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/resource/aws_db_instance/main/instance_class", Value: map[string]any{"Ref": "InstanceClass"}}},
			WantBytes: sub(pretty, `"db.t3.micro"`, `{"Ref":"InstanceClass"}`),
		},
		{
			Name:      "tfjson-compact-scalar-to-object-replace",
			Filename:  tfName,
			Input:     compact,
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/resource/aws_db_instance/main/instance_class", Value: map[string]any{"Ref": "InstanceClass"}}},
			WantBytes: sub(compact, `"db.t3.micro"`, `{"Ref":"InstanceClass"}`),
		},
		{
			// A structured replace whose comma-adjacent sibling (the
			// interpolation pointer db_endpoint) must survive verbatim — the
			// patch-isolation proof for an inline structured splice.
			Name:      "flat-scalar-to-object-replace-isolates-sibling",
			Filename:  flatName,
			Input:     flat,
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/db_name", Value: map[string]any{"Fn::ImportValue": "db-name-export"}}},
			WantBytes: sub(flat, `"appdb"`, `{"Fn::ImportValue":"db-name-export"}`),
		},
		{
			Name:      "flat-flow-add-into-non-empty",
			Filename:  flatName,
			Input:     flat,
			Ops:       []frp.JSONPatchOp{{Op: "add", Path: "/multi_az", Value: true}},
			WantBytes: sub(flat, `"allocated_storage": 20`, "\"allocated_storage\": 20,\n  \"multi_az\": true"),
		},
		{
			Name:     "tfjson-flow-remove-halts",
			Filename: tfName,
			Input:    pretty,
			Ops:      []frp.JSONPatchOp{{Op: "remove", Path: "/resource/aws_db_instance/main/allocated_storage"}},
			WantHalt: true,
		},
		{
			Name:     "native-hcl-opaque-patch-halts",
			Filename: "main.tf",
			Input:    []byte("resource \"aws_db_instance\" \"main\" {\n  instance_class = \"db.t3.micro\"\n}\n"),
			Ops:      []frp.JSONPatchOp{{Op: "replace", Path: "/instance_class", Value: "db.t3.small"}},
			WantHalt: true,
		},
	}
}

// multiLineConstructCorpus returns cases over YAML constructs whose source spans
// more lines than the parser reports a position for. These are the constructs
// where a naive span (stop at the first newline; a last-member end read from the
// header line) silently corrupts a surgical patch: a replace leaves the block
// body behind, an add splices a new member inside the block, a remove orphans the
// body. Each case pins the whole output byte-for-byte so any such over- or
// under-reach fails.
//
// The construct axis is block literal (|), folded (>), the four chomping variants
// (|-, |+, >-, >+), multi-line double- and single-quoted scalars, a multi-line
// flow mapping and a multi-line flow sequence (closing delimiter on its own
// line), and the nested shapes where a block scalar or a multi-line flow
// collection is a mapping's last member. The operation axis is replace
// (scalar→scalar and scalar→object), add (last-member and empty-parent), and
// remove (last-member and mid). Isolation is asserted structurally: cases carry
// comment-bearing siblings whose exact bytes the WantBytes pins, so a patch that
// disturbs a neighbor fails byte comparison.
//
// The replace-on-block cases exercise the coupled span + style-drop fix: the span
// must cover the whole block body (else the old body re-absorbs into the
// document) and the replacement scalar must not re-emit as a block (a single
// inline scalar spliced over the span, which re-parses to the intended value).
func multiLineConstructCorpus() []codecconformance.Case {
	const name = "manifest.yaml"
	b := func(s string) []byte { return []byte(s) }

	return []codecconformance.Case{
		// Block literal | — replace scalar→scalar. The body carries a line that
		// looks like a document separator; a first-newline span would leave it
		// behind to re-absorb as document text.
		{
			Name:      "blockliteral-replace-scalar",
			Filename:  name,
			Input:     b("a: |\n  ---\n  not a separator\nb: 2\n"),
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: "newval"}},
			WantBytes: b("a: newval\nb: 2\n"),
		},
		// Block literal | — replace scalar→object. The span covers the whole
		// block; the structured value splices inline as compact JSON.
		{
			Name:      "blockliteral-replace-to-object",
			Filename:  name,
			Input:     b("a: |\n  line1\n  line2\nb: 2\n"),
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: map[string]any{"Ref": "InstanceClass"}}},
			WantBytes: b("a: {\"Ref\":\"InstanceClass\"}\nb: 2\n"),
		},
		// Folded > — replace scalar→scalar.
		{
			Name:      "folded-replace-scalar",
			Filename:  name,
			Input:     b("a: >\n  folded\n  text\nb: 2\n"),
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: "newval"}},
			WantBytes: b("a: newval\nb: 2\n"),
		},
		// Folded > — replace scalar→object.
		{
			Name:      "folded-replace-to-object",
			Filename:  name,
			Input:     b("a: >\n  folded\n  text\nb: 2\n"),
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: map[string]any{"Fn::ImportValue": "y"}}},
			WantBytes: b("a: {\"Fn::ImportValue\":\"y\"}\nb: 2\n"),
		},
		// Chomping strip | - — replace scalar→scalar.
		{
			Name:      "blockliteral-strip-replace",
			Filename:  name,
			Input:     b("a: |-\n  one\n  two\nb: 2\n"),
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: "newval"}},
			WantBytes: b("a: newval\nb: 2\n"),
		},
		// Chomping keep | + — replace scalar→scalar.
		{
			Name:      "blockliteral-keep-replace",
			Filename:  name,
			Input:     b("a: |+\n  one\n  two\nb: 2\n"),
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: "newval"}},
			WantBytes: b("a: newval\nb: 2\n"),
		},
		// Chomping strip > - — replace scalar→scalar.
		{
			Name:      "folded-strip-replace",
			Filename:  name,
			Input:     b("a: >-\n  one\n  two\nb: 2\n"),
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: "newval"}},
			WantBytes: b("a: newval\nb: 2\n"),
		},
		// Chomping keep > + — replace scalar→scalar.
		{
			Name:      "folded-keep-replace",
			Filename:  name,
			Input:     b("a: >+\n  one\n  two\nb: 2\n"),
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: "newval"}},
			WantBytes: b("a: newval\nb: 2\n"),
		},
		// Multi-line double-quoted — replace scalar→scalar; the quote style
		// survives.
		{
			Name:      "mlq-double-replace",
			Filename:  name,
			Input:     b("a: \"one\n  two\"\nb: 2\n"),
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: "newval"}},
			WantBytes: b("a: \"newval\"\nb: 2\n"),
		},
		// Multi-line single-quoted — replace scalar→scalar; the quote style
		// survives.
		{
			Name:      "mlq-single-replace",
			Filename:  name,
			Input:     b("a: 'one\n  two'\nb: 2\n"),
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/a", Value: "newval"}},
			WantBytes: b("a: 'newval'\nb: 2\n"),
		},
		// Multi-line flow mapping (closing } on its own line) — replace an inner
		// scalar; the flow structure and the sibling b survive verbatim.
		{
			Name:      "flowmap-multiline-replace-inner",
			Filename:  name,
			Input:     b("a: {\n  x: 1\n}\nb: 2\n"),
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/a/x", Value: float64(5)}},
			WantBytes: b("a: {\n  x: 5\n}\nb: 2\n"),
		},
		// Multi-line flow sequence (closing ] on its own line) — replace a
		// brace-adjacent element; the flow structure and sibling b survive.
		{
			Name:      "flowseq-multiline-replace-elem",
			Filename:  name,
			Input:     b("a: [\n  1,\n  2\n]\nb: 3\n"),
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/a/1", Value: float64(9)}},
			WantBytes: b("a: [\n  1,\n  9\n]\nb: 3\n"),
		},
		// Nested: a block scalar as a mapping's last member — add. The new member
		// lands after the whole block, not inside its continuation.
		{
			Name:      "nested-blockscalar-last-add",
			Filename:  name,
			Input:     b("root:\n  name: web\n  config: |\n    line1\n    line2\n"),
			Ops:       []frp.JSONPatchOp{{Op: "add", Path: "/root/added", Value: "x"}},
			WantBytes: b("root:\n  name: web\n  config: |\n    line1\n    line2\n  added: x\n"),
		},
		// Nested: a block scalar as a mapping's last member — remove. The whole
		// block (header and body) is deleted; the sibling survives.
		{
			Name:      "nested-blockscalar-last-remove",
			Filename:  name,
			Input:     b("root:\n  name: web\n  config: |\n    line1\n    line2\n"),
			Ops:       []frp.JSONPatchOp{{Op: "remove", Path: "/root/config"}},
			WantBytes: b("root:\n  name: web\n"),
		},
		// Nested: a multi-line flow collection as a mapping's last member — add.
		// The new member lands after the closing }, not one line short.
		{
			Name:      "nested-flowmap-last-add",
			Filename:  name,
			Input:     b("root:\n  name: web\n  cfg: {\n    x: 1\n  }\n"),
			Ops:       []frp.JSONPatchOp{{Op: "add", Path: "/root/added", Value: "tail"}},
			WantBytes: b("root:\n  name: web\n  cfg: {\n    x: 1\n  }\n  added: tail\n"),
		},
		// Remove a mid member whose value is a block scalar — the member's whole
		// span (header + body) is deleted and both siblings survive. This is a
		// correctness case that holds regardless of the last-member span math
		// (the end is the next sibling's line), included for matrix completeness.
		{
			Name:      "blockscalar-mid-remove",
			Filename:  name,
			Input:     b("root:\n  keep1: a\n  block: |\n    x\n    y\n  keep2: b\n"),
			Ops:       []frp.JSONPatchOp{{Op: "remove", Path: "/root/block"}},
			WantBytes: b("root:\n  keep1: a\n  keep2: b\n"),
		},
		// Add a first member into an empty flow mapping {} — the braces are
		// rewritten in place to carry the JSON-quoted member.
		{
			Name:      "empty-flowmap-add",
			Filename:  name,
			Input:     b("root:\n  cfg: {}\n"),
			Ops:       []frp.JSONPatchOp{{Op: "add", Path: "/root/cfg/new", Value: true}},
			WantBytes: b("root:\n  cfg: {\"new\": true}\n"),
		},
		// Isolation: replace a block-scalar member sitting between two
		// comment-bearing siblings. The siblings' bytes — value and inline
		// comment — are pinned verbatim; only the block collapses to the scalar.
		{
			Name:      "isolation-blockliteral-replace",
			Filename:  name,
			Input:     b("first: 1  # keep\nblock: |\n  body1\n  body2\nlast: 2  # keep\n"),
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/block", Value: "new"}},
			WantBytes: b("first: 1  # keep\nblock: new\nlast: 2  # keep\n"),
		},
		// Isolation: add after a block-scalar last member with comment-bearing
		// siblings above. Every pre-existing byte, including both comments and the
		// block body, survives; only the new member is appended.
		{
			Name:      "isolation-add-after-blockscalar",
			Filename:  name,
			Input:     b("before: x  # c1\nm:\n  k: v  # c2\n  blk: |\n    l1\n    l2\n"),
			Ops:       []frp.JSONPatchOp{{Op: "add", Path: "/m/added", Value: "z"}},
			WantBytes: b("before: x  # c1\nm:\n  k: v  # c2\n  blk: |\n    l1\n    l2\n  added: z\n"),
		},
	}
}

// jsonIndent2 pretty-prints compact JSON at 2-space indent with a trailing
// newline — the shape a formatted JSON body lands in on disk.
func jsonIndent2(compact []byte) []byte {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		panic(err)
	}
	pretty.WriteByte('\n')
	return pretty.Bytes()
}

// flatJSONDocument builds the flat end of the corpus's depth axis: an ordered
// map marshalled to compact JSON in author order, then pretty-printed at 2-space
// indent, so its addressed members sit directly inside the document's own
// mapping rather than four levels down. Its last member is where an add splices,
// which makes the document's own closing brace the terminator that case reaches.
// One value is an interpolation pointer string, which the codec must carry
// through a patch of its sibling untouched.
func flatJSONDocument() []byte {
	om := types.NewOrderedMap[any]()
	om.Set("db_name", "appdb")
	om.Set("db_endpoint", "${aws_db_instance.main.endpoint}")
	om.Set("allocated_storage", float64(20))
	compact, err := om.MarshalJSON()
	if err != nil {
		panic(err)
	}
	return jsonIndent2(compact)
}
