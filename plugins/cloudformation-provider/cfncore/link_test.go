// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// testLinkEdge builds a cross-boundary link edge from consumer "api" to producer
// "orders-db" on the "database" socket, injecting the target's "host" output. The
// annotations carry the consumed endpoint's placement.
func testLinkEdge(ann map[string]string) rendercore.LinkEdge {
	var env types.OrderedMap[string]
	env.Set("DB_HOST", "host")
	var inject types.OrderedMap[types.OrderedMap[string]]
	inject.Set("env", env)
	return rendercore.LinkEdge{
		Source:            rendercore.LinkEndpoint{Node: "api", Boundary: "prod/app", Provider: "cloudformation"},
		Target:            rendercore.LinkEndpoint{Node: "orders-db", Boundary: "prod/data", Provider: "cloudformation"},
		TargetAnnotations: ann,
		Socket:            "database",
		Inject:            inject,
	}
}

func TestLinkPublishAddsExport(t *testing.T) {
	out, err := LinkPublish(testLinkEdge(nil))
	if err != nil {
		t.Fatalf("LinkPublish: %v", err)
	}
	wantExport := ExportName("", "prod/data", "orders-db", "host")

	if got := out.Handle[handleStackNameKey]; got != StackName("", "prod/data") {
		t.Errorf("handle stackName = %q, want %q", got, StackName("", "prod/data"))
	}
	if got := out.Handle[handleExportNamePrefix+"host"]; got != wantExport {
		t.Errorf("handle export name = %q, want %q", got, wantExport)
	}

	if len(out.Patches) != 1 || len(out.Patches[0].Ops) != 1 {
		t.Fatalf("want one patch with one op, got %+v", out.Patches)
	}
	op := out.Patches[0].Ops[0]
	if out.Patches[0].File != StackTemplateName {
		t.Errorf("patch file = %q, want %q", out.Patches[0].File, StackTemplateName)
	}
	if op.Op != "add" || op.Path != "/Outputs/host/Export" {
		t.Errorf("op = %q at %q, want add at /Outputs/host/Export", op.Op, op.Path)
	}
	val, ok := op.Value.(map[string]any)
	if !ok || val["Name"] != wantExport {
		t.Errorf("op value = %+v, want {Name:%q}", op.Value, wantExport)
	}
}

// consumerStack returns a consumer stack template whose DB_HOST scalar carries
// the given value, so the sentinel round-trips to a whole scalar the resolver
// can address at /Resources/Api/Properties/Environment/DB_HOST.
func consumerStack(dbHost string) []byte {
	return []byte(`{"Resources":{"Api":{"Properties":{"Environment":{"DB_HOST":"` + dbHost + `"}}}}}`)
}

func TestLinkResolveSameAccountImportValue(t *testing.T) {
	edge := testLinkEdge(nil) // no account/region → same account+region
	pub, err := LinkPublish(edge)
	if err != nil {
		t.Fatalf("LinkPublish: %v", err)
	}
	sentinel := rendercore.CrossBoundaryLinkSentinel("api", "orders-db", "database", "host")
	files := []rendercore.RenderedFile{{Path: StackTemplateName, Content: consumerStack(sentinel)}}

	out, err := LinkResolve(edge, pub.Handle, files)
	if err != nil {
		t.Fatalf("LinkResolve: %v", err)
	}
	if len(out.Patches) != 1 || len(out.Patches[0].Ops) != 1 {
		t.Fatalf("want one patch with one op, got %+v", out.Patches)
	}
	op := out.Patches[0].Ops[0]
	if op.Op != "replace" || op.Path != "/Resources/Api/Properties/Environment/DB_HOST" {
		t.Errorf("op = %q at %q, want replace at the DB_HOST path", op.Op, op.Path)
	}
	val, ok := op.Value.(map[string]any)
	if !ok {
		t.Fatalf("op value not a map: %+v", op.Value)
	}
	wantExport := ExportName("", "prod/data", "orders-db", "host")
	if val["Fn::ImportValue"] != wantExport {
		t.Errorf("Fn::ImportValue = %v, want %q", val["Fn::ImportValue"], wantExport)
	}
}

func TestLinkResolveCrossAccountGetStackOutput(t *testing.T) {
	ann := map[string]string{
		annotationAWSAccount:       "222222222222",
		annotationAWSRegion:        "eu-west-1",
		annotationAWSAssumeRoleARN: "arn:aws:iam::222222222222:role/xacct",
	}
	edge := testLinkEdge(ann)
	pub, err := LinkPublish(edge)
	if err != nil {
		t.Fatalf("LinkPublish: %v", err)
	}
	sentinel := rendercore.CrossBoundaryLinkSentinel("api", "orders-db", "database", "host")
	files := []rendercore.RenderedFile{{Path: StackTemplateName, Content: consumerStack(sentinel)}}

	out, err := LinkResolve(edge, pub.Handle, files)
	if err != nil {
		t.Fatalf("LinkResolve: %v", err)
	}
	op := out.Patches[0].Ops[0]
	val, ok := op.Value.(map[string]any)
	if !ok {
		t.Fatalf("op value not a map: %+v", op.Value)
	}
	gso, ok := val["Fn::GetStackOutput"].(map[string]any)
	if !ok {
		t.Fatalf("value missing Fn::GetStackOutput: %+v", val)
	}
	if gso["StackName"] != StackName("", "prod/data") {
		t.Errorf("StackName = %v, want %q", gso["StackName"], StackName("", "prod/data"))
	}
	if gso["OutputName"] != "host" {
		t.Errorf("OutputName = %v, want %q", gso["OutputName"], "host")
	}
	if gso["Region"] != "eu-west-1" {
		t.Errorf("Region = %v, want eu-west-1", gso["Region"])
	}
	if gso["RoleArn"] != ann[annotationAWSAssumeRoleARN] {
		t.Errorf("RoleArn = %v, want %q", gso["RoleArn"], ann[annotationAWSAssumeRoleARN])
	}
}

func TestLinkResolveInterpolatedSentinelHalts(t *testing.T) {
	edge := testLinkEdge(nil)
	pub, _ := LinkPublish(edge)
	sentinel := rendercore.CrossBoundaryLinkSentinel("api", "orders-db", "database", "host")
	// The sentinel survives only inside a larger string — an interpolated value a
	// whole-scalar cross-reference cannot replace.
	files := []rendercore.RenderedFile{{Path: StackTemplateName, Content: consumerStack("jdbc://" + sentinel + ":5432")}}

	_, err := LinkResolve(edge, pub.Handle, files)
	if !errors.Is(err, ErrLinkSentinelInterpolated) {
		t.Fatalf("want ErrLinkSentinelInterpolated, got %v", err)
	}
}

func TestLinkResolveMissingConsumerStackHalts(t *testing.T) {
	edge := testLinkEdge(nil)
	pub, _ := LinkPublish(edge)
	_, err := LinkResolve(edge, pub.Handle, nil)
	if !errors.Is(err, ErrLinkConsumerStackMissing) {
		t.Fatalf("want ErrLinkConsumerStackMissing, got %v", err)
	}
}
