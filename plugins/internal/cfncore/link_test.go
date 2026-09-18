// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// testRelease is the Release identity both endpoints of the test edge
// carry, so the published export and stack names lead with a release-bearing
// scope segment rather than the empty-segment placeholder.
const testRelease = "prod-release"

// testLinkEdge builds a cross-boundary link edge from consumer "api" to producer
// "orders-db" on the "database" socket, injecting the target's "host" output. The
// target and source annotation maps carry the consumed and consuming endpoints'
// placement respectively, and both endpoints carry the Release the published
// names are scoped within.
func testLinkEdge(targetAnn, sourceAnn map[string]string) rendercore.LinkEdge {
	var env types.OrderedMap[string]
	env.Set("DB_HOST", "host")
	var inject types.OrderedMap[types.OrderedMap[string]]
	inject.Set("env", env)
	return rendercore.LinkEdge{
		Source:            rendercore.LinkEndpoint{Node: "api", Boundary: "prod/app", Provider: "cloudformation", Release: testRelease},
		Target:            rendercore.LinkEndpoint{Node: "orders-db", Boundary: "prod/data", Provider: "cloudformation", Release: testRelease},
		TargetAnnotations: targetAnn,
		SourceAnnotations: sourceAnn,
		Socket:            "database",
		Inject:            inject,
	}
}

func TestLinkPublishAddsExport(t *testing.T) {
	out, err := LinkPublish(testLinkEdge(nil, nil), nil)
	if err != nil {
		t.Fatalf("LinkPublish: %v", err)
	}
	wantExport := ExportName(testRelease, "prod/data", "orders-db", "host")

	if got := out.Handle[handleStackNameKey]; got != StackName(testRelease, "prod/data") {
		t.Errorf("handle stackName = %q, want %q", got, StackName(testRelease, "prod/data"))
	}
	if got := out.Handle[handleExportNamePrefix+"host"]; got != wantExport {
		t.Errorf("handle export name = %q, want %q", got, wantExport)
	}
	// The published names lead with the Release identity from the payload, so
	// distinct Releases produce distinct names within one account and region.
	if !strings.Contains(wantExport, testRelease) {
		t.Errorf("export name %q does not carry the Release segment %q", wantExport, testRelease)
	}
	if got := out.Handle[handleStackNameKey]; !strings.Contains(got, testRelease) {
		t.Errorf("stack name %q does not carry the Release segment %q", got, testRelease)
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
	wantPath := "/Outputs/ordersXzhdbXXhost/Export"
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
	pub, err := LinkPublish(edge, nil)
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
	wantExport := ExportName(testRelease, "prod/data", "orders-db", "host")
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
	pub, err := LinkPublish(edge, nil)
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
	if gso["StackName"] != StackName(testRelease, "prod/data") {
		t.Errorf("StackName = %v, want %q", gso["StackName"], StackName(testRelease, "prod/data"))
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
			pub, err := LinkPublish(edge, nil)
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
			wantExport := ExportName(testRelease, "prod/data", "orders-db", "host")
			if val["Fn::ImportValue"] != wantExport {
				t.Errorf("Fn::ImportValue = %v, want %q", val["Fn::ImportValue"], wantExport)
			}
		})
	}
}

func TestLinkResolveInterpolatedSentinelHalts(t *testing.T) {
	edge := testLinkEdge(nil, nil)
	pub, _ := LinkPublish(edge, nil)
	sentinel := rendercore.CrossBoundaryLinkSentinel("api", "orders-db", "database", "host")
	// The sentinel survives only inside a larger string — an interpolated value a
	// whole-scalar cross-reference cannot replace.
	files := []rendercore.RenderedFile{{Path: StackTemplateName, Content: consumerStack("jdbc://" + sentinel + ":5432")}}

	_, err := LinkResolve(edge, pub.Handle, files)
	if !errors.Is(err, wasm.ErrPluginValueNotRepresentable()) {
		t.Fatalf("want the value-not-representable condition, got %v", err)
	}
	// The condition names the kind; the sentence is what still says an
	// interpolated link value is the value with no representation.
	if want := "cross-boundary link value is interpolated into a larger string"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the halt no longer says the value was interpolated.\n  want: %s\n  got:  %v", want, err)
	}
}

