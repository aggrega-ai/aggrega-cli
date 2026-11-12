// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfncore

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
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

// TestLinkKeyOnlyACustomMechanismInjectsIsExportedAndResolved holds the link to
// every mechanism the edge injects under: a key the edge delivers only under a
// mechanism of its author's naming is exported by the publish and its placeholder
// replaced by the resolve with the import of that export, exactly as an env key
// is. Each row names the edge's inject mapping; in every row the key "host"
// reaches the consumer only under the custom mechanism ssm_ref.
func TestLinkKeyOnlyACustomMechanismInjectsIsExportedAndResolved(t *testing.T) {
	mapping := func(pairs ...[3]string) types.OrderedMap[types.OrderedMap[string]] {
		var out types.OrderedMap[types.OrderedMap[string]]
		for _, p := range pairs {
			keys, _ := out.Get(p[0])
			keys.Set(p[1], p[2])
			out.Set(p[0], keys)
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		inject types.OrderedMap[types.OrderedMap[string]]
	}{
		{name: "the custom mechanism alone", inject: mapping([3]string{"ssm_ref", "DB_HOST_REF", "host"})},
		{name: "beside an env key carrying another value", inject: mapping(
			[3]string{"env", "DB_PORT", "port"}, [3]string{"ssm_ref", "DB_HOST_REF", "host"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edge := testLinkEdge(nil, nil)
			edge.Inject = tc.inject
			wantExport := ExportName(testRelease, "prod/data", "orders-db", "host")

			pub, err := LinkPublish(edge, nil)
			if err != nil {
				t.Fatalf("LinkPublish: %v", err)
			}
			if got := pub.Handle[handleExportNamePrefix+"host"]; got != wantExport {
				t.Fatalf("handle export name for host = %q, want %q: the key a custom mechanism injects was not published", got, wantExport)
			}
			wantPath := "/Outputs/" + OutputLogicalID("orders-db", "host") + "/Export"
			exported := false
			for _, patch := range pub.Patches {
				for _, op := range patch.Ops {
					if op.Op == "add" && op.Path == wantPath {
						exported = true
					}
				}
			}
			if !exported {
				t.Fatalf("the publish adds no Export at %s; patches: %+v", wantPath, pub.Patches)
			}

			stack := []byte(`{"Resources":{"Api":{"Properties":{"Environment":{"DB_HOST_REF":"` + sentinelFor("host") + `"}}}}}`)
			out, err := LinkResolve(edge, pub.Handle, []rendercore.RenderedFile{{Path: StackTemplateName, Content: stack}})
			if err != nil {
				t.Fatalf("LinkResolve: %v", err)
			}
			if len(out.Patches) != 1 || len(out.Patches[0].Ops) != 1 {
				t.Fatalf("want one patch with one op replacing the placeholder, got %+v", out.Patches)
			}
			op := out.Patches[0].Ops[0]
			if op.Op != "replace" || op.Path != "/Resources/Api/Properties/Environment/DB_HOST_REF" {
				t.Errorf("op = %q at %q, want replace at the DB_HOST_REF path", op.Op, op.Path)
			}
			if val, _ := op.Value.(map[string]any); val["Fn::ImportValue"] != wantExport {
				t.Errorf("the placeholder becomes %+v, want the import of %q", op.Value, wantExport)
			}
		})
	}
}

// TestLinkResolveReplacesEveryOccurrence proves the resolver wires every place a
// consumer renders the placeholder: a value bound to a sourced template's parameter
// lands wherever the template referenced the parameter, so one injected key can
// reach the stack more than once, and each occurrence gets its own replace.
func TestLinkResolveReplacesEveryOccurrence(t *testing.T) {
	edge := testLinkEdge(nil, nil)
	pub, err := LinkPublish(edge, nil)
	if err != nil {
		t.Fatalf("LinkPublish: %v", err)
	}
	sentinel := rendercore.CrossBoundaryLinkSentinel("api", "orders-db", "database", "host")
	stack := []byte(`{"Resources":{"Api":{"Properties":{"Environment":{"DB_HOST":"` + sentinel + `"},"Tags":[{"Key":"db","Value":"` + sentinel + `"}]}}}}`)

	out, err := LinkResolve(edge, pub.Handle, []rendercore.RenderedFile{{Path: StackTemplateName, Content: stack}})
	if err != nil {
		t.Fatalf("LinkResolve: %v", err)
	}
	if len(out.Patches) != 1 {
		t.Fatalf("want one patch, got %+v", out.Patches)
	}
	var paths []string
	for _, op := range out.Patches[0].Ops {
		if op.Op != "replace" {
			t.Errorf("op %q at %q, want replace", op.Op, op.Path)
		}
		paths = append(paths, op.Path)
	}
	want := []string{"/Resources/Api/Properties/Environment/DB_HOST", "/Resources/Api/Properties/Tags/0/Value"}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Errorf("replaced %v, want every occurrence %v", paths, want)
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
	if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
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
	if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
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
	if !errors.Is(err, plugincondition.ErrPluginOutputNameCollision()) {
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
// keeps this guard independent of the order edges are published in, and the
// one-export rule that rides on it. The host applies each publish's wiring to
// the consumed endpoint's assembled files before it publishes the next edge, so
// a second edge injecting the same key from the same endpoint is handed a stack
// already carrying the Export this same publish wrote for the first edge. That
// Export sits at the entry the publish addresses and is the publish's own output
// rather than a name declared independently of the derivation — comparing
// against it would halt on edge order alone. Writing it a second time is what
// the stock codec refuses, as a replace over a mapping, so the second publish
// must emit nothing for the key and hand its consumer the same name.
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
	secondOut, err := LinkPublish(second, files)
	if err != nil {
		t.Fatalf("the second publish halted on the first publish's own Export: %v", err)
	}
	if len(secondOut.Patches) != 0 {
		t.Errorf("the second publish wrote the already-published Export again; want no operation, got %+v", secondOut.Patches)
	}
	if got := secondOut.Handle[handleExportNamePrefix+"host"]; got != derived {
		t.Errorf("the second publish hands its consumer the export name %q, want the one already published, %q", got, derived)
	}
	if got, want := secondOut.Handle[handleExportNamePrefix+"host"], firstOut.Handle[handleExportNamePrefix+"host"]; got != want {
		t.Errorf("the two consumers would import different names: %q and %q", want, got)
	}
}

// TestLinkPublishExportsOnlyTheKeyNoEarlierEdgeInjected covers a later edge that
// reads one key an earlier edge already exported and one it did not: the
// already-exported key is reused, and the new one gets its own Export.
func TestLinkPublishExportsOnlyTheKeyNoEarlierEdgeInjected(t *testing.T) {
	hostExport := ExportName(testRelease, "prod/data", "orders-db", "host")
	files := producerStack(t, map[string]string{
		OutputLogicalID("orders-db", "host"): hostExport,
		OutputLogicalID("orders-db", "port"): "",
	})
	edge := testLinkEdge(nil, nil)
	edge.Source.Node = "reports"
	var env types.OrderedMap[string]
	env.Set("DB_HOST", "host")
	env.Set("DB_PORT", "port")
	var inject types.OrderedMap[types.OrderedMap[string]]
	inject.Set("env", env)
	edge.Inject = inject

	out, err := LinkPublish(edge, files)
	if err != nil {
		t.Fatalf("LinkPublish: %v", err)
	}
	if len(out.Patches) != 1 || len(out.Patches[0].Ops) != 1 {
		t.Fatalf("want one patch with one op, for the key no earlier edge exported; got %+v", out.Patches)
	}
	op := out.Patches[0].Ops[0]
	portExport := ExportName(testRelease, "prod/data", "orders-db", "port")
	if want := "/Outputs/" + OutputLogicalID("orders-db", "port") + "/Export"; op.Op != "add" || op.Path != want {
		t.Errorf("op = %q at %q, want add at %q", op.Op, op.Path, want)
	}
	if val, ok := op.Value.(map[string]any); !ok || val["Name"] != portExport {
		t.Errorf("op value = %+v, want {Name:%q}", op.Value, portExport)
	}
	for key, want := range map[string]string{"host": hostExport, "port": portExport} {
		if got := out.Handle[handleExportNamePrefix+key]; got != want {
			t.Errorf("handle export name for %q = %q, want %q", key, got, want)
		}
	}
}

// producerStackWithBody returns the consumed endpoint's assembled files whose
// one Outputs entry, under the given stack Output name, carries the given Export
// block as written — so a case can give its Name a shape no string-valued
// fixture can express.
func producerStackWithBody(t *testing.T, outputName string, export any) []rendercore.RenderedFile {
	t.Helper()
	doc, err := json.Marshal(map[string]any{"Outputs": map[string]any{
		outputName: map[string]any{"Value": "a-value", "Export": export},
	}})
	if err != nil {
		t.Fatalf("marshal the producer stack: %v", err)
	}
	return []rendercore.RenderedFile{{Path: StackTemplateName, Content: doc}}
}

// TestLinkPublishRefusesAnotherExportAtItsOwnOutput covers the stack output the
// publish writes to already carrying an Export the link did not write. Writing
// the link's Export there would overwrite the author's, and keeping the author's
// would leave every consumer importing a name the link never published, so the
// publish halts naming what it found and how to clear it. Each row carries one
// Export shape, and the positive control beside them is the sibling-edge case
// above: the same entry carrying exactly the derived name publishes.
func TestLinkPublishRefusesAnotherExportAtItsOwnOutput(t *testing.T) {
	derived := ExportName(testRelease, "prod/data", "orders-db", "host")
	ownEntry := OutputLogicalID("orders-db", "host")
	cases := []struct {
		name   string
		export any
		found  string
	}{
		{
			name:   "a different string",
			export: map[string]any{"Name": "orders-db-host-chosen-by-hand"},
			found:  `already declares the export name "orders-db-host-chosen-by-hand"`,
		},
		{
			name:   "a name given as an intrinsic",
			export: map[string]any{"Name": map[string]any{"Fn::Sub": "${AWS::StackName}-orders-db-host"}},
			found:  "already declares an export name that is not a string — one given as an intrinsic",
		},
		{
			name:   "an Export block naming nothing",
			export: map[string]any{},
			found:  "already carries an Export block declaring no export name",
		},
		{
			name:   "an Export that is not a block",
			export: "orders-db-host-as-a-plain-value",
			found:  "already carries an Export that is not a block",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("LinkPublish panicked: %v", r)
				}
			}()
			out, err := LinkPublish(testLinkEdge(nil, nil), producerStackWithBody(t, ownEntry, tc.export))
			if err == nil {
				t.Fatalf("LinkPublish accepted an Export the link did not write at its own output; got %+v", out)
			}
			if !errors.Is(err, plugincondition.ErrPluginOutputNameCollision()) {
				t.Fatalf("want the output-name-collision condition, got %v", err)
			}
			for _, want := range []string{
				"the stack output a cross-boundary link publishes its export on already carries an Export the link did not write",
				derived,
				`provides key "host"`,
				`component "orders-db"`,
				`boundary "prod/data"`,
				`Release "` + testRelease + `"`,
				tc.found,
				"remove that entry's Export block and let the link publish the export every consumer imports",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the diagnostic does not carry %s\n  got: %v", want, err)
				}
			}
			// The assembled Output name is this provider's own, so the halt
			// names the component and the key it was assembled from instead.
			if strings.Contains(err.Error(), ownEntry) {
				t.Errorf("the diagnostic presents the assembled Output name %q as something to search for\n  got: %v", ownEntry, err)
			}
		})
	}
}

