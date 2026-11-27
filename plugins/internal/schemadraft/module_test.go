// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

const queueModuleURL = "oci://registry.example/modules/queue"

// queueModule is a module realizing the sample queue, setting one override and
// one parameter a neighbour supplies, and reading one output.
func queueModule(version string) WrappedModule {
	prop := func(name string) *Property {
		return &Property{Name: name, Path: []string{name}, Kinds: []Kind{KindString}, Written: true}
	}
	return WrappedModule{
		Type: &Type{Name: fxQueue}, SourceKind: "sample-module", URL: queueModuleURL, Version: version,
		Inputs: []Input{
			{Property: prop("delaySeconds"), Source: InputSource{Pillar: PillarOverrides, Key: "delaySeconds"}},
			{Property: prop("topicArn"), Required: true,
				Source: InputSource{Pillar: PillarInject, Key: "topic_arn", Mechanism: MechanismProperty}},
		},
		Fixed:   []importer.Entry{{Key: "telemetry", Value: "false"}},
		Outputs: []Provide{{Key: "url", Attribute: []string{"queueUrl"}}},
	}
}

// moduleWriter is a ModuleWriter writing the source as a block and recording
// the module it is handed.
func moduleWriter(got *[]WrappedModule) ModuleWriter {
	return func(m WrappedModule) (importer.RecipeDraft, []aggrega.Diagnostic) {
		*got = append(*got, m)
		return importer.RecipeDraft{Source: "kind: " + m.SourceKind + "\nurl: " + m.URL + "\n",
			ValuesMapping: "x: 1\n"}, nil
	}
}

// TestDraftWrappedNamesAsDraftResource names the Component NameFor of the type,
// under the namespace the settings give, and the Recipe after its last segment,
// on the format's capability, with the module written under spec.source.
func TestDraftWrappedNamesAsDraftResource(t *testing.T) {
	f := fxFormat()
	for _, settings := range []string{"", `{"target_namespace": "acme"}`} {
		in := writtenInput(settings)
		var handed []WrappedModule
		drafted, diags := DraftWrapped(in, f, queueModule("1.2.3"), moduleWriter(&handed))
		noHaltIn(t, diags)
		if want := NameFor(in, f, fxQueue); drafted.Name != want {
			t.Errorf("under %q the Component is %s, want %s", settings, drafted.Name, want)
		}
		if drafted.Recipe != lastSegment(drafted.Name) || drafted.Entities[0].Entity.Kind != "Component" ||
			drafted.Entities[1].Entity.Kind != "Recipe" {
			t.Errorf("drafted %s and %+v", drafted.Recipe, drafted.Entities)
		}
		if drafted.Decision.Chosen == nil || drafted.Decision.Chosen.Target != drafted.Name ||
			drafted.Decision.Chosen.Mode != "draft" {
			t.Errorf("decision chooses %+v", drafted.Decision.Chosen)
		}
		recipe := drafted.Entities[1].Entity.Body
		for _, want := range []string{"  implements: " + drafted.Name + ":0.1.0\n  source:\n    kind: sample-module\n" +
			"    url: " + queueModuleURL + "\n", "sources the published module " + queueModuleURL + " whole",
			"no vendor schema was read"} {
			if !strings.Contains(recipe, want) {
				t.Errorf("the Recipe lacks %q:\n%s", want, recipe)
			}
		}
		if strings.Contains(recipe, "one resource of the type") {
			t.Errorf("the Recipe says it declares a resource:\n%s", recipe)
		}
		if strings.Contains(recipe, "telemetry:") && len(handed[0].Inputs) != 2 {
			t.Errorf("handed %+v", handed)
		}
	}
	// The Component publishes the output's key and nothing it reads.
	var handed []WrappedModule
	drafted, _ := DraftWrapped(writtenInput(""), f, queueModule("1"), moduleWriter(&handed))
	if body := drafted.Entities[0].Entity.Body; !strings.Contains(body, "url:") {
		t.Errorf("the Component does not provide url:\n%s", body)
	}
}

// TestWrappedWithoutVersionWarns leaves the version for the author and warns
// naming the module and the field; with a version it does not warn.
func TestWrappedWithoutVersionWarns(t *testing.T) {
	var handed []WrappedModule
	drafted, diags := DraftWrapped(writtenInput(""), fxFormat(), queueModule(""), moduleWriter(&handed))
	var warn *aggrega.Diagnostic
	for i := range diags {
		if diags[i].Code == CodeModuleVersionUnpinned {
			warn = &diags[i]
		}
	}
	if warn == nil || warn.Severity != aggrega.SeverityWarn || !strings.Contains(warn.Message, queueModuleURL) ||
		!strings.Contains(warn.Message, "spec.source.version") {
		t.Fatalf("diagnostics %+v, want a warn module-version-unpinned naming the module and spec.source.version", diags)
	}
	if len(drafted.Entities) != 2 {
		t.Errorf("no draft is written: %+v", drafted)
	}
	_, diags = DraftWrapped(writtenInput(""), fxFormat(), queueModule("1.0.0"), moduleWriter(&handed))
	for _, d := range diags {
		if d.Code == CodeModuleVersionUnpinned {
			t.Errorf("a pinned module warns: %+v", d)
		}
	}
}

