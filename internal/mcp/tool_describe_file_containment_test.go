// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

// How containment decides the reported document's spelling.
//
// A catalog directory need not sit inside the workspace root, and the catalog
// records the path in whatever form it holds — which for an outside directory is
// a climb above the root. The climb is the one form a caller cannot use, because
// it resolves differently depending on where it is read from. Inside the root the
// answer is the workspace-relative name, which is the coordinate the rest of this
// surface is in; outside it the answer is the absolute path, which is what a
// caller can open and what the command line prints for that same file.
//
// The expectation comes from the shared by-name resolution rather than from the
// payload, because a payload that stopped reporting the path would otherwise be
// compared against itself.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
)

// writeToolFixture writes content at path, creating its parent directories.
func writeToolFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create the fixture directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write the fixture %s: %v", path, err)
	}
}

// TestDescribeSpellsTheReportedPathByContainment pins the rule on the path this
// surface reports.
//
// The row whose catalog sits INSIDE the root is the positive control. Without it,
// a payload that reported the absolute path for every input, contained or not,
// would satisfy the outside row and this test would assert nothing.
func TestDescribeSpellsTheReportedPathByContainment(t *testing.T) {
	const aggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: probe-module
  version: "1.0.0"
spec:
  components:
    only:
      type: core/x/y
`

	for _, tc := range []struct {
		label      string
		catalogRel string
		wantInside bool
	}{
		{"inside the workspace root", "catalog", true},
		{"outside the workspace root", filepath.Join("..", "elsewhere", "catalog"), false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			base := t.TempDir()
			ws := filepath.Join(base, "workspace")
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatalf("create the workspace root: %v", err)
			}
			catalogDir := filepath.Join(ws, tc.catalogRel)
			writeToolFixture(t, filepath.Join(catalogDir, "aggregates", "probe-module.yaml"), aggregate)

			s := newTestServer(t)

			// The root the call resolves to is what the containment judgement is
			// taken against, so an unintended one would make the rows below
			// compare against a root this test never named.
			tw, err := s.resolveToolWorkspace(ws, []string{catalogDir})
			if err != nil {
				t.Fatalf("resolve the workspace: %v", err)
			}
			if tw.root != ws {
				t.Fatalf("precondition: the call resolved to root %q, not the intended %q, so the "+
					"containment verdict below would be taken against the wrong root", tw.root, ws)
			}

			want, ok := compilerops.ResolveEntity(tw.newView("", nil), "probe-module")
			if !ok {
				t.Fatalf("the oracle resolved nothing from %s, so nothing here is observable", catalogDir)
			}
			if len(want.Bytes) == 0 {
				t.Fatal("precondition: the resolution retained no authored document, so nothing here is observable")
			}
			if want.Source == "" {
				t.Fatal("precondition: the resolution recorded no document, so withholding one asserts nothing")
			}

			res := callTool(t, s, "describe", describeArgs{
				Name: "probe-module", WorkspaceRoot: ws, CatalogDirs: []string{catalogDir},
			})
			if res.IsError {
				t.Fatalf("describe probe-module reported an error: %s", res.Content[0].Text)
			}
			var got describeEntityPayload
			if err := json.Unmarshal(res.StructuredContent, &got); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}

			// Reported either way, and it answers a different question from the
			// path: how much authored text the resolution retained.
			if got.AuthoredLength != len(want.Bytes) {
				t.Errorf("the payload reports %d byte(s); the resolution retained %d",
					got.AuthoredLength, len(want.Bytes))
			}

			file := told(got.File)
			if file == "" {
				t.Fatalf("the payload reported no path at all; the resolution recorded %q", want.Source)
			}
			if strings.Contains(file, "..") {
				t.Errorf("the reported path climbs above the workspace root: %q. A climb resolves "+
					"differently depending on where it is read from, which is what the absolute "+
					"spelling exists to avoid", file)
			}
			// The document this fixture wrote, named independently of the
			// resolution, so the assertions below are not the answer compared
			// against itself.
			document := filepath.Join(catalogDir, "aggregates", "probe-module.yaml")
			switch {
			case tc.wantInside:
				if filepath.IsAbs(file) {
					t.Errorf("a document inside the workspace root was reported at %q; inside the root "+
						"the answer is in the coordinate the caller supplied", file)
				}
				if anchored := filepath.Join(ws, file); anchored != filepath.Clean(document) {
					t.Errorf("the reported path %q anchors to %q against the root the call named; the "+
						"document is at %q", file, anchored, document)
				}
			default:
				if !filepath.IsAbs(file) {
					t.Errorf("a document outside the workspace root was reported at %q; outside the root "+
						"the absolute path is the one spelling the caller can open", file)
				}
				if file != filepath.Clean(document) {
					t.Errorf("the payload names document %q; the fixture wrote it at %q",
						file, document)
				}
			}
		})
	}
}
