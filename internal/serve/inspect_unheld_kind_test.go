// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

// What the daemon says about a catalog file that declares a kind no catalog
// holds. Such a file never enters the catalog, so it can only explain a failure
// on a reference to the name it declares: that failure names it, and every
// other message the daemon composes from the catalog's skip record leaves it
// out, as it read before the file was recorded at all.

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// unheldKindWorkspace builds a workspace whose catalog directory holds one
// document declaring the name declares under the misspelled kind `Featur`, a
// valid Component demo/server, and the only document declaring demo-server —
// the Recipe the Manifest maps demo/server to — under the misspelled kind
// `Recipee`. Its root holds a valid Feature the catalog does not read and a
// Release placing that component. It returns a server holding the resolution
// for it and the Release's path.
//
// The Recipe is the misspelled one because the Manifest mapping's reference is
// the one the closure walk fails on in this workspace: a document misspelling
// the Component instead declares a name that failing reference never asks for.
func unheldKindWorkspace(t *testing.T, declares string) (*Server, string) {
	t.Helper()
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")
	writeFixtureFile(t, filepath.Join(ws, workspaceconfig.ConfigFilename), "workspace:\n  catalog_dirs:\n    - ./catalog\n")
	writeFixtureFile(t, filepath.Join(catalogDir, "feature", "nightly-backup.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Featur
metadata:
  name: `+declares+`
  version: 1.0.0
spec:
  appliesTo: "component.aggrega.ai/category=datastore"
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: demo/server
  version: "1.0.0"
spec:
  schema:
    values: {}
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipee
metadata:
  name: demo-server
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/server
`)
	writeFixtureFile(t, filepath.Join(ws, "feature-at-root.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: local/at-root
  version: 1.0.0
spec:
  appliesTo: "component.aggrega.ai/category=datastore"
`)
	writeFixtureFile(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: demo-aggregate
  version: "1.0.0"
spec:
  components:
    web:
      type: demo/server
`)
	writeFixtureFile(t, filepath.Join(ws, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: demo-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        demo/server: demo-server
`)
	releasePath := filepath.Join(ws, "release.yaml")
	writeFixtureFile(t, releasePath, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: demo-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        web:
          infra:
            replicas: 1
`)
	var in, out bytes.Buffer
	srv := newTestServer(t, ws, &in, &out)
	srv.lazyCatalog = catalog.NewLazyLookup(ws, "", []string{catalogDir}, nil)
	rw, err := workspaceconfig.ResolveWithoutGlobal(ws, ws, func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve the fixture workspace: %v", err)
	}
	srv.resolvedWorkspace = rw
	srv.wsConfig = rw.Config()
	return srv, releasePath
}

// misdeclaredNoteOpening is how the note naming such a file opens on a
// single-line message.
const misdeclaredNoteOpening = "note: a catalog file declares the name this reference asks for under a kind a catalog does not hold, " +
	"so the load skipped it: [CATALOG_ERROR E0371] "

// TestEntityDefinitionNamesAFileOfAKindNoCatalogHolds drives the by-name leg
// through the production dispatch.
//
// The first row looks up the name the misspelled file declares: the refusal
// names that file under its own short code and the kind it declares, and leaves
// out the account of where local entities are read from, because the file
// already sits where the catalog reads. The second row looks up a name the
// misspelled file does not declare, and is asserted by equality against the
// sentence and the account alone — so the file appearing anywhere in it, in the
// list of skipped files included, fails the row.
func TestEntityDefinitionNamesAFileOfAKindNoCatalogHolds(t *testing.T) {
	srv, _ := unheldKindWorkspace(t, "local/nightly-backup")
	account := strings.TrimSpace(srv.resolvedWorkspace.LocalCatalogAdviceNote(""))
	if account == "" {
		t.Fatal("precondition: this workspace composes no account, so the rows below would say nothing about it")
	}

	t.Run("the name the misspelled file declares", func(t *testing.T) {
		resp := callEntityDefinition(t, srv, map[string]any{"name": "local/nightly-backup"})
		if resp.Error == nil {
			t.Fatalf("a name the catalog resolves nothing for was answered: %s", resp.Result)
		}
		msg := resp.Error.Message
		for _, want := range []string{
			`"local/nightly-backup" is declared as a Featur by catalog/feature/nightly-backup.yaml, but the catalog resolved no entity for it; `,
			misdeclaredNoteOpening + "catalog/feature/nightly-backup.yaml: catalog document declares a kind a catalog does not hold (kind \"Featur\")",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("the refusal does not carry\n  %s\ngot\n  %s", want, msg)
			}
		}
		if strings.Contains(msg, account) {
			t.Errorf("the refusal tells the reader to move a file the catalog already reads:\n  %s", msg)
		}
		if strings.Contains(msg, "catalog files skipped at load") {
			t.Errorf("the misspelled files are listed as refusals besides the note naming one:\n  %s", msg)
		}
	})

	t.Run("a name the misspelled file does not declare", func(t *testing.T) {
		resp := callEntityDefinition(t, srv, map[string]any{"name": "local/at-root"})
		if resp.Error == nil {
			t.Fatalf("a name the catalog resolves nothing for was answered: %s", resp.Result)
		}
		want := `"local/at-root" is declared as a Feature by feature-at-root.yaml, but the catalog resolved no entity for it; ` + account
		if resp.Error.Message != want {
			t.Errorf("the refusal reads\n  %s\nwant\n  %s", resp.Error.Message, want)
		}
	})
}

// TestReleaseClosureFailureNamesAFileOfAKindNoCatalogHolds drives the
// closure-level failure the editor's Release-scope diagnostics and build carry:
// the Manifest maps the placed component to a Recipe whose only declaration
// misspells its kind, so the walk fails on that reference. The failure keeps
// its condition, names the misspelled Recipe file, and does not name the
// misspelled Feature file, which declares a name the walk never asked for.
//
// The reference the walk failed on is checked first, because the note is owed
// only for that reference: a fixture whose walk fails on some other name would
// make the remaining assertions say nothing about the note.
func TestReleaseClosureFailureNamesAFileOfAKindNoCatalogHolds(t *testing.T) {
	srv, releasePath := unheldKindWorkspace(t, "local/nightly-backup")
	_, jerr := srv.resolveReleaseClosure(context.Background(), srv.lazyCatalog, releasePath)
	if jerr == nil {
		t.Fatal("the closure resolved a Recipe no document of a held kind declares")
	}
	if !errors.Is(jerr.cause, closure.ErrUnresolvableURI()) {
		t.Errorf("the failure lost the unresolvable-reference condition: %v", jerr.cause)
	}
	if !strings.Contains(jerr.Message, `entity "demo-server"`) {
		t.Fatalf("precondition: the walk did not fail on the Manifest's reference to demo-server, so the note is not owed here:\n  %s", jerr.Message)
	}
	want := misdeclaredNoteOpening + "catalog/recipes/server.yaml: catalog document declares a kind a catalog does not hold (kind \"Recipee\")"
	if !strings.Contains(jerr.Message, want) {
		t.Errorf("the failure does not name the misspelled Recipe file:\n  want: %s\n  got:  %s", want, jerr.Message)
	}
	if strings.Contains(jerr.Message, "nightly-backup") {
		t.Errorf("the failure names a file declaring a name the walk never asked for:\n  %s", jerr.Message)
	}
}
