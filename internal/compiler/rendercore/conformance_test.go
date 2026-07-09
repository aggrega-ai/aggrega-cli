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
//     JSON bodies (the shape a JSON-authored Terraform module and its auto-loaded
//     variables overlay land in), where a scalar's terminator is a flow delimiter
//     rather than the end of the line. A replace of a comma-adjacent scalar and a
//     replace of a last (brace-adjacent) member both edit only the addressed
//     value's span; add and remove against a flow object halt; a patch against an
//     opaque (non-JSON) body halts.
//   - the absent-token guardrail (CheckAbsentTokenHalts): a Reassemble naming a
//     token the segmentation never produced must be rejected.
func TestStockYAMLCodecConformance(t *testing.T) {
	t.Parallel()
	codec := newStockYAMLCodec(2)

	corpus := stockYAMLCorpus()
	corpus = append(corpus, jsonFlowCorpus()...)
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
// (`,`, `}`, `]`) rather than the end of the line. It covers the two shapes a
// JSON-authored Terraform module and its auto-loaded variables overlay land in
// — compact single-line and 2-space pretty — plus a native-HCL body that is
// opaque to the codec.
//
// Each replace case asserts byte-fidelity exactly (WantBytes is the input with
// only the addressed value literal substituted), so an over-reaching span that
// swallowed a trailing comma or ran to the end of the line would fail here.
// Both flow terminators are exercised: a comma-adjacent value (a member with a
// sibling after it) and a brace-adjacent value (the last member). The add and
// remove cases assert the flow-collection member-mutation halt, and the
// native-HCL case asserts an opaque body is not a patch target.
func jsonFlowCorpus() []codecconformance.Case {
	const tfName = "main.tf.json"
	const varsName = "aggrega.auto.tfvars.json"

	compact := []byte(`{"resource":{"aws_db_instance":{"main":{"instance_class":"db.t3.micro","allocated_storage":20}}}}` + "\n")
	pretty := jsonIndent2(compact)
	tfvars := tfvarsOverlay()

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
			Name:      "tfvars-replace-comma-adjacent",
			Filename:  varsName,
			Input:     tfvars,
			Ops:       []frp.JSONPatchOp{{Op: "replace", Path: "/db_name", Value: "appdb2"}},
			WantBytes: sub(tfvars, `"appdb"`, `"appdb2"`),
		},
		{
			Name:     "tfjson-flow-add-halts",
			Filename: tfName,
			Input:    pretty,
			Ops:      []frp.JSONPatchOp{{Op: "add", Path: "/resource/aws_db_instance/main/multi_az", Value: true}},
			WantHalt: true,
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

// tfvarsOverlay builds a variables overlay the way the Terraform provider does:
// an ordered map marshalled to compact JSON in author order, then pretty-printed
// at 2-space indent. One value is a deterministic interpolation pointer string.
func tfvarsOverlay() []byte {
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
