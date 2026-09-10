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
// publishing scope, the document the entity was declared in and the retained
// authored text onto the structured payload, with every expectation read off the
// resolution's own result.
//
// The rows differ in which facts they carry — one kind carries a description and
// a scope and no authored text, another authored text and neither of the first
// two, a third none of the three — so a payload filling these fields with
// constants fails on some row whatever constant it chose.
//
// The document is the one fact every row carries, and it is reported per kind, so
// what discriminates it is not a population split but the value: each row's path
// names that row's own file, and a constant fails every row but one.
func TestDescribeCarriesEveryFactTheResolutionDecided(t *testing.T) {
	s := newTestServer(t)
	root, catalogDir, _ := fullstackWorkspace(t)

	names := []string{"provider/helm", "db-module", "core/compute/api-server"}

	var described, undescribed, scoped, unscoped, texted, untexted int
	files := map[string]string{}
	for _, name := range names {
		want := describeOracle(t, s, root, catalogDir, name)
		if want.Description != "" {
			described++
		} else {
			undescribed++
		}
		if want.Scope != "" {
			scoped++
		} else {
			unscoped++
		}
		if len(want.Bytes) != 0 {
			texted++
		} else {
			untexted++
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
			if got.File != want.Source {
				t.Errorf("the payload declares %q in %q; the resolution resolved it in %q",
					name, got.File, want.Source)
			}
			if got.File == "" {
				t.Errorf("%q was declared in a document under the workspace root and is reported with "+
					"no file at all", name)
			}
			if got.AuthoredLength != len(want.Bytes) {
				t.Errorf("the payload reports %d byte(s) of authored text for %q; the resolution retained %d",
					got.AuthoredLength, name, len(want.Bytes))
			}
			// The summary line is written for a reader and states less than the
			// payload, but what it does state comes from the same field.
			if strings.TrimSpace(want.Description) != "" &&
				!strings.Contains(res.Content[0].Text, want.Description) {
				t.Errorf("the summary line drops the description; got: %s", res.Content[0].Text)
			}
			files[name] = got.File
		})
	}

	// Both polarities of each optional fact, or the table asserts less than it
	// appears to: a payload filling one with a constant passes any table whose
	// rows agree on it.
	if described == 0 || undescribed == 0 || scoped == 0 || unscoped == 0 || texted == 0 || untexted == 0 {
		t.Fatalf("the table carries %d described / %d not, %d scoped / %d not, %d with authored text / "+
			"%d without; it needs both polarities of each to discriminate a constant-filling payload",
			described, undescribed, scoped, unscoped, texted, untexted)
	}
	// The document is reported for every kind, so its discriminator is that the
	// rows disagree: a payload reporting one entity's file for all of them would
	// satisfy every per-row comparison above only if they happened to coincide,
	// and this says they do not.
	seen := map[string]string{}
	for name, file := range files {
		if other, clash := seen[file]; clash {
			t.Errorf("%q and %q are both reported as declared in %q, so the rows cannot discriminate a "+
				"payload reporting one file for every entity", name, other, file)
		}
		seen[file] = name
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

	if got.File != want.Source {
		t.Errorf("the payload names document %q; the resolution resolved it in %q", got.File, want.Source)
	}
	if got.AuthoredLength != len(want.Bytes) {
		t.Errorf("the payload reports %d byte(s); the resolution retained %d", got.AuthoredLength, len(want.Bytes))
	}
	if !filepath.IsAbs(got.File) && !strings.HasSuffix(got.File, ".yaml") {
		t.Errorf("the reported document %q is not a document path", got.File)
	}
	if strings.Contains(string(res.StructuredContent), "public_db: main-db") {
		t.Error("the payload carries the authored document's own text, so it spends the caller's " +
			"payload on a document the path lets it read")
	}
}
