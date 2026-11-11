// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package crewaiimport

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// TestTheDraftIsNamedAfterTheCrew imports CrewAI projects under each way the
// draft can be named, one row each, and reads the name off the drafted Aggregate
// and Release entities and off the metadata.name of both files: the name a
// crew.jsonc gives its crew, also from a named directory; a classic project's
// files give it none — its program does — so a classic project falls to the
// file's stem, to the directory's name from a directory, and to flow from a
// directory the request names nothing for; and the namespace the
// target_namespace setting names comes first.
// The crew's name, read to name the draft, is not listed among the members of
// crew.jsonc the draft does not carry.
//
// 🔴 PLANTED DEFECT: in readCrew (crews.go), drop the assignment to c.name: the
// JSON-first rows redden, each naming flow.
func TestTheDraftIsNamedAfterTheCrew(t *testing.T) {
	t.Parallel()
	crew := []byte(`{"name": "Release Notes Crew", "agents": ["researcher"], "process": "sequential"}`)
	agent := []byte(`{"role": "Researcher", "goal": "Find the changes", "backstory": "Reads pull requests."}`)
	agents := []byte("researcher:\n  role: Researcher\n  goal: Find the changes\n  backstory: Reads pull requests.\n")
	jsonFirst := []aggrega.ImportSource{{Name: "crew.jsonc", Bytes: crew}, {Name: "agents/researcher.jsonc", Bytes: agent}}
	for _, row := range []struct {
		scenario string
		in       aggrega.ImportInput
		want     string
	}{
		{"the crew's name", aggrega.ImportInput{Format: Format, Sources: jsonFirst}, "imported/release-notes-crew"},
		{"a classic project's file, its stem", aggrega.ImportInput{Format: Format, Bytes: agents, Name: "agents.yaml"},
			"imported/agents"},
		{"a classic project, from a directory naming nothing", aggrega.ImportInput{Format: Format,
			Sources: []aggrega.ImportSource{{Name: "src/brief/config/agents.yaml", Bytes: agents}}}, "imported/flow"},
		{"a classic project, from a named directory", aggrega.ImportInput{Format: Format, Name: "support_brief",
			Sources: []aggrega.ImportSource{{Name: "config/agents.yaml", Bytes: agents}}}, "imported/support-brief"},
		{"the crew's name, from a named directory", aggrega.ImportInput{Format: Format, Name: "support_brief",
			Sources: jsonFirst}, "imported/release-notes-crew"},
		{"the target namespace", aggrega.ImportInput{Format: Format, Sources: jsonFirst,
			Settings: json.RawMessage(`{"target_namespace":"team-a"}`)}, "team-a/release-notes-crew"},
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
			if f.Path != "aggregate.yaml" && f.Path != "release.yaml" {
				continue
			}
			if !strings.Contains(f.Content, "\nmetadata:\n  name: "+row.want+"\n") {
				t.Errorf("%s: %s does not name the draft %s:\n%s", row.scenario, f.Path, row.want, f.Content)
			}
			for _, line := range strings.Split(f.Content, "\n") {
				if strings.Contains(line, "Not carried, since the draft reads a crew's agents and tasks alone") &&
					strings.Contains(line, "name") {
					t.Errorf("%s: %s lists the crew's name as not carried: %q", row.scenario, f.Path, line)
				}
			}
		}
	}
}
