// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// The refusal the by-name leg returns for a name the workspace declares and the
// catalog resolved nothing for.
//
// The fixture is the shape a reader meets it in: the Release's own documents sit
// at the workspace root, and the catalog search reads a directory that is not the
// root. A build resolves those documents through the Release's own directory; a
// by-name lookup has no such tier and reads the catalog alone, so the name plainly
// written at the root resolves to nothing. What is asserted here is what the
// refusal SAYS about that, never what resolves.

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// unresolvedRefusalWorkspace builds that workspace, with the catalog-directory
// setting written as workspaceBlock supplies it, and returns the root and a
// server holding the resolution for it.
//
// The catalog search stack is the same in every case: only the configuration the
// account is composed from varies, so a case that asserts the account's absence
// is asserting the configuration and not a second difference in what resolves.
//
// The resolution skips the global configuration layer and reads a frozen
// environment. Taking the machine's own layers would compose the account under
// test out of whatever configuration the machine running this happens to carry.
func unresolvedRefusalWorkspace(t *testing.T, workspaceBlock string) (string, *Server) {
	t.Helper()
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")

	writeFixtureFile(t, filepath.Join(ws, workspaceconfig.ConfigFilename), workspaceBlock)

	// Two documents at the root, declaring kinds whose articles differ, so one
	// fixture drives both and a hardcoded article fails on one of them.
	writeFixtureFile(t, filepath.Join(ws, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo-aggregate
  version: "1.0.0"
spec:
  components:
    web:
      type: demo/server
`)
	writeFixtureFile(t, filepath.Join(ws, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	// One entity the catalog does resolve, so a refusal here reports a name the
	// search missed rather than a search that found nothing at all.
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/server
  version: "1.0.0"
spec:
  schema:
    values: {}
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
	return ws, srv
}

// TestEntityDefinitionRefusalSaysWhereLocalEntitiesAreReadFrom is the invocation
// proof for the whole clause: the by-name request is driven through the
// production dispatch, and the refusal it returns is read for the account of
// where this workspace reads local entities from.
//
// The account is compared against the workspace's own composition of it rather
// than against a sentence written here, so the two cannot come to disagree and
// this asserts that the refusal reached that composition — not that someone
// re-typed it.
//
// The third case is the branch that deliberately carries no account. By the time
// it is reached the daemon has walked the workspace and established that no
// document in it declares the name, so there is no document to relocate and no
// directory to list that would make one appear.
func TestEntityDefinitionRefusalSaysWhereLocalEntitiesAreReadFrom(t *testing.T) {
	_, srv := unresolvedRefusalWorkspace(t, "workspace:\n  catalog_dirs:\n    - ./catalog\n")

	account := strings.TrimSpace(srv.resolvedWorkspace.LocalCatalogAdviceNote())
	if account == "" {
		t.Fatal("precondition: this workspace composes no account of where local entities are read from, " +
			"so every assertion below would hold on a refusal that carried nothing")
	}

	for _, tc := range []struct {
		name        string
		sentence    string
		wantAccount bool
	}{
		{"demo-aggregate", `"demo-aggregate" is declared as an Aggregate by aggregate.yaml`, true},
		{"demo-manifest", `"demo-manifest" is declared as a Manifest by manifest.yaml`, true},
		{"demo/nothing-declares-this", `"demo/nothing-declares-this" matched no entity of any kind`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := callEntityDefinition(t, srv, map[string]any{"name": tc.name})
			if resp.Error == nil {
				t.Fatalf("a name the catalog resolves nothing for was answered: %s", resp.Result)
			}
			if !strings.Contains(resp.Error.Message, tc.sentence) {
				t.Errorf("the refusal does not read as the sentence it owes:\n  got: %s\n want: %s",
					resp.Error.Message, tc.sentence)
			}
			carries := strings.Contains(resp.Error.Message, account)
			if carries != tc.wantAccount {
				t.Errorf("the refusal carries the account of where local entities are read from = %v, want %v:\n  got: %s\n account: %s",
					carries, tc.wantAccount, resp.Error.Message, account)
			}
		})
	}
}

// TestEntityDefinitionRefusalArguesWithNoDeclaredAbsenceOfALocalCatalog is the
// negative half, and it is the one that fails quietly if the account is appended
// unconditionally: a reader who wrote the catalog-directory list as an empty list
// declared that this workspace has no local catalog, and telling them where local
// entities are read from contradicts what they wrote.
func TestEntityDefinitionRefusalArguesWithNoDeclaredAbsenceOfALocalCatalog(t *testing.T) {
	_, srv := unresolvedRefusalWorkspace(t, "workspace:\n  catalog_dirs: []\n")

	if account := strings.TrimSpace(srv.resolvedWorkspace.LocalCatalogAdviceNote()); account != "" {
		t.Fatalf("precondition: this workspace still composes an account despite the empty list, "+
			"so the assertion below would not be about the empty list: %s", account)
	}

	resp := callEntityDefinition(t, srv, map[string]any{"name": "demo-aggregate"})
	if resp.Error == nil {
		t.Fatalf("a name the catalog resolves nothing for was answered: %s", resp.Result)
	}
	want := `"demo-aggregate" is declared as an Aggregate by aggregate.yaml, but the catalog resolved no entity for it`
	if resp.Error.Message != want {
		t.Errorf("the refusal says more than the workspace's own declaration leaves it free to say:\n  got: %s\n want: %s",
			resp.Error.Message, want)
	}
	if strings.Contains(resp.Error.Message, workspaceconfig.CatalogDirsSetting) {
		t.Errorf("the refusal names the catalog-directory setting to a reader who declared the list empty: %s",
			resp.Error.Message)
	}
}

// refusedDocumentWorkspace builds a workspace whose catalog directory holds a
// Feature its kind's schema refuses, and whose root — not a catalog directory —
// holds a valid Feature, and returns a server holding the resolution for it.
func refusedDocumentWorkspace(t *testing.T) *Server {
	t.Helper()
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")
	writeFixtureFile(t, filepath.Join(ws, workspaceconfig.ConfigFilename), "workspace:\n  catalog_dirs:\n    - ./catalog\n")
	// The one fault is the missing metadata.version.
	writeFixtureFile(t, filepath.Join(catalogDir, "feature", "nightly-backup.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: local/nightly-backup
spec:
  appliesTo: "component.aggrega.ai/category=datastore"
`)
	writeFixtureFile(t, filepath.Join(ws, "feature-at-root.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: local/at-root
  version: 1.0.0
spec:
  appliesTo: "component.aggrega.ai/category=datastore"
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
	return srv
}

// TestEntityDefinitionStatesARefusedDocumentOnce is the invocation proof for the
// refusal of a name a document declares: driven through the production dispatch,
// the refusal for a document the catalog read and refused leads with that
// document's fault, stated once, and carries no advice to move a file that sits
// where the catalog reads; a document the catalog does not read still gets the
// account of where it reads.
//
// The first row is asserted by equality, so a second copy of the refusal, a
// second category head, the absolute path after the relative one, or the account
// appearing anywhere in it fails the row. The second row is the positive control
// for the account: a refusal withheld everywhere would pass the first row and
// fail this one.
func TestEntityDefinitionStatesARefusedDocumentOnce(t *testing.T) {
	srv := refusedDocumentWorkspace(t)
	account := strings.TrimSpace(srv.resolvedWorkspace.LocalCatalogAdviceNote())
	if account == "" {
		t.Fatal("precondition: this workspace composes no account, so the rows below would say nothing about it")
	}
	code, ok := cerrors.ShortCodeOf(envelope.ErrEntityEnvelope())
	if !ok {
		t.Fatal("the envelope-validation condition must be registered with a short code")
	}

	for _, tc := range []struct {
		name     string
		file     string
		want     string
		wantLead string
	}{
		{
			name: "local/nightly-backup",
			file: "catalog/feature/nightly-backup.yaml",
			want: "[" + string(cerrors.CatalogError) + " " + code + "] catalog/feature/nightly-backup.yaml: " +
				"document envelope validation failed (Feature); 3:1: metadata.version: missing required property: version. " +
				"correct the document to match its kind's schema; note: this file was skipped at load, so the catalog " +
				`holds nothing under the identity it declares, and "local/nightly-backup" resolved to no entity`,
		},
		{
			name:     "local/at-root",
			file:     "feature-at-root.yaml",
			wantLead: `"local/at-root" is declared as a Feature by feature-at-root.yaml, but the catalog resolved no entity for it; `,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := callEntityDefinition(t, srv, map[string]any{"name": tc.name})
			if resp.Error == nil {
				t.Fatalf("a name the catalog resolves nothing for was answered: %s", resp.Result)
			}
			if resp.Error.Code != errEntityNotResolvable {
				t.Errorf("the refusal carries code %d; want %d", resp.Error.Code, errEntityNotResolvable)
			}
			if resp.Error.Data == nil || resp.Error.Data.File != tc.file {
				t.Errorf("the refusal names %+v; want the declaring document %q", resp.Error.Data, tc.file)
			}
			if tc.want != "" {
				if resp.Error.Message != tc.want {
					t.Errorf("the refusal reads\n  %s\nwant\n  %s", resp.Error.Message, tc.want)
				}
				return
			}
			if want := tc.wantLead + account; resp.Error.Message != want {
				t.Errorf("the refusal reads\n  %s\nwant\n  %s", resp.Error.Message, want)
			}
		})
	}
}

// TestIndefiniteArticleAgreesWithTheKindItPrecedes covers the kinds the sentence
// above cannot reach in one fixture, including the empty kind a document that
// declares none yields.
func TestIndefiniteArticleAgreesWithTheKindItPrecedes(t *testing.T) {
	for _, tc := range []struct{ kind, want string }{
		{"Aggregate", "an"},
		{"ArtifactLibrary", "an"},
		{"Component", "a"},
		{"Feature", "a"},
		{"Manifest", "a"},
		{"Recipe", "a"},
		{"Release", "a"},
		{"SchemaLibrary", "a"},
		{"", "a"},
	} {
		t.Run(fmt.Sprintf("%q", tc.kind), func(t *testing.T) {
			if got := indefiniteArticle(tc.kind); got != tc.want {
				t.Errorf("indefiniteArticle(%q) = %q, want %q", tc.kind, got, tc.want)
			}
		})
	}
}
