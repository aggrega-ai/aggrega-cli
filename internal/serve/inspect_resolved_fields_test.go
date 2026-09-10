// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// What the by-name answer carries of the resolution, asserted against the
// resolution itself.
//
// The oracle is a direct call to the shared by-name resolution over the same
// view, so these prove the answer carries what that resolution decided rather
// than agreeing with a second copy of it. Reading the expectation back off this
// response's own projection would compare a value with itself: a field the
// projection stopped carrying would be missing from both sides, and the
// comparison would go on passing with nothing to say a fact had stopped being
// reported.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// resolvedFieldsWorkspace points a server at the shared fullstack catalog, whose
// kinds differ in which of the resolution's facts they carry — which is what
// makes the table below discriminate.
func resolvedFieldsWorkspace(t *testing.T) (srv *Server, catalogDir string) {
	t.Helper()
	ws, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "fullstack-app"))
	if err != nil {
		t.Fatalf("resolve the fixture workspace: %v", err)
	}
	catalogDir = filepath.Join(ws, "catalog")

	// The catalog sits inside the workspace root here, so a recorded authored path
	// is directly reportable and the comparisons below can be against the
	// resolution's own value. The containment rule itself is asserted separately,
	// over a catalog outside the root.
	if rel, ok := pathscope.Relative(ws, catalogDir); !ok {
		t.Fatalf("precondition: the fixture catalog %s is not inside the workspace root %s, so a "+
			"reported path would be withheld and the source comparisons would assert nothing",
			catalogDir, ws)
	} else if rel == "" {
		t.Fatalf("precondition: the fixture catalog resolved to an empty workspace-relative path")
	}

	var in, out bytes.Buffer
	srv = newTestServer(t, ws, &in, &out)
	srv.wsConfig.Workspace.CatalogDirs = []string{catalogDir}
	srv.lazyCatalog = catalog.NewLazyLookup(ws, "", []string{catalogDir}, nil)
	return srv, catalogDir
}

// TestEntityDefinitionCarriesEveryFactTheResolutionDecided pins the description,
// the publishing scope, the document the entity was declared in and the retained
// authored text onto the answer, with every expectation read off the resolution's
// own result.
//
// The rows differ in which facts they carry — one kind carries a description and
// a scope and no authored text, another carries authored text and neither of the
// first two, a third carries none of the three — so an answer filling these
// fields with constants fails on some row whatever constant it chose, and one
// that carried them for a single kind fails on the others.
//
// The document is the one fact every row carries, and it is reported per kind, so
// what discriminates it is not a population split but the value: each row's path
// names that row's own file, and a constant fails every row but one.
func TestEntityDefinitionCarriesEveryFactTheResolutionDecided(t *testing.T) {
	srv, _ := resolvedFieldsWorkspace(t)

	names := []string{"provider/helm", "db-module", "core/compute/api-server"}

	var described, undescribed, scoped, unscoped, texted, untexted int
	files := map[string]string{}
	for _, name := range names {
		view := srv.lazyCatalog.ForRequest(nil)
		want, ok := compilerops.ResolveEntity(view, name)
		if !ok {
			t.Fatalf("the oracle resolved nothing for %q, so nothing can be asserted about it", name)
		}
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
			got := decodeEntityDefinition(t, callEntityDefinition(t, srv, map[string]any{"name": name}))

			if got.Description != want.Description {
				t.Errorf("the answer describes %q as %q; the resolution decided %q",
					name, got.Description, want.Description)
			}
			if got.PublishScope != want.Scope {
				t.Errorf("the answer scopes %q by %q; the resolution decided %q",
					name, got.PublishScope, want.Scope)
			}
			if got.File != want.Source {
				t.Errorf("the answer declares %q in %q; the resolution resolved it in %q",
					name, got.File, want.Source)
			}
			if got.File == "" {
				t.Errorf("%q was declared in a document under the workspace root and is reported with "+
					"no file at all", name)
			}
			if got.AuthoredLength != len(want.Bytes) {
				t.Errorf("the answer reports %d byte(s) of authored text for %q; the resolution retained %d",
					got.AuthoredLength, name, len(want.Bytes))
			}
			// A path reported to a client is workspace-relative everywhere else in
			// this daemon's answers, and a host path would disclose where the
			// workspace sits on the machine.
			if filepath.IsAbs(got.File) {
				t.Errorf("the answer discloses a host path for %q: %q", name, got.File)
			}
			files[name] = got.File
		})
	}

	// Both polarities of each optional fact, or the table asserts less than it
	// appears to: a projection filling one with a constant passes any table whose
	// rows agree on it.
	if described == 0 || undescribed == 0 || scoped == 0 || unscoped == 0 || texted == 0 || untexted == 0 {
		t.Fatalf("the table carries %d described / %d not, %d scoped / %d not, %d with authored text / "+
			"%d without; it needs both polarities of each to discriminate a constant-filling projection",
			described, undescribed, scoped, unscoped, texted, untexted)
	}
	// The document is reported for every kind, so its discriminator is that the
	// rows disagree: a projection reporting one entity's file for all of them
	// would satisfy every per-row comparison above only if they happened to
	// coincide, and this says they do not.
	seen := map[string]string{}
	for name, file := range files {
		if other, clash := seen[file]; clash {
			t.Errorf("%q and %q are both reported as declared in %q, so the rows cannot discriminate a "+
				"projection reporting one file for every entity", name, other, file)
		}
		seen[file] = name
	}
}

