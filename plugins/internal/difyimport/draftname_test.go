// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package difyimport

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// TestTheDraftIsNamedAfterTheApp imports Dify applications under each
// way the draft can be named, one row each, and reads the name off the drafted
// Aggregate entity and off the metadata.name aggregate.yaml writes: the name the
// DSL gives the application at app.name; an application naming none falls to
// the file's stem; two applications named apart fall to flow, a directory naming
// no file; and the namespace the target_namespace setting names comes first.
//
// 🔴 PLANTED DEFECT: drop the Title of Dify (difyimport.go): the rows reading
// app.name redden, each naming the file's stem or flow.
func TestTheDraftIsNamedAfterTheApp(t *testing.T) {
	t.Parallel()
	chat, workflow := fixture(t, "authored/chat.yml"), fixture(t, "authored/workflow.yml")
	nameless := []byte(strings.Replace(string(workflow), "  name: Support Answers\n", "", 1))
	if string(nameless) == string(workflow) {
		t.Fatalf("the workflow fixture no longer names its application Support Answers; the nameless row tests nothing")
	}
	for _, row := range []struct {
		scenario string
		in       aggrega.ImportInput
		want     string
	}{
		{"the application's name", aggrega.ImportInput{Format: Format, Bytes: workflow, Name: "workflow.yml"},
			"imported/support-answers"},
		{"no name, the file's stem", aggrega.ImportInput{Format: Format, Bytes: nameless, Name: "support-flow.yml"},
			"imported/support-flow"},
		{"two applications named apart", aggrega.ImportInput{Format: Format, Sources: []aggrega.ImportSource{
			{Name: "chat.yml", Bytes: chat}, {Name: "workflow.yml", Bytes: workflow}}}, "imported/flow"},
		{"the target namespace", aggrega.ImportInput{Format: Format, Bytes: workflow,
			Settings: json.RawMessage(`{"target_namespace":"team-a"}`)}, "team-a/support-answers"},
	} {
		row.in.Catalog = catalog
		out := Import(row.in, hostParse)
		var got []string
		for _, e := range out.Entities {
			if e.Kind == "Aggregate" && e.Mode == "draft" {
				got = append(got, e.Name)
			}
		}
		if want := []string{row.want}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the import drafted %q, want %q (diagnostics %+v)", row.scenario, got, want, out.Diagnostics)
		}
		for _, f := range out.Files {
			if f.Path == "aggregate.yaml" && !strings.Contains(f.Content, "\nmetadata:\n  name: "+row.want+"\n") {
				t.Errorf("%s: aggregate.yaml does not name the draft %s:\n%s", row.scenario, row.want, f.Content)
			}
		}
	}
}
