// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// releaseVariantWorkspace copies the shared fixture keeping two variants of one
// Component side by side, one per Release, and returns its root.
//
// The fixture's catalog-a/ and catalog-b/ each declare demo/datastore/db, and only
// catalog-b/ declares demo/datastore/cache; its external/ directory, copied to a
// directory of its own outside the workspace, holds the Feature demo/audit-trail.
// The configuration written here gives release-a.yaml catalog-a/ and that outside
// directory, and release-b.yaml catalog-b/, with no workspace list and no
// conventional catalog directory, so a call naming no Release reads no catalog
// directory at all.
func releaseVariantWorkspace(t *testing.T) string {
	t.Helper()
	src := filepath.Join(moduleRoot(t), "internal", "compiler", "testdata", "release-variant-catalogs")
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
	return root
}

// TestListEntitiesListsTheEntitiesInTheTermsOfTheReleaseNamed is the tool
// protocol's half of the listing proof: the same three answers the daemon gives,
// held by equality over the whole set.
//
// The first row is the control — no Release lists both variants and no Feature
// from a Release's directories — so the two named rows fail on the variant they
// gained or lost and on the Feature only release-a.yaml's directories declare.
func TestListEntitiesListsTheEntitiesInTheTermsOfTheReleaseNamed(t *testing.T) {
	t.Parallel()
	root := releaseVariantWorkspace(t)
	shared := []string{
		"Aggregate demo-app [aggregate.yaml] workspace",
		"Release variant-a [release-a.yaml] workspace",
		"Release variant-b [release-b.yaml] workspace",
	}
	cases := []struct {
		name    string
		release *string
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
			release: stringPointer("release-a.yaml"),
			want: append([]string{
				"Component demo/datastore/db [catalog-a/component/db.yaml] workspace",
				"Feature demo/audit-trail [] catalog",
			}, shared...),
		},
		{
			name:    "release-b.yaml lists its variant, its own Component, and no Feature",
			release: stringPointer("release-b.yaml"),
			want: append([]string{
				"Component demo/datastore/cache [catalog-b/component/cache.yaml] workspace",
				"Component demo/datastore/db [catalog-b/component/db.yaml] workspace",
			}, shared...),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := callTool(t, newTestServer(t), "list-entities", listEntitiesArgs{
				WorkspaceRoot:  root,
				Release:        tc.release,
				ResponseFormat: stringPointer(responseFormatDetailed),
			})
			if res.IsError {
				t.Fatalf("list-entities was refused: %s", res.Content[0].Text)
			}
			var payload listEntitiesResult
			if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
				t.Fatalf("decode the listing: %v", err)
			}
			got := entityRows(payload.Entities)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("list-entities naming %v\n got:\n  %s\nwant:\n  %s",
					tc.release, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
			}
		})
	}
}