func TestLinkResolveMissingConsumerStackHalts(t *testing.T) {
	edge := testLinkEdge(nil, nil)
	pub, _ := LinkPublish(edge, nil)
	_, err := LinkResolve(edge, pub.Handle, nil)
	if !errors.Is(err, wasm.ErrPluginRequiredInputUnresolved()) {
		t.Fatalf("want the unresolved-required-input condition, got %v", err)
	}
	if want := "cross-boundary link resolve received no consumer stack template"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the halt no longer says which input went unresolved.\n  want: %s\n  got:  %v", want, err)
	}
}

// producerStack returns the consumed endpoint's assembled files, carrying a
// stack template whose Outputs entries declare the given export names — keyed by
// the stack Output name the assembled template carries each under. An entry
// mapped to the empty string carries no Export block at all, which is what an
// Outputs entry its author wrote no export name on looks like once assembled.
func producerStack(t *testing.T, exports map[string]string) []rendercore.RenderedFile {
	t.Helper()
	outputs := map[string]any{}
	for entry, name := range exports {
		body := map[string]any{"Value": "a-value"}
		if name != "" {
			body["Export"] = map[string]any{"Name": name}
		}
		outputs[entry] = body
	}
	doc, err := json.Marshal(map[string]any{"Outputs": outputs})
	if err != nil {
		t.Fatalf("marshal the producer stack: %v", err)
	}
	return []rendercore.RenderedFile{{Path: StackTemplateName, Content: doc}}
}

func TestLinkPublishRefusesShadowedExportName(t *testing.T) {
	derived := ExportName(testRelease, "prod/data", "orders-db", "host")
	// The authored entry sits at its own Outputs name, so nothing overwrites
	// anything: the stack would carry one export name declared from two places.
	files := producerStack(t, map[string]string{
		OutputLogicalID("orders-db", "endpoint"): derived,
	})

	_, err := LinkPublish(testLinkEdge(nil, nil), files)
	if err == nil {
		t.Fatal("LinkPublish accepted an authored export name that shadows a derived one")
	}
	if !errors.Is(err, wasm.ErrPluginOutputNameCollision()) {
		t.Fatalf("want the output-name-collision condition, got %v", err)
	}
	if want := "an export name a cross-boundary link derives is already declared by another Outputs entry in the same stack"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the halt no longer says an export name is what collided.\n  want: %s\n  got:  %v", want, err)
	}
	// The halt presents the derived name by the coordinates it was assembled from,
	// and every identifier it shows is either a string the author typed or one it
	// marks as this core's own. The export name carries both jobs: it is the term
	// the author can search for, because the derivation reproduced a name they
	// wrote — that identity is what makes the collision a collision.
	for _, want := range []string{
		derived,
		`provides key "host"`,
		`component "orders-db"`,
		`boundary "prod/data"`,
		`Release "` + testRelease + `"`,
		`stack output "ordersXzhdbXXendpoint", a name this provider assembles rather than one a Recipe wrote`,
		"searching your Recipes for that name reaches the entry",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the diagnostic does not carry %s\n  got: %v", want, err)
		}
	}
	// The assembled Output name must never be presented as an authored one. The
	// derivation behind it drops separators and capitalises the following letter,
	// so several authored spellings reach one Output name and none of them can be
	// read back out of it — an author sent to look for that name searches their
	// own files for a string that is in none of them.
	if strings.Contains(err.Error(), `Outputs entry "`) {
		t.Errorf("the diagnostic presents an assembled Output name as an authored Outputs entry\n  got: %v", err)
	}
}

func TestLinkPublishAcceptsAuthoredExportNameCollidingWithNothing(t *testing.T) {
	files := producerStack(t, map[string]string{
		OutputLogicalID("orders-db", "endpoint"): "orders-db-endpoint-written-by-hand",
	})
	if _, err := LinkPublish(testLinkEdge(nil, nil), files); err != nil {
		t.Fatalf("an authored export name colliding with nothing was refused: %v", err)
	}
}