// readLinkEdge builds the test edge with the producer's provides set from
// key/value pairs, in the order given, and the given keys listed as read off the
// edge by the consuming templates. It injects nothing unless inject is true, in
// which case it keeps testLinkEdge's injection of "host".
func readLinkEdge(t *testing.T, inject bool, read []string, provides ...string) rendercore.LinkEdge {
	t.Helper()
	if len(provides)%2 != 0 {
		t.Fatalf("readLinkEdge: odd provides key/value count %d", len(provides))
	}
	edge := testLinkEdge(nil, nil)
	if !inject {
		edge.Inject = types.OrderedMap[types.OrderedMap[string]]{}
	}
	var p types.OrderedMap[string]
	for i := 0; i < len(provides); i += 2 {
		p.Set(provides[i], provides[i+1])
	}
	edge.Provides = p
	edge.ReadProvidesKeys = read
	return edge
}

// sentinelFor is the placeholder the host stamps for one provides key of the test
// edge, which every placeholder the resolve locates is.
func sentinelFor(key string) string {
	return rendercore.CrossBoundaryLinkSentinel("api", "orders-db", "database", key)
}

// TestLinkPublishOverReadProvidesKeys holds the publish to exporting a key read
// off the edge exactly when its value is a CloudFormation intrinsic, by the same
// reading whichever form the intrinsic is written in, and to exporting nothing for
// a read key whose value is plain text.
func TestLinkPublishOverReadProvidesKeys(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		exported bool
	}{
		{name: "a short-form intrinsic", value: "!GetAtt OrdersDb.Endpoint.Address", exported: true},
		{name: "a short-form Ref", value: "!Ref OrdersDbEndpoint", exported: true},
		{name: "a long-form intrinsic", value: `{"Fn::GetAtt": ["OrdersDb", "Endpoint.Address"]}`, exported: true},
		{name: "plain text", value: "orders.internal.example", exported: false},
		{name: "a number", value: "5432", exported: false},
		{name: "empty", value: "", exported: false},
		{name: "a mapping that is no intrinsic", value: `{"Name": "orders"}`, exported: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edge := readLinkEdge(t, false, []string{"host"}, "host", tc.value, "port", "5432")
			out, err := LinkPublish(edge, nil)
			if err != nil {
				t.Fatalf("LinkPublish: %v", err)
			}
			wantExport := ExportName(testRelease, "prod/data", "orders-db", "host")
			got, handled := out.Handle[handleExportNamePrefix+"host"]
			if !tc.exported {
				if handled || len(out.Patches) != 0 {
					t.Fatalf("a plain read key was exported: handle %v, patches %+v", out.Handle, out.Patches)
				}
				return
			}
			if got != wantExport {
				t.Errorf("handle export name = %q, want %q", got, wantExport)
			}
			if len(out.Patches) != 1 || len(out.Patches[0].Ops) != 1 {
				t.Fatalf("want one patch with one op, got %+v", out.Patches)
			}
			op := out.Patches[0].Ops[0]
			if want := "/Outputs/" + OutputLogicalID("orders-db", "host") + "/Export"; op.Op != "add" || op.Path != want {
				t.Errorf("op = %q at %q, want add at %q", op.Op, op.Path, want)
			}
			if val, ok := op.Value.(map[string]any); !ok || val["Name"] != wantExport {
				t.Errorf("op value = %+v, want {Name:%q}", op.Value, wantExport)
			}
			// The provides key nothing read stays unexported.
			if _, present := out.Handle[handleExportNamePrefix+"port"]; present {
				t.Errorf("a provides key nothing read or injected was exported: %v", out.Handle)
			}
		})
	}
}