// TestDescribeResolvesTheNameInTheTermsOfTheReleaseNamed is the tool protocol's
// half of the lookup proof: a name with a Release resolves to that Release's
// variant, a name only the other Release's directories declare is refused with
// the account naming the Release's entry, and the refusals of the Release
// argument each carry their one fault.
//
// The refusal row is paired with a control asking the same name of the Release
// that does declare it, so the refusal is about the Release named.
func TestDescribeResolvesTheNameInTheTermsOfTheReleaseNamed(t *testing.T) {
	t.Parallel()
	root := releaseVariantWorkspace(t)
	cases := []struct {
		name    string
		args    describeArgs
		file    string
		about   string
		refusal []string
	}{
		{name: "release-a.yaml resolves its own variant",
			args: describeArgs{Name: "demo/datastore/db", Release: "release-a.yaml"},
			file: "catalog-a/component/db.yaml", about: "the variant release-a.yaml builds with"},
		{name: "release-b.yaml resolves its own variant",
			args: describeArgs{Name: "demo/datastore/db", Release: "release-b.yaml"},
			file: "catalog-b/component/db.yaml", about: "the variant release-b.yaml builds with"},
		{name: "a name only the other Release's directories declare is refused with that Release's account",
			args: describeArgs{Name: "demo/datastore/cache", Release: "release-a.yaml"},
			refusal: []string{"in the terms of the Release release-a.yaml",
				"local entities for the Release `release-a.yaml`", workspaceconfig.ReleaseCatalogsSetting}},
		{name: "the same name resolves for the Release that declares it",
			args: describeArgs{Name: "demo/datastore/cache", Release: "release-b.yaml"},
			file: "catalog-b/component/cache.yaml", about: "declared only among release-b.yaml's directories"},
		{name: "no Release is answered over the workspace's own stack",
			args:    describeArgs{Name: "demo/datastore/db"},
			refusal: []string{`No catalog entity named "demo/datastore/db" resolved, in any kind`}},
		{name: "a Release that is not there is refused",
			args:    describeArgs{Name: "demo/datastore/db", Release: "release-gone.yaml"},
			refusal: []string{"does not exist"}},
		{name: "a registry reference with a Release is refused",
			args:    describeArgs{Name: "oci://registry.example.com/component/demo/datastore/db:1.0.0", Release: "release-a.yaml"},
			refusal: []string{"registry reference", "'release' cannot be sent with one"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := tc.args
			args.WorkspaceRoot = root
			res := callTool(t, newTestServer(t), "describe", args)
			if len(tc.refusal) == 0 {
				if res.IsError {
					t.Fatalf("describe was refused: %s", res.Content[0].Text)
				}
				var payload describeEntityPayload
				if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
					t.Fatalf("decode the description: %v", err)
				}
				if payload.File == nil || *payload.File != tc.file || payload.Description != tc.about {
					t.Errorf("resolved %q in %v (%q); want %q (%q)", tc.args.Name, payload.File, payload.Description, tc.file, tc.about)
				}
				return
			}
			if !res.IsError {
				t.Fatalf("describe answered; want a refusal carrying %v", tc.refusal)
			}
			for _, phrase := range tc.refusal {
				if !strings.Contains(res.Content[0].Text, phrase) {
					t.Errorf("refusal %q does not carry %q", res.Content[0].Text, phrase)
				}
			}
			if tc.args.Release == "" && strings.Contains(res.Content[0].Text, "in the terms of") {
				t.Errorf("a name sent with no Release was answered in a Release's terms: %q", res.Content[0].Text)
			}
		})
	}
}

// TestListEntitiesRefusesAReleaseThatNamesNone pins the two refusals of the
// listing's Release argument: a blank value, which names none, and a value
// naming no Release file.
func TestListEntitiesRefusesAReleaseThatNamesNone(t *testing.T) {
	t.Parallel()
	root := releaseVariantWorkspace(t)
	for _, tc := range []struct {
		name    string
		release string
		fault   string
	}{
		{name: "a blank Release", release: "", fault: "a blank value names none"},
		{name: "a Release that is not there", release: "release-gone.yaml", fault: "does not exist"},
		{name: "a directory", release: "catalog-a", fault: "is a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := callTool(t, newTestServer(t), "list-entities", listEntitiesArgs{
				WorkspaceRoot: root,
				Release:       stringPointer(tc.release),
			})
			if !res.IsError {
				t.Fatalf("list-entities answered; want a refusal carrying %q", tc.fault)
			}
			if !strings.Contains(res.Content[0].Text, tc.fault) {
				t.Errorf("refusal %q does not carry %q", res.Content[0].Text, tc.fault)
			}
		})
	}
}

// entityRows renders an entity listing as sorted rows, so two answers compare as
// sets.
func entityRows(entities []serve.CatalogEntity) []string {
	rows := make([]string, 0, len(entities))
	for _, e := range entities {
		rows = append(rows, fmt.Sprintf("%s %s [%s] %s", e.Kind, e.Name, e.File, e.Origin))
	}
	sort.Strings(rows)
	return rows
}
