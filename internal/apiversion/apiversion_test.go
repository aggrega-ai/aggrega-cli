// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package apiversion_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/apiversion"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// TestThePluginKitDraftsTheVersionTheSchemasDeclare drafts through the plugin
// kit's importer and holds every document it drafts to the generated constant.
//
// The kit is a module of its own and reads no schema, so it carries its own copy
// of the version; nothing else ties that copy to the schemas. A move to a new
// API version that regenerates the constant and leaves the kit's copy behind
// would ship an importer kit whose every draft this compiler refuses — this is
// the test that fails instead.
//
// One source node with no catalog to match is drafted as a Component of its own,
// and the Aggregate wiring it is drafted after it, so both of the kit's node-layout
// writers are read; the case fails if either draft is missing, so it cannot pass
// over an import that drafted nothing.
func TestThePluginKitDraftsTheVersionTheSchemasDeclare(t *testing.T) {
	t.Parallel()

	served := importer.Format{
		Name:   "probe",
		Syntax: "json",
		Walk: func(json.RawMessage) ([]importer.Node, []importer.Edge) {
			return []importer.Node{{ID: "step", Type: "probe/step", Fields: []string{"setting"}}}, nil
		},
	}
	parse := func(string, []byte) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }
	out := importer.Import(aggrega.ImportInput{Format: served.Name, Bytes: []byte(`{}`)}, served, parse)

	want := "apiVersion: " + apiversion.APIVersion
	drafted := map[string]bool{}
	for _, e := range out.Entities {
		if e.Body == "" {
			continue
		}
		drafted[e.Kind] = true
		if first, _, _ := strings.Cut(e.Body, "\n"); first != want {
			t.Errorf("the kit drafts the %s %q opening with %q; want %q", e.Kind, e.Name, first, want)
		}
	}
	for _, kind := range []string{"Component", "Aggregate"} {
		if !drafted[kind] {
			t.Errorf("the kit drafted no %s, so its writer was not read; drafted %v, diagnostics %v",
				kind, drafted, out.Diagnostics)
		}
	}
}