// TestWrappedModuleHaltsOnWhatItLacks halts resource-schema-unreadable, drafting
// nothing and not calling the writer, on a module with no type, address or kind.
func TestWrappedModuleHaltsOnWhatItLacks(t *testing.T) {
	for name, edit := range map[string]func(*WrappedModule){
		"no type": func(m *WrappedModule) { m.Type = nil },
		"no url":  func(m *WrappedModule) { m.URL = "" },
		"no kind": func(m *WrappedModule) { m.SourceKind = "" },
	} {
		m := queueModule("1")
		edit(&m)
		var handed []WrappedModule
		drafted, diags := DraftWrapped(writtenInput(""), fxFormat(), m, moduleWriter(&handed))
		if len(diags) != 1 || diags[0].Code != CodeResourceSchemaUnreadable || diags[0].Severity != aggrega.SeverityHalt {
			t.Errorf("%s: diagnostics %+v", name, diags)
		}
		if !reflect.DeepEqual(drafted, Drafted{}) || len(handed) != 0 {
			t.Errorf("%s: drafted %+v, writer called %d times", name, drafted, len(handed))
		}
	}
}

// TestWrappedDraftIsDeterministicAndFixedWins drafts twice to one byte
// sequence, and leaves out an input a fixed parameter binds.
func TestWrappedDraftIsDeterministicAndFixedWins(t *testing.T) {
	m := queueModule("1")
	m.Inputs = append(m.Inputs, Input{Property: &Property{Name: "telemetry", Path: []string{"telemetry"}, Written: true},
		Source: InputSource{Pillar: PillarOverrides, Key: "telemetry"}})
	var handed []WrappedModule
	a, da := DraftWrapped(writtenInput(""), fxFormat(), m, moduleWriter(&handed))
	b, db := DraftWrapped(writtenInput(""), fxFormat(), m, moduleWriter(&handed))
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(da, db) {
		t.Errorf("two runs differ")
	}
	for _, in := range handed[0].Inputs {
		if in.Source.Key == "telemetry" {
			t.Errorf("the writer is handed the fixed parameter as an input")
		}
	}
	var warns []aggrega.Diagnostic
	for _, d := range da {
		if d.Code == CodeValueNotCarried {
			warns = append(warns, d)
		}
	}
	if len(warns) != 1 || warns[0].Severity != aggrega.SeverityWarn ||
		!strings.Contains(warns[0].Message, queueModuleURL) || !strings.Contains(warns[0].Message, "parameter telemetry ") ||
		!strings.Contains(warns[0].Message, "binds that parameter itself") {
		t.Errorf("diagnostics %+v, want one warn value-not-carried naming the module and telemetry", da)
	}
	if !strings.Contains(a.Decision.Rationale, "telemetry is not carried") {
		t.Errorf("the rationale %q does not mention the superseded input", a.Decision.Rationale)
	}
	if len(m.Inputs) != 3 {
		t.Errorf("the caller's module is changed")
	}
}

// TestWrappedFixedValueOnOneLine halts, drafting nothing, on a fixed parameter
// whose value runs over more than one line.
func TestWrappedFixedValueOnOneLine(t *testing.T) {
	m := queueModule("1")
	m.Fixed = []importer.Entry{{Key: "telemetry", Value: "a\nb"}}
	var handed []WrappedModule
	drafted, diags := DraftWrapped(writtenInput(""), fxFormat(), m, moduleWriter(&handed))
	if len(diags) != 1 || diags[0].Code != CodeResourceSchemaUnreadable || diags[0].Severity != aggrega.SeverityHalt ||
		!strings.Contains(diags[0].Message, "telemetry") || !strings.Contains(diags[0].Message, "more than one line") {
		t.Errorf("diagnostics %+v", diags)
	}
	if !reflect.DeepEqual(drafted, Drafted{}) || len(handed) != 0 {
		t.Errorf("drafted %+v, writer called %d times", drafted, len(handed))
	}
}

// TestWrappedNameOverridesTheSlug names the Component and the Recipe by Name
// where it is set, and by the type's name otherwise.
func TestWrappedNameOverridesTheSlug(t *testing.T) {
	var handed []WrappedModule
	plain, _ := DraftWrapped(writtenInput(""), fxFormat(), queueModule("1"), moduleWriter(&handed))
	m := queueModule("1")
	m.Name = "Jobs"
	named, _ := DraftWrapped(writtenInput(""), fxFormat(), m, moduleWriter(&handed))
	if want := NameFor(writtenInput(""), fxFormat(), fxQueue); plain.Name != want {
		t.Errorf("without Name the Component is %q, want %q", plain.Name, want)
	}
	if want := path.Dir(plain.Name) + "/jobs"; named.Name != want || named.Recipe != "jobs" {
		t.Errorf("with Name the Component is %q and the Recipe %q, want %q and jobs", named.Name, named.Recipe, want)
	}
}

// TestWrappedWithAnAddressAndNoURLIsDrafted drafts a module whose address is
// known and whose URL is not, warning that the url is the author's to write.
func TestWrappedWithAnAddressAndNoURLIsDrafted(t *testing.T) {
	m := queueModule("1")
	m.URL, m.AddressOnly = "", "app.terraform.io/acme/queue/aws"
	var handed []WrappedModule
	drafted, diags := DraftWrapped(writtenInput(""), fxFormat(), m, moduleWriter(&handed))
	if len(drafted.Entities) != 2 || len(handed) != 1 {
		t.Fatalf("drafted %+v, writer called %d times", drafted.Entities, len(handed))
	}
	if len(diags) != 1 || diags[0].Code != CodeLinkedSourceNotRead || diags[0].Severity != aggrega.SeverityWarn ||
		!strings.Contains(diags[0].Message, m.AddressOnly) {
		t.Errorf("diagnostics %+v", diags)
	}
}
