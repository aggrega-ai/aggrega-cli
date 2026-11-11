// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package langgraphimport

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// TestTheDraftIsNamedAfterTheGraph imports LangGraph configurations under each
// way the draft can be named, one row each, and reads the name off the drafted
// Aggregate and Release entities and off the metadata.name of both files: a
// configuration gives itself no name, so one declaring a single graph names the
// draft after that graph's id; one declaring two falls to the file's stem; a
// directory handing no file name falls to flow; and the namespace the
// target_namespace setting names comes first.
//
// 🔴 PLANTED DEFECT: in Import (langgraphimport.go), hand importer.DraftName no
// title: the single-graph rows redden, each naming the file's stem or flow.
func TestTheDraftIsNamedAfterTheGraph(t *testing.T) {
	t.Parallel()
	one := []byte(`{"graphs": {"support": "./agent.py:graph"}}`)
	two := []byte(`{"graphs": {"support": "./agent.py:graph", "triage": "./triage.py:graph"}}`)
	for _, row := range []struct {
		scenario string
		in       aggrega.ImportInput
		want     string
	}{
		{"one graph, its id", aggrega.ImportInput{Format: Format, Bytes: one, Name: ConfigFile}, "imported/support"},
		{"one graph, from a directory", aggrega.ImportInput{Format: Format,
			Sources: []aggrega.ImportSource{{Name: ConfigFile, Bytes: one}}}, "imported/support"},
		{"two graphs, the file's stem", aggrega.ImportInput{Format: Format, Bytes: two, Name: ConfigFile},
			"imported/langgraph"},
		{"two graphs, from a directory", aggrega.ImportInput{Format: Format,
			Sources: []aggrega.ImportSource{{Name: ConfigFile, Bytes: two}}}, "imported/flow"},
		{"the target namespace", aggrega.ImportInput{Format: Format, Bytes: one,
			Settings: json.RawMessage(`{"target_namespace":"team-a"}`)}, "team-a/support"},
	} {
		row.in.Catalog = catalog
		out := Import(row.in, hostParse(t))
		var got []string
		for _, e := range out.Entities {
			if e.Kind == "Aggregate" || e.Kind == "Release" {
				got = append(got, e.Kind+" "+e.Name)
			}
		}
		if want := []string{"Aggregate " + row.want, "Release " + row.want}; !slices.Equal(got, want) {
			t.Errorf("%s: the import drafted %q, want %q", row.scenario, got, want)
		}
		for _, f := range out.Files {
			if (f.Path == "aggregate.yaml" || f.Path == "release.yaml") &&
				!strings.Contains(f.Content, "\nmetadata:\n  name: "+row.want+"\n") {
				t.Errorf("%s: %s does not name the draft %s:\n%s", row.scenario, f.Path, row.want, f.Content)
			}
		}
	}
}
