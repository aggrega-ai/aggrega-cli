// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// releaseVariantWorkspace copies the shared fixture keeping two variants of one
// Component side by side, one per Release, and opens it the way the daemon opens
// any workspace a request names, through the session's own resolution — the one
// place a workspace's per-Release lookups are set up.
//
// The fixture's catalog-a/ and catalog-b/ each declare demo/datastore/db, and only
// catalog-b/ declares demo/datastore/cache; its external/ directory, copied to a
// directory of its own outside the workspace, holds the Feature demo/audit-trail.
// The configuration written here gives release-a.yaml catalog-a/ and that outside
// directory, and release-b.yaml catalog-b/, with no workspace list and no
// conventional catalog directory, so a request naming no Release reads no catalog
// directory at all.
func releaseVariantWorkspace(t *testing.T) *Server {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "release-variant-catalogs"))
	if err != nil {
		t.Fatalf("locate the fixture: %v", err)
	}
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join(src, "workspace"))); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	external := t.TempDir()
	if err := os.CopyFS(external, os.DirFS(filepath.Join(src, "external"))); err != nil {
		t.Fatalf("copy the fixture's outside directory: %v", err)
	}
	config := fmt.Sprintf("workspace:\n  releases:\n    release-a.yaml:\n      catalog_dirs:\n        - catalog-a\n"+
		"        - %s\n    release-b.yaml:\n      catalog_dirs:\n        - catalog-b\n", external)
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	sess := newTestSession(t, root, bytes.NewReader(nil), &bytes.Buffer{})
	srv, err := sess.workspaceFor(root)
	if err != nil {
		t.Fatalf("open the workspace at %s: %v", root, err)
	}
	return srv
}

// indexRows renders an entity index as sorted rows, so two answers compare as sets.
func indexRows(entities []CatalogEntity) []string {
	rows := make([]string, 0, len(entities))
	for _, e := range entities {
		rows = append(rows, fmt.Sprintf("%s %s [%s] %s", e.Kind, e.Name, e.File, e.Origin))
	}
	sort.Strings(rows)
	return rows
}

// TestCatalogIndexListsTheEntitiesInTheTermsOfTheReleaseNamed is the invocation
// proof for the listing's Release: one daemon, three requests, and three answers
// that differ only in which Release was named.
//
// Every row is held by equality over the whole set, so a row fails on an entity
// it gained as well as on one it lost. The first row is the control: with no
// Release named, both variants are listed and the outside Feature is not, which
// is the workspace's own listing. The two named rows then disagree with it and
// with each other in exactly the variant each Release reads, and in the Feature
// only release-a.yaml's directories declare.
func TestCatalogIndexListsTheEntitiesInTheTermsOfTheReleaseNamed(t *testing.T) {
	t.Parallel()
	srv := releaseVariantWorkspace(t)
	shared := []string{
		"Aggregate demo-app [aggregate.yaml] workspace",
		"Release variant-a [release-a.yaml] workspace",
		"Release variant-b [release-b.yaml] workspace",
	}
	cases := []struct {
		name    string
		release string
		want    []string
	}{
		{
			name: "no Release lists both variants and no Feature from a Release's directories",
			want: append([]string{
				"Component demo/datastore/cache [catalog-b/component/cache.yaml] workspace",
				"Component demo/datastore/db [catalog-a/component/db.yaml] workspace",
				"Component demo/datastore/db [catalog-b/component/db.yaml] workspace",
			}, shared...),
		},
		{
			name:    "release-a.yaml lists its variant and the Feature its outside directory declares",
			release: "release-a.yaml",
			want: append([]string{
				"Component demo/datastore/db [catalog-a/component/db.yaml] workspace",
				"Feature demo/audit-trail [] catalog",
			}, shared...),
		},
		{
			name:    "release-b.yaml lists its variant, its own Component, and no Feature",
			release: "release-b.yaml",
			want: append([]string{
				"Component demo/datastore/cache [catalog-b/component/cache.yaml] workspace",
				"Component demo/datastore/db [catalog-b/component/db.yaml] workspace",
			}, shared...),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{}
			if tc.release != "" {
				params["release"] = tc.release
			}
			var res catalogIndexResult
			decodeDaemonResult(t, callDaemonMethod(t, srv, "aggrega/catalogIndex", params), &res)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if got := indexRows(res.Entities); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("aggrega/catalogIndex naming %q\n got:\n  %s\nwant:\n  %s",
					tc.release, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
			}
		})
	}
}