// TestEntityDefinitionCarriesAnAuthoredDocumentByReferenceNotByValue pins the
// deliberate omission: the retained document is reported by its path and its
// length, and its bytes are not carried.
//
// The definition already answers what the entity IS, with inheritance applied.
// A second copy of the one file that declared it would answer a question this
// request was not asked, at the cost of the payload — so the path and the length
// are what a client is given, and this asserts the bytes did not arrive with
// them.
func TestEntityDefinitionCarriesAnAuthoredDocumentByReferenceNotByValue(t *testing.T) {
	srv, _ := resolvedFieldsWorkspace(t)

	view := srv.lazyCatalog.ForRequest(nil)
	want, ok := compilerops.ResolveEntity(view, "db-module")
	if !ok {
		t.Fatal("the oracle resolved nothing for db-module")
	}
	if want.Source == "" || len(want.Bytes) == 0 {
		t.Fatalf("precondition: the resolution retained no authored document (source %q, %d byte(s)), "+
			"so an omission here asserts nothing", want.Source, len(want.Bytes))
	}

	got := decodeEntityDefinition(t, callEntityDefinition(t, srv, map[string]any{"name": "db-module"}))
	if got.File != want.Source {
		t.Errorf("the answer names document %q; the resolution resolved it in %q", got.File, want.Source)
	}
	if got.AuthoredLength != len(want.Bytes) {
		t.Errorf("the answer reports %d byte(s); the resolution retained %d", got.AuthoredLength, len(want.Bytes))
	}
	if got.Definition == string(want.Bytes) {
		t.Error("the answer carries the authored bytes verbatim, so it answers what one document says " +
			"rather than what the entity is")
	}
}

// TestEntityDefinitionReportsNoPathForADocumentOutsideTheWorkspace pins the
// containment rule on the reported path.
//
// A catalog directory need not sit inside the workspace root, and the catalog
// records the path in whatever form it holds — which for an outside directory is
// a climb above the root. A client cannot resolve such a path, and the honest
// alternative names a location on the machine the daemon runs on. So the path is
// withheld, while the length still reports that an authored document was
// retained.
//
// The row where the directory is INSIDE the root is the positive control: without
// it, a response that reported no path at all would pass this.
func TestEntityDefinitionReportsNoPathForADocumentOutsideTheWorkspace(t *testing.T) {
	const aggregate = `apiVersion: aggrega.ai/v1alpha1
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
		wantPath   bool
	}{
		{"inside the workspace root", filepath.Join("catalog"), true},
		{"outside the workspace root", filepath.Join("..", "elsewhere", "catalog"), false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			base := t.TempDir()
			ws := filepath.Join(base, "workspace")
			if err := os.MkdirAll(ws, 0o755); err != nil {
				t.Fatalf("create the workspace root: %v", err)
			}
			catalogDir := filepath.Join(ws, tc.catalogRel)
			writeFixtureFile(t, filepath.Join(catalogDir, "aggregates", "probe-module.yaml"), aggregate)

			var in, out bytes.Buffer
			srv := newTestServer(t, ws, &in, &out)
			srv.wsConfig.Workspace.CatalogDirs = []string{catalogDir}
			srv.lazyCatalog = catalog.NewLazyLookup(ws, "", []string{catalogDir}, nil)

			want, ok := compilerops.ResolveEntity(srv.lazyCatalog.ForRequest(nil), "probe-module")
			if !ok {
				t.Fatalf("the oracle resolved nothing from %s", catalogDir)
			}
			if len(want.Bytes) == 0 {
				t.Fatal("precondition: the resolution retained no authored document, so nothing here is observable")
			}

			got := decodeEntityDefinition(t, callEntityDefinition(t, srv, map[string]any{"name": "probe-module"}))

			// Reported either way: it is what tells a client an authored text
			// was retained even where the document's path cannot be carried.
			if got.AuthoredLength != len(want.Bytes) {
				t.Errorf("the answer reports %d byte(s); the resolution retained %d",
					got.AuthoredLength, len(want.Bytes))
			}
			switch {
			case tc.wantPath:
				if got.File == "" {
					t.Errorf("a document inside the workspace root was reported with no path at all")
				}
			default:
				if got.File != "" {
					t.Errorf("a document outside the workspace root was reported at %q, which this "+
						"client cannot resolve", got.File)
				}
			}
			if strings.Contains(got.File, "..") {
				t.Errorf("the reported path climbs above the workspace root: %q", got.File)
			}
			if filepath.IsAbs(got.File) {
				t.Errorf("the reported path discloses a host location: %q", got.File)
			}
		})
	}
}
