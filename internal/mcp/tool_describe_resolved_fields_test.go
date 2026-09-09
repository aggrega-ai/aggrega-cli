// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// What the describe payload carries of the resolution, asserted against the
// resolution itself.
//
// The oracle is a direct call to the shared by-name resolution over the same
// view, so these prove the payload carries what that resolution decided. An
// expectation read back off this tool's own payload would compare a value with
// itself, and a field the payload stopped carrying would be missing from both
// sides with nothing to say a fact had stopped being reported.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
)

// describeOracle resolves name through the shared by-name resolution over the
// same request-scoped view the tool builds, independently of the tool call.
func describeOracle(t *testing.T, s *server, root, catalogDir, name string) compilerops.Entity {
	t.Helper()
	tw, err := s.resolveToolWorkspace(root, []string{catalogDir})
	if err != nil {
		t.Fatalf("resolve the oracle workspace: %v", err)
	}
	e, ok := compilerops.ResolveEntity(tw.newView(nil), name)
	if !ok {
		t.Fatalf("the oracle resolved nothing for %q, so nothing can be asserted about it", name)
	}
	return e
}

// TestDescribeCarriesEveryFactTheResolutionDecided pins the description, the
// publishing scope and the retained authored document onto the structured
// payload, with every expectation read off the resolution's own result.
//
// The rows differ in which facts they carry — one kind carries a description and
// a scope and no authored document, another an authored document and neither of
// the first two, a third none of them — so a payload filling these fields with
// constants fails on some row whatever constant it chose.
func TestDescribeCarriesEveryFactTheResolutionDecided(t *testing.T) {
	s := newTestServer(t)
	root, catalogDir, _ := fullstackWorkspace(t)

	names := []string{"provider/helm", "db-module", "core/compute/api-server"}

	var described, scoped, sourced, bare int
	for _, name := range names {
		want := describeOracle(t, s, root, catalogDir, name)
		switch {
		case want.Description != "":
			described++
		case want.Source != "":
			sourced++
		default:
			bare++
		}
		if want.Scope != "" {
			scoped++
		}

		t.Run(name, func(t *testing.T) {
			res := callTool(t, s, "describe", describeArgs{
				Name: name, WorkspaceRoot: root, CatalogDirs: []string{catalogDir},
			})
			if res.IsError {
				t.Fatalf("describe %q reported an error: %s", name, res.Content[0].Text)
			}
			var got describeEntityPayload
			if err := json.Unmarshal(res.StructuredContent, &got); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}

			if got.Description != want.Description {
				t.Errorf("the payload describes %q as %q; the resolution decided %q",
					name, got.Description, want.Description)
			}
			if got.PublishScope != want.Scope {
				t.Errorf("the payload scopes %q by %q; the resolution decided %q",
					name, got.PublishScope, want.Scope)
			}
			if got.AuthoredPath != want.Source {
				t.Errorf("the payload names authored document %q for %q; the resolution retained %q",
					got.AuthoredPath, name, want.Source)
			}
			if got.AuthoredLength != len(want.Bytes) {
				t.Errorf("the payload reports a %d-byte authored document for %q; the resolution retained %d",
					got.AuthoredLength, name, len(want.Bytes))
			}
			// The summary line is written for a reader and states less than the
			// payload, but what it does state comes from the same field.
			if strings.TrimSpace(want.Description) != "" &&
				!strings.Contains(res.Content[0].Text, want.Description) {
				t.Errorf("the summary line drops the description; got: %s", res.Content[0].Text)
			}
		})
	}

	if described == 0 || scoped == 0 || sourced == 0 || bare == 0 {
		t.Fatalf("the table carries %d described, %d scoped, %d sourced and %d bare entity(ies); "+
			"it needs all four to discriminate a constant-filling payload",
			described, scoped, sourced, bare)
	}
}

// TestDescribeCarriesAnAuthoredDocumentByReferenceNotByValue pins the deliberate
// omission: the retained document is reported by its path and its length, and
// its bytes are not carried into the payload.
//
// The definition already answers what the entity IS. A second copy of the one
// file that declared it would spend the caller's payload on a document the path
// lets it read for itself.
func TestDescribeCarriesAnAuthoredDocumentByReferenceNotByValue(t *testing.T) {
	s := newTestServer(t)
	root, catalogDir, _ := fullstackWorkspace(t)

	want := describeOracle(t, s, root, catalogDir, "db-module")
	if want.Source == "" || len(want.Bytes) == 0 {
		t.Fatalf("precondition: the resolution retained no authored document (source %q, %d byte(s)), "+
			"so an omission here asserts nothing", want.Source, len(want.Bytes))
	}

	res := callTool(t, s, "describe", describeArgs{
		Name: "db-module", WorkspaceRoot: root, CatalogDirs: []string{catalogDir},
	})
	if res.IsError {
		t.Fatalf("describe db-module reported an error: %s", res.Content[0].Text)
	}
	var got describeEntityPayload
	if err := json.Unmarshal(res.StructuredContent, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if got.AuthoredPath != want.Source {
		t.Errorf("the payload names authored document %q; the resolution retained %q", got.AuthoredPath, want.Source)
	}
	if got.AuthoredLength != len(want.Bytes) {
		t.Errorf("the payload reports %d byte(s); the resolution retained %d", got.AuthoredLength, len(want.Bytes))
	}
	if !filepath.IsAbs(got.AuthoredPath) && !strings.HasSuffix(got.AuthoredPath, ".yaml") {
		t.Errorf("the reported authored document %q is not a document path", got.AuthoredPath)
	}
	if strings.Contains(string(res.StructuredContent), "public_db: main-db") {
		t.Error("the payload carries the authored document's own text, so it spends the caller's " +
			"payload on a document the path lets it read")
	}
}
