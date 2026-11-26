// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudformation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

const (
	configSet    = "AWS::SES::ConfigurationSet"
	poolInject   = "delivery_options_sending_pool_name"
	poolValue    = "!GetAtt Pool.Name"
	templateName = "main.yaml"
)

// configWritten is an AWS::SES::ConfigurationSet as a template writes it: the
// name, two settings, a password fed by a NoEcho parameter, a neighbour's pool
// inside DeliveryOptions, the policies written beside Properties, and the
// attributes its neighbours read.
func configWritten() schemadraft.WrittenResource {
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	return schemadraft.WrittenResource{
		ID: "Config", Type: configSet,
		Properties: []schemadraft.WrittenProperty{
			{Path: []string{"Properties", "Name"}, Value: raw(`"orders"`), Identity: true},
			{Path: []string{"Properties", "ReputationOptions"}, Value: raw(`{"ReputationMetricsEnabled": true}`)},
			{Path: []string{"Properties", "SendingOptions", "SendingEnabled"}, Value: raw(`false`)},
			{Path: []string{"Properties", "SmtpPassword"}, Secret: true},
			{Path: []string{"Properties", "DeliveryOptions"}, Value: raw(`{"TlsPolicy": "REQUIRE"}`)},
			{Path: []string{"Properties", "DeliveryOptions", "SendingPoolName"}, Inject: poolInject},
			{Path: []string{"DeletionPolicy"}, Value: raw(`"Retain"`)},
			{Path: []string{"Metadata"}, Value: raw(`{"note": "kept"}`)},
		},
		Provides: []schemadraft.Provide{
			{Key: "id", Attribute: []string{"Name"}, Identifier: true},
			{Key: "arn", Attribute: []string{"Arn"}},
		},
	}
}

func templateInput() aggrega.ImportInput {
	return aggrega.ImportInput{Format: "cloudformation", Name: templateName}
}

// draftedConfig drafts w from the source, which must raise no halt, and returns
// the drafted Component and Recipe bodies.
func draftedConfig(t *testing.T, w schemadraft.WrittenResource) (schemadraft.Drafted, []aggrega.Diagnostic, string, string) {
	t.Helper()
	d, diags := schemadraft.DraftResource(templateInput(), Format, w)
	noHalt(t, diags)
	if len(d.Entities) != 2 {
		t.Fatalf("drafted %d entities, want a Component and a Recipe", len(d.Entities))
	}
	return d, diags, d.Entities[0].Entity.Body, d.Entities[1].Entity.Body
}

// writtenGolden compares got with testdata/written/<name>; UPDATE_GOLDEN=1
// writes it instead.
func writtenGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "written", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the golden %s: %v (run with UPDATE_GOLDEN=1 to write it)", path, err)
	}
	if got != string(want) {
		t.Errorf("%s differs from its golden (run with UPDATE_GOLDEN=1 to rewrite it):\n%s", path, got)
	}
}

// configContext is the context the drafted Recipe renders in for the instance
// orders-config, a neighbour's pool injected and the overrides given.
func configContext(overrides map[string]any) map[string]any {
	return map[string]any{
		"context": map[string]any{"instance_id": "orders-config", "inject": map[string]any{
			"property": map[string]any{poolInject: poolValue}}},
		"metadata": map[string]any{"instance_id": "orders-config"},
		"values":   map[string]any{"name": "orders"},
		"infra":    map[string]any{"overrides": overrides},
		"features": map[string]any{},
	}
}

// configOverrides are the overrides a Release sets for the configuration set,
// each under the vendor's spelling.
func configOverrides() map[string]any {
	return map[string]any{
		"ReputationOptions": map[string]any{"ReputationMetricsEnabled": true},
		"SendingEnabled":    false,
		"DeliveryOptions":   map[string]any{"TlsPolicy": "REQUIRE"},
		"DeletionPolicy":    "Retain",
	}
}

