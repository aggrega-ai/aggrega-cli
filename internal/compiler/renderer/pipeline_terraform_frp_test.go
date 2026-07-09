// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// prettyJSON renders a compact JSON body the way a JSON-authored Terraform
// module body reaches the pipeline: 2-space indentation plus a trailing
// newline. It mirrors the encoding the stock codec must reassemble
// byte-faithfully.
func prettyJSON(t *testing.T, compact string) []byte {
	t.Helper()
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(compact), "", "  "); err != nil {
		t.Fatalf("indent module body: %v", err)
	}
	pretty.WriteByte('\n')
	return pretty.Bytes()
}

// tfvarsFixture builds an aggrega.auto.tfvars.json overlay the same way the
// terraform provider does: an author-ordered value map marshalled to JSON, then
// 2-space indented with a trailing newline. Author order is preserved so the
// reassembled bytes are deterministic.
func tfvarsFixture(t *testing.T) []byte {
	t.Helper()
	values := types.NewOrderedMap[any]()
	values.Set("db_name", "appdb")
	values.Set("db_endpoint", "${aws_db_instance.main.endpoint}")
	values.Set("allocated_storage", float64(20))

	compact, err := values.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal overlay values: %v", err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		t.Fatalf("indent overlay: %v", err)
	}
	pretty.WriteByte('\n')
	return pretty.Bytes()
}

// fileByName returns the reassembled body of the named file from a result set,
// failing the test if it is absent.
func fileByName(t *testing.T, files []EmittedFile, name string) []byte {
	t.Helper()
	for _, f := range files {
		if f.Name == name {
			return f.Body
		}
	}
	t.Fatalf("file %q absent from result set", name)
	return nil
}

// TestPipelineTerraformFrp_ByteFaithfulTwoPatches proves that in one executor
// run a FeatureRecipe pipeline patches both a JSON-authored Terraform module
// body (main.tf.json) and the module-input overlay (aggrega.auto.tfvars.json),
// each reassembling byte-faithfully: only the addressed scalar literal changes,
// with the surrounding bytes — braces, commas, and an unrelated interpolation
// pointer — untouched. A native-HCL sibling that no patch addresses passes
// through verbatim, proving patch isolation across a mixed structured/opaque
// set. terraform borrows the stock YAML codec (both scopes here), which the
// executor routes carve and reassemble through.
func TestPipelineTerraformFrp_ByteFaithfulTwoPatches(t *testing.T) {
	t.Parallel()

	moduleBody := prettyJSON(t, `{"resource":{"aws_db_instance":{"main":{"instance_class":"db.t3.micro","allocated_storage":20}}}}`)
	overlay := tfvarsFixture(t)
	// A native-HCL body has no RFC-6902-addressable object tree; the codec
	// carves it opaque, so it must survive verbatim when no patch addresses it.
	hclBody := []byte("resource \"aws_db_instance\" \"main\" {\n  instance_class = \"db.t3.micro\"\n}\n")

	own := []EmittedFile{
		{Name: "main.tf.json", Origin: EmitOriginWorkload, Body: moduleBody},
		{Name: "aggrega.auto.tfvars.json", Origin: EmitOriginWorkload, Body: overlay},
		{Name: "main.tf", Origin: EmitOriginWorkload, Body: hclBody},
	}

	// One step returns one output carrying two patches — one per JSON document.
	out := frp.FRPStepOutput{Patches: []frp.JSONPatchOp{
		{
			Scope: frp.DocumentScopeOwn,
			Key:   "main.tf.json",
			Op:    "replace",
			Path:  "/resource/aws_db_instance/main/instance_class",
			Value: "db.t3.small",
		},
		{
			Scope: frp.DocumentScopeOwn,
			Key:   "aggrega.auto.tfvars.json",
			Op:    "replace",
			Path:  "/db_name",
			Value: "appdb2",
		},
	}}

	res, err := runNodePipelineWithInvoker(context.Background(), oneStep(),
		own, nil, false, "terraform", "",
		renderctx.RenderContext{}, &types.TemplateContext{},
		newStockYAMLCodec(2), newStockYAMLCodec(2),
		cannedInvoker(t, out), describerFor("own", []string{"terraform"}), "node")
	if err != nil {
		t.Fatalf("expected a clean run; got halt: %v", err)
	}
	if len(res.Own) != len(own) {
		t.Fatalf("result set changed cardinality: got %d files, want %d", len(res.Own), len(own))
	}

	// The module body: only the addressed scalar literal changes. A comma or
	// brace over-reach in reassembly would fail this byte-for-byte equality.
	gotModule := fileByName(t, res.Own, "main.tf.json")
	wantModule := bytes.Replace(moduleBody, []byte(`"db.t3.micro"`), []byte(`"db.t3.small"`), 1)
	if !bytes.Equal(gotModule, wantModule) {
		t.Errorf("main.tf.json not byte-faithful.\n got: %q\nwant: %q", gotModule, wantModule)
	}

	// The overlay: only /db_name changes; the interpolation pointer survives
	// verbatim because a pointer string is opaque scalar data the compiler pipes
	// through, never resolves.
	gotOverlay := fileByName(t, res.Own, "aggrega.auto.tfvars.json")
	wantOverlay := bytes.Replace(overlay, []byte(`"appdb"`), []byte(`"appdb2"`), 1)
	if !bytes.Equal(gotOverlay, wantOverlay) {
		t.Errorf("aggrega.auto.tfvars.json not byte-faithful.\n got: %q\nwant: %q", gotOverlay, wantOverlay)
	}
	if !bytes.Contains(gotOverlay, []byte("${aws_db_instance.main.endpoint}")) {
		t.Errorf("interpolation pointer did not survive verbatim in the patched overlay; got: %q", gotOverlay)
	}

	// The native-HCL sibling no patch addressed survives byte-identical.
	gotHCL := fileByName(t, res.Own, "main.tf")
	if !bytes.Equal(gotHCL, hclBody) {
		t.Errorf("unaddressed native-HCL body was mutated.\n got: %q\nwant: %q", gotHCL, hclBody)
	}
}

