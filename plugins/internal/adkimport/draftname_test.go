// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package adkimport

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// TestTheDraftIsNamedAfterTheApplication imports ADK applications under each
// way the draft can be named, one row each, and reads the name off the drafted
// Aggregate and Release entities and off the metadata.name of both files: the
// name the root agent's configuration declares; two applications whose root
// agents are named apart fall to flow, a directory naming no file; and the
// namespace the target_namespace setting names comes first.
//
// 🔴 PLANTED DEFECT: in Import (adkimport.go), hand importer.DraftName no title:
// the root-agent rows redden, each naming the file's stem or flow.
func TestTheDraftIsNamedAfterTheApplication(t *testing.T) {
	t.Parallel()
	root := func(name string) []byte {
		return []byte("name: " + name + "\nmodel: gemini-2.5-flash\ninstruction: Answer the question.\n")
	}
	for _, row := range []struct {
		scenario string
		in       aggrega.ImportInput
		want     string
	}{
		{"the root agent's name", aggrega.ImportInput{Format: Format, Bytes: root("support_agent"), Name: RootFile},
			"imported/support-agent"},
		{"from a directory", aggrega.ImportInput{Format: Format,
			Sources: []aggrega.ImportSource{{Name: "support/" + RootFile, Bytes: root("support_agent")}}},
			"imported/support-agent"},
		{"two applications named apart", aggrega.ImportInput{Format: Format, Sources: []aggrega.ImportSource{
			{Name: "a/" + RootFile, Bytes: root("first")}, {Name: "b/" + RootFile, Bytes: root("second")}}},
			"imported/flow"},
		{"the target namespace", aggrega.ImportInput{Format: Format, Bytes: root("support_agent"),
			Settings: json.RawMessage(`{"target_namespace":"team-a"}`)}, "team-a/support-agent"},
	} {
		out := Import(row.in, hostParse)
		var got []string
		for _, e := range out.Entities {
			if e.Kind == "Aggregate" || e.Kind == "Release" {
				got = append(got, e.Kind+" "+e.Name)
			}
		}
		if want := []string{"Aggregate " + row.want, "Release " + row.want}; !slices.Equal(got, want) {
			t.Errorf("%s: the import drafted %q, want %q (diagnostics %+v)", row.scenario, got, want, out.Diagnostics)
		}
		for _, f := range out.Files {
			if (f.Path == "aggregate.yaml" || f.Path == "release.yaml") &&
				!strings.Contains(f.Content, "\nmetadata:\n  name: "+row.want+"\n") {
				t.Errorf("%s: %s does not name the draft %s:\n%s", row.scenario, f.Path, row.want, f.Content)
			}
		}
	}
}