func TestLinkPublishAcceptsTwoDifferingAuthoredExportNames(t *testing.T) {
	files := producerStack(t, map[string]string{
		OutputLogicalID("orders-db", "endpoint"): "orders-db-endpoint",
		OutputLogicalID("orders-db", "replica"):  "orders-db-replica",
	})
	if _, err := LinkPublish(testLinkEdge(nil, nil), files); err != nil {
		t.Fatalf("two differing authored export names were refused: %v", err)
	}
}

func TestLinkPublishUnaffectedByStackDeclaringNoExportName(t *testing.T) {
	// An Outputs entry with no Export block declares no export name, so there is
	// nothing for a derived name to collide with.
	files := producerStack(t, map[string]string{
		OutputLogicalID("orders-db", "endpoint"): "",
	})
	out, err := LinkPublish(testLinkEdge(nil, nil), files)
	if err != nil {
		t.Fatalf("a stack declaring no export name was refused: %v", err)
	}
	if len(out.Patches) != 1 || len(out.Patches[0].Ops) != 1 {
		t.Fatalf("want the publish's own one patch with one op, got %+v", out.Patches)
	}
}

// TestLinkPublishUnaffectedByAbsentProducerFiles covers the optional field's
// absent readings. The payload field is optional, so a publish with nothing to
// compare against behaves exactly as one that was handed no files at all: it
// still publishes, because the publish needs no document to author its wiring.
func TestLinkPublishUnaffectedByAbsentProducerFiles(t *testing.T) {
	cases := []struct {
		name  string
		files []rendercore.RenderedFile
	}{
		{name: "nil", files: nil},
		{name: "empty", files: []rendercore.RenderedFile{}},
		{name: "no stack template", files: []rendercore.RenderedFile{{Path: "metadata.json", Content: []byte(`{}`)}}},
		{name: "stack that never parses", files: []rendercore.RenderedFile{{Path: StackTemplateName, Content: []byte(`{not json`)}}},
	}
	for _, tc := range cases {
		files := tc.files
		t.Run(tc.name, func(t *testing.T) {
			out, err := LinkPublish(testLinkEdge(nil, nil), files)
			if err != nil {
				t.Fatalf("LinkPublish: %v", err)
			}
			want := ExportName(testRelease, "prod/data", "orders-db", "host")
			if got := out.Handle[handleExportNamePrefix+"host"]; got != want {
				t.Errorf("handle export name = %q, want %q", got, want)
			}
		})
	}
}

// TestLinkPublishIgnoresItsOwnPriorWriteForASiblingEdge locks the exclusion that
// keeps this guard independent of the order edges are published in. The host
// applies each publish's wiring to the consumed endpoint's assembled files
// before it publishes the next edge, so a second edge injecting the same key
// from the same endpoint is handed a stack already carrying the Export this same
// publish wrote for the first edge. That Export sits at the entry the publish
// addresses and is the publish's own output rather than a name declared
// independently of the derivation — comparing against it would halt on edge
// order alone.
func TestLinkPublishIgnoresItsOwnPriorWriteForASiblingEdge(t *testing.T) {
	firstOut, err := LinkPublish(testLinkEdge(nil, nil), nil)
	if err != nil {
		t.Fatalf("the first publish: %v", err)
	}
	ownEntry := OutputLogicalID("orders-db", "host")
	// Assert the fixture below still models what the publish actually writes: if
	// the publish moved to another Outputs entry, the fixture would stop
	// reproducing the collision and this test would pass without testing it.
	if want := "/Outputs/" + ownEntry + "/Export"; firstOut.Patches[0].Ops[0].Path != want {
		t.Fatalf("the publish writes its Export at %q, want %q", firstOut.Patches[0].Ops[0].Path, want)
	}
	derived := ExportName(testRelease, "prod/data", "orders-db", "host")
	files := producerStack(t, map[string]string{ownEntry: derived})

	// A second consumer of the same endpoint and the same injected key, so its
	// publish derives that identical export name.
	second := testLinkEdge(nil, nil)
	second.Source.Node = "reports"
	if _, err := LinkPublish(second, files); err != nil {
		t.Fatalf("the second publish halted on the first publish's own Export: %v", err)
	}
}