// TestLinkResolveRewritesAnIntrinsicReadKey holds the resolve to replacing every
// placeholder of a read key whose value is an intrinsic exactly as it replaces an
// injected key's: an import of the published export where the ends share a
// placement, a cross-placement read where both declare differing ones.
func TestLinkResolveRewritesAnIntrinsicReadKey(t *testing.T) {
	cases := []struct {
		name      string
		targetAnn map[string]string
		sourceAnn map[string]string
		check     func(t *testing.T, value map[string]any)
	}{
		{
			name: "same placement",
			check: func(t *testing.T, value map[string]any) {
				if want := ExportName(testRelease, "prod/data", "orders-db", "host"); value["Fn::ImportValue"] != want {
					t.Errorf("Fn::ImportValue = %v, want %q", value["Fn::ImportValue"], want)
				}
			},
		},
		{
			name:      "differing placements",
			targetAnn: map[string]string{annotationAWSAccount: "222222222222", annotationAWSRegion: "eu-west-1"},
			sourceAnn: map[string]string{annotationAWSAccount: "111111111111"},
			check: func(t *testing.T, value map[string]any) {
				gso, ok := value["Fn::GetStackOutput"].(map[string]any)
				if !ok {
					t.Fatalf("value carries no Fn::GetStackOutput: %+v", value)
				}
				if want := OutputLogicalID("orders-db", "host"); gso["OutputName"] != want {
					t.Errorf("OutputName = %v, want %q", gso["OutputName"], want)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edge := readLinkEdge(t, false, []string{"host"}, "host", "!GetAtt OrdersDb.Endpoint.Address")
			edge.TargetAnnotations = tc.targetAnn
			edge.SourceAnnotations = tc.sourceAnn
			pub, err := LinkPublish(edge, nil)
			if err != nil {
				t.Fatalf("LinkPublish: %v", err)
			}
			stack := []byte(`{"Resources":{"Api":{"Properties":{"Environment":{"DB_HOST":"` + sentinelFor("host") + `"},"Tags":[{"Key":"db","Value":"` + sentinelFor("host") + `"}]}}}}`)
			out, err := LinkResolve(edge, pub.Handle, []rendercore.RenderedFile{{Path: StackTemplateName, Content: stack}})
			if err != nil {
				t.Fatalf("LinkResolve: %v", err)
			}
			if len(out.Patches) != 1 || len(out.Patches[0].Ops) != 2 {
				t.Fatalf("want one patch replacing both occurrences, got %+v", out.Patches)
			}
			for _, op := range out.Patches[0].Ops {
				value, ok := op.Value.(map[string]any)
				if op.Op != "replace" || !ok {
					t.Fatalf("op %q at %q carries %+v, want a replace by a reference", op.Op, op.Path, op.Value)
				}
				tc.check(t, value)
			}
		})
	}
}

// TestLinkResolveWritesBackAPlainReadKey holds the resolve to writing a plain read
// key's value back in place of its placeholder: a whole-scalar placeholder as the
// value the provides-to-Outputs projection reads, so a number lands as a number
// and an empty value as nothing, and placeholders inside one larger string as the
// text the producer wrote, in one operation for that string. The publish exports
// nothing for any of them.
func TestLinkResolveWritesBackAPlainReadKey(t *testing.T) {
	edge := readLinkEdge(t, false, []string{"protocol", "port", "user"},
		"protocol", "postgres", "port", "5432", "user", "")
	pub, err := LinkPublish(edge, nil)
	if err != nil {
		t.Fatalf("LinkPublish: %v", err)
	}
	if len(pub.Patches) != 0 {
		t.Fatalf("the publish exported a plain read key: %+v", pub.Patches)
	}
	stack := []byte(`{"Resources":{"Api":{"Properties":{"Environment":{` +
		`"DB_PORT":"` + sentinelFor("port") + `",` +
		`"DB_URL":"` + sentinelFor("protocol") + `://orders:` + sentinelFor("port") + `/app",` +
		`"DB_USER":"` + sentinelFor("user") + `"}}}}}`)
	out, err := LinkResolve(edge, pub.Handle, []rendercore.RenderedFile{{Path: StackTemplateName, Content: stack}})
	if err != nil {
		t.Fatalf("LinkResolve: %v", err)
	}
	if len(out.Patches) != 1 {
		t.Fatalf("want one patch, got %+v", out.Patches)
	}
	got := map[string]any{}
	for _, op := range out.Patches[0].Ops {
		if op.Op != "replace" {
			t.Errorf("op %q at %q, want replace", op.Op, op.Path)
		}
		if _, twice := got[op.Path]; twice {
			t.Errorf("the path %q is replaced twice", op.Path)
		}
		got[op.Path] = op.Value
	}
	const env = "/Resources/Api/Properties/Environment/"
	want := map[string]any{
		env + "DB_PORT": int64(5432),
		env + "DB_URL":  "postgres://orders:5432/app",
		env + "DB_USER": nil,
	}
	if len(got) != len(want) {
		t.Fatalf("replaced %v, want exactly %v", got, want)
	}
	for path, value := range want {
		if v, ok := got[path]; !ok || v != value {
			t.Errorf("%s = %#v, want %#v", path, got[path], value)
		}
	}
}

// TestLinkKeyBothInjectedAndReadFollowsTheInjectedRule holds a key the edge both
// injects and lists as read to the injected rule at every occurrence: exported
// once and imported everywhere, even where its value is plain text, which a read
// alone would write back.
func TestLinkKeyBothInjectedAndReadFollowsTheInjectedRule(t *testing.T) {
	edge := readLinkEdge(t, true, []string{"host"}, "host", "orders.internal.example")
	pub, err := LinkPublish(edge, nil)
	if err != nil {
		t.Fatalf("LinkPublish: %v", err)
	}
	if len(pub.Patches) != 1 || len(pub.Patches[0].Ops) != 1 {
		t.Fatalf("want one export, got %+v", pub.Patches)
	}
	stack := []byte(`{"Resources":{"Api":{"Properties":{"Environment":{"DB_HOST":"` + sentinelFor("host") + `","DIRECT":"` + sentinelFor("host") + `"}}}}}`)
	out, err := LinkResolve(edge, pub.Handle, []rendercore.RenderedFile{{Path: StackTemplateName, Content: stack}})
	if err != nil {
		t.Fatalf("LinkResolve: %v", err)
	}
	if len(out.Patches) != 1 || len(out.Patches[0].Ops) != 2 {
		t.Fatalf("want both occurrences replaced, got %+v", out.Patches)
	}
	wantExport := ExportName(testRelease, "prod/data", "orders-db", "host")
	for _, op := range out.Patches[0].Ops {
		if value, ok := op.Value.(map[string]any); !ok || value["Fn::ImportValue"] != wantExport {
			t.Errorf("%s = %+v, want an import of %q", op.Path, op.Value, wantExport)
		}
	}
}

// TestLinkResolveHaltsOnAnInterpolatedIntrinsicReadKey holds a read key whose value
// is an intrinsic to the same halt an injected key meets inside a larger string.
func TestLinkResolveHaltsOnAnInterpolatedIntrinsicReadKey(t *testing.T) {
	edge := readLinkEdge(t, false, []string{"host"}, "host", "!GetAtt OrdersDb.Endpoint.Address")
	pub, err := LinkPublish(edge, nil)
	if err != nil {
		t.Fatalf("LinkPublish: %v", err)
	}
	files := []rendercore.RenderedFile{{Path: StackTemplateName, Content: consumerStack("jdbc://" + sentinelFor("host") + ":5432")}}
	_, err = LinkResolve(edge, pub.Handle, files)
	if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
		t.Fatalf("want the value-not-representable condition, got %v", err)
	}
	if want := `cross-boundary link value is interpolated into a larger string; only a whole-scalar injection can carry a cross-stack reference: output key "host" on socket "database"`; !strings.Contains(err.Error(), want) {
		t.Fatalf("the halt is not the one an injected key meets.\n  want: %s\n  got:  %v", want, err)
	}
}

// TestLinkHaltsOnAReadKeyTheEdgeDoesNotCarry holds both exports to refusing a read
// key that names no provides key of the edge, naming the key, the consumer, the
// socket and the producer, rather than wiring it to nothing.
func TestLinkHaltsOnAReadKeyTheEdgeDoesNotCarry(t *testing.T) {
	edge := readLinkEdge(t, false, []string{"replica"}, "host", "orders.internal.example")
	check := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
			t.Fatalf("want the unresolved-required-input condition, got %v", err)
		}
		for _, want := range []string{`provides key "replica"`, `component "api"`, `socket "database"`, `component "orders-db" provides no such key`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the halt does not carry %s\n  got: %v", want, err)
			}
		}
	}
	t.Run("publish", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("LinkPublish panicked: %v", r)
			}
		}()
		_, err := LinkPublish(edge, nil)
		check(t, err)
	})
	t.Run("resolve", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("LinkResolve panicked: %v", r)
			}
		}()
		_, err := LinkResolve(edge, nil, []rendercore.RenderedFile{{Path: StackTemplateName, Content: consumerStack("x")}})
		check(t, err)
	})
}
