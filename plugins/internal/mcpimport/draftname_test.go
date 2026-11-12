// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcpimport

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk-go/pkg/aggrega"
)

// TestTheDraftIsNamedAfterTheSource imports MCP sources under each way the draft
// can be named, one row each, and reads the name off the drafted Aggregate and
// Release entities and off the metadata.name of both files: a registry server
// description names the draft after the server's name, the identifier the
// registry lists it under rather than its title; a client's configuration gives
// itself no name, so it falls to the file's stem, and to flow where no file is
// named; descriptions naming two servers fall to the file's stem too; and the
// namespace the target_namespace setting names comes first.
//
// 🔴 PLANTED DEFECT: in registryServer (servers.go), drop the assignment to
// s.title: the description rows redden, each naming the file's stem or flow.
func TestTheDraftIsNamedAfterTheSource(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		scenario string
		in       aggrega.ImportInput
		want     string
	}{
		{"a registry description, the server's name", aggrega.ImportInput{Format: Format,
			Bytes: fixture(t, "registry-npm.server.json"), Name: "server.json"}, "imported/io-github-example-weather"},
		{"a client's configuration, the file's stem", aggrega.ImportInput{Format: Format,
			Bytes: fixture(t, "claude_desktop_config.json"), Name: "claude_desktop_config.json"},
			"imported/claude-desktop-config"},
		{"a client's configuration named as a dotfile", aggrega.ImportInput{Format: Format,
			Bytes: fixture(t, "claude-code.mcp.json"), Name: ".mcp.json"}, "imported/mcp"},
		{"a client's configuration, from a directory", aggrega.ImportInput{Format: Format,
			Sources: []aggrega.ImportSource{{Name: ".mcp.json", Bytes: fixture(t, "claude-code.mcp.json")}}},
			"imported/flow"},
		{"two descriptions naming two servers", aggrega.ImportInput{Format: Format, Sources: []aggrega.ImportSource{
			{Name: "a.server.json", Bytes: fixture(t, "registry-npm.server.json")},
			{Name: "b.server.json", Bytes: fixture(t, "registry-oci.server.json")}}}, "imported/flow"},
		{"the target namespace", aggrega.ImportInput{Format: Format, Bytes: fixture(t, "registry-npm.server.json"),
			Settings: json.RawMessage(`{"target_namespace":"team-a"}`)}, "team-a/io-github-example-weather"},
	} {
		out := Import(row.in, hostParse)
		var got []string
		for _, e := range out.Entities {
			if e.Kind == "Aggregate" || e.Kind == "Release" {
				got = append(got, e.Kind+" "+e.Name)
			}
		}
		if want := []string{"Aggregate " + row.want, "Release " + row.want}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the import drafted %q, want %q (diagnostics %+v)", row.scenario, got, want, out.Diagnostics)
		}
		for _, f := range out.Files {
			if !strings.Contains(f.Content, "\nmetadata:\n  name: "+row.want+"\n") {
				t.Errorf("%s: %s does not name the draft %s:\n%s", row.scenario, f.Path, row.want, f.Content)
			}
		}
	}
}
