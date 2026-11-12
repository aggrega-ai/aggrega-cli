// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composeimport

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// TestTheDraftIsNamedAfterTheProject imports Compose sources under each way the
// draft can be named, one row each, and reads the name off the drafted Aggregate
// and Release entities and off the metadata.name of both files: the project's
// top-level name; an override file's name over the Compose file's, as Compose
// merges it; the file's stem where no project names itself; the directory's
// name, as Compose names a project after the directory holding it, where a
// directory's project names nothing or its projects name themselves apart, and
// the project's own name over the directory's; flow for a directory the request
// names nothing for whose projects name themselves apart; and the namespace the
// target_namespace setting names in front. A project's name, read to name the
// draft, is not listed among the top-level members the draft does not carry.
//
// 🔴 PLANTED DEFECT: in read (services.go), drop the assignment to p.name: every
// row reading a name off the source reddens.
func TestTheDraftIsNamedAfterTheProject(t *testing.T) {
	t.Parallel()
	named := func(name string) []byte {
		return []byte("name: " + name + "\nservices:\n  web:\n    image: nginx:1.27\nvolumes:\n  data: {}\n")
	}
	unnamed := []byte("services:\n  web:\n    image: nginx:1.27\n")
	for _, row := range []struct {
		scenario string
		in       aggrega.ImportInput
		want     string
	}{
		{"the project's name", aggrega.ImportInput{Format: Format, Bytes: named("Notes App"), Name: "compose.yaml"},
			"imported/notes-app"},
		{"the override file's name", aggrega.ImportInput{Format: Format, Sources: []aggrega.ImportSource{
			{Name: "compose.override.yaml", Bytes: named("staging")}, {Name: "compose.yaml", Bytes: named("notes")}}},
			"imported/staging"},
		{"no name, the file's stem", aggrega.ImportInput{Format: Format, Bytes: unnamed, Name: "docker-compose.yml"},
			"imported/docker-compose"},
		{"two projects named apart, a directory naming nothing", aggrega.ImportInput{Format: Format,
			Sources: []aggrega.ImportSource{
				{Name: "a/compose.yaml", Bytes: named("first")}, {Name: "b/compose.yaml", Bytes: named("second")}}},
			"imported/flow"},
		{"no name, the directory's name", aggrega.ImportInput{Format: Format, Name: "shop",
			Sources: []aggrega.ImportSource{{Name: "compose.yaml", Bytes: unnamed}}}, "imported/shop"},
		{"the project's name over the directory's", aggrega.ImportInput{Format: Format, Name: "shop",
			Sources: []aggrega.ImportSource{{Name: "compose.yaml", Bytes: named("notes")}}}, "imported/notes"},
		{"two projects named apart, the directory's name", aggrega.ImportInput{Format: Format, Name: "shop",
			Sources: []aggrega.ImportSource{
				{Name: "a/compose.yaml", Bytes: named("first")}, {Name: "b/compose.yaml", Bytes: named("second")}}},
			"imported/shop"},
		{"the target namespace", aggrega.ImportInput{Format: Format, Bytes: named("notes"),
			Settings: json.RawMessage(`{"target_namespace":"team-a"}`)}, "team-a/notes"},
	} {
		out := Import(row.in, hostParse)
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
			if f.Path != "aggregate.yaml" && f.Path != "release.yaml" {
				continue
			}
			if !strings.Contains(f.Content, "\nmetadata:\n  name: "+row.want+"\n") {
				t.Errorf("%s: %s does not name the draft %s:\n%s", row.scenario, f.Path, row.want, f.Content)
			}
			for _, line := range strings.Split(f.Content, "\n") {
				if strings.Contains(line, "Not carried, since the draft reads the services and the models alone") &&
					strings.Contains(line, "name") {
					t.Errorf("%s: %s lists the project's name as not carried: %q", row.scenario, f.Path, line)
				}
			}
		}
	}
}
