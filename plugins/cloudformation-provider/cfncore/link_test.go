// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// testDeployment is the deployment identity both endpoints of the test edge
// carry, so the published export and stack names lead with a release-bearing
// scope segment rather than the empty-segment placeholder.
const testDeployment = "prod-release"

// testLinkEdge builds a cross-boundary link edge from consumer "api" to producer
// "orders-db" on the "database" socket, injecting the target's "host" output. The
// target and source annotation maps carry the consumed and consuming endpoints'
// placement respectively, and both endpoints carry the deployment identity the
// published names are scoped within.
func testLinkEdge(targetAnn, sourceAnn map[string]string) rendercore.LinkEdge {
	var env types.OrderedMap[string]
	env.Set("DB_HOST", "host")
	var inject types.OrderedMap[types.OrderedMap[string]]
	inject.Set("env", env)
	return rendercore.LinkEdge{
		Source:            rendercore.LinkEndpoint{Node: "api", Boundary: "prod/app", Provider: "cloudformation", Deployment: testDeployment},
		Target:            rendercore.LinkEndpoint{Node: "orders-db", Boundary: "prod/data", Provider: "cloudformation", Deployment: testDeployment},
		TargetAnnotations: targetAnn,
		SourceAnnotations: sourceAnn,
		Socket:            "database",
		Inject:            inject,
	}
}

func TestLinkPublishAddsExport(t *testing.T) {
	out, err := LinkPublish(testLinkEdge(nil, nil))
	if err != nil {
		t.Fatalf("LinkPublish: %v", err)
	}
	wantExport := ExportName(testDeployment, "prod/data", "orders-db", "host")

	if got := out.Handle[handleStackNameKey]; got != StackName(testDeployment, "prod/data") {
		t.Errorf("handle stackName = %q, want %q", got, StackName(testDeployment, "prod/data"))
	}
	if got := out.Handle[handleExportNamePrefix+"host"]; got != wantExport {
		t.Errorf("handle export name = %q, want %q", got, wantExport)
	}
	// The published names lead with the deployment identity from the payload, so
	// distinct deployments produce distinct names within one account and region.
	if !strings.Contains(wantExport, testDeployment) {
		t.Errorf("export name %q does not carry the deployment segment %q", wantExport, testDeployment)
	}
	if got := out.Handle[handleStackNameKey]; !strings.Contains(got, testDeployment) {
		t.Errorf("stack name %q does not carry the deployment segment %q", got, testDeployment)
	}

	if len(out.Patches) != 1 || len(out.Patches[0].Ops) != 1 {
		t.Fatalf("want one patch with one op, got %+v", out.Patches)
	}
	op := out.Patches[0].Ops[0]
	if out.Patches[0].File != StackTemplateName {
		t.Errorf("patch file = %q, want %q", out.Patches[0].File, StackTemplateName)
	}
	// The Export lands on the output the producer node's own projection named, not
	// on the bare injected key: another node in the producer's stack may export the
	// same key, and the two are separate outputs.
	wantPath := "/Outputs/OrdersDbHost/Export"
	if got := "/Outputs/" + OutputLogicalID("orders-db", "host") + "/Export"; got != wantPath {
		t.Fatalf("the producer output path derives as %q, want %q", got, wantPath)
	}
	if op.Op != "add" || op.Path != wantPath {
		t.Errorf("op = %q at %q, want add at %q", op.Op, op.Path, wantPath)
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
	edge := testLinkEdge(nil, nil) // neither end declares placement → same account+region
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
	wantExport := ExportName(testDeployment, "prod/data", "orders-db", "host")
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
	// The consumer (source) declares a different account, so both ends declare a
	// placement and the placements differ — the cross-placement read fires.
	sourceAnn := map[string]string{
		annotationAWSAccount: "111111111111",
	}
	edge := testLinkEdge(ann, sourceAnn)
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
	if gso["StackName"] != StackName(testDeployment, "prod/data") {
		t.Errorf("StackName = %v, want %q", gso["StackName"], StackName(testDeployment, "prod/data"))
	}
	// The cross-account read names the output as the producer stack named it —
	// scoped by the producer node — rather than the bare injected key.
	if want := OutputLogicalID("orders-db", "host"); gso["OutputName"] != want {
		t.Errorf("OutputName = %v, want %q", gso["OutputName"], want)
	}
	if gso["Region"] != "eu-west-1" {
		t.Errorf("Region = %v, want eu-west-1", gso["Region"])
	}
	if gso["RoleArn"] != ann[annotationAWSAssumeRoleARN] {
		t.Errorf("RoleArn = %v, want %q", gso["RoleArn"], ann[annotationAWSAssumeRoleARN])
	}
}

// TestLinkResolveSameAccountDeclaredImportValue proves the tightened
// peer-selection: a producer (target) that declares its placement no longer
// forces a cross-placement read on its own. When the consumer (source) declares
// the same placement, or declares none at all, the two ends are treated as
// co-located and the reference resolves to a same-placement Fn::ImportValue.
func TestLinkResolveSameAccountDeclaredImportValue(t *testing.T) {
	cases := []struct {
		name      string
		targetAnn map[string]string
		sourceAnn map[string]string
	}{
		{
			name: "both declare the same account",
			targetAnn: map[string]string{
				annotationAWSAccount: "111111111111",
			},
			sourceAnn: map[string]string{
				annotationAWSAccount: "111111111111",
			},
		},
		{
			name: "producer declares, consumer undeclared",
			targetAnn: map[string]string{
				annotationAWSAccount:       "111111111111",
				annotationAWSRegion:        "eu-west-1",
				annotationAWSAssumeRoleARN: "arn:aws:iam::111111111111:role/xacct",
			},
			sourceAnn: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edge := testLinkEdge(tc.targetAnn, tc.sourceAnn)
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
			val, ok := out.Patches[0].Ops[0].Value.(map[string]any)
			if !ok {
				t.Fatalf("op value not a map: %+v", out.Patches[0].Ops[0].Value)
			}
			if _, present := val["Fn::GetStackOutput"]; present {
				t.Fatalf("same-placement consumer value carries an Fn::GetStackOutput read; want Fn::ImportValue. got %#v", val)
			}
			wantExport := ExportName(testDeployment, "prod/data", "orders-db", "host")
			if val["Fn::ImportValue"] != wantExport {
				t.Errorf("Fn::ImportValue = %v, want %q", val["Fn::ImportValue"], wantExport)
			}
		})
	}
}

func TestLinkResolveInterpolatedSentinelHalts(t *testing.T) {
	edge := testLinkEdge(nil, nil)
	pub, _ := LinkPublish(edge)
	sentinel := rendercore.CrossBoundaryLinkSentinel("api", "orders-db", "database", "host")
	// The sentinel survives only inside a larger string — an interpolated value a
	// whole-scalar cross-reference cannot replace.
	files := []rendercore.RenderedFile{{Path: StackTemplateName, Content: consumerStack("jdbc://" + sentinel + ":5432")}}

	_, err := LinkResolve(edge, pub.Handle, files)
	if !errors.Is(err, ErrLinkSentinelInterpolated()) {
		t.Fatalf("want ErrLinkSentinelInterpolated, got %v", err)
	}
}

func TestLinkResolveMissingConsumerStackHalts(t *testing.T) {
	edge := testLinkEdge(nil, nil)
	pub, _ := LinkPublish(edge)
	_, err := LinkResolve(edge, pub.Handle, nil)
	if !errors.Is(err, ErrLinkConsumerStackMissing()) {
		t.Fatalf("want ErrLinkConsumerStackMissing, got %v", err)
	}
}