// TestARequestNamingAReleaseIsRefusedWhereTheValueCannotNameOne pins the
// refusals of the Release parameter on the two methods that gained it, each with
// its one fault, and the positive control on each method beside them.
//
// The unknown-field row is the reason the listing's params are declared rather
// than left to the routing fields: naming a Release must not turn a strict decode
// into one that drops what it does not know.
func TestARequestNamingAReleaseIsRefusedWhereTheValueCannotNameOne(t *testing.T) {
	t.Parallel()
	srv := releaseVariantWorkspace(t)
	cases := []struct {
		name   string
		method string
		params map[string]any
		// fault is the phrase the refusal carries, or empty for the control.
		fault string
	}{
		{name: "the listing, a Release that is there", method: "aggrega/catalogIndex",
			params: map[string]any{"release": "release-a.yaml"}},
		{name: "the listing, an absolute Release", method: "aggrega/catalogIndex",
			params: map[string]any{"release": filepath.Join(srv.workspaceRoot, "release-a.yaml")}, fault: "is an absolute path"},
		{name: "the listing, a Release above the root", method: "aggrega/catalogIndex",
			params: map[string]any{"release": "../release-a.yaml"}, fault: "resolves outside the workspace root"},
		{name: "the listing, a Release that is not there", method: "aggrega/catalogIndex",
			params: map[string]any{"release": "release-gone.yaml"}, fault: "does not exist"},
		{name: "the listing, a directory", method: "aggrega/catalogIndex",
			params: map[string]any{"release": "catalog-a"}, fault: "is a directory"},
		{name: "the listing, an undeclared field beside a Release", method: "aggrega/catalogIndex",
			params: map[string]any{"release": "release-a.yaml", "zzz_not_a_declared_field": true}, fault: "does not accept"},
		{name: "the lookup, a Release that is there", method: "aggrega/entityDefinition",
			params: map[string]any{"name": "demo/datastore/db", "release": "release-a.yaml"}},
		{name: "the lookup, a Release that is not there", method: "aggrega/entityDefinition",
			params: map[string]any{"name": "demo/datastore/db", "release": "release-gone.yaml"}, fault: "does not exist"},
		{name: "the lookup, a registry reference with a Release", method: "aggrega/entityDefinition",
			params: map[string]any{"name": "oci://registry.example.com/component/demo/datastore/db:1.0.0", "release": "release-a.yaml"},
			fault:  "registry reference"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := callDaemonMethod(t, srv, tc.method, tc.params)
			if tc.fault == "" {
				if resp.Error != nil {
					t.Fatalf("%s refused the control: %d %s", tc.method, resp.Error.Code, resp.Error.Message)
				}
				return
			}
			if resp.Error == nil {
				t.Fatalf("%s answered %s; want a refusal carrying %q", tc.method, resp.Result, tc.fault)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("code = %d; want %d, the request being at fault", resp.Error.Code, jsonRPCInvalidParams)
			}
			if !strings.Contains(resp.Error.Message, tc.fault) {
				t.Errorf("refusal %q does not carry %q", resp.Error.Message, tc.fault)
			}
		})
	}
}

// TestEntityDefinitionResolvesTheNameInTheTermsOfTheReleaseNamed is the
// invocation proof for the lookup's Release: one name, both variants, and the
// refusal for a name only the other Release's directories declare.
//
// The two resolving rows disagree in the one thing that can tell them apart, the
// variant's file and what it says about itself. The refusal row is paired with a
// control asking the same name of the Release that does declare it, so the
// refusal is about the Release named and not about the name. The no-Release row
// pins the workspace's own answer: the session reads no catalog directory here, so
// the name is refused naming the workspace document that declares it.
func TestEntityDefinitionResolvesTheNameInTheTermsOfTheReleaseNamed(t *testing.T) {
	t.Parallel()
	srv := releaseVariantWorkspace(t)
	cases := []struct {
		name    string
		params  map[string]any
		file    string
		about   string
		refusal []string
		// offered is the document the refusal names in its data, or empty for none.
		offered string
	}{
		{name: "release-a.yaml resolves its own variant",
			params: map[string]any{"name": "demo/datastore/db", "release": "release-a.yaml"},
			file:   "catalog-a/component/db.yaml", about: "the variant release-a.yaml builds with"},
		{name: "release-b.yaml resolves its own variant",
			params: map[string]any{"name": "demo/datastore/db", "release": "release-b.yaml"},
			file:   "catalog-b/component/db.yaml", about: "the variant release-b.yaml builds with"},
		{name: "a name only the other Release's directories declare is refused with that Release's account",
			params: map[string]any{"name": "demo/datastore/cache", "release": "release-a.yaml"},
			refusal: []string{"in the terms of the Release release-a.yaml",
				"local entities for the Release `release-a.yaml`", workspaceconfig.ReleaseCatalogsSetting}},
		{name: "the same name resolves for the Release that declares it",
			params: map[string]any{"name": "demo/datastore/cache", "release": "release-b.yaml"},
			file:   "catalog-b/component/cache.yaml", about: "declared only among release-b.yaml's directories"},
		{name: "no Release is answered over the workspace's own lookup, naming the declaring document",
			params:  map[string]any{"name": "demo/datastore/cache"},
			refusal: []string{"is declared as a Component by catalog-b/component/cache.yaml"},
			offered: "catalog-b/component/cache.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := callDaemonMethod(t, srv, "aggrega/entityDefinition", tc.params)
			if len(tc.refusal) == 0 {
				var res entityDefinitionResult
				decodeDaemonResult(t, resp, &res)
				if res.File != tc.file || res.Description != tc.about {
					t.Errorf("resolved %q in %q (%q); want %q (%q)", tc.params["name"], res.File, res.Description, tc.file, tc.about)
				}
				return
			}
			if resp.Error == nil {
				t.Fatalf("answered %s; want a refusal", resp.Result)
			}
			if resp.Error.Code != errEntityNotResolvable {
				t.Errorf("code = %d; want entity-not-resolvable (%d)", resp.Error.Code, errEntityNotResolvable)
			}
			for _, phrase := range tc.refusal {
				if !strings.Contains(resp.Error.Message, phrase) {
					t.Errorf("refusal %q does not carry %q", resp.Error.Message, phrase)
				}
			}
			var offered string
			if resp.Error.Data != nil {
				offered = resp.Error.Data.File
			}
			if offered != tc.offered {
				t.Errorf("the refusal offers %q as the declaring document; want %q", offered, tc.offered)
			}
		})
	}
}