// TestCloudFormationDraftResourceGolden drafts the configuration set from the
// template and renders it: each property under Properties at its path, the
// neighbour's pool set inside DeliveryOptions without changing the Release's
// value, the password a NoEcho parameter, the policy beside Properties, and the
// provides !Ref and !GetAtt; the Component and Recipe match their goldens and
// two runs draft one byte sequence.
func TestCloudFormationDraftResourceGolden(t *testing.T) {
	d, diags, component, recipe := draftedConfig(t, configWritten())
	writtenGolden(t, "configurationset.component.yaml", component)
	writtenGolden(t, "configurationset.recipe.yaml", recipe)

	parts := partsOf(t, recipe)
	overrides := configOverrides()
	before := canon(t, overrides)
	text, doc := renderFragment(t, parts, configContext(overrides))
	res := at(doc, "Resources", "OrdersConfig")
	want := `{"DeletionPolicy": "Retain", "Type": "` + configSet + `", "Properties": {"Name": "orders", ` +
		`"ReputationOptions": {"ReputationMetricsEnabled": true}, "SendingOptions": {"SendingEnabled": false}, ` +
		`"SmtpPassword": {"Ref": "OrdersConfigSmtpPassword"}, "DeliveryOptions": {"TlsPolicy": "REQUIRE", ` +
		`"SendingPoolName": {"Fn::GetAtt": ["Pool", "Name"]}}}}`
	if got := canon(t, res); got != canonText(t, want) {
		t.Errorf("resource %s, want %s\n%s", got, canonText(t, want), text)
	}
	if got := canon(t, at(doc, "Resources", "OrdersConfig", "Properties", "DeliveryOptions", "SendingPoolName")); got !=
		`{"Fn::GetAtt":["Pool","Name"]}` {
		t.Errorf("SendingPoolName %s", got)
	}
	if got := canon(t, at(doc, "Parameters")); got != canonText(t,
		`{"OrdersConfigSmtpPassword": {"Type": "String", "NoEcho": true}}`) {
		t.Errorf("parameters %s", got)
	}
	if got := canon(t, at(doc, "provides")); got != canonText(t,
		`{"arn": {"Fn::GetAtt": ["OrdersConfig", "Arn"]}, "id": {"Ref": "OrdersConfig"}}`) {
		t.Errorf("fragment provides %s", got)
	}
	if after := canon(t, overrides); after != before {
		t.Errorf("rendering changed the Release's overrides:\n%s\nwas\n%s", after, before)
	}
	if len(doc) != 3 {
		t.Errorf("the fragment holds %d sections, want Parameters, Resources and provides", len(doc))
	}

	// What is left out is named, and nothing else is.
	var info string
	for _, g := range diags {
		if g.Code == schemadraft.CodeResourceDraftedFromSource {
			info = g.Message
		}
	}
	if !strings.Contains(info, "Metadata is left out") || strings.Contains(info, "DeletionPolicy is left out") {
		t.Errorf("the info %q does not name Metadata alone as left out", info)
	}

	again, againDiags := schemadraft.DraftResource(templateInput(), Format, configWritten())
	if !reflect.DeepEqual(d, again) || !reflect.DeepEqual(diags, againDiags) {
		t.Errorf("two runs differ")
	}
}

// TestCloudFormationDraftResourceKeepsPropertiesOfOneSegment writes a member of
// Properties named at a path of two segments and one of a single segment alike.
func TestCloudFormationDraftResourceKeepsPropertiesOfOneSegment(t *testing.T) {
	w := schemadraft.WrittenResource{ID: "Q", Type: "AWS::SQS::Queue", Properties: []schemadraft.WrittenProperty{
		{Path: []string{"Properties", "QueueName"}, Value: json.RawMessage(`"jobs"`), Identity: true},
		{Path: []string{"Properties", "DelaySeconds"}, Value: json.RawMessage(`5`)},
	}, Provides: []schemadraft.Provide{{Key: "arn", Attribute: []string{"Arn"}}}}
	_, _, _, recipe := draftedConfig(t, w)
	ctx := map[string]any{"context": map[string]any{"instance_id": "jobs", "inject": map[string]any{}},
		"values": map[string]any{"queue_name": "jobs"}, "infra": map[string]any{"overrides": map[string]any{"DelaySeconds": 5}},
		"features": map[string]any{}}
	_, doc := renderFragment(t, partsOf(t, recipe), ctx)
	if got := canon(t, at(doc, "Resources", "Jobs", "Properties")); got != `{"DelaySeconds":5,"QueueName":"jobs"}` {
		t.Errorf("properties %s", got)
	}
}

