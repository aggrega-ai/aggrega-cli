// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDescribeNamesAFileOfAKindNoCatalogHolds drives the describe tool's by-name
// lookup in a Release's terms over a catalog directory holding a document that
// declares the looked-up name under the misspelled kind `Componnent`.
//
// The refusal names that file, the kind it declares and its short code, and
// carries no account of where local entities are read from, because the file
// already sits where the Release's catalog reads. The second row asks a name the
// misspelled file does not declare: the account stands and the file is not
// mentioned, so the first row cannot pass on a refusal that names nothing.
func TestDescribeNamesAFileOfAKindNoCatalogHolds(t *testing.T) {
	t.Parallel()
	root := releaseVariantWorkspace(t)
	misspelled := filepath.Join(root, "catalog-a", "component", "cache-misspelled.yaml")
	if err := os.WriteFile(misspelled, []byte(`apiVersion: aggrega.ai/v1beta1
kind: Componnent
metadata:
  name: demo/datastore/cache
  version: "1.0.0"
spec:
  schema:
    values: {}
`), 0o644); err != nil {
		t.Fatalf("write the misspelled document: %v", err)
	}
	const account = "local entities for the Release `release-a.yaml`"
	for _, tc := range []struct {
		name  string
		named bool
	}{
		{name: "demo/datastore/cache", named: true},
		{name: "demo/datastore/nothing-declares-this", named: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res := callTool(t, newTestServer(t), "describe", describeArgs{
				WorkspaceRoot: root, Name: tc.name, Release: "release-a.yaml",
			})
			if !res.IsError {
				t.Fatalf("describe answered a name no document of a held kind declares in release-a.yaml's terms")
			}
			text := res.Content[0].Text
			if !strings.Contains(text, "in the terms of the Release release-a.yaml") {
				t.Errorf("the refusal is not the by-name lookup's: %q", text)
			}
			names := strings.Contains(text, "cache-misspelled.yaml") && strings.Contains(text, `kind "Componnent"`) &&
				strings.Contains(text, "E0371") && strings.Contains(text, "a kind a catalog does not hold")
			if names != tc.named {
				t.Errorf("the refusal names the misspelled file = %v, want %v: %q", names, tc.named, text)
			}
			if advises := strings.Contains(text, account); advises == tc.named {
				t.Errorf("the refusal carries the account of where local entities are read from = %v, want %v: %q",
					advises, !tc.named, text)
			}
		})
	}
}
