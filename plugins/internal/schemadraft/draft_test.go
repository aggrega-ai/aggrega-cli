// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// queueWritten is a queue of the sample format as a source writes it, which a
// schema import of the sample format places alike: its name, region, a
// setting, a secret, and its url and arn read back.
func queueWritten() WrittenResource {
	return WrittenResource{
		Type: fxQueue,
		Properties: []WrittenProperty{
			{Path: []string{"QueueName"}, Value: json.RawMessage(`"orders"`), Identity: true},
			{Path: []string{"Region"}, Value: json.RawMessage(`"eu-west-1"`), Placement: true},
			{Path: []string{"DelaySeconds"}, Value: json.RawMessage(`30`)},
			{Path: []string{"Tags"}, Value: json.RawMessage(`{"team": "orders"}`)},
			{Path: []string{"AuthToken"}, Secret: true},
		},
		Provides: []Provide{
			{Key: "url", Attribute: []string{"Url"}, Identifier: true},
			{Key: "arn", Attribute: []string{"Arn"}},
		},
	}
}

// writtenFormat is the sample format reading the one type FromWritten builds
// of w, with rules placing each property as w flags it.
func writtenFormat(w WrittenResource) Format {
	typ, _ := FromWritten(w)
	flagged := func(pick func(WrittenProperty) bool) func(*Type, *Property) bool {
		return func(_ *Type, p *Property) bool {
			for _, wp := range w.Properties {
				if slices.Equal(wp.Path, p.Path) {
					return pick(wp)
				}
			}
			return false
		}
	}
	f := fxFormat()
	f.List = func(in aggrega.ImportInput, _ ParseFunc) ([]TypeRef, []aggrega.Diagnostic) {
		return []TypeRef{{Name: typ.Name, APIVersion: typ.APIVersion, Source: in.Name}}, nil
	}
	f.Lift = func(TypeRef) (*Type, []aggrega.Diagnostic) { return typ, nil }
	f.Rules = Rules{
		Placement: flagged(func(wp WrittenProperty) bool { return wp.Placement }),
		Identity:  flagged(func(wp WrittenProperty) bool { return wp.Identity }),
		Secret:    flagged(func(wp WrittenProperty) bool { return wp.Secret }),
	}
	f.SchemaFile = "sample-types.json"
	return f
}

// writtenInput is an import request of the sample format, under settings given
// as JSON, empty for none.
func writtenInput(settings string) aggrega.ImportInput {
	in := aggrega.ImportInput{Format: fxFormatName, Name: "queue.json", Bytes: []byte("{}")}
	if settings != "" {
		in.Settings = json.RawMessage(settings)
	}
	return in
}

// noHaltIn fails where diags hold a halt.
func noHaltIn(t *testing.T, diags []aggrega.Diagnostic) {
	t.Helper()
	for _, d := range diags {
		if d.Severity == aggrega.SeverityHalt {
			t.Fatalf("halted: %+v", diags)
		}
	}
}

// TestDraftResourceAndImportDraftOneSequence drafts one type and its inputs
// through Import and through DraftResource: the Component and the Recipe, and
// the files beside it, are byte-identical, and the writer drafts the Recipe
// from exactly the type and inputs FromWritten builds.
func TestDraftResourceAndImportDraftOneSequence(t *testing.T) {
	w := queueWritten()
	f := writtenFormat(w)
	for _, settings := range []string{"", `{"target_namespace": "acme"}`} {
		in := writtenInput(settings)
		out := Import(in, f, fxParse)
		fxNoHalt(t, out)
		drafted, diags := DraftResource(in, f, w)
		noHaltIn(t, diags)
		if len(drafted.Entities) != 2 || drafted.Entities[0].Entity.Kind != "Component" ||
			drafted.Entities[1].Entity.Kind != "Recipe" {
			t.Fatalf("drafted %+v, want a Component and a Recipe", drafted.Entities)
		}
		got := importer.CatalogFiles(in.Settings, drafted.Entities, []aggrega.ImportDecision{drafted.Decision})
		if !reflect.DeepEqual(got, out.Files) {
			t.Errorf("under %q DraftResource writes\n%+v\nand Import writes\n%+v", settings, got, out.Files)
		}
		if drafted.Name != out.Entities[0].Name || drafted.Recipe != out.Entities[1].Name {
			t.Errorf("DraftResource names %s and %s, Import %s and %s", drafted.Name, drafted.Recipe,
				out.Entities[0].Name, out.Entities[1].Name)
		}
	}

	// With a property a neighbour supplies, the writer is handed the type and
	// the inputs FromWritten builds.
	w.Properties = append(w.Properties, WrittenProperty{Path: []string{"Policy", "TargetArn"}, Inject: "policy_target_arn"})
	captured, handed := fxCapturing(writtenFormat(w))
	_, diags := DraftResource(writtenInput(""), captured, w)
	noHaltIn(t, diags)
	typ, inputs := FromWritten(w)
	if len(*handed) != 1 || !reflect.DeepEqual((*handed)[0].Type, typ) || !reflect.DeepEqual((*handed)[0].Inputs, inputs) {
		t.Fatalf("the writer is handed %+v, want the type %+v and the inputs %+v", *handed, typ, inputs)
	}
}