// TestPipelineTerraformFrp_NativeHCLOpaqueHalts proves a PATCH against a
// native-HCL body — which the codec carves opaque (no RFC-6902-addressable
// tree) — halts the build with the ErrPipelinePatchTargetNotStructured Loud
// Guardrail, with the walk's typed ErrPipelineTargetOpaque preserved in the
// errors.Is chain. A JSON-authored .tf.json body is structured and patchable;
// a native-HCL body is opaque to the JSON-oriented codec.
func TestPipelineTerraformFrp_NativeHCLOpaqueHalts(t *testing.T) {
	t.Parallel()

	hclBody := []byte("resource \"aws_db_instance\" \"main\" {\n  instance_class = \"db.t3.micro\"\n}\n")
	own := []EmittedFile{{Name: "main.tf", Origin: EmitOriginWorkload, Body: hclBody}}

	out := frp.FRPStepOutput{Patches: []frp.JSONPatchOp{{
		Scope: frp.DocumentScopeOwn,
		Key:   "main.tf",
		Op:    "replace",
		Path:  "/instance_class",
		Value: "db.t3.small",
	}}}

	_, err := runNodePipelineWithInvoker(context.Background(), oneStep(),
		own, nil, false, "terraform", "",
		renderctx.RenderContext{}, &types.TemplateContext{},
		newStockYAMLCodec(2), newStockYAMLCodec(2),
		cannedInvoker(t, out), describerFor("own", []string{"terraform"}), "node")
	if err == nil {
		t.Fatalf("expected halt for PATCH against a native-HCL opaque body; got nil")
	}
	if !errors.Is(err, ErrPipelinePatchTargetNotStructured) {
		t.Errorf("expected ErrPipelinePatchTargetNotStructured; got %v", err)
	}
	if !errors.Is(err, frp.ErrPipelineTargetOpaque) {
		t.Errorf("expected the walk's ErrPipelineTargetOpaque to remain in the errors.Is chain; got %v", err)
	}
}