// TestServerlessTypeDraftCarriesTheTransform writes the SAM transform into the
// fragment of a type under AWS::Serverless::, and none for any other type.
func TestServerlessTypeDraftCarriesTheTransform(t *testing.T) {
	layer := schemadraft.WrittenResource{ID: "Layer", Type: "AWS::Serverless::LayerVersion",
		Properties: []schemadraft.WrittenProperty{
			{Path: []string{"Properties", "LayerName"}, Value: json.RawMessage(`"deps"`), Identity: true},
			{Path: []string{"Properties", "ContentUri"}, Value: json.RawMessage(`"s3://b/k"`)},
		}, Provides: []schemadraft.Provide{{Key: "arn", Attribute: []string{"Arn"}}}}
	render := func(w schemadraft.WrittenResource) map[string]any {
		_, _, _, recipe := draftedConfig(t, w)
		ctx := map[string]any{"context": map[string]any{"instance_id": "deps", "inject": map[string]any{}},
			"values": map[string]any{"layer_name": "deps"}, "infra": map[string]any{
				"overrides": map[string]any{"ContentUri": "s3://b/k"}}, "features": map[string]any{}}
		_, doc := renderFragment(t, partsOf(t, recipe), ctx)
		return doc
	}
	doc := render(layer)
	if got := at(doc, "Transform"); got != "AWS::Serverless-2016-10-31" {
		t.Errorf("Transform %v, want AWS::Serverless-2016-10-31", got)
	}
	if got := at(doc, "Resources", "Deps", "Type"); got != "AWS::Serverless::LayerVersion" {
		t.Errorf("type %v", got)
	}
	plain := schemadraft.WrittenResource{ID: "Layer", Type: "AWS::Lambda::LayerVersion", Properties: layer.Properties, Provides: layer.Provides}
	if doc := render(plain); at(doc, "Transform") != nil {
		t.Errorf("a type outside AWS::Serverless:: writes Transform %v", at(doc, "Transform"))
	}
}

// TestCloudFormationFromSourceSwapHint names the schema import of the type, the
// command producing its schema file and the setting naming the type.
func TestCloudFormationFromSourceSwapHint(t *testing.T) {
	d, _, _, _ := draftedConfig(t, configWritten())
	for _, named := range []string{
		"aggrega import --format cloudformation-schema <describe-type output>",
		"aws cloudformation describe-type --type RESOURCE --type-name " + configSet,
		"resource_types"} {
		if !strings.Contains(d.Decision.SwapHint, named) {
			t.Errorf("the swap hint %q does not name %s", d.Decision.SwapHint, named)
		}
	}
}

// TestCloudFormationLeavesOutWhatSitsBesideProperties leaves out a resource
// attribute beside Properties other than the two policies, naming it, and
// refuses a neighbour's value for a policy.
func TestCloudFormationLeavesOutWhatSitsBesideProperties(t *testing.T) {
	w := configWritten()
	w.Properties = append(w.Properties,
		schemadraft.WrittenProperty{Path: []string{"UpdateReplacePolicy"}, Value: json.RawMessage(`"Delete"`)},
		schemadraft.WrittenProperty{Path: []string{"DependsOn"}, Value: json.RawMessage(`["Pool"]`)})
	_, diags, _, recipe := draftedConfig(t, w)
	for _, g := range diags {
		if g.Code == schemadraft.CodeResourceDraftedFromSource {
			for _, left := range []string{"Metadata is left out", "DependsOn is left out"} {
				if !strings.Contains(g.Message, left) {
					t.Errorf("the info %q does not say %q", g.Message, left)
				}
			}
		}
	}
	ctx := configContext(configOverrides())
	ctx["infra"].(map[string]any)["overrides"].(map[string]any)["UpdateReplacePolicy"] = "Delete"
	_, doc := renderFragment(t, partsOf(t, recipe), ctx)
	if at(doc, "Resources", "OrdersConfig", "UpdateReplacePolicy") != "Delete" ||
		at(doc, "Resources", "OrdersConfig", "DeletionPolicy") != "Retain" {
		t.Errorf("the policies beside Properties: %s", canon(t, at(doc, "Resources", "OrdersConfig")))
	}

	w.Properties = append(w.Properties, schemadraft.WrittenProperty{Path: []string{"DeletionPolicy"}, Inject: "deletion_policy"})
	_, ds := schemadraft.DraftResource(templateInput(), Format, w)
	if h := onlyHalt(t, ds, codeRefused); !strings.Contains(h.Message, "DeletionPolicy") {
		t.Errorf("halt %q does not name DeletionPolicy", h.Message)
	}
}
