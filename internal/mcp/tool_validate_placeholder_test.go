// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
)

// draftThroughTheTool asks the scaffold tool for a drafted document and writes
// it into root, returning the path — the first two steps of the authoring loop
// an assistant runs.
func draftThroughTheTool(t *testing.T, s *server, root, kind, name, file string, edits ...string) string {
	t.Helper()
	res := callTool(t, s, "scaffold", scaffoldArgs{Kind: kind, Name: name})
	if res.IsError {
		t.Fatalf("the scaffold tool refused %q: %s", kind, res.Content[0].Text)
	}
	var drafted scaffoldResult
	if err := json.Unmarshal(res.StructuredContent, &drafted); err != nil {
		t.Fatalf("decode the scaffold payload: %v", err)
	}
	doc := drafted.Skeleton
	for i := 0; i+1 < len(edits); i += 2 {
		if !strings.Contains(doc, edits[i]) {
			t.Fatalf("the drafted %s carries no %q:\n%s", kind, edits[i], doc)
		}
		doc = strings.Replace(doc, edits[i], edits[i+1], 1)
	}
	path := filepath.Join(root, file)
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatalf("write the draft: %v", err)
	}
	return path
}

// validateThroughTheTool runs the validate tool over one entity and holds its
// payload to the single-entity core's own verdict on the same file, so what the
// assertions read is the core's refusal carried verbatim rather than a reading
// of the tool's own.
func validateThroughTheTool(t *testing.T, s *server, root, path string) (validateResultPayload, string) {
	t.Helper()
	tw := resolvedToolWorkspace(t, s, root, nil)
	_, coreErr := validateops.ValidateEntity(context.Background(), tw.root, tw.cacheDir, tw.dirs,
		resolvedEntityPath(t, tw, path), false)
	res := callTool(t, s, "validate", validateArgs{Entity: path, WorkspaceRoot: root})
	payload := unmarshalValidatePayload(t, res)
	assertHaltParity(t, payload, coreErr)
	return payload, res.Content[0].Text
}

// TestTheValidateToolNamesEveryPlaceholderADraftCarries drives the loop the
// tool descriptions teach an assistant: draft a FeatureRecipe with the scaffold
// tool, then validate it. As drafted, and with only its version filled, the
// answer names every placeholder still owed and what each takes, in the
// structured diagnostic and in the text alike; filled, it is valid.
func TestTheValidateToolNamesEveryPlaceholderADraftCarries(t *testing.T) {
	s := newTestServer(t)
	root := t.TempDir()
	refs := []string{
		"7:3: spec.capability takes what its schema describes: The explicit provider capability this recipe binds to",
		"8:3: spec.implements takes what its schema describes: Declares which catalog Feature type this recipe is able to implement",
	}

	cases := []struct {
		name  string
		edits []string
		want  []string
	}{
		{name: "as drafted", want: append([]string{"3 placeholders still owed", "5:3: metadata.version takes a semantic version, such as 1.0.0"}, refs...)},
		{name: "with only its version filled", edits: []string{`  version: ""`, `  version: "1.0.0"`}, want: append([]string{"2 placeholders still owed"}, refs...)},
	}
	for i, tc := range cases {
		path := draftThroughTheTool(t, s, root, "featurerecipe", "my-feature-recipe", "refused-"+string(rune('a'+i))+".yaml", tc.edits...)
		payload, text := validateThroughTheTool(t, s, root, path)
		if payload.Valid || len(payload.Diagnostics) != 1 {
			t.Fatalf("%s: want one blocking diagnostic, got %+v", tc.name, payload)
		}
		for _, line := range tc.want {
			if !strings.Contains(payload.Diagnostics[0], line) {
				t.Errorf("%s: the diagnostic does not say %q:\n%s", tc.name, line, payload.Diagnostics[0])
			}
			if !strings.Contains(text, line) {
				t.Errorf("%s: the text half does not say %q:\n%s", tc.name, line, text)
			}
		}
		if strings.Contains(payload.Diagnostics[0], "does not match regular expression") {
			t.Errorf("%s: the schema's rendering of an empty value still stands in for the placeholder line:\n%s",
				tc.name, payload.Diagnostics[0])
		}
	}

	filled := draftThroughTheTool(t, s, root, "featurerecipe", "my-feature-recipe", "filled.yaml",
		`  version: ""`, `  version: "1.0.0"`,
		`  capability: ""`, `  capability: "helm"`,
		`  implements: ""`, `  implements: "example/observability"`)
	if payload, text := validateThroughTheTool(t, s, root, filled); !payload.Valid {
		t.Errorf("the filled draft is still refused:\n%s", text)
	}
}

// TestTheValidateToolRefusesAnAggregateNamingTheScopesThatCheckIt hands the
// entity scope a drafted Aggregate, which it does not take, and reads that the
// answer names the kinds it does take and the two scopes that check an
// Aggregate.
func TestTheValidateToolRefusesAnAggregateNamingTheScopesThatCheckIt(t *testing.T) {
	s := newTestServer(t)
	root := t.TempDir()
	path := draftThroughTheTool(t, s, root, "aggregate", "my-app", "aggregate.yaml")

	payload, text := validateThroughTheTool(t, s, root, path)
	if payload.Valid || len(payload.Diagnostics) != 1 {
		t.Fatalf("want one blocking diagnostic, got %+v", payload)
	}
	for _, want := range []string{
		"the kinds this scope accepts are Component, Feature, Recipe, FeatureRecipe, Workload and Plugin",
		"An Aggregate is checked by the whole-catalog scope when it sits in a catalog directory",
		"and by the Release scope over a Release that deploys it",
	} {
		if !strings.Contains(payload.Diagnostics[0], want) || !strings.Contains(text, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, text)
		}
	}
}
