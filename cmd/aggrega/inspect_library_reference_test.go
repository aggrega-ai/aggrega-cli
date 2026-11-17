// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestLibraryReadByReferenceThroughInspect: `aggrega inspect` of a library named
// with a version reads the version that reference selects from a catalog directory
// holding the library at two versions — not the one a bare reference would — and
// a bare name reads the highest. Each version declares an entry of its own, so the
// output says which version's document was read.
func TestLibraryReadByReferenceThroughInspect(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	catalogDir := filepath.Join(root, "catalog")
	doc := func(version, entry string) string {
		return fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
kind: MappingLibrary
metadata:
  name: Lib
  version: %q
spec:
  mappings:
    %s: 'level: 1'
`, version, entry)
	}
	writeFixture(t, filepath.Join(catalogDir, "lib-1.0.0.yaml"), doc("1.0.0", "OlderEntry"))
	writeFixture(t, filepath.Join(catalogDir, "lib-1.1.0.yaml"), doc("1.1.0", "NewerEntry"))

	for _, row := range []struct {
		reference      string
		header         string
		entry, notThis string
	}{
		{reference: "Lib:1.0.0", header: "MappingLibrary: Lib:1.0.0", entry: "OlderEntry", notThis: "NewerEntry"},
		{reference: "Lib", header: "MappingLibrary: Lib:1.1.0", entry: "NewerEntry", notThis: "OlderEntry"},
	} {
		t.Run(row.reference, func(t *testing.T) {
			var buf bytes.Buffer
			ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
			if err := dispatchInspect(context.Background(), ws, row.reference, []string{catalogDir}, testInspectOptions(&buf)); err != nil {
				t.Fatalf("inspect %s: %v", row.reference, err)
			}
			out := buf.String()
			if !strings.Contains(out, row.header) || !strings.Contains(out, row.entry) || strings.Contains(out, row.notThis) {
				t.Errorf("inspect %s: want the output titled %q listing %s and not %s; got:\n%s",
					row.reference, row.header, row.entry, row.notThis, out)
			}
		})
	}
}