// TestDraftResourceReportsDraftedFromSource records the draft in an info
// naming the resource, the Component, the Recipe and each written property
// left out, and decides it in draft mode with a swap hint naming the schema
// import of the format and its resource_types setting.
func TestDraftResourceReportsDraftedFromSource(t *testing.T) {
	w := queueWritten()
	w.ID = "ordersQueue"
	w.Properties = append(w.Properties,
		WrittenProperty{Path: []string{"properties", "provisioningState"}, Value: json.RawMessage(`"Succeeded"`)})
	drafted, diags := DraftResource(writtenInput(""), writtenFormat(w), w)
	noHaltIn(t, diags)
	var info []aggrega.Diagnostic
	for _, d := range diags {
		if d.Code == CodeResourceDraftedFromSource {
			info = append(info, d)
		}
	}
	if len(info) != 1 || info[0].Severity != aggrega.SeverityInfo || info[0].SourceRef != "ordersQueue" {
		t.Fatalf("diagnostics %+v, want one info %s on ordersQueue", diags, CodeResourceDraftedFromSource)
	}
	for _, named := range []string{"ordersQueue", fxQueue, "imported/messaging/sample-store-queue",
		"sample-store-queue", "properties.provisioningState"} {
		if !strings.Contains(info[0].Message, named) {
			t.Errorf("the info %q does not name %s", info[0].Message, named)
		}
	}

	d := drafted.Decision
	if d.SourceNode.ID != "ordersQueue" || d.SourceNode.Type != fxQueue {
		t.Errorf("source node %+v", d.SourceNode)
	}
	if d.Chosen == nil || d.Chosen.Target != "imported/messaging/sample-store-queue" || d.Chosen.Mode != "draft" {
		t.Errorf("chosen %+v", d.Chosen)
	}
	for _, named := range []string{"aggrega import --format " + fxFormatName + " <sample-types.json>", "resource_types", fxQueue} {
		if !strings.Contains(d.SwapHint, named) {
			t.Errorf("the swap hint %q does not name %s", d.SwapHint, named)
		}
	}
	for _, named := range []string{"1 identity key in values", "1 placement key in infra",
		"2 overrides under infra.overrides", "AuthToken", "properties.provisioningState"} {
		if !strings.Contains(d.Rationale, named) {
			t.Errorf("the rationale %q does not say %s", d.Rationale, named)
		}
	}
	for _, e := range drafted.Entities {
		if e.Entity.SourceRef != "ordersQueue" || e.Entity.DecisionRef != "decision:ordersQueue" {
			t.Errorf("%s %s refers to %s, %s", e.Entity.Kind, e.Entity.Name, e.Entity.SourceRef, e.Entity.DecisionRef)
		}
	}
}

// TestDraftResourceTwoRunsEqual drafts one resource twice to the same bytes.
func TestDraftResourceTwoRunsEqual(t *testing.T) {
	w := appWritten()
	f := writtenFormat(w)
	a, da := DraftResource(writtenInput(""), f, w)
	b, db := DraftResource(writtenInput(""), f, appWritten())
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(da, db) {
		t.Errorf("two runs differ:\n%+v\n%+v", a, b)
	}
}

// TestDraftResourceHaltDraftsNothing returns the writer's halt and nothing
// drafted.
func TestDraftResourceHaltDraftsNothing(t *testing.T) {
	w := queueWritten()
	f := writtenFormat(w)
	halt := aggrega.Diagnostic{Severity: aggrega.SeverityHalt, Code: CodeResourceTypeRefused, Message: "refused"}
	f.Write = func(Resource) (importer.RecipeDraft, []aggrega.Diagnostic) {
		return importer.RecipeDraft{}, []aggrega.Diagnostic{halt}
	}
	drafted, diags := DraftResource(writtenInput(""), f, w)
	if !reflect.DeepEqual(drafted, Drafted{}) || !reflect.DeepEqual(diags, []aggrega.Diagnostic{halt}) {
		t.Errorf("a halted writer drafts %+v with %+v", drafted, diags)
	}
}

// TestNameForIsTheNameImportAndDraftResourceGive names a type as Import and
// DraftResource name its Component: under the target namespace, in the
// category the format guesses or the fallback segment.
func TestNameForIsTheNameImportAndDraftResourceGive(t *testing.T) {
	f := fxFormat()
	for _, c := range []struct{ settings, typ, want string }{
		{"", fxQueue, "imported/messaging/sample-store-queue"},
		{`{"target_namespace": "acme"}`, fxQueue, "acme/messaging/sample-store-queue"},
		{"", fxBucket, "imported/component/sample-store-bucket"},
	} {
		in := writtenInput(c.settings)
		if got := NameFor(in, f, c.typ); got != c.want {
			t.Errorf("NameFor(%s, %s) = %q, want %q", c.settings, c.typ, got, c.want)
		}
	}

	w := queueWritten()
	in := writtenInput(`{"target_namespace": "acme"}`)
	drafted, diags := DraftResource(in, writtenFormat(w), w)
	noHaltIn(t, diags)
	if want := NameFor(in, f, fxQueue); drafted.Name != want {
		t.Errorf("DraftResource names %s, NameFor %s", drafted.Name, want)
	}
	out := Import(in, writtenFormat(w), fxParse)
	fxNoHalt(t, out)
	if want := NameFor(in, f, fxQueue); out.Entities[0].Name != want {
		t.Errorf("Import names %s, NameFor %s", out.Entities[0].Name, want)
	}
}
