// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The by-name branch's identity reporting, asserted against the resolution
// itself rather than against strings written out here.
//
// The oracle is a direct call to the shared by-name resolution over the same
// catalog, so these prove the command reports what that resolution decided. An
// expectation written out here, or read back off the command's own rendering,
// would pass on a command that reported the string a person typed and never the
// identity the catalog settled on — which is the one difference these assertions
// exist to catch.

// resolvedEntityOracle resolves name through the shared by-name resolution over
// the fullstack catalog, independently of the command under test.
//
// It loads its own catalog rather than reaching into the command's, which is a
// deliberate second construction: a cross-check that shared the command's
// loading would agree with it by definition, and agreement is the whole signal
// here.
func resolvedEntityOracle(t *testing.T, catalogDir, name string) compilerops.Entity {
	t.Helper()
	rw, err := workspaceconfig.Resolve(t.TempDir(), "")
	if err != nil {
		t.Fatalf("resolve a workspace for the oracle: %v", err)
	}
	cat, err := catalog.LoadAllWithCacheDir(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), catalogDir)
	if err != nil {
		t.Fatalf("oracle catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("oracle resolve extends: %v", extErrs[0])
	}
	e, ok := compilerops.ResolveEntity(newCatalogEntityView(cat), name)
	if !ok {
		t.Fatalf("the oracle resolved nothing for %q, so nothing can be asserted about it", name)
	}
	return e
}

// TestInspectByNameTitlesOutputWithTheResolvedIdentity pins that the header
// carries the identity and version the resolution settled on, and that the
// entity's own description is reported under it.
//
// The rows vary the description AND the version-bearing kind independently: a
// renderer that printed a fixed description line would fail the rows that carry
// none, one that printed none would fail the row that does, and one that titled
// its output with the argument rather than the resolved identity would fail the
// row whose argument carries no version.
func TestInspectByNameTitlesOutputWithTheResolvedIdentity(t *testing.T) {
	t.Parallel()
	catalogDir, err := filepath.Abs(fullstackAppInspectCatalog)
	if err != nil {
		t.Fatalf("resolve the catalog directory: %v", err)
	}

	// Every row's argument deliberately carries no version, so the version in the
	// header can only have come from the resolution.
	names := []string{"provider/helm", "db-module", "core/compute/api-server"}

	described := 0
	undescribed := 0
	for _, name := range names {
		want := resolvedEntityOracle(t, catalogDir, name)
		if strings.Contains(name, ":") {
			t.Fatalf("%q already carries a version, so the header would not discriminate", name)
		}
		if want.Version == "" {
			t.Fatalf("%q resolved to no version, so the header assertion would not discriminate", name)
		}
		if strings.TrimSpace(want.Description) == "" {
			undescribed++
		} else {
			described++
		}

		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			opts := testInspectOptions(&buf)
			if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), name, []string{catalogDir}, opts); err != nil {
				t.Fatalf("inspect %q: %v", name, err)
			}
			out := buf.String()

			header := want.Kind + ": " + identifierVersion(want.Name, want.Version)
			if !strings.Contains(out, header) {
				t.Errorf("the output is not titled with the resolved identity %q; got:\n%s", header, out)
			}

			descLine := "Description: " + want.Description
			switch {
			case strings.TrimSpace(want.Description) != "":
				if !strings.Contains(out, descLine) {
					t.Errorf("%q describes itself as %q and the output does not say so; got:\n%s",
						name, want.Description, out)
				}
			default:
				if strings.Contains(out, "Description:") {
					t.Errorf("%q declares no description and the output reports one anyway; got:\n%s", name, out)
				}
			}
		})
	}

	// Both directions have to be present or the table asserts only one of them.
	if described == 0 || undescribed == 0 {
		t.Fatalf("the table carries %d described and %d undescribed entity(ies); it needs both to "+
			"discriminate a renderer that always reports a description from one that never does",
			described, undescribed)
	}
}

// TestInspectByNameRendersAnAggregateFromItsAuthoredDocument pins that the
// authored file the resolution retained is what an Aggregate is rendered from.
//
// Only an Aggregate match carries one; every other kind is served from the
// catalog's index, which retains no authored-file source. So this is the one row
// where the retained path and bytes are observable, and it is asserted against
// the resolution's own values.
func TestInspectByNameRendersAnAggregateFromItsAuthoredDocument(t *testing.T) {
	t.Parallel()
	catalogDir, err := filepath.Abs(fullstackAppInspectCatalog)
	if err != nil {
		t.Fatalf("resolve the catalog directory: %v", err)
	}

	want := resolvedEntityOracle(t, catalogDir, "db-module")
	if want.Source == "" || len(want.Bytes) == 0 {
		t.Fatalf("precondition: the resolution retained no authored document for db-module "+
			"(source %q, %d byte(s)), so nothing here is observable", want.Source, len(want.Bytes))
	}
	if !strings.HasSuffix(want.Source, ".yaml") {
		t.Errorf("the retained source %q is not an authored document path", want.Source)
	}

	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	if err := dispatchInspect(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), "db-module", []string{catalogDir}, opts); err != nil {
		t.Fatalf("inspect db-module: %v", err)
	}
	out := buf.String()

	// The authored document declares this component; a render from anything other
	// than those bytes would not carry it.
	if !strings.Contains(out, "main-db") {
		t.Errorf("the render does not carry what the authored document declares; got:\n%s", out)
	}
	if !strings.Contains(out, "Aggregate: "+identifierVersion(want.Name, want.Version)) {
		t.Errorf("the render is not titled with the resolved identity; got:\n%s", out)
	}
}
